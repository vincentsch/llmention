package engine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

// JudgeEngineID is the engine id of the AI grader, the model set up under
// analysis.judge.
const JudgeEngineID = "analysis.judge"

type SecretResolver interface {
	LookupEnv(name string) (string, bool)
	RegisterSecret(value string)
}

type FactoryOptions struct {
	ConfigDir  string
	HTTPClient *http.Client
	Transport  http.RoundTripper
	// Timeout limits each provider request. Zero means config.DefaultTimeout.
	Timeout time.Duration
	// BaseURLs overrides provider base URLs by provider name, for tests.
	BaseURLs        map[string]string
	SecretResolver  SecretResolver
	LookupEnv       func(string) (string, bool)
	RegisterSecret  func(string)
	MockFixtureSets map[string]*MockFixtureSet
	// World tells the mock provider which brands to write demo answers about.
	World *MockWorld
}

// Factory builds engines from the engines in llmention.yaml.
type Factory struct {
	opts adapterOptions
}

type ProjectEngines struct {
	Engines []Engine
	Judge   Engine
}

type adapterOptions struct {
	configDir      string
	httpClient     *http.Client
	baseURLs       map[string]string
	secretResolver resolver
	mockFixtures   map[string]*MockFixtureSet
	world          *MockWorld
}

type resolver struct {
	lookup   func(string) (string, bool)
	register func(string)
}

func NewFactory(opts FactoryOptions) *Factory {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = config.DefaultTimeout
	}
	client := opts.HTTPClient
	if client == nil && opts.Transport != nil {
		client = &http.Client{Transport: opts.Transport, Timeout: timeout}
	}
	if client == nil {
		client = NewHTTPClient(timeout)
	}
	lookup := os.LookupEnv
	register := func(string) {}
	if opts.SecretResolver != nil {
		lookup = opts.SecretResolver.LookupEnv
		register = opts.SecretResolver.RegisterSecret
	}
	if opts.LookupEnv != nil {
		lookup = opts.LookupEnv
	}
	if opts.RegisterSecret != nil {
		register = opts.RegisterSecret
	}
	return &Factory{
		opts: adapterOptions{
			configDir:  opts.ConfigDir,
			httpClient: client,
			baseURLs:   cloneStringMap(opts.BaseURLs),
			secretResolver: resolver{
				lookup:   lookup,
				register: register,
			},
			mockFixtures: cloneMockFixtureSetMap(opts.MockFixtureSets),
			world:        opts.World,
		},
	}
}

func (f *Factory) NewEngine(cfg config.EngineConfig) (Engine, error) {
	if f == nil {
		f = NewFactory(FactoryOptions{})
	}
	if err := validateFactoryEngineConfig(cfg); err != nil {
		return nil, err
	}
	profile, err := capabilityProfileForConfig(cfg.Provider, cfg.Model, cfg.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("engine %q: %w", cfg.ID, err)
	}
	base := baseEngine{
		id:       cfg.ID,
		provider: cfg.Provider,
		model:    cfg.Model,
		keyEnv:   cfg.KeyEnv,
		baseURL:  firstNonEmpty(cfg.BaseURL, f.opts.baseURLs[cfg.Provider]),
		caps:     profile,
		opts:     f.opts,
	}
	if cfg.Provider == "mock" {
		return f.newMockEngine(base, cfg.FixturesPath, cfg.Capabilities)
	}
	if cfg.Provider == "openai" {
		return newOpenAIEngine(base), nil
	}
	if cfg.Provider == "google" {
		return newGoogleEngine(base), nil
	}
	if cfg.Provider == "perplexity" {
		return newPerplexityEngine(base), nil
	}
	if cfg.Provider == "anthropic" {
		return newAnthropicEngine(base), nil
	}
	if cfg.Provider == "openrouter" {
		return newOpenRouterEngine(base), nil
	}
	if cfg.Provider == "openai-compatible" {
		return newCompatibleEngine(base)
	}
	return &stubEngine{baseEngine: base}, nil
}

func (f *Factory) NewJudgeEngine(judge *config.JudgeConfig) (Engine, error) {
	if judge == nil {
		return nil, nil
	}
	return f.NewEngine(config.EngineConfig{
		ID:           JudgeEngineID,
		Provider:     judge.Provider,
		Model:        judge.Model,
		KeyEnv:       judge.KeyEnv,
		BaseURL:      judge.BaseURL,
		FixturesPath: judge.FixturesPath,
		Capabilities: judge.Capabilities,
	})
}

func (f *Factory) NewTargetEngines(project *config.Project) ([]Engine, error) {
	if project == nil {
		return nil, fmt.Errorf("project config is nil")
	}
	engines := make([]Engine, 0, len(project.Engines))
	for _, engineConfig := range project.Engines {
		engine, err := f.NewEngine(engineConfig)
		if err != nil {
			return nil, err
		}
		engines = append(engines, engine)
	}
	return engines, nil
}

func (f *Factory) NewProjectEngines(project *config.Project) (ProjectEngines, error) {
	engines, err := f.NewTargetEngines(project)
	if err != nil {
		return ProjectEngines{}, err
	}
	judge, err := f.NewJudgeEngine(project.Analysis.Judge)
	if err != nil {
		return ProjectEngines{}, err
	}
	return ProjectEngines{Engines: engines, Judge: judge}, nil
}

func validateFactoryEngineConfig(cfg config.EngineConfig) error {
	if cfg.ID == "" {
		return fmt.Errorf("engine id is required")
	}
	if cfg.Provider == "" {
		return fmt.Errorf("engine %q provider is required", cfg.ID)
	}
	if _, ok := config.KnownProviders[cfg.Provider]; !ok {
		return fmt.Errorf("engine %q provider %q is not supported", cfg.ID, cfg.Provider)
	}
	if cfg.Model == "" {
		return fmt.Errorf("engine %q model is required", cfg.ID)
	}
	if config.ProviderNeedsKey(cfg.Provider) && cfg.KeyEnv == "" {
		return fmt.Errorf("engine %q key_env is required for provider %q", cfg.ID, cfg.Provider)
	}
	switch {
	case cfg.Provider == "openai-compatible" && cfg.BaseURL == "":
		return fmt.Errorf("engine %q base_url is required for provider %q", cfg.ID, cfg.Provider)
	case cfg.Provider == "mock" && cfg.BaseURL != "":
		return fmt.Errorf("engine %q base_url is not used by the mock provider", cfg.ID)
	case cfg.BaseURL != "":
		if err := config.ValidateBaseURL("engine "+cfg.ID+" base_url", cfg.BaseURL, cfg.KeyEnv); err != nil {
			return err
		}
	}
	if cfg.FixturesPath != "" && cfg.Provider != "mock" {
		return fmt.Errorf("engine %q: fixtures_path only works with provider %q", cfg.ID, "mock")
	}
	return nil
}

func capabilityProfileForConfig(provider, model string, overrides config.Capabilities) (CapabilityProfile, error) {
	profile := baseCapabilityProfile(provider, model)
	if err := applyCapabilityOverrides(&profile, overrides); err != nil {
		return CapabilityProfile{}, err
	}
	if err := validateCapabilityCeiling(provider, baseCapabilityProfile(provider, model), profile, overrides); err != nil {
		return CapabilityProfile{}, err
	}
	return profile, nil
}

func CapabilityProfileForConfig(provider, model string, overrides config.Capabilities) (CapabilityProfile, error) {
	return capabilityProfileForConfig(provider, model, overrides)
}

func baseCapabilityProfile(provider, model string) CapabilityProfile {
	profile := CapabilityProfile{
		Provider:       provider,
		Model:          model,
		SupportsMemory: true,
	}
	switch provider {
	case "mock":
		profile.SupportsSearchAuto = true
		profile.SupportsSearchRequired = true
		profile.SupportsSearchCacheOnly = true
		profile.SupportsLiveAccessControl = true
		profile.SupportsRequiredToolChoice = true
		profile.SupportsSearchAllowDomains = true
		profile.SupportsSearchBlockDomains = true
		profile.SupportsSearchLocation = true
		profile.SupportsSearchRecency = true
		profile.SupportsSearchDateFilters = true
		profile.SupportsSearchMaxQueries = true
		profile.SupportsSourceMetadataRequest = true
		profile.ExposesCitations = true
		profile.ExposesConsultedSources = true
		profile.ExposesToolCalls = true
		profile.SupportsStructuredOutput = true
		profile.SupportsStructuredOutputWithTools = true
		profile.SupportsStructuredOutputWithCitations = true
	case "perplexity":
		profile.SupportsSearchAuto = true
		profile.SupportsSearchRequired = true
		profile.SupportsRequiredToolChoice = true
		profile.SupportsSearchAllowDomains = true
		profile.SupportsSearchBlockDomains = true
		profile.SupportsSearchLocation = true
		profile.SupportsSearchRecency = true
		profile.SupportsSearchDateFilters = true
		profile.InherentlySearchGrounded = true
		profile.ExposesCitations = true
		profile.ExposesConsultedSources = true
	case "google":
		profile.SupportsSearchAuto = true
		profile.SupportsSearchRequired = true
		profile.SupportsRequiredToolChoice = true
		profile.ExposesCitations = true
		profile.ExposesConsultedSources = true
		profile.ExposesToolCalls = true
		profile.SupportsStructuredOutput = true
	case "anthropic":
		profile.SupportsSearchAuto = true
		profile.SupportsSearchAllowDomains = true
		profile.SupportsSearchBlockDomains = true
		profile.SupportsSearchLocation = true
		profile.SupportsSearchMaxQueries = true
		profile.ExposesCitations = true
		profile.ExposesConsultedSources = true
		profile.ExposesToolCalls = true
	case "openrouter":
		profile.SupportsSearchAuto = true
		profile.ExposesCitations = true
		if openRouterSearchesItself(model) {
			profile.InherentlySearchGrounded = true
			break
		}
		profile.SupportsSearchAllowDomains = true
		profile.SupportsSearchBlockDomains = true
		profile.SupportsSearchLocation = true
		profile.SupportsSearchMaxQueries = true
	case "openai":
		profile.SupportsStructuredOutput = true
		profile.SupportsSearchAuto = true
		profile.SupportsSearchRequired = true
		profile.SupportsSearchCacheOnly = true
		profile.SupportsLiveAccessControl = true
		profile.SupportsRequiredToolChoice = true
		profile.SupportsSearchAllowDomains = true
		profile.SupportsSearchBlockDomains = true
		profile.SupportsSearchLocation = true
		profile.SupportsSourceMetadataRequest = true
		profile.ExposesCitations = true
		profile.ExposesConsultedSources = true
		profile.ExposesToolCalls = true
		profile.SupportsStructuredOutputWithTools = true
		profile.SupportsStructuredOutputWithCitations = true
	}
	return profile
}

// validateCapabilityCeiling refuses overrides that turn on something the
// provider cannot do. Overrides may always turn a feature off. Only the ones
// in positiveCapabilityOverrides may turn a feature on.
func validateCapabilityCeiling(provider string, base, effective CapabilityProfile, overrides config.Capabilities) error {
	if provider != "google" && provider != "perplexity" && provider != "anthropic" && provider != "openrouter" && provider != "openai-compatible" {
		return nil
	}
	positive := positiveCapabilityOverrides(provider)
	for key, value := range overrides {
		if key == "knowledge_cutoff" {
			continue
		}
		enabled, ok := value.(bool)
		if !ok || !enabled {
			continue
		}
		if capabilityValue(base, key) {
			continue
		}
		if _, ok := positive[key]; !ok {
			return fmt.Errorf("capabilities.%s cannot be turned on for provider %q", key, provider)
		}
		if !capabilityValue(effective, key) {
			return fmt.Errorf("capabilities.%s could not be turned on for provider %q", key, provider)
		}
	}
	if err := validateCapabilityDependencies(provider, effective); err != nil {
		return err
	}
	return nil
}

func validateCapabilityDependencies(provider string, profile CapabilityProfile) error {
	if profile.SupportsStructuredOutputWithTools && !profile.SupportsStructuredOutput {
		return fmt.Errorf("capabilities.%s only works with capabilities.%s set to true", "supports_structured_output_with_tools", "supports_structured_output")
	}
	if profile.SupportsStructuredOutputWithCitations && !profile.SupportsStructuredOutput {
		return fmt.Errorf("capabilities.%s only works with capabilities.%s set to true", "supports_structured_output_with_citations", "supports_structured_output")
	}
	if profile.SupportsStructuredOutputWithTools && profile.ExposesCitations && !profile.SupportsStructuredOutputWithCitations {
		return fmt.Errorf("capabilities.%s only works with capabilities.%s set to true, because %s returns citations", "supports_structured_output_with_tools", "supports_structured_output_with_citations", providerDisplayName(provider))
	}
	return nil
}

// positiveCapabilityOverrides lists the features a provider may turn on in
// llmention.yaml, beyond what llmention assumes it can do.
func positiveCapabilityOverrides(provider string) map[string]struct{} {
	switch provider {
	case "google":
		return map[string]struct{}{
			"supports_structured_output_with_tools":     {},
			"supports_structured_output_with_citations": {},
		}
	case "perplexity":
		return map[string]struct{}{
			"supports_structured_output":                {},
			"supports_structured_output_with_tools":     {},
			"supports_structured_output_with_citations": {},
		}
	case "anthropic":
		return map[string]struct{}{
			"supports_structured_output": {},
		}
	case "openrouter":
		return map[string]struct{}{
			"inherently_search_grounded":                {},
			"supports_structured_output":                {},
			"supports_structured_output_with_tools":     {},
			"supports_structured_output_with_citations": {},
		}
	case "openai-compatible":
		return map[string]struct{}{
			"supports_structured_output": {},
			"exposes_citations":          {},
			"exposes_tool_calls":         {},
		}
	default:
		return nil
	}
}

func capabilityValue(profile CapabilityProfile, key string) bool {
	switch key {
	case "supports_memory":
		return profile.SupportsMemory
	case "supports_search_auto":
		return profile.SupportsSearchAuto
	case "supports_search_required":
		return profile.SupportsSearchRequired
	case "supports_search_cache_only":
		return profile.SupportsSearchCacheOnly
	case "supports_live_access_control":
		return profile.SupportsLiveAccessControl
	case "supports_required_tool_choice":
		return profile.SupportsRequiredToolChoice
	case "supports_search_allow_domains":
		return profile.SupportsSearchAllowDomains
	case "supports_search_block_domains":
		return profile.SupportsSearchBlockDomains
	case "supports_search_location":
		return profile.SupportsSearchLocation
	case "supports_search_recency":
		return profile.SupportsSearchRecency
	case "supports_search_date_filters":
		return profile.SupportsSearchDateFilters
	case "supports_search_max_queries":
		return profile.SupportsSearchMaxQueries
	case "supports_source_metadata_request":
		return profile.SupportsSourceMetadataRequest
	case "inherently_search_grounded":
		return profile.InherentlySearchGrounded
	case "exposes_citations":
		return profile.ExposesCitations
	case "exposes_consulted_sources":
		return profile.ExposesConsultedSources
	case "exposes_tool_calls":
		return profile.ExposesToolCalls
	case "supports_structured_output":
		return profile.SupportsStructuredOutput
	case "supports_structured_output_with_tools":
		return profile.SupportsStructuredOutputWithTools
	case "supports_structured_output_with_citations":
		return profile.SupportsStructuredOutputWithCitations
	default:
		return false
	}
}

func applyCapabilityOverrides(profile *CapabilityProfile, overrides map[string]any) error {
	for key, value := range overrides {
		switch key {
		case "supports_memory":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsMemory = v
		case "supports_search_auto":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchAuto = v
		case "supports_search_required":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchRequired = v
		case "supports_search_cache_only":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchCacheOnly = v
		case "supports_live_access_control":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsLiveAccessControl = v
		case "supports_required_tool_choice":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsRequiredToolChoice = v
		case "supports_search_allow_domains":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchAllowDomains = v
		case "supports_search_block_domains":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchBlockDomains = v
		case "supports_search_location":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchLocation = v
		case "supports_search_recency":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchRecency = v
		case "supports_search_date_filters":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchDateFilters = v
		case "supports_search_max_queries":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSearchMaxQueries = v
		case "supports_source_metadata_request":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsSourceMetadataRequest = v
		case "inherently_search_grounded":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.InherentlySearchGrounded = v
		case "exposes_citations":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.ExposesCitations = v
		case "exposes_consulted_sources":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.ExposesConsultedSources = v
		case "exposes_tool_calls":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.ExposesToolCalls = v
		case "supports_structured_output":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsStructuredOutput = v
		case "supports_structured_output_with_tools":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsStructuredOutputWithTools = v
		case "supports_structured_output_with_citations":
			v, err := boolCapability(key, value)
			if err != nil {
				return err
			}
			profile.SupportsStructuredOutputWithCitations = v
		case "knowledge_cutoff":
			v, ok := value.(string)
			if !ok {
				return fmt.Errorf("capabilities.%s must be a string", key)
			}
			profile.KnowledgeCutoff = v
		default:
			return fmt.Errorf("capabilities has no setting %q", key)
		}
	}
	return nil
}

func boolCapability(key string, value any) (bool, error) {
	v, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("capabilities.%s must be true or false", key)
	}
	return v, nil
}

type baseEngine struct {
	id       string
	provider string
	model    string
	keyEnv   string
	baseURL  string
	caps     CapabilityProfile
	opts     adapterOptions
	// noRedirects stops the client from following redirects, so a redirect
	// from a self-hosted gateway never sends the key to another host.
	noRedirects bool
}

func (e *baseEngine) ID() string {
	return e.id
}

func (e *baseEngine) Provider() string {
	return e.provider
}

func (e *baseEngine) Model() string {
	return e.model
}

func (e *baseEngine) Capabilities() CapabilityProfile {
	return e.caps
}

func (e *baseEngine) ValidateRequest(req Request) error {
	return ValidateRequest(e.caps, req)
}

func (e *baseEngine) resolveKey() (string, error) {
	if e.keyEnv == "" {
		return "", nil
	}
	value, ok := e.opts.secretResolver.lookup(e.keyEnv)
	if !ok || value == "" {
		return "", &ProviderError{
			ProviderID:  e.provider,
			SafeMessage: fmt.Sprintf("the API key %s is not set", e.keyEnv),
			Class:       ErrorClassConfig,
		}
	}
	e.opts.secretResolver.register(value)
	return value, nil
}

// stubEngine stands in for a provider that has no adapter. Every request
// fails with a clear error.
type stubEngine struct {
	baseEngine
}

func (e *stubEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return Answer{}, err
	}
	if err := e.ValidateRequest(req); err != nil {
		return Answer{}, &ProviderError{
			ProviderID:  e.provider,
			SafeMessage: err.Error(),
			Class:       ErrorClassCaller,
		}
	}
	if _, err := e.resolveKey(); err != nil {
		return Answer{}, err
	}
	return Answer{}, &ProviderError{
		ProviderID:  e.provider,
		SafeMessage: fmt.Sprintf("llmention cannot send requests to provider %q yet", e.provider),
		Class:       ErrorClassConfig,
	}
}

func (f *Factory) newMockEngine(base baseEngine, fixturesPath string, configOverrides config.Capabilities) (Engine, error) {
	fixtures := base.opts.mockFixtures[base.id]
	if fixtures == nil && fixturesPath != "" {
		path := fixturesPath
		if !filepath.IsAbs(path) {
			configDir := base.opts.configDir
			if configDir == "" {
				configDir = "."
			}
			path = filepath.Join(configDir, path)
		}
		loaded, err := LoadMockFixtures(path)
		if err != nil {
			return nil, fmt.Errorf("could not load the mock answers for engine %q: %w", base.id, err)
		}
		fixtures = loaded
	}
	return newMockEngine(base, fixtures, configOverrides)
}

func cloneMockFixtureSetMap(in map[string]*MockFixtureSet) map[string]*MockFixtureSet {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*MockFixtureSet, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
