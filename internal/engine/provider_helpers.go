package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/domain"
)

const (
	MaxProviderResponseBytes = 8 << 20
	webSearchToolName        = "web_search"
	secretRedactionText      = "[REDACTED]"
)

// providerMetadataAllowlist names the response fields llmention saves with an
// answer. Anything else the provider sends stays out of the saved run.
var providerMetadataAllowlist = map[string]struct{}{
	"response_id":           {},
	"request_id":            {},
	"model":                 {},
	"status":                {},
	"created":               {},
	"object":                {},
	"usage_input_tokens":    {},
	"usage_output_tokens":   {},
	"usage_total_tokens":    {},
	"usage_cached_tokens":   {},
	"usage_search_queries":  {},
	"usage_cost":            {},
	"native_finish_reason":  {},
	"dropped_evidence_urls": {},
}

// evidenceMapper turns cited and consulted URLs into citations and sources.
// It counts the URLs it had to drop, such as relative links or ftp links, so
// the saved answer shows that some were left out.
type evidenceMapper struct {
	dropped int
}

// providerEndpoint joins the base URL, or the provider's public API when there
// is none, with the path of the API call.
func providerEndpoint(base, fallback string, segments ...string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if err != nil {
			return "", fmt.Errorf("base_url is not a valid URL: %w", err)
		}
		return "", fmt.Errorf("base_url must be a full URL, such as https://api.example.com/v1")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("base_url must use http or https")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("base_url must not include a query (?) or a fragment (#)")
	}
	u.Path = path.Join(append([]string{u.Path}, segments...)...)
	return u.String(), nil
}

// readProviderBody reads at most MaxProviderResponseBytes. The second result
// is true when the body was longer.
func readProviderBody(body io.Reader) ([]byte, bool, error) {
	limited := io.LimitReader(body, MaxProviderResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(raw) > MaxProviderResponseBytes {
		return raw[:MaxProviderResponseBytes], true, nil
	}
	return raw, false, nil
}

func redactSecretBytes(data []byte, secret string) []byte {
	if secret == "" || len(data) == 0 {
		return data
	}
	return bytes.ReplaceAll(data, []byte(secret), []byte(secretRedactionText))
}

func redactSecretString(value, secret string) string {
	if secret == "" || value == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, secretRedactionText)
}

func (m *evidenceMapper) citation(rawURL, title string, metadata map[string]string) (Citation, bool) {
	cleanURL, observedDomain, ok := m.evidenceURL(rawURL)
	if !ok {
		return Citation{}, false
	}
	citation := Citation{URL: cleanURL, Title: title, Domain: googleSiteDomain(observedDomain, title)}
	if len(metadata) > 0 {
		citation.Metadata = cloneStringMap(metadata)
	}
	return citation, true
}

func (m *evidenceMapper) source(rawURL, title, kind string, metadata map[string]string) (Source, bool) {
	cleanURL, observedDomain, ok := m.evidenceURL(rawURL)
	if !ok {
		return Source{}, false
	}
	source := Source{URL: cleanURL, Title: title, Domain: googleSiteDomain(observedDomain, title), Kind: kind}
	if len(metadata) > 0 {
		source.Metadata = cloneStringMap(metadata)
	}
	return source, true
}

func (m *evidenceMapper) evidenceURL(raw string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		m.dropped++
		return "", "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		m.dropped++
		return "", "", false
	}
	observedDomain, err := domain.NormalizeObservedHost(u.Hostname())
	if err != nil {
		m.dropped++
		return "", "", false
	}
	return u.String(), observedDomain, true
}

func setProviderMetadata(metadata map[string]string, key, value string) {
	if value == "" {
		return
	}
	if _, ok := providerMetadataAllowlist[key]; !ok {
		return
	}
	metadata[key] = value
}

func setProviderMetadataAny(metadata map[string]string, key string, value any) {
	if value == nil {
		return
	}
	if _, ok := providerMetadataAllowlist[key]; !ok {
		return
	}
	switch v := value.(type) {
	case string:
		setProviderMetadata(metadata, key, v)
	case json.Number:
		setProviderMetadata(metadata, key, v.String())
	case float64:
		if v != 0 {
			metadata[key] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	case int:
		setProviderMetadataInt(metadata, key, v)
	case int64:
		if v != 0 {
			metadata[key] = strconv.FormatInt(v, 10)
		}
	case bool:
		metadata[key] = strconv.FormatBool(v)
	}
}

func setProviderMetadataInt(metadata map[string]string, key string, value int) {
	if value == 0 {
		return
	}
	if _, ok := providerMetadataAllowlist[key]; !ok {
		return
	}
	metadata[key] = strconv.Itoa(value)
}

func setProviderMetadataNumber(metadata map[string]string, key string, value json.Number) {
	if value == "" {
		return
	}
	if _, ok := providerMetadataAllowlist[key]; !ok {
		return
	}
	metadata[key] = value.String()
}

func setDroppedEvidenceMetadata(metadata map[string]string, dropped int) {
	if dropped > 0 {
		setProviderMetadataInt(metadata, "dropped_evidence_urls", dropped)
	}
}

func parseRetryAfter(header http.Header) *time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return nil
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		duration := time.Duration(seconds) * time.Second
		return &duration
	}
	if when, err := http.ParseTime(value); err == nil {
		duration := time.Until(when)
		if duration < 0 {
			duration = 0
		}
		return &duration
	}
	return nil
}

func compactJSONBytes(data []byte) (json.RawMessage, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, false
	}
	return append(json.RawMessage(nil), buf.Bytes()...), true
}

func rawSnippet(raw []byte) string {
	return SafeResponseSnippet(raw)
}

func providerReadError(provider string, status int, requestID string, err error) *ProviderError {
	return &ProviderError{
		ProviderID:  provider,
		RequestID:   requestID,
		StatusCode:  status,
		SafeMessage: fmt.Sprintf("could not read the answer from %s: %v", providerDisplayName(provider), err),
		Class:       ErrorClassTransient,
		Retryable:   true,
	}
}

func providerOverflowError(provider string, status int, requestID string) *ProviderError {
	return &ProviderError{
		ProviderID:  provider,
		RequestID:   requestID,
		StatusCode:  status,
		SafeMessage: fmt.Sprintf("the answer from %s was larger than %d bytes", providerDisplayName(provider), MaxProviderResponseBytes),
		Class:       ErrorClassProvider,
		SafeSnippet: "",
	}
}

func malformedProviderResponse(provider string, raw []byte, message string) *ProviderError {
	if message == "" {
		message = providerDisplayName(provider) + " sent a response llmention could not read"
	}
	return &ProviderError{
		ProviderID:  provider,
		SafeMessage: message,
		Class:       ErrorClassProvider,
		SafeSnippet: rawSnippet(raw),
	}
}

// providerDisplayName is the provider's name as people write it, for messages.
func providerDisplayName(provider string) string {
	switch provider {
	case "google":
		return "Google"
	case "perplexity":
		return "Perplexity"
	case "anthropic":
		return "Anthropic"
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "OpenAI"
	case "openai-compatible":
		return "the OpenAI-compatible server"
	default:
		return provider
	}
}

func hasWildcardSearchDomain(domains []string) bool {
	for _, value := range domains {
		if strings.HasPrefix(value, "*.") {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
