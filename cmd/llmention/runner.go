package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	rungrad "github.com/vincentsch/rungrad"
	"golang.org/x/term"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/cost"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/gate"
	"github.com/vincentsch/llmention/internal/notify"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/store"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Exit codes beyond rungrad's own.
const (
	exitGateFailed  = 7
	exitInterrupted = 130
)

// runOptions narrows what a run sends.
type runOptions struct {
	SuiteIDs      []string
	EngineIDs     []string
	Modes         []string
	Repetitions   int
	MaxRequests   int
	FailUnder     []string
	ModelAssisted *bool
	// World overrides the demo market the mock provider writes about.
	World *engine.MockWorld
	// Now dates the saved run; the demo uses it to fake earlier weeks.
	Now time.Time
	// Quiet hides the progress line.
	Quiet bool
	// Mock lets the mock engine answer for every engine, see withMockEngines.
	Mock bool
}

// prepared is a run that is ready to send.
type prepared struct {
	project    *config.Project
	configDir  string
	factory    *engine.Factory
	engines    []engine.Engine
	bound      []suite.LoadedSuite
	plan       *run.Plan
	evaluation evaluate.PreparedEvaluation
	gates      []gate.Gate
	secrets    *secretRecorder
	estimate   cost.Estimate
	now        time.Time
	quiet      bool
	narrowed   bool
	mock       bool
}

// prepareRun builds the plan and checks everything that can be checked
// without reading a key or sending a request.
func prepareRun(f *rungrad.Factory, project *config.Project, configPath string, opts runOptions) (*prepared, error) {
	if opts.Mock {
		project = withMockEngines(project)
	}
	if err := project.ValidateForRun(); err != nil {
		return nil, usageError(err)
	}
	gates, err := gate.ParseAll(opts.FailUnder)
	if err != nil {
		return nil, usageError(err)
	}
	catalog, err := suite.LoadCatalog(project)
	if err != nil {
		return nil, usageError(err)
	}
	secrets := newSecretRecorder(f.RegisterSecret)
	world := opts.World
	if world == nil {
		world = mockWorld(project, "")
	}
	factory := engine.NewFactory(engine.FactoryOptions{
		ConfigDir:      filepath.Dir(configPath),
		LookupEnv:      lookupEnv(f),
		RegisterSecret: secrets.register,
		Timeout:        project.Execution.Timeout,
		World:          world,
	})
	engines, err := factory.NewTargetEngines(project)
	if err != nil {
		return nil, usageError(err)
	}
	bound, bindIssues, err := evaluate.BindProjectContext(project, catalog.Entries())
	if err != nil {
		return nil, err
	}
	boundCatalog, err := suite.NewCatalog(bound)
	if err != nil {
		return nil, err
	}
	planOpts := run.PlanOptions{SuiteIDs: opts.SuiteIDs, EngineIDs: opts.EngineIDs, Repetitions: opts.Repetitions, MaxRequests: opts.MaxRequests}
	for _, m := range opts.Modes {
		mode, err := engine.ParseToolPolicy(m)
		if err != nil {
			return nil, usageError(err)
		}
		planOpts.Modes = append(planOpts.Modes, mode)
	}
	plan, err := run.BuildPlan(project, boundCatalog, engines, planOpts)
	if err != nil {
		return nil, usageError(err)
	}
	selected := entriesInPlan(bound, plan)
	modelAssisted := evaluate.ModelAssistedPlanInput{
		Enabled:          project.Analysis.ModelAssistedEnabled,
		Judge:            project.Analysis.Judge,
		MaxJudgeRequests: project.Execution.MaxJudgeRequests,
	}
	if opts.ModelAssisted != nil {
		modelAssisted.Enabled = *opts.ModelAssisted
	}
	evaluation, err := evaluate.Prepare(evaluate.PrepareInput{
		Project:            *project,
		Suites:             selected,
		BoundSuites:        evaluate.SuiteSnapshots(selected),
		Plan:               *plan,
		EngineCapabilities: evaluate.EngineCapabilitySnapshots(engines),
		ModelAssisted:      modelAssisted,
		PrecomputedIssues:  issuesInPlan(bindIssues, plan),
	})
	if err != nil {
		return nil, usageError(err)
	}
	if err := project.CheckModels(engineIDsInPlan(plan)); err != nil {
		return nil, usageError(err)
	}
	var planned gate.Planned
	for _, s := range selected {
		planned.Suites = append(planned.Suites, s.Suite)
	}
	planned.Engines = engineIDsInPlan(plan)
	for _, s := range plan.Suites {
		for _, m := range s.Modes {
			if !slices.Contains(planned.Modes, m) {
				planned.Modes = append(planned.Modes, m)
			}
		}
	}
	if err := gate.Check(gates, planned); err != nil {
		return nil, usageError(err)
	}
	narrowed := len(opts.EngineIDs) > 0 && len(planned.Engines) < len(project.Engines) ||
		len(opts.Modes) > 0 && len(planned.Modes) < len(project.Modes)
	return &prepared{
		project:    project,
		configDir:  filepath.Dir(configPath),
		factory:    factory,
		engines:    engines,
		bound:      selected,
		plan:       plan,
		evaluation: evaluation,
		gates:      gates,
		secrets:    secrets,
		estimate:   cost.ForPlan(plan, project.Engines),
		now:        opts.Now,
		quiet:      opts.Quiet,
		narrowed:   narrowed,
		mock:       opts.Mock,
	}, nil
}

// mockRunsDir is where runs made with --mock are saved: a folder inside the
// history folder. The store only reads the files at the top of a folder, so
// the made-up runs never show up in reports, diffs or trends of real runs.
func mockRunsDir(runsDir string) string { return filepath.Join(runsDir, "mock") }

// withMockEngines returns a copy of the project in which the mock engine
// answers for every engine. The engine ids stay the same, so the report has
// the same rows as a real one. The copy needs no key and sends nothing, so
// the AI grader and the alerts are turned off.
func withMockEngines(p *config.Project) *config.Project {
	mocked := *p
	mocked.Engines = nil
	for _, e := range p.Engines {
		if e.Provider != "mock" {
			e = config.EngineConfig{ID: e.ID, Provider: "mock", Model: "demo"}
		}
		mocked.Engines = append(mocked.Engines, e)
	}
	mocked.Analysis = config.AnalysisConfig{}
	mocked.Notify = config.NotifyConfig{}
	mocked.RunsDir = mockRunsDir(p.RunsDir)
	return &mocked
}

// runOutcome is a finished run.
type runOutcome struct {
	card          *scorecard.Scorecard
	analysis      *analysis.Analysis
	path          string
	interrupted   bool
	notifications []notify.HookSummary
}

// execute sends the plan, scores the answers and saves the run. Ctrl+C stops
// sending; whatever came back is still scored and saved.
func execute(f *rungrad.Factory, p *prepared, parent context.Context) (*runOutcome, error) {
	if err := run.CheckBudget(p.plan); err != nil {
		return nil, usageError(err)
	}
	if !p.evaluation.EvaluationReady {
		return nil, usageError(errors.New(readinessMessage(p.evaluation.ReadinessIssues)))
	}
	var judge engine.Engine
	if p.evaluation.ModelAssisted.Enabled && p.evaluation.ModelAssisted.PlannedCandidates > 0 {
		if _, err := p.project.ResolveKeys(config.KeySelection{IncludeJudge: true}, lookupEnv(f), p.secrets.register); err != nil {
			return nil, rungrad.NewError(rungrad.ExitAuth, keyHint(err))
		}
		j, err := p.factory.NewJudgeEngine(p.project.Analysis.Judge)
		if err != nil {
			return nil, err
		}
		judge = j
	}
	if ids := engineIDsInPlan(p.plan); len(ids) > 0 {
		if _, err := p.project.ResolveKeys(config.KeySelection{EngineIDs: ids}, lookupEnv(f), p.secrets.register); err != nil {
			return nil, rungrad.NewError(rungrad.ExitAuth, keyHint(err))
		}
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	progress := newProgress(f, len(p.plan.WorkUnits), p.quiet)
	raw, err := run.Execute(ctx, p.plan, p.engines, run.ExecuteOptions{Progress: progress.update})
	progress.done()
	if err != nil {
		return nil, usageError(err)
	}
	stop()
	if raw.Interrupted {
		f.Infof("Stopped. Saving the %d answers that came back.", raw.CompletedWorkUnits)
		judge = nil
	}
	card, err := scorecard.New(scorecard.Input{Project: p.project, Plan: p.plan, Suites: p.bound, Engines: p.engines, Raw: raw, Now: p.now})
	if err != nil {
		return nil, err
	}
	card.Narrowed = p.narrowed
	card.Mock = p.mock
	a, err := analysis.Analyze(card, analysis.Options{Project: p.project, Judge: judge, Context: context.Background(), Redact: p.secrets.redact})
	if err != nil {
		return nil, err
	}
	card.Judgments = a.Judgments
	card.Headline = headline(a.Visibility)
	card.Gates = gate.Evaluate(p.gates, a)
	path, err := store.New(p.project.RunsDir).Save(card)
	if err != nil {
		return nil, err
	}
	out := &runOutcome{card: card, analysis: a, path: path, interrupted: raw.Interrupted}
	if gate.Failed(card.Gates) {
		out.notifications = notify.Deliver(context.Background(), notify.Config{Command: p.project.Notify.Command, WebhookURLEnv: p.project.Notify.WebhookURLEnv}, card, notify.Options{
			WorkingDir:     p.configDir,
			LookupEnv:      lookupEnv(f),
			RegisterSecret: f.RegisterSecret,
		})
	}
	return out, nil
}

// exitFor turns a finished run into an exit code: 130 when it was stopped, 7
// when a --fail-under check failed, and 2 when requests failed or were not
// sent.
// Unfinished answers do not change the exit code: the provider answered, the
// answers are saved, and the report says they are left out of the numbers.
func exitFor(o *runOutcome) error {
	c := o.card.Counts
	switch {
	case o.interrupted:
		return rungrad.NewError(exitInterrupted, "the run was stopped early; the answers that came back are saved as run "+o.card.ID)
	case gate.Failed(o.card.Gates):
		return rungrad.NewError(exitGateFailed, "a --fail-under check did not pass")
	case c.Answered+c.Unfinished < c.Planned:
		missing := c.Planned - c.Answered - c.Unfinished
		return rungrad.NewError(rungrad.ExitAPI, fmt.Sprintf("%d of %d requests got no answer; see `llmention report %s --answers`", missing, c.Planned, o.card.ID))
	}
	return nil
}

func headline(s *visibility.Summary) []scorecard.Headline {
	if s == nil {
		return nil
	}
	var out []scorecard.Headline
	for _, c := range s.Cells {
		you := c.You()
		h := scorecard.Headline{Engine: c.Engine, Mode: c.Mode, Answers: c.Answers, Eligible: you.Eligible, Mentioned: you.Mentioned,
			MentionRate: you.Rate, ShareOfVoice: you.ShareOfVoice, AvgRank: you.Rank}
		out = append(out, h)
	}
	return out
}

// mockWorld describes the project's market to the mock provider so its
// demo answers name the right brands.
func mockWorld(p *config.Project, seed string) *engine.MockWorld {
	if p == nil || p.Brand == nil {
		return nil
	}
	w := &engine.MockWorld{
		Brand:    engine.MockBrand{Name: p.Brand.Name, Domain: first(p.Brand.Domains)},
		Category: p.Brand.Category,
		People:   p.Brand.People,
		Products: p.Brand.Products,
		Seed:     seed,
	}
	for _, c := range p.Competitors {
		w.Competitors = append(w.Competitors, engine.MockBrand{Name: c.Name, Domain: first(c.Domains)})
	}
	return w
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func entriesInPlan(entries []suite.LoadedSuite, plan *run.Plan) []suite.LoadedSuite {
	var out []suite.LoadedSuite
	for _, e := range entries {
		if slices.ContainsFunc(plan.Suites, func(s run.SuitePlan) bool { return s.ID == e.Suite.ID }) {
			out = append(out, e)
		}
	}
	return out
}

func issuesInPlan(issues []evaluate.ReadinessIssue, plan *run.Plan) []evaluate.ReadinessIssue {
	var out []evaluate.ReadinessIssue
	for _, i := range issues {
		if i.SuiteID == "" || slices.ContainsFunc(plan.Suites, func(s run.SuitePlan) bool { return s.ID == i.SuiteID }) {
			out = append(out, i)
		}
	}
	return out
}

func engineIDsInPlan(plan *run.Plan) []string {
	var ids []string
	for _, m := range plan.EngineModeMatrix {
		if !slices.Contains(ids, m.EngineID) {
			ids = append(ids, m.EngineID)
		}
	}
	return ids
}

func readinessMessage(issues []evaluate.ReadinessIssue) string {
	var msgs []string
	for _, i := range issues {
		if i.Severity != evaluate.ReadinessFatal {
			continue
		}
		where := i.SuiteID
		if i.ItemID != "" {
			where += " / " + i.ItemID
		}
		if where != "" {
			msgs = append(msgs, where+": "+i.Message)
		} else {
			msgs = append(msgs, i.Message)
		}
		if len(msgs) == 3 {
			break
		}
	}
	return "nothing was sent, because the answers could not be scored: " + strings.Join(msgs, "; ")
}

func keyHint(err error) string {
	var missing config.MissingKeyError
	if errors.As(err, &missing) {
		return fmt.Sprintf("%s. Export %s in your shell, for example `export %s=...`, then run again", err.Error(), pluralWord(len(missing.EnvVars), "it", "them"), missing.EnvVars[0])
	}
	return err.Error()
}

func usageError(err error) error {
	var rg *rungrad.Error
	if errors.As(err, &rg) {
		return err
	}
	return rungrad.NewError(rungrad.ExitUsage, err.Error())
}

func lookupEnv(f *rungrad.Factory) func(string) (string, bool) {
	if f.LookupEnv != nil {
		return f.LookupEnv
	}
	return os.LookupEnv
}

// secretRecorder remembers the keys it is given, so they can be removed from
// text sent to the AI grader. It also passes them on to rungrad, which hides
// them in the output.
type secretRecorder struct {
	mu      sync.Mutex
	secrets []string
	forward func(string)
}

func newSecretRecorder(forward func(string)) *secretRecorder {
	return &secretRecorder{forward: forward}
}

func (r *secretRecorder) register(value string) {
	if value == "" {
		return
	}
	r.mu.Lock()
	r.secrets = append(r.secrets, value)
	r.mu.Unlock()
	if r.forward != nil {
		r.forward(value)
	}
}

func (r *secretRecorder) redact(text string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, "[REDACTED]")
	}
	return text
}

// progress shows "Asking: 12 of 40 requests done" on a terminal and prints
// nothing otherwise.
type progress struct {
	w     io.Writer
	total int
	mu    sync.Mutex
	shown bool
}

func newProgress(f *rungrad.Factory, total int, quiet bool) *progress {
	p := &progress{total: total}
	if quiet || (f.Flags != nil && (f.Flags.Quiet || f.Flags.JSON)) {
		return p
	}
	if file, ok := f.Stderr.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		p.w = f.Stderr
	}
	return p
}

func (p *progress) update(done, total int, r run.Result) {
	if p.w == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "\rAsking: %d of %d requests done", done, total)
	p.shown = true
}

func (p *progress) done() {
	if p.w != nil && p.shown {
		fmt.Fprint(p.w, "\r"+strings.Repeat(" ", 48)+"\r")
	}
}
