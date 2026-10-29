package engine

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/config"
)

// The files in testdata/live are real provider responses from a live run on
// 2026-10-29. Encrypted blobs and ids were replaced and account ids removed;
// the rest is what the provider sent.
func liveResponse(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "live", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// liveEngine returns an engine whose every request gets one recorded answer.
// sent receives the request body.
func liveEngine(t *testing.T, provider, model string, status int, body string, sent *map[string]any) Engine {
	t.Helper()
	eng, err := NewFactory(FactoryOptions{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if sent != nil {
				*sent = decodeRequestBody(t, req)
			}
			return jsonResponse(status, body), nil
		}),
		BaseURLs:  map[string]string{provider: providerBaseURL(provider)},
		LookupEnv: func(string) (string, bool) { return "live-test-key", true },
	}).NewEngine(config.EngineConfig{ID: "live", Provider: provider, Model: model, KeyEnv: "KEY"})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestLiveSearchAnswersKeepTheirCitationsAndSearches(t *testing.T) {
	tests := []struct {
		provider, model, file string
		citations, sources    int
		toolCalls             int
		searches              string
	}{
		// Three searches and one opened page. OpenAI bills the searches only.
		{"openai", "gpt-6.1-sol", "openai-search.json", 6, 0, 4, "3"},
		{"anthropic", "claude-sonnet-5-5", "anthropic-search.json", 13, 9, 1, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			eng := liveEngine(t, tt.provider, tt.model, http.StatusOK, liveResponse(t, tt.file), nil)
			answer, err := eng.Ask(context.Background(), validRequest(ToolPolicySearchAuto))
			if err != nil {
				t.Fatal(err)
			}
			if answer.Unfinished || len(answer.Text) < 1000 {
				t.Fatalf("unfinished=%v, text %d bytes", answer.Unfinished, len(answer.Text))
			}
			if len(answer.Citations) != tt.citations || len(answer.SourcesConsulted) != tt.sources || len(answer.ToolCalls) != tt.toolCalls {
				t.Fatalf("citations=%d sources=%d tool calls=%d", len(answer.Citations), len(answer.SourcesConsulted), len(answer.ToolCalls))
			}
			for _, c := range answer.Citations {
				if c.Domain == "" || !strings.HasPrefix(c.URL, "https://") {
					t.Fatalf("citation without a site: %#v", c)
				}
			}
			if got := answer.ProviderMetadata["usage_search_queries"]; got != tt.searches {
				t.Fatalf("usage_search_queries = %q, want %s", got, tt.searches)
			}
		})
	}
}

func TestLiveAnswersCutOffAtTheTokenLimitAreUnfinished(t *testing.T) {
	tests := []struct {
		provider, model, file, stop string
		wantText                    bool
	}{
		{"openai", "gpt-6.1-sol", "openai-cutoff.json", "max_output_tokens", true},
		// Claude spent all 200 tokens thinking and wrote no text.
		{"anthropic", "claude-sonnet-5-5", "anthropic-cutoff-thinking-only.json", "max_tokens", false},
		// OpenRouter sends blank lines before the JSON while it works.
		{"openrouter", "perplexity/sonar", "openrouter-perplexity-cutoff.json", "length", true},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			eng := liveEngine(t, tt.provider, tt.model, http.StatusOK, liveResponse(t, tt.file), nil)
			answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			if err != nil {
				t.Fatal(err)
			}
			if !answer.Unfinished || answer.StopReason != tt.stop || (answer.Text != "") != tt.wantText {
				t.Fatalf("unfinished=%v stop=%q text=%q", answer.Unfinished, answer.StopReason, answer.Text)
			}
		})
	}
}

func TestLivePerplexityOnOpenRouterSearchesWithoutATool(t *testing.T) {
	var sent map[string]any
	eng := liveEngine(t, "openrouter", "perplexity/sonar", http.StatusOK, liveResponse(t, "openrouter-perplexity-search.json"), &sent)
	if caps := eng.Capabilities(); !caps.InherentlySearchGrounded || caps.SupportsSearchAllowDomains {
		t.Fatalf("capabilities = %#v", caps)
	}
	answer, err := eng.Ask(context.Background(), validRequest(ToolPolicySearchAuto))
	if err != nil {
		t.Fatal(err)
	}
	// With OpenRouter's search tool the request failed with a 404, because
	// no Perplexity endpoint takes tools.
	assertAbsent(t, sent, "tools")
	assertAbsent(t, sent, "disable_search")
	if len(answer.Citations) != 18 || answer.Citations[0].Domain != "nops.io" {
		t.Fatalf("citations = %d %#v", len(answer.Citations), answer.Citations)
	}

	eng = liveEngine(t, "openrouter", "perplexity/sonar", http.StatusOK, liveResponse(t, "openrouter-perplexity-no-search.json"), &sent)
	answer, err = eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatal(err)
	}
	// Without disable_search, Sonar searched and cited 18 pages in the no
	// web search mode.
	assertBoolField(t, sent, "disable_search", true)
	assertAbsent(t, sent, "tools")
	if len(answer.Citations) != 0 || answer.Text == "" {
		t.Fatalf("answer = %#v", answer)
	}

	// Other OpenRouter models still get OpenRouter's search tool.
	eng = liveEngine(t, "openrouter", "openai/gpt-6.1-sol", http.StatusOK, liveResponse(t, "openrouter-perplexity-search.json"), &sent)
	if _, err := eng.Ask(context.Background(), validRequest(ToolPolicySearchAuto)); err != nil {
		t.Fatal(err)
	}
	assertField(t, onlyTool(t, sent), "type", openRouterSearchTool)
}

func TestLiveProviderErrorsSayWhatToDo(t *testing.T) {
	tests := []struct {
		provider, model, file string
		status                int
		want                  []string
		class                 ErrorClass
	}{
		{"openai", "gpt-6.1-sool", "openai-unknown-model.json", 404,
			[]string{"OpenAI answered with status 404: The model `gpt-6.1-sool` does not exist", unknownModelHint}, ErrorClassCaller},
		{"anthropic", "claude-sonnet-5-55", "anthropic-unknown-model.json", 404,
			[]string{"Anthropic answered with status 404: model not found (model: claude-sonnet-5-55). " + unknownModelHint}, ErrorClassCaller},
		{"openrouter", "perplexity/sonarr", "openrouter-unknown-model.json", 400,
			[]string{"perplexity/sonarr is not a valid model ID. " + unknownModelHint}, ErrorClassCaller},
		{"openrouter", "some/model", "openrouter-no-tool-use.json", 404,
			[]string{"No endpoints found that support tool use", "supports_search_auto: false"}, ErrorClassCaller},
		// The Interactions API sends its error inside a list.
		{"google", "gemini-3.8-flash", "google-invalid-key.json", 400,
			[]string{"Google answered with status 400: API key not valid. Please pass a valid API key."}, ErrorClassConfig},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			eng := liveEngine(t, tt.provider, tt.model, tt.status, liveResponse(t, tt.file), nil)
			_, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Class != tt.class {
				t.Fatalf("error = %#v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err.Error(), want)
				}
			}
			// The message names the provider, so the id is not put in front.
			if strings.HasPrefix(err.Error(), tt.provider+":") {
				t.Fatalf("error repeats the provider: %q", err.Error())
			}
		})
	}
}
