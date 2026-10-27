package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/vincentsch/llmention/internal/domain"
)

// mockFixtureFile is the YAML file of fixed answers that a mock engine reads
// from fixtures_path, as in examples/research/fixtures.
type mockFixtureFile struct {
	Version      int                    `yaml:"version"`
	Defaults     mockDefaultsFile       `yaml:"defaults"`
	Capabilities map[string]any         `yaml:"capabilities"`
	Fixtures     []mockFixtureEntryFile `yaml:"fixtures"`
}

type mockDefaultsFile struct {
	Answer *mockAnswerFile `yaml:"answer"`
}

type mockFixtureEntryFile struct {
	Match  mockMatchFile   `yaml:"match"`
	Answer *mockAnswerFile `yaml:"answer"`
	Error  *mockErrorFile  `yaml:"error"`
}

type mockMatchFile struct {
	EngineID  string `yaml:"engine_id"`
	SuiteID   string `yaml:"suite_id"`
	ItemID    string `yaml:"item_id"`
	VariantID string `yaml:"variant_id"`
	Mode      string `yaml:"mode"`
	Prompt    string `yaml:"prompt"`
}

type mockAnswerFile struct {
	Text             string             `yaml:"text"`
	ParsedJSON       yaml.Node          `yaml:"parsed_json"`
	Raw              string             `yaml:"raw"`
	RawBase64        string             `yaml:"raw_base64"`
	Citations        []Citation         `yaml:"citations"`
	SourcesConsulted []Source           `yaml:"sources_consulted"`
	ToolCalls        []mockToolCallFile `yaml:"tool_calls"`
	ToolsAvailable   []string           `yaml:"tools_available"`
	ToolsUsed        []string           `yaml:"tools_used"`
	StopReason       string             `yaml:"stop_reason"`
	ProviderMetadata map[string]string  `yaml:"provider_metadata"`
}

type mockToolCallFile struct {
	Type               string `yaml:"type"`
	Query              string `yaml:"query"`
	URL                string `yaml:"url"`
	Status             string `yaml:"status"`
	ProviderToolCallID string `yaml:"provider_tool_call_id"`
	ErrorCode          string `yaml:"error_code"`
	ErrorMessage       string `yaml:"error_message"`
	Raw                string `yaml:"raw"`
	RawBase64          string `yaml:"raw_base64"`
}

type mockErrorFile struct {
	Class        string `yaml:"class"`
	Message      string `yaml:"message"`
	Retryable    bool   `yaml:"retryable"`
	RequestID    string `yaml:"request_id"`
	StatusCode   int    `yaml:"status_code"`
	RetryAfterMS int    `yaml:"retry_after_ms"`
	SafeSnippet  string `yaml:"safe_snippet"`
}

// LoadMockFixtures reads a file of fixed answers for the mock provider.
func LoadMockFixtures(path string) (*MockFixtureSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadMockFixturesBytes(data)
}

func LoadMockFixturesBytes(data []byte) (*MockFixtureSet, error) {
	var raw mockFixtureFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("the mock answers file must hold exactly one YAML document")
		}
		return nil, err
	}
	if raw.Version != 1 {
		return nil, fmt.Errorf("the mock answers file needs version: 1")
	}
	profile := baseCapabilityProfile("mock", "fixture")
	if err := applyCapabilityOverrides(&profile, raw.Capabilities); err != nil {
		return nil, err
	}
	set := &MockFixtureSet{
		Version:      raw.Version,
		Capabilities: CapabilityOverrides(raw.Capabilities),
		Fixtures:     make([]MockFixture, 0, len(raw.Fixtures)),
	}
	if raw.Defaults.Answer != nil {
		answer, err := raw.Defaults.Answer.toAnswer()
		if err != nil {
			return nil, fmt.Errorf("defaults.answer: %w", err)
		}
		set.Defaults.Answer = &answer
	}
	for i, fixture := range raw.Fixtures {
		converted := MockFixture{Match: fixture.Match.toMatch()}
		var err error
		if fixture.Answer != nil {
			answer, err := fixture.Answer.toAnswer()
			if err != nil {
				return nil, fmt.Errorf("fixtures[%d].answer: %w", i, err)
			}
			converted.Answer = &answer
		}
		if fixture.Error != nil {
			converted.Error, err = fixture.Error.toMockError()
			if err != nil {
				return nil, fmt.Errorf("fixtures[%d].error: %w", i, err)
			}
		}
		set.Fixtures = append(set.Fixtures, converted)
	}
	if _, err := validateMockFixtures(set); err != nil {
		return nil, err
	}
	return set, nil
}

func (m mockMatchFile) toMatch() MockMatch {
	var mode ToolPolicy
	if m.Mode != "" {
		mode = ToolPolicy(strings.TrimSpace(m.Mode))
	}
	return MockMatch{
		EngineID:  strings.TrimSpace(m.EngineID),
		SuiteID:   strings.TrimSpace(m.SuiteID),
		ItemID:    strings.TrimSpace(m.ItemID),
		VariantID: strings.TrimSpace(m.VariantID),
		Mode:      mode,
		Prompt:    m.Prompt,
	}
}

func (a mockAnswerFile) toAnswer() (Answer, error) {
	raw, err := decodeRaw("raw", a.Raw, a.RawBase64)
	if err != nil {
		return Answer{}, err
	}
	parsed, err := yamlNodeToJSON(a.ParsedJSON)
	if err != nil {
		return Answer{}, fmt.Errorf("parsed_json: %w", err)
	}
	toolCalls := make([]ToolCall, 0, len(a.ToolCalls))
	for i, call := range a.ToolCalls {
		converted, err := call.toToolCall()
		if err != nil {
			return Answer{}, fmt.Errorf("tool_calls[%d]: %w", i, err)
		}
		toolCalls = append(toolCalls, converted)
	}
	return Answer{
		Text:             a.Text,
		Parsed:           parsed,
		Citations:        cloneCitations(a.Citations),
		SourcesConsulted: cloneSources(a.SourcesConsulted),
		ToolCalls:        toolCalls,
		ToolsAvailable:   append([]string(nil), a.ToolsAvailable...),
		ToolsUsed:        append([]string(nil), a.ToolsUsed...),
		Raw:              raw,
		StopReason:       a.StopReason,
		ProviderMetadata: cloneStringMap(a.ProviderMetadata),
	}, nil
}

func (c mockToolCallFile) toToolCall() (ToolCall, error) {
	raw, err := decodeRaw("tool call raw", c.Raw, c.RawBase64)
	if err != nil {
		return ToolCall{}, err
	}
	return ToolCall{
		Type:               c.Type,
		Query:              c.Query,
		URL:                c.URL,
		Status:             c.Status,
		ProviderToolCallID: c.ProviderToolCallID,
		ErrorCode:          c.ErrorCode,
		ErrorMessage:       c.ErrorMessage,
		Raw:                raw,
	}, nil
}

func (e mockErrorFile) toMockError() (*MockError, error) {
	class := ErrorClass(e.Class)
	if class != "" && !class.Valid() {
		return nil, fmt.Errorf("class %q is invalid", e.Class)
	}
	return &MockError{
		Class:        class,
		Message:      e.Message,
		Retryable:    e.Retryable,
		RequestID:    e.RequestID,
		StatusCode:   e.StatusCode,
		RetryAfterMS: e.RetryAfterMS,
		SafeSnippet:  e.SafeSnippet,
	}, nil
}

func decodeRaw(field, raw, rawBase64 string) ([]byte, error) {
	if raw != "" && rawBase64 != "" {
		return nil, fmt.Errorf("%s and raw_base64 must not both be set", field)
	}
	if raw != "" {
		if !utf8.ValidString(raw) {
			return nil, fmt.Errorf("%s must be valid UTF-8", field)
		}
		return []byte(raw), nil
	}
	if rawBase64 == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(rawBase64)
	if err != nil {
		return nil, fmt.Errorf("raw_base64 is invalid: %w", err)
	}
	return decoded, nil
}

func yamlNodeToJSON(node yaml.Node) (json.RawMessage, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	value, err := yamlNodeValue(&node)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func normalizeParsedJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, fmt.Errorf("parsed JSON is invalid: %w", err)
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

func yamlNodeValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil, nil
		}
		return yamlNodeValue(node.Content[0])
	case yaml.MappingNode:
		out := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("map key must be a scalar")
			}
			key, err := yamlScalarValue(keyNode)
			if err != nil {
				return nil, err
			}
			keyString, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("map key must be a string")
			}
			value, err := yamlNodeValue(valueNode)
			if err != nil {
				return nil, err
			}
			out[keyString] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlNodeValue(child)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case yaml.ScalarNode:
		return yamlScalarValue(node)
	case yaml.AliasNode:
		return nil, fmt.Errorf("aliases are not supported")
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
}

func yamlScalarValue(node *yaml.Node) (any, error) {
	switch node.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		return strconv.ParseBool(node.Value)
	case "!!int":
		return strconv.ParseInt(node.Value, 10, 64)
	case "!!float":
		return strconv.ParseFloat(node.Value, 64)
	case "!!str":
		return node.Value, nil
	default:
		return node.Value, nil
	}
}

func normalizeCitation(citation Citation) (Citation, error) {
	out := Citation{
		URL:      citation.URL,
		Title:    citation.Title,
		Metadata: cloneStringMap(citation.Metadata),
	}
	domainValue, err := normalizeEvidenceDomain(citation.URL, citation.Domain)
	if err != nil {
		return Citation{}, err
	}
	out.Domain = domainValue
	return out, nil
}

func normalizeSource(source Source) (Source, error) {
	out := Source{
		URL:      source.URL,
		Title:    source.Title,
		Kind:     source.Kind,
		Metadata: cloneStringMap(source.Metadata),
	}
	domainValue, err := normalizeEvidenceDomain(source.URL, source.Domain)
	if err != nil {
		return Source{}, err
	}
	out.Domain = domainValue
	return out, nil
}

// normalizeEvidenceDomain returns the domain of a fixture citation. When the
// fixture gives both a URL and a domain, they must name the same site.
func normalizeEvidenceDomain(rawURL, rawDomain string) (string, error) {
	var fromURL string
	if rawURL != "" {
		normalized, err := domain.NormalizeScoringDomain(rawURL)
		if err != nil {
			return "", fmt.Errorf("the URL has no valid domain: %w", err)
		}
		fromURL = normalized
	}
	if rawDomain == "" {
		return fromURL, nil
	}
	normalizedDomain, err := domain.NormalizeScoringDomain(rawDomain)
	if err != nil {
		return "", fmt.Errorf("the domain is not valid: %w", err)
	}
	if fromURL != "" && normalizedDomain != fromURL {
		return "", fmt.Errorf("domain %q does not match url domain %q", normalizedDomain, fromURL)
	}
	return normalizedDomain, nil
}
