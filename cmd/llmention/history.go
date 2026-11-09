package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/store"
)

func historyCommand() *rungrad.Command {
	history := &rungrad.Command{
		Use:     "history",
		Short:   "List and export saved runs",
		Long:    "Every run is saved as one JSON file in the history folder (runs_dir in llmention.yaml, .llmention/runs by default).",
		Related: []string{"llmention report"},
		GroupID: "manage",
	}
	history.AddCommand(&rungrad.Command{
		Use:          "list",
		Short:        "List saved runs, newest first",
		Long:         "List saved runs, newest first. Each line shows when the run was made, what it asked and its mention rate over all engines and modes. Files that cannot be read are listed at the end.",
		Examples:     []string{"llmention history list", "llmention history list --json"},
		Related:      []string{"llmention report"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		SupportsMeta: true,
		Args:         noArgs,
		Run:          runHistoryList,
	}, &rungrad.Command{
		Use:         "export <run-id>",
		Short:       "Copy a saved run to a file",
		Long:        "Copy the JSON file of a saved run, for example to attach it to an issue or load it elsewhere. A unique prefix of the run id is enough.",
		Examples:    []string{"llmention history export 20261026-1608 --output run.json", "llmention history export 20261026-1608 --output run.json --dry-run"},
		Related:     []string{"llmention history list"},
		OutputModes: []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON},
		Mutates:     true,
		Args:        oneArg("run id", "llmention history export 20261026-1608 --output run.json", "`llmention history list` shows the run ids."),
		Configure: func(cmd *cobra.Command) {
			cmd.Flags().String("output", "", "File to write")
			cmd.Flags().Bool("force", false, "Overwrite the output file")
		},
		Run: runHistoryExport,
	})
	return history
}

type historyListResult struct {
	Directory string          `json:"directory"`
	Runs      []store.Entry   `json:"runs"`
	Problems  []store.Problem `json:"problems,omitempty"`
}

func runHistoryList(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	dir := runsDir(project)
	entries, problems, err := store.New(dir).List()
	if err != nil {
		return err
	}
	result := historyListResult{Directory: dir, Runs: entries, Problems: problems}
	if result.Runs == nil {
		result.Runs = []store.Entry{}
	}
	return f.WriteOutput(rungrad.Output{Model: result, Human: func(w io.Writer) {
		if len(entries) == 0 {
			fmt.Fprintf(w, "No saved runs in %s yet. Start one with `llmention run`.\n", dir)
		} else {
			tw := newTable(w)
			fmt.Fprintln(tw, "Run\tDate\tSuites\tEngines\tAnswers\tMention rate")
			for _, e := range entries {
				date := e.CreatedAt.Local().Format("2 Jan 2006 15:04")
				answers := fmt.Sprintf("%d of %d", e.Answers, e.Planned)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, date, strings.Join(e.Suites, ", "), strings.Join(e.Engines, ", "), answers, headlineRate(e))
			}
			tw.Flush()
		}
		for _, p := range problems {
			fmt.Fprintf(w, "Could not read %s: %s\n", p.File, p.Message)
		}
	}})
}

// headlineRate is the mention rate saved with the run, over all its engines
// and modes: answers that name the brand out of the answers that count. The
// report counts the mentions again; this number is only for the listing.
func headlineRate(e store.Entry) string {
	mentioned, eligible := 0, 0
	for _, h := range e.Headline {
		mentioned += h.Mentioned
		eligible += h.Eligible
	}
	if eligible == 0 {
		return "–"
	}
	text := fmt.Sprintf("%.0f%%", math.Round(float64(mentioned)/float64(eligible)*100))
	if e.Demo {
		text += " (demo)"
	}
	return text
}

type exportResult struct {
	RunID  string `json:"run_id"`
	Output string `json:"output"`
	Bytes  int    `json:"bytes"`
	DryRun bool   `json:"dry_run"`
}

func runHistoryExport(f *rungrad.Factory, cmd *cobra.Command, args []string) error {
	output, _ := cmd.Flags().GetString("output")
	force, _ := cmd.Flags().GetBool("force")
	if output == "" {
		return rungrad.NewError(rungrad.ExitUsage, "pass --output with the file to write, for example --output run.json")
	}
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	st := store.New(runsDir(project))
	card, err := st.Load(args[0])
	if err != nil {
		return storeError(err)
	}
	source, err := st.Path(card.ID)
	if err != nil {
		return storeError(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil && !force {
		return rungrad.NewError(rungrad.ExitUsage, output+" exists; pass --force to overwrite it")
	}
	result := exportResult{RunID: card.ID, Output: output, Bytes: len(data), DryRun: f.DryRun()}
	if !f.DryRun() {
		if err := config.WriteFileAtomic(output, data, 0o600); err != nil {
			return err
		}
	}
	return f.WriteResult(result, func(w io.Writer) {
		if result.DryRun {
			fmt.Fprintf(w, "Dry run: would copy run %s (%d bytes) to %s.\n", card.ID, len(data), output)
			return
		}
		fmt.Fprintf(w, "Copied run %s to %s.\n", card.ID, output)
	})
}
