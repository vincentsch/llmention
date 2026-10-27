package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

// CapabilityOverrides turns engine features on or off, like capabilities in
// llmention.yaml.
type CapabilityOverrides map[string]any

// MockFixtureSet holds fixed answers for the mock provider. A fixture answers
// the requests it matches. A request that matches no fixture gets
// defaults.answer when it is set. Otherwise the MockWorld writes a demo
// answer.
type MockFixtureSet struct {
	Version      int
	Defaults     MockDefaults
	Capabilities CapabilityOverrides
	Fixtures     []MockFixture
}

type MockDefaults struct {
	Answer *Answer
}

type MockFixture struct {
	Match  MockMatch
	Answer *Answer
	Error  *MockError
}

type MockMatch struct {
	EngineID  string
	SuiteID   string
	ItemID    string
	VariantID string
	Mode      ToolPolicy
	Prompt    string
}

type MockError struct {
	Class        ErrorClass
	Message      string
	Retryable    bool
	RequestID    string
	StatusCode   int
	RetryAfterMS int
	SafeSnippet  string
}

// mockEngine answers without the network, from fixtures or the MockWorld.
type mockEngine struct {
	baseEngine
	fixtures *validatedMockFixtures
}

// NewMockEngine builds a mock engine for tests.
func NewMockEngine(id, model string, fixtures *MockFixtureSet) (Engine, error) {
	if id == "" {
		return nil, fmt.Errorf("mock engine id is required")
	}
	if model == "" {
		return nil, fmt.Errorf("mock engine model is required")
	}
	base := baseEngine{
		id:       id,
		provider: "mock",
		model:    model,
		caps:     baseCapabilityProfile("mock", model),
	}
	return newMockEngine(base, fixtures, nil)
}

func newMockEngine(base baseEngine, fixtures *MockFixtureSet, configOverrides config.Capabilities) (Engine, error) {
	if fixtures != nil {
		if err := applyCapabilityOverrides(&base.caps, map[string]any(fixtures.Capabilities)); err != nil {
			return nil, err
		}
	}
	if err := applyCapabilityOverrides(&base.caps, configOverrides); err != nil {
		return nil, err
	}
	validated, err := validateMockFixtures(fixtures)
	if err != nil {
		return nil, err
	}
	return &mockEngine{baseEngine: base, fixtures: validated}, nil
}

func (e *mockEngine) Ask(ctx context.Context, req Request) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return Answer{}, err
	}
	if err := e.ValidateRequest(req); err != nil {
		return Answer{}, &ProviderError{
			ProviderID:  e.provider,
			SafeMessage: err.Error(),
			Class:       ErrorClassCaller,
		}
	}
	if err := ctx.Err(); err != nil {
		return Answer{}, err
	}
	selected := e.fixtures.resolve(e.id, req)
	if selected.err != nil {
		return Answer{}, selected.err.providerError(e.provider)
	}
	if selected.answer == nil {
		if generated, ok := e.opts.world.answer(e.id, e.model, req); ok && !e.fixtures.defaultConfigured {
			selected.answer = &generated
		} else {
			selected.answer = &e.fixtures.defaultAnswer
		}
	}
	answer := cloneAnswer(*selected.answer)
	answer.Raw, answer.ProviderMetadata = CaptureRawPayload(answer.Raw, answer.ProviderMetadata)
	return answer, nil
}

type validatedMockFixtures struct {
	defaultAnswer     Answer
	hasDefault        bool
	defaultConfigured bool
	fixtures          []validatedMockFixture
}

type validatedMockFixture struct {
	priority int
	key      string
	match    MockMatch
	answer   *Answer
	err      *MockError
}

type resolvedMockFixture struct {
	answer *Answer
	err    *MockError
}

func validateMockFixtures(set *MockFixtureSet) (*validatedMockFixtures, error) {
	if set == nil {
		return &validatedMockFixtures{
			defaultAnswer: Answer{Text: "Mock answer"},
			hasDefault:    true,
		}, nil
	}
	version := set.Version
	if version == 0 {
		version = 1
	}
	if version != 1 {
		return nil, fmt.Errorf("the mock answers need version: 1")
	}
	out := &validatedMockFixtures{}
	if set.Defaults.Answer != nil {
		answer, err := normalizeFixtureAnswer(*set.Defaults.Answer)
		if err != nil {
			return nil, fmt.Errorf("defaults.answer: %w", err)
		}
		out.defaultAnswer = answer
		out.hasDefault = true
		out.defaultConfigured = true
	}
	seen := map[string]struct{}{}
	for i, fixture := range set.Fixtures {
		priority, key, err := fixture.Match.priorityKey()
		if err != nil {
			return nil, fmt.Errorf("fixtures[%d].match: %w", i, err)
		}
		if priority == 7 && out.hasDefault {
			return nil, fmt.Errorf("fixtures[%d] matches every request, which defaults.answer already does", i)
		}
		duplicateKey := fmt.Sprintf("%d:%s", priority, key)
		if _, ok := seen[duplicateKey]; ok {
			return nil, fmt.Errorf("fixtures[%d] has the same match as an earlier fixture: %q", i, key)
		}
		seen[duplicateKey] = struct{}{}
		if (fixture.Answer == nil) == (fixture.Error == nil) {
			return nil, fmt.Errorf("fixtures[%d] must set exactly one of answer or error", i)
		}
		validated := validatedMockFixture{priority: priority, key: key, match: fixture.Match}
		if fixture.Answer != nil {
			answer, err := normalizeFixtureAnswer(*fixture.Answer)
			if err != nil {
				return nil, fmt.Errorf("fixtures[%d].answer: %w", i, err)
			}
			validated.answer = &answer
		}
		if fixture.Error != nil {
			if err := fixture.Error.validate(); err != nil {
				return nil, fmt.Errorf("fixtures[%d].error: %w", i, err)
			}
			copied := *fixture.Error
			validated.err = &copied
		}
		out.fixtures = append(out.fixtures, validated)
	}
	if !out.hasDefault {
		out.defaultAnswer = Answer{Text: "Mock answer"}
		out.hasDefault = true
	}
	return out, nil
}

func (f *validatedMockFixtures) resolve(engineID string, req Request) resolvedMockFixture {
	bestPriority := 8
	var best *validatedMockFixture
	for i := range f.fixtures {
		fixture := &f.fixtures[i]
		if fixture.priority >= bestPriority {
			continue
		}
		if fixture.match.matches(engineID, req) {
			best = fixture
			bestPriority = fixture.priority
		}
	}
	if best != nil {
		return resolvedMockFixture{answer: best.answer, err: best.err}
	}
	return resolvedMockFixture{}
}

// priorityKey ranks a match by how specific it is, 1 being the most specific,
// and returns a key that finds two fixtures with the same match. Only the
// field combinations listed here are allowed.
func (m MockMatch) priorityKey() (int, string, error) {
	hasEngine := m.EngineID != ""
	hasSuite := m.SuiteID != ""
	hasItem := m.ItemID != ""
	hasVariant := m.VariantID != ""
	hasMode := m.Mode != ""
	hasPrompt := m.Prompt != ""
	if hasMode && !m.Mode.Valid() {
		return 0, "", fmt.Errorf("mode %q is invalid", m.Mode)
	}
	switch {
	case hasEngine && hasSuite && hasItem && hasVariant && hasMode && !hasPrompt:
		return 1, joinMatchKey(m.EngineID, m.SuiteID, m.ItemID, m.VariantID, m.Mode.String()), nil
	case hasEngine && hasSuite && hasItem && !hasVariant && hasMode && !hasPrompt:
		return 2, joinMatchKey(m.EngineID, m.SuiteID, m.ItemID, m.Mode.String()), nil
	case !hasEngine && hasSuite && hasItem && hasVariant && hasMode && !hasPrompt:
		return 3, joinMatchKey(m.SuiteID, m.ItemID, m.VariantID, m.Mode.String()), nil
	case !hasEngine && hasSuite && hasItem && !hasVariant && hasMode && !hasPrompt:
		return 4, joinMatchKey(m.SuiteID, m.ItemID, m.Mode.String()), nil
	case !hasEngine && !hasSuite && !hasItem && !hasVariant && hasMode && hasPrompt:
		return 5, joinMatchKey(m.Prompt, m.Mode.String()), nil
	case !hasEngine && !hasSuite && !hasItem && !hasVariant && hasMode && !hasPrompt:
		return 6, m.Mode.String(), nil
	case !hasEngine && !hasSuite && !hasItem && !hasVariant && !hasMode && !hasPrompt:
		return 7, "global", nil
	default:
		return 0, "", fmt.Errorf("this combination of match fields is not supported")
	}
}

func joinMatchKey(values ...string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += "\x00"
		}
		out += value
	}
	return out
}

func (m MockMatch) matches(engineID string, req Request) bool {
	if m.EngineID != "" && m.EngineID != engineID {
		return false
	}
	if m.SuiteID != "" && m.SuiteID != req.SuiteID {
		return false
	}
	if m.ItemID != "" && m.ItemID != req.ItemID {
		return false
	}
	if m.VariantID != "" && m.VariantID != req.VariantID {
		return false
	}
	if m.Mode != "" && m.Mode != req.Mode {
		return false
	}
	if m.Prompt != "" && m.Prompt != req.Prompt {
		return false
	}
	return true
}

func (e MockError) validate() error {
	if e.Message == "" {
		return fmt.Errorf("message must not be empty")
	}
	if e.Class != "" && !e.Class.Valid() {
		return fmt.Errorf("class %q is invalid", e.Class)
	}
	if e.StatusCode < 0 {
		return fmt.Errorf("status_code must be positive when set")
	}
	if e.RetryAfterMS < 0 {
		return fmt.Errorf("retry_after_ms must be positive when set")
	}
	return nil
}

func (e MockError) providerError(provider string) *ProviderError {
	class := e.Class
	if class == "" {
		class = ErrorClassProvider
	}
	err := &ProviderError{
		ProviderID:  provider,
		RequestID:   e.RequestID,
		StatusCode:  e.StatusCode,
		SafeMessage: e.Message,
		Class:       class,
		Retryable:   e.Retryable,
		SafeSnippet: SafeResponseSnippet([]byte(e.SafeSnippet)),
	}
	if e.RetryAfterMS > 0 {
		duration := timeDurationMS(e.RetryAfterMS)
		err.RetryAfter = &duration
	}
	return err
}

func timeDurationMS(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

func normalizeFixtureAnswer(answer Answer) (Answer, error) {
	out := cloneAnswer(answer)
	parsed, err := normalizeParsedJSON(out.Parsed)
	if err != nil {
		return Answer{}, err
	}
	out.Parsed = parsed
	for i := range out.Citations {
		out.Citations[i], err = normalizeCitation(out.Citations[i])
		if err != nil {
			return Answer{}, fmt.Errorf("citations[%d]: %w", i, err)
		}
	}
	for i := range out.SourcesConsulted {
		out.SourcesConsulted[i], err = normalizeSource(out.SourcesConsulted[i])
		if err != nil {
			return Answer{}, fmt.Errorf("sources_consulted[%d]: %w", i, err)
		}
	}
	return out, nil
}

func cloneAnswer(in Answer) Answer {
	return Answer{
		Text:             in.Text,
		Parsed:           append([]byte(nil), in.Parsed...),
		Citations:        cloneCitations(in.Citations),
		SourcesConsulted: cloneSources(in.SourcesConsulted),
		ToolCalls:        cloneToolCalls(in.ToolCalls),
		ToolsAvailable:   append([]string(nil), in.ToolsAvailable...),
		ToolsUsed:        append([]string(nil), in.ToolsUsed...),
		Raw:              append([]byte(nil), in.Raw...),
		StopReason:       in.StopReason,
		ProviderMetadata: cloneStringMap(in.ProviderMetadata),
	}
}

func cloneCitations(in []Citation) []Citation {
	if len(in) == 0 {
		return nil
	}
	out := make([]Citation, len(in))
	for i, citation := range in {
		out[i] = Citation{
			URL:      citation.URL,
			Title:    citation.Title,
			Domain:   citation.Domain,
			Metadata: cloneStringMap(citation.Metadata),
		}
	}
	return out
}

func cloneSources(in []Source) []Source {
	if len(in) == 0 {
		return nil
	}
	out := make([]Source, len(in))
	for i, source := range in {
		out[i] = Source{
			URL:      source.URL,
			Title:    source.Title,
			Domain:   source.Domain,
			Kind:     source.Kind,
			Metadata: cloneStringMap(source.Metadata),
		}
	}
	return out
}

func cloneToolCalls(in []ToolCall) []ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolCall, len(in))
	for i, call := range in {
		out[i] = call
		out[i].Raw = append([]byte(nil), call.Raw...)
	}
	return out
}
