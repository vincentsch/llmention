package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// chatCompletionParseInput is what the shared Chat Completions parser needs.
// OpenRouter and openai-compatible servers both answer in this format, and
// the Allow fields say which parts of the answer the engine trusts.
type chatCompletionParseInput struct {
	Provider             string
	Raw                  []byte
	Header               http.Header
	Request              Request
	AllowCitations       bool
	AllowSources         bool
	AllowToolCalls       bool
	ResponseIDFromHeader string
}

type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Model   string                 `json:"model"`
	Created any                    `json:"created"`
	Object  string                 `json:"object"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage"`
	Cost    any                    `json:"cost"`
}

type chatCompletionChoice struct {
	Message            chatCompletionMessage `json:"message"`
	FinishReason       string                `json:"finish_reason"`
	NativeFinishReason string                `json:"native_finish_reason"`
	Error              *chatCompletionError  `json:"error"`
}

type chatCompletionMessage struct {
	Content     string                     `json:"content"`
	Annotations []chatCompletionAnnotation `json:"annotations"`
	ToolCalls   []chatCompletionToolCall   `json:"tool_calls"`
	Sources     []chatCompletionSource     `json:"sources"`
}

type chatCompletionAnnotation struct {
	Type        string                 `json:"type"`
	URL         string                 `json:"url"`
	Title       string                 `json:"title"`
	URLCitation *chatCompletionURLCite `json:"url_citation"`
}

type chatCompletionURLCite struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type chatCompletionToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function *chatCompletionFunction `json:"function"`
}

type chatCompletionFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionSource struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type chatCompletionUsage struct {
	PromptTokens     int                          `json:"prompt_tokens"`
	CompletionTokens int                          `json:"completion_tokens"`
	TotalTokens      int                          `json:"total_tokens"`
	ServerToolUse    *chatCompletionServerToolUse `json:"server_tool_use"`
	// OpenRouter reports the searches of a provider's own search tool here,
	// as seen live on 2026-11-08.
	ServerToolUseDetails *chatCompletionServerToolUse `json:"server_tool_use_details"`
	Cost                 any                          `json:"cost"`
}

// webSearches is the number of searches the usage reports, or -1 when it
// reports none.
func (u *chatCompletionUsage) webSearches() int {
	if u == nil {
		return -1
	}
	n := -1
	for _, use := range []*chatCompletionServerToolUse{u.ServerToolUse, u.ServerToolUseDetails} {
		if use != nil && use.WebSearchRequests > n {
			n = use.WebSearchRequests
		}
	}
	return n
}

type chatCompletionServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
}

type chatCompletionError struct {
	Code    any    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func chatCompletionAnswerFromResponse(input chatCompletionParseInput) (Answer, error) {
	decoder := json.NewDecoder(bytes.NewReader(input.Raw))
	decoder.UseNumber()
	var parsed chatCompletionResponse
	if err := decoder.Decode(&parsed); err != nil || !json.Valid(input.Raw) {
		return Answer{}, malformedProviderResponse(input.Provider, input.Raw, providerDisplayName(input.Provider)+" sent a response llmention could not read")
	}
	if len(parsed.Choices) == 0 {
		return Answer{}, malformedProviderResponse(input.Provider, input.Raw, providerDisplayName(input.Provider)+" sent a response without an answer")
	}
	choice := parsed.Choices[0]
	if choice.Error != nil {
		return Answer{}, &ProviderError{
			StatusCode:  http.StatusOK,
			SafeMessage: chatCompletionEmbeddedErrorMessage(input.Provider, choice.Error),
			Class:       ErrorClassProvider,
			SafeSnippet: rawSnippet(input.Raw),
		}
	}
	// finish_reason "length" means the answer hit the token limit.
	unfinished := choice.FinishReason == "length"
	if choice.Message.Content == "" && !unfinished {
		return Answer{}, malformedProviderResponse(input.Provider, input.Raw, providerDisplayName(input.Provider)+" sent a response without answer text")
	}
	mapper := &evidenceMapper{}
	citations := chatCompletionCitations(mapper, choice.Message.Annotations, input.AllowCitations)
	sources := chatCompletionSources(mapper, choice.Message.Sources, input.AllowSources)
	toolCalls := chatCompletionToolCalls(choice.Message.ToolCalls, input.AllowToolCalls)
	metadata := chatCompletionMetadata(input, parsed, choice, mapper.dropped)
	answer := Answer{
		Text:             choice.Message.Content,
		Citations:        citations,
		SourcesConsulted: sources,
		ToolCalls:        toolCalls,
		ToolsAvailable:   chatCompletionToolsAvailable(input.Provider, input.Request.Mode),
		ToolsUsed:        chatCompletionToolsUsed(input.Provider, parsed.Usage, len(citations)),
		StopReason:       choice.FinishReason,
		Unfinished:       unfinished,
		ProviderMetadata: metadata,
	}
	if input.Request.OutputSchema != nil && !unfinished {
		structured, ok := compactJSONBytes([]byte(answer.Text))
		if ok {
			answer.Parsed = structured
		} else if input.Request.OutputSchema.Strict {
			return Answer{}, &ProviderError{SafeMessage: providerDisplayName(input.Provider) + " was asked for JSON, but the answer is not valid JSON", Class: ErrorClassProvider, SafeSnippet: rawSnippet(input.Raw)}
		}
	}
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(input.Raw, answer.ProviderMetadata)
	return answer, nil
}

func chatCompletionEmbeddedErrorMessage(provider string, err *chatCompletionError) string {
	message := firstNonEmpty(err.Message, providerDisplayName(provider)+" reported an error")
	code := firstNonEmpty(err.Type, fmt.Sprint(err.Code))
	if code != "" && code != "<nil>" {
		message += " (" + code + ")"
	}
	return message
}

func chatCompletionCitations(mapper *evidenceMapper, annotations []chatCompletionAnnotation, enabled bool) []Citation {
	if !enabled {
		return nil
	}
	var out []Citation
	for _, annotation := range annotations {
		rawURL := annotation.URL
		title := annotation.Title
		metadata := map[string]string{}
		if annotation.URLCitation != nil {
			rawURL = firstNonEmpty(annotation.URLCitation.URL, rawURL)
			title = firstNonEmpty(annotation.URLCitation.Title, title)
			if annotation.URLCitation.Content != "" {
				metadata["cited_text"] = annotation.URLCitation.Content
			}
		}
		if annotation.Type != "" && annotation.Type != "url_citation" {
			continue
		}
		if citation, ok := mapper.citation(rawURL, title, metadata); ok {
			out = append(out, citation)
		}
	}
	return out
}

func chatCompletionSources(mapper *evidenceMapper, values []chatCompletionSource, enabled bool) []Source {
	if !enabled {
		return nil
	}
	var out []Source
	for _, value := range values {
		kind := firstNonEmpty(value.Type, "web")
		if source, ok := mapper.source(value.URL, value.Title, kind, nil); ok {
			out = append(out, source)
		}
	}
	return out
}

func chatCompletionToolCalls(values []chatCompletionToolCall, enabled bool) []ToolCall {
	if !enabled {
		return nil
	}
	var out []ToolCall
	for _, value := range values {
		call := ToolCall{
			Type:               value.Type,
			Status:             "completed",
			ProviderToolCallID: value.ID,
		}
		if value.Function != nil {
			call.Query = value.Function.Arguments
			if value.Function.Name != "" {
				call.Type = value.Function.Name
			}
		}
		out = append(out, call)
	}
	return out
}

func chatCompletionToolsAvailable(provider string, mode ToolPolicy) []string {
	if provider == "openrouter" && mode == ToolPolicySearchAuto {
		return []string{webSearchToolName}
	}
	return nil
}

// chatCompletionToolsUsed says whether an OpenRouter answer searched the web.
// OpenRouter counts its own searches in usage. Native searches, like
// Gemini's Google Search, leave no count there, so cited pages count too.
func chatCompletionToolsUsed(provider string, usage *chatCompletionUsage, citations int) []string {
	if provider != "openrouter" {
		return nil
	}
	counted := usage.webSearches() > 0
	if !counted && citations == 0 {
		return nil
	}
	return []string{webSearchToolName}
}

func chatCompletionMetadata(input chatCompletionParseInput, parsed chatCompletionResponse, choice chatCompletionChoice, dropped int) map[string]string {
	metadata := map[string]string{}
	responseID := ""
	if input.ResponseIDFromHeader != "" {
		responseID = strings.TrimSpace(input.Header.Get(input.ResponseIDFromHeader))
	}
	setProviderMetadata(metadata, "response_id", firstNonEmpty(responseID, parsed.ID))
	setProviderMetadata(metadata, "model", parsed.Model)
	setProviderMetadataAny(metadata, "created", parsed.Created)
	setProviderMetadata(metadata, "object", parsed.Object)
	setProviderMetadata(metadata, "native_finish_reason", choice.NativeFinishReason)
	if parsed.Usage != nil {
		setProviderMetadataInt(metadata, "usage_input_tokens", parsed.Usage.PromptTokens)
		setProviderMetadataInt(metadata, "usage_output_tokens", parsed.Usage.CompletionTokens)
		setProviderMetadataInt(metadata, "usage_total_tokens", parsed.Usage.TotalTokens)
		if n := parsed.Usage.webSearches(); n >= 0 {
			setProviderMetadataInt(metadata, "usage_search_queries", n)
		}
		setProviderMetadataAny(metadata, "usage_cost", firstNonNil(parsed.Usage.Cost, parsed.Cost))
	} else {
		setProviderMetadataAny(metadata, "usage_cost", parsed.Cost)
	}
	setDroppedEvidenceMetadata(metadata, dropped)
	return metadata
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
