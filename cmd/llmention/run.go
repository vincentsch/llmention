package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/cost"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/notify"
	"github.com/vincentsch/llmention/internal/report"
	"github.com/vincentsch/llmention/internal/run"
)

func runCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "run",
		Short: "Ask the assistants your prompts and save the answers",
		Long: "Send the prompts in llmention.yaml to each engine in each mode, then save the answers as a run and print the summary. " +
			"Use --dry-run first. It sends nothing and shows how many requests the run would send and roughly what they cost, in total and for each engine. It also says which API keys are set. " +
			"Ctrl+C stops the run and saves the answers that already came back. " +
			"With --mock, the built-in mock engine makes up the answers for every engine, so you see the report for your own product without keys or cost. " +
			"Those runs are saved apart from real runs, and `llmention report --mock` shows them.",
		Examples: []string{
			"llmention run --dry-run",
			"llmention run",
			"llmention run --mock",
			"llmention run --engine chatgpt --mode search",
			"llmention run --fail-under mention-rate=40%",
			"llmention run --suite recognition.basic",
			"llmention run --json",
		},
		Related:      []string{"llmention report", "llmention init"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		Mutates:      true,
		SupportsMeta: true,
		GroupID:      "measure",
		Args:         noArgs,
		Configure: func(cmd *cobra.Command) {
			addRunFlags(cmd)
			cmd.Flags().StringArray("fail-under", nil, "Fail with exit code 7 when a number is below a threshold, for example mention-rate=40% (repeatable)")
		},
		Run: func(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
			path, err := config.ProjectConfigPath(cmd, f)
			if err != nil {
				return err
			}
			project, err := config.Load(path)
			if err != nil {
				return usageError(err)
			}
			opts, err := runOptionsFromFlags(cmd)
			if err != nil {
				return usageError(err)
			}
			return runAndReport(f, cmd, project, path, opts)
		},
	}
}

func addRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringSlice("engine", nil, "Only ask these engines (ids from llmention.yaml, comma-separated)")
	cmd.Flags().StringSlice("mode", nil, "Only use these modes (no-search, search, forced-search, default)")
	cmd.Flags().StringSlice("suite", nil, "Run these suites instead of your prompts, for example recognition.basic")
	cmd.Flags().Int("repetitions", 0, "Ask each prompt this many times (default from llmention.yaml)")
	cmd.Flags().Int("max-requests", 0, "The most requests this run may send, retries included (default execution.max_requests)")
	cmd.Flags().Bool("judge", false, "Use the AI grader set up under analysis.judge")
	cmd.Flags().Bool("mock", false, "Let the mock engine make up every answer, and save the run apart from real runs")
}

func runOptionsFromFlags(cmd *cobra.Command) (runOptions, error) {
	var o runOptions
	o.EngineIDs, _ = cmd.Flags().GetStringSlice("engine")
	o.Modes, _ = cmd.Flags().GetStringSlice("mode")
	o.SuiteIDs, _ = cmd.Flags().GetStringSlice("suite")
	o.Repetitions, _ = cmd.Flags().GetInt("repetitions")
	o.MaxRequests, _ = cmd.Flags().GetInt("max-requests")
	if f := cmd.Flags().Lookup("fail-under"); f != nil {
		o.FailUnder, _ = cmd.Flags().GetStringArray("fail-under")
	}
	if o.Repetitions < 0 || o.MaxRequests < 0 {
		return o, fmt.Errorf("--repetitions and --max-requests cannot be negative")
	}
	o.Mock, _ = cmd.Flags().GetBool("mock")
	if cmd.Flags().Changed("judge") {
		on, _ := cmd.Flags().GetBool("judge")
		if on && o.Mock {
			return o, fmt.Errorf("--judge does not work with --mock, because the AI grader is a real model that needs a key")
		}
		o.ModelAssisted = &on
	}
	return o, nil
}

// runAndReport is shared by run, quick and demo: plan, preview or send,
// save, print the summary.
func runAndReport(f *rungrad.Factory, cmd *cobra.Command, project *config.Project, path string, opts runOptions) error {
	p, err := prepareRun(f, project, path, opts)
	if err != nil {
		return err
	}
	if f.DryRun() {
		return writePlan(f, p)
	}
	outcome, err := execute(f, p, cmd.Context())
	if err != nil {
		return err
	}
	r := report.Build(outcome.analysis)
	r.Metrics = nil // `llmention report --details --json` has the full table
	result := runResult{RunID: outcome.card.ID, SavedTo: outcome.path, Report: r, Notifications: outcome.notifications}
	if err := f.WriteOutput(rungrad.Output{
		Model: result,
		Human: func(w io.Writer) {
			report.WriteText(w, r, styleFor(f))
			fmt.Fprintf(w, "Saved as run %s in %s.\n", outcome.card.ID, displayDir(p.project.RunsDir))
			for _, n := range outcome.notifications {
				if n.Status == notify.StatusFailed {
					fmt.Fprintf(w, "The alert did not go out: %s.\n", notify.Describe(n.ReasonCode))
				}
			}
		},
	}); err != nil {
		return err
	}
	return exitFor(outcome)
}

type runResult struct {
	RunID         string               `json:"run_id"`
	SavedTo       string               `json:"saved_to"`
	Report        report.Report        `json:"report"`
	Notifications []notify.HookSummary `json:"notifications,omitempty"`
}

// planPreview is what --dry-run prints.
type planPreview struct {
	DryRun          bool            `json:"dry_run"`
	Requests        int             `json:"requests"`
	MaxRequests     int             `json:"max_requests"`
	WorstCase       int             `json:"requests_if_every_retry_is_used"`
	Prompts         int             `json:"prompts"`
	Engines         []enginePreview `json:"engines"`
	Modes           []string        `json:"modes"`
	Suites          []string        `json:"suites"`
	EstimatedCost   cost.Estimate   `json:"estimated_cost"`
	PricesCheckedOn string          `json:"prices_checked_on"`
	// KeysFound and KeysMissing split the key variables the run needs. The
	// real run stops with exit code 3 while a key is missing, but the dry run
	// still exits 0, so scripts check keys_missing.
	KeysFound     []string `json:"keys_found"`
	KeysMissing   []string `json:"keys_missing"`
	Skipped       []string `json:"skipped,omitempty"`
	Checks        []string `json:"checks,omitempty"`
	JudgeRequests int      `json:"judge_requests,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	// Problems stop the real run. It is always a list, empty when there are
	// none.
	Problems   []string `json:"problems"`
	FitsBudget bool     `json:"fits_budget"`
}

type enginePreview struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Requests int    `json:"requests"`
}

func writePlan(f *rungrad.Factory, p *prepared) error {
	pv := planPreview{
		DryRun:          true,
		Requests:        len(p.plan.WorkUnits),
		MaxRequests:     p.plan.MaxRequests,
		WorstCase:       p.plan.MaxRequestsWithRetries,
		EstimatedCost:   p.estimate,
		PricesCheckedOn: cost.PricesCheckedOn,
		FitsBudget:      len(p.plan.WorkUnits) <= p.plan.MaxRequests,
		KeysFound:       []string{},
		KeysMissing:     []string{},
		Problems:        []string{},
	}
	prompts := map[string]bool{}
	for _, u := range p.plan.WorkUnits {
		prompts[u.SuiteID+"/"+u.ItemID] = true
	}
	pv.Prompts = len(prompts)
	for _, s := range p.plan.Suites {
		pv.Suites = append(pv.Suites, s.ID)
		for _, m := range s.Modes {
			if !slices.Contains(pv.Modes, m) {
				pv.Modes = append(pv.Modes, m)
			}
		}
	}
	for _, e := range p.engines {
		n := 0
		for _, u := range p.plan.WorkUnits {
			if u.EngineID == e.ID() {
				n++
			}
		}
		if n > 0 || slices.ContainsFunc(p.plan.UnsupportedPairs, func(u run.UnsupportedPair) bool { return u.EngineID == e.ID() }) {
			pv.Engines = append(pv.Engines, enginePreview{ID: e.ID(), Provider: e.Provider(), Model: e.Model(), Requests: n})
		}
	}
	keys := slices.Clone(p.plan.RequiredKeyEnv)
	// The run reads the AI grader's key only when the grader has work.
	graded := p.evaluation.ModelAssisted.Enabled && p.evaluation.ModelAssisted.PlannedCandidates > 0
	if judge := p.project.Analysis.Judge; graded && judge != nil && judge.KeyEnv != "" && !slices.Contains(keys, judge.KeyEnv) {
		keys = append(keys, judge.KeyEnv)
	}
	for _, env := range keys {
		if v, ok := lookupEnv(f)(env); ok && v != "" {
			pv.KeysFound = append(pv.KeysFound, env)
		} else {
			pv.KeysMissing = append(pv.KeysMissing, env)
		}
	}
	for _, u := range p.plan.UnsupportedPairs {
		pv.Skipped = append(pv.Skipped, fmt.Sprintf("%s · %s: %s", u.EngineID, config.ModeLabel(u.Mode), u.Reason))
	}
	slices.Sort(pv.Skipped)
	pv.Skipped = slices.Compact(pv.Skipped)
	for _, g := range p.gates {
		pv.Checks = append(pv.Checks, g.Raw)
	}
	if p.evaluation.ModelAssisted.Enabled {
		pv.JudgeRequests = p.evaluation.ModelAssisted.PlannedCandidates
	}
	// Problems stop the real run, so the dry run names them as such instead
	// of as notes.
	for _, issue := range p.evaluation.ReadinessIssues {
		if issue.Severity == evaluate.ReadinessFatal {
			pv.Problems = append(pv.Problems, issue.Message)
		} else {
			pv.Warnings = append(pv.Warnings, issue.Message)
		}
	}
	if note := retryRoomNote(pv); note != "" {
		pv.Warnings = append(pv.Warnings, note)
	}
	slices.Sort(pv.Warnings)
	pv.Warnings = slices.Compact(pv.Warnings)
	slices.Sort(pv.Problems)
	pv.Problems = slices.Compact(pv.Problems)
	return f.WriteOutput(rungrad.Output{Model: pv, Human: func(w io.Writer) { writePlanText(w, pv) }})
}

// retryRoomNote gives the budget that covers every retry when the current
// one doesn't. Retries use the same budget as first attempts, so when many
// requests fail, the last requests of the run are never sent.
func retryRoomNote(pv planPreview) string {
	if !pv.FitsBudget || pv.WorstCase <= pv.MaxRequests {
		return ""
	}
	room := pv.MaxRequests - pv.Requests
	left := "no room for retries"
	if room > 0 {
		left = fmt.Sprintf("room for only %d %s", room, pluralWord(room, "retry", "retries"))
	}
	return fmt.Sprintf("the budget of %d requests leaves %s. If the run needs more retries than that, the last requests of the run are not sent. "+
		"To allow every retry, set execution.max_requests to %d or pass --max-requests %d.", pv.MaxRequests, left, pv.WorstCase, pv.WorstCase)
}

func writePlanText(w io.Writer, pv planPreview) {
	var modes []string
	for _, m := range pv.Modes {
		modes = append(modes, config.ModeLabel(m))
	}
	fmt.Fprintln(w, "Dry run: nothing was sent.")
	fmt.Fprintf(w, "Would send %d %s: %d %s to %d %s, in %s %s.\n",
		pv.Requests, pluralWord(pv.Requests, "request", "requests"), pv.Prompts, pluralWord(pv.Prompts, "prompt", "prompts"), len(pv.Engines), pluralWord(len(pv.Engines), "engine", "engines"),
		config.JoinAnd(modes), pluralWord(len(modes), "mode", "modes"))
	if pv.WorstCase > pv.Requests {
		if pv.MaxRequests < pv.WorstCase {
			fmt.Fprintf(w, "Failed requests are retried. The run stops at %d requests, its budget.\n", pv.MaxRequests)
		} else {
			fmt.Fprintf(w, "Failed requests are retried, so the run sends at most %d requests.\n", pv.WorstCase)
		}
	}
	switch {
	case pv.EstimatedCost.Free:
		fmt.Fprintln(w, "Estimated cost: nothing, because these engines are free.")
	case pv.EstimatedCost.Dollars == 0:
		fmt.Fprintf(w, "Estimated cost: %s.\n", pv.EstimatedCost)
	default:
		yearly := cost.Estimate{Dollars: 52 * pv.EstimatedCost.Dollars}
		fmt.Fprintf(w, "Estimated cost: %s, from list prices checked on %s. A run every week comes to %s a year. Longer answers and extra web searches can make it higher.\n", pv.EstimatedCost, pv.PricesCheckedOn, yearly)
	}
	for _, line := range pv.EstimatedCost.LeftOut() {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
	costs := map[string]cost.EngineEstimate{}
	for _, e := range pv.EstimatedCost.Engines {
		costs[e.ID] = e
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  Engine\tModel\tRequests\tEstimated cost")
	for _, e := range pv.Engines {
		estimate := "nothing"
		if c, ok := costs[e.ID]; ok {
			estimate = c.String()
		}
		fmt.Fprintf(tw, "  %s\t%s %s\t%d\t%s\n", e.ID, e.Provider, e.Model, e.Requests, estimate)
	}
	tw.Flush()
	if len(pv.KeysFound) > 0 || len(pv.KeysMissing) > 0 || len(pv.Skipped) > 0 || len(pv.Checks) > 0 || pv.JudgeRequests > 0 || len(pv.Warnings) > 0 {
		fmt.Fprintln(w)
	}
	if len(pv.KeysFound) > 0 {
		fmt.Fprintf(w, "Keys found: %s\n", strings.Join(pv.KeysFound, ", "))
	}
	if missing := pv.KeysMissing; len(missing) > 0 {
		fmt.Fprintf(w, "Keys missing: %s. Export %s before you run, for example `export %s=...`.\n", strings.Join(missing, ", "), pluralWord(len(missing), "it", "them"), missing[0])
	}
	for _, s := range pv.Skipped {
		fmt.Fprintf(w, "Left out: %s\n", s)
	}
	for _, c := range pv.Checks {
		fmt.Fprintf(w, "Check after the run: %s\n", c)
	}
	if pv.JudgeRequests > 0 {
		fmt.Fprintf(w, "The AI grader would get up to %d %s.\n", pv.JudgeRequests, pluralWord(pv.JudgeRequests, "request", "requests"))
	}
	for _, warn := range pv.Warnings {
		fmt.Fprintf(w, "Note: %s.\n", strings.TrimSuffix(warn, "."))
	}
	if !pv.FitsBudget {
		fmt.Fprintf(w, "\nThis run needs %d requests but the budget is %d. Raise execution.max_requests or pass --max-requests %d.\n", pv.Requests, pv.MaxRequests, pv.Requests)
	}
	for _, problem := range pv.Problems {
		fmt.Fprintf(w, "\nThe run would not start: %s.\n", problem)
	}
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func styleFor(f *rungrad.Factory) report.Style {
	return report.Style{ANSI: f.TerminalMode().ANSI}
}

func displayDir(dir string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return dir
}
