package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/report"
	"github.com/vincentsch/llmention/internal/store"
)

// The demo market uses well-known placeholder company names, so nobody
// mistakes the made-up answers for facts about a real product.
var demoStarter = config.Starter{
	Brand:    "Acme Books",
	Category: "invoicing for freelancers",
	Domains:  []string{"acme-books.example"},
	Competitors: []config.CompetitorConfig{
		{Name: "Globex Billing", Domains: []string{"globex.example"}},
		{Name: "Initech Invoices", Domains: []string{"initech.example"}},
	},
}

// demoWeeks raises the demo brand's visibility a little each week, so the
// trend has something to show.
var demoWeeks = []float64{-0.15, -0.05, 0.05, 0.15}

// demoAffinity makes one demo assistant name the demo brand often and the
// other rarely, so the report has a gap to show.
var demoAffinity = map[string]float64{"chatgpt-demo": 0.55, "claude-demo": 0.1}

func demoCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "demo",
		Short: "Try llmention without API keys",
		Long: "Make up a market with one brand and two competitors, and let two demo assistants answer once a week for four weeks. " +
			"The answers come from the built-in mock engine, and nothing is sent anywhere. The demo prints the same summary and trend you get from a real run. " +
			"Pass --keep DIR to save the project and its history, so you can try the other commands on them.",
		Examples: []string{
			"llmention demo",
			"llmention demo --keep ./llmention-demo",
			"llmention demo --json",
		},
		Related:     []string{"llmention quick", "llmention init"},
		OutputModes: []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON},
		GroupID:     "start",
		Args:        noArgs,
		Configure: func(cmd *cobra.Command) {
			cmd.Flags().String("keep", "", "Save the demo project and its history in this folder")
		},
		Run: runDemo,
	}
}

type demoResult struct {
	Directory string        `json:"directory,omitempty"`
	Report    report.Report `json:"report"`
	Trend     report.Trend  `json:"trend"`
}

// shellQuote quotes a path for a shell command line when it needs it.
func shellQuote(path string) string {
	if strings.ContainsAny(path, " '\"$`\\") {
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return path
}

func runDemo(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	keep, _ := cmd.Flags().GetString("keep")
	dir := keep
	if dir == "" {
		tmp, err := os.MkdirTemp("", "llmention-demo-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		dir = tmp
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "llmention.yaml")
	if _, err := os.Stat(path); err == nil {
		return rungrad.NewError(rungrad.ExitUsage, path+" already exists; pick an empty folder for --keep")
	}
	starter := demoStarter
	starter.Providers = []string{"mock"}
	project, err := config.StarterProject(starter)
	if err != nil {
		return err
	}
	project.Engines = []config.EngineConfig{
		{ID: "chatgpt-demo", Provider: "mock", Model: "demo"},
		{ID: "claude-demo", Provider: "mock", Model: "demo"},
	}
	if err := config.WriteFileAtomic(path, config.StarterYAML(project), 0o644); err != nil {
		return err
	}
	if project, err = config.Load(path); err != nil {
		return err
	}
	now := time.Now()
	for week, boost := range demoWeeks {
		world := mockWorld(project, fmt.Sprintf("week-%d", week+1))
		world.Boost = boost
		world.Affinity = demoAffinity
		opts := runOptions{World: world, Now: now.AddDate(0, 0, 7*(week-len(demoWeeks)+1)), Quiet: true}
		p, err := prepareRun(f, project, path, opts)
		if err != nil {
			return err
		}
		if _, err := execute(f, p, cmd.Context()); err != nil {
			return err
		}
	}
	st := store.New(project.RunsDir)
	card, err := st.Last(config.PromptsSuiteID)
	if err != nil {
		return err
	}
	a, err := analysis.Analyze(card, analysis.Options{Project: project})
	if err != nil {
		return err
	}
	r := report.Build(a)
	trend, err := buildTrend(st, project, config.PromptsSuiteID, len(demoWeeks))
	if err != nil {
		return err
	}
	result := demoResult{Report: r, Trend: trend}
	if keep != "" {
		result.Directory = dir
	}
	return f.WriteResult(result, func(w io.Writer) {
		style := styleFor(f)
		report.WriteSummary(w, r, style)
		fmt.Fprintln(w)
		report.WriteTrendText(w, trend, style)
		fmt.Fprintln(w)
		if keep != "" {
			fmt.Fprintf(w, "The demo project and its four runs are in %s. Try:\n", dir)
			fmt.Fprintf(w, "  cd %s\n  llmention report --answers\n  llmention report --html report.html\n  llmention report --diff\n", shellQuote(dir))
		} else {
			fmt.Fprintln(w, "This was a demo with made-up answers; nothing was saved. To save it and read every answer:")
			fmt.Fprintln(w, "  llmention demo --keep ./llmention-demo")
		}
		fmt.Fprintln(w, "\nTo measure your own product, fill in this command. With --dry-run it only shows the plan:")
		fmt.Fprintln(w, `  llmention quick --brand "Your Product" --category "what it helps with" --competitors "Rival One, Rival Two" --dry-run`)
	})
}
