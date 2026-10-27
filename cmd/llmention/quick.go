package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/config"
)

func quickCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "quick",
		Short: "Write llmention.yaml and run it in one step",
		Long: "Write llmention.yaml from a few flags, then ask the assistants and print the summary. " +
			"When llmention.yaml already exists and you pass no --brand, quick runs it as it is. " +
			"Use --dry-run to see the requests and the estimated cost first.",
		Examples: []string{
			"llmention quick --brand Tabslip --category \"AI API cost tracking\" --competitors \"Helicone,Langfuse\" --engines openai,anthropic --dry-run",
			"llmention quick --brand Tabslip --category \"AI API cost tracking\" --competitors \"Helicone,Langfuse\" --engines openai,anthropic",
			"llmention quick --brand Acme --category \"invoicing for freelancers\" --engines mock",
		},
		Related:      []string{"llmention init", "llmention run", "llmention demo"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		Mutates:      true,
		SupportsMeta: true,
		GroupID:      "start",
		Args:         cobra.NoArgs,
		Configure: func(cmd *cobra.Command) {
			addStarterFlags(cmd)
			cmd.Flags().Bool("force", false, "Replace an existing llmention.yaml")
		},
		Run: func(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
			path, err := config.ProjectConfigPath(cmd, f)
			if err != nil {
				return err
			}
			starter, err := starterFromFlags(f, cmd)
			if err != nil {
				return usageError(err)
			}
			force, _ := cmd.Flags().GetBool("force")
			_, statErr := os.Stat(path)
			exists := statErr == nil
			var project *config.Project
			switch {
			case starter.Brand == "" && exists:
				if project, err = config.Load(path); err != nil {
					return usageError(err)
				}
			case starter.Brand == "":
				return rungrad.NewError(rungrad.ExitUsage, "pass --brand, for example: llmention quick --brand Acme --category \"invoicing for freelancers\" --competitors \"Globex,Initech\"")
			case exists && !force:
				return rungrad.NewError(rungrad.ExitUsage, fmt.Sprintf("%s already exists; run `llmention run`, or pass --force to replace it", path))
			case starter.Category == "":
				// Without a category there are no starter questions, and the
				// run would fail right after writing the file.
				return rungrad.NewError(rungrad.ExitUsage, "pass --category with what your product helps with, for example --category \"invoicing for freelancers\"; quick builds the questions from it")
			default:
				if project, err = config.StarterProject(starter); err != nil {
					return usageError(err)
				}
				// Check the keys before writing anything, so the same quick
				// command works once they are exported.
				if !f.DryRun() {
					if _, err := project.ResolveKeys(config.KeySelection{}, lookupEnv(f), f.RegisterSecret); err != nil {
						return rungrad.NewError(rungrad.ExitAuth, keyHint(err))
					}
				}
				if !f.DryRun() {
					if err := config.WriteFileAtomic(path, config.StarterYAML(project), 0o644); err != nil {
						return err
					}
					f.Infof("Wrote %s. You can edit its prompts at any time, and `llmention run` sends them again.", path)
				}
				// Read it back so paths resolve the same way as in later runs.
				if !f.DryRun() {
					if project, err = config.Load(path); err != nil {
						return err
					}
				}
			}
			return runAndReport(f, cmd, project, path, runOptions{})
		},
	}
}
