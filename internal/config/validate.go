package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/domain"
)

var (
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidateShape checks the project file and fills in what follows from it,
// for example the prompt ids and the full names of the modes.
func (cfg *Project) ValidateShape() error {
	if cfg == nil {
		return fmt.Errorf("project config is nil")
	}
	if cfg.Version != 1 {
		return fmt.Errorf("version must be 1; put `version: 1` at the top of the file")
	}
	if cfg.RunsDir == "" {
		cfg.RunsDir = DefaultRunsDir
	}
	for _, check := range []func() error{
		cfg.validateBrand,
		cfg.validateCompetitors,
		cfg.validateEngines,
		cfg.validatePrompts,
		cfg.validateModes,
		cfg.validateSuites,
		cfg.validateExecution,
		cfg.validateAnalysis,
		cfg.validateNotify,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// ValidateForRun checks that a run has something to send and somewhere to
// send it.
func (cfg *Project) ValidateForRun() error {
	if err := cfg.ValidateShape(); err != nil {
		return err
	}
	if len(cfg.Engines) == 0 {
		return fmt.Errorf("add at least one engine under engines: in llmention.yaml")
	}
	return nil
}

// CheckModels rejects engines whose model is still a placeholder, so a dry
// run fails before anyone exports keys or pays for a request.
func (cfg *Project) CheckModels(engineIDs []string) error {
	for _, e := range cfg.Engines {
		if len(engineIDs) > 0 && !slices.Contains(engineIDs, e.ID) {
			continue
		}
		if IsPlaceholderModel(e.Model) {
			return fmt.Errorf("engine %q still uses the placeholder model %q; replace it with a model id your key can use (see docs/providers.md)", e.ID, e.Model)
		}
	}
	return nil
}

// IsPlaceholderModel reports model names that only mark where a real model
// id belongs.
func IsPlaceholderModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "replace-with") || strings.Contains(m, "<") || strings.Contains(m, "your-model")
}

func (cfg *Project) validateBrand() error {
	b := cfg.Brand
	if b == nil {
		return nil
	}
	b.Name = strings.TrimSpace(b.Name)
	b.Category = strings.TrimSpace(b.Category)
	if b.Name == "" {
		return fmt.Errorf("brand.name is required when brand is set")
	}
	b.Aliases = trimList(b.Aliases)
	b.Products = trimList(b.Products)
	b.People = trimList(b.People)
	domains, err := SiteDomains(trimList(b.Domains))
	if err != nil {
		return fmt.Errorf("brand.domains: %w", err)
	}
	b.Domains = domains
	return nil
}

func (cfg *Project) validateCompetitors() error {
	seen := map[string]bool{}
	if cfg.Brand != nil {
		seen[strings.ToLower(cfg.Brand.Name)] = true
	}
	for i := range cfg.Competitors {
		c := &cfg.Competitors[i]
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			return fmt.Errorf("competitors[%d].name is required", i)
		}
		key := strings.ToLower(c.Name)
		if seen[key] {
			return fmt.Errorf("competitor %q is listed twice or matches your brand name", c.Name)
		}
		seen[key] = true
		c.Aliases = trimList(c.Aliases)
		domains, err := SiteDomains(trimList(c.Domains))
		if err != nil {
			return fmt.Errorf("competitors[%d].domains: %w", i, err)
		}
		c.Domains = domains
	}
	return nil
}

// SiteDomains normalizes the sites of a brand. A site counts as a whole: every
// page on the host and its subdomains. An entry with a path such as
// github.com/acme/books is refused, because cutting it to github.com would
// count every GitHub page as that brand's.
func SiteDomains(values []string) ([]string, error) {
	for _, v := range values {
		rest := v
		if _, after, ok := strings.Cut(v, "://"); ok {
			rest = after
		}
		if i := strings.IndexAny(rest, "/?#"); i >= 0 && strings.Trim(rest[i:], "/") != "" {
			return nil, fmt.Errorf("%q has a path; llmention counts whole sites, so give the host only (for example %s), or leave the entry out if the host is shared, such as github.com", v, strings.TrimPrefix(rest[:i], "www."))
		}
	}
	return domain.NormalizeScoringDomains(values)
}

func (cfg *Project) validateEngines() error {
	seen := map[string]bool{}
	for i := range cfg.Engines {
		e := &cfg.Engines[i]
		e.ID = strings.TrimSpace(e.ID)
		e.Provider = strings.TrimSpace(e.Provider)
		e.Model = strings.TrimSpace(e.Model)
		e.KeyEnv = strings.TrimSpace(e.KeyEnv)
		e.BaseURL = strings.TrimSpace(e.BaseURL)
		prefix := fmt.Sprintf("engines[%d]", i)
		if e.ID == "" {
			return fmt.Errorf("%s.id is required", prefix)
		}
		if !idPattern.MatchString(e.ID) {
			return fmt.Errorf("%s.id %q may only use letters, digits, dots, dashes and underscores", prefix, e.ID)
		}
		if seen[e.ID] {
			return fmt.Errorf("engine id %q is used twice", e.ID)
		}
		seen[e.ID] = true
		if err := validateEngineLike(prefix, e.Provider, e.Model, e.KeyEnv, e.BaseURL, e.FixturesPath, e.Capabilities); err != nil {
			return err
		}
		if e.MaxOutputTokens < 0 {
			return fmt.Errorf("%s.max_output_tokens must be 1 or more, or left out", prefix)
		}
	}
	return nil
}

func (cfg *Project) validatePrompts() error {
	used := map[string]bool{}
	seenText := map[string]bool{}
	var kept []Prompt
	for i := range cfg.Prompts {
		p := cfg.Prompts[i]
		p.ID = strings.TrimSpace(p.ID)
		p.Text = strings.TrimSpace(p.Text)
		p.Tags = trimList(p.Tags)
		if p.Text == "" {
			return fmt.Errorf("prompts[%d] is empty", i)
		}
		// The same question in prompts and prompts_file is asked once, so it
		// is neither paid for nor counted twice.
		key := promptKey(p.Text)
		if seenText[key] && p.ID == "" {
			continue
		}
		seenText[key] = true
		if p.ID != "" {
			if !idPattern.MatchString(p.ID) {
				return fmt.Errorf("prompts[%d].id %q may only use letters, digits, dots, dashes and underscores", i, p.ID)
			}
			if used[p.ID] {
				return fmt.Errorf("prompt id %q is used twice", p.ID)
			}
			used[p.ID] = true
		}
		kept = append(kept, p)
	}
	cfg.Prompts = kept
	for i := range cfg.Prompts {
		p := &cfg.Prompts[i]
		if p.ID != "" {
			continue
		}
		id := PromptID(p.Text)
		if used[id] {
			return fmt.Errorf("prompt %q gets the id %q, which another prompt already uses; give one of them an id", p.Text, id)
		}
		used[id] = true
		p.ID = id
	}
	if len(cfg.Prompts) > 0 && cfg.Brand == nil {
		return fmt.Errorf("prompts need a brand; add brand.name to llmention.yaml")
	}
	return nil
}

// PromptID builds a short, readable id from prompt text, such as
// "best-tools-ai-api-cost-tracking-3f9a". The words make it readable; the
// hash of the full text keeps two prompts apart when their words are the same,
// as in "Best C++ IDE?" and "Best C# IDE?". Rewording a prompt gives it a new
// id, so a trend never mixes answers to two different questions. To keep the
// history of a prompt you reword, give it an id in llmention.yaml.
func PromptID(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	id := ""
	for _, w := range words {
		if idStopWords[w] {
			continue
		}
		if len(id)+len(w)+1 > 40 {
			break
		}
		id = strings.TrimPrefix(id+"-"+w, "-")
	}
	sum := sha256.Sum256([]byte(promptKey(text)))
	return strings.TrimPrefix(id+"-"+hex.EncodeToString(sum[:2]), "-")
}

// promptKey ignores case and spacing, which do not change the question.
func promptKey(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

var idStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "what": true, "which": true, "who": true, "how": true,
	"is": true, "are": true, "do": true, "does": true, "i": true, "you": true, "me": true, "my": true,
	"for": true, "of": true, "to": true, "in": true, "on": true, "at": true, "with": true,
	"should": true, "would": true, "could": true, "can": true, "some": true, "any": true, "these": true,
}

func (cfg *Project) validateModes() error {
	if len(cfg.Modes) == 0 {
		cfg.Modes = slices.Clone(DefaultModes)
	}
	seen := map[string]bool{}
	for i, m := range cfg.Modes {
		mode, err := CanonicalMode(m)
		if err != nil {
			return fmt.Errorf("modes[%d]: %w", i, err)
		}
		if seen[mode] {
			return fmt.Errorf("mode %q is listed twice", mode)
		}
		seen[mode] = true
		cfg.Modes[i] = mode
	}
	if cfg.Repetitions <= 0 {
		return fmt.Errorf("repetitions must be 1 or more")
	}
	return nil
}

func (cfg *Project) validateSuites() error {
	for i, path := range cfg.Suites.Paths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("suites.paths[%d] must not be empty", i)
		}
		cfg.Suites.Paths[i] = strings.TrimSpace(path)
	}
	return nil
}

func (cfg *Project) validateExecution() error {
	e := cfg.Execution
	switch {
	case e.MaxRequests <= 0:
		return fmt.Errorf("execution.max_requests must be 1 or more")
	case e.Retries < 0:
		return fmt.Errorf("execution.retries must be 0 or more")
	case e.MaxJudgeRequests < 0:
		return fmt.Errorf("execution.max_judge_requests must be 0 or more")
	case e.Timeout <= 0:
		return fmt.Errorf("execution.timeout must be longer than zero")
	case e.Concurrency <= 0:
		return fmt.Errorf("execution.concurrency must be 1 or more")
	}
	if cfg.Execution.MaxJudgeRequests == 0 {
		cfg.Execution.MaxJudgeRequests = DefaultMaxJudgeRequests
	}
	return nil
}

func (cfg *Project) validateAnalysis() error {
	j := cfg.Analysis.Judge
	if j == nil {
		if cfg.Analysis.ModelAssistedEnabled {
			return fmt.Errorf("analysis.model_assisted_enabled turns on the AI grader, which needs a model under analysis.judge")
		}
		return nil
	}
	j.Provider = strings.TrimSpace(j.Provider)
	j.Model = strings.TrimSpace(j.Model)
	j.KeyEnv = strings.TrimSpace(j.KeyEnv)
	j.BaseURL = strings.TrimSpace(j.BaseURL)
	return validateEngineLike("analysis.judge", j.Provider, j.Model, j.KeyEnv, j.BaseURL, j.FixturesPath, j.Capabilities)
}

func (cfg *Project) validateNotify() error {
	cfg.Notify.Command = strings.TrimSpace(cfg.Notify.Command)
	cfg.Notify.WebhookURLEnv = strings.TrimSpace(cfg.Notify.WebhookURLEnv)
	if cfg.Notify.WebhookURLEnv != "" && !envNamePattern.MatchString(cfg.Notify.WebhookURLEnv) {
		return fmt.Errorf("notify.webhook_url_env %q is not a valid environment variable name; use letters, digits and underscores, such as LLMENTION_WEBHOOK_URL", cfg.Notify.WebhookURLEnv)
	}
	return nil
}

func validateEngineLike(prefix, provider, model, keyEnv, baseURL, fixturesPath string, capabilities Capabilities) error {
	if provider == "" {
		return fmt.Errorf("%s.provider is required", prefix)
	}
	if _, ok := KnownProviders[provider]; !ok {
		return fmt.Errorf("%s.provider %q is not supported; use openai, anthropic, google, perplexity, openrouter, openai-compatible or mock", prefix, provider)
	}
	if model == "" {
		return fmt.Errorf("%s.model is required", prefix)
	}
	if ProviderNeedsKey(provider) && keyEnv == "" {
		return fmt.Errorf("%s.key_env is required for provider %q", prefix, provider)
	}
	if keyEnv != "" && !envNamePattern.MatchString(keyEnv) {
		return fmt.Errorf("%s.key_env %q is not a valid environment variable name; use letters, digits and underscores, such as OPENAI_API_KEY", prefix, keyEnv)
	}
	switch {
	case provider == "openai-compatible" && baseURL == "":
		return fmt.Errorf("%s.base_url is required for provider \"openai-compatible\", for example http://localhost:11434/v1 for Ollama", prefix)
	case provider == "mock" && baseURL != "":
		return fmt.Errorf("%s.base_url is not used by the mock provider", prefix)
	}
	if baseURL != "" {
		if err := ValidateBaseURL(prefix+".base_url", baseURL, keyEnv); err != nil {
			return err
		}
	}
	if fixturesPath != "" {
		if provider != "mock" {
			return fmt.Errorf("%s.fixtures_path is only used by the mock provider", prefix)
		}
		if u, err := url.Parse(fixturesPath); (err == nil && u.Scheme != "") || filepath.Clean(fixturesPath) == "." {
			return fmt.Errorf("%s.fixtures_path must be a local file path", prefix)
		}
	}
	return validateCapabilities(prefix+".capabilities", capabilities)
}

// ProviderNeedsKey reports whether a provider always needs an API key.
func ProviderNeedsKey(provider string) bool {
	switch provider {
	case "openai", "anthropic", "google", "perplexity", "openrouter":
		return true
	}
	return false
}

func validateCapabilities(prefix string, capabilities Capabilities) error {
	for key, value := range capabilities {
		want, ok := capabilityOverrideTypes[key]
		if !ok {
			return fmt.Errorf("%s has no setting %q", prefix, key)
		}
		switch want {
		case "bool":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s.%s must be true or false", prefix, key)
			}
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s.%s must be a string", prefix, key)
			}
		}
	}
	return nil
}

// ValidateBaseURL accepts full http and https URLs. A key only travels over
// plain http when the host is this machine.
func ValidateBaseURL(field, raw, keyEnv string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s %q must be an absolute http or https URL", field, raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must not include a query string or fragment", field)
	}
	if _, err := domain.NormalizeEndpointHost(raw); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if keyEnv != "" && u.Scheme != "https" && !IsLoopbackHost(u.Hostname()) {
		return fmt.Errorf("%s must use https because the API key would otherwise travel unencrypted", field)
	}
	return nil
}

// IsLoopbackHost reports whether host names this machine.
func IsLoopbackHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// SupportedCapabilityKeys lists the keys accepted under capabilities.
func SupportedCapabilityKeys() []string {
	return slices.Sorted(maps.Keys(capabilityOverrideTypes))
}

func trimList(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
