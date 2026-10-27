package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/config"
)

func TestFactoryBuildsMockAndJudgeAndAppliesOverrides(t *testing.T) {
	fixtures := &MockFixtureSet{
		Capabilities: CapabilityOverrides{
			"supports_search_required": false,
			"knowledge_cutoff":         "fixture-cutoff",
		},
		Defaults: MockDefaults{Answer: &Answer{Text: "fixture default"}},
	}
	project := &config.Project{
		Version: 1,
		Engines: []config.EngineConfig{{
			ID:       "mock",
			Provider: "mock",
			Model:    "fixture",
			Capabilities: config.Capabilities{
				"supports_search_required": true,
				"knowledge_cutoff":         "config-cutoff",
			},
		}},
		Analysis: config.AnalysisConfig{Judge: &config.JudgeConfig{
			Provider: "mock",
			Model:    "judge-fixture",
		}},
	}
	factory := NewFactory(FactoryOptions{
		MockFixtureSets: map[string]*MockFixtureSet{
			"mock": fixtures,
		},
	})
	constructed, err := factory.NewProjectEngines(project)
	if err != nil {
		t.Fatalf("NewProjectEngines() error = %v", err)
	}
	if len(constructed.Engines) != 1 || constructed.Engines[0].ID() != "mock" {
		t.Fatalf("engines = %#v", constructed.Engines)
	}
	caps := constructed.Engines[0].Capabilities()
	if !caps.SupportsSearchRequired || caps.KnowledgeCutoff != "config-cutoff" {
		t.Fatalf("capability overrides = %#v", caps)
	}
	if constructed.Judge == nil || constructed.Judge.ID() != JudgeEngineID || constructed.Judge.Provider() != "mock" {
		t.Fatalf("judge = %#v", constructed.Judge)
	}
}

func TestFactoryOmitsAbsentJudge(t *testing.T) {
	project := &config.Project{
		Version: 1,
		Engines: []config.EngineConfig{{
			ID:       "mock",
			Provider: "mock",
			Model:    "fixture",
		}},
	}
	constructed, err := NewFactory(FactoryOptions{}).NewProjectEngines(project)
	if err != nil {
		t.Fatalf("NewProjectEngines() error = %v", err)
	}
	if constructed.Judge != nil {
		t.Fatalf("judge = %#v, want nil", constructed.Judge)
	}
}

func TestFactoryBuildsTargetsWithoutJudge(t *testing.T) {
	project := &config.Project{
		Version: 1,
		Engines: []config.EngineConfig{{
			ID:       "mock",
			Provider: "mock",
			Model:    "fixture",
		}},
		Analysis: config.AnalysisConfig{Judge: &config.JudgeConfig{
			Provider: "unknown",
			Model:    "judge-fixture",
		}},
	}
	targets, err := NewFactory(FactoryOptions{}).NewTargetEngines(project)
	if err != nil {
		t.Fatalf("NewTargetEngines() error = %v", err)
	}
	if len(targets) != 1 || targets[0].ID() != "mock" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestMockWithoutWorldAnswersWithTheDefault(t *testing.T) {
	mock, err := NewFactory(FactoryOptions{}).NewEngine(config.EngineConfig{ID: "mock", Provider: "mock", Model: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := mock.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil || answer.Text != "Mock answer" {
		t.Fatalf("answer = %q err = %v", answer.Text, err)
	}
}

func TestFactoryBuildsOpenAIAndResolvesKeyOnlyOnAsk(t *testing.T) {
	var lookups []string
	var registered []string
	factory := NewFactory(FactoryOptions{
		LookupEnv: func(name string) (string, bool) {
			lookups = append(lookups, name)
			return "sk-test", true
		},
		RegisterSecret: func(value string) {
			registered = append(registered, value)
		},
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"id": "resp_factory",
				"status": "completed",
				"output": [{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
			}`), nil
		}),
	})
	engine, err := factory.NewEngine(config.EngineConfig{
		ID:       "openai-gpt",
		Provider: "openai",
		Model:    "gpt-test",
		KeyEnv:   "OPENAI_API_KEY",
		Capabilities: config.Capabilities{
			"supports_memory": true,
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if engine.ID() != "openai-gpt" || engine.Provider() != "openai" || engine.Model() != "gpt-test" {
		t.Fatalf("engine identity = %s/%s/%s", engine.ID(), engine.Provider(), engine.Model())
	}
	if len(lookups) != 0 {
		t.Fatalf("factory construction read env: %#v", lookups)
	}
	if err := engine.ValidateRequest(validRequest(ToolPolicyMemory)); err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
	if len(lookups) != 0 {
		t.Fatalf("ValidateRequest read env: %#v", lookups)
	}
	answer, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Text != "ok" {
		t.Fatalf("answer.Text = %q", answer.Text)
	}
	if len(lookups) != 1 || lookups[0] != "OPENAI_API_KEY" {
		t.Fatalf("lookups = %#v", lookups)
	}
	if len(registered) != 1 || registered[0] != "sk-test" {
		t.Fatalf("registered = %#v", registered)
	}
}

func TestFactoryProviderMissingKeyIsConfigProviderError(t *testing.T) {
	engine, err := NewFactory(FactoryOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
	}).NewEngine(config.EngineConfig{
		ID:       "openai-gpt",
		Provider: "openai",
		Model:    "gpt-test",
		KeyEnv:   "OPENAI_API_KEY",
		Capabilities: config.Capabilities{
			"supports_memory": true,
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	_, err = engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
	}
	if providerErr.Class != ErrorClassConfig || !strings.Contains(providerErr.SafeMessage, "OPENAI_API_KEY") {
		t.Fatalf("providerErr = %#v", providerErr)
	}
}

func TestFactoryRejectsUnknownProviderAndInvalidOverrides(t *testing.T) {
	factory := NewFactory(FactoryOptions{})
	_, err := factory.NewEngine(config.EngineConfig{ID: "bad", Provider: "unknown", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown provider error = %v", err)
	}
	_, err = factory.NewEngine(config.EngineConfig{ID: "openai-gpt", Provider: "openai", Model: "gpt-test"})
	if err == nil || !strings.Contains(err.Error(), "key_env is required") {
		t.Fatalf("missing key env error = %v", err)
	}
	_, err = factory.NewEngine(config.EngineConfig{ID: "openai-compatible", Provider: "openai-compatible", Model: "llama"})
	if err == nil || !strings.Contains(err.Error(), "base_url is required") {
		t.Fatalf("missing base URL error = %v", err)
	}
	_, err = factory.NewEngine(config.EngineConfig{
		ID:       "mock",
		Provider: "mock",
		Model:    "fixture",
		Capabilities: config.Capabilities{
			"supports_memory": "true",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be true or false") {
		t.Fatalf("invalid override error = %v", err)
	}
}

func TestFactoryInjectsHTTPClientTransportAndBaseURL(t *testing.T) {
	transport := &recordingRoundTripper{}
	factory := NewFactory(FactoryOptions{
		Transport: transport,
		BaseURLs:  map[string]string{"openai": "https://example.test/v1"},
	})
	engine, err := factory.NewEngine(config.EngineConfig{
		ID:       "openai-gpt",
		Provider: "openai",
		Model:    "gpt-test",
		KeyEnv:   "OPENAI_API_KEY",
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	openai, ok := engine.(*openAIEngine)
	if !ok {
		t.Fatalf("engine type = %T, want *openAIEngine", engine)
	}
	if openai.opts.httpClient == nil || openai.opts.httpClient.Transport != transport {
		t.Fatalf("http client transport was not injected: %#v", openai.opts.httpClient)
	}
	if openai.baseURL != "https://example.test/v1" {
		t.Fatalf("base URL = %q", openai.baseURL)
	}
}

func TestFactoryBuildsChatCompletionsEndpointFromBaseURL(t *testing.T) {
	for base, want := range map[string]string{
		"http://localhost:11434/v1":                           "http://localhost:11434/v1/chat/completions",
		"http://localhost:11434/v1/":                          "http://localhost:11434/v1/chat/completions",
		"https://api.groq.example/openai/v1/chat/completions": "https://api.groq.example/openai/v1/chat/completions",
	} {
		engine, err := NewFactory(FactoryOptions{}).NewEngine(config.EngineConfig{ID: "ollama", Provider: "openai-compatible", Model: "llama", BaseURL: base})
		if err != nil {
			t.Fatalf("NewEngine(%s) error = %v", base, err)
		}
		if got := engine.(*compatibleEngine).endpoint; got != want {
			t.Fatalf("endpoint for %s = %q, want %q", base, got, want)
		}
	}
}

type recordingRoundTripper struct{}

func (*recordingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}
