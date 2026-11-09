package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/config"
)

func initCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "init",
		Short: "Write llmention.yaml for your product",
		Long: "Ask a few questions about your product and your competitors, then write llmention.yaml with starter buyer questions you can edit. " +
			"If you pass the answers as flags, init asks nothing.",
		Examples: []string{
			"llmention init",
			"llmention init --brand Tabslip --category \"AI API cost tracking\" --competitors \"Helicone (helicone.ai), Langfuse\" --engines openai,anthropic",
			"llmention init --dry-run --brand Acme",
		},
		Related:     []string{"llmention run", "llmention quick"},
		OutputModes: []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON},
		Mutates:     true,
		GroupID:     "start",
		Args:        noArgs,
		Configure: func(cmd *cobra.Command) {
			addStarterFlags(cmd)
			cmd.Flags().Bool("force", false, "Overwrite an existing llmention.yaml")
		},
		Run: runInit,
	}
}

func addStarterFlags(cmd *cobra.Command) {
	cmd.Flags().String("brand", "", "Your product or company name")
	cmd.Flags().String("category", "", "What it helps with in a few words, used in the starter prompts")
	cmd.Flags().StringSlice("domain", nil, "Your website domains (optional)")
	cmd.Flags().String("competitors", "", "Competitors, comma-separated; add a site in brackets: \"Helicone (helicone.ai), Langfuse\"")
	cmd.Flags().StringSlice("engines", nil, "Providers to ask: openai, anthropic, google, perplexity, openrouter (all four through one key) or mock (default: the ones with a key in your environment)")
}

type initResult struct {
	Path    string   `json:"path"`
	DryRun  bool     `json:"dry_run"`
	Engines []string `json:"engines"`
	Prompts int      `json:"prompts"`
	Keys    []string `json:"keys_needed"`
	// Warnings are short notes about the setup, such as engines that have
	// had fewer checks with real requests.
	Warnings []string `json:"warnings,omitempty"`
	YAML     string   `json:"yaml,omitempty"`
}

func runInit(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	path, err := config.ProjectConfigPath(cmd, f)
	if err != nil {
		return err
	}
	force, _ := cmd.Flags().GetBool("force")
	if _, err := os.Stat(path); err == nil && !force && !f.DryRun() {
		return rungrad.NewError(rungrad.ExitUsage, fmt.Sprintf("%s already exists; edit it, or pass --force to replace it", path))
	}
	starter, err := starterFromFlags(f, cmd)
	if err != nil {
		return usageError(err)
	}
	if starter.Brand == "" {
		if !f.CanPrompt() {
			return rungrad.NewError(rungrad.ExitUsage, "pass --brand, best with --category and --competitors, or run init in a terminal and answer its questions")
		}
		if err := askStarter(f, &starter); err != nil {
			return err
		}
	}
	project, err := config.StarterProject(starter)
	if err != nil {
		return usageError(err)
	}
	data := config.StarterYAML(project)
	result := initResult{Path: path, DryRun: f.DryRun(), Prompts: len(project.Prompts)}
	for _, e := range project.Engines {
		result.Engines = append(result.Engines, e.ID+" ("+e.Model+")")
	}
	// Engines can share a key, like the four OpenRouter engines, so take the
	// list from the project, which names each variable once.
	if result.Keys, err = project.RequiredKeyEnvVars(config.KeySelection{}); err != nil {
		return err
	}
	if note := lessTestedNote(project.Engines); note != "" {
		result.Warnings = append(result.Warnings, note)
	}
	if f.DryRun() {
		result.YAML = string(data)
	} else if err := config.WriteFileAtomic(path, data, 0o644); err != nil {
		return err
	}
	return f.WriteResult(result, func(w io.Writer) {
		if result.DryRun {
			fmt.Fprintf(w, "Dry run: would write %s:\n\n%s\n", path, data)
			for _, note := range result.Warnings {
				fmt.Fprintf(w, "Note: %s\n", note)
			}
			return
		}
		fmt.Fprintf(w, "Wrote %s with %d prompts. Engines: %s.\n", path, result.Prompts, strings.Join(result.Engines, ", "))
		for _, note := range result.Warnings {
			fmt.Fprintf(w, "\nNote: %s\n", note)
		}
		writeNextSteps(w, f, result.Keys, result.Prompts)
	})
}

func writeNextSteps(w io.Writer, f *rungrad.Factory, keys []string, prompts int) {
	fmt.Fprintln(w, "\nNext:")
	if prompts == 0 {
		fmt.Fprintln(w, "  1. Add the questions your buyers ask under prompts: in llmention.yaml. With --category, init writes some for you.")
	} else {
		fmt.Fprintln(w, "  1. Read the prompts in llmention.yaml and make them sound like your buyers.")
	}
	step := 2
	var missing []string
	for _, k := range keys {
		if v, ok := lookupEnv(f)(k); !ok || v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "  %d. Export your API %s. llmention reads keys from the environment, never from the file:\n", step, pluralWord(len(missing), "key", "keys"))
		for _, k := range missing {
			fmt.Fprintf(w, "       export %s=...\n", k)
		}
		step++
	}
	fmt.Fprintf(w, "  %d. llmention run --dry-run   (shows the requests and an estimated cost)\n", step)
	fmt.Fprintf(w, "  %d. llmention run\n", step+1)
	if len(missing) > 0 && prompts > 0 {
		fmt.Fprintf(w, "\nBefore you have %s, `llmention run --mock` shows what the report will look like, with made-up answers.\n", pluralWord(len(missing), "the key", "the keys"))
	}
}

func starterFromFlags(f *rungrad.Factory, cmd *cobra.Command) (config.Starter, error) {
	var s config.Starter
	s.Brand, _ = cmd.Flags().GetString("brand")
	s.Category, _ = cmd.Flags().GetString("category")
	s.Domains, _ = cmd.Flags().GetStringSlice("domain")
	competitors, _ := cmd.Flags().GetString("competitors")
	s.Competitors = parseCompetitors(competitors)
	s.Providers, _ = cmd.Flags().GetStringSlice("engines")
	if len(s.Providers) == 0 {
		s.Providers = defaultProviders(f)
	}
	return s, nil
}

// askStarter asks the questions on stderr and reads answers from stdin.
func askStarter(f *rungrad.Factory, s *config.Starter) error {
	in := bufio.NewReader(f.Stdin)
	out := f.Stderr
	ask := func(question, fallback string) (string, error) {
		if fallback != "" {
			fmt.Fprintf(out, "%s [%s]: ", question, fallback)
		} else {
			fmt.Fprintf(out, "%s: ", question)
		}
		line, err := in.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", rungrad.NewError(rungrad.ExitUsage, "init stopped before all questions were answered")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return fallback, nil
		}
		return line, nil
	}
	fmt.Fprintln(out, "Set up llmention. Press Enter to take the value in brackets or to leave an optional question empty.")
	var err error
	for s.Brand == "" {
		if s.Brand, err = ask("Product or company name", ""); err != nil {
			return err
		}
	}
	if s.Category, err = ask(`What does it help with, in a few words (for example "LLM cost tracking")`, s.Category); err != nil {
		return err
	}
	// When an answer is refused, ask again with the old default, so Enter
	// still leaves the question empty instead of repeating the bad answer.
	siteDefault := strings.Join(s.Domains, ", ")
	for {
		site, err := ask("Your website, optional", siteDefault)
		if err != nil {
			return err
		}
		domains := splitList(site)
		if _, err := config.SiteDomains(domains); err != nil {
			fmt.Fprintf(out, "That site does not work: %v\n", err)
			continue
		}
		s.Domains = domains
		break
	}
	competitors, err := ask("Competitors, comma-separated; add a site in brackets if you know it, like Helicone (helicone.ai)", "")
	if err != nil {
		return err
	}
	if competitors != "" {
		s.Competitors = parseCompetitors(competitors)
	}
	providerDefault := strings.Join(s.Providers, ", ")
	for {
		providers, err := ask("Providers you have API keys for (openai, anthropic, google, perplexity, or openrouter for all four through one key), or mock to try it without keys", providerDefault)
		if err != nil {
			return err
		}
		chosen := splitList(providers)
		unknown := slices.DeleteFunc(slices.Clone(chosen), func(p string) bool { _, ok := config.StarterProvider(p); return ok })
		if len(unknown) == 0 && len(chosen) > 0 {
			s.Providers = chosen
			return nil
		}
		fmt.Fprintf(out, "Please name providers llmention knows: %s.\n", strings.Join(config.StarterProviders, ", "))
	}
}

// lessTestedNote speaks up when init or quick set up the direct Gemini or
// Perplexity provider. Those connections have had fewer checks with real
// requests than OpenRouter, which reaches the same assistants.
func lessTestedNote(engines []config.EngineConfig) string {
	var names []string
	for _, e := range engines {
		switch e.Provider {
		case "google":
			names = append(names, "Gemini")
		case "perplexity":
			names = append(names, "Perplexity")
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("the direct %s to %s %s less tested than OpenRouter, which reaches all four assistants with one key (OPENROUTER_API_KEY). "+
		"To use OpenRouter instead, run the command again with --engines openrouter --force.",
		pluralWord(len(names), "connection", "connections"), config.JoinAnd(names), pluralWord(len(names), "is", "are"))
}

// defaultProviders picks the providers whose usual key variable is set. It
// only checks that a value exists; the value itself is not used here.
func defaultProviders(f *rungrad.Factory) []string {
	var out []string
	for _, p := range config.StarterProviders {
		e := config.StarterEngines[p]
		if e.KeyEnv == "" {
			continue
		}
		if v, ok := lookupEnv(f)(e.KeyEnv); ok && v != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		// With only an OpenRouter key, ask all four assistants through it.
		if v, ok := lookupEnv(f)("OPENROUTER_API_KEY"); ok && v != "" {
			return []string{"openrouter"}
		}
		return []string{"openai", "anthropic"}
	}
	return out
}

// parseCompetitors reads "Helicone (helicone.ai), Langfuse" or
// "Helicone=helicone.ai, Langfuse".
func parseCompetitors(value string) []config.CompetitorConfig {
	var out []config.CompetitorConfig
	for _, part := range splitList(value) {
		c := config.CompetitorConfig{Name: part}
		if name, site, ok := strings.Cut(part, "="); ok {
			c.Name, c.Domains = strings.TrimSpace(name), splitList(site)
		} else if open := strings.LastIndex(part, "("); open > 0 && strings.HasSuffix(part, ")") {
			c.Name = strings.TrimSpace(part[:open])
			c.Domains = splitList(strings.TrimSuffix(part[open+1:], ")"))
		}
		if c.Name != "" {
			out = append(out, c)
		}
	}
	return out
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
