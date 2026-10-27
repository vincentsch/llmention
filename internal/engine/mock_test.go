package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

func TestMockFixturesRejectTrailingYAMLDocuments(t *testing.T) {
	for _, suffix := range []string{"\n---\nunknown_field: true\n", "\n---\n", "\n---\n[broken"} {
		if _, err := LoadMockFixturesBytes([]byte("version: 1\n" + suffix)); err == nil {
			t.Fatalf("accepted mock fixtures with trailing document %q", suffix)
		}
	}
}

func TestMockFixtureFileResolutionAndAnswerMetadata(t *testing.T) {
	fixturesYAML := `
version: 1
defaults:
  answer:
    text: "global"
    tools_available: []
capabilities:
  supports_memory: true
  supports_search_auto: true
fixtures:
  - match:
      mode: "search_auto"
    answer:
      text: "mode default"
  - match:
      suite_id: "visibility.basic"
      item_id: "visibility.example"
      mode: "search_auto"
    answer:
      text: "suite item"
  - match:
      engine_id: "mock"
      suite_id: "visibility.basic"
      item_id: "visibility.example"
      variant_id: "direct"
      mode: "search_auto"
    answer:
      text: "specific"
      parsed_json:
        mentioned: true
        rank: 1
      raw: "{\"fixture\":true}"
      citations:
        - url: "https://Acme.example/example"
          title: "Example"
      sources_consulted:
        - url: "https://acme.example/source"
          title: "Source"
          kind: "web"
      tools_available: ["web_search"]
      tools_used: ["web_search"]
      tool_calls:
        - type: "web_search"
          provider_tool_call_id: "fixture-tool-call-1"
          query: "Acme"
          status: "completed"
          raw_base64: "e30="
      provider_metadata:
        fixture_id: "visibility-example"
      stop_reason: "stop"
`
	set, err := LoadMockFixturesBytes([]byte(fixturesYAML))
	if err != nil {
		t.Fatalf("LoadMockFixturesBytes() error = %v", err)
	}
	engine, err := NewMockEngine("mock", "fixture", set)
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	req := validRequest(ToolPolicySearchAuto)
	answer, err := engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Text != "specific" {
		t.Fatalf("answer.Text = %q", answer.Text)
	}
	if string(answer.Parsed) != `{"mentioned":true,"rank":1}` {
		t.Fatalf("Parsed = %s", answer.Parsed)
	}
	if string(answer.Raw) != `{"fixture":true}` {
		t.Fatalf("Raw = %s", answer.Raw)
	}
	if got := answer.Citations[0].Domain; got != "acme.example" {
		t.Fatalf("citation domain = %q", got)
	}
	if got := answer.SourcesConsulted[0].Domain; got != "acme.example" {
		t.Fatalf("source domain = %q", got)
	}
	if got := answer.ToolCalls[0].ProviderToolCallID; got != "fixture-tool-call-1" {
		t.Fatalf("tool call id = %q", got)
	}
	if string(answer.ToolCalls[0].Raw) != "{}" {
		t.Fatalf("tool call raw = %q", answer.ToolCalls[0].Raw)
	}
	if !reflect.DeepEqual(answer.ToolsAvailable, []string{"web_search"}) || !reflect.DeepEqual(answer.ToolsUsed, []string{"web_search"}) {
		t.Fatalf("tools available/used = %#v/%#v", answer.ToolsAvailable, answer.ToolsUsed)
	}
	if answer.ProviderMetadata["fixture_id"] != "visibility-example" || answer.StopReason != "stop" {
		t.Fatalf("metadata/stop = %#v/%q", answer.ProviderMetadata, answer.StopReason)
	}

	req.VariantID = "other"
	answer, err = engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() less specific error = %v", err)
	}
	if answer.Text != "suite item" {
		t.Fatalf("less specific answer.Text = %q", answer.Text)
	}
	req.ItemID = "other"
	answer, err = engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() mode default error = %v", err)
	}
	if answer.Text != "mode default" {
		t.Fatalf("mode default answer.Text = %q", answer.Text)
	}
	req.Mode = ToolPolicyMemory
	answer, err = engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() global default error = %v", err)
	}
	if answer.Text != "global" {
		t.Fatalf("global answer.Text = %q", answer.Text)
	}
}

func TestMockPromptFixtureForcedErrorAndRetryAfter(t *testing.T) {
	engine, err := NewMockEngine("mock", "fixture", &MockFixtureSet{
		Fixtures: []MockFixture{{
			Match: MockMatch{Mode: ToolPolicyMemory, Prompt: "force failure"},
			Error: &MockError{
				Class:        ErrorClassProvider,
				Message:      "forced mock error",
				Retryable:    true,
				RequestID:    "mock-req",
				StatusCode:   503,
				RetryAfterMS: 1500,
				SafeSnippet:  strings.Repeat("x", MaxProviderErrorSnippetBytes+1),
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	req := validRequest(ToolPolicyMemory)
	req.Prompt = "force failure"
	_, err = engine.Ask(context.Background(), req)
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
	}
	if providerErr.Class != ErrorClassProvider || providerErr.RequestID != "mock-req" || providerErr.StatusCode != 503 || !providerErr.Retryable {
		t.Fatalf("providerErr = %#v", providerErr)
	}
	if providerErr.RetryAfter == nil || *providerErr.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("RetryAfter = %#v", providerErr.RetryAfter)
	}
	if len(providerErr.SafeSnippet) != MaxProviderErrorSnippetBytes {
		t.Fatalf("SafeSnippet len = %d", len(providerErr.SafeSnippet))
	}
}

func TestMockRawPayloadCapAndDeterministicClone(t *testing.T) {
	raw := []byte(strings.Repeat("a", MaxRawPayloadBytes+20))
	engine, err := NewMockEngine("mock", "fixture", &MockFixtureSet{
		Defaults: MockDefaults{Answer: &Answer{
			Text:             "default",
			Raw:              raw,
			ProviderMetadata: map[string]string{"fixture": "true"},
		}},
	})
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	req := validRequest(ToolPolicyMemory)
	first, err := engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	second, err := engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() second error = %v", err)
	}
	if len(first.Raw) != MaxRawPayloadBytes || first.ProviderMetadata["raw_truncated"] != "true" {
		t.Fatalf("raw cap/metadata = %d/%#v", len(first.Raw), first.ProviderMetadata)
	}
	first.ProviderMetadata["fixture"] = "changed"
	first.Raw[0] = 'b'
	if second.ProviderMetadata["fixture"] != "true" || second.Raw[0] != 'a' {
		t.Fatalf("answers were not cloned independently: %#v %q", second.ProviderMetadata, second.Raw[:1])
	}
}

func TestMockFixturePathRelativeToConfigDir(t *testing.T) {
	dir := t.TempDir()
	fixtureDir := filepath.Join(dir, "fixtures")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	fixturePath := filepath.Join(fixtureDir, "mock.yaml")
	if err := os.WriteFile(fixturePath, []byte(`
version: 1
defaults:
  answer:
    text: "from file"
`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	engine, err := NewFactory(FactoryOptions{ConfigDir: dir}).NewEngine(config.EngineConfig{
		ID:           "mock",
		Provider:     "mock",
		Model:        "fixture",
		FixturesPath: "./fixtures/mock.yaml",
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	answer, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Text != "from file" {
		t.Fatalf("answer.Text = %q", answer.Text)
	}
}

func TestMockFixtureValidationFailures(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown field",
			yaml: `
version: 1
defaults:
  answer:
    text: "ok"
    unexpected: true
`,
			want: "field unexpected not found",
		},
		{
			name: "duplicate fixture",
			yaml: `
version: 1
fixtures:
  - match:
      mode: "memory"
    answer:
      text: "one"
  - match:
      mode: "memory"
    answer:
      text: "two"
`,
			want: "same match as an earlier fixture",
		},
		{
			name: "unsupported match combination",
			yaml: `
version: 1
fixtures:
  - match:
      engine_id: "mock"
      mode: "memory"
    answer:
      text: "bad"
`,
			want: "combination of match fields is not supported",
		},
		{
			name: "raw and raw base64",
			yaml: `
version: 1
defaults:
  answer:
    text: "bad"
    raw: "{}"
    raw_base64: "e30="
`,
			want: "must not both be set",
		},
		{
			name: "domain mismatch",
			yaml: `
version: 1
defaults:
  answer:
    text: "bad"
    citations:
      - url: "https://example.com/path"
        domain: "other.example"
`,
			want: "does not match",
		},
		{
			name: "invalid capability key",
			yaml: `
version: 1
capabilities:
  supports_provider_default: true
`,
			want: "has no setting",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMockFixturesBytes([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadMockFixturesBytes() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMockProgrammaticFixtureRejectsInvalidParsedJSON(t *testing.T) {
	_, err := NewMockEngine("mock", "fixture", &MockFixtureSet{
		Defaults: MockDefaults{Answer: &Answer{
			Text:   "bad parsed",
			Parsed: []byte(`{`),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "parsed JSON is invalid") {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
}

func TestMockContextCancellationAndParsedNull(t *testing.T) {
	set, err := LoadMockFixturesBytes([]byte(`
version: 1
defaults:
  answer:
    text: "null parsed"
    parsed_json: null
`))
	if err != nil {
		t.Fatalf("LoadMockFixturesBytes() error = %v", err)
	}
	engine, err := NewMockEngine("mock", "fixture", set)
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	answer, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if !json.Valid(answer.Parsed) || string(answer.Parsed) != "null" {
		t.Fatalf("Parsed = %q", answer.Parsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = engine.Ask(ctx, validRequest(ToolPolicyMemory))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask canceled error = %v", err)
	}
}
