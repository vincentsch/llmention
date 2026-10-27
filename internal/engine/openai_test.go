package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

func TestOpenAIRequestModesAndSearchOptions(t *testing.T) {
	var bodies []map[string]any
	var authHeaders []string
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.test/v1/responses" {
			t.Fatalf("request target = %s %s", req.Method, req.URL.String())
		}
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		bodies = append(bodies, decodeRequestBody(t, req))
		return jsonResponse(http.StatusOK, `{"id":"resp_ok","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`), nil
	}), nil, nil)

	requests := []Request{
		func() Request {
			req := validRequest(ToolPolicyMemory)
			req.MaxOutputTokens = 25
			return req
		}(),
		validRequest(ToolPolicyProviderDefault),
		func() Request {
			req := validRequest(ToolPolicySearchAuto)
			req.SearchOptions = &SearchOptions{
				AllowedDomains:        []string{"https://Example.com/path", "example.com."},
				UserLocation:          &SearchLocation{CountryCode: "us", Region: "CA", City: "San Francisco", Timezone: "America/Los_Angeles"},
				IncludeSourceMetadata: true,
			}
			return req
		}(),
		func() Request {
			req := validRequest(ToolPolicySearchRequired)
			req.SearchOptions = &SearchOptions{BlockedDomains: []string{"Blocked.example"}}
			return req
		}(),
		func() Request {
			req := validRequest(ToolPolicySearchCacheOnly)
			req.SearchOptions = &SearchOptions{RequireSearch: true}
			return req
		}(),
	}
	for _, req := range requests {
		if _, err := engine.Ask(context.Background(), req); err != nil {
			t.Fatalf("Ask(%s) error = %v", req.Mode, err)
		}
	}
	if len(bodies) != len(requests) {
		t.Fatalf("captured %d request bodies, want %d", len(bodies), len(requests))
	}
	for _, auth := range authHeaders {
		if auth != "Bearer sk-test-openai" {
			t.Fatalf("Authorization header = %q", auth)
		}
	}

	memory := bodies[0]
	assertField(t, memory, "model", "gpt-test")
	assertField(t, memory, "input", "Who mentions Acme?")
	assertBoolField(t, memory, "store", false)
	assertNumberField(t, memory, "max_output_tokens", 25)
	assertField(t, memory, "tool_choice", "none")
	assertAbsent(t, memory, "tools")

	providerDefault := bodies[1]
	assertBoolField(t, providerDefault, "store", false)
	assertAbsent(t, providerDefault, "tools")
	assertAbsent(t, providerDefault, "tool_choice")

	searchAuto := bodies[2]
	assertField(t, searchAuto, "tool_choice", "auto")
	assertStringSlice(t, searchAuto, "include", []string{openAISourceInclude})
	autoTool := onlyTool(t, searchAuto)
	assertField(t, autoTool, "type", "web_search")
	assertAbsent(t, autoTool, "external_web_access")
	assertNestedStringSlice(t, autoTool, "filters", "allowed_domains", []string{"example.com"})
	location := asMap(t, autoTool["user_location"])
	assertField(t, location, "type", "approximate")
	assertField(t, location, "country", "US")
	assertField(t, location, "region", "CA")
	assertField(t, location, "city", "San Francisco")
	assertField(t, location, "timezone", "America/Los_Angeles")

	searchRequired := bodies[3]
	assertField(t, searchRequired, "tool_choice", "required")
	requiredTool := onlyTool(t, searchRequired)
	assertNestedStringSlice(t, requiredTool, "filters", "blocked_domains", []string{"blocked.example"})

	cacheOnly := bodies[4]
	assertField(t, cacheOnly, "tool_choice", "required")
	cacheTool := onlyTool(t, cacheOnly)
	assertField(t, cacheTool, "type", "web_search")
	assertBoolField(t, cacheTool, "external_web_access", false)
}

func TestOpenAIRequestValidationRejectsUnsupportedOptionsBeforeHTTP(t *testing.T) {
	calls := 0
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{}`), nil
	}), config.Capabilities{
		"supports_search_recency":      true,
		"supports_search_date_filters": true,
		"supports_search_max_queries":  true,
	}, nil)

	tests := []struct {
		name string
		req  Request
		want string
	}{
		{
			name: "allow and block lists",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{AllowedDomains: []string{"example.com"}, BlockedDomains: []string{"blocked.example"}}
				return req
			}(),
			want: "allowed_domains or blocked_domains",
		},
		{
			name: "wildcard domain",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{AllowedDomains: []string{"*.example.com"}}
				return req
			}(),
			want: "wildcard",
		},
		{
			name: "locale",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{Locale: "en-US"}
				return req
			}(),
			want: "locale",
		},
		{
			name: "recency",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{RecencyDays: 7}
				return req
			}(),
			want: "recency",
		},
		{
			name: "date",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{DateFrom: "2026-08-01"}
				return req
			}(),
			want: "date filter",
		},
		{
			name: "max searches",
			req: func() Request {
				req := validRequest(ToolPolicySearchAuto)
				req.SearchOptions = &SearchOptions{MaxSearches: 2}
				return req
			}(),
			want: "max search",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Ask(context.Background(), tt.req)
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
			}
			if providerErr.Class != ErrorClassCaller || !strings.Contains(providerErr.SafeMessage, tt.want) {
				t.Fatalf("providerErr = %#v, want message containing %q", providerErr, tt.want)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls)
	}
}

func TestOpenAIValidationRejectsStrictSchemaWhenCapabilityDisabled(t *testing.T) {
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP should not be called")
		return nil, nil
	}), config.Capabilities{
		"supports_structured_output_with_tools": false,
	}, nil)
	req := validRequest(ToolPolicySearchAuto)
	req.OutputSchema = &OutputSchema{
		Name:       "answer",
		Format:     OutputFormatJSONSchema,
		SchemaJSON: []byte(`{"type":"object"}`),
		Strict:     true,
	}
	_, err := engine.Ask(context.Background(), req)
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassCaller || !strings.Contains(providerErr.SafeMessage, "with tools") {
		t.Fatalf("Ask() error = %#v, want caller structured-output error", err)
	}
}

func TestOpenAICacheOnlyValidationRequiresLiveAccessControl(t *testing.T) {
	calls := 0
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{}`), nil
	}), config.Capabilities{
		"supports_live_access_control": false,
	}, nil)
	_, err := engine.Ask(context.Background(), validRequest(ToolPolicySearchCacheOnly))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
	}
	if providerErr.Class != ErrorClassCaller || !strings.Contains(providerErr.SafeMessage, "live access control") {
		t.Fatalf("providerErr = %#v, want caller live-access error", providerErr)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls)
	}
}

func TestOpenAIMapsStructuredResponseEvidenceAndMetadata(t *testing.T) {
	var requestBody map[string]any
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestBody = decodeRequestBody(t, req)
		resp := jsonResponse(http.StatusOK, `{
			"id": "resp_123",
			"model": "gpt-test",
			"status": "completed",
			"service_tier": "default",
			"output": [
				{
					"type": "web_search_call",
					"id": "ws_1",
					"status": "completed",
					"action": {
						"type": "search",
						"queries": ["Acme search", "Acme reviews"],
						"sources": [
							{"type": "url", "url": "https://Example.com/source", "title": "Example source"}
						]
					}
				},
				{
					"type": "message",
					"id": "msg_1",
					"status": "completed",
					"content": [
						{
							"type": "output_text",
							"text": "{\"mentioned\":true,\"rank\":1}",
							"annotations": [
								{"type": "url_citation", "start_index": 0, "end_index": 10, "url": "https://Acme.example/page", "title": "Acme page"}
							]
						}
					]
				}
			],
			"tools": [{"type": "web_search"}],
			"usage": {
				"input_tokens": 10,
				"output_tokens": 5,
				"total_tokens": 15,
				"input_tokens_details": {"cached_tokens": 2, "cache_write_tokens": 1},
				"output_tokens_details": {"reasoning_tokens": 3}
			}
		}`)
		resp.Header.Set("x-request-id", "req_123")
		return resp, nil
	}), nil, nil)

	req := validRequest(ToolPolicySearchAuto)
	req.OutputSchema = &OutputSchema{
		Name:       "answer",
		Format:     OutputFormatJSONSchema,
		SchemaJSON: []byte(`{"type":"object","properties":{"mentioned":{"type":"boolean"},"rank":{"type":"number"}}}`),
		Strict:     true,
	}
	answer, err := engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	text := asMap(t, requestBody["text"])
	format := asMap(t, text["format"])
	assertField(t, format, "type", "json_schema")
	assertField(t, format, "name", "answer")
	assertBoolField(t, format, "strict", true)
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("text.format.schema = %#v, want object", format["schema"])
	}

	if answer.Text != `{"mentioned":true,"rank":1}` {
		t.Fatalf("answer.Text = %q", answer.Text)
	}
	if string(answer.Parsed) != `{"mentioned":true,"rank":1}` {
		t.Fatalf("answer.Parsed = %s", answer.Parsed)
	}
	if got := answer.Citations[0].Domain; got != "acme.example" {
		t.Fatalf("citation domain = %q", got)
	}
	if got := answer.Citations[0].Metadata["start_index"]; got != "0" {
		t.Fatalf("citation metadata = %#v", answer.Citations[0].Metadata)
	}
	if got := answer.SourcesConsulted[0].Domain; got != "example.com" {
		t.Fatalf("source domain = %q", got)
	}
	if answer.SourcesConsulted[0].Kind != "web" || answer.SourcesConsulted[0].Metadata["provider_source_type"] != "url" {
		t.Fatalf("source = %#v", answer.SourcesConsulted[0])
	}
	if got := answer.ToolCalls[0].ProviderToolCallID; got != "ws_1" {
		t.Fatalf("tool call id = %q", got)
	}
	if answer.ToolCalls[0].Query != "Acme search\nAcme reviews" || answer.ToolCalls[0].Status != "completed" {
		t.Fatalf("tool call = %#v", answer.ToolCalls[0])
	}
	if got, want := answer.ToolsAvailable, []string{"web_search"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tools available = %#v, want %#v", got, want)
	}
	if got, want := answer.ToolsUsed, []string{"web_search"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tools used = %#v, want %#v", got, want)
	}
	if answer.StopReason != "completed" {
		t.Fatalf("StopReason = %q", answer.StopReason)
	}
	if answer.ProviderMetadata["response_id"] != "resp_123" ||
		answer.ProviderMetadata["request_id"] != "req_123" ||
		answer.ProviderMetadata["usage_total_tokens"] != "15" ||
		answer.ProviderMetadata["usage_reasoning_tokens"] != "3" {
		t.Fatalf("metadata = %#v", answer.ProviderMetadata)
	}
}

func TestOpenAINonStrictSchemaParsesOnlyValidReturnedJSON(t *testing.T) {
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":"resp_text","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"not json"}]}]}`), nil
	}), nil, nil)
	req := validRequest(ToolPolicyMemory)
	req.OutputSchema = &OutputSchema{
		Name:       "answer",
		Format:     OutputFormatJSONSchema,
		SchemaJSON: []byte(`{"type":"object"}`),
		Strict:     false,
	}
	answer, err := engine.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Parsed != nil {
		t.Fatalf("Parsed = %s, want nil", answer.Parsed)
	}
}

func TestOpenAIProviderErrorsAndCancellation(t *testing.T) {
	t.Run("malformed response", func(t *testing.T) {
		engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			resp := jsonResponse(http.StatusOK, `{bad json`)
			resp.Header.Set("x-request-id", "req_bad")
			return resp, nil
		}), nil, nil)
		_, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
		var providerErr *ProviderError
		if !errors.As(err, &providerErr) {
			t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
		}
		if providerErr.Class != ErrorClassProvider || providerErr.StatusCode != http.StatusOK || providerErr.RequestID != "req_bad" {
			t.Fatalf("providerErr = %#v", providerErr)
		}
	})

	t.Run("rate limit response", func(t *testing.T) {
		engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			resp := jsonResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited for sk-test-openai","type":"rate_limit_error","code":"rate_limit"}}`)
			resp.Header.Set("x-request-id", "req_rate")
			resp.Header.Set("Retry-After", "2")
			return resp, nil
		}), nil, nil)
		_, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
		var providerErr *ProviderError
		if !errors.As(err, &providerErr) {
			t.Fatalf("Ask() error = %T %v, want ProviderError", err, err)
		}
		if providerErr.Class != ErrorClassTransient || !providerErr.Retryable || providerErr.StatusCode != http.StatusTooManyRequests || providerErr.RequestID != "req_rate" {
			t.Fatalf("providerErr = %#v", providerErr)
		}
		if providerErr.RetryAfter == nil || *providerErr.RetryAfter != 2*time.Second {
			t.Fatalf("RetryAfter = %#v", providerErr.RetryAfter)
		}
		if strings.Contains(providerErr.SafeMessage, "sk-test-openai") || strings.Contains(providerErr.SafeSnippet, "sk-test-openai") {
			t.Fatalf("providerErr leaked secret: %#v", providerErr)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		calls := 0
		engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return jsonResponse(http.StatusOK, `{}`), nil
		}), nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := engine.Ask(ctx, validRequest(ToolPolicyMemory))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Ask() error = %v, want context.Canceled", err)
		}
		if calls != 0 {
			t.Fatalf("HTTP calls = %d, want 0", calls)
		}
	})

	t.Run("cancelled during response read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     http.StatusText(http.StatusOK),
				Header:     make(http.Header),
				Body:       &cancelingReadCloser{cancel: cancel, err: context.Canceled},
			}, nil
		}), nil, nil)
		_, err := engine.Ask(ctx, validRequest(ToolPolicyMemory))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Ask() error = %v, want context.Canceled", err)
		}
	})
}

func TestOpenAIRejectsMissingAnswerContent(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"status":"completed","output":[]}`, `{"output":[{"type":"message","content":[]}]}`} {
		t.Run(body, func(t *testing.T) {
			eng := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, body), nil
			}), nil, nil)
			_, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Class != ErrorClassProvider || providerErr.Retryable {
				t.Fatalf("missing answer accepted: err=%#v", err)
			}
		})
	}
}

// A response cut off at max_output_tokens has status "incomplete", with or
// without visible text (OpenAI reasoning guide, checked 2026-10-26).
func TestOpenAIIncompleteResponseIsUnfinished(t *testing.T) {
	for name, body := range map[string]string{
		"partial text": `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"The best tools are"}]}]}`,
		"no text":      `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"reasoning","summary":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			eng := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, body), nil
			}), nil, nil)
			answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
			if err != nil || !answer.Unfinished || answer.StopReason != "max_output_tokens" {
				t.Fatalf("answer = %#v, err = %v", answer, err)
			}
		})
	}
}

func TestOpenAIPreservesRefusalContent(t *testing.T) {
	eng := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot help with that request."}]}]}`), nil
	}), nil, nil)
	answer, err := eng.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil || answer.Text != "I cannot help with that request." {
		t.Fatalf("refusal answer=%q err=%v", answer.Text, err)
	}
}

func TestOpenAIRawCaptureTruncatesAndRedactsProviderKey(t *testing.T) {
	key := "sk-test-openai"
	largePad := strings.Repeat("a", MaxRawPayloadBytes+100)
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"id": "resp_secret",
			"status": "completed",
			"output": [{"type":"message","content":[{"type":"output_text","text":"sk-test-openai"}]}],
			"pad": "`+largePad+`"
		}`), nil
	}), nil, func(string) (string, bool) { return key, true })
	answer, err := engine.Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if strings.Contains(answer.Text, key) {
		t.Fatalf("answer.Text leaked key: %q", answer.Text)
	}
	if len(answer.Raw) != MaxRawPayloadBytes || answer.ProviderMetadata["raw_truncated"] != "true" {
		t.Fatalf("raw capture = len %d metadata %#v", len(answer.Raw), answer.ProviderMetadata)
	}
	if strings.Contains(string(answer.Raw), key) {
		t.Fatal("answer.Raw leaked key")
	}
}

func TestOpenAICapabilities(t *testing.T) {
	engine := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{}`), nil
	}), nil, nil)
	caps := engine.Capabilities()
	for _, mode := range []ToolPolicy{ToolPolicyMemory, ToolPolicyProviderDefault, ToolPolicySearchAuto, ToolPolicySearchRequired, ToolPolicySearchCacheOnly} {
		if !caps.SupportsMode(mode) {
			t.Fatalf("OpenAI SupportsMode(%q) = false", mode)
		}
	}
	if !caps.SupportsLiveAccessControl ||
		!caps.SupportsSearchAllowDomains ||
		!caps.SupportsSearchBlockDomains ||
		!caps.SupportsSearchLocation ||
		!caps.SupportsSourceMetadataRequest ||
		!caps.ExposesCitations ||
		!caps.ExposesConsultedSources ||
		!caps.ExposesToolCalls ||
		!caps.SupportsStructuredOutputWithTools ||
		!caps.SupportsStructuredOutputWithCitations {
		t.Fatalf("OpenAI capabilities = %#v", caps)
	}
	if caps.SupportsSearchRecency || caps.SupportsSearchDateFilters || caps.SupportsSearchMaxQueries {
		t.Fatalf("OpenAI unsupported search capabilities enabled = %#v", caps)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type cancelingReadCloser struct {
	cancel context.CancelFunc
	err    error
}

func (r *cancelingReadCloser) Read([]byte) (int, error) {
	r.cancel()
	return 0, r.err
}

func (*cancelingReadCloser) Close() error {
	return nil
}

func newOpenAIEngineForTest(t *testing.T, transport http.RoundTripper, capabilities config.Capabilities, lookup func(string) (string, bool)) Engine {
	t.Helper()
	if lookup == nil {
		lookup = func(string) (string, bool) { return "sk-test-openai", true }
	}
	engine, err := NewFactory(FactoryOptions{
		Transport: transport,
		BaseURLs:  map[string]string{"openai": "https://api.test/v1"},
		LookupEnv: lookup,
	}).NewEngine(config.EngineConfig{
		ID:           "openai-gpt",
		Provider:     "openai",
		Model:        "gpt-test",
		KeyEnv:       "OPENAI_API_KEY",
		Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func decodeRequestBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func assertAbsent(t *testing.T, object map[string]any, field string) {
	t.Helper()
	if _, ok := object[field]; ok {
		t.Fatalf("field %q present in %#v", field, object)
	}
}

func assertField(t *testing.T, object map[string]any, field string, want string) {
	t.Helper()
	got, ok := object[field].(string)
	if !ok || got != want {
		t.Fatalf("field %q = %#v, want %q", field, object[field], want)
	}
}

func assertBoolField(t *testing.T, object map[string]any, field string, want bool) {
	t.Helper()
	got, ok := object[field].(bool)
	if !ok || got != want {
		t.Fatalf("field %q = %#v, want %v", field, object[field], want)
	}
}

func assertNumberField(t *testing.T, object map[string]any, field string, want int) {
	t.Helper()
	got, ok := object[field].(float64)
	if !ok || int(got) != want {
		t.Fatalf("field %q = %#v, want %d", field, object[field], want)
	}
}

func assertStringSlice(t *testing.T, object map[string]any, field string, want []string) {
	t.Helper()
	got := stringSlice(t, object[field])
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("field %q = %#v, want %#v", field, got, want)
	}
}

func assertNestedStringSlice(t *testing.T, object map[string]any, parent, field string, want []string) {
	t.Helper()
	nested := asMap(t, object[parent])
	assertStringSlice(t, nested, field, want)
}

func onlyTool(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	rawTools, ok := body["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		t.Fatalf("tools = %#v, want one tool", body["tools"])
	}
	return asMap(t, rawTools[0])
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want object", value)
	}
	return object
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want array", value)
	}
	out := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("value[%d] = %#v, want string", i, value)
		}
		out[i] = text
	}
	return out
}
