package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Trend is your mention rate and share of voice over several runs, one
// column per engine and mode.
type Trend struct {
	Brand  string       `json:"brand"`
	Cells  []TrendCell  `json:"cells"`
	Points []TrendPoint `json:"points"` // oldest first
	// LeftOut counts runs made with --engine or --mode. The trend leaves them
	// out, so the engines they did not ask do not show up as gaps.
	LeftOut int `json:"left_out_runs,omitempty"`
}

// TrendCell names one column.
type TrendCell struct {
	Engine string `json:"engine"`
	Mode   string `json:"mode"`
}

// TrendPoint is one run.
type TrendPoint struct {
	RunID        string     `json:"run_id"`
	CreatedAt    time.Time  `json:"created_at"`
	Demo         bool       `json:"demo,omitempty"`
	MentionRate  []*float64 `json:"mention_rate"`
	ShareOfVoice []*float64 `json:"share_of_voice"`
	// Changed is true when the prompts or a model differ from the previous
	// run, so a jump may come from the change rather than from the market.
	Changed bool     `json:"changed"`
	Changes []string `json:"changes,omitempty"`
}

// TrendRun is one scored run for BuildTrend.
type TrendRun struct {
	Card    *scorecard.Scorecard
	Summary *visibility.Summary
}

// BuildTrend lines runs up oldest first.
func BuildTrend(brand string, runs []TrendRun) Trend {
	t := Trend{Brand: brand}
	for _, r := range runs {
		for _, c := range r.Summary.Cells {
			cell := TrendCell{c.Engine, c.Mode}
			if !slices.Contains(t.Cells, cell) {
				t.Cells = append(t.Cells, cell)
			}
		}
	}
	var prev *TrendRun
	for i := range runs {
		r := &runs[i]
		p := TrendPoint{RunID: r.Card.ID, CreatedAt: r.Card.CreatedAt, Demo: r.Card.Demo()}
		for _, cell := range t.Cells {
			c := findCell(r.Summary.Cells, cell.Engine, cell.Mode)
			if c == nil {
				p.MentionRate = append(p.MentionRate, nil)
				p.ShareOfVoice = append(p.ShareOfVoice, nil)
				continue
			}
			p.MentionRate = append(p.MentionRate, optional(c.You().MentionRate()))
			p.ShareOfVoice = append(p.ShareOfVoice, c.You().ShareOfVoice)
		}
		if prev != nil {
			p.Changes = runChanges(prev, r)
			p.Changed = len(p.Changes) > 0
		}
		t.Points = append(t.Points, p)
		prev = r
	}
	return t
}

func runChanges(prev, cur *TrendRun) []string {
	var out []string
	for _, e := range cur.Card.Engines {
		if old, ok := prev.Card.Engine(e.ID); ok && old.Model != e.Model {
			out = append(out, fmt.Sprintf("%s model %s → %s", e.ID, old.Model, e.Model))
		}
	}
	before, now := promptTextsOf(prev.Summary), promptTextsOf(cur.Summary)
	added, removed, reworded := 0, 0, 0
	for id, text := range now {
		old, ok := before[id]
		switch {
		case !ok:
			added++
		case old != text:
			reworded++
		}
	}
	for id := range before {
		if _, ok := now[id]; !ok {
			removed++
		}
	}
	if added > 0 || removed > 0 {
		out = append(out, fmt.Sprintf("prompts changed (%d added, %d removed)", added, removed))
	}
	if reworded > 0 {
		out = append(out, fmt.Sprintf("%s reworded under the same id", plural(reworded, "prompt")))
	}
	return out
}

func promptTextsOf(s *visibility.Summary) map[string]string {
	out := map[string]string{}
	for _, p := range s.Prompts {
		out[p.ID] = p.Prompt
	}
	return out
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// Spark draws values between 0 and 1 as a small bar chart, with a gap for
// runs that had no value.
func Spark(values []*float64) string {
	var b strings.Builder
	for _, v := range values {
		if v == nil {
			b.WriteRune(' ')
			continue
		}
		i := int(*v*float64(len(sparks)-1) + 0.5)
		b.WriteRune(sparks[max(0, min(len(sparks)-1, i))])
	}
	return b.String()
}

// WriteTrendText prints the trend table.
func WriteTrendText(w io.Writer, t Trend, style Style) {
	if len(t.Points) == 0 {
		fmt.Fprintln(w, "No saved runs with prompts yet. Run `llmention run` a few times, for example once a week.")
		return
	}
	fmt.Fprintf(w, "%s\n", style.heading(fmt.Sprintf("Mention rate of %s over %s", t.Brand, plural(len(t.Points), "run"))))
	tw := table(w)
	header := []string{"  Run"}
	for _, c := range t.Cells {
		header = append(header, c.Engine+" · "+config.ModeLabel(c.Mode))
	}
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, p := range t.Points {
		label := "  " + runLabel(p.CreatedAt)
		if p.Changed {
			label += " *"
		}
		row := []string{label}
		for _, v := range p.MentionRate {
			row = append(row, ptrPercent(v))
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	if len(t.Points) > 1 {
		row := []string{"  Trend"}
		for i := range t.Cells {
			var col []*float64
			for _, p := range t.Points {
				col = append(col, p.MentionRate[i])
			}
			row = append(row, Spark(col))
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
	for _, line := range trendFootnotes(t) {
		fmt.Fprintln(w)
		fmt.Fprintln(w, line)
	}
}

// trendFootnotes explain the marks in the trend table.
func trendFootnotes(t Trend) []string {
	var out []string
	var changes []string
	for _, p := range t.Points {
		if p.Changed {
			changes = append(changes, "  "+runLabel(p.CreatedAt)+": "+strings.Join(p.Changes, "; "))
		}
	}
	if len(changes) > 0 {
		out = append(out, "* Prompts or models changed since the run before:\n"+strings.Join(changes, "\n"))
	}
	if t.LeftOut > 0 {
		out = append(out, fmt.Sprintf("Left out %s made with --engine or --mode, which asked only some engines or modes.", plural(t.LeftOut, "run")))
	}
	if slices.ContainsFunc(t.Points, func(p TrendPoint) bool { return p.Demo }) {
		out = append(out, "Demo data: the mock engine made up these answers.")
	}
	return out
}

// runLabel names a run by its local date and time.
func runLabel(t time.Time) string {
	return t.Local().Format("2 Jan 2006 15:04")
}
