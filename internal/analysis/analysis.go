// Package analysis scores a saved run with your current brand and
// competitors. The result is the mention summary that reports show and the
// full metric table behind `llmention report --details`.
package analysis

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Options control how a scorecard is scored.
type Options struct {
	// Project is the current llmention.yaml, or nil. When it names a brand,
	// its brand and competitors are used instead of the ones saved with the
	// run, so every run is scored the same way.
	Project *config.Project
	// Judge is a live AI grader for a run that just finished. Without it,
	// grader answers are replayed from the scorecard.
	Judge   engine.Engine
	Context context.Context
	// Redact removes secrets from text sent to the grader.
	Redact func(string) string
}

// Analysis is a scored run.
type Analysis struct {
	Card   *scorecard.Scorecard `json:"-"`
	Brands visibility.Brands    `json:"brands"`
	// ScoredWithCurrentConfig is true when the brand and competitors came
	// from llmention.yaml rather than from the saved run.
	ScoredWithCurrentConfig bool `json:"scored_with_current_config"`
	// BrandsChanged is true when they differ from the ones the run saved.
	BrandsChanged bool                      `json:"brands_changed"`
	Visibility    *visibility.Summary       `json:"visibility,omitempty"`
	Evaluated     evaluate.EvaluatedRun     `json:"-"`
	Metrics       []evaluate.MetricValue    `json:"metrics"`
	Issues        []evaluate.ReadinessIssue `json:"issues,omitempty"`
	// Judgments are the grader answers collected by a live Judge.
	Judgments []scorecard.Judgment `json:"-"`
}

// Analyze scores a scorecard.
func Analyze(card *scorecard.Scorecard, opts Options) (*Analysis, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	project, current := scoringProject(card, opts.Project)
	out := &Analysis{Card: card, ScoredWithCurrentConfig: current}
	if project.Brand != nil {
		out.Brands = Brands(project)
		if current {
			out.BrandsChanged = !snapshotBrands(card).Same(out.Brands)
		}
	}

	if answers := VisibilityAnswers(card); len(answers) > 0 && project.Brand != nil {
		summary := visibility.Summarize(out.Brands, answers)
		summary.Highlights = visibility.Highlights(summary, card.Demo())
		out.Visibility = &summary
	}

	entries, err := suiteEntries(card)
	if err != nil {
		return nil, err
	}
	entries, err = evaluate.RebindProject(project, entries)
	if err != nil {
		return nil, err
	}
	plan := planFromCard(card)
	judge, recorder := judgeFor(card, opts.Judge)
	modelAssisted := evaluate.ModelAssistedPlanInput{
		Enabled:          judge != nil,
		Judge:            judgeConfig(project, card),
		MaxJudgeRequests: max(project.Execution.MaxJudgeRequests, len(card.Results)*(1+plan.Retries)),
	}
	if judge != nil {
		caps := judge.Capabilities()
		modelAssisted.JudgeCapability = &caps
	}
	prepared, err := evaluate.Prepare(evaluate.PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        evaluate.SuiteSnapshots(entries),
		Plan:               plan,
		EngineCapabilities: engineSnapshots(card),
		ModelAssisted:      modelAssisted,
	})
	if err != nil {
		return nil, fmt.Errorf("could not score run %s: %w", card.ID, err)
	}
	out.Issues = prepared.ReadinessIssues
	if !prepared.EvaluationReady {
		return out, nil
	}
	evaluated, err := evaluate.Evaluate(evaluate.EvaluationInput{
		Context:            ctx,
		Prepared:           prepared,
		RawRun:             rawRunFromCard(card),
		JudgeEngine:        judge,
		DisclosureRedactor: opts.Redact,
	})
	if err != nil {
		return nil, fmt.Errorf("could not score run %s: %w", card.ID, err)
	}
	out.Evaluated = evaluated
	out.Metrics = evaluated.Metrics
	if recorder != nil {
		out.Judgments = recorder.judgments()
	}
	return out, nil
}

// Visibility scores only the visibility answers of a run, which is all a
// trend needs. It returns nil when the run has no visibility answers or no
// brand is known.
func Visibility(card *scorecard.Scorecard, current *config.Project) (*visibility.Summary, visibility.Brands) {
	project, _ := scoringProject(card, current)
	if project.Brand == nil {
		return nil, visibility.Brands{}
	}
	brands := Brands(project)
	answers := VisibilityAnswers(card)
	if len(answers) == 0 {
		return nil, brands
	}
	s := visibility.Summarize(brands, answers)
	return &s, brands
}

// ScoringBrands returns the brands a run is scored with.
func ScoringBrands(card *scorecard.Scorecard, current *config.Project) (visibility.Brands, bool) {
	project, _ := scoringProject(card, current)
	if project.Brand == nil {
		return visibility.Brands{}, false
	}
	return Brands(project), true
}

// Brands turns a project's brand and competitors into visibility brands.
// Product names count as mentions of you.
func Brands(p *config.Project) visibility.Brands {
	b := visibility.Brands{You: visibility.Brand{
		Name:      p.Brand.Name,
		Aliases:   append(slices.Clone(p.Brand.Aliases), p.Brand.Products...),
		Domains:   slices.Clone(p.Brand.Domains),
		MatchCase: p.Brand.MatchCase,
	}}
	for _, c := range p.Competitors {
		b.Competitors = append(b.Competitors, visibility.Brand{Name: c.Name, Aliases: slices.Clone(c.Aliases), Domains: slices.Clone(c.Domains), MatchCase: c.MatchCase})
	}
	return b
}

func snapshotBrands(card *scorecard.Scorecard) visibility.Brands {
	if card.Brand == nil {
		return visibility.Brands{}
	}
	return Brands(&config.Project{Brand: card.Brand, Competitors: card.Competitors})
}

// scoringProject picks the brand and competitors to score with. That is
// llmention.yaml when it names a brand, otherwise what the run saved.
func scoringProject(card *scorecard.Scorecard, current *config.Project) (*config.Project, bool) {
	p := &config.Project{Version: 1, Brand: card.Brand, Competitors: card.Competitors, Execution: config.ExecutionConfig{MaxJudgeRequests: config.DefaultMaxJudgeRequests}}
	usedCurrent := false
	if current != nil {
		if current.Brand != nil {
			p.Brand, p.Competitors = current.Brand, current.Competitors
			usedCurrent = true
		}
		p.Analysis = current.Analysis
		if current.Execution.MaxJudgeRequests > 0 {
			p.Execution.MaxJudgeRequests = current.Execution.MaxJudgeRequests
		}
	}
	return p, usedCurrent
}

// VisibilityAnswers lists every request of the visibility suites, answered or
// not, in report order: engines as configured, then modes, then prompts.
func VisibilityAnswers(card *scorecard.Scorecard) []visibility.Answer {
	type keyed struct {
		answer visibility.Answer
		key    []int
	}
	var list []keyed
	for _, s := range card.Suites {
		if s.Type != "visibility" {
			continue
		}
		promptOrder := map[string]int{}
		for i, item := range s.Definition.Items {
			promptOrder[item.ID] = i
		}
		for _, r := range card.Results {
			if r.Suite != s.ID {
				continue
			}
			e, _ := card.Engine(r.Engine)
			a := visibility.Answer{
				PromptID:          r.PromptID,
				Prompt:            r.Prompt,
				Engine:            r.Engine,
				Mode:              r.Mode,
				Repetition:        r.Repetition,
				Text:              r.Answer,
				Failed:            !r.HasText(),
				Stopped:           r.Status == run.ResultStatusNotStarted || r.Status == run.ResultStatusCancelled,
				Unfinished:        r.Status == run.ResultStatusUnfinished,
				CitationsReported: config.IsSearchMode(r.Mode) && e.Capabilities.ExposesCitations,
			}
			for _, c := range r.Citations {
				a.Citations = append(a.Citations, visibility.Citation{URL: c.URL, Domain: c.Domain, Title: c.Title})
			}
			engineIndex := slices.IndexFunc(card.Engines, func(e scorecard.Engine) bool { return e.ID == r.Engine })
			list = append(list, keyed{a, []int{engineIndex, slices.Index(s.Modes, r.Mode), promptOrder[r.PromptID], r.Repetition}})
		}
	}
	slices.SortStableFunc(list, func(a, b keyed) int { return slices.Compare(a.key, b.key) })
	out := make([]visibility.Answer, len(list))
	for i, k := range list {
		out[i] = k.answer
	}
	return out
}

func suiteEntries(card *scorecard.Scorecard) ([]suite.LoadedSuite, error) {
	var out []suite.LoadedSuite
	for _, s := range card.Suites {
		if s.Definition == nil {
			return nil, fmt.Errorf("run %s has no definition for suite %s", card.ID, s.ID)
		}
		out = append(out, suite.LoadedSuite{Suite: s.Definition, Source: suite.Source(s.Source)})
	}
	return out, nil
}

func planFromCard(card *scorecard.Scorecard) run.Plan {
	var plan run.Plan
	for _, s := range card.Suites {
		plan.Suites = append(plan.Suites, run.SuitePlan{ID: s.ID, Version: s.Version, Type: s.Type, Source: s.Source, Modes: s.Modes, Repetitions: s.Repetitions})
	}
	seen := map[[2]string]bool{}
	addMatrix := func(engineID, mode string, supported bool) {
		if key := [2]string{engineID, mode}; !seen[key] {
			seen[key] = true
			e, _ := card.Engine(engineID)
			plan.EngineModeMatrix = append(plan.EngineModeMatrix, run.EngineModeSupport{EngineID: engineID, Provider: e.Provider, Model: e.Model, Mode: mode, Supported: supported})
		}
	}
	for _, r := range card.Results {
		plan.WorkUnits = append(plan.WorkUnits, workUnit(r))
		addMatrix(r.Engine, r.Mode, true)
	}
	for _, u := range card.Unsupported {
		addMatrix(u.EngineID, u.Mode, false)
	}
	plan.UnsupportedPairs = slices.Clone(card.Unsupported)
	slices.SortStableFunc(plan.EngineModeMatrix, func(a, b run.EngineModeSupport) int {
		return cmp.Or(cmp.Compare(a.EngineID, b.EngineID), cmp.Compare(a.Mode, b.Mode))
	})
	return plan
}

func workUnit(r scorecard.Result) run.WorkUnit {
	return run.WorkUnit{
		SuiteID:           r.Suite,
		SuiteVersion:      r.SuiteVersion,
		ItemID:            r.PromptID,
		VariantID:         r.Variant,
		Dimensions:        r.Dimensions,
		ComparisonGroupID: r.ComparisonGroupID,
		Mode:              engine.ToolPolicy(r.Mode),
		EngineID:          r.Engine,
		Repetition:        r.Repetition,
		Prompt:            r.Prompt,
	}
}

func rawRunFromCard(card *scorecard.Scorecard) run.RawSuiteRun {
	raw := run.RawSuiteRun{RunID: card.RunID, UnsupportedPairs: card.Unsupported}
	for _, r := range card.Results {
		e, _ := card.Engine(r.Engine)
		result := run.Result{
			WorkUnit:  workUnit(r),
			Provider:  e.Provider,
			Model:     e.Model,
			Status:    r.Status,
			Attempts:  r.Attempts,
			ElapsedMS: r.ElapsedMS,
			Error:     r.Error,
		}
		if r.HasText() {
			result.Answer = &engine.Answer{
				Text:             r.Answer,
				Parsed:           r.Parsed,
				Citations:        r.Citations,
				SourcesConsulted: r.Sources,
				ToolCalls:        r.Searches,
				ToolsAvailable:   r.ToolsAvailable,
				ToolsUsed:        r.ToolsUsed,
				StopReason:       r.StopReason,
				Unfinished:       r.Status == run.ResultStatusUnfinished,
			}
		}
		raw.Results = append(raw.Results, result)
	}
	return raw
}

func engineSnapshots(card *scorecard.Scorecard) []evaluate.EngineCapabilitySnapshot {
	var out []evaluate.EngineCapabilitySnapshot
	for _, e := range card.Engines {
		out = append(out, evaluate.EngineCapabilitySnapshot{EngineID: e.ID, Provider: e.Provider, Model: e.Model, Capabilities: e.Capabilities})
	}
	slices.SortFunc(out, func(a, b evaluate.EngineCapabilitySnapshot) int { return cmp.Compare(a.EngineID, b.EngineID) })
	return out
}

// judgeFor returns the grader to use. For a run that just finished, that is
// the live grader, and its answers are recorded. Otherwise it replays the
// answers saved with the run.
func judgeFor(card *scorecard.Scorecard, live engine.Engine) (engine.Engine, *recordingJudge) {
	if live != nil {
		r := &recordingJudge{Engine: live}
		return r, r
	}
	if len(card.Judgments) == 0 {
		return nil, nil
	}
	return newReplayJudge(card.Judgments), nil
}

func judgeConfig(p *config.Project, card *scorecard.Scorecard) *config.JudgeConfig {
	if p.Analysis.Judge != nil {
		return p.Analysis.Judge
	}
	if len(card.Judgments) > 0 {
		j := card.Judgments[0]
		return &config.JudgeConfig{Provider: j.Provider, Model: j.Model, AllowAnswerDisclosure: true}
	}
	return nil
}

type recordingJudge struct {
	engine.Engine
	mu       sync.Mutex
	recorded []scorecard.Judgment
}

func (r *recordingJudge) Ask(ctx context.Context, req engine.Request) (engine.Answer, error) {
	answer, err := r.Engine.Ask(ctx, req)
	if err == nil {
		r.mu.Lock()
		r.recorded = append(r.recorded, scorecard.Judgment{
			EvaluationID: req.Metadata["evaluation_id"],
			TemplateID:   req.Metadata["template_id"],
			Provider:     r.Provider(),
			Model:        r.Model(),
			Answer:       answer.Text,
			Parsed:       answer.Parsed,
		})
		r.mu.Unlock()
	}
	return answer, err
}

func (r *recordingJudge) judgments() []scorecard.Judgment {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.recorded)
	slices.SortFunc(out, func(a, b scorecard.Judgment) int {
		return cmp.Or(cmp.Compare(a.EvaluationID, b.EvaluationID), cmp.Compare(a.TemplateID, b.TemplateID))
	})
	return out
}

// replayJudge answers grader requests from saved judgments.
type replayJudge struct {
	byKey           map[string]scorecard.Judgment
	provider, model string
}

func newReplayJudge(judgments []scorecard.Judgment) *replayJudge {
	r := &replayJudge{byKey: map[string]scorecard.Judgment{}, provider: judgments[0].Provider, model: judgments[0].Model}
	for _, j := range judgments {
		r.byKey[j.EvaluationID+"\x00"+j.TemplateID] = j
	}
	return r
}

func (r *replayJudge) ID() string       { return engine.JudgeEngineID }
func (r *replayJudge) Provider() string { return r.provider }
func (r *replayJudge) Model() string    { return r.model }
func (r *replayJudge) Capabilities() engine.CapabilityProfile {
	return engine.CapabilityProfile{Provider: r.provider, Model: r.model, SupportsMemory: true, SupportsStructuredOutput: true}
}
func (r *replayJudge) ValidateRequest(engine.Request) error { return nil }
func (r *replayJudge) Ask(_ context.Context, req engine.Request) (engine.Answer, error) {
	j, ok := r.byKey[req.Metadata["evaluation_id"]+"\x00"+req.Metadata["template_id"]]
	if !ok {
		return engine.Answer{}, &engine.ProviderError{ProviderID: r.provider, SafeMessage: "the AI grader did not grade this answer when the run was made", Class: engine.ErrorClassConfig}
	}
	return engine.Answer{Text: j.Answer, Parsed: j.Parsed}, nil
}
