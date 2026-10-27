package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/suite"
)

func suitesCommand() *rungrad.Command {
	suites := &rungrad.Command{
		Use:     "suites",
		Short:   "List and check suites",
		Long:    "A suite is a set of prompts with the checks that score their answers. Your prompts in llmention.yaml form the suite \"prompts\". recognition.basic comes with llmention. You can add more suites as YAML files listed under suites.paths in llmention.yaml.",
		Related: []string{"llmention run"},
		GroupID: "manage",
	}
	suites.AddCommand(&rungrad.Command{
		Use:          "list",
		Short:        "List the suites you can run",
		Long:         "List your prompts and the other suites you can run, with where each one comes from.",
		Examples:     []string{"llmention suites list", "llmention suites list --json"},
		Related:      []string{"llmention suites validate", "llmention run"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		SupportsMeta: true,
		Args:         cobra.NoArgs,
		Run:          runSuitesList,
	}, &rungrad.Command{
		Use:         "validate <suite-id-or-file>",
		Short:       "Check a suite and show its prompts",
		Long:        "Load a suite by id or file path, check it and print the prompts it would send.",
		Examples:    []string{"llmention suites validate prompts", "llmention suites validate ./examples/research/factoid.yaml"},
		Related:     []string{"llmention suites list"},
		OutputModes: []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON},
		Args:        cobra.ExactArgs(1),
		Run:         runSuitesValidate,
	})
	return suites
}

type suiteSummary struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Source  string   `json:"source"`
	Path    string   `json:"path,omitempty"`
	Purpose string   `json:"purpose,omitempty"`
	Modes   []string `json:"default_modes"`
	Items   int      `json:"items"`
	Metrics []string `json:"metrics"`
}

func summarize(entry suite.LoadedSuite) suiteSummary {
	s := entry.Suite
	out := suiteSummary{ID: s.ID, Type: s.Type, Source: string(entry.Source), Path: entry.Path, Purpose: s.Purpose, Modes: s.DefaultModes, Items: len(s.Items)}
	for _, m := range s.Metrics {
		out.Metrics = append(out.Metrics, m.ID)
	}
	return out
}

func runSuitesList(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	catalog, err := suite.LoadCatalog(project)
	if err != nil {
		return usageError(err)
	}
	entries := catalog.Entries()
	if project != nil {
		if bound, _, err := evaluate.BindProjectContext(project, entries); err == nil {
			entries = bound
		}
	}
	var out []suiteSummary
	for _, e := range entries {
		out = append(out, summarize(e))
	}
	model := map[string]any{"suites": out}
	return f.WriteOutput(rungrad.Output{Model: model, Human: func(w io.Writer) {
		tw := newTable(w)
		fmt.Fprintln(tw, "Suite\tType\tFrom\tPrompts\tModes")
		for _, s := range out {
			from := sourceText(s)
			var modes []string
			for _, m := range s.Modes {
				modes = append(modes, config.ModeLabel(m))
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", s.ID, s.Type, from, s.Items, strings.Join(modes, ", "))
		}
		tw.Flush()
	}})
}

// sourceText says in words where a suite comes from, for the table.
func sourceText(s suiteSummary) string {
	switch {
	case s.Path != "":
		return s.Path
	case s.Source == string(suite.SourceBuiltIn):
		return "built in"
	case s.Source == string(suite.SourceProject):
		return "llmention.yaml"
	}
	return s.Source
}

type validateResult struct {
	Suite   suiteSummary `json:"suite"`
	Valid   bool         `json:"valid"`
	Prompts []string     `json:"prompts"`
}

func runSuitesValidate(f *rungrad.Factory, cmd *cobra.Command, args []string) error {
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	loaded, err := suite.ResolveTarget(args[0], project)
	if err != nil {
		return usageError(err)
	}
	if project != nil {
		if bound, _, err := evaluate.BindProjectContext(project, []suite.LoadedSuite{loaded}); err == nil {
			loaded = bound[0]
		}
	}
	result := validateResult{Suite: summarize(loaded), Valid: true}
	for _, item := range loaded.Suite.Items {
		variants, err := suite.RenderVariants(loaded.Suite, item)
		if err != nil {
			return usageError(fmt.Errorf("%s: %w", item.ID, err))
		}
		for _, v := range variants {
			text := v.Prompt
			if text == "" {
				text = item.Query + item.Question
			}
			result.Prompts = append(result.Prompts, text)
		}
	}
	return f.WriteResult(result, func(w io.Writer) {
		fmt.Fprintf(w, "%s is valid: %s suite with %d prompts.\n", result.Suite.ID, result.Suite.Type, len(result.Prompts))
		for _, p := range result.Prompts {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	})
}

func newTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 2, 2, ' ', 0) }
