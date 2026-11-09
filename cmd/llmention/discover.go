package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/discover"
	"github.com/vincentsch/llmention/internal/mention"
)

func discoverCommand() *rungrad.Command {
	return &rungrad.Command{
		Use:   "discover",
		Short: "Suggest buyer questions to use as prompts",
		Long: "Suggest prompts for llmention.yaml. Without --from it fills question templates with your topics, which costs nothing. " +
			"With --from chab and CHAB_API_KEY set, it uses search data from Chab (chab.ai). It reads the \"People also ask\" questions on Google and asks for related search phrases, then sorts the questions by how often people search for them each month. " +
			"Questions that name your brand are left out. Use --out to write a prompts file.",
		Examples: []string{
			"llmention discover",
			"llmention discover --seed \"LLM cost tracking\" --seed \"OpenAI usage dashboard\"",
			"llmention discover --from chab --seed \"LLM cost tracking\" --dry-run",
			"llmention discover --from chab --seed \"LLM cost tracking\" --out prompts.txt",
		},
		Related:      []string{"llmention run", "llmention init"},
		OutputModes:  []string{rungrad.OutputModeHuman, rungrad.OutputModeJSON, rungrad.OutputModeJQ, rungrad.OutputModeTemplate},
		Mutates:      true,
		SupportsMeta: true,
		GroupID:      "manage",
		Args:         noArgs,
		Configure: func(cmd *cobra.Command) {
			cmd.Flags().StringArray("seed", nil, "A topic or search phrase (repeatable, default: brand.category)")
			cmd.Flags().String("from", "", "Where to find questions: chab, or leave it out for templates")
			cmd.Flags().String("country", "US", "Country for search data, as a two-letter code")
			cmd.Flags().String("language", "en-US", "Language for search data")
			cmd.Flags().Int("pages", 3, "Search result pages to read with --from chab (5 Chab credits each)")
			cmd.Flags().Int("limit", 25, "Show at most this many suggestions")
			cmd.Flags().String("out", "", "Write the suggestions to this prompts file, one per line")
			cmd.Flags().Bool("force", false, "Overwrite the --out file")
		},
		Run: runDiscover,
	}
}

type discoverResult struct {
	Source      string               `json:"source"`
	Seeds       []string             `json:"seeds"`
	Candidates  []discover.Candidate `json:"candidates"`
	NamedBrand  int                  `json:"left_out_naming_brand"`
	Out         string               `json:"out,omitempty"`
	DryRun      bool                 `json:"dry_run"`
	ChabCredits int                  `json:"chab_credits,omitempty"`
}

func runDiscover(f *rungrad.Factory, cmd *cobra.Command, _ []string) error {
	project, err := loadOptionalProject(cmd, f)
	if err != nil {
		return err
	}
	seeds, _ := cmd.Flags().GetStringArray("seed")
	from, _ := cmd.Flags().GetString("from")
	country, _ := cmd.Flags().GetString("country")
	language, _ := cmd.Flags().GetString("language")
	pages, _ := cmd.Flags().GetInt("pages")
	limit, _ := cmd.Flags().GetInt("limit")
	out, _ := cmd.Flags().GetString("out")
	force, _ := cmd.Flags().GetBool("force")
	if len(seeds) == 0 && project != nil && project.Brand != nil && project.Brand.Category != "" {
		seeds = []string{project.Brand.Category}
	}
	if len(seeds) == 0 {
		return rungrad.NewError(rungrad.ExitUsage, "pass --seed with a topic such as \"LLM cost tracking\", or set brand.category in llmention.yaml")
	}
	if out != "" && !force && !f.DryRun() {
		if _, err := os.Stat(out); err == nil {
			return rungrad.NewError(rungrad.ExitUsage, out+" exists; pass --force to overwrite it")
		}
	}
	result := discoverResult{Source: "templates", Seeds: seeds, Out: out, DryRun: f.DryRun()}
	var candidates []discover.Candidate
	switch from {
	case "":
		var competitors []string
		if project != nil {
			for _, c := range project.Competitors {
				competitors = append(competitors, c.Name)
			}
		}
		candidates = discover.FromTemplates(seeds, competitors)
	case "chab":
		result.Source = "chab"
		result.ChabCredits = discover.KeywordIdeasCredits + discover.SERPCredits*pages
		if f.DryRun() {
			return f.WriteResult(result, func(w io.Writer) {
				fmt.Fprintf(w, "Dry run: nothing was sent. llmention would ask Chab for related search phrases for %s and read %d pages of search results, for about %d Chab credits.\n", strings.Join(seeds, ", "), pages, result.ChabCredits)
			})
		}
		key, ok := lookupEnv(f)("CHAB_API_KEY")
		if !ok || key == "" {
			return rungrad.NewError(rungrad.ExitAuth, "set CHAB_API_KEY to an API key from chab.ai (Team → API Tokens) with the api:search:read and api:seo:read scopes")
		}
		f.RegisterSecret(key)
		base, _ := lookupEnv(f)("CHAB_BASE_URL")
		chab := &discover.Chab{BaseURL: base, Key: key}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		f.Infof("Asking Chab for search data. This can take a minute.")
		candidates, err = chab.Collect(ctx, seeds, strings.ToUpper(country), language, pages)
		if err != nil && len(candidates) == 0 {
			return rungrad.NewError(rungrad.ExitAPI, err.Error())
		}
		if err != nil {
			f.Infof("Some search data is missing: %v", err)
		}
	default:
		return rungrad.NewError(rungrad.ExitUsage, "--from takes chab, or leave it out for templates")
	}
	var brand mention.Brand
	var existing []string
	if project != nil {
		if project.Brand != nil {
			b := analysis.Brands(project).You
			brand = mention.Brand{ID: b.Name, Name: b.Name, Aliases: b.Aliases, Domains: b.Domains, MatchCase: b.MatchCase}
		}
		for _, p := range project.Prompts {
			existing = append(existing, p.Text)
		}
	}
	candidates, result.NamedBrand = discover.Clean(candidates, brand, existing)
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result.Candidates = candidates
	if result.Candidates == nil {
		result.Candidates = []discover.Candidate{}
	}
	if out != "" && !f.DryRun() {
		var b strings.Builder
		fmt.Fprintf(&b, "# Prompts suggested by llmention discover on %s from %s.\n", time.Now().Format("2006-01-02"), result.Source)
		b.WriteString("# Delete the questions a buyer would not ask.\n")
		for _, c := range candidates {
			b.WriteString(c.Prompt + "\n")
		}
		if err := config.WriteFileAtomic(out, []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return f.WriteResult(result, func(w io.Writer) {
		if len(candidates) == 0 && result.Source == "templates" && result.NamedBrand == 0 {
			fmt.Fprintln(w, "No new prompts found. llmention.yaml already has the template questions for "+config.JoinAnd(quoted(seeds))+".")
			fmt.Fprintln(w, "Try other --seed topics, or --from chab for questions from search data.")
			return
		}
		if len(candidates) == 0 {
			fmt.Fprintln(w, "No new prompts found. Try other --seed topics.")
			return
		}
		tw := newTable(w)
		fmt.Fprintln(tw, "Searches/month\tPrompt\tFrom")
		for _, c := range candidates {
			volume := "–"
			if c.MonthlySearches != nil {
				volume = fmt.Sprint(*c.MonthlySearches)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", volume, c.Prompt, strings.ReplaceAll(c.Source, "_", " "))
		}
		tw.Flush()
		if result.NamedBrand > 0 {
			fmt.Fprintf(w, "\nLeft out %d questions that name your brand; their answers would not count toward your mention rate.\n", result.NamedBrand)
		}
		switch {
		case out != "" && result.DryRun:
			fmt.Fprintf(w, "\nDry run: would write these to %s.\n", out)
		case out != "":
			fmt.Fprintf(w, "\nWrote %s. Add `prompts_file: %s` to llmention.yaml to use it.\n", out, out)
		default:
			fmt.Fprintln(w, "\nCopy the ones you like under prompts: in llmention.yaml, or save them with --out prompts.txt.")
		}
	})
}

func quoted(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = strconv.Quote(s)
	}
	return out
}
