// Package report turns scored runs into what people read. It writes the
// summary as text, as Markdown or as a single HTML page, and it compares runs
// and shows trends over time.
package report

import (
	"fmt"
	"slices"
	"time"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Report is one scored run, ready to render. Its JSON form is what
// `llmention report --json` prints.
type Report struct {
	Run        RunInfo             `json:"run"`
	Notes      []string            `json:"notes,omitempty"`
	Visibility *visibility.Summary `json:"visibility,omitempty"`
	// Recognition has one summary per recognition suite in the run.
	Recognition []Recognition    `json:"recognition,omitempty"`
	Suites      []SuiteMetrics   `json:"suites,omitempty"`
	Gates       []scorecard.Gate `json:"gates,omitempty"`
	Answers     []Answer         `json:"answers"`
	// Metrics is the full metric table. It is large, so --json carries it
	// only together with --details.
	Metrics []evaluate.MetricValue `json:"metrics,omitempty"`
	Trend   *Trend                 `json:"trend,omitempty"`
}

// RunInfo describes the run behind a report.
type RunInfo struct {
	ID          string             `json:"id"`
	CreatedAt   time.Time          `json:"created_at"`
	Status      string             `json:"status"`
	Brand       string             `json:"brand,omitempty"`
	Competitors []string           `json:"competitors,omitempty"`
	Engines     []scorecard.Engine `json:"engines"`
	Modes       []string           `json:"modes"`
	Suites      []string           `json:"suites"`
	Prompts     int                `json:"prompts"`
	Counts      scorecard.Counts   `json:"counts"`
	Demo        bool               `json:"demo"`
	// Mock is true for a run made with --mock, which is saved apart from
	// real runs.
	Mock bool `json:"mock,omitempty"`
}

// Answer is one stored answer, for --answers and the HTML page.
type Answer struct {
	Suite    string `json:"suite"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
	// Question is a short form of a long prompt, such as the instructions
	// and JSON format of a recognition check.
	Question   string                `json:"question,omitempty"`
	Engine     string                `json:"engine"`
	Mode       string                `json:"mode"`
	Repetition int                   `json:"repetition"`
	Status     string                `json:"status"`
	StopReason string                `json:"stop_reason,omitempty"`
	Text       string                `json:"text,omitempty"`
	Error      string                `json:"error,omitempty"`
	Citations  []visibility.Citation `json:"citations,omitempty"`

	// readable is the answer in words when Text is JSON a person should not
	// have to read.
	readable string
}

// SuiteMetrics is a compact table for any other suite, such as the research
// suites: one value per metric, engine and mode.
type SuiteMetrics struct {
	Suite   string      `json:"suite"`
	Type    string      `json:"type"`
	Columns []string    `json:"columns"` // engine · mode
	Rows    []MetricRow `json:"rows"`
}

// MetricRow is one metric across the columns of a SuiteMetrics table.
type MetricRow struct {
	Metric string     `json:"metric"`
	Values []*float64 `json:"values"`
}

// Build assembles a report from a scored run.
func Build(a *analysis.Analysis) Report {
	card := a.Card
	r := Report{
		Run:        runInfo(card),
		Visibility: a.Visibility,
		Gates:      card.Gates,
		Metrics:    a.Metrics,
	}
	// Name the brands the numbers were scored with, which may be newer than
	// the ones saved with the run.
	if a.Brands.You.Name != "" {
		r.Run.Brand = a.Brands.You.Name
		r.Run.Competitors = nil
		for _, c := range a.Brands.Competitors {
			r.Run.Competitors = append(r.Run.Competitors, c.Name)
		}
	}
	r.Notes = notes(a)
	payloads := recognitionPayloads(a.Evaluated.Evaluations)
	for _, res := range card.Results {
		ans := Answer{
			Suite: res.Suite, PromptID: res.PromptID, Prompt: res.Prompt, Engine: res.Engine,
			Mode: res.Mode, Repetition: res.Repetition, Status: res.Status, Text: res.Answer,
		}
		if p, ok := payloads[answerKey(res.Suite, res.PromptID, res.Engine, res.Mode, res.Repetition)]; ok {
			ans.readable = recognitionAnswerText(p)
			if item, ok := card.Item(res.Suite, res.PromptID); ok {
				ans.Question = recognitionQuestion(item)
			}
		}
		if res.Status == run.ResultStatusUnfinished {
			ans.StopReason = res.StopReason
		}
		if res.Error != nil {
			ans.Error = res.Error.Message
		}
		for _, c := range res.Citations {
			ans.Citations = append(ans.Citations, visibility.Citation{URL: c.URL, Domain: c.Domain, Title: c.Title})
		}
		r.Answers = append(r.Answers, ans)
	}
	sortAnswers(r.Answers, card)
	for _, s := range card.Suites {
		switch s.Type {
		case "visibility":
		case "recognition":
			r.Recognition = append(r.Recognition, buildRecognition(s, card, r.Run.Brand, payloads))
		default:
			r.Suites = append(r.Suites, suiteMetrics(s, card, a.Metrics))
		}
	}
	return r
}

// sortAnswers groups answers by prompt, in the order of the suite file, then
// by engine and mode as configured.
func sortAnswers(answers []Answer, card *scorecard.Scorecard) {
	key := func(a Answer) []int {
		si := slices.IndexFunc(card.Suites, func(s scorecard.Suite) bool { return s.ID == a.Suite })
		pi, mi := 0, 0
		if si >= 0 {
			pi = slices.IndexFunc(card.Suites[si].Definition.Items, func(i suite.Item) bool { return i.ID == a.PromptID })
			mi = slices.Index(card.Suites[si].Modes, a.Mode)
		}
		ei := slices.IndexFunc(card.Engines, func(e scorecard.Engine) bool { return e.ID == a.Engine })
		return []int{si, pi, ei, mi, a.Repetition}
	}
	slices.SortStableFunc(answers, func(a, b Answer) int { return slices.Compare(key(a), key(b)) })
}

func runInfo(card *scorecard.Scorecard) RunInfo {
	info := RunInfo{
		ID:        card.ID,
		CreatedAt: card.CreatedAt,
		Status:    card.Status,
		Engines:   card.Engines,
		Counts:    card.Counts,
		Demo:      card.Demo(),
		Mock:      card.Mock,
	}
	if card.Brand != nil {
		info.Brand = card.Brand.Name
	}
	for _, c := range card.Competitors {
		info.Competitors = append(info.Competitors, c.Name)
	}
	prompts := map[string]bool{}
	for _, s := range card.Suites {
		info.Suites = append(info.Suites, s.ID)
		for _, m := range s.Modes {
			if !slices.Contains(info.Modes, m) {
				info.Modes = append(info.Modes, m)
			}
		}
		for _, item := range s.Definition.Items {
			prompts[s.ID+"/"+item.ID] = true
		}
	}
	info.Prompts = len(prompts)
	return info
}

func notes(a *analysis.Analysis) []string {
	var out []string
	card := a.Card
	switch {
	case card.Mock:
		out = append(out, "Demo data: this run used --mock, so the mock engine made up every answer. The numbers and quotes say nothing about what the real assistants answer.")
	case card.Demo():
		out = append(out, "Demo data: the mock engine made up these answers. To measure real assistants, set up a provider.")
	}
	switch card.Status {
	case scorecard.StatusInterrupted:
		out = append(out, "This run was stopped early. The numbers cover the answers that came back before it stopped.")
	case scorecard.StatusPartial:
		if card.Counts.BudgetUsedUp {
			out = append(out, "The request budget ran out before every prompt was sent. Raise execution.max_requests or ask fewer prompts.")
		} else if card.Counts.Failed > 0 {
			out = append(out, "Some requests failed. The numbers cover the answers that came back; `llmention report --answers` shows the errors.")
		}
	}
	if n := card.Counts.Unfinished; n > 0 {
		out = append(out, fmt.Sprintf("%d %s cut off before %s finished. %s with --answers, but %s out of the numbers.",
			n, pick(n, "answer was", "answers were"), pick(n, "it", "they"), pick(n, "You can read it", "You can read them"), pick(n, "it is left", "they are left")))
	}
	if a.BrandsChanged {
		out = append(out, "The numbers use the brand and competitors in llmention.yaml, which changed since this run.")
	}
	for _, issue := range a.Issues {
		if issue.Severity == evaluate.ReadinessFatal {
			out = append(out, "The full metric table is not available: "+issue.Message)
			break
		}
	}
	return out
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// suiteMetrics picks the rows of a suite's metrics that have one value per
// engine and mode.
func suiteMetrics(s scorecard.Suite, card *scorecard.Scorecard, metrics []evaluate.MetricValue) SuiteMetrics {
	out := SuiteMetrics{Suite: s.ID, Type: s.Type}
	type col struct{ engine, mode string }
	var cols []col
	for _, e := range card.Engines {
		for _, m := range s.Modes {
			cols = append(cols, col{e.ID, m})
			out.Columns = append(out.Columns, e.ID+" · "+config.ModeLabel(m))
		}
	}
	rows := map[string]*MetricRow{}
	var order []string
	for _, m := range metrics {
		if m.Scope.SuiteID != s.ID || m.Scope.Level != "suite_mode_engine" || m.Scope.EntityID != "" || m.Scope.Domain != "" || m.Scope.SourceChannel != "" {
			continue
		}
		i := slices.Index(cols, col{m.Scope.EngineID, m.Scope.Mode})
		if i < 0 {
			continue
		}
		row := rows[m.MetricID]
		if row == nil {
			row = &MetricRow{Metric: m.MetricID, Values: make([]*float64, len(cols))}
			rows[m.MetricID] = row
			order = append(order, m.MetricID)
		}
		if v, ok := metricNumber(m); ok {
			row.Values[i] = &v
		}
	}
	slices.Sort(order)
	for _, id := range order {
		out.Rows = append(out.Rows, *rows[id])
	}
	return out
}

func metricNumber(m evaluate.MetricValue) (float64, bool) {
	if m.Status != evaluate.MetricStatusAvailable {
		return 0, false
	}
	switch {
	case m.Value.Number != nil:
		return *m.Value.Number, true
	case m.Value.Integer != nil:
		return float64(*m.Value.Integer), true
	}
	return 0, false
}
