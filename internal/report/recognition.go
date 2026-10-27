package report

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
)

// Recognition summarizes a recognition suite: for each name the assistants
// were asked about, what each engine said it is.
type Recognition struct {
	Suite string   `json:"suite"`
	Title string   `json:"title"`
	Modes []string `json:"modes"`
	// Columns are the engines, or engine · mode when the suite ran in more
	// than one mode.
	Columns []string         `json:"columns"`
	Rows    []RecognitionRow `json:"rows"`
	// Totals add up every name, one per column.
	Totals []RecognitionCell `json:"totals"`
}

// RecognitionRow is one name across the columns.
type RecognitionRow struct {
	ItemID string            `json:"item_id"`
	Name   string            `json:"name"`
	Kind   string            `json:"kind,omitempty"` // brand, product, person or site
	Cells  []RecognitionCell `json:"cells"`
}

// RecognitionCell counts what one engine in one mode said about a name,
// repetitions included.
type RecognitionCell struct {
	// Knew counts answers that described the one you meant.
	Knew int `json:"knew"`
	// SomethingElse counts answers that only described other things with
	// the same name.
	SomethingElse int `json:"something_else"`
	// MadeUp counts answers the AI grader judged to be made up.
	MadeUp int `json:"possibly_made_up"`
	// NotKnown counts answers that named nothing.
	NotKnown int `json:"did_not_know"`
	// Unreadable counts answers that were not in the JSON format the
	// question asked for.
	Unreadable int `json:"unreadable"`
	// NoAnswer counts requests that failed, were stopped or were cut off.
	NoAnswer int `json:"no_answer"`
}

// Read counts the answers llmention could read.
func (c RecognitionCell) Read() int { return c.Knew + c.SomethingElse + c.MadeUp + c.NotKnown }

func (c RecognitionCell) total() int { return c.Read() + c.Unreadable + c.NoAnswer }

func (c *RecognitionCell) add(o RecognitionCell) {
	c.Knew += o.Knew
	c.SomethingElse += o.SomethingElse
	c.MadeUp += o.MadeUp
	c.NotKnown += o.NotKnown
	c.Unreadable += o.Unreadable
	c.NoAnswer += o.NoAnswer
}

// Text says in a few words what the answers said, for example "knew it" or
// "knew it 2 of 3" when a name was asked more than once.
func (c RecognitionCell) Text() string {
	total := c.total()
	if total == 0 {
		return "–"
	}
	if c.Knew > 0 {
		if c.Knew == total {
			return "knew it"
		}
		return fmt.Sprintf("knew it %d of %d", c.Knew, total)
	}
	outcomes := []struct {
		n    int
		text string
	}{
		{c.SomethingElse, "something else"},
		{c.MadeUp, "possibly made up"},
		{c.NotKnown, "did not know it"},
		{c.Unreadable, "unreadable answer"},
		{c.NoAnswer, "no answer"},
	}
	best := outcomes[0]
	for _, o := range outcomes[1:] {
		if o.n > best.n {
			best = o
		}
	}
	if best.n == total {
		return best.text
	}
	return fmt.Sprintf("%s %d of %d", best.text, best.n, total)
}

// recognitionCell sorts one answer into its RecognitionCell count.
func recognitionCell(p recognition.CellPayload) RecognitionCell {
	switch {
	case p.ParserStatus == recognition.ParserStatusError:
		return RecognitionCell{Unreadable: 1}
	case p.ParserStatus != recognition.ParserStatusCompleted:
		return RecognitionCell{NoAnswer: 1}
	case p.TargetRecognized != nil && *p.TargetRecognized:
		return RecognitionCell{Knew: 1}
	case len(p.Assignments) == 0:
		return RecognitionCell{NotKnown: 1}
	}
	for _, a := range p.Assignments {
		if a.AssignmentClassification != recognition.ClassificationHallucinationCandidate {
			return RecognitionCell{SomethingElse: 1}
		}
	}
	return RecognitionCell{MadeUp: 1}
}

// recognitionPayloads collects the scored recognition answers by suite,
// prompt, engine, mode and repetition.
func recognitionPayloads(evaluations []evaluate.EvaluationRecord) map[string]recognition.CellPayload {
	out := map[string]recognition.CellPayload{}
	for _, e := range evaluations {
		if p, ok := e.Payload.(recognition.CellPayload); ok {
			out[answerKey(p.SuiteID, p.ItemID, p.EngineID, p.Mode, p.Repetition)] = p
		}
	}
	return out
}

func answerKey(suiteID, itemID, engineID, mode string, repetition int) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", suiteID, itemID, engineID, mode, repetition)
}

func buildRecognition(s scorecard.Suite, card *scorecard.Scorecard, brand string, payloads map[string]recognition.CellPayload) Recognition {
	out := Recognition{Suite: s.ID, Title: "Do assistants know these names?", Modes: s.Modes}
	if brand != "" && s.Definition.ContextMode == suite.ContextModeProjectBound {
		out.Title = "Do assistants know " + brand + "?"
	}
	type col struct{ engine, mode string }
	var cols []col
	for _, e := range card.Engines {
		for _, m := range s.Modes {
			cols = append(cols, col{e.ID, m})
			label := e.ID
			if len(s.Modes) > 1 {
				label += " · " + config.ModeLabel(m)
			}
			out.Columns = append(out.Columns, label)
		}
	}
	out.Totals = make([]RecognitionCell, len(cols))
	for _, item := range s.Definition.Items {
		row := RecognitionRow{ItemID: item.ID, Name: item.Query, Cells: make([]RecognitionCell, len(cols))}
		if row.Name == "" {
			row.Name = item.ID
		}
		if len(item.Tags) > 0 {
			row.Kind = item.Tags[0]
			if row.Kind == "domain" {
				row.Kind = "site"
			}
		}
		for i, c := range cols {
			for rep := 1; rep <= max(1, s.Repetitions); rep++ {
				if p, ok := payloads[answerKey(s.ID, item.ID, c.engine, c.mode, rep)]; ok {
					row.Cells[i].add(recognitionCell(p))
				}
			}
			out.Totals[i].add(row.Cells[i])
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// recognitionNotes explains the words in the table, and why some answers
// could not be read.
func recognitionNotes(r Recognition) []string {
	asked := "Each assistant was asked who or what the name is."
	if len(r.Modes) > 0 && !slices.ContainsFunc(r.Modes, func(m string) bool { return m != config.ModeMemory }) {
		asked = "Each assistant was asked who or what the name is, without web search."
	}
	notes := []string{asked + ` "Knew it" means the answer described yours. "Something else" means it only described other things with the same name.`}
	unreadable := 0
	for _, t := range r.Totals {
		unreadable += t.Unreadable
	}
	if unreadable > 0 {
		notes = append(notes, fmt.Sprintf("%s not in the JSON format the question asked for, so llmention could not read %s. `--answers` shows %s.",
			pick(unreadable, "1 answer was", fmt.Sprintf("%d answers were", unreadable)), pick(unreadable, "it", "them"), pick(unreadable, "it", "them")))
	}
	return notes
}

func knewText(c RecognitionCell) string {
	return fraction(c.Knew, c.Read())
}

func writeRecognition(w io.Writer, r Recognition, style Style) {
	fmt.Fprintf(w, "\n%s\n", style.heading(r.Title))
	tw := table(w)
	fmt.Fprintln(tw, "  Name\tKind\t"+strings.Join(r.Columns, "\t"))
	for _, row := range r.Rows {
		cells := []string{"  " + row.Name, row.Kind}
		for _, c := range row.Cells {
			cells = append(cells, c.Text())
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	if len(r.Rows) > 1 {
		cells := []string{"  Knew", ""}
		for _, t := range r.Totals {
			cells = append(cells, knewText(t))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
	for _, n := range recognitionNotes(r) {
		fmt.Fprintln(w, "  "+n)
	}
}

func writeRecognitionMarkdown(w io.Writer, r Recognition) {
	fmt.Fprintf(w, "\n**%s**\n\n| Name | Kind | %s |\n|---|---|%s\n", md(r.Title), strings.Join(mdAll(r.Columns), " | "), strings.Repeat("---|", len(r.Columns)))
	for _, row := range r.Rows {
		fmt.Fprintf(w, "| %s | %s |", md(row.Name), row.Kind)
		for _, c := range row.Cells {
			fmt.Fprintf(w, " %s |", c.Text())
		}
		fmt.Fprintln(w)
	}
	if len(r.Rows) > 1 {
		fmt.Fprint(w, "| **Knew** | |")
		for _, t := range r.Totals {
			fmt.Fprintf(w, " **%s** |", knewText(t))
		}
		fmt.Fprintln(w)
	}
	for _, n := range recognitionNotes(r) {
		fmt.Fprintf(w, "\n%s\n", md(n))
	}
}

func mdAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = md(v)
	}
	return out
}

// recognitionAnswerText turns a recognition answer into lines a person can
// read, such as "1. Acme Books (product, 70% sure): an invoicing tool". It
// returns "" when the answer could not be read, so the raw text is shown.
func recognitionAnswerText(p recognition.CellPayload) string {
	if p.ParserStatus != recognition.ParserStatusCompleted {
		return ""
	}
	if len(p.ParsedResults) == 0 {
		return "Named nothing."
	}
	results := slices.Clone(p.ParsedResults)
	slices.SortStableFunc(results, func(a, b recognition.ParsedResult) int { return a.Rank - b.Rank })
	var lines []string
	for i, r := range results {
		line := fmt.Sprintf("%d. %s (%s, %d%% sure)", i+1, r.Name, r.Type, r.Confidence)
		if r.Snippet != "" {
			line += ": " + r.Snippet
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// recognitionQuestion is the short form of the long recognition prompt.
func recognitionQuestion(item suite.Item) string {
	if item.Query == "" {
		return ""
	}
	return fmt.Sprintf("Who or what is %q?", item.Query)
}
