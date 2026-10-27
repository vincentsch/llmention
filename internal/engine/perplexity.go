package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultPerplexityBaseURL = "https://api.perplexity.ai/v1"

// perplexityEngine asks Perplexity's Sonar API at /v1/sonar. Perplexity is
// moving synchronous Sonar requests over to its newer Agent API, model by
// model. llmention stays on Sonar until the Agent API has been checked with
// real requests, because its answers and prices may differ.
type perplexityEngine struct {
	baseEngine
}

func newPerplexityEngine(base baseEngine) Engine {
	return &perplexityEngine{baseEngine: base}
}

func (e *perplexityEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	if req.MaxOutputTokens > 128000 {
		return fmt.Errorf("max_output_tokens for Perplexity Sonar must be 128000 or less")
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return err
	}
	if opts == nil {
		return nil
	}
	if opts.MaxSearches > 0 {
		return fmt.Errorf("sonar does not support max search query controls")
	}
	if len(opts.AllowedDomains) > 0 && len(opts.BlockedDomains) > 0 {
		return fmt.Errorf("sonar accepts allowed_domains or blocked_domains, not both")
	}
	if len(opts.AllowedDomains)+len(opts.BlockedDomains) > 20 {
		return fmt.Errorf("sonar domain filters allow at most 20 entries")
	}
	if hasWildcardSearchDomain(opts.AllowedDomains) || hasWildcardSearchDomain(opts.BlockedDomains) {
		return fmt.Errorf("sonar does not support wildcard domains")
	}
	if opts.Locale != "" {
		if _, err := perplexityLanguageFilter(opts.Locale); err != nil {
			return err
		}
	}
	if opts.RecencyDays > 0 && (opts.DateFrom != "" || opts.DateTo != "") {
		return fmt.Errorf("sonar does not allow recency with explicit date filters")
	}
	if opts.RecencyDays > 0 {
		if _, err := perplexityRecency(opts.RecencyDays); err != nil {
			return err
		}
	}
	if opts.UserLocation != nil && opts.UserLocation.Timezone != "" {
		return fmt.Errorf("sonar user location does not support timezone")
	}
	return nil
}

func (e *perplexityEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	endpoint, err := providerEndpoint(e.baseURL, defaultPerplexityBaseURL, "sonar")
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassConfig}
	}
	body, err := e.sonarRequest(req)
	if err != nil {
		return Answer{}, &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	resp, err := e.post(ctx, endpoint, map[string]string{"Authorization": "Bearer " + key}, body, key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, perplexityHTTPError(e.provider, resp, key)
	}
	answer, err := perplexityAnswerFromResponse(resp.body, req)
	return e.finish(answer, err, resp)
}

func (e *perplexityEngine) sonarRequest(req Request) (map[string]any, error) {
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
				"schema": json.RawMessage(req.OutputSchema.SchemaJSON),
			},
		}
	}
	opts, err := NormalizeSearchOptions(req.SearchOptions)
	if err != nil {
		return nil, err
	}
	switch req.Mode {
	case ToolPolicyMemory:
		body["disable_search"] = true
	case ToolPolicySearchAuto:
		body["enable_search_classifier"] = true
		body["disable_search"] = false
	case ToolPolicySearchRequired:
		body["disable_search"] = false
	}
	if opts == nil {
		return body, nil
	}
	if len(opts.AllowedDomains) > 0 {
		body["search_domain_filter"] = opts.AllowedDomains
	}
	if len(opts.BlockedDomains) > 0 {
		filter := make([]string, 0, len(opts.BlockedDomains))
		for _, blocked := range opts.BlockedDomains {
			filter = append(filter, "-"+blocked)
		}
		body["search_domain_filter"] = filter
	}
	if opts.Locale != "" {
		language, err := perplexityLanguageFilter(opts.Locale)
		if err != nil {
			return nil, err
		}
		body["search_language_filter"] = []string{language}
	}
	if opts.RecencyDays > 0 {
		value, err := perplexityRecency(opts.RecencyDays)
		if err != nil {
			return nil, err
		}
		body["search_recency_filter"] = value
	}
	if opts.DateFrom != "" {
		body["search_after_date_filter"] = perplexityDate(opts.DateFrom)
	}
	if opts.DateTo != "" {
		body["search_before_date_filter"] = perplexityDate(opts.DateTo)
	}
	if opts.UserLocation != nil && !opts.UserLocation.IsZero() {
		body["web_search_options"] = map[string]any{
			"user_location": perplexityUserLocation(opts.UserLocation),
		}
	}
	return body, nil
}

type perplexityResponse struct {
	ID            string                    `json:"id"`
	Model         string                    `json:"model"`
	Created       any                       `json:"created"`
	Object        string                    `json:"object"`
	Choices       []perplexityChoice        `json:"choices"`
	Citations     []perplexityCitationValue `json:"citations"`
	SearchResults []perplexitySearchResult  `json:"search_results"`
	Usage         *perplexityUsage          `json:"usage"`
}

type perplexityChoice struct {
	Message      perplexityMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type perplexityMessage struct {
	Content string `json:"content"`
}

type perplexityCitationValue struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func (v *perplexityCitationValue) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		v.URL = text
		return nil
	}
	var object struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	v.URL = object.URL
	v.Title = object.Title
	return nil
}

type perplexitySearchResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Date  string `json:"date"`
}

type perplexityUsage struct {
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	NumSearchQueries int             `json:"num_search_queries"`
	Cost             *perplexityCost `json:"cost"`
}

type perplexityCost struct {
	TotalCost json.Number `json:"total_cost"`
}

func perplexityAnswerFromResponse(raw []byte, req Request) (Answer, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed perplexityResponse
	if err := decoder.Decode(&parsed); err != nil || !json.Valid(raw) {
		return Answer{}, malformedProviderResponse("perplexity", raw, "Perplexity sent a response llmention could not read")
	}
	if len(parsed.Choices) == 0 {
		return Answer{}, malformedProviderResponse("perplexity", raw, "Perplexity sent a response without an answer")
	}
	text := parsed.Choices[0].Message.Content
	unfinished := parsed.Choices[0].FinishReason == "length"
	if text == "" && !unfinished {
		return Answer{}, malformedProviderResponse("perplexity", raw, "Perplexity sent a response without answer text")
	}
	// The numbered references in a Sonar answer, [1] and so on, point into
	// search_results, so those are the pages it cites. Perplexity dropped the
	// older citations list of plain URLs. When a response still has one, its
	// extra URLs also count as citations.
	mapper := &evidenceMapper{}
	var citations []Citation
	var sources []Source
	cited := map[string]bool{}
	for _, result := range parsed.SearchResults {
		metadata := map[string]string{}
		if result.Date != "" {
			metadata["date"] = result.Date
		}
		if source, ok := mapper.source(result.URL, result.Title, "web", metadata); ok {
			sources = append(sources, source)
			citations = append(citations, Citation{URL: source.URL, Title: source.Title, Domain: source.Domain, Metadata: source.Metadata})
			cited[source.URL] = true
		}
	}
	for _, citation := range parsed.Citations {
		if mapped, ok := mapper.citation(citation.URL, citation.Title, nil); ok && !cited[mapped.URL] {
			citations = append(citations, mapped)
			cited[mapped.URL] = true
		}
	}
	metadata := perplexityMetadata(parsed, mapper.dropped)
	answer := Answer{
		Text:             text,
		Citations:        citations,
		SourcesConsulted: sources,
		ToolsAvailable:   perplexityToolsAvailable(req.Mode),
		ToolsUsed:        perplexityToolsUsed(parsed.Usage, sources),
		StopReason:       parsed.Choices[0].FinishReason,
		Unfinished:       unfinished,
		ProviderMetadata: metadata,
	}
	if req.OutputSchema != nil && !unfinished {
		structured, ok := compactJSONBytes([]byte(answer.Text))
		if ok {
			answer.Parsed = structured
		} else if req.OutputSchema.Strict {
			return Answer{}, &ProviderError{SafeMessage: "Perplexity was asked for JSON, but the answer is not valid JSON", Class: ErrorClassProvider, SafeSnippet: rawSnippet(raw)}
		}
	}
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(raw, answer.ProviderMetadata)
	return answer, nil
}

func perplexityToolsAvailable(mode ToolPolicy) []string {
	if mode == ToolPolicyMemory {
		return nil
	}
	return []string{webSearchToolName}
}

func perplexityToolsUsed(usage *perplexityUsage, sources []Source) []string {
	if len(sources) > 0 || (usage != nil && usage.NumSearchQueries > 0) {
		return []string{webSearchToolName}
	}
	return nil
}

func perplexityMetadata(parsed perplexityResponse, dropped int) map[string]string {
	metadata := map[string]string{}
	setProviderMetadata(metadata, "response_id", parsed.ID)
	setProviderMetadata(metadata, "model", parsed.Model)
	setProviderMetadataAny(metadata, "created", parsed.Created)
	setProviderMetadata(metadata, "object", parsed.Object)
	if parsed.Usage != nil {
		setProviderMetadataInt(metadata, "usage_input_tokens", parsed.Usage.PromptTokens)
		setProviderMetadataInt(metadata, "usage_output_tokens", parsed.Usage.CompletionTokens)
		setProviderMetadataInt(metadata, "usage_total_tokens", parsed.Usage.TotalTokens)
		setProviderMetadataInt(metadata, "usage_search_queries", parsed.Usage.NumSearchQueries)
		if parsed.Usage.Cost != nil {
			setProviderMetadataNumber(metadata, "usage_cost", parsed.Usage.Cost.TotalCost)
		}
	}
	setDroppedEvidenceMetadata(metadata, dropped)
	return metadata
}

func perplexityLanguageFilter(locale string) (string, error) {
	primary := strings.ToLower(strings.Split(locale, "-")[0])
	if len(primary) != 2 {
		return "", fmt.Errorf("sonar locale %q must start with a two-letter ISO-639-1 language subtag", locale)
	}
	for _, r := range primary {
		if r < 'a' || r > 'z' {
			return "", fmt.Errorf("sonar locale %q must start with a two-letter ISO-639-1 language subtag", locale)
		}
	}
	return primary, nil
}

func perplexityRecency(days int) (string, error) {
	switch days {
	case 1:
		return "day", nil
	case 7:
		return "week", nil
	case 30:
		return "month", nil
	case 365:
		return "year", nil
	default:
		return "", fmt.Errorf("sonar recency supports only 1, 7, 30, or 365 days")
	}
}

func perplexityDate(value string) string {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return value
	}
	return parsed.Format("01/02/2006")
}

func perplexityUserLocation(location *SearchLocation) map[string]string {
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
	return out
}

type perplexityErrorPayload struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func perplexityHTTPError(provider string, resp reply, key string) *ProviderError {
	message, code := "", ""
	var payload perplexityErrorPayload
	if json.Unmarshal(resp.body, &payload) == nil && payload.Error != nil {
		message, code = payload.Error.Message, firstNonEmpty(payload.Error.Type, fmt.Sprint(payload.Error.Code))
	}
	return httpError(provider, resp, key, message, perplexityErrorClass(resp.status, code, string(resp.body)))
}

func perplexityErrorClass(status int, code, body string) ErrorClass {
	code = strings.ToLower(code)
	body = strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || strings.Contains(code, "authentication") || code == "invalid_api_key":
		return ErrorClassConfig
	case status == http.StatusPaymentRequired || code == "payment_required" || code == "insufficient_quota" || strings.Contains(body, "billing"):
		return ErrorClassConfig
	case status == http.StatusForbidden || strings.Contains(code, "permission") || code == "forbidden":
		return ErrorClassConfig
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || strings.Contains(code, "timeout") || strings.Contains(code, "rate_limit"):
		return ErrorClassTransient
	case status >= 500 || code == "api_error" || strings.Contains(body, "overload") || strings.Contains(body, "unavailable") || strings.Contains(body, "timeout"):
		return ErrorClassTransient
	case status >= 400:
		return ErrorClassCaller
	default:
		return ErrorClassProvider
	}
}
