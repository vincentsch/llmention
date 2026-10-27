package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
)

// WriteMarkdown prints the summary as Markdown, for example for a GitHub job
// summary or a pull request comment.
func WriteMarkdown(w io.Writer, r Report) {
	title := r.Run.Brand
	if title == "" {
		title = strings.Join(r.Run.Suites, ", ")
	}
	fmt.Fprintf(w, "## %s in AI answers\n\n", md(title))
	var engines []string
	for _, e := range r.Run.Engines {
		engines = append(engines, fmt.Sprintf("%s (`%s`)", md(e.ID), e.Model))
	}
	fmt.Fprintf(w, "Run `%s` · %s · %s · %s\n\n", r.Run.ID, localTime(r.Run.CreatedAt), strings.Join(engines, ", "), plural(r.Run.Counts.Answered, "answer"))
	for _, n := range r.Notes {
		fmt.Fprintf(w, "> %s\n\n", md(n))
	}
	if v := r.Visibility; v != nil && len(v.Cells) > 0 {
		fmt.Fprintf(w, "| Engine | Mode | Mentioned | Rate | Share of voice | Avg. rank |\n|---|---|---|---|---|---|\n")
		for _, c := range v.Cells {
			if status := noAnswers(c); status != "" {
				fmt.Fprintf(w, "| %s | %s | %s | – | – | – |\n", md(c.Engine), config.ModeLabel(c.Mode), status)
				continue
			}
			you := c.You()
			fmt.Fprintf(w, "| %s | %s | %s | **%s** | %s | %s |\n", md(c.Engine), config.ModeLabel(c.Mode), fraction(you.Mentioned, you.Eligible), rate(you), ptrPercent(you.ShareOfVoice), avgRank(you))
		}
		if len(v.Competitors) > 0 {
			fmt.Fprintf(w, "\n**Mention rate next to competitors**\n\n| Engine · mode |")
			for i, b := range v.Cells[0].Brands {
				name := md(b.Name)
				if i == 0 {
					name += " (you)"
				}
				fmt.Fprintf(w, " %s |", name)
			}
			fmt.Fprintf(w, "\n|---|%s\n", strings.Repeat("---|", len(v.Cells[0].Brands)))
			for _, c := range v.Cells {
				fmt.Fprintf(w, "| %s · %s |", md(c.Engine), config.ModeLabel(c.Mode))
				status := noAnswers(c)
				for i, b := range c.Brands {
					switch {
					case status == "":
						fmt.Fprintf(w, " %s |", rate(b))
					case i == 0:
						fmt.Fprintf(w, " %s |", status)
					default:
						fmt.Fprint(w, " – |")
					}
				}
				fmt.Fprintln(w)
			}
		}
		if len(v.Domains) > 0 {
			fmt.Fprintf(w, "\n**Sites the answers cited**\n\n| Site | Citations | Whose |\n|---|---|---|\n")
			for i, d := range v.Domains {
				if i == 8 {
					break
				}
				fmt.Fprintf(w, "| %s | %d | %s |\n", md(d.Domain), d.Citations, md(owner(d.Owner)))
			}
		}
		if len(v.Highlights) > 0 {
			fmt.Fprintf(w, "\n**What stands out**\n\n")
			for _, s := range v.Highlights {
				fmt.Fprintf(w, "- %s\n", md(s))
			}
		}
	}
	for _, rec := range r.Recognition {
		writeRecognitionMarkdown(w, rec)
	}
	for _, s := range r.Suites {
		fmt.Fprintf(w, "\n**Suite %s**\n\n| Metric | %s |\n|---|%s\n", md(s.Suite), strings.Join(s.Columns, " | "), strings.Repeat("---|", len(s.Columns)))
		for _, row := range s.Rows {
			fmt.Fprintf(w, "| %s |", row.Metric)
			for _, v := range row.Values {
				fmt.Fprintf(w, " %s |", metricText(row.Metric, v))
			}
			fmt.Fprintln(w)
		}
	}
	if len(r.Gates) > 0 {
		fmt.Fprintf(w, "\n**Checks**\n\n")
		for _, g := range r.Gates {
			mark := "✅"
			switch g.Status {
			case "fail", "error":
				mark = "❌"
			case "inconclusive":
				mark = "⚠️"
			}
			fmt.Fprintf(w, "- %s `%s`: %s\n", mark, g.Selector, md(g.Message))
		}
	}
}

// WriteTrendMarkdown prints the trend as a Markdown table, for a GitHub job
// summary.
func WriteTrendMarkdown(w io.Writer, t Trend) {
	if len(t.Points) == 0 {
		fmt.Fprintln(w, "No saved runs with prompts yet.")
		return
	}
	fmt.Fprintf(w, "## Mention rate of %s over %s\n\n| Run |", md(t.Brand), plural(len(t.Points), "run"))
	for _, c := range t.Cells {
		fmt.Fprintf(w, " %s · %s |", md(c.Engine), config.ModeLabel(c.Mode))
	}
	fmt.Fprintf(w, "\n|---|%s\n", strings.Repeat("---|", len(t.Cells)))
	for _, p := range t.Points {
		label := runLabel(p.CreatedAt)
		if p.Changed {
			label += " \\*"
		}
		fmt.Fprintf(w, "| %s |", label)
		for _, v := range p.MentionRate {
			fmt.Fprintf(w, " %s |", ptrPercent(v))
		}
		fmt.Fprintln(w)
	}
	if len(t.Points) > 1 {
		fmt.Fprint(w, "| Trend |")
		for i := range t.Cells {
			var col []*float64
			for _, p := range t.Points {
				col = append(col, p.MentionRate[i])
			}
			fmt.Fprintf(w, " %s |", Spark(col))
		}
		fmt.Fprintln(w)
	}
	for _, line := range trendFootnotes(t) {
		fmt.Fprintf(w, "\n%s\n", md(line))
	}
}

// WriteDiffMarkdown prints a comparison of two runs as Markdown.
func WriteDiffMarkdown(w io.Writer, d Diff) {
	fmt.Fprintf(w, "## %s: run `%s` compared with run `%s`\n\n", md(d.Brand), d.New.ID, d.Old.ID)
	fmt.Fprintf(w, "Compared on the %s both runs asked, scored with the same brand names.\n\n", plural(d.SharedPrompts, "prompt"))
	for _, n := range diffNotes(d) {
		fmt.Fprintf(w, "> %s\n\n", md(n))
	}
	if len(d.Rows) == 0 {
		fmt.Fprintln(w, "The runs share no engine, mode and prompt with an answer in both, so there is nothing to compare.")
		return
	}
	fmt.Fprintf(w, "| Engine | Mode | Before | Now | Change | Share of voice now |\n|---|---|---|---|---|---|\n")
	for _, r := range d.Rows {
		fmt.Fprintf(w, "| %s | %s | %s | **%s** | %s | %s |\n", md(r.Engine), config.ModeLabel(r.Mode), ptrPercent(r.OldRate), ptrPercent(r.NewRate), change(r.OldRate, r.NewRate), ptrPercent(r.NewShareOfVoice))
	}
	if len(d.Prompts) > 0 {
		fmt.Fprintf(w, "\n**Answers that changed**\n\n")
		for i, p := range d.Prompts {
			if i == 12 {
				fmt.Fprintf(w, "- …and %d more\n", len(d.Prompts)-12)
				break
			}
			fmt.Fprintf(w, "- %s · %s · %q\n  - before: %s\n  - now: %s\n", md(p.Engine), config.ModeLabel(p.Mode), md(shorten(p.Prompt, 70)), md(p.Before), md(p.After))
		}
	}
}

// md escapes characters that would break a table cell or start formatting.
func md(s string) string {
	return strings.NewReplacer("|", `\|`, "*", `\*`, "_", `\_`, "`", "'", "\n", " ").Replace(s)
}
