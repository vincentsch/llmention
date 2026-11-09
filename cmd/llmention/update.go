package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"
	rgupdate "github.com/vincentsch/rungrad/update"

	"github.com/vincentsch/llmention/internal/version"
)

var currentVersion = func() string { return version.Version }

var newReleaseFetcher = func() rgupdate.Fetcher {
	return rgupdate.GitHubFetcher{Owner: "vincentsch", Repo: "llmention"}
}

func updateCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:         "update",
		Short:       "Check for a newer llmention release",
		Long:        "Compare the installed version with the latest release on GitHub. It never changes the installed program. To update, run the install script again or use `go install`.",
		Examples:    []string{"llmention update --check", "llmention update --check --json"},
		Related:     []string{"llmention --version"},
		OutputModes: []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		GroupID:     "manage",
		Args:        noArgs,
		Configure: func(cmd *cobra.Command) {
			cmd.Flags().Bool("check", false, "Check for an update without installing it")
		},
		Run: runUpdate,
	}
}

func runUpdate(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	check, err := cmd.Flags().GetBool("check")
	if err != nil {
		return err
	}
	if !check {
		return rungrad.NewError(rungrad.ExitUsage, "update needs --check, because llmention does not install updates itself; run the install script again to update")
	}
	current := currentVersion()
	result, err := updateResult(current)
	if err != nil {
		return err
	}
	return f.WriteResult(result, func(w io.Writer) { fmt.Fprintln(w, updateText(result)) })
}

// updateText says what an update check found, in a sentence.
func updateText(r rgupdate.Result) string {
	switch r.Status {
	case rgupdate.StatusUpToDate:
		return fmt.Sprintf("llmention %s is the latest release.", r.Current)
	case rgupdate.StatusUpdateAvailable:
		return fmt.Sprintf("llmention %s is out; you have %s. To update, run the install script again or use go install.", r.Latest, r.Current)
	case rgupdate.StatusDevelopmentBuild:
		return fmt.Sprintf("This is a development build (%s), so there is no release to compare it with.", r.Current)
	case rgupdate.StatusNewerThanLatest:
		return fmt.Sprintf("You have llmention %s, which is newer than the latest release (%s).", r.Current, r.Latest)
	}
	return fmt.Sprintf("You have llmention %s. llmention could not tell which release is the latest.", r.Current)
}

func updateResult(current string) (rgupdate.Result, error) {
	if developmentBuild(current) {
		return rgupdate.Result{
			Current:   current,
			Latest:    "",
			Available: false,
			Status:    rgupdate.StatusDevelopmentBuild,
		}, nil
	}
	fetcher := newReleaseFetcher()
	if fetcher == nil {
		return rgupdate.Result{}, rungrad.NewError(rungrad.ExitAPI, "no release fetcher configured")
	}
	latest, err := fetcher.Latest()
	if err != nil {
		return rgupdate.Result{}, rungrad.NewError(rungrad.ExitAPI, "could not get the latest release from GitHub: "+err.Error())
	}
	return rgupdate.Evaluate(current, latest), nil
}

func developmentBuild(current string) bool {
	value := strings.TrimSpace(strings.ToLower(current))
	return value == "" || value == "dev" || strings.Contains(value, "-dev") || strings.Contains(value, "+dev")
}
