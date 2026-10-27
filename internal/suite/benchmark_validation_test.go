package suite_test

import (
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/suite"
)

func TestBenchmarkBuiltInsKeepAnswersOutOfRenderedFraming(t *testing.T) {
	entries := researchExamples(t)
	for _, entry := range entries {
		if entry.Suite.Type != "factoid" && entry.Suite.Type != "programming" && entry.Suite.Type != "temporal" {
			continue
		}
		for _, item := range entry.Suite.Items {
			rendered, err := suite.RenderVariants(entry.Suite, item)
			if err != nil {
				t.Fatalf("RenderVariants(%s/%s) error = %v", entry.Suite.ID, item.ID, err)
			}
			for _, variant := range rendered {
				framing := strings.ToLower(variant.Prompt)
				if item.Answers.Canonical != "" && strings.Contains(framing, strings.ToLower(item.Answers.Canonical)) {
					t.Fatalf("%s/%s leaked canonical answer into framing: %q", entry.Suite.ID, item.ID, variant.Prompt)
				}
				for _, term := range []string{"search", "browse", "json", "schema"} {
					if strings.Contains(framing, term) {
						t.Fatalf("%s/%s framing includes forbidden term %q: %q", entry.Suite.ID, item.ID, term, variant.Prompt)
					}
				}
			}
		}
	}
}

func TestBenchmarkSuiteValidationRejectsAnswerCollisions(t *testing.T) {
	input := strings.Replace(validFactoidSuiteYAML, `aliases: ["Armstrong"]`, `aliases: ["Buzz Aldrin"]`, 1)
	input = strings.Replace(input, "    grading:", "    known_wrong_answers:\n      - id: wrong\n        aliases: [\"Buzz Aldrin\"]\n    grading:", 1)
	_, err := suite.LoadBytes([]byte(input))
	if err == nil {
		t.Fatal("LoadBytes() accepted colliding correct and known-wrong answers")
	}
	if !strings.Contains(err.Error(), "once case and spacing are ignored") {
		t.Fatalf("error %q does not mention the clash", err.Error())
	}
}

func TestTemporalSourceRequirementModesMustRequireSources(t *testing.T) {
	_, err := suite.LoadBytes([]byte(`
id: temporal.custom
version: 1
type: temporal
purpose: "Custom temporal fixture."
default_modes: [memory, search_required]
repetitions: 1
evaluators:
  - id: exact
    type: exact_or_alias
metrics:
  - id: correct
    evaluator: exact
    kind: rate
items:
  - id: temporal.custom.item
    type: temporal.post_cutoff_fact
    question: "Who won the 2026 example award?"
    event_date: "2026-01-01"
    answers:
      canonical: "Example Winner"
    grading:
      type: exact_or_alias
      abstain_allowed: true
    expected_mode_behavior:
      memory: abstain_or_unknown
      search_required: not_required
    source_expectation:
      required_for_modes: [search_required]
      freshness_reference_date: "2026-08-01"
    evaluator_ids: [exact]
    metric_ids: [correct]
    tags: [events]
`))
	if err == nil {
		t.Fatal("LoadBytes() accepted source-required mode without correct_with_source behavior")
	}
	if !strings.Contains(err.Error(), "mode must have expected_mode_behavior correct_with_source") {
		t.Fatalf("error %q does not mention required mode behavior", err.Error())
	}
}
