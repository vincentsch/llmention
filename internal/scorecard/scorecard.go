// Package scorecard defines the file llmention saves for every run.
//
// A scorecard holds what was asked and what each assistant answered, with the
// pages it cited. When the AI grader ran, it also holds the grader's answers.
// Reports work every number out again from the saved answers. So the numbers
// follow your current brand and competitors, and old runs stay comparable
// after you edit llmention.yaml.
package scorecard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/version"
)

// Run status values.
const (
	StatusComplete    = "complete"    // every request got an answer, finished or not
	StatusPartial     = "partial"     // some requests failed or were not sent
	StatusInterrupted = "interrupted" // stopped with Ctrl+C; finished answers are kept
)

// Scorecard is one saved run.
type Scorecard struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
	Version   string    `json:"llmention_version"`
	Status    string    `json:"status"`
	// Narrowed is true for a run made with --engine or --mode that asked
	// fewer engines or modes than llmention.yaml lists. Comparisons and
	// trends leave it out, so a one-off run never becomes the baseline.
	Narrowed bool `json:"narrowed,omitempty"`
	// Mock is true for a run made with --mock. The mock engine answered for
	// every engine, and the run is saved apart from real runs.
	Mock        bool                      `json:"mock,omitempty"`
	Brand       *config.BrandConfig       `json:"brand,omitempty"`
	Competitors []config.CompetitorConfig `json:"competitors,omitempty"`
	Engines     []Engine                  `json:"engines"`
	Suites      []Suite                   `json:"suites"`
	Counts      Counts                    `json:"counts"`
	Unsupported []run.UnsupportedPair     `json:"unsupported,omitempty"`
	Results     []Result                  `json:"results"`
	Judgments   []Judgment                `json:"judgments,omitempty"`
	Headline    []Headline                `json:"headline,omitempty"`
	Gates       []Gate                    `json:"gates,omitempty"`
	StartedAt   time.Time                 `json:"started_at"`
	FinishedAt  time.Time                 `json:"finished_at"`
}

// Engine records which provider and model answered.
type Engine struct {
	ID           string                   `json:"id"`
	Provider     string                   `json:"provider"`
	Model        string                   `json:"model"`
	BaseURLHost  string                   `json:"base_url_host,omitempty"`
	Capabilities engine.CapabilityProfile `json:"capabilities"`
}

// Demo reports whether the engine is the built-in mock, whose answers are
// made up.
func (e Engine) Demo() bool { return e.Provider == "mock" }

// Suite records a suite as it was run, including its items, so the answers
// can be scored again even after the suite file changes.
type Suite struct {
	ID          string       `json:"id"`
	Version     int          `json:"version"`
	Type        string       `json:"type"`
	Source      string       `json:"source"`
	Modes       []string     `json:"modes"`
	Repetitions int          `json:"repetitions"`
	Definition  *suite.Suite `json:"definition"`
}

// Counts summarize what happened to the requests.
type Counts struct {
	Planned  int `json:"planned"`
	Answered int `json:"answered"`
	// Unfinished answers were cut off by the provider. They are saved, but
	// they do not count as answers.
	Unfinished   int  `json:"unfinished,omitempty"`
	Failed       int  `json:"failed"`
	Cancelled    int  `json:"cancelled"`
	NotSent      int  `json:"not_sent"`
	RequestsSent int  `json:"requests_sent"`
	MaxRequests  int  `json:"max_requests"`
	BudgetUsedUp bool `json:"budget_used_up,omitempty"`
}

// Result is one answer, or the reason there is none.
type Result struct {
	Suite             string            `json:"suite"`
	SuiteVersion      int               `json:"suite_version"`
	PromptID          string            `json:"prompt_id"`
	Variant           string            `json:"variant"`
	Engine            string            `json:"engine"`
	Mode              string            `json:"mode"`
	Repetition        int               `json:"repetition"`
	Dimensions        map[string]string `json:"dimensions,omitempty"`
	ComparisonGroupID string            `json:"comparison_group_id,omitempty"`
	Status            string            `json:"status"`
	Prompt            string            `json:"prompt"`
	Answer            string            `json:"answer,omitempty"`
	Parsed            json.RawMessage   `json:"parsed,omitempty"`
	Citations         []engine.Citation `json:"citations,omitempty"`
	Sources           []engine.Source   `json:"sources_consulted,omitempty"`
	Searches          []engine.ToolCall `json:"tool_calls,omitempty"`
	ToolsAvailable    []string          `json:"tools_available,omitempty"`
	ToolsUsed         []string          `json:"tools_used,omitempty"`
	StopReason        string            `json:"stop_reason,omitempty"`
	Usage             *Usage            `json:"usage,omitempty"`
	Attempts          int               `json:"attempts"`
	ElapsedMS         int64             `json:"elapsed_ms"`
	Error             *run.ResultError  `json:"error,omitempty"`
}

// Answered reports whether the result holds a finished answer.
func (r Result) Answered() bool { return r.Status == run.ResultStatusSuccess }

// HasText reports whether the result holds answer text, finished or not.
func (r Result) HasText() bool {
	return r.Status == run.ResultStatusSuccess || r.Status == run.ResultStatusUnfinished
}

// Usage is what the provider reported about tokens and searches.
type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	Searches     int `json:"searches,omitempty"`
}

// Judgment is one answer from the AI grader. It is saved so a report can
// score the run again without asking the grader twice.
type Judgment struct {
	EvaluationID string          `json:"evaluation_id"`
	TemplateID   string          `json:"template_id"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Answer       string          `json:"answer"`
	Parsed       json.RawMessage `json:"parsed,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// Headline holds the mention numbers of one engine and mode. They are saved
// so `history list` does not have to score every old run again.
type Headline struct {
	Engine       string   `json:"engine"`
	Mode         string   `json:"mode"`
	Answers      int      `json:"answers"`
	Eligible     int      `json:"eligible_answers"`
	Mentioned    int      `json:"mentioned"`
	MentionRate  *float64 `json:"mention_rate"`
	ShareOfVoice *float64 `json:"share_of_voice"`
	AvgRank      *float64 `json:"avg_rank"`
}

// Gate is the outcome of one --fail-under check.
type Gate struct {
	Selector  string   `json:"selector"`
	Status    string   `json:"status"` // pass, fail, inconclusive or error
	Value     *float64 `json:"value,omitempty"`
	Threshold float64  `json:"threshold"`
	Scope     string   `json:"scope,omitempty"`
	Message   string   `json:"message"`
}

// Input is everything New needs.
type Input struct {
	Project *config.Project
	Plan    *run.Plan
	Suites  []suite.LoadedSuite // with your brand filled in, as they ran
	Engines []engine.Engine
	Raw     run.RawSuiteRun
	Now     time.Time
}

// New builds a scorecard from a finished or interrupted run.
func New(in Input) (*Scorecard, error) {
	if in.Project == nil || in.Plan == nil {
		return nil, fmt.Errorf("scorecard needs a project and a plan")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	card := &Scorecard{
		ID:          NewID(now),
		RunID:       in.Raw.RunID,
		CreatedAt:   now.UTC(),
		Version:     version.Version,
		Brand:       in.Project.Brand,
		Competitors: in.Project.Competitors,
		Unsupported: slices.Clone(in.Raw.UnsupportedPairs),
		Counts: Counts{
			Planned:      in.Raw.TargetWorkUnits,
			Answered:     in.Raw.CompletedWorkUnits,
			Unfinished:   in.Raw.UnfinishedWorkUnits,
			Failed:       in.Raw.ErroredWorkUnits,
			Cancelled:    in.Raw.CancelledWorkUnits,
			NotSent:      in.Raw.NotStartedWorkUnits,
			RequestsSent: in.Raw.RequestsSent,
			MaxRequests:  in.Raw.MaxRequests,
			BudgetUsedUp: in.Raw.BudgetExhausted,
		},
	}
	card.StartedAt, _ = time.Parse(time.RFC3339Nano, in.Raw.StartedAt)
	card.FinishedAt, _ = time.Parse(time.RFC3339Nano, in.Raw.FinishedAt)
	switch {
	case in.Raw.Interrupted:
		card.Status = StatusInterrupted
	case card.Counts.Answered+card.Counts.Unfinished == card.Counts.Planned:
		card.Status = StatusComplete
	default:
		card.Status = StatusPartial
	}
	baseURLs := map[string]string{}
	for _, e := range in.Project.Engines {
		baseURLs[e.ID] = e.BaseURL
	}
	for _, eng := range in.Engines {
		if !slices.ContainsFunc(in.Plan.EngineModeMatrix, func(m run.EngineModeSupport) bool { return m.EngineID == eng.ID() }) {
			continue
		}
		card.Engines = append(card.Engines, Engine{
			ID:           eng.ID(),
			Provider:     eng.Provider(),
			Model:        eng.Model(),
			BaseURLHost:  host(baseURLs[eng.ID()]),
			Capabilities: eng.Capabilities(),
		})
	}
	for _, sp := range in.Plan.Suites {
		var def *suite.Suite
		for _, entry := range in.Suites {
			if entry.Suite != nil && entry.Suite.ID == sp.ID {
				def = entry.Suite
			}
		}
		card.Suites = append(card.Suites, Suite{
			ID:          sp.ID,
			Version:     sp.Version,
			Type:        sp.Type,
			Source:      sp.Source,
			Modes:       sp.Modes,
			Repetitions: sp.Repetitions,
			Definition:  def,
		})
	}
	for _, r := range in.Raw.Results {
		card.Results = append(card.Results, fromRaw(r))
	}
	return card, nil
}

func fromRaw(r run.Result) Result {
	u := r.WorkUnit
	out := Result{
		Suite:             u.SuiteID,
		SuiteVersion:      u.SuiteVersion,
		PromptID:          u.ItemID,
		Variant:           u.VariantID,
		Engine:            u.EngineID,
		Mode:              u.Mode.String(),
		Repetition:        u.Repetition,
		Dimensions:        u.Dimensions,
		ComparisonGroupID: u.ComparisonGroupID,
		Status:            r.Status,
		Prompt:            u.Prompt,
		Attempts:          r.Attempts,
		ElapsedMS:         r.ElapsedMS,
		Error:             r.Error,
	}
	if a := r.Answer; a != nil {
		out.Answer = a.Text
		out.Parsed = a.Parsed
		out.Citations = a.Citations
		out.Sources = a.SourcesConsulted
		for _, call := range a.ToolCalls {
			call.Raw = nil
			out.Searches = append(out.Searches, call)
		}
		out.ToolsAvailable = a.ToolsAvailable
		out.ToolsUsed = a.ToolsUsed
		out.StopReason = a.StopReason
		out.Usage = usageFrom(a.ProviderMetadata)
	}
	return out
}

func usageFrom(meta map[string]string) *Usage {
	var u Usage
	fmt.Sscan(meta["usage_input_tokens"], &u.InputTokens)
	fmt.Sscan(meta["usage_output_tokens"], &u.OutputTokens)
	fmt.Sscan(meta["usage_search_queries"], &u.Searches)
	if u == (Usage{}) {
		return nil
	}
	return &u
}

// NewID returns a short id that sorts by time, such as
// "20261026-160841-3f9a1c".
func NewID(t time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return t.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func host(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Demo reports whether any engine in the run was the mock provider.
func (c *Scorecard) Demo() bool {
	return slices.ContainsFunc(c.Engines, Engine.Demo)
}

// Engine returns the engine with the given id.
func (c *Scorecard) Engine(id string) (Engine, bool) {
	i := slices.IndexFunc(c.Engines, func(e Engine) bool { return e.ID == id })
	if i < 0 {
		return Engine{}, false
	}
	return c.Engines[i], true
}

// Item returns a prompt of a suite as the run asked it.
func (c *Scorecard) Item(suiteID, itemID string) (suite.Item, bool) {
	for _, s := range c.Suites {
		if s.ID != suiteID || s.Definition == nil {
			continue
		}
		for _, item := range s.Definition.Items {
			if item.ID == itemID {
				return item, true
			}
		}
	}
	return suite.Item{}, false
}

// HasSuite reports whether the run included a suite.
func (c *Scorecard) HasSuite(id string) bool {
	return slices.ContainsFunc(c.Suites, func(s Suite) bool { return s.ID == id })
}

// Validate checks that a decoded scorecard is usable.
func (c *Scorecard) Validate() error {
	switch {
	case c.ID == "":
		return fmt.Errorf("scorecard has no id")
	case len(c.Suites) == 0:
		return fmt.Errorf("scorecard %s lists no suites", c.ID)
	}
	for _, s := range c.Suites {
		if s.Definition == nil {
			return fmt.Errorf("scorecard %s has no definition for suite %s", c.ID, s.ID)
		}
	}
	for i, r := range c.Results {
		if r.Suite == "" || r.PromptID == "" || r.Engine == "" || r.Mode == "" {
			return fmt.Errorf("scorecard %s: result %d is incomplete", c.ID, i)
		}
	}
	return nil
}
