package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// StarterEngines are the engines `init` and `quick` write for each provider.
// Model ids change every few months; these were current on the providers'
// model pages in October 2026 and are meant to be edited.
var StarterEngines = map[string]EngineConfig{
	"openai":     {ID: "chatgpt", Provider: "openai", Model: "gpt-6.1-sol", KeyEnv: "OPENAI_API_KEY"},
	"anthropic":  {ID: "claude", Provider: "anthropic", Model: "claude-sonnet-5-5", KeyEnv: "ANTHROPIC_API_KEY"},
	"google":     {ID: "gemini", Provider: "google", Model: "gemini-3.8-flash", KeyEnv: "GEMINI_API_KEY"},
	"perplexity": {ID: "perplexity", Provider: "perplexity", Model: "sonar", KeyEnv: "PERPLEXITY_API_KEY"},
	"mock":       {ID: "mock", Provider: "mock", Model: "demo"},
}

// OpenRouterStarterEngines ask the same four assistants through one
// OpenRouter key. OpenRouter gives OpenAI, Anthropic and Gemini models their
// own provider's web search, and Perplexity searches by itself.
var OpenRouterStarterEngines = []EngineConfig{
	{ID: "chatgpt", Provider: "openrouter", Model: "openai/gpt-6.1-sol", KeyEnv: "OPENROUTER_API_KEY"},
	{ID: "claude", Provider: "openrouter", Model: "anthropic/claude-sonnet-5.5", KeyEnv: "OPENROUTER_API_KEY"},
	{ID: "gemini", Provider: "openrouter", Model: "google/gemini-3.8-flash", KeyEnv: "OPENROUTER_API_KEY"},
	{ID: "perplexity", Provider: "openrouter", Model: "perplexity/sonar", KeyEnv: "OPENROUTER_API_KEY"},
}

// StarterProviders is the order in which init offers providers.
var StarterProviders = []string{"openai", "anthropic", "google", "perplexity", "openrouter", "mock"}

// providerNames maps what people type to a starter provider, so "chatgpt,
// claude" works as well as "openai, anthropic".
var providerNames = map[string]string{
	"openai": "openai", "chatgpt": "openai", "gpt": "openai",
	"anthropic": "anthropic", "claude": "anthropic",
	"google": "google", "gemini": "google",
	"perplexity": "perplexity", "sonar": "perplexity",
	"openrouter": "openrouter",
	"mock":       "mock", "demo": "mock",
}

// StarterProvider returns the provider for a name such as "claude".
func StarterProvider(name string) (string, bool) {
	p, ok := providerNames[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// Starter holds the answers that describe a new project.
type Starter struct {
	Brand       string
	Category    string
	Domains     []string
	Competitors []CompetitorConfig
	Providers   []string
	Prompts     []string
}

// StarterPrompts writes buyer questions for a topic such as "LLM cost
// tracking". None of them names your brand, because a question that names you
// says nothing about whether an assistant would bring you up on its own. The
// questions don't say who is asking, so they fit a freelancer, a company and
// a consumer alike.
func StarterPrompts(category string, competitors []string) []string {
	topic := topicPhrase(category)
	if topic == "" {
		return nil
	}
	prompts := []string{
		"What are the best tools for " + topic + "?",
		"What would you recommend for " + topic + "?",
		"I need something for " + topic + ". What are my options?",
		"What do most people use for " + topic + "?",
		"Which options for " + topic + " are worth paying for?",
		"What are good free or open-source options for " + topic + "?",
		"What should I look for when choosing a tool for " + topic + "?",
		"What is the easiest way to get started with " + topic + "?",
	}
	for i, name := range competitors {
		if i == 2 {
			break
		}
		prompts = append(prompts, "What are good alternatives to "+name+"?")
	}
	return prompts
}

// topicPhrase turns "AI API cost tracking tool" into "AI API cost tracking"
// so it reads well inside the starter questions.
func topicPhrase(category string) string {
	topic := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(category), ".?!"))
	lower := strings.ToLower(topic)
	for _, suffix := range []string{" tools", " tool", " software", " apps", " app", " platform", " service", " solution", " solutions"} {
		if strings.HasSuffix(lower, suffix) && len(topic) > len(suffix) {
			topic = topic[:len(topic)-len(suffix)]
			break
		}
	}
	return strings.TrimSpace(topic)
}

// StarterProject builds the project a Starter describes.
func StarterProject(s Starter) (*Project, error) {
	p := &Project{
		Version: 1,
		Brand: &BrandConfig{
			Name:     strings.TrimSpace(s.Brand),
			Category: strings.TrimSpace(s.Category),
			Domains:  s.Domains,
		},
		Competitors: s.Competitors,
		Modes:       append([]string(nil), DefaultModes...),
		Repetitions: DefaultRepetitions,
		RunsDir:     DefaultRunsDir,
		Execution: ExecutionConfig{
			MaxRequests: DefaultMaxRequests,
			Retries:     DefaultRetries,
			Timeout:     DefaultTimeout,
			Concurrency: DefaultConcurrency,
		},
	}
	for _, name := range s.Providers {
		provider, ok := StarterProvider(name)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q; choose from %s", name, strings.Join(StarterProviders, ", "))
		}
		if provider == "openrouter" {
			// Through OpenRouter, add the assistants no direct key covers yet.
			for _, e := range OpenRouterStarterEngines {
				if !slices.ContainsFunc(p.Engines, func(have EngineConfig) bool { return have.ID == e.ID }) {
					p.Engines = append(p.Engines, e)
				}
			}
			continue
		}
		if !slices.ContainsFunc(p.Engines, func(e EngineConfig) bool { return e.Provider == provider }) {
			p.Engines = append(p.Engines, StarterEngines[provider])
		}
	}
	prompts := s.Prompts
	if len(prompts) == 0 {
		var names []string
		for _, c := range s.Competitors {
			names = append(names, c.Name)
		}
		prompts = StarterPrompts(s.Category, names)
	}
	for _, text := range prompts {
		p.Prompts = append(p.Prompts, Prompt{Text: text})
	}
	p.Execution.MaxRequests = starterBudget(len(p.Prompts)*len(p.Engines)*len(p.Modes)*p.Repetitions, p.Execution.Retries)
	if err := p.ValidateShape(); err != nil {
		return nil, err
	}
	return p, nil
}

// starterBudget is the max_requests that init writes: enough for every
// request and all its retries, and never less than the default.
func starterBudget(requests, retries int) int {
	return max(DefaultMaxRequests, requests*(1+retries))
}

// StarterYAML writes a project as a commented llmention.yaml. The comments
// are for someone who opens the file for the first time and has not read the
// docs.
func StarterYAML(p *Project) []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	w("# llmention settings for your product. After you edit this file, run\n")
	w("# `llmention run --dry-run` to see what a run would send and cost.\n")
	w("version: 1\n\n")
	w("brand:\n")
	w("  name: %s\n", scalar(p.Brand.Name))
	if p.Brand.Category != "" {
		w("  category: %s  # what your product helps with, in a few words\n", scalar(p.Brand.Category))
	}
	w("  aliases: %s  # other names or spellings that count as a mention of you\n", flow(p.Brand.Aliases))
	w("  domains: %s  # your websites, so the report can tell when an answer links to you\n", flow(p.Brand.Domains))
	w("  products: %s  # names of your products that count as a mention of you\n", flow(p.Brand.Products))
	w("  people: %s  # founders or authors; only the recognition suite asks about them\n\n", flow(p.Brand.People))
	w("# The products you compete with. Add each one's website under domains, so\n")
	w("# the report can tell whose site an answer cites.\n")
	if len(p.Competitors) == 0 {
		w("# For example:\n")
		w("#   competitors:\n")
		w("#     - name: Globex Billing\n")
		w("#       domains: [\"globex.example\"]\n")
		w("competitors: []\n\n")
	} else {
		w("competitors:\n")
		for _, c := range p.Competitors {
			w("  - name: %s\n", scalar(c.Name))
			w("    domains: %s\n", flow(c.Domains))
		}
		w("\n")
	}
	w("# The AI assistants to ask. Each one needs an API key from its provider.\n")
	w("# llmention reads the key from the environment variable in key_env and\n")
	w("# never writes it to a file. Providers release new models often, and you\n")
	w("# can use any model your key has access to.\n")
	w("engines:\n")
	for _, e := range p.Engines {
		w("  - id: %s\n", scalar(e.ID))
		w("    provider: %s\n", scalar(e.Provider))
		w("    model: %s\n", scalar(e.Model))
		if e.KeyEnv != "" {
			w("    key_env: %s\n", scalar(e.KeyEnv))
		}
		if e.BaseURL != "" {
			w("    base_url: %s\n", scalar(e.BaseURL))
		}
	}
	w("\n# The questions llmention asks each assistant. Write them the way your\n")
	w("# buyers would ask. Leave your product's name out. Your mention rate is the\n")
	w("# share of answers that name you, and answers to a question that already\n")
	w("# names you do not count toward it.\n")
	if len(p.Prompts) == 0 {
		w("# For example:\n")
		w("#   prompts:\n")
		w("#     - What are the best tools for invoicing for freelancers?\n")
		w("prompts: []\n")
	} else {
		w("prompts:\n")
		for _, prompt := range p.Prompts {
			w("  - %s\n", scalar(prompt.Text))
		}
	}
	w("\n# Each question is asked once in each mode.\n")
	w("# memory: the assistant answers from what it learned in training.\n")
	w("# search_auto: the assistant may search the web before it answers.\n")
	w("modes: %s\n\n", flow(p.Modes))
	w("# max_requests leaves room for every request and all its retries. When you\n")
	w("# add prompts or engines, the dry run tells you if it gets too low.\n")
	w("execution:\n")
	w("  max_requests: %d  # a run stops after this many requests, retries included\n", p.Execution.MaxRequests)
	w("  retries: %d  # how often a failed request is tried again\n", p.Execution.Retries)
	w("  timeout: %s  # how long to wait for one answer\n", durationText(p.Execution.Timeout))
	w("  concurrency: %d  # how many requests run at the same time\n\n", p.Execution.Concurrency)
	w("# Where llmention saves each run. The files hold the full answers.\n")
	w("runs_dir: %s\n", scalar(p.RunsDir))
	return b.Bytes()
}

// durationText writes 2m instead of Go's 2m0s.
func durationText(d time.Duration) string {
	text := d.String()
	text = strings.TrimSuffix(text, "0s")
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	if text == "" {
		return "0s"
	}
	return text
}

func scalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return quote(s)
	}
	text := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(text, "\n") {
		return quote(s)
	}
	return text
}

func flow(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = quote(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// quote writes a JSON string, which YAML reads as a double-quoted scalar.
func quote(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}
