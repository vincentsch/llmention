package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/vincentsch/llmention/internal/domain"
)

const (
	defaultGoogleBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	googleSearchTool     = "google_search"
)

// googleEngine asks Gemini through the Interactions API, with Google Search in
// the search modes.
type googleEngine struct {
	baseEngine
}

func newGoogleEngine(base baseEngine) Engine {
	return &googleEngine{baseEngine: base}
}

func (e *googleEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return err
	}
	if opts == nil {
		return nil
	}
	if len(opts.AllowedDomains) > 0 || len(opts.BlockedDomains) > 0 {
		return fmt.Errorf("search on Google does not support domain filters")
	}
	if opts.Locale != "" || opts.UserLocation != nil {
		return fmt.Errorf("search on Google does not support locale or user location controls")
	}
	if opts.RecencyDays > 0 || opts.DateFrom != "" || opts.DateTo != "" {
		return fmt.Errorf("search on Google does not support recency or date filters")
	}
	if opts.MaxSearches > 0 {
		return fmt.Errorf("search on Google does not support max search query controls")
	}
	if opts.CacheOnly || opts.AllowLiveAccess || opts.IncludeSourceMetadata {
		return fmt.Errorf("search on Google does not support cache, live-access, or source-metadata controls")
	}
	return nil
}

func (e *googleEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	endpoint, err := providerEndpoint(e.baseURL, defaultGoogleBaseURL, "interactions")
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassConfig}
	}
	body, err := e.interactionsRequest(req)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	resp, err := e.post(ctx, endpoint, map[string]string{"x-goog-api-key": key}, body, key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, googleHTTPError(e.provider, resp, key)
	}
	answer, err := googleAnswerFromResponse(resp.body, req)
	return e.finish(answer, err, resp)
}

func (e *googleEngine) interactionsRequest(req Request) (map[string]any, error) {
	body := map[string]any{
		"model": e.model,
		"input": req.Prompt,
		"store": false,
	}
	generation := map[string]any{}
	if req.MaxOutputTokens > 0 {
		generation["max_output_tokens"] = req.MaxOutputTokens
	}
	if req.OutputSchema != nil && req.OutputSchema.Strict {
		body["response_format"] = map[string]any{
			"type":      "text",
			"mime_type": "application/json",
			"schema":    json.RawMessage(req.OutputSchema.SchemaJSON),
		}
	}
	switch req.Mode {
	case ToolPolicySearchAuto:
		body["tools"] = []map[string]any{{"type": googleSearchTool}}
	case ToolPolicySearchRequired:
		// tool_choice goes in generation_config. With Google Search as the
		// only tool, "any" makes the model search before it answers.
		body["tools"] = []map[string]any{{"type": googleSearchTool}}
		generation["tool_choice"] = "any"
	}
	if len(generation) > 0 {
		body["generation_config"] = generation
	}
	return body, nil
}

type googleInteractionResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Status  string             `json:"status"`
	Created any                `json:"created"`
	Object  string             `json:"object"`
	Usage   *googleUsage       `json:"usage"`
	Errors  []googleBodyError  `json:"errors"`
	Steps   []googleOutputStep `json:"steps"`
}

type googleUsage struct {
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
	TotalTokens       int `json:"total_tokens"`
	TotalCachedTokens int `json:"total_cached_tokens"`
}

type googleBodyError struct {
	Message string `json:"message"`
	Status  string `json:"status"`
	Code    any    `json:"code"`
}

type googleOutputStep struct {
	Type      string           `json:"type"`
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	Arguments *googleArguments `json:"arguments"`
	Query     string           `json:"query"`
	Queries   []string         `json:"queries"`
	URL       string           `json:"url"`
	Title     string           `json:"title"`
	Result    json.RawMessage  `json:"result"` // a list of results for google_search_result

	Content     []googleContentBlock `json:"content"`
	Results     []googleSearchResult `json:"results"`
	Search      []googleSearchResult `json:"search_results"`
	Annotations []googleAnnotation   `json:"annotations"`
	Text        string               `json:"text"`
}

type googleArguments struct {
	Query   string   `json:"query"`
	Queries []string `json:"queries"`
}

type googleContentBlock struct {
	Type        string             `json:"type"`
	Text        string             `json:"text"`
	Annotations []googleAnnotation `json:"annotations"`
}

type googleAnnotation struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	StartIndex *int   `json:"start_index"`
	EndIndex   *int   `json:"end_index"`
}

type googleSearchResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Query string `json:"query"`
}

func googleAnswerFromResponse(raw []byte, req Request) (Answer, error) {
	var parsed googleInteractionResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Answer{}, malformedProviderResponse("google", raw, "Google sent a response llmention could not read")
	}
	if len(parsed.Errors) > 0 {
		return Answer{}, &ProviderError{SafeMessage: googleEmbeddedErrorMessage(parsed.Errors[0]), Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
	}
	// "incomplete" still carries the text so far; it is saved as unfinished.
	switch parsed.Status {
	case "", "completed", "incomplete":
	default:
		return Answer{}, &ProviderError{SafeMessage: fmt.Sprintf("Google returned status %q instead of a finished answer", parsed.Status), Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
	}

	mapper := &evidenceMapper{}
	var textParts []string
	var citations []Citation
	var sources []Source
	var toolCalls []ToolCall
	for _, step := range parsed.Steps {
		switch step.Type {
		case "google_search_call":
			toolCalls = append(toolCalls, ToolCall{
				Type:               webSearchToolName,
				Query:              googleStepQuery(step),
				Status:             firstNonEmpty(step.Status, "completed"),
				ProviderToolCallID: step.ID,
			})
			continue
		case "google_search_result", "search_result":
			googleAppendStepSources(mapper, &sources, step)
			continue
		case "thought", "thinking", "tool", "tool_call":
			continue
		}
		if step.Text != "" {
			textParts = append(textParts, step.Text)
		}
		for _, block := range step.Content {
			if block.Type != "" && block.Type != "text" && block.Type != "output_text" {
				continue
			}
			textParts = append(textParts, block.Text)
			citations = append(citations, googleCitations(mapper, block.Annotations)...)
		}
		citations = append(citations, googleCitations(mapper, step.Annotations)...)
		googleAppendStepSources(mapper, &sources, step)
	}
	answerText := strings.Join(textParts, "")
	incomplete := parsed.Status == "incomplete"
	if strings.TrimSpace(answerText) == "" && !incomplete {
		return Answer{}, malformedProviderResponse("google", raw, "Google sent a response without answer text")
	}
	answer := Answer{
		Text:             answerText,
		Citations:        citations,
		SourcesConsulted: sources,
		ToolCalls:        toolCalls,
		ToolsAvailable:   googleToolsAvailable(req.Mode),
		ToolsUsed:        googleToolsUsed(toolCalls, sources),
		StopReason:       parsed.Status,
		Unfinished:       incomplete,
		ProviderMetadata: googleMetadata(parsed, mapper.dropped),
	}
	if req.Mode == ToolPolicySearchRequired && len(answer.ToolCalls) == 0 && len(answer.SourcesConsulted) == 0 {
		return Answer{}, &ProviderError{SafeMessage: "Google answered without searching the web, although forced web search was on", Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
	}
	if req.OutputSchema != nil && !incomplete {
		structured, ok := compactJSONBytes([]byte(answer.Text))
		if ok {
			answer.Parsed = structured
		} else if req.OutputSchema.Strict {
			return Answer{}, &ProviderError{SafeMessage: "Google was asked for JSON, but the answer is not valid JSON", Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
		}
	}
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(raw, answer.ProviderMetadata)
	return answer, nil
}

func googleCitations(mapper *evidenceMapper, annotations []googleAnnotation) []Citation {
	var out []Citation
	for _, annotation := range annotations {
		if annotation.Type != "" && annotation.Type != "url_citation" {
			continue
		}
		metadata := map[string]string{}
		if annotation.Text != "" {
			metadata["cited_text"] = annotation.Text
		}
		if annotation.StartIndex != nil {
			metadata["start_index"] = strconv.Itoa(*annotation.StartIndex)
		}
		if annotation.EndIndex != nil {
			metadata["end_index"] = strconv.Itoa(*annotation.EndIndex)
		}
		if citation, ok := mapper.citation(annotation.URL, annotation.Title, metadata); ok {
			citation.Domain = googleSiteDomain(citation.Domain, annotation.Title)
			out = append(out, citation)
		}
	}
	return out
}

func googleAppendStepSources(mapper *evidenceMapper, sources *[]Source, step googleOutputStep) {
	results := slices.Concat(step.Results, step.Search)
	if step.URL != "" {
		results = append(results, googleSearchResult{URL: step.URL, Title: step.Title})
	}
	// A google_search_result step holds a list. Each entry has a url and a
	// title, or only search_suggestions, the HTML widget Google asks apps to
	// show. Entries without a url are left out.
	var listed []googleSearchResult
	if json.Unmarshal(step.Result, &listed) == nil {
		results = append(results, listed...)
	}
	for _, result := range results {
		if result.URL == "" {
			continue
		}
		if source, ok := mapper.source(result.URL, result.Title, "web", nil); ok {
			source.Domain = googleSiteDomain(source.Domain, result.Title)
			*sources = append(*sources, source)
		}
	}
}

// googleSiteDomain names the cited site. Answers that searched the web can
// link through a Google redirect
// (vertexaisearch.cloud.google.com/grounding-api-redirect/...) and put the
// site's domain in the title. Without this, all those citations would count
// for one Google site.
func googleSiteDomain(observed, title string) string {
	if observed != "vertexaisearch.cloud.google.com" {
		return observed
	}
	if site, err := domain.NormalizeObservedHost(strings.TrimSpace(title)); err == nil && strings.Contains(site, ".") && !strings.ContainsAny(title, " /") {
		return site
	}
	return observed
}

func googleStepQuery(step googleOutputStep) string {
	if step.Arguments != nil {
		if len(step.Arguments.Queries) > 0 {
			if queries := nonEmptyStrings(step.Arguments.Queries); len(queries) > 0 {
				return strings.Join(queries, "\n")
			}
		}
		if step.Arguments.Query != "" {
			return step.Arguments.Query
		}
	}
	if len(step.Queries) > 0 {
		if queries := nonEmptyStrings(step.Queries); len(queries) > 0 {
			return strings.Join(queries, "\n")
		}
	}
	return step.Query
}

func googleToolsAvailable(mode ToolPolicy) []string {
	if mode == ToolPolicySearchAuto || mode == ToolPolicySearchRequired {
		return []string{webSearchToolName}
	}
	return nil
}

func googleToolsUsed(calls []ToolCall, sources []Source) []string {
	if len(calls) > 0 || len(sources) > 0 {
		return []string{webSearchToolName}
	}
	return nil
}

func googleMetadata(parsed googleInteractionResponse, dropped int) map[string]string {
	metadata := map[string]string{}
	setProviderMetadata(metadata, "response_id", parsed.ID)
	setProviderMetadata(metadata, "model", parsed.Model)
	setProviderMetadata(metadata, "status", parsed.Status)
	setProviderMetadataAny(metadata, "created", parsed.Created)
	setProviderMetadata(metadata, "object", parsed.Object)
	if parsed.Usage != nil {
		setProviderMetadataInt(metadata, "usage_input_tokens", parsed.Usage.TotalInputTokens)
		setProviderMetadataInt(metadata, "usage_output_tokens", parsed.Usage.TotalOutputTokens)
		setProviderMetadataInt(metadata, "usage_total_tokens", parsed.Usage.TotalTokens)
		setProviderMetadataInt(metadata, "usage_cached_tokens", parsed.Usage.TotalCachedTokens)
	}
	setDroppedEvidenceMetadata(metadata, dropped)
	return metadata
}

func googleEmbeddedErrorMessage(err googleBodyError) string {
	message := firstNonEmpty(err.Message, err.Status, "Google reported an error")
	if err.Code != nil {
		message += " (" + fmt.Sprint(err.Code) + ")"
	}
	return message
}

type googleErrorPayload struct {
	Error *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func googleHTTPError(provider string, resp reply, key string) *ProviderError {
	message, code, hint := "", "", ""
	var payload googleErrorPayload
	if json.Unmarshal(resp.body, &payload) != nil || payload.Error == nil {
		// The Interactions API sends the error inside a list.
		var list []googleErrorPayload
		if json.Unmarshal(resp.body, &list) == nil && len(list) > 0 {
			payload = list[0]
		}
	}
	if payload.Error != nil {
		message, code = payload.Error.Message, payload.Error.Status
		if code == "" && payload.Error.Code != nil {
			code = fmt.Sprint(payload.Error.Code)
		}
	}
	if code == "NOT_FOUND" {
		hint = unknownModelHint
	}
	return httpError(provider, resp, key, message, hint, googleErrorClass(resp.status, code, string(resp.body)))
}

func googleErrorClass(status int, code, body string) ErrorClass {
	code = strings.ToUpper(code)
	lowerBody := strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || code == "UNAUTHENTICATED" || strings.Contains(body, "API_KEY_INVALID"):
		return ErrorClassConfig
	case status == http.StatusForbidden || code == "PERMISSION_DENIED" || code == "SERVICE_DISABLED" || code == "BILLING_NOT_ENABLED" || strings.Contains(lowerBody, "billing"):
		return ErrorClassConfig
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || code == "DEADLINE_EXCEEDED" || code == "ABORTED" || code == "RESOURCE_EXHAUSTED":
		return ErrorClassTransient
	case status >= 500 || code == "INTERNAL" || code == "UNAVAILABLE" || strings.Contains(lowerBody, "overload") || strings.Contains(lowerBody, "timeout"):
		return ErrorClassTransient
	case status >= 400:
		return ErrorClassCaller
	default:
		return ErrorClassProvider
	}
}
