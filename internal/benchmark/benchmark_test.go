package benchmark

import (
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/engine"
)

func TestParseAnswerStrictJSONAndConfidencePolicy(t *testing.T) {
	parsed, err := ParseAnswer(`{"answer":"Sao Paulo","confidence":101}`, ConfidencePolicyOptional)
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v", err)
	}
	if parsed.Answer == nil || *parsed.Answer != "Sao Paulo" {
		t.Fatalf("answer = %#v", parsed.Answer)
	}
	if parsed.Confidence == nil || *parsed.Confidence != 100 {
		t.Fatalf("confidence = %#v, want clamped 100", parsed.Confidence)
	}
	if !hasWarning(parsed.Warnings, WarningConfidenceClamped) {
		t.Fatalf("warnings = %#v, want confidence clamp", parsed.Warnings)
	}

	if _, err := ParseAnswer(`{"answer":"Sao Paulo","extra":true}`, ConfidencePolicyOptional); err == nil {
		t.Fatal("ParseAnswer() accepted unknown JSON field")
	}
	if _, err := ParseAnswer(`plain answer`, ConfidencePolicyRequired); err == nil {
		t.Fatal("ParseAnswer() accepted plain text when confidence is required")
	}
}

func TestMatchAnswerCanonicalAliasWrongAndAbstention(t *testing.T) {
	config := MatchConfig{
		Canonical: "Neil Armstrong",
		Aliases:   []string{"N. Armstrong"},
		KnownIncorrect: []ConfiguredAnswer{{
			ID:      "wrong:first-man",
			Aliases: []string{"Buzz Aldrin"},
			Class:   FailureClassIncorrect,
		}},
	}
	tests := []struct {
		name       string
		answer     *string
		confidence *int
		wantStatus string
		wantKind   string
		wantClass  string
		wantWrong  bool
	}{
		{name: "canonical", answer: stringPtr("Neil Armstrong"), wantStatus: AnswerStatusCorrect, wantKind: MatchKindCanonical},
		{name: "alias", answer: stringPtr("n. armstrong"), wantStatus: AnswerStatusCorrect, wantKind: MatchKindAlias},
		{name: "abstention", answer: stringPtr("I don't know"), wantStatus: AnswerStatusNotAttempted, wantKind: MatchKindAbstention},
		{name: "known wrong", answer: stringPtr("Buzz Aldrin"), confidence: intPtr(90), wantStatus: AnswerStatusIncorrect, wantKind: MatchKindConfiguredIncorrect, wantClass: FailureClassIncorrect, wantWrong: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchAnswer(ParsedAnswer{Answer: tt.answer, Confidence: tt.confidence}, config, SuiteFamilyFactoid)
			if got.AnswerStatus != tt.wantStatus || got.MatchKind != tt.wantKind || got.FailureClass != tt.wantClass || got.OverconfidentWrong != tt.wantWrong {
				t.Fatalf("MatchAnswer() = %#v", got)
			}
		})
	}
}

func TestBuildPromptMemoryInstructionAndSchema(t *testing.T) {
	prompt := BuildPrompt("What is the answer?", "Answer tersely.", "memory")
	for _, want := range []string{memoryModeInstruction, "Answer tersely.", "Question: What is the answer?", OutputSchemaName} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(BuildPrompt("What is the answer?", "", "search_required"), memoryModeInstruction) {
		t.Fatal("search prompt included memory-only instruction")
	}
}

func TestDeriveSourceEvidenceNoFreshnessReferenceWithUnavailableSources(t *testing.T) {
	got := DeriveSourceEvidence(nil, engine.CapabilityProfile{}, "search_required", SourceExpectation{
		RequiredForModes: []string{"search_required"},
	})
	if got.RequirementStatus != SourceStatusUnavailable {
		t.Fatalf("requirement status = %q, want %q", got.RequirementStatus, SourceStatusUnavailable)
	}
	if got.FreshnessStatus != FreshnessNotRequired {
		t.Fatalf("freshness status = %q, want %q", got.FreshnessStatus, FreshnessNotRequired)
	}
}

func hasWarning(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
