package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

func TestProviderCapabilityProfilesAndCeilings(t *testing.T) {
	tests := []struct {
		provider string
		assert   func(t *testing.T, caps CapabilityProfile)
	}{
		{
			provider: "google",
			assert: func(t *testing.T, caps CapabilityProfile) {
				if !caps.SupportsMemory || !caps.SupportsSearchAuto || !caps.SupportsSearchRequired || !caps.SupportsRequiredToolChoice || !caps.ExposesToolCalls || !caps.SupportsStructuredOutput {
					t.Fatalf("google capabilities = %#v", caps)
				}
				if caps.SupportsSearchAllowDomains || caps.SupportsSourceMetadataRequest || caps.InherentlySearchGrounded {
					t.Fatalf("google hard-false capabilities enabled = %#v", caps)
				}
			},
		},
		{
			provider: "perplexity",
			assert: func(t *testing.T, caps CapabilityProfile) {
				if !caps.SupportsMemory || !caps.SupportsSearchAuto || !caps.SupportsSearchRequired || !caps.SupportsRequiredToolChoice || !caps.SupportsSearchRecency || !caps.InherentlySearchGrounded {
					t.Fatalf("perplexity capabilities = %#v", caps)
				}
				if caps.SupportsSearchMaxQueries || caps.ExposesToolCalls {
					t.Fatalf("perplexity hard-false capabilities enabled = %#v", caps)
				}
			},
		},
		{
			provider: "anthropic",
			assert: func(t *testing.T, caps CapabilityProfile) {
				if !caps.SupportsMemory || !caps.SupportsSearchAuto || !caps.SupportsSearchAllowDomains || !caps.SupportsSearchMaxQueries || !caps.ExposesToolCalls {
					t.Fatalf("anthropic capabilities = %#v", caps)
				}
				if caps.SupportsSearchRequired || caps.SupportsStructuredOutput {
					t.Fatalf("anthropic hard-false/base-false capabilities enabled = %#v", caps)
				}
			},
		},
		{
			provider: "openrouter",
			assert: func(t *testing.T, caps CapabilityProfile) {
				if !caps.SupportsMemory || !caps.SupportsSearchAuto || !caps.SupportsSearchAllowDomains || !caps.SupportsSearchMaxQueries || !caps.ExposesCitations {
					t.Fatalf("openrouter capabilities = %#v", caps)
				}
				if caps.SupportsSearchRequired || caps.ExposesConsultedSources || caps.ExposesToolCalls {
					t.Fatalf("openrouter hard-false capabilities enabled = %#v", caps)
				}
			},
		},
		{
			provider: "openai-compatible",
			assert: func(t *testing.T, caps CapabilityProfile) {
				if !caps.SupportsMemory {
					t.Fatalf("local capabilities = %#v", caps)
				}
				if caps.SupportsSearchAuto || caps.ExposesConsultedSources {
					t.Fatalf("local hard-false capabilities enabled = %#v", caps)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			caps, err := CapabilityProfileForConfig(tt.provider, "model", nil)
			if err != nil {
				t.Fatalf("CapabilityProfileForConfig() error = %v", err)
			}
			tt.assert(t, caps)
		})
	}

	if _, err := CapabilityProfileForConfig("openai-compatible", "model", config.Capabilities{"supports_search_auto": true}); err == nil || !strings.Contains(err.Error(), "supports_search_auto") {
		t.Fatalf("local search override error = %v, want ceiling failure", err)
	}
	if caps, err := CapabilityProfileForConfig("anthropic", "model", config.Capabilities{"supports_structured_output": true}); err != nil || !caps.SupportsStructuredOutput {
		t.Fatalf("anthropic structured output override = %#v/%v", caps, err)
	}
	for _, tt := range []struct {
		provider string
		caps     config.Capabilities
		want     string
	}{
		{
			provider: "perplexity",
			caps:     config.Capabilities{"supports_structured_output_with_tools": true},
			want:     "supports_structured_output",
		},
		{
			provider: "openrouter",
			caps:     config.Capabilities{"supports_structured_output_with_citations": true},
			want:     "supports_structured_output",
		},
		{
			provider: "google",
			caps:     config.Capabilities{"supports_structured_output_with_tools": true},
			want:     "supports_structured_output_with_citations",
		},
		{
			provider: "perplexity",
			caps: config.Capabilities{
				"supports_structured_output":            true,
				"supports_structured_output_with_tools": true,
			},
			want: "supports_structured_output_with_citations",
		},
		{
			provider: "openrouter",
			caps: config.Capabilities{
				"supports_structured_output":            true,
				"supports_structured_output_with_tools": true,
			},
			want: "supports_structured_output_with_citations",
		},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			_, err := CapabilityProfileForConfig(tt.provider, "model", tt.caps)
			if err == nil || !strings.Contains(err.Error(), "only works with") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CapabilityProfileForConfig() error = %v, want dependency failure", err)
			}
		})
	}
}

func TestProviderHelpersEvidenceReadRetryAndMetadata(t *testing.T) {
	mapper := &evidenceMapper{}
	if citation, ok := mapper.citation("https://Example.com/path?q=1", "Example", map[string]string{"cited_text": "text"}); !ok || citation.URL != "https://Example.com/path?q=1" || citation.Domain != "example.com" || citation.Metadata["cited_text"] != "text" {
		t.Fatalf("citation = %#v ok=%v", citation, ok)
	}
	for _, raw := range []string{"../relative", "ftp://example.com/file", "https://user:pass@example.com/file", "https:///missing-host"} {
		if _, ok := mapper.citation(raw, "", nil); ok {
			t.Fatalf("citation(%q) succeeded, want dropped", raw)
		}
	}
	if mapper.dropped != 4 {
		t.Fatalf("dropped = %d, want 4", mapper.dropped)
	}

	raw, overflow, err := readProviderBody(strings.NewReader(strings.Repeat("a", MaxProviderResponseBytes+1)))
	if err != nil || !overflow || len(raw) != MaxProviderResponseBytes {
		t.Fatalf("readProviderBody overflow = len %d overflow %v err %v", len(raw), overflow, err)
	}

	header := make(http.Header)
	header.Set("Retry-After", "2")
	if got := parseRetryAfter(header); got == nil || *got != 2*time.Second {
		t.Fatalf("Retry-After seconds = %#v", got)
	}
	header.Set("Retry-After", time.Now().Add(time.Second).UTC().Format(http.TimeFormat))
	if got := parseRetryAfter(header); got == nil {
		t.Fatalf("Retry-After date = nil")
	}

	metadata := map[string]string{}
	setProviderMetadata(metadata, "response_id", "resp")
	setProviderMetadata(metadata, "untrusted", "copy-me")
	if !reflect.DeepEqual(metadata, map[string]string{"response_id": "resp"}) {
		t.Fatalf("metadata = %#v", metadata)
	}
}

// googleGroundedResponse follows the Interactions API reference and the
// "Grounding with Google Search" guide (ai.google.dev, checked 2026-10-26):
// a steps list with thought, google_search_call, google_search_result (whose
// result is a list) and model_output, with url_citation annotations on text.
const googleGroundedResponse = `{
	"id":"int_1",
	"model":"gemini-test",
	"object":"interaction",
	"status":"completed",
	"created":"2026-08-18T00:00:00Z",
	"steps":[
		{"type":"thought","signature":"sig","summary":[{"type":"text","text":"I should search."}]},
		{"type":"google_search_call","id":"call_1","arguments":{"queries":["Acme"]}},
		{"type":"google_search_result","call_id":"call_1","result":[
			{"title":"Example source","url":"https://Example.com/source","snippet":"About Acme."},
			{"search_suggestions":"<!-- widget -->"}
		]},
		{"type":"model_output","content":[{"type":"text","text":"answer","annotations":[
			{"type":"url_citation","url":"https://Acme.example/page","title":"acme.example","start_index":0,"end_index":6},
			{"type":"url_citation","url":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AbC","title":"globex.example"},
			{"type":"url_citation","url":"ftp://bad.example/file","title":"bad"}
		]}]}
	],
	"usage":{"total_input_tokens":10,"total_output_tokens":5,"total_tokens":15,"total_cached_tokens":2}
}`

func TestGoogleAdapterRequestAndResponseMapping(t *testing.T) {
	var body map[string]any
	eng := newHostedEngineForTest(t, "google", "GOOGLE_API_KEY", "https://api.test/v1beta", "g-key", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.test/v1beta/interactions" {
			t.Fatalf("URL = %s", req.URL.String())
		}
		if req.Header.Get("x-goog-api-key") != "g-key" {
			t.Fatalf("x-goog-api-key = %q", req.Header.Get("x-goog-api-key"))
		}
		body = decodeRequestBody(t, req)
		return jsonResponse(http.StatusOK, googleGroundedResponse), nil
	}))
	req := validRequest(ToolPolicySearchRequired)
	req.MaxOutputTokens = 33
	answer, err := eng.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	assertField(t, body, "model", "google-test")
	assertBoolField(t, body, "store", false)
	generation := asMap(t, body["generation_config"])
	assertNumberField(t, generation, "max_output_tokens", 33)
	// tool_choice belongs in generation_config, not at the top level.
	assertField(t, generation, "tool_choice", "any")
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("tool_choice sent at the top level: %#v", body)
	}

	if answer.Text != "answer" || answer.StopReason != "completed" || answer.Unfinished {
		t.Fatalf("answer text/stop = %q/%q", answer.Text, answer.StopReason)
	}
	if got, want := answer.ToolsUsed, []string{webSearchToolName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolsUsed = %#v, want %#v", got, want)
	}
	if answer.ToolCalls[0].ProviderToolCallID != "call_1" || answer.ToolCalls[0].Query != "Acme" {
		t.Fatalf("ToolCalls = %#v", answer.ToolCalls)
	}
	var domains []string
	for _, c := range answer.Citations {
		domains = append(domains, c.Domain)
	}
	// A citation through Google's redirect counts for the site in its title.
	if !reflect.DeepEqual(domains, []string{"acme.example", "globex.example"}) {
		t.Fatalf("citation domains = %q", domains)
	}
	if len(answer.SourcesConsulted) != 1 || answer.SourcesConsulted[0].Domain != "example.com" {
		t.Fatalf("SourcesConsulted = %#v", answer.SourcesConsulted)
	}
	if answer.ProviderMetadata["response_id"] != "int_1" || answer.ProviderMetadata["usage_total_tokens"] != "15" || answer.ProviderMetadata["dropped_evidence_urls"] != "1" {
		t.Fatalf("metadata = %#v", answer.ProviderMetadata)
	}

	// Without search the request carries no tools and no tool_choice.
	if _, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory)); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["tools"]; ok {
		t.Fatalf("memory request sent tools: %#v", body)
	}
}

func TestGoogleIncompleteInteractionIsUnfinished(t *testing.T) {
	answer, err := googleAnswerFromResponse([]byte(`{"id":"int_2","status":"incomplete","steps":[{"type":"model_output","content":[{"type":"text","text":"The best tools are"}]}]}`), validRequest(ToolPolicyMemory))
	if err != nil || !answer.Unfinished || answer.Text != "The best tools are" {
		t.Fatalf("answer = %#v, err = %v", answer, err)
	}
	answer, err = googleAnswerFromResponse([]byte(`{"id":"int_3","status":"incomplete","steps":[]}`), validRequest(ToolPolicyMemory))
	if err != nil || !answer.Unfinished {
		t.Fatalf("empty incomplete answer = %#v, err = %v", answer, err)
	}
}

func TestGoogleSearchRequiredRejectsCitationOnlyEvidence(t *testing.T) {
	eng := newHostedEngineForTest(t, "google", "GOOGLE_API_KEY", "https://api.test/v1beta", "g-key", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"id":"int_citation_only",
			"status":"completed",
			"steps":[{"type":"model_output","content":[{"type":"text","text":"answer","annotations":[{"type":"url_citation","url":"https://example.com/cite"}]}]}]
		}`), nil
	}))
	_, err := eng.Ask(context.Background(), validRequest(ToolPolicySearchRequired))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassProvider || !strings.Contains(providerErr.SafeMessage, "without searching") {
		t.Fatalf("Ask() error = %#v", err)
	}
}

func TestGoogleAdapterDoesNotDuplicateStepCitations(t *testing.T) {
	answer, err := googleAnswerFromResponse([]byte(`{
		"id":"int_step_citations",
		"status":"completed",
		"steps":[{"type":"model_output","text":"answer","annotations":[{"type":"url_citation","url":"https://example.com/cite","title":"Example"}]}]
	}`), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("googleAnswerFromResponse() error = %v", err)
	}
	if got := len(answer.Citations); got != 1 {
		t.Fatalf("Citations = %d, want 1: %#v", got, answer.Citations)
	}
}

func TestPerplexityAdapterRequestAndResponseMapping(t *testing.T) {
	var body map[string]any
	eng := newHostedEngineForTest(t, "perplexity", "PERPLEXITY_API_KEY", "https://api.test/v1", "p-key", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.test/v1/sonar" {
			t.Fatalf("URL = %s", req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer p-key" {
			t.Fatalf("Authorization = %q", req.Header.Get("Authorization"))
		}
		body = decodeRequestBody(t, req)
		return jsonResponse(http.StatusOK, `{
			"id":"sonar_1",
			"model":"sonar-test",
			"created":123,
			"object":"chat.completion",
			"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}],
			"citations":["https://Acme.example/cite"],
			"search_results":[{"url":"https://Example.com/source","title":"Example","date":"2026-08-18"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"num_search_queries":2,"cost":{"total_cost":0.01}}
		}`), nil
	}), config.Capabilities{
		"supports_structured_output":                true,
		"supports_structured_output_with_tools":     true,
		"supports_structured_output_with_citations": true,
	})
	req := validRequest(ToolPolicySearchAuto)
	req.SearchOptions = &SearchOptions{
		AllowedDomains: []string{"https://Example.com/path"},
		Locale:         "en-US",
		RecencyDays:    7,
		UserLocation:   &SearchLocation{CountryCode: "us", Region: "CA", City: "San Francisco"},
	}
	req.OutputSchema = &OutputSchema{Name: "answer", Format: OutputFormatJSONSchema, SchemaJSON: []byte(`{"type":"object"}`), Strict: true}
	answer, err := eng.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	assertBoolField(t, body, "enable_search_classifier", true)
	assertBoolField(t, body, "disable_search", false)
	assertStringSlice(t, body, "search_domain_filter", []string{"example.com"})
	assertStringSlice(t, body, "search_language_filter", []string{"en"})
	assertField(t, body, "search_recency_filter", "week")
	location := asMap(t, asMap(t, body["web_search_options"])["user_location"])
	assertField(t, location, "country", "US")
	// search_results are the cited pages; the older citations list only
	// adds URLs that are not already there.
	if string(answer.Parsed) != `{"ok":true}` || len(answer.Citations) != 2 || answer.Citations[0].Domain != "example.com" || answer.Citations[1].Domain != "acme.example" || answer.SourcesConsulted[0].Metadata["date"] != "2026-08-18" {
		t.Fatalf("answer = %#v", answer)
	}
	if got, want := answer.ToolsUsed, []string{webSearchToolName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolsUsed = %#v, want %#v", got, want)
	}
	if answer.ProviderMetadata["usage_search_queries"] != "2" || answer.ProviderMetadata["usage_cost"] != "0.01" {
		t.Fatalf("metadata = %#v", answer.ProviderMetadata)
	}
}

// The current Sonar response has no citations field: the pages an answer
// cites are its search_results (docs.perplexity.ai changelog, May 2025).
func TestPerplexityCitesSearchResults(t *testing.T) {
	answer, err := perplexityAnswerFromResponse([]byte(`{
		"id":"8158ec84","model":"sonar","object":"chat.completion",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Acme leads [1]. Globex is cheaper [2]."},"finish_reason":"stop"}],
		"search_results":[
			{"title":"Acme review","url":"https://www.acme.example/review","date":"2026-06-14"},
			{"title":"Globex pricing","url":"https://globex.example/pricing"}
		],
		"usage":{"prompt_tokens":9,"completion_tokens":12,"total_tokens":21}
	}`), validRequest(ToolPolicySearchAuto))
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Citations) != 2 || answer.Citations[0].Domain != "acme.example" || answer.Citations[1].Domain != "globex.example" {
		t.Fatalf("citations = %#v", answer.Citations)
	}
}

func TestChatCompletionLengthIsUnfinished(t *testing.T) {
	answer, err := perplexityAnswerFromResponse([]byte(`{"choices":[{"message":{"content":"The best tools are"},"finish_reason":"length"}]}`), validRequest(ToolPolicyMemory))
	if err != nil || !answer.Unfinished {
		t.Fatalf("perplexity answer = %#v, err = %v", answer, err)
	}
	answer, err = chatCompletionAnswerFromResponse(chatCompletionParseInput{Provider: "openrouter", Raw: []byte(`{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`), Request: validRequest(ToolPolicyMemory)})
	if err != nil || !answer.Unfinished {
		t.Fatalf("openrouter answer = %#v, err = %v", answer, err)
	}
}

func TestAnthropicUnfinishedStopReasons(t *testing.T) {
	for _, stop := range []string{"pause_turn", "max_tokens", "model_context_window_exceeded"} {
		answer, err := anthropicAnswerFromResponse([]byte(`{"id":"msg","stop_reason":"`+stop+`","content":[{"type":"text","text":"Helicone is"}]}`), http.Header{}, validRequest(ToolPolicySearchAuto))
		if err != nil || !answer.Unfinished || answer.Text != "Helicone is" {
			t.Fatalf("%s: answer = %#v, err = %v", stop, answer, err)
		}
	}
	for _, stop := range []string{"end_turn", "refusal"} {
		answer, err := anthropicAnswerFromResponse([]byte(`{"id":"msg","stop_reason":"`+stop+`","content":[{"type":"text","text":"Done."}]}`), http.Header{}, validRequest(ToolPolicyMemory))
		if err != nil || answer.Unfinished {
			t.Fatalf("%s: answer = %#v, err = %v", stop, answer, err)
		}
	}
}

func TestAnthropicAdapterRequestAndResponseMapping(t *testing.T) {
	var body map[string]any
	eng := newHostedEngineForTest(t, "anthropic", "ANTHROPIC_API_KEY", "https://api.test/v1", "a-key", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.test/v1/messages" {
			t.Fatalf("URL = %s", req.URL.String())
		}
		if req.Header.Get("x-api-key") != "a-key" || req.Header.Get("anthropic-version") != anthropicVersion {
			t.Fatalf("headers = %#v", req.Header)
		}
		body = decodeRequestBody(t, req)
		resp := jsonResponse(http.StatusOK, `{
			"id":"msg_1",
			"model":"claude-test",
			"stop_reason":"end_turn",
			"content":[
				{"type":"text","text":"Claude answer","citations":[{"type":"web_search_result_location","url":"https://Acme.example/cite","title":"Citation","cited_text":"quoted"}]},
				{"type":"server_tool_use","id":"toolu_1","name":"web_search","input":{"query":"Acme"}},
				{"type":"web_search_tool_result","tool_use_id":"toolu_1","content":[
					{"type":"web_search_result","url":"https://Example.com/source","title":"Example","content":"source text"},
					{"type":"web_search_tool_result_error","error_code":"rate_limited","message":"limited"}
				]}
			],
			"usage":{"input_tokens":12,"output_tokens":8,"cache_read_input_tokens":3}
		}`)
		resp.Header.Set("request-id", "req_anthropic")
		return resp, nil
	}))
	req := validRequest(ToolPolicySearchAuto)
	req.SearchOptions = &SearchOptions{
		BlockedDomains: []string{"blocked.example"},
		MaxSearches:    3,
		UserLocation:   &SearchLocation{CountryCode: "de", Region: "BE", City: "Berlin", Timezone: "Europe/Berlin"},
	}
	answer, err := eng.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	assertNumberField(t, body, "max_tokens", anthropicDefaultMaxTokens)
	tool := onlyTool(t, body)
	assertField(t, tool, "type", anthropicSearchToolType)
	assertField(t, tool, "name", webSearchToolName)
	assertNumberField(t, tool, "max_uses", 3)
	assertStringSlice(t, tool, "blocked_domains", []string{"blocked.example"})
	location := asMap(t, tool["user_location"])
	assertField(t, location, "type", "approximate")
	assertField(t, location, "country", "DE")

	if answer.Text != "Claude answer" || answer.ProviderMetadata["request_id"] != "req_anthropic" {
		t.Fatalf("answer/metadata = %#v %#v", answer, answer.ProviderMetadata)
	}
	if answer.ToolCalls[0].Status != "error" || answer.ToolCalls[0].ErrorCode != "rate_limited" || answer.ToolCalls[0].ProviderToolCallID != "toolu_1" {
		t.Fatalf("ToolCalls = %#v", answer.ToolCalls)
	}
	if answer.SourcesConsulted[0].Domain != "example.com" || answer.Citations[0].Domain != "acme.example" {
		t.Fatalf("evidence = %#v %#v", answer.SourcesConsulted, answer.Citations)
	}
}

func TestAnthropicStrictTerminalStatesAllowEmptyText(t *testing.T) {
	for _, stopReason := range []string{"max_tokens", "model_context_window_exceeded"} {
		t.Run(stopReason, func(t *testing.T) {
			eng := newHostedEngineForTest(t, "anthropic", "ANTHROPIC_API_KEY", "https://api.test/v1", "a-key", roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{
					"id":"msg_terminal",
					"model":"claude-test",
					"stop_reason":"`+stopReason+`",
					"content":[]
				}`), nil
			}), config.Capabilities{"supports_structured_output": true})
			req := validRequest(ToolPolicyMemory)
			req.OutputSchema = &OutputSchema{Name: "answer", Format: OutputFormatJSONSchema, SchemaJSON: []byte(`{"type":"object"}`), Strict: true}
			answer, err := eng.Ask(context.Background(), req)
			if err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			if answer.StopReason != stopReason || answer.Parsed != nil || answer.Text != "" {
				t.Fatalf("answer = %#v", answer)
			}
		})
	}
}

func TestOpenRouterAdapterRequestAndResponseMapping(t *testing.T) {
	var body map[string]any
	eng := newHostedEngineForTest(t, "openrouter", "OPENROUTER_API_KEY", "https://api.test/api/v1", "or-key", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.test/api/v1/chat/completions" {
			t.Fatalf("URL = %s", req.URL.String())
		}
		body = decodeRequestBody(t, req)
		resp := jsonResponse(http.StatusOK, `{
			"id":"cmpl_1",
			"model":"routed/model",
			"created":456,
			"object":"chat.completion",
			"choices":[{"message":{"content":"OpenRouter answer","annotations":[{"type":"url_citation","url_citation":{"url":"https://Acme.example/cite","title":"Citation","content":"quoted"}}]},"finish_reason":"stop","native_finish_reason":"stop"}],
			"usage":{"prompt_tokens":13,"completion_tokens":9,"total_tokens":22,"server_tool_use":{"web_search_requests":2},"cost":"0.02"}
		}`)
		resp.Header.Set("X-Generation-Id", "gen_1")
		return resp, nil
	}), config.Capabilities{
		"supports_structured_output":                true,
		"supports_structured_output_with_tools":     true,
		"supports_structured_output_with_citations": true,
	})
	req := validRequest(ToolPolicySearchAuto)
	req.SearchOptions = &SearchOptions{AllowedDomains: []string{"allowed.example"}, BlockedDomains: []string{"blocked.example"}, MaxSearches: 4}
	answer, err := eng.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	tool := onlyTool(t, body)
	assertField(t, tool, "type", openRouterSearchTool)
	params := asMap(t, tool["parameters"])
	assertField(t, params, "engine", "exa")
	assertNumberField(t, params, "max_uses", 4)
	assertStringSlice(t, params, "allowed_domains", []string{"allowed.example"})
	assertStringSlice(t, params, "excluded_domains", []string{"blocked.example"})
	assertNumberField(t, body, "max_tool_calls", 4)
	if answer.Text != "OpenRouter answer" || answer.Citations[0].Domain != "acme.example" {
		t.Fatalf("answer = %#v", answer)
	}
	if len(answer.ToolCalls) != 0 {
		t.Fatalf("OpenRouter fabricated ToolCalls = %#v", answer.ToolCalls)
	}
	if got, want := answer.ToolsUsed, []string{webSearchToolName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolsUsed = %#v, want %#v", got, want)
	}
	if answer.ProviderMetadata["response_id"] != "gen_1" || answer.ProviderMetadata["usage_cost"] != "0.02" {
		t.Fatalf("metadata = %#v", answer.ProviderMetadata)
	}
}

// Gemini on OpenRouter searches with Google, and its citations link through
// Google's redirect with the site in the title, as seen live on 2026-11-08.
func TestOpenRouterGeminiCitationsCountForTheSiteInTheTitle(t *testing.T) {
	eng := newHostedEngineForTest(t, "openrouter", "OPENROUTER_API_KEY", "https://api.test/api/v1", "or-key", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"id":"gen_2","model":"google/gemini-3.8-flash","object":"chat.completion",
			"choices":[{"message":{"content":"Answer","annotations":[
				{"type":"url_citation","url_citation":{"url":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZIYQabc","title":"cloudzero.com","start_index":0,"end_index":10}},
				{"type":"url_citation","url_citation":{"url":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZIYQdef","title":"A page title, not a site","start_index":0,"end_index":10}}
			]},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}
		}`), nil
	}), nil)
	answer, err := eng.Ask(context.Background(), validRequest(ToolPolicySearchAuto))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(answer.Citations) != 2 || answer.Citations[0].Domain != "cloudzero.com" || answer.Citations[1].Domain != "vertexaisearch.cloud.google.com" {
		t.Fatalf("citations = %#v", answer.Citations)
	}
}

func TestOpenRouterEmbeddedErrorReturnsNoPartialAnswer(t *testing.T) {
	eng := newHostedEngineForTest(t, "openrouter", "OPENROUTER_API_KEY", "https://api.test/api/v1", "or-key", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"partial"},"finish_reason":"error","error":{"code":"provider_unavailable","message":"upstream failed"}}]}`), nil
	}))
	_, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusOK || providerErr.Class != ErrorClassProvider || !strings.Contains(providerErr.SafeMessage, "provider_unavailable") {
		t.Fatalf("Ask() error = %#v", err)
	}
}

func TestLocalAdapterEndpointAuthStructuredOutputAndRedirects(t *testing.T) {
	var lookups []string
	var body map[string]any
	eng, err := NewFactory(FactoryOptions{
		BaseURLs: map[string]string{"openai-compatible": "http://localhost:11434/v1"},
		LookupEnv: func(name string) (string, bool) {
			lookups = append(lookups, name)
			return "", false
		},
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "http://localhost:11434/v1/chat/completions" {
				t.Fatalf("URL = %s", req.URL.String())
			}
			if req.Header.Get("Authorization") != "" {
				t.Fatalf("Authorization = %q, want empty", req.Header.Get("Authorization"))
			}
			body = decodeRequestBody(t, req)
			return jsonResponse(http.StatusOK, `{
				"id":"local_1",
				"model":"llama",
				"created":789,
				"object":"chat.completion",
				"choices":[{"message":{"content":"{\"ok\":true}","annotations":[{"type":"url_citation","url":"https://Acme.example/cite","title":"Citation"}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}
			}`), nil
		}),
	}).NewEngine(config.EngineConfig{
		ID:       "local-llama",
		Provider: "openai-compatible",
		Model:    "llama",
		BaseURL:  "http://localhost:11434/v1",
		Capabilities: config.Capabilities{
			"supports_structured_output": true,
			"exposes_citations":          true,
			"exposes_tool_calls":         true,
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	req := validRequest(ToolPolicyMemory)
	req.OutputSchema = &OutputSchema{Name: "answer", Format: OutputFormatJSONSchema, SchemaJSON: []byte(`{"type":"object"}`), Strict: true}
	answer, err := eng.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(lookups) != 0 {
		t.Fatalf("local no-auth looked up secrets: %#v", lookups)
	}
	format := asMap(t, body["response_format"])
	assertField(t, format, "type", "json_schema")
	if string(answer.Parsed) != `{"ok":true}` || answer.Citations[0].Domain != "acme.example" || answer.ToolCalls[0].Type != "lookup" {
		t.Fatalf("answer = %#v", answer)
	}

	redirectEngine, err := NewFactory(FactoryOptions{
		BaseURLs:  map[string]string{"openai-compatible": "http://localhost:11434/v1"},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusFound, ""), nil }),
	}).NewEngine(config.EngineConfig{
		ID:       "local-redirect",
		Provider: "openai-compatible",
		Model:    "llama",
		BaseURL:  "http://localhost:11434/v1",
	})
	if err != nil {
		t.Fatalf("NewEngine(redirect) error = %v", err)
	}
	_, err = redirectEngine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassProvider || providerErr.StatusCode != http.StatusFound {
		t.Fatalf("redirect error = %#v", err)
	}
}

func TestLocalAdapterIgnoresConsultedSources(t *testing.T) {
	eng, err := NewFactory(FactoryOptions{
		BaseURLs: map[string]string{"openai-compatible": "http://localhost:11434/v1"},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"id":"local_sources",
				"model":"llama",
				"choices":[{"message":{"content":"local answer","sources":[{"url":"https://debug.example/source","title":"Debug source"}]},"finish_reason":"stop"}]
			}`), nil
		}),
	}).NewEngine(config.EngineConfig{
		ID:       "local-sources",
		Provider: "openai-compatible",
		Model:    "llama",
		BaseURL:  "http://localhost:11434/v1",
		Capabilities: config.Capabilities{
			"exposes_citations":  true,
			"exposes_tool_calls": true,
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(answer.SourcesConsulted) != 0 {
		t.Fatalf("SourcesConsulted = %#v, want none", answer.SourcesConsulted)
	}
}

func TestLocalFactoryRejectsMalformedEndpointHost(t *testing.T) {
	_, err := NewFactory(FactoryOptions{}).NewEngine(config.EngineConfig{
		ID:       "local-bad",
		Provider: "openai-compatible",
		Model:    "llama",
		BaseURL:  "http://bad_host/v1",
	})
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("NewEngine() error = %v, want endpoint host validation error", err)
	}
}

func TestProviderAdaptersRejectUnsupportedBeforeKeyLookup(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		keyEnv   string
		baseURL  string
		req      Request
		want     string
	}{
		{
			name:     "google domain filters",
			provider: "google",
			keyEnv:   "GOOGLE_API_KEY",
			baseURL:  "https://api.test/v1beta",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{AllowedDomains: []string{"example.com"}}
				return req
			}(),
			want: "allowed search domains",
		},
		{
			name:     "perplexity max searches",
			provider: "perplexity",
			keyEnv:   "PERPLEXITY_API_KEY",
			baseURL:  "https://api.test/v1",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{MaxSearches: 1}
				return req
			}(),
			want: "max queries",
		},
		{
			name:     "anthropic required",
			provider: "anthropic",
			keyEnv:   "ANTHROPIC_API_KEY",
			baseURL:  "https://api.test/v1",
			req:      validRequest(ToolPolicySearchRequired),
			want:     "does not support mode",
		},
		{
			name:     "openrouter location with domains",
			provider: "openrouter",
			keyEnv:   "OPENROUTER_API_KEY",
			baseURL:  "https://api.test/v1",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{AllowedDomains: []string{"example.com"}, UserLocation: &SearchLocation{CountryCode: "US"}}
				return req
			}(),
			want: "location with domain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookups := 0
			eng := newHostedEngineForTest(t, tt.provider, tt.keyEnv, tt.baseURL, "secret", roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("HTTP should not be called")
				return nil, nil
			}), nil, func(string) (string, bool) {
				lookups++
				return "secret", true
			})
			_, err := eng.Ask(context.Background(), tt.req)
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassCaller || !strings.Contains(providerErr.SafeMessage, tt.want) {
				t.Fatalf("Ask() error = %#v, want %q", err, tt.want)
			}
			if lookups != 0 {
				t.Fatalf("secret lookups = %d, want 0", lookups)
			}
		})
	}
}

func TestProviderAdaptersResolveKeysRedactSecretsAndTruncateRaw(t *testing.T) {
	for _, provider := range []string{"google", "perplexity", "anthropic", "openrouter", "openai-compatible"} {
		t.Run(provider, func(t *testing.T) {
			keyEnv := strings.ToUpper(provider) + "_API_KEY"
			if provider == "openai-compatible" {
				keyEnv = "LOCAL_API_KEY"
			}
			secret := provider + "-secret"
			var lookups []string
			var registered []string
			eng := newProviderEngineForTest(t, provider, keyEnv, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch provider {
				case "google":
					if req.Header.Get("x-goog-api-key") != secret {
						t.Fatalf("google key header = %q", req.Header.Get("x-goog-api-key"))
					}
				case "anthropic":
					if req.Header.Get("x-api-key") != secret {
						t.Fatalf("anthropic key header = %q", req.Header.Get("x-api-key"))
					}
				default:
					if req.Header.Get("Authorization") != "Bearer "+secret {
						t.Fatalf("authorization header = %q", req.Header.Get("Authorization"))
					}
				}
				return jsonResponse(http.StatusOK, providerSuccessBody(provider, secret, strings.Repeat("a", MaxRawPayloadBytes+100))), nil
			}), func(name string) (string, bool) {
				lookups = append(lookups, name)
				return secret, true
			}, func(value string) {
				registered = append(registered, value)
			})

			if err := eng.ValidateRequest(validRequest(ToolPolicyMemory)); err != nil {
				t.Fatalf("ValidateRequest() error = %v", err)
			}
			if len(lookups) != 0 || len(registered) != 0 {
				t.Fatalf("validation resolved secrets: lookups=%#v registered=%#v", lookups, registered)
			}
			answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			if err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			if got, want := lookups, []string{keyEnv}; !reflect.DeepEqual(got, want) {
				t.Fatalf("lookups = %#v, want %#v", got, want)
			}
			if got, want := registered, []string{secret}; !reflect.DeepEqual(got, want) {
				t.Fatalf("registered = %#v, want %#v", got, want)
			}
			if strings.Contains(answer.Text, secret) || !strings.Contains(answer.Text, secretRedactionText) {
				t.Fatalf("answer text redaction = %q", answer.Text)
			}
			if len(answer.Raw) != MaxRawPayloadBytes || answer.ProviderMetadata["raw_truncated"] != "true" {
				t.Fatalf("raw capture len=%d metadata=%#v", len(answer.Raw), answer.ProviderMetadata)
			}
			if strings.Contains(string(answer.Raw), secret) {
				t.Fatalf("raw response leaked secret")
			}
		})
	}
}

func TestProviderAdaptersRejectResponseOverflow(t *testing.T) {
	for _, provider := range []string{"google", "perplexity", "anthropic", "openrouter", "openai-compatible"} {
		t.Run(provider, func(t *testing.T) {
			eng := newProviderEngineForTest(t, provider, "PROVIDER_API_KEY", roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     http.StatusText(http.StatusOK),
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", MaxProviderResponseBytes+1))),
				}, nil
			}), nil, nil)
			_, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassProvider || providerErr.StatusCode != http.StatusOK || !strings.Contains(providerErr.SafeMessage, "larger than") {
				t.Fatalf("Ask() error = %#v", err)
			}
		})
	}
}

func TestProviderAdaptersReturnDirectCancellation(t *testing.T) {
	for _, provider := range []string{"google", "perplexity", "anthropic", "openrouter", "openai-compatible"} {
		t.Run(provider, func(t *testing.T) {
			called := false
			eng := newProviderEngineForTest(t, provider, "PROVIDER_API_KEY", roundTripFunc(func(*http.Request) (*http.Response, error) {
				called = true
				return jsonResponse(http.StatusOK, providerSuccessBody(provider, "secret", "")), nil
			}), nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := eng.Ask(ctx, validRequest(ToolPolicyMemory))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Ask() error = %v, want context.Canceled", err)
			}
			if called {
				t.Fatalf("HTTP was called after cancellation")
			}
		})
	}
}

func TestProviderHTTPErrorsRedactSecretsAndClassifyByProvider(t *testing.T) {
	tests := []struct {
		name          string
		provider      string
		keyEnv        string
		status        int
		body          func(secret string) string
		headers       map[string]string
		wantClass     ErrorClass
		wantRetryable bool
		wantRequestID string
		wantRetry     time.Duration
	}{
		{
			name:     "google rate limit",
			provider: "google",
			keyEnv:   "GOOGLE_API_KEY",
			status:   http.StatusTooManyRequests,
			body: func(secret string) string {
				return `{"error":{"message":"rate limited ` + secret + `","status":"RESOURCE_EXHAUSTED"}}`
			},
			headers:       map[string]string{"Retry-After": "2"},
			wantClass:     ErrorClassTransient,
			wantRetryable: true,
			wantRetry:     2 * time.Second,
		},
		{
			name:     "perplexity payment required",
			provider: "perplexity",
			keyEnv:   "PERPLEXITY_API_KEY",
			status:   http.StatusPaymentRequired,
			body: func(secret string) string {
				return `{"error":{"message":"billing issue ` + secret + `","type":"payment_required"}}`
			},
			wantClass: ErrorClassConfig,
		},
		{
			name:     "anthropic overloaded",
			provider: "anthropic",
			keyEnv:   "ANTHROPIC_API_KEY",
			status:   529,
			body: func(secret string) string {
				return `{"error":{"message":"overloaded ` + secret + `","type":"overloaded_error"}}`
			},
			headers:       map[string]string{"request-id": "req_anthropic_error", "Retry-After": "2"},
			wantClass:     ErrorClassTransient,
			wantRetryable: true,
			wantRequestID: "req_anthropic_error",
			wantRetry:     2 * time.Second,
		},
		{
			name:     "openrouter upstream unavailable",
			provider: "openrouter",
			keyEnv:   "OPENROUTER_API_KEY",
			status:   http.StatusServiceUnavailable,
			body: func(secret string) string {
				return `{"error":{"message":"upstream unavailable ` + secret + `","type":"provider_unavailable"}}`
			},
			wantClass:     ErrorClassTransient,
			wantRetryable: true,
		},
		{
			name:     "local auth rejected",
			provider: "openai-compatible",
			keyEnv:   "LOCAL_API_KEY",
			status:   http.StatusUnauthorized,
			body: func(secret string) string {
				return `{"error":"bad local key ` + secret + `"}`
			},
			wantClass: ErrorClassConfig,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := tt.provider + "-secret"
			eng := newProviderEngineForTest(t, tt.provider, tt.keyEnv, roundTripFunc(func(*http.Request) (*http.Response, error) {
				resp := jsonResponse(tt.status, tt.body(secret))
				for key, value := range tt.headers {
					resp.Header.Set(key, value)
				}
				return resp, nil
			}), func(string) (string, bool) {
				return secret, true
			}, nil)
			answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			if len(answer.Raw) != 0 {
				t.Fatalf("non-2xx answer raw = %q, want empty", string(answer.Raw))
			}
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
			}
			if providerErr.Class != tt.wantClass || providerErr.Retryable != tt.wantRetryable || providerErr.StatusCode != tt.status {
				t.Fatalf("providerErr = %#v, want class=%s retryable=%v status=%d", providerErr, tt.wantClass, tt.wantRetryable, tt.status)
			}
			if providerErr.RequestID != tt.wantRequestID {
				t.Fatalf("RequestID = %q, want %q", providerErr.RequestID, tt.wantRequestID)
			}
			if tt.wantRetry > 0 {
				if providerErr.RetryAfter == nil || *providerErr.RetryAfter != tt.wantRetry {
					t.Fatalf("RetryAfter = %#v, want %v", providerErr.RetryAfter, tt.wantRetry)
				}
			} else if providerErr.RetryAfter != nil {
				t.Fatalf("RetryAfter = %#v, want nil", providerErr.RetryAfter)
			}
			if strings.Contains(providerErr.SafeMessage, secret) || strings.Contains(providerErr.SafeSnippet, secret) {
				t.Fatalf("providerErr leaked key: %#v", providerErr)
			}
			if !strings.Contains(providerErr.SafeSnippet, secretRedactionText) {
				t.Fatalf("SafeSnippet = %q, want redacted secret marker", providerErr.SafeSnippet)
			}
		})
	}
}

func newHostedEngineForTest(t *testing.T, provider, keyEnv, baseURL, key string, transport http.RoundTripper, extras ...any) Engine {
	t.Helper()
	capabilities := config.Capabilities(nil)
	lookup := func(string) (string, bool) { return key, true }
	for _, extra := range extras {
		switch v := extra.(type) {
		case config.Capabilities:
			capabilities = v
		case func(string) (string, bool):
			lookup = v
		case nil:
		default:
			t.Fatalf("unsupported test option %T", extra)
		}
	}
	engine, err := NewFactory(FactoryOptions{
		Transport: transport,
		BaseURLs:  map[string]string{provider: baseURL},
		LookupEnv: lookup,
	}).NewEngine(config.EngineConfig{
		ID:           provider + "-engine",
		Provider:     provider,
		Model:        provider + "-test",
		KeyEnv:       keyEnv,
		Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func newProviderEngineForTest(t *testing.T, provider, keyEnv string, transport http.RoundTripper, lookup func(string) (string, bool), register func(string)) Engine {
	t.Helper()
	if lookup == nil {
		lookup = func(string) (string, bool) { return provider + "-secret", true }
	}
	cfg := config.EngineConfig{
		ID:       provider + "-engine",
		Provider: provider,
		Model:    provider + "-test",
		KeyEnv:   keyEnv,
	}
	baseURLs := map[string]string{provider: providerBaseURL(provider)}
	if provider == "openai-compatible" {
		cfg.BaseURL = "http://localhost:11434/v1"
		baseURLs[provider] = cfg.BaseURL
	}
	engine, err := NewFactory(FactoryOptions{
		Transport:      transport,
		BaseURLs:       baseURLs,
		LookupEnv:      lookup,
		RegisterSecret: register,
	}).NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func providerBaseURL(provider string) string {
	switch provider {
	case "google":
		return "https://api.test/v1beta"
	case "perplexity":
		return "https://api.test/v1"
	case "anthropic":
		return "https://api.test/v1"
	case "openrouter":
		return "https://api.test/api/v1"
	case "openai-compatible":
		return "http://localhost:11434/v1"
	default:
		return "https://api.test"
	}
}

func providerSuccessBody(provider, text, pad string) string {
	switch provider {
	case "google":
		return `{"id":"resp_google","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"` + text + `"}]}],"pad":"` + pad + `"}`
	case "perplexity":
		return `{"id":"resp_perplexity","choices":[{"message":{"content":"` + text + `"},"finish_reason":"stop"}],"pad":"` + pad + `"}`
	case "anthropic":
		return `{"id":"resp_anthropic","stop_reason":"end_turn","content":[{"type":"text","text":"` + text + `"}],"pad":"` + pad + `"}`
	case "openrouter", "openai-compatible":
		return `{"id":"resp_chat","choices":[{"message":{"content":"` + text + `"},"finish_reason":"stop"}],"pad":"` + pad + `"}`
	default:
		return `{}`
	}
}

func TestProviderAdaptersRejectTrailingResponseData(t *testing.T) {
	for _, provider := range []string{"google", "perplexity", "anthropic", "openrouter", "openai-compatible"} {
		for _, suffix := range []string{"\n{}", " trailing garbage"} {
			t.Run(provider+suffix, func(t *testing.T) {
				calls := 0
				eng := newProviderEngineForTest(t, provider, "SYNTHETIC_KEY", roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return jsonResponse(http.StatusOK, providerSuccessBody(provider, "answer", "")+suffix), nil
				}), nil, nil)
				_, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
				var providerErr *ProviderError
				if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassProvider || providerErr.Retryable || calls != 1 {
					t.Fatalf("malformed response: calls=%d err=%#v", calls, err)
				}
			})
		}
	}
}

type readCloserFunc struct {
	read func([]byte) (int, error)
}

func (r readCloserFunc) Read(p []byte) (int, error) {
	return r.read(p)
}

func (readCloserFunc) Close() error {
	return nil
}

var _ io.ReadCloser = readCloserFunc{}
