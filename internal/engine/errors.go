package engine

import (
	"fmt"
	"strings"
	"time"
)

// Limits on what llmention saves from a provider's response: the raw answer,
// and the part of the body it saves with an error.
const (
	MaxRawPayloadBytes           = 1 << 20
	MaxProviderErrorSnippetBytes = 4 << 10
)

// ErrorClass says what kind of problem a provider error is. The runner
// retries transient errors, such as a rate limit or a timeout.
type ErrorClass string

const (
	ErrorClassConfig    ErrorClass = "config"
	ErrorClassCaller    ErrorClass = "caller"
	ErrorClassProvider  ErrorClass = "provider"
	ErrorClassTransient ErrorClass = "transient"
)

func (c ErrorClass) Valid() bool {
	switch c {
	case ErrorClassConfig, ErrorClassCaller, ErrorClassProvider, ErrorClassTransient:
		return true
	default:
		return false
	}
}

// ProviderError is a failed request. SafeMessage and SafeSnippet never hold
// the API key, so they can be printed and saved with the run.
type ProviderError struct {
	ProviderID  string         `json:"provider_id,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	StatusCode  int            `json:"status_code,omitempty"`
	SafeMessage string         `json:"safe_message"`
	Class       ErrorClass     `json:"class"`
	Retryable   bool           `json:"retryable"`
	RetryAfter  *time.Duration `json:"retry_after,omitempty"`
	SafeSnippet string         `json:"safe_snippet,omitempty"`
}

// Error reads like "OpenAI answered with status 429: rate limit reached
// (request req_123)". A message that does not name the provider gets the
// provider id in front, as in "openai: could not reach api.openai.com".
func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(e.SafeMessage)
	if message == "" {
		message = "provider error"
		if e.StatusCode != 0 {
			message = fmt.Sprintf("status %d", e.StatusCode)
		}
	}
	if e.ProviderID != "" && !strings.HasPrefix(message, providerDisplayName(e.ProviderID)+" ") {
		message = e.ProviderID + ": " + message
	}
	if e.RequestID != "" {
		message += " (request " + e.RequestID + ")"
	}
	return message
}

func NewProviderError(providerID string, class ErrorClass, safeMessage string) *ProviderError {
	if !class.Valid() {
		class = ErrorClassProvider
	}
	return &ProviderError{
		ProviderID:  providerID,
		SafeMessage: safeMessage,
		Class:       class,
	}
}

func SafeResponseSnippet(data []byte) string {
	if len(data) > MaxProviderErrorSnippetBytes {
		data = data[:MaxProviderErrorSnippetBytes]
	}
	return strings.ToValidUTF8(string(data), "?")
}

func CaptureRawPayload(raw []byte, metadata map[string]string) ([]byte, map[string]string) {
	if len(raw) == 0 {
		return nil, cloneStringMap(metadata)
	}
	out := append([]byte(nil), raw...)
	meta := cloneStringMap(metadata)
	if len(out) > MaxRawPayloadBytes {
		out = out[:MaxRawPayloadBytes]
		if meta == nil {
			meta = map[string]string{}
		}
		meta["raw_truncated"] = "true"
	}
	return out, meta
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
