package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// compatibleEngine talks to any server with an OpenAI-style Chat Completions
// API, for example Ollama on your machine or a hosted gateway. These servers
// turn on web search in different ways, if at all, so only the modes without
// search work.
type compatibleEngine struct {
	baseEngine
	endpoint string
}

func newCompatibleEngine(base baseEngine) (Engine, error) {
	endpoint, err := chatCompletionsEndpoint(base.baseURL)
	if err != nil {
		return nil, fmt.Errorf("engine %q base_url is invalid: %w", base.id, err)
	}
	base.noRedirects = true
	return &compatibleEngine{baseEngine: base, endpoint: endpoint}, nil
}

func (e *compatibleEngine) ValidateRequest(req Request) error {
	if err := e.baseEngine.ValidateRequest(req); err != nil {
		return err
	}
	if req.Mode.IsSearchMode() {
		return fmt.Errorf("openai-compatible engines answer from memory only; llmention cannot turn on web search for them")
	}
	if req.SearchOptions != nil && !req.SearchOptions.IsZero() {
		return fmt.Errorf("openai-compatible engines do not take search options")
	}
	return nil
}

func (e *compatibleEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	key, err := e.prepare(ctx, req, e.ValidateRequest)
	if err != nil {
		return Answer{}, err
	}
	headers := map[string]string{}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	resp, err := e.post(ctx, e.endpoint, headers, e.chatRequest(req), key)
	if err != nil {
		return Answer{}, err
	}
	if !resp.ok() {
		return Answer{}, compatibleHTTPError(e.provider, resp, key, e.keyEnv != "")
	}
	answer, err := chatCompletionAnswerFromResponse(chatCompletionParseInput{
		Provider:       e.provider,
		Raw:            resp.body,
		Header:         resp.header,
		Request:        req,
		AllowCitations: e.caps.ExposesCitations,
		AllowToolCalls: e.caps.ExposesToolCalls,
	})
	return e.finish(answer, err, resp)
}

func (e *compatibleEngine) chatRequest(req Request) map[string]any {
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
	return body
}

// chatCompletionsEndpoint accepts a base URL such as http://localhost:11434/v1
// or a full URL that already ends in /chat/completions.
func chatCompletionsEndpoint(rawBase string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawBase))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("base_url must be a full http or https URL, such as http://localhost:11434/v1")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("base_url must not include a query string or fragment")
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/chat/completions") {
		u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	}
	u.RawPath = ""
	return u.String(), nil
}

func compatibleHTTPError(provider string, resp reply, key string, hadAuth bool) *ProviderError {
	class := statusClass(resp.status)
	switch {
	case resp.status >= 300 && resp.status < 400:
		class = ErrorClassProvider
	case !hadAuth && class == ErrorClassConfig:
		class = ErrorClassCaller
	}
	return httpError(provider, resp, key, embeddedErrorMessage(resp.body), class)
}

// embeddedErrorMessage finds the server's own explanation in an error body.
// Servers put it in different places, such as error.message or message.
func embeddedErrorMessage(body []byte) string {
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	var nested struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "" {
		return truncateMessage(nested.Message)
	}
	var plain string
	if json.Unmarshal(payload.Error, &plain) == nil && plain != "" {
		return truncateMessage(plain)
	}
	return truncateMessage(payload.Message)
}

func truncateMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = strings.ToValidUTF8(s[:300], "") + "…"
	}
	return s
}
