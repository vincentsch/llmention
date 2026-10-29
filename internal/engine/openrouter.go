package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	openRouterSearchTool     = "openrouter:web_search"
	openRouterDefaultMaxUse  = 5
)

// openRouterEngine asks a model through OpenRouter's Chat Completions API,
// with OpenRouter's own web search tool in the search mode. Perplexity's
// models search by themselves instead.
type openRouterEngine struct {
	baseEngine
}

func newOpenRouterEngine(base baseEngine) Engine {
	return &openRouterEngine{baseEngine: base}
}

func (e *openRouterEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict && req.Mode.IsSearchMode() {
		if !e.caps.SupportsStructuredOutput || !e.caps.SupportsStructuredOutputWithTools || !e.caps.SupportsStructuredOutputWithCitations {
			return fmt.Errorf("JSON answers with web search on OpenRouter need capabilities.supports_structured_output, supports_structured_output_with_tools and supports_structured_output_with_citations set to true")
		}
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return err
	}
	if opts == nil {
		return nil
	}
	if hasWildcardSearchDomain(opts.AllowedDomains) || hasWildcardSearchDomain(opts.BlockedDomains) {
		return fmt.Errorf("OpenRouter web search does not support wildcard domains")
	}
	if opts.MaxSearches > 30 {
		return fmt.Errorf("OpenRouter web search max searches must be between 1 and 30")
	}
	if opts.Locale != "" {
		return fmt.Errorf("OpenRouter web search does not support locale filters")
	}
	if opts.UserLocation != nil && (len(opts.AllowedDomains) > 0 || len(opts.BlockedDomains) > 0) {
		return fmt.Errorf("OpenRouter web search does not support user location with domain filters")
	}
	return nil
}

func (e *openRouterEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	endpoint, err := providerEndpoint(e.baseURL, defaultOpenRouterBaseURL, "chat", "completions")
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassConfig}
	}
	body, err := e.chatRequest(req)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	resp, err := e.post(ctx, endpoint, map[string]string{"Authorization": "Bearer " + key}, body, key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, openRouterHTTPError(e.provider, resp, key)
	}
	answer, err := chatCompletionAnswerFromResponse(chatCompletionParseInput{
		Provider:             e.provider,
		Raw:                  resp.body,
		Header:               resp.header,
		Request:              req,
		AllowCitations:       true,
		AllowSources:         true,
		ResponseIDFromHeader: "X-Generation-Id",
	})
	return e.finish(answer, err, resp)
}

func (e *openRouterEngine) chatRequest(req Request) (map[string]any, error) {
	body := map[string]any{
		"model": e.model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": req.Prompt,
		}},
	}
	if req.MaxOutputTokens > 0 {
		body["max_tokens"] = req.MaxOutputTokens
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict {
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   req.OutputSchema.Name,
				"strict": true,
				"schema": json.RawMessage(req.OutputSchema.SchemaJSON),
			},
		}
	}
	if openRouterSearchesItself(e.model) {
		if req.Mode == ToolPolicyMemory {
			body["disable_search"] = true
		}
		return body, nil
	}
	if req.Mode != ToolPolicySearchAuto {
		return body, nil
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return nil, err
	}
	maxUses := openRouterDefaultMaxUse
	if opts != nil && opts.MaxSearches > 0 {
		maxUses = opts.MaxSearches
	}
	parameters := map[string]any{"max_uses": maxUses}
	if opts != nil {
		if len(opts.AllowedDomains) > 0 {
			parameters["engine"] = "exa"
			parameters["allowed_domains"] = opts.AllowedDomains
		}
		if len(opts.BlockedDomains) > 0 {
			parameters["engine"] = "exa"
			parameters["excluded_domains"] = opts.BlockedDomains
		}
		if opts.UserLocation != nil && !opts.UserLocation.IsZero() {
			parameters["user_location"] = openRouterLocation(opts.UserLocation)
		}
	}
	body["tools"] = []map[string]any{{
		"type":       openRouterSearchTool,
		"parameters": parameters,
	}}
	body["max_tool_calls"] = maxUses
	return body, nil
}

// openRouterSearchesItself reports whether a model searches the web on its
// own. Perplexity's models on OpenRouter search before every answer and take
// no tools, so a request with OpenRouter's search tool fails. OpenRouter
// passes Perplexity's own disable_search switch on, which llmention sends in
// the no web search mode.
func openRouterSearchesItself(model string) bool {
	return strings.HasPrefix(model, "perplexity/")
}

func openRouterLocation(location *SearchLocation) map[string]string {
	out := map[string]string{}
	if location.CountryCode != "" {
		out["country"] = strings.ToUpper(location.CountryCode)
	}
	if location.Region != "" {
		out["region"] = location.Region
	}
	if location.City != "" {
		out["city"] = location.City
	}
	if location.Timezone != "" {
		out["timezone"] = location.Timezone
	}
	return out
}

type openRouterErrorPayload struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func openRouterHTTPError(provider string, resp reply, key string) *ProviderError {
	message, code := "", ""
	var payload openRouterErrorPayload
	if json.Unmarshal(resp.body, &payload) == nil && payload.Error != nil {
		message, code = payload.Error.Message, firstNonEmpty(payload.Error.Type, fmt.Sprint(payload.Error.Code))
	}
	return httpError(provider, resp, key, message, openRouterHint(message), openRouterErrorClass(resp.status, code, string(resp.body)))
}

// openRouterHint says what to do about the OpenRouter errors that a wrong
// setting in llmention.yaml causes.
func openRouterHint(message string) string {
	switch {
	case strings.Contains(message, "is not a valid model ID"):
		return unknownModelHint
	case strings.Contains(message, "support tool use"):
		// OpenRouter has no endpoint for this model that takes its web
		// search tool.
		return "This model cannot use OpenRouter's web search. Leave the web search mode out for this engine with capabilities: {supports_search_auto: false}."
	}
	return ""
}

func openRouterErrorClass(status int, code, body string) ErrorClass {
	code = strings.ToLower(code)
	body = strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || code == "invalid_api_key" || strings.Contains(code, "authentication"):
		return ErrorClassConfig
	case status == http.StatusPaymentRequired || code == "insufficient_credits" || code == "payment_required":
		return ErrorClassConfig
	case status == http.StatusForbidden || strings.Contains(code, "permission") || strings.Contains(code, "account"):
		return ErrorClassConfig
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || code == "timeout" || code == "conflict" || code == "rate_limit_exceeded":
		return ErrorClassTransient
	case status >= 500 || code == "provider_unavailable" || code == "provider_overloaded" || code == "server" || code == "api_error":
		return ErrorClassTransient
	case status >= 400:
		if strings.Contains(body, "guardrail") || strings.Contains(body, "moderation") || strings.Contains(body, "content_policy") {
			return ErrorClassCaller
		}
		return ErrorClassCaller
	default:
		return ErrorClassProvider
	}
}
