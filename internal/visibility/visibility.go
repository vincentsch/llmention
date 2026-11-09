// Package visibility turns stored answers into the numbers llmention reports.
// It counts how often each assistant mentions you and your competitors and
// where you rank, and it lists the websites the answers cite.
//
// Three rules decide which answers count. A mention only counts when the prompt
// did not already name that brand, since assistants repeat names from the
// question even when they know nothing about them. Share of voice only uses
// answers to prompts that name none of the tracked brands, so every brand's
// share comes from the same answers. Answers that were cut off before they
// finished are saved so you can read them, but they are left out of every
// number.
package visibility

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/mention"
)

// Brands are the names to look for.
type Brands struct {
	You         Brand   `json:"you"`
	Competitors []Brand `json:"competitors"`
}

// Brand is one tracked name. Aliases include product names, and domains also
// count as names ("tabslip.com" is a mention of Tabslip).
type Brand struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Domains []string `json:"domains,omitempty"`
	// MatchCase counts the name and aliases only with the same capitals.
	MatchCase bool `json:"match_case,omitempty"`
}

// Same reports whether two brand definitions score answers the same way. An
// empty list and a missing list are the same.
func (b Brands) Same(other Brands) bool {
	x, err1 := json.Marshal(b)
	y, err2 := json.Marshal(other)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// Answer is one stored answer to score.
type Answer struct {
	PromptID   string
	Prompt     string
	Engine     string
	Mode       string
	Repetition int
	Text       string
	Citations  []Citation
	// Failed means there is no answer. Stopped narrows that down: the run
	// stopped before the request got an answer, because of Ctrl+C or the
	// request budget. Unfinished means the provider cut the answer off, for
	// example at its token limit; its text is saved for reading but not
	// counted.
	Failed     bool
	Stopped    bool
	Unfinished bool
	// CitationsReported is true for an answer in a web search mode from an
	// engine that reports the pages it cites, so an empty list means it
	// cited nothing.
	CitationsReported bool
}

// Counted reports whether an answer takes part in the numbers.
func (a Answer) Counted() bool { return !a.Failed && !a.Unfinished }

// Key identifies an answer across runs: the same prompt, engine, mode and
// repetition.
func (a Answer) Key() string {
	return strings.Join([]string{a.PromptID, a.Engine, a.Mode, strconv.Itoa(a.Repetition)}, "\x00")
}

// Citation is a page an answer cited.
type Citation struct {
	URL    string `json:"url"`
	Domain string `json:"domain"`
	Title  string `json:"title,omitempty"`
}

// Summary is everything the report shows about one run.
type Summary struct {
	Brand       string   `json:"brand"`
	Competitors []string `json:"competitors"`
	// Answers counts the answers that take part in the numbers.
	Answers    int            `json:"answers"`
	Failed     int            `json:"failed"`
	Stopped    int            `json:"stopped"`
	Unfinished int            `json:"unfinished"`
	Cells      []Cell         `json:"cells"`
	Prompts    []PromptRow    `json:"prompts"`
	Domains    []DomainCount  `json:"cited_domains"`
	Unknown    []UnknownBrand `json:"other_names"`
	Excerpts   []Excerpt      `json:"excerpts"`
	// PromptsNamingYou lists prompt ids left out of your numbers because they
	// already contain your name.
	PromptsNamingYou []string `json:"prompts_naming_you"`
	Highlights       []string `json:"highlights"`
}

// Cell holds the numbers for one engine in one mode. A cell whose requests
// all failed or were stopped stays in the summary with no answers, so
// reports can say what happened to it.
type Cell struct {
	Engine     string       `json:"engine"`
	Mode       string       `json:"mode"`
	Answers    int          `json:"answers"`
	Failed     int          `json:"failed"`
	Stopped    int          `json:"stopped"`
	Unfinished int          `json:"unfinished"`
	Brands     []BrandStats `json:"brands"` // you first, then competitors in config order
	// ShareAnswers counts the answers to prompts that name no tracked
	// brand, the only ones share of voice is worked out from.
	ShareAnswers int `json:"share_of_voice_answers"`
	// CitationAnswers counts the answers whose citations llmention can see:
	// web search answers from engines that report the pages they cite.
	CitationAnswers int           `json:"answers_with_citations_reported"`
	YourSiteCited   int           `json:"answers_citing_you"`
	CitedAny        int           `json:"answers_citing_any_site"`
	Domains         []DomainCount `json:"cited_domains"`
}

// You returns your own numbers.
func (c Cell) You() BrandStats { return c.Brands[0] }

// CitationRate is the share of answers that cite your site, out of the
// answers whose citations can be seen. ok is false when there are none.
func (c Cell) CitationRate() (rate float64, ok bool) {
	if c.CitationAnswers == 0 {
		return 0, false
	}
	return float64(c.YourSiteCited) / float64(c.CitationAnswers), true
}

// BrandStats are one brand's numbers in one cell.
type BrandStats struct {
	Name string `json:"name"`
	You  bool   `json:"you,omitempty"`
	// Eligible counts answers to prompts that do not name this brand.
	Eligible int `json:"eligible_answers"`
	// Mentioned counts eligible answers that mention the brand.
	Mentioned int `json:"mentioned"`
	// ShareMentions counts mentions in answers to prompts that name no
	// tracked brand.
	ShareMentions int `json:"share_of_voice_mentions"`
	RankSum       int `json:"-"`
	Ranked        int `json:"ranked_answers"`

	// The numbers below are worked out from the counts; nil means there is
	// nothing to divide by.
	Rate         *float64 `json:"mention_rate"`
	Rank         *float64 `json:"avg_rank"`
	ShareOfVoice *float64 `json:"share_of_voice"`
}

// MentionRate is Mentioned / Eligible; ok is false when nothing was eligible.
func (b BrandStats) MentionRate() (rate float64, ok bool) {
	if b.Eligible == 0 {
		return 0, false
	}
	return float64(b.Mentioned) / float64(b.Eligible), true
}

// AvgRank is the average list position when the brand was ranked.
func (b BrandStats) AvgRank() (rank float64, ok bool) {
	if b.Ranked == 0 {
		return 0, false
	}
	return float64(b.RankSum) / float64(b.Ranked), true
}

// SOV is the share of voice; ok is false when no tracked brand was mentioned
// in the answers it is worked out from, so there is no share to give.
func (b BrandStats) SOV() (share float64, ok bool) {
	if b.ShareOfVoice == nil {
		return 0, false
	}
	return *b.ShareOfVoice, true
}

// finish fills the worked-out numbers. total is the share-of-voice mentions
// of all brands together.
func (b *BrandStats) finish(total int) {
	b.Rate = optional(b.MentionRate())
	b.Rank = optional(b.AvgRank())
	b.ShareOfVoice = nil
	if total > 0 {
		v := float64(b.ShareMentions) / float64(total)
		b.ShareOfVoice = &v
	}
}

func optional(v float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &v
}

func finishBrands(brands []BrandStats) {
	total := 0
	for _, b := range brands {
		total += b.ShareMentions
	}
	for i := range brands {
		brands[i].finish(total)
	}
}

// DomainCount is how often a website was cited.
type DomainCount struct {
	Domain    string `json:"domain"`
	Owner     string `json:"owner,omitempty"` // "you", a competitor's name, or empty
	Citations int    `json:"citations"`
	Answers   int    `json:"answers"`
}

// PromptRow shows one prompt across all cells.
type PromptRow struct {
	ID       string       `json:"id"`
	Prompt   string       `json:"prompt"`
	NamesYou bool         `json:"names_you,omitempty"`
	Cells    []PromptCell `json:"cells"` // same order as Summary.Cells
}

// PromptCell is what one engine in one mode said to one prompt, summed over
// repetitions.
type PromptCell struct {
	Answers     int      `json:"answers"`
	Mentioned   int      `json:"mentioned"`
	BestRank    int      `json:"best_rank,omitempty"`
	Competitors []string `json:"competitors,omitempty"`
}

// UnknownBrand is a name the assistants used that nobody tracks. It is a
// candidate: often a product, sometimes a label the filter missed.
type UnknownBrand struct {
	Name    string   `json:"name"`
	Answers int      `json:"answers"`
	Engines []string `json:"engines"`
}

// Excerpt is a short quote from an answer.
type Excerpt struct {
	Engine   string `json:"engine"`
	Mode     string `json:"mode"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
	Text     string `json:"text"`
	// MentionsYou is false for excerpts that show a competitor in a place
	// where you were missing.
	MentionsYou bool `json:"mentions_you"`
}

const excerptLength = 220

// Summarize scores answers. Cells and prompts appear in the order in which
// their first answer arrives, so callers set the order by sorting the answers.
func Summarize(brands Brands, answers []Answer) Summary {
	names := mentionBrands(brands)
	s := Summary{Brand: brands.You.Name}
	for _, c := range brands.Competitors {
		s.Competitors = append(s.Competitors, c.Name)
	}
	cellIndex := map[[2]string]int{}
	promptIndex := map[string]int{}
	type unknownAcc struct {
		name    string
		answers int
		engines []string
	}
	unknown := map[string]*unknownAcc{}
	var unknownOrder []string
	domainTotals := map[string]*DomainCount{}
	cellDomains := map[int]map[string]*DomainCount{}
	var youExcerpts, missExcerpts []Excerpt

	for _, a := range answers {
		key := [2]string{a.Engine, a.Mode}
		ci, ok := cellIndex[key]
		if !ok {
			ci = len(s.Cells)
			cellIndex[key] = ci
			cell := Cell{Engine: a.Engine, Mode: a.Mode}
			for i, b := range names {
				cell.Brands = append(cell.Brands, BrandStats{Name: b.Name, You: i == 0})
			}
			s.Cells = append(s.Cells, cell)
			cellDomains[ci] = map[string]*DomainCount{}
			for i := range s.Prompts {
				s.Prompts[i].Cells = append(s.Prompts[i].Cells, PromptCell{})
			}
		}
		named := mention.NamedIn(a.Prompt, names)
		pi, ok := promptIndex[a.PromptID]
		if !ok {
			pi = len(s.Prompts)
			promptIndex[a.PromptID] = pi
			row := PromptRow{ID: a.PromptID, Prompt: a.Prompt, NamesYou: named[names[0].ID]}
			row.Cells = make([]PromptCell, len(s.Cells))
			s.Prompts = append(s.Prompts, row)
			if row.NamesYou {
				s.PromptsNamingYou = append(s.PromptsNamingYou, a.PromptID)
			}
		}
		cell := &s.Cells[ci]
		switch {
		case a.Stopped:
			cell.Stopped++
			s.Stopped++
			continue
		case a.Failed:
			cell.Failed++
			s.Failed++
			continue
		case a.Unfinished:
			cell.Unfinished++
			s.Unfinished++
			continue
		}
		cell.Answers++
		s.Answers++
		pc := &s.Prompts[pi].Cells[ci]
		pc.Answers++

		analysis := mention.Analyze(a.Prompt, a.Text, names)
		neutral := len(named) == 0
		if neutral {
			cell.ShareAnswers++
		}
		for i, r := range analysis.Brands {
			stats := &cell.Brands[i]
			if r.NamedInPrompt {
				continue
			}
			stats.Eligible++
			if !r.Counted() {
				continue
			}
			stats.Mentioned++
			if neutral {
				stats.ShareMentions++
			}
			if r.Rank > 0 {
				stats.RankSum += r.Rank
				stats.Ranked++
			}
			if i == 0 {
				pc.Mentioned++
				if r.Rank > 0 && (pc.BestRank == 0 || r.Rank < pc.BestRank) {
					pc.BestRank = r.Rank
				}
			} else if !slices.Contains(pc.Competitors, names[i].Name) {
				pc.Competitors = append(pc.Competitors, names[i].Name)
			}
		}

		you := analysis.Brands[0]
		switch {
		case you.Counted():
			youExcerpts = append(youExcerpts, Excerpt{Engine: a.Engine, Mode: a.Mode, PromptID: a.PromptID, Prompt: a.Prompt, Text: mention.Excerpt(a.Text, you.Match, excerptLength), MentionsYou: true})
		case !you.NamedInPrompt:
			for _, r := range analysis.Brands[1:] {
				if r.Counted() {
					missExcerpts = append(missExcerpts, Excerpt{Engine: a.Engine, Mode: a.Mode, PromptID: a.PromptID, Prompt: a.Prompt, Text: mention.Excerpt(a.Text, r.Match, excerptLength)})
					break
				}
			}
		}

		for _, name := range analysis.Unknown(a.Text, names) {
			k := mention.Normalize(name)
			acc := unknown[k]
			if acc == nil {
				acc = &unknownAcc{name: name}
				unknown[k] = acc
				unknownOrder = append(unknownOrder, k)
			}
			acc.answers++
			if !slices.Contains(acc.engines, a.Engine) {
				acc.engines = append(acc.engines, a.Engine)
			}
		}

		if a.CitationsReported {
			cell.CitationAnswers++
		}
		citedYou := false
		seen := map[string]bool{}
		for _, c := range a.Citations {
			d := displayDomain(c.Domain, c.URL)
			if d == "" {
				continue
			}
			owner := owner(d, brands)
			if owner == "you" {
				citedYou = true
			}
			for _, counts := range []map[string]*DomainCount{domainTotals, cellDomains[ci]} {
				dc := counts[d]
				if dc == nil {
					dc = &DomainCount{Domain: d, Owner: owner}
					counts[d] = dc
				}
				dc.Citations++
				if !seen[d] {
					dc.Answers++
				}
			}
			seen[d] = true
		}
		if len(seen) > 0 {
			cell.CitedAny++
		}
		if citedYou {
			cell.YourSiteCited++
		}
	}

	for ci := range s.Cells {
		cell := &s.Cells[ci]
		finishBrands(cell.Brands)
		cell.Domains = sortedDomains(cellDomains[ci])
	}
	s.Domains = sortedDomains(domainTotals)
	for _, k := range unknownOrder {
		acc := unknown[k]
		s.Unknown = append(s.Unknown, UnknownBrand{Name: acc.name, Answers: acc.answers, Engines: acc.engines})
	}
	slices.SortStableFunc(s.Unknown, func(a, b UnknownBrand) int { return cmp.Compare(b.Answers, a.Answers) })
	s.Excerpts = pickExcerpts(youExcerpts, missExcerpts)
	s.Highlights = Highlights(s)
	return s
}

// Totals adds up every cell, for a one-line headline.
func (s Summary) Totals() []BrandStats {
	if len(s.Cells) == 0 {
		return nil
	}
	out := make([]BrandStats, len(s.Cells[0].Brands))
	for _, cell := range s.Cells {
		for i, b := range cell.Brands {
			out[i].Name, out[i].You = b.Name, b.You
			out[i].Eligible += b.Eligible
			out[i].Mentioned += b.Mentioned
			out[i].ShareMentions += b.ShareMentions
			out[i].RankSum += b.RankSum
			out[i].Ranked += b.Ranked
		}
	}
	finishBrands(out)
	return out
}

// mentionBrands lists you first. Names are unique in a valid project, so
// they double as ids.
func mentionBrands(b Brands) []mention.Brand {
	out := []mention.Brand{b.You.mentionBrand()}
	for _, c := range b.Competitors {
		out = append(out, c.mentionBrand())
	}
	return out
}

func (b Brand) mentionBrand() mention.Brand {
	return mention.Brand{ID: b.Name, Name: b.Name, Aliases: b.Aliases, Domains: b.Domains, MatchCase: b.MatchCase}
}

func displayDomain(d, rawURL string) string {
	if d == "" {
		d = hostOf(rawURL)
	}
	if normalized, err := domain.NormalizeObservedHost(d); err == nil {
		return normalized
	}
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "www.")
}

func hostOf(rawURL string) string {
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func owner(d string, b Brands) string {
	for _, policy := range b.You.Domains {
		if ok, _ := domain.MatchesScoringPolicy(d, policy); ok {
			return "you"
		}
	}
	for _, c := range b.Competitors {
		for _, policy := range c.Domains {
			if ok, _ := domain.MatchesScoringPolicy(d, policy); ok {
				return c.Name
			}
		}
	}
	return ""
}

func sortedDomains(m map[string]*DomainCount) []DomainCount {
	out := make([]DomainCount, 0, len(m))
	for _, dc := range m {
		out = append(out, *dc)
	}
	slices.SortFunc(out, func(a, b DomainCount) int {
		if c := cmp.Compare(b.Citations, a.Citations); c != 0 {
			return c
		}
		return strings.Compare(a.Domain, b.Domain)
	})
	return out
}

// pickExcerpts picks a few quotes: some where you were mentioned and some
// where a competitor was named and you were not, spread across engines.
func pickExcerpts(you, miss []Excerpt) []Excerpt {
	var out []Excerpt
	usedEngine := map[string]int{}
	take := func(list []Excerpt, n int) {
		for pass := 0; pass < 2 && n > 0; pass++ {
			for _, e := range list {
				if n == 0 {
					return
				}
				if (pass == 0 && usedEngine[e.Engine] > 0) || slices.ContainsFunc(out, func(o Excerpt) bool { return o.Text == e.Text }) {
					continue
				}
				out = append(out, e)
				usedEngine[e.Engine]++
				n--
			}
		}
	}
	take(you, 2)
	take(miss, 3-len(out))
	return out
}
