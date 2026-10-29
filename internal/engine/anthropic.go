package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com/v1"
	anthropicMessagesPath   = "messages"
	anthropicVersion        = "2023-06-01"
	// The basic web search tool works on every current Claude model and
	// platform. Later versions add filtering through code execution, and
	// llmention does not need that.
	anthropicSearchToolType = "web_search_20250305"
	anthropicDefaultMaxUses = 5
	// Anthropic requires max_tokens, and on current models it counts the
	// model's thinking and the answer together. A low cap cuts answers off,
	// so the default leaves room for both. You pay only for the tokens used.
	anthropicDefaultMaxTokens = 16000
)

// anthropicEngine asks Anthropic's Messages API, with its web search tool in
// the search mode.
type anthropicEngine struct {
	baseEngine
}

func newAnthropicEngine(base baseEngine) Engine {
	return &anthropicEngine{baseEngine: base}
}

func (e *anthropicEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return err
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict && req.Mode.IsSearchMode() {
		return fmt.Errorf("strict structured output on Anthropic works only without web search")
	}
	if opts == nil {
		return nil
	}
	if len(opts.AllowedDomains) > 0 && len(opts.BlockedDomains) > 0 {
		return fmt.Errorf("web search on Anthropic accepts allowed_domains or blocked_domains, not both")
	}
	if hasWildcardSearchDomain(opts.AllowedDomains) || hasWildcardSearchDomain(opts.BlockedDomains) {
		return fmt.Errorf("web search on Anthropic does not support wildcard domains")
	}
	if opts.Locale != "" {
		return fmt.Errorf("web search on Anthropic does not support locale filters")
	}
	return nil
}

func (e *anthropicEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	endpoint, err := providerEndpoint(e.baseURL, defaultAnthropicBaseURL, anthropicMessagesPath)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassConfig}
	}
	body, err := e.messagesRequest(req)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	resp, err := e.post(ctx, endpoint, map[string]string{"x-api-key": key, "anthropic-version": anthropicVersion}, body, key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, anthropicHTTPError(e.provider, resp, key)
	}
	answer, err := anthropicAnswerFromResponse(resp.body, resp.header, req)
	return e.finish(answer, err, resp)
}

func (e *anthropicEngine) messagesRequest(req Request) (map[string]any, error) {
	maxTokens := req.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	body := map[string]any{
		"model":      e.model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{{
			"role":    "user",
			"content": req.Prompt,
		}},
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict {
		body["output_config"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"schema": json.RawMessage(req.OutputSchema.SchemaJSON),
			},
		}
	}
	if req.Mode != ToolPolicySearchAuto {
		return body, nil
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return nil, err
	}
	tool := map[string]any{
		"type": anthropicSearchToolType,
		"name": webSearchToolName,
	}
	maxUses := anthropicDefaultMaxUses
	if opts != nil && opts.MaxSearches > 0 {
		maxUses = opts.MaxSearches
	}
	tool["max_uses"] = maxUses
	if opts != nil {
		if len(opts.AllowedDomains) > 0 {
			tool["allowed_domains"] = opts.AllowedDomains
		}
		if len(opts.BlockedDomains) > 0 {
			tool["blocked_domains"] = opts.BlockedDomains
		}
		if opts.UserLocation != nil && !opts.UserLocation.IsZero() {
			tool["user_location"] = anthropicLocation(opts.UserLocation)
		}
	}
	body["tools"] = []map[string]any{tool}
	return body, nil
}

type anthropicMessageResponse struct {
	ID         string           `json:"id"`
	Model      string           `json:"model"`
	StopReason string           `json:"stop_reason"`
	Content    []anthropicBlock `json:"content"`
	Usage      *anthropicUsage  `json:"usage"`
}

type anthropicUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	ServerToolUse        *struct {
		WebSearchRequests int `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

type anthropicBlock struct {
	Type      string              `json:"type"`
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Text      string              `json:"text"`
	Input     map[string]any      `json:"input"`
	Citations []anthropicCitation `json:"citations"`
	ToolUseID string              `json:"tool_use_id"`
	Content   json.RawMessage     `json:"content"`
	Raw       json.RawMessage     `json:"-"`
}

type anthropicCitation struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	CitedText string `json:"cited_text"`
}

func (b *anthropicBlock) UnmarshalJSON(data []byte) error {
	type alias anthropicBlock
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*b = anthropicBlock(out)
	b.Raw = append([]byte(nil), data...)
	return nil
}

type anthropicToolResultEntry struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func anthropicAnswerFromResponse(raw []byte, header http.Header, req Request) (Answer, error) {
	var parsed anthropicMessageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Answer{}, malformedProviderResponse("anthropic", raw, "Anthropic sent a response llmention could not read")
	}
	mapper := &evidenceMapper{}
	var textParts []string
	var citations []Citation
	toolCallsByID := map[string]*ToolCall{}
	var toolOrder []string
	var sources []Source
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			citations = append(citations, anthropicCitations(mapper, block.Citations)...)
		case "server_tool_use":
			id := block.ID
			if id == "" {
				id = block.ToolUseID
			}
			call := &ToolCall{
				Type:               webSearchToolName,
				Query:              anthropicToolQuery(block.Input),
				Status:             "pending",
				ProviderToolCallID: id,
				Raw:                capProviderRaw(block.Raw),
			}
			toolCallsByID[id] = call
			toolOrder = append(toolOrder, id)
		case "web_search_tool_result":
			id := block.ToolUseID
			call := toolCallsByID[id]
			if call == nil {
				call = &ToolCall{Type: webSearchToolName, Status: "completed", ProviderToolCallID: id, Raw: capProviderRaw(block.Raw)}
				toolCallsByID[id] = call
				toolOrder = append(toolOrder, id)
			}
			status, code, message, resultSources := anthropicToolResult(mapper, block.Content)
			call.Status = status
			if code != "" {
				call.ErrorCode = code
			}
			if message != "" {
				call.ErrorMessage = message
			}
			sources = append(sources, resultSources...)
		}
	}
	var toolCalls []ToolCall
	for _, id := range toolOrder {
		if call := toolCallsByID[id]; call != nil {
			toolCalls = append(toolCalls, *call)
		}
	}
	answerText := strings.Join(textParts, "")
	answer := Answer{
		Text:             answerText,
		Citations:        citations,
		SourcesConsulted: sources,
		ToolCalls:        toolCalls,
		ToolsAvailable:   anthropicToolsAvailable(req.Mode),
		ToolsUsed:        anthropicToolsUsed(toolCalls),
		StopReason:       parsed.StopReason,
		Unfinished:       anthropicUnfinished(parsed.StopReason),
		ProviderMetadata: anthropicMetadata(parsed, header, mapper.dropped),
	}
	if strings.TrimSpace(answer.Text) == "" && !anthropicEmptyTextTerminalAllowed(parsed.StopReason) {
		return Answer{}, malformedProviderResponse("anthropic", raw, "Anthropic sent a response without answer text")
	}
	if req.OutputSchema != nil {
		structured, ok := compactJSONBytes([]byte(answer.Text))
		if ok {
			answer.Parsed = structured
		} else if req.OutputSchema.Strict && !anthropicStrictTerminalException(parsed.StopReason) {
			return Answer{}, &ProviderError{SafeMessage: "Anthropic was asked for JSON, but the answer is not valid JSON", Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
		}
	}
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(raw, answer.ProviderMetadata)
	return answer, nil
}

func anthropicCitations(mapper *evidenceMapper, citations []anthropicCitation) []Citation {
	var out []Citation
	for _, citation := range citations {
		if citation.Type != "" && citation.Type != "web_search_result_location" && citation.Type != "url_citation" {
			continue
		}
		metadata := map[string]string{}
		if citation.CitedText != "" {
			metadata["cited_text"] = citation.CitedText
		}
		if mapped, ok := mapper.citation(citation.URL, citation.Title, metadata); ok {
			out = append(out, mapped)
		}
	}
	return out
}

func anthropicToolQuery(input map[string]any) string {
	if input == nil {
		return ""
	}
	if value, ok := input["query"].(string); ok {
		return value
	}
	return ""
}

func anthropicToolResult(mapper *evidenceMapper, raw json.RawMessage) (string, string, string, []Source) {
	if len(raw) == 0 || string(raw) == "null" {
		return "completed", "", "", nil
	}
	var entries []anthropicToolResultEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		var single anthropicToolResultEntry
		if err := json.Unmarshal(raw, &single); err != nil {
			return "completed", "", "", nil
		}
		entries = []anthropicToolResultEntry{single}
	}
	status := "completed"
	var code, message string
	var sources []Source
	for _, entry := range entries {
		switch entry.Type {
		case "web_search_tool_result_error":
			status = "error"
			code = firstNonEmpty(entry.ErrorCode, code)
			message = firstNonEmpty(entry.Message, message)
		case "web_search_result":
			metadata := map[string]string{}
			if entry.Content != "" {
				metadata["cited_text"] = entry.Content
			}
			if source, ok := mapper.source(entry.URL, entry.Title, "web", metadata); ok {
				sources = append(sources, source)
			}
		}
	}
	return status, code, message, sources
}

func anthropicToolsAvailable(mode ToolPolicy) []string {
	if mode == ToolPolicySearchAuto {
		return []string{webSearchToolName}
	}
	return nil
}

func anthropicToolsUsed(calls []ToolCall) []string {
	if len(calls) > 0 {
		return []string{webSearchToolName}
	}
	return nil
}

// anthropicUnfinished reports stop reasons that leave the answer incomplete.
// The answer hit the token limit or the context window, or the API paused a
// long web search and expected llmention to send the turn back to continue.
func anthropicUnfinished(stopReason string) bool {
	switch stopReason {
	case "pause_turn", "max_tokens", "model_context_window_exceeded":
		return true
	}
	return false
}

// anthropicEmptyTextTerminalAllowed reports stop reasons where an answer
// without text is expected, so it is not an error.
func anthropicEmptyTextTerminalAllowed(stopReason string) bool {
	switch stopReason {
	case "refusal", "pause_turn", "max_tokens", "model_context_window_exceeded":
		return true
	default:
		return false
	}
}

// anthropicStrictTerminalException reports stop reasons where an answer that
// should be JSON may not be JSON, because the model refused or stopped early.
func anthropicStrictTerminalException(stopReason string) bool {
	switch stopReason {
	case "refusal", "pause_turn", "max_tokens", "model_context_window_exceeded":
		return true
	default:
		return false
	}
}

func anthropicMetadata(parsed anthropicMessageResponse, header http.Header, dropped int) map[string]string {
	metadata := map[string]string{}
	setProviderMetadata(metadata, "response_id", parsed.ID)
	setProviderMetadata(metadata, "request_id", strings.TrimSpace(header.Get("request-id")))
	setProviderMetadata(metadata, "model", parsed.Model)
	setProviderMetadata(metadata, "status", parsed.StopReason)
	if parsed.Usage != nil {
		setProviderMetadataInt(metadata, "usage_input_tokens", parsed.Usage.InputTokens)
		setProviderMetadataInt(metadata, "usage_output_tokens", parsed.Usage.OutputTokens)
		setProviderMetadataInt(metadata, "usage_cached_tokens", parsed.Usage.CacheReadInputTokens)
		if parsed.Usage.ServerToolUse != nil {
			setProviderMetadataInt(metadata, "usage_search_queries", parsed.Usage.ServerToolUse.WebSearchRequests)
		}
	}
	setDroppedEvidenceMetadata(metadata, dropped)
	return metadata
}

func anthropicLocation(location *SearchLocation) map[string]string {
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

type anthropicErrorPayload struct {
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func anthropicHTTPError(provider string, resp reply, key string) *ProviderError {
	message, code, hint := "", "", ""
	var payload anthropicErrorPayload
	if json.Unmarshal(resp.body, &payload) == nil && payload.Error != nil {
		message, code = payload.Error.Message, payload.Error.Type
	}
	// The Messages API has one path, so "not found" means the model. Its
	// message is only "model: <id>".
	if code == "not_found_error" {
		message = "model not found (" + message + ")"
		hint = unknownModelHint
	}
	return httpError(provider, resp, key, message, hint, anthropicErrorClass(resp.status, code, string(resp.body)))
}

func anthropicErrorClass(status int, code, body string) ErrorClass {
	code = strings.ToLower(code)
	body = strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || code == "authentication_error":
		return ErrorClassConfig
	case status == http.StatusForbidden || code == "permission_error" || strings.Contains(body, "billing"):
		return ErrorClassConfig
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status == 529 || code == "timeout_error" || code == "conflict_error" || code == "rate_limit_error" || code == "overloaded_error":
		return ErrorClassTransient
	case status >= 500 || code == "api_error":
		return ErrorClassTransient
	case status >= 400:
		return ErrorClassCaller
	default:
		return ErrorClassProvider
	}
}

func capProviderRaw(raw []byte) []byte {
	if len(raw) > MaxRawPayloadBytes {
		raw = raw[:MaxRawPayloadBytes]
	}
	return append([]byte(nil), raw...)
}
