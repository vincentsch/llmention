package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/gate"
	"github.com/vincentsch/llmention/internal/report"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/store"
)

func reportCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "report [run-id] [run-id]",
		Short: "Show what the assistants said and how it changed",
		Long: "Show the summary of a saved run. It says how often each assistant mentions you and your competitors, and which sites the answers cite. " +
			"Without a run id it shows the newest run. The numbers are worked out again from the saved answers with the brand and competitors in llmention.yaml, so you can still compare old runs after you edit the file. " +
			"--html writes a single page you can share. --mock shows the runs made with `llmention run --mock` instead of the real ones.",
		Examples: []string{
			"llmention report",
			"llmention report --answers",
			"llmention report --diff",
			"llmention report --trend",
			"llmention report --html report.html",
			"llmention report --format markdown >> \"$GITHUB_STEP_SUMMARY\"",
			"llmention report 20261026-1608 --details",
			"llmention report --fail-under mention-rate=30%",
			"llmention report --mock --html preview.html",
		},
		Related:      []string{"llmention run", "llmention history list"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		Mutates:      true,
		SupportsMeta: true,
		GroupID:      "measure",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 2 {
				return fmt.Errorf("llmention report takes up to two run ids, but got %d: %s", len(args), strings.Join(args, " "))
			}
			return nil
		},
		Configure: func(cmd *cobra.Command) {
			cmd.Flags().Bool("answers", false, "Print every answer instead of the summary")
			cmd.Flags().Bool("details", false, "Print every metric of the run")
			cmd.Flags().Bool("diff", false, "Compare with the run before it, or compare two run ids")
			cmd.Flags().Bool("trend", false, "Show your mention rate across recent runs")
			cmd.Flags().Int("runs", 12, "How many recent runs --trend and --html include")
			cmd.Flags().String("suite", "", "Only look at runs of this suite (default: your prompts)")
			cmd.Flags().String("format", "text", "Summary format: text or markdown")
			cmd.Flags().String("html", "", "Write the report as one HTML file to this path")
			cmd.Flags().StringArray("fail-under", nil, "Fail with exit code 7 when a number is below a threshold, for example mention-rate=40% (repeatable)")
			cmd.Flags().Bool("mock", false, "Show the runs made with llmention run --mock")
		},
		Run: runReport,
	}
}

func runReport(f *rungrad.Factory, cmd *cobra.Command, args []string) error {
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	mock, _ := cmd.Flags().GetBool("mock")
	dir := runsDir(project)
	if mock {
		dir = mockRunsDir(dir)
	}
	st := store.New(dir)
	suiteID, _ := cmd.Flags().GetString("suite")
	answers, _ := cmd.Flags().GetBool("answers")
	details, _ := cmd.Flags().GetBool("details")
	diff, _ := cmd.Flags().GetBool("diff")
	trend, _ := cmd.Flags().GetBool("trend")
	runs, _ := cmd.Flags().GetInt("runs")
	format, _ := cmd.Flags().GetString("format")
	htmlPath, _ := cmd.Flags().GetString("html")
	failUnder, _ := cmd.Flags().GetStringArray("fail-under")
	if format != "text" && format != "markdown" {
		return rungrad.NewError(rungrad.ExitUsage, "--format must be text or markdown; for a web page use --html report.html")
	}
	if countTrue(answers, details, diff, trend) > 1 {
		return rungrad.NewError(rungrad.ExitUsage, "use only one of --answers, --details, --diff and --trend")
	}
	if len(args) == 2 && !diff {
		return rungrad.NewError(rungrad.ExitUsage, "two run ids only make sense with --diff")
	}
	// These options look at one run. Saying so beats ignoring them, because
	// a CI job with --diff --fail-under would otherwise always pass.
	if (trend || diff) && (htmlPath != "" || len(failUnder) > 0) {
		return rungrad.NewError(rungrad.ExitUsage, "--html and --fail-under look at one run, so they do not work with --trend or --diff; run them as a separate llmention report")
	}
	if format == "markdown" && (answers || details) {
		return rungrad.NewError(rungrad.ExitUsage, "--format markdown works for the summary and with --trend or --diff, not with --answers or --details")
	}
	defaultSuite := suiteID
	if defaultSuite == "" && len(args) == 0 {
		defaultSuite = config.PromptsSuiteID
	}

	if trend {
		t, err := buildTrend(st, project, defaultSuite, runs)
		if err != nil {
			return storeError(err)
		}
		return f.WriteOutput(rungrad.Output{Model: t, Human: func(w io.Writer) {
			if format == "markdown" {
				report.WriteTrendMarkdown(w, t)
			} else {
				report.WriteTrendText(w, t, styleFor(f))
			}
		}})
	}
	if diff {
		return runDiff(f, st, project, args, defaultSuite, format)
	}

	card, err := pickRun(st, args, defaultSuite)
	var nf *store.NotFoundError
	if len(args) == 0 && errors.As(err, &nf) {
		if mock {
			return rungrad.NewError(rungrad.ExitNotFound, "there is no run made with --mock yet; start one with `llmention run --mock`")
		}
		if runs, _, _ := store.New(mockRunsDir(dir)).List(); len(runs) > 0 {
			return rungrad.NewError(rungrad.ExitNotFound, nf.Message+". Runs made with --mock are saved apart, so add --mock to see them")
		}
	}
	if err != nil {
		return storeError(err)
	}
	a, err := analysis.Analyze(card, analysis.Options{Project: project})
	if err != nil {
		return err
	}
	gates, err := gate.ParseAll(failUnder)
	if err != nil {
		return usageError(err)
	}
	if len(gates) > 0 {
		card.Gates = gate.Evaluate(gates, a)
	}
	r := report.Build(a)
	if !details {
		r.Metrics = nil
	}
	if htmlPath != "" || f.DryRun() {
		if t, err := buildTrend(st, project, firstSuite(card), runs); err == nil && len(t.Points) > 1 {
			r.Trend = &t
		}
	}
	if htmlPath != "" {
		if err := writeHTMLFile(f, htmlPath, r); err != nil {
			return err
		}
	}
	err = f.WriteOutput(rungrad.Output{
		Model: r,
		Human: func(w io.Writer) {
			style := styleFor(f)
			switch {
			case htmlPath != "":
				if f.DryRun() {
					fmt.Fprintf(w, "Dry run: would write the report for run %s to %s.\n", card.ID, htmlPath)
				} else {
					fmt.Fprintf(w, "Wrote the report for run %s to %s.\n", card.ID, htmlPath)
				}
			case answers:
				report.WriteAnswers(w, r, style)
			case details:
				report.WriteDetails(w, r)
			case format == "markdown":
				report.WriteMarkdown(w, r)
			default:
				report.WriteText(w, r, style)
			}
		},
	})
	if err != nil {
		return err
	}
	if gate.Failed(card.Gates) && len(gates) > 0 {
		return rungrad.NewError(exitGateFailed, "a --fail-under check did not pass")
	}
	return nil
}

func runDiff(f *rungrad.Factory, st store.Store, project *config.Project, args []string, suiteID, format string) error {
	var oldCard, newCard *scorecard.Scorecard
	var err error
	switch len(args) {
	case 2:
		if oldCard, err = st.Load(args[0]); err == nil {
			newCard, err = st.Load(args[1])
		}
		// The older run is always "before", whichever order the ids came in.
		if err == nil && oldCard.CreatedAt.After(newCard.CreatedAt) {
			oldCard, newCard = newCard, oldCard
		}
	case 1:
		newCard, err = st.Load(args[0])
		if err == nil {
			oldCard, err = runBefore(st, newCard, firstSuite(newCard))
		}
	default:
		if newCard, err = st.Last(suiteID); err == nil {
			oldCard, err = runBefore(st, newCard, suiteID)
		}
	}
	if err != nil {
		var nf *store.NotFoundError
		if errors.As(err, &nf) && newCard != nil {
			return rungrad.NewError(rungrad.ExitNotFound, "there is no earlier run to compare with; run again later, for example next week")
		}
		return storeError(err)
	}
	brands, ok := analysis.ScoringBrands(newCard, project)
	if !ok {
		return rungrad.NewError(rungrad.ExitUsage, "comparisons need a brand; add brand.name to llmention.yaml")
	}
	d := report.BuildDiff(oldCard, newCard, analysis.VisibilityAnswers(oldCard), analysis.VisibilityAnswers(newCard), brands)
	return f.WriteOutput(rungrad.Output{Model: d, Human: func(w io.Writer) {
		if format == "markdown" {
			report.WriteDiffMarkdown(w, d)
		} else {
			report.WriteDiffText(w, d, styleFor(f))
		}
	}})
}

// runBefore finds the run to compare card with: the newest older run of the
// same suite that asked every engine and mode card asked. So a full run is
// never compared with a run made with --engine or --mode.
func runBefore(st store.Store, card *scorecard.Scorecard, suiteID string) (*scorecard.Scorecard, error) {
	entries, _, err := st.List()
	if err != nil {
		return nil, err
	}
	cur := store.EntryFor(card)
	for _, e := range entries {
		if e.ID < card.ID && (suiteID == "" || slices.Contains(e.Suites, suiteID)) && covers(e, cur) {
			return st.Load(e.ID)
		}
	}
	return nil, &store.NotFoundError{Message: "no earlier run"}
}

// covers reports whether run a asked every engine and mode that run b asked.
func covers(a, b store.Entry) bool {
	if a.Narrowed && !b.Narrowed {
		return false
	}
	for _, h := range b.Headline {
		if !slices.ContainsFunc(a.Headline, func(o scorecard.Headline) bool { return o.Engine == h.Engine && o.Mode == h.Mode }) {
			return false
		}
	}
	return true
}

func buildTrend(st store.Store, project *config.Project, suiteID string, limit int) (report.Trend, error) {
	entries, _, err := st.List()
	if err != nil {
		return report.Trend{}, err
	}
	var picked []store.Entry
	leftOut := 0
	for _, e := range entries {
		if suiteID != "" && !slices.Contains(e.Suites, suiteID) {
			continue
		}
		if e.Narrowed {
			leftOut++
			continue
		}
		picked = append(picked, e)
		if limit > 0 && len(picked) == limit {
			break
		}
	}
	slices.Reverse(picked)
	var runs []report.TrendRun
	brand := ""
	for _, e := range picked {
		card, err := st.Load(e.ID)
		if err != nil {
			continue
		}
		summary, brands := analysis.Visibility(card, project)
		if summary == nil {
			continue
		}
		brand = brands.You.Name
		runs = append(runs, report.TrendRun{Card: card, Summary: summary})
	}
	t := report.BuildTrend(brand, runs)
	t.LeftOut = leftOut
	return t, nil
}

func pickRun(st store.Store, args []string, suiteID string) (*scorecard.Scorecard, error) {
	if len(args) == 1 {
		return st.Load(args[0])
	}
	card, err := st.Last(suiteID)
	var nf *store.NotFoundError
	if errors.As(err, &nf) && suiteID == config.PromptsSuiteID {
		// No run of your prompts yet; fall back to the newest run of any suite.
		return st.Last("")
	}
	return card, err
}

func writeHTMLFile(f *rungrad.Factory, path string, r report.Report) error {
	if f.DryRun() {
		return nil
	}
	var buf bytes.Buffer
	if err := report.WriteHTML(&buf, r); err != nil {
		return err
	}
	// The page holds every answer, so it is private like the saved runs.
	if err := config.WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func firstSuite(card *scorecard.Scorecard) string {
	if card.HasSuite(config.PromptsSuiteID) || len(card.Suites) == 0 {
		return config.PromptsSuiteID
	}
	return card.Suites[0].ID
}

// loadOptionalProject reads llmention.yaml when there is one. Commands that
// only read history still work without it.
func loadOptionalProject(cmd *cobra.Command, f *rungrad.Factory) (*config.Project, error) {
	path, err := config.ProjectConfigPath(cmd, f)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) && !configFlagChanged(cmd) {
		return nil, nil
	}
	project, err := config.Load(path)
	if err != nil {
		return nil, usageError(err)
	}
	return project, nil
}

func runsDir(p *config.Project) string {
	if p == nil {
		return config.DefaultRunsDir
	}
	return p.RunsDir
}

func configFlagChanged(cmd *cobra.Command) bool {
	flag := cmd.Root().PersistentFlags().Lookup("config")
	return flag != nil && flag.Changed
}

func storeError(err error) error {
	var nf *store.NotFoundError
	if errors.As(err, &nf) {
		return rungrad.NewError(rungrad.ExitNotFound, nf.Message)
	}
	if strings.Contains(err.Error(), "matches") || strings.Contains(err.Error(), "is not a run id") {
		return rungrad.NewError(rungrad.ExitUsage, err.Error())
	}
	return err
}

func countTrue(values ...bool) int {
	n := 0
	for _, v := range values {
		if v {
			n++
		}
	}
	return n
}
