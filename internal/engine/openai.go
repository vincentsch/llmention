package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/vincentsch/llmention/internal/domain"
)

const (
	defaultOpenAIBaseURL      = "https://api.openai.com/v1"
	openAIResponsesPath       = "responses"
	openAIWebSearchTool       = "web_search"
	openAISourceInclude       = "web_search_call.action.sources"
	openAISecretRedactionText = "[REDACTED]"
)

// openAIEngine asks OpenAI's Responses API, with its web_search tool in the
// search modes.
type openAIEngine struct {
	baseEngine
}

func newOpenAIEngine(base baseEngine) Engine {
	return &openAIEngine{baseEngine: base}
}

func (e *openAIEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return err
	}
	if req.Mode == ToolPolicySearchCacheOnly && !e.caps.SupportsLiveAccessControl {
		return fmt.Errorf("OpenAI cache-only search requires live access control support")
	}
	if opts == nil {
		return nil
	}
	if len(opts.AllowedDomains) > 0 && len(opts.BlockedDomains) > 0 {
		return fmt.Errorf("OpenAI web_search filters accept allowed_domains or blocked_domains, not both")
	}
	if len(opts.AllowedDomains) > 100 {
		return fmt.Errorf("OpenAI web_search filters allow at most 100 allowed domains")
	}
	if len(opts.BlockedDomains) > 100 {
		return fmt.Errorf("OpenAI web_search filters allow at most 100 blocked domains")
	}
	if hasWildcardDomain(opts.AllowedDomains) || hasWildcardDomain(opts.BlockedDomains) {
		return fmt.Errorf("OpenAI web_search filters do not support wildcard domains")
	}
	if opts.Locale != "" {
		return fmt.Errorf("OpenAI web_search does not support locale search options")
	}
	if opts.RecencyDays > 0 {
		return fmt.Errorf("OpenAI web_search does not support recency search options")
	}
	if opts.DateFrom != "" || opts.DateTo != "" {
		return fmt.Errorf("OpenAI web_search does not support date filter search options")
	}
	if opts.MaxSearches > 0 {
		return fmt.Errorf("OpenAI web_search does not support max search query options")
	}
	return nil
}

func (e *openAIEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	endpoint, err := providerEndpoint(e.baseURL, defaultOpenAIBaseURL, openAIResponsesPath)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassConfig}
	}
	body, err := e.responsesRequest(req)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	resp, err := e.post(ctx, endpoint, map[string]string{"Authorization": "Bearer " + key}, body, key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, openAIHTTPError(e.provider, resp, key)
	}
	answer, err := openAIAnswerFromResponse(resp.body, resp.header, req)
	return e.finish(answer, err, resp)
}

func (e *openAIEngine) responsesRequest(req Request) (map[string]any, error) {
	body := map[string]any{
		"model": e.model,
		"input": req.Prompt,
		"store": false,
	}
	if req.MaxOutputTokens > 0 {
		body["max_output_tokens"] = req.MaxOutputTokens
	}
	if req.Mode == ToolPolicyMemory {
		body["tool_choice"] = "none"
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict {
		body["text"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   req.OutputSchema.Name,
				"schema": json.RawMessage(req.OutputSchema.SchemaJSON),
				"strict": true,
			},
		}
	}
	if !req.Mode.IsSearchMode() {
		return body, nil
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return nil, err
	}
	tool := map[string]any{"type": openAIWebSearchTool}
	if opts != nil {
		if len(opts.AllowedDomains) > 0 {
			tool["filters"] = map[string]any{"allowed_domains": opts.AllowedDomains}
		}
		if len(opts.BlockedDomains) > 0 {
			tool["filters"] = map[string]any{"blocked_domains": opts.BlockedDomains}
		}
		if opts.UserLocation != nil && !opts.UserLocation.IsZero() {
			tool["user_location"] = openAIUserLocation(opts.UserLocation)
		}
		if opts.IncludeSourceMetadata {
			body["include"] = []string{openAISourceInclude}
		}
	}
	if req.Mode == ToolPolicySearchCacheOnly {
		tool["external_web_access"] = false
	}
	body["tools"] = []map[string]any{tool}
	if req.Mode == ToolPolicySearchRequired || (req.Mode == ToolPolicySearchCacheOnly && opts != nil && opts.RequireSearch) {
		body["tool_choice"] = "required"
	} else {
		body["tool_choice"] = "auto"
	}
	return body, nil
}

type openAIResponse struct {
	ID                string               `json:"id"`
	Model             string               `json:"model"`
	Status            string               `json:"status"`
	Output            []json.RawMessage    `json:"output"`
	Tools             []openAIResponseTool `json:"tools"`
	Usage             *openAIUsage         `json:"usage"`
	IncompleteDetails *openAIIncomplete    `json:"incomplete_details"`
	Error             *openAIResponseError `json:"error"`
	ServiceTier       string               `json:"service_tier"`
	ToolUsage         *struct {
		WebSearch *struct {
			NumRequests int `json:"num_requests"`
		} `json:"web_search"`
	} `json:"tool_usage"`
}

type openAIResponseTool struct {
	Type string `json:"type"`
}

type openAIUsage struct {
	InputTokens         int                    `json:"input_tokens"`
	OutputTokens        int                    `json:"output_tokens"`
	TotalTokens         int                    `json:"total_tokens"`
	InputTokensDetails  map[string]json.Number `json:"input_tokens_details"`
	OutputTokensDetails map[string]json.Number `json:"output_tokens_details"`
}

type openAIIncomplete struct {
	Reason string `json:"reason"`
}

type openAIResponseError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

type openAIOutputItem struct {
	Type    string               `json:"type"`
	ID      string               `json:"id"`
	Status  string               `json:"status"`
	Content []openAIContentPart  `json:"content"`
	Action  *openAIWebAction     `json:"action"`
	Query   string               `json:"query"`
	Queries []string             `json:"queries"`
	URL     string               `json:"url"`
	Error   *openAIResponseError `json:"error"`
}

type openAIContentPart struct {
	Type        string             `json:"type"`
	Text        string             `json:"text"`
	Refusal     string             `json:"refusal"`
	Annotations []openAIAnnotation `json:"annotations"`
}

type openAIAnnotation struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	StartIndex *int   `json:"start_index"`
	EndIndex   *int   `json:"end_index"`
}

type openAIWebAction struct {
	Type    string         `json:"type"`
	Query   string         `json:"query"`
	Queries []string       `json:"queries"`
	URL     string         `json:"url"`
	Sources []openAISource `json:"sources"`
}

type openAISource struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

func openAIAnswerFromResponse(raw []byte, header http.Header, req Request) (Answer, error) {
	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Answer{}, &ProviderError{
			SafeMessage: "OpenAI sent a response llmention could not read",
			Class:       ErrorClassProvider,
			SafeSnippet: SafeResponseSnippet(raw),
		}
	}
	if parsed.Error != nil {
		return Answer{}, &ProviderError{
			SafeMessage: "OpenAI reported an error: " + parsed.Error.Message,
			Class:       ErrorClassProvider,
			SafeSnippet: SafeResponseSnippet(raw),
		}
	}
	// "incomplete" still carries the text so far; it is saved as unfinished.
	switch parsed.Status {
	case "", "completed", "incomplete":
	default:
		return Answer{}, &ProviderError{
			SafeMessage: fmt.Sprintf("OpenAI returned status %q instead of a finished answer", parsed.Status),
			Class:       ErrorClassProvider,
			SafeSnippet: SafeResponseSnippet(raw),
		}
	}

	var textParts []string
	providerRefused := false
	var citations []Citation
	var sources []Source
	var toolCalls []ToolCall
	for _, rawItem := range parsed.Output {
		var item openAIOutputItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return Answer{}, &ProviderError{
				SafeMessage: "OpenAI sent a part of the response llmention could not read",
				Class:       ErrorClassProvider,
				SafeSnippet: SafeResponseSnippet(raw),
			}
		}
		if item.Type == "message" {
			for _, part := range item.Content {
				if part.Type == "refusal" {
					providerRefused = true
					textParts = append(textParts, part.Refusal)
					continue
				}
				if part.Type != "output_text" {
					continue
				}
				textParts = append(textParts, part.Text)
				citations = append(citations, openAICitations(part.Annotations)...)
			}
			continue
		}
		if item.Type == "web_search_call" {
			toolCalls = append(toolCalls, openAIToolCall(item, rawItem))
			sources = append(sources, openAISources(item)...)
		}
	}
	incomplete := parsed.Status == "incomplete"
	if strings.TrimSpace(strings.Join(textParts, "")) == "" && !incomplete {
		return Answer{}, &ProviderError{
			SafeMessage: "OpenAI sent a response without answer text",
			Class:       ErrorClassProvider,
			SafeSnippet: SafeResponseSnippet(raw),
		}
	}

	metadata := map[string]string{}
	setMetadata(metadata, "response_id", parsed.ID)
	setMetadata(metadata, "model", parsed.Model)
	setMetadata(metadata, "status", parsed.Status)
	setMetadata(metadata, "request_id", requestIDFrom(header))
	setMetadata(metadata, "service_tier", parsed.ServiceTier)
	if parsed.IncompleteDetails != nil {
		setMetadata(metadata, "incomplete_reason", parsed.IncompleteDetails.Reason)
	}
	if parsed.Usage != nil {
		setMetadataInt(metadata, "usage_input_tokens", parsed.Usage.InputTokens)
		setMetadataInt(metadata, "usage_output_tokens", parsed.Usage.OutputTokens)
		setMetadataInt(metadata, "usage_total_tokens", parsed.Usage.TotalTokens)
		setMetadataNumber(metadata, "usage_cached_tokens", parsed.Usage.InputTokensDetails["cached_tokens"])
		setMetadataNumber(metadata, "usage_cache_write_tokens", parsed.Usage.InputTokensDetails["cache_write_tokens"])
		setMetadataNumber(metadata, "usage_reasoning_tokens", parsed.Usage.OutputTokensDetails["reasoning_tokens"])
	}
	// OpenAI bills each search. Opening a page it found is not a search.
	if parsed.ToolUsage != nil && parsed.ToolUsage.WebSearch != nil {
		setMetadataInt(metadata, "usage_search_queries", parsed.ToolUsage.WebSearch.NumRequests)
	}

	answer := Answer{
		Text:             strings.Join(textParts, ""),
		Citations:        citations,
		SourcesConsulted: sources,
		ToolCalls:        toolCalls,
		ToolsAvailable:   openAIResponseToolNames(parsed.Tools),
		ToolsUsed:        openAIToolsUsed(toolCalls),
		StopReason:       openAIStopReason(parsed),
		Unfinished:       incomplete,
		ProviderMetadata: metadata,
	}
	if providerRefused {
		answer.StopReason = "refusal"
	}
	if req.OutputSchema != nil && !providerRefused && !incomplete {
		structured, ok := compactJSON([]byte(answer.Text))
		if ok {
			answer.Parsed = structured
		} else if req.OutputSchema.Strict {
			return Answer{}, &ProviderError{
				SafeMessage: "OpenAI was asked for JSON, but the answer is not valid JSON",
				Class:       ErrorClassProvider,
				SafeSnippet: SafeResponseSnippet(raw),
			}
		}
	}
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(raw, answer.ProviderMetadata)
	if len(raw) > MaxRawPayloadBytes {
		if answer.ProviderMetadata == nil {
			answer.ProviderMetadata = map[string]string{}
		}
		answer.ProviderMetadata["raw_truncated"] = "true"
	}
	return answer, nil
}

func openAIHTTPError(provider string, resp reply, key string) *ProviderError {
	var payload struct {
		Error *openAIResponseError `json:"error"`
	}
	message, hint := "", ""
	if json.Unmarshal(resp.body, &payload) == nil && payload.Error != nil {
		message = payload.Error.Message
		if stringifyOpenAIValue(payload.Error.Code) == "model_not_found" {
			hint = unknownModelHint
		}
	}
	return httpError(provider, resp, key, message, hint, statusClass(resp.status))
}

func openAIUserLocation(location *SearchLocation) map[string]string {
	out := map[string]string{"type": "approximate"}
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

func openAICitations(annotations []openAIAnnotation) []Citation {
	var out []Citation
	for _, annotation := range annotations {
		if annotation.Type != "url_citation" {
			continue
		}
		citation := Citation{
			URL:    annotation.URL,
			Title:  annotation.Title,
			Domain: normalizedOpenAIDomain(annotation.URL),
		}
		metadata := map[string]string{}
		if annotation.StartIndex != nil {
			metadata["start_index"] = strconv.Itoa(*annotation.StartIndex)
		}
		if annotation.EndIndex != nil {
			metadata["end_index"] = strconv.Itoa(*annotation.EndIndex)
		}
		if len(metadata) > 0 {
			citation.Metadata = metadata
		}
		out = append(out, citation)
	}
	return out
}

func openAIToolCall(item openAIOutputItem, raw json.RawMessage) ToolCall {
	call := ToolCall{
		Type:               openAIWebSearchTool,
		Query:              openAIToolQuery(item),
		URL:                openAIToolURL(item),
		Status:             item.Status,
		ProviderToolCallID: item.ID,
		Raw:                capOpenAIRaw(raw),
	}
	if item.Error != nil {
		call.ErrorCode = stringifyOpenAIValue(item.Error.Code)
		call.ErrorMessage = item.Error.Message
	}
	return call
}

func openAIToolQuery(item openAIOutputItem) string {
	if item.Action != nil && len(item.Action.Queries) > 0 {
		if queries := nonEmptyStrings(item.Action.Queries); len(queries) > 0 {
			return strings.Join(queries, "\n")
		}
	}
	if item.Action != nil && item.Action.Query != "" {
		return item.Action.Query
	}
	if item.Query != "" {
		return item.Query
	}
	if len(item.Queries) > 0 {
		if queries := nonEmptyStrings(item.Queries); len(queries) > 0 {
			return strings.Join(queries, "\n")
		}
	}
	return ""
}

func openAIToolURL(item openAIOutputItem) string {
	if item.Action != nil && item.Action.URL != "" {
		return item.Action.URL
	}
	return item.URL
}

func openAISources(item openAIOutputItem) []Source {
	if item.Action == nil {
		return nil
	}
	out := make([]Source, 0, len(item.Action.Sources))
	for _, source := range item.Action.Sources {
		mapped := Source{
			URL:    source.URL,
			Title:  source.Title,
			Domain: normalizedOpenAIDomain(source.URL),
			Kind:   "web",
		}
		if source.Type != "" {
			mapped.Metadata = map[string]string{"provider_source_type": source.Type}
		}
		out = append(out, mapped)
	}
	return out
}

func openAIResponseToolNames(tools []openAIResponseTool) []string {
	out := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	for _, tool := range tools {
		name := tool.Type
		if name == "" {
			continue
		}
		if name == "web_search_preview" {
			name = openAIWebSearchTool
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func openAIToolsUsed(calls []ToolCall) []string {
	out := make([]string, 0, len(calls))
	seen := map[string]struct{}{}
	for _, call := range calls {
		if call.Type == "" {
			continue
		}
		if _, ok := seen[call.Type]; ok {
			continue
		}
		seen[call.Type] = struct{}{}
		out = append(out, call.Type)
	}
	return out
}

func openAIStopReason(response openAIResponse) string {
	if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
		return response.IncompleteDetails.Reason
	}
	return response.Status
}

func normalizedOpenAIDomain(value string) string {
	normalized, err := domain.NormalizeScoringDomain(value)
	if err != nil {
		return ""
	}
	return normalized
}

func compactJSON(data []byte) (json.RawMessage, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, false
	}
	return append(json.RawMessage(nil), buf.Bytes()...), true
}

func setMetadata(metadata map[string]string, key, value string) {
	if value == "" {
		return
	}
	metadata[key] = value
}

func setMetadataInt(metadata map[string]string, key string, value int) {
	if value == 0 {
		return
	}
	metadata[key] = strconv.Itoa(value)
}

func setMetadataNumber(metadata map[string]string, key string, value json.Number) {
	if value == "" {
		return
	}
	metadata[key] = value.String()
}

func stringifyOpenAIValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}

func hasWildcardDomain(domains []string) bool {
	for _, value := range domains {
		if strings.HasPrefix(value, "*.") {
			return true
		}
	}
	return false
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func capOpenAIRaw(raw []byte) []byte {
	if len(raw) > MaxRawPayloadBytes {
		raw = raw[:MaxRawPayloadBytes]
	}
	return append([]byte(nil), raw...)
}
