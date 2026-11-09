package report

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Style decides whether headings are bold. Plain output, pipes and --no-ansi
// get no escape codes.
type Style struct{ ANSI bool }

func (s Style) heading(text string) string {
	if s.ANSI {
		return "\x1b[1m" + text + "\x1b[0m"
	}
	return text
}

// WriteText prints the summary a person reads first, then how to see more.
func WriteText(w io.Writer, r Report, style Style) {
	WriteSummary(w, r, style)
	command := "llmention report " + r.Run.ID
	if r.Run.Mock {
		command += " --mock"
	}
	fmt.Fprintf(w, "\nRead every answer with `%s --answers`. For a page you can share, add --html report.html.\n", command)
}

// WriteSummary prints the summary without pointing at saved runs, for
// output such as the demo's that is not saved.
func WriteSummary(w io.Writer, r Report, style Style) {
	writeHeader(w, r)
	if v := r.Visibility; v != nil && len(v.Cells) > 0 {
		writeVisibility(w, r, v, style)
	}
	for _, rec := range r.Recognition {
		writeRecognition(w, rec, style)
	}
	for _, s := range r.Suites {
		writeSuiteMetrics(w, s, style)
	}
	writeGates(w, r, style)
}

func writeHeader(w io.Writer, r Report) {
	title := r.Run.Brand
	if title == "" {
		title = strings.Join(r.Run.Suites, ", ")
	}
	fmt.Fprintf(w, "%s · run %s · %s\n", title, r.Run.ID, localTime(r.Run.CreatedAt))
	var engines []string
	for _, e := range r.Run.Engines {
		engines = append(engines, fmt.Sprintf("%s (%s)", e.ID, e.Model))
	}
	fmt.Fprintf(w, "Engines: %s\n", strings.Join(engines, ", "))
	if len(r.Run.Competitors) > 0 {
		fmt.Fprintf(w, "Competitors: %s\n", strings.Join(r.Run.Competitors, ", "))
	}
	c := r.Run.Counts
	line := fmt.Sprintf("%s, %s", plural(r.Run.Prompts, "prompt"), plural(c.Answered, "answer"))
	if c.Unfinished > 0 {
		line += fmt.Sprintf(", %d unfinished", c.Unfinished)
	}
	if c.Failed > 0 {
		line += fmt.Sprintf(", %d failed", c.Failed)
	}
	if c.Cancelled > 0 {
		line += fmt.Sprintf(", %d stopped before the answer came back", c.Cancelled)
	}
	if c.NotSent > 0 {
		line += fmt.Sprintf(", %d not sent", c.NotSent)
	}
	fmt.Fprintln(w, line)
	for _, n := range r.Notes {
		fmt.Fprintln(w, n)
	}
}

func writeVisibility(w io.Writer, r Report, v *visibility.Summary, style Style) {
	fmt.Fprintf(w, "\n%s\n", style.heading("How often assistants mention "+v.Brand))
	tw := table(w)
	fmt.Fprintln(tw, "  Engine\tMode\tMentioned\tRate\tShare of voice\tAvg. rank")
	for _, c := range v.Cells {
		if status := noAnswers(c); status != "" {
			// Dashes in every column keep the rows below aligned; the reason
			// goes after the last column.
			fmt.Fprintf(tw, "  %s\t%s\t–\t–\t–\t–\t%s\n", c.Engine, config.ModeLabel(c.Mode), status)
			continue
		}
		you := c.You()
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", c.Engine, config.ModeLabel(c.Mode),
			fraction(you.Mentioned, you.Eligible), rate(you), ptrPercent(you.ShareOfVoice), avgRank(you))
	}
	tw.Flush()
	fmt.Fprintln(w, "  Rate is the share of counted answers that name "+v.Brand+". Share of voice is its part of all mentions of the brands you track.")
	fmt.Fprintln(w, "  Avg. rank is its average place in the answers that name it, where 1 means first. A dash means there was nothing to count.")

	if len(v.Competitors) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("Mention rate next to your competitors"))
		tw = table(w)
		header := []string{"  Engine · mode"}
		for i, b := range v.Cells[0].Brands {
			name := b.Name
			if i == 0 {
				name += " (you)"
			}
			header = append(header, name)
		}
		fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, c := range v.Cells {
			row := []string{"  " + c.Engine + " · " + config.ModeLabel(c.Mode)}
			if status := noAnswers(c); status != "" {
				for range c.Brands {
					row = append(row, "–")
				}
				row = append(row, status)
			} else {
				for _, b := range c.Brands {
					row = append(row, rate(b))
				}
			}
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		tw.Flush()
	}

	if len(v.Domains) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("Sites the answers cited"))
		tw = table(w)
		fmt.Fprintln(tw, "  Site\tCitations\tIn answers\tWhose")
		for i, d := range v.Domains {
			if i == 8 {
				fmt.Fprintf(tw, "  …and %d more\t\t\t\n", len(v.Domains)-8)
				break
			}
			fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\n", d.Domain, d.Citations, d.Answers, owner(d.Owner))
		}
		tw.Flush()
	} else if hasSearchMode(r.Run.Modes) {
		fmt.Fprintln(w, "\nNo answer cited a website.")
	}

	if len(v.Unknown) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("Other names in the answers, possibly competitors"))
		for i, u := range v.Unknown {
			if i == 6 {
				fmt.Fprintf(w, "  …and %d more (see --json)\n", len(v.Unknown)-6)
				break
			}
			fmt.Fprintf(w, "  %s: %s, from %s\n", u.Name, plural(u.Answers, "answer"), config.JoinAnd(u.Engines))
		}
	}

	if len(v.Excerpts) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("What the assistants said"))
		for _, e := range v.Excerpts {
			fmt.Fprintf(w, "  %s · %s · %q\n", e.Engine, config.ModeLabel(e.Mode), shorten(e.Prompt, 70))
			suffix := ""
			if !e.MentionsYou {
				suffix = fmt.Sprintf(" (%s not mentioned)", v.Brand)
			}
			fmt.Fprintf(w, "    “%s”%s\n", e.Text, suffix)
		}
	}

	if len(v.Highlights) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("What stands out"))
		for _, line := range v.Highlights {
			fmt.Fprintln(w, "  - "+line)
		}
	}
	if note := namingYouNote(len(v.PromptsNamingYou), v.Brand); note != "" {
		fmt.Fprintf(w, "\n%s\n", note)
	}
}

// namingYouNote says how many prompts were left out of your numbers because
// they already name you.
func namingYouNote(n int, brand string) string {
	switch {
	case n == 1:
		return fmt.Sprintf("1 prompt names %s, so its answers are left out of %s's numbers.", brand, brand)
	case n > 1:
		return fmt.Sprintf("%d prompts name %s, so their answers are left out of %s's numbers.", n, brand, brand)
	}
	return ""
}

func writeSuiteMetrics(w io.Writer, s SuiteMetrics, style Style) {
	fmt.Fprintf(w, "\n%s\n", style.heading("Suite "+s.Suite))
	if len(s.Rows) == 0 {
		fmt.Fprintln(w, "  No metrics. `llmention report --details` lists everything that was measured.")
		return
	}
	tw := table(w)
	fmt.Fprintln(tw, "  Metric\t"+strings.Join(s.Columns, "\t"))
	for _, row := range s.Rows {
		cells := []string{"  " + row.Metric}
		for _, v := range row.Values {
			cells = append(cells, metricText(row.Metric, v))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

func writeGates(w io.Writer, r Report, style Style) {
	if len(r.Gates) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", style.heading("Checks"))
	for _, g := range r.Gates {
		fmt.Fprintf(w, "  %s  %s: %s\n", strings.ToUpper(g.Status), g.Selector, g.Message)
	}
}

// WriteAnswers prints every answer, grouped by prompt.
func WriteAnswers(w io.Writer, r Report, style Style) {
	writeHeader(w, r)
	last := ""
	for _, a := range r.Answers {
		key := a.Suite + "/" + a.PromptID
		if key != last {
			fmt.Fprintf(w, "\n%s\n", style.heading(cmp.Or(a.Question, a.Prompt)))
			fmt.Fprintf(w, "  prompt id %s\n", a.PromptID)
			last = key
		}
		label := a.Engine + " · " + config.ModeLabel(a.Mode)
		if a.Repetition > 1 {
			label += fmt.Sprintf(" · repetition %d", a.Repetition)
		}
		fmt.Fprintf(w, "\n  %s\n", label)
		switch {
		case a.Status == "success" || a.Status == "unfinished":
			if a.Status == "unfinished" {
				fmt.Fprintf(w, "    (unfinished: %s; left out of the numbers)\n", UnfinishedReason(a.StopReason))
			}
			text := strings.TrimSpace(cmp.Or(a.readable, a.Text))
			if text == "" {
				text = "(" + noTextYet + ")"
			}
			fmt.Fprintln(w, indent(text, "    "))
			if len(a.Citations) > 0 {
				fmt.Fprintln(w, "    Cited:")
				for _, c := range a.Citations {
					fmt.Fprintf(w, "      %s\n", c.URL)
				}
			}
		case a.Error != "":
			fmt.Fprintf(w, "    (no answer: %s)\n", a.Error)
		default:
			fmt.Fprintf(w, "    (no answer: %s)\n", strings.ReplaceAll(a.Status, "_", " "))
		}
	}
}

// WriteDetails prints every metric in the full table.
func WriteDetails(w io.Writer, r Report) {
	writeHeader(w, r)
	fmt.Fprintln(w)
	tw := table(w)
	fmt.Fprintln(tw, "Metric\tLevel\tEngine\tMode\tScope\tValue\tCount")
	metrics := slices.Clone(r.Metrics)
	slices.SortStableFunc(metrics, func(a, b evaluate.MetricValue) int {
		return cmp.Or(
			strings.Compare(a.MetricID, b.MetricID),
			cmp.Compare(levelOrder(a.Scope.Level), levelOrder(b.Scope.Level)),
			strings.Compare(a.Scope.EngineID, b.Scope.EngineID),
			strings.Compare(a.Scope.Mode+a.Scope.ModeFrom, b.Scope.Mode+b.Scope.ModeFrom),
		)
	})
	for _, m := range metrics {
		scope := strings.Trim(strings.Join([]string{m.Scope.ItemID, m.Scope.VariantID, m.Scope.Tag, m.Scope.EntityID, m.Scope.Domain, m.Scope.SourceChannel}, " "), " ")
		scope = strings.Join(strings.Fields(scope), " ")
		mode := m.Scope.Mode
		if m.Scope.ModeFrom != "" {
			mode = m.Scope.ModeFrom + " → " + m.Scope.ModeTo
		}
		value := "–"
		if v, ok := metricNumber(m); ok {
			value = metricText(m.MetricID, &v)
		} else if m.UnavailableReason != "" {
			value = "– (" + strings.ReplaceAll(m.UnavailableReason, "_", " ") + ")"
		}
		count := ""
		if m.Numerator != nil && m.Denominator != nil && *m.Denominator > 0 {
			count = fmt.Sprintf("%g/%g", *m.Numerator, *m.Denominator)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.MetricID, strings.TrimPrefix(m.Scope.Level, "suite_"), m.Scope.EngineID, mode, scope, value, count)
	}
	tw.Flush()
}

// levelOrder puts the broad rows of the metric table before the narrow ones.
func levelOrder(level string) int {
	order := []string{"suite_mode", "suite_mode_engine", "suite_mode_engine_tag", "suite_mode_item", "suite_mode_engine_item", "suite_mode_item_variant", "suite_mode_engine_item_variant"}
	if i := slices.Index(order, level); i >= 0 {
		return i
	}
	return len(order)
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 2, 2, ' ', 0) }

func localTime(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return t.Local().Format("2 Jan 2006 15:04")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func fraction(n, d int) string {
	if d == 0 {
		return "–"
	}
	return fmt.Sprintf("%d of %d", n, d)
}

func rate(b visibility.BrandStats) string {
	v, ok := b.MentionRate()
	return percent(v, ok)
}

func percent(v float64, ok bool) string {
	if !ok {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", math.Round(v*100))
}

// noTextYet stands in for an unfinished answer without text. A model that
// thinks before it writes can use up the whole token limit on thinking.
const noTextYet = "no text came back before the provider stopped"

// UnfinishedReason says in words why a provider cut an answer off.
func UnfinishedReason(stopReason string) string {
	switch stopReason {
	case "max_tokens", "max_output_tokens", "length", "MAX_TOKENS":
		return "the answer reached the output token limit"
	case "pause_turn":
		return "the provider paused a long web search before the answer was written"
	case "model_context_window_exceeded":
		return "the conversation got too long for the model's context window"
	case "content_filter":
		return "the provider's content filter stopped the answer"
	case "", "incomplete":
		return "the provider marked the answer incomplete"
	}
	return "the provider stopped early (" + strings.ReplaceAll(stopReason, "_", " ") + ")"
}

// noAnswers says why an engine and mode has nothing to count, for example
// "no answers to count: 3 stopped". It returns "" when there are answers.
func noAnswers(c visibility.Cell) string {
	if c.Answers > 0 {
		return ""
	}
	var parts []string
	for _, p := range []struct {
		n    int
		word string
	}{{c.Stopped, "stopped"}, {c.Failed, "failed"}, {c.Unfinished, "unfinished"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
		}
	}
	if len(parts) == 0 {
		return "no answers to count"
	}
	return "no answers to count: " + config.JoinAnd(parts)
}

func avgRank(b visibility.BrandStats) string {
	v, ok := b.AvgRank()
	if !ok {
		return "–"
	}
	return fmt.Sprintf("%.1f", v)
}

func owner(o string) string {
	switch o {
	case "":
		return ""
	case "you":
		return "yours"
	}
	return o
}

func hasSearchMode(modes []string) bool {
	for _, m := range modes {
		if config.IsSearchMode(m) {
			return true
		}
	}
	return false
}

// metricText formats a metric value: rates as percentages, the rest as
// numbers.
func metricText(metricID string, v *float64) string {
	if v == nil {
		return "–"
	}
	if strings.HasSuffix(metricID, "_rate") || strings.HasSuffix(metricID, "_share") || strings.HasSuffix(metricID, "share_of_voice") || strings.HasSuffix(metricID, "coverage") {
		return percent(*v, true)
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", *v), "0"), ".")
}

func shorten(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(prefix+l, " ")
	}
	return strings.Join(lines, "\n")
}
