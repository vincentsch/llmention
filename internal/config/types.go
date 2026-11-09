// Package config loads and checks llmention.yaml, the project file that names
// your brand, your competitors, the assistants to ask and the prompts to send.
package config

import "time"

const (
	DefaultProjectConfigPath = "./llmention.yaml"
	DefaultRunsDir           = "./.llmention/runs"
	DefaultMaxRequests       = 100
	DefaultRetries           = 2
	DefaultMaxJudgeRequests  = 100
	DefaultConcurrency       = 4
	DefaultRepetitions       = 1
	// DefaultTimeout is long because an answer with thinking and several web
	// searches can take minutes to come back.
	DefaultTimeout = 5 * time.Minute

	// PromptsSuiteID names the suite llmention builds from the prompts list.
	PromptsSuiteID = "prompts"
)

// DefaultModes are the modes for your prompts when the project file lists
// none. The model answers once from memory and once with web search allowed.
var DefaultModes = []string{"memory", "search_auto"}

// KnownProviders lists the provider names an engine may use.
var KnownProviders = map[string]struct{}{
	"openai":            {},
	"anthropic":         {},
	"google":            {},
	"perplexity":        {},
	"openrouter":        {},
	"openai-compatible": {},
	"mock":              {},
}

// capabilityOverrideTypes lists the settings allowed under an engine's
// capabilities and the type of each value.
var capabilityOverrideTypes = map[string]string{
	"supports_memory":                           "bool",
	"supports_search_auto":                      "bool",
	"supports_search_required":                  "bool",
	"supports_search_cache_only":                "bool",
	"supports_live_access_control":              "bool",
	"supports_required_tool_choice":             "bool",
	"supports_search_allow_domains":             "bool",
	"supports_search_block_domains":             "bool",
	"supports_search_location":                  "bool",
	"supports_search_recency":                   "bool",
	"supports_search_date_filters":              "bool",
	"supports_search_max_queries":               "bool",
	"supports_source_metadata_request":          "bool",
	"inherently_search_grounded":                "bool",
	"exposes_citations":                         "bool",
	"exposes_consulted_sources":                 "bool",
	"exposes_tool_calls":                        "bool",
	"supports_structured_output":                "bool",
	"supports_structured_output_with_tools":     "bool",
	"supports_structured_output_with_citations": "bool",
	"knowledge_cutoff":                          "string",
}

// Project is a loaded and checked llmention.yaml.
type Project struct {
	Version     int                `json:"version" yaml:"version"`
	Brand       *BrandConfig       `json:"brand,omitempty" yaml:"brand,omitempty"`
	Competitors []CompetitorConfig `json:"competitors,omitempty" yaml:"competitors,omitempty"`
	Engines     []EngineConfig     `json:"engines,omitempty" yaml:"engines,omitempty"`
	Prompts     []Prompt           `json:"prompts,omitempty" yaml:"prompts,omitempty"`
	PromptsFile string             `json:"prompts_file,omitempty" yaml:"prompts_file,omitempty"`
	Modes       []string           `json:"modes,omitempty" yaml:"modes,omitempty"`
	Repetitions int                `json:"repetitions,omitempty" yaml:"repetitions,omitempty"`
	Suites      SuiteConfig        `json:"suites,omitempty" yaml:"suites,omitempty"`
	Execution   ExecutionConfig    `json:"execution" yaml:"execution"`
	Analysis    AnalysisConfig     `json:"analysis,omitempty" yaml:"analysis,omitempty"`
	RunsDir     string             `json:"runs_dir" yaml:"runs_dir"`
	Notify      NotifyConfig       `json:"notify,omitempty" yaml:"notify,omitempty"`
}

// BrandConfig describes you. Mentions of the name, an alias, a domain or a
// product count as mentions of you.
type BrandConfig struct {
	Name     string   `json:"name" yaml:"name"`
	Category string   `json:"category,omitempty" yaml:"category,omitempty"`
	Aliases  []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Domains  []string `json:"domains,omitempty" yaml:"domains,omitempty"`
	Products []string `json:"products,omitempty" yaml:"products,omitempty"`
	People   []string `json:"people,omitempty" yaml:"people,omitempty"`
	// MatchCase counts the name, aliases and products only with the same
	// capital letters, for a name that is also an everyday word.
	MatchCase bool `json:"match_case,omitempty" yaml:"match_case,omitempty"`
}

// CompetitorConfig describes one competitor to compare against.
type CompetitorConfig struct {
	Name      string   `json:"name" yaml:"name"`
	Aliases   []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Domains   []string `json:"domains,omitempty" yaml:"domains,omitempty"`
	MatchCase bool     `json:"match_case,omitempty" yaml:"match_case,omitempty"`
}

// EngineConfig is one assistant to ask: a provider, a model and the name of
// the environment variable that holds the API key.
type EngineConfig struct {
	ID              string       `json:"id" yaml:"id"`
	Provider        string       `json:"provider" yaml:"provider"`
	Model           string       `json:"model" yaml:"model"`
	KeyEnv          string       `json:"key_env,omitempty" yaml:"key_env,omitempty"`
	BaseURL         string       `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	MaxOutputTokens int          `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"`
	FixturesPath    string       `json:"fixtures_path,omitempty" yaml:"fixtures_path,omitempty"`
	Capabilities    Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// Capabilities overrides what llmention assumes a provider and model can do.
type Capabilities map[string]any

// Prompt is one question to send. In YAML it is either a plain string or a
// mapping with an id, the prompt text and optional tags.
type Prompt struct {
	ID   string   `json:"id" yaml:"id"`
	Text string   `json:"prompt" yaml:"prompt"`
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// SuiteConfig lists extra suite files, for example research suites.
type SuiteConfig struct {
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

// ExecutionConfig sets the limits of a run.
type ExecutionConfig struct {
	// MaxRequests caps the provider requests a run may send, retries included.
	MaxRequests      int           `json:"max_requests" yaml:"max_requests"`
	Retries          int           `json:"retries" yaml:"retries"`
	MaxJudgeRequests int           `json:"max_judge_requests,omitempty" yaml:"max_judge_requests,omitempty"`
	Timeout          time.Duration `json:"timeout" yaml:"timeout"`
	Concurrency      int           `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
}

// AnalysisConfig sets up the optional AI grader, which llmention.yaml calls
// the judge.
type AnalysisConfig struct {
	ModelAssistedEnabled bool         `json:"model_assisted_enabled,omitempty" yaml:"model_assisted_enabled,omitempty"`
	Judge                *JudgeConfig `json:"judge,omitempty" yaml:"judge,omitempty"`
}

// JudgeConfig is the model that grades answers for the AI grader.
type JudgeConfig struct {
	Provider              string       `json:"provider" yaml:"provider"`
	Model                 string       `json:"model" yaml:"model"`
	KeyEnv                string       `json:"key_env" yaml:"key_env"`
	BaseURL               string       `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	FixturesPath          string       `json:"fixtures_path,omitempty" yaml:"fixtures_path,omitempty"`
	AllowAnswerDisclosure bool         `json:"allow_answer_disclosure" yaml:"allow_answer_disclosure"`
	Capabilities          Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// NotifyConfig names the script and the webhook that llmention calls when a
// --fail-under check fails.
type NotifyConfig struct {
	Command       string `json:"command,omitempty" yaml:"command,omitempty"`
	WebhookURLEnv string `json:"webhook_url_env,omitempty" yaml:"webhook_url_env,omitempty"`
}

// EngineByID returns the engine with the given id.
func (cfg *Project) EngineByID(id string) (EngineConfig, bool) {
	for _, e := range cfg.Engines {
		if e.ID == id {
			return e, true
		}
	}
	return EngineConfig{}, false
}
