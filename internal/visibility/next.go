package visibility

import (
	"fmt"
	"math"

	"github.com/vincentsch/llmention/internal/config"
)

// Highlights picks out a few things in the numbers worth a closer look, most
// useful first. Each one says what was observed in these answers. None of
// them claims to know why an assistant answered the way it did.
func Highlights(s Summary) []string {
	var out []string
	byEngine := map[string][]Cell{}
	var engines []string
	for _, c := range s.Cells {
		if _, ok := byEngine[c.Engine]; !ok {
			engines = append(engines, c.Engine)
		}
		byEngine[c.Engine] = append(byEngine[c.Engine], c)
	}

	for _, engine := range engines {
		eligible, mentioned := 0, 0
		competitorHits := map[string]int{}
		for _, c := range byEngine[engine] {
			eligible += c.You().Eligible
			mentioned += c.You().Mentioned
			for _, b := range c.Brands[1:] {
				competitorHits[b.Name] += b.Mentioned
			}
		}
		if eligible > 0 && mentioned == 0 {
			line := fmt.Sprintf("%s never mentioned %s in %s.", engine, s.Brand, plural(eligible, "answer"))
			if eligible == 1 {
				line = fmt.Sprintf("%s did not mention %s in its only answer.", engine, s.Brand)
			}
			if name, n := top(competitorHits); n == 1 {
				line += fmt.Sprintf(" It named %s once.", name)
			} else if n > 1 {
				line += fmt.Sprintf(" It named %s %d times.", name, n)
			}
			out = append(out, line)
		}
	}

	for _, engine := range engines {
		var memory, search *BrandStats
		for _, c := range byEngine[engine] {
			you := c.You()
			switch c.Mode {
			case "memory":
				memory = &you
			case "search_auto", "search_required":
				search = &you
			}
		}
		if memory == nil || search == nil {
			continue
		}
		m, okM := memory.MentionRate()
		w, okW := search.MentionRate()
		switch {
		case okM && okW && w-m >= 0.25:
			out = append(out, fmt.Sprintf("%s mentioned %s more often with web search on (%s) than off (%s).", engine, s.Brand, pct(w), pct(m)))
		case okM && okW && m-w >= 0.25:
			out = append(out, fmt.Sprintf("%s mentioned %s less often with web search on (%s) than off (%s).", engine, s.Brand, pct(w), pct(m)))
		}
	}

	// A prompt counts when no engine named you in any mode and at least one
	// named a competitor.
	missing, asked := 0, 0
	example := ""
	for _, p := range s.Prompts {
		if p.NamesYou {
			continue
		}
		answered, you, competitor := false, false, false
		for _, c := range p.Cells {
			answered = answered || c.Answers > 0
			you = you || c.Mentioned > 0
			competitor = competitor || len(c.Competitors) > 0
		}
		if !answered {
			continue
		}
		asked++
		if !you && competitor {
			missing++
			if example == "" {
				example = p.Prompt
			}
		}
	}
	if missing > 0 {
		out = append(out, fmt.Sprintf("On %d of %d prompts, no assistant named %s while at least one named a competitor. For example: %q", missing, asked, s.Brand, example))
	}

	cited, youCited := 0, 0
	var competitorSites, otherSites []string
	for _, d := range s.Domains {
		cited += d.Citations
		switch d.Owner {
		case "you":
			youCited += d.Citations
		case "":
			if len(otherSites) < 2 {
				otherSites = append(otherSites, fmt.Sprintf("%s (%d)", d.Domain, d.Citations))
			}
		default:
			if len(competitorSites) < 2 {
				competitorSites = append(competitorSites, fmt.Sprintf("%s (%d)", d.Domain, d.Citations))
			}
		}
	}
	if cited > 0 && youCited == 0 && len(competitorSites) > 0 {
		out = append(out, fmt.Sprintf("Web search answers cited your competitors' sites, %s, and never yours.", config.JoinAnd(competitorSites)))
	}
	if len(otherSites) > 0 {
		out = append(out, fmt.Sprintf("The most cited sites outside the brands you track were %s.", config.JoinAnd(otherSites)))
	}

	var unknown []string
	for _, u := range s.Unknown {
		if u.Answers < 2 || len(unknown) == 2 {
			continue
		}
		unknown = append(unknown, fmt.Sprintf("%s (%s)", u.Name, plural(u.Answers, "answer")))
	}
	if len(unknown) > 0 {
		out = append(out, fmt.Sprintf("Names you don't track came up in several answers: %s. If they compete with you, add them to competitors in llmention.yaml.", config.JoinAnd(unknown)))
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func top(counts map[string]int) (string, int) {
	best, n := "", 0
	for name, c := range counts {
		if c > n || (c == n && name < best) {
			best, n = name, c
		}
	}
	return best, n
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", math.Round(v*100)) }
