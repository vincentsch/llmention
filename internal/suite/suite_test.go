package suite_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/suite"
)

func TestBuiltInsAreTheRecognitionSuite(t *testing.T) {
	entries, err := suite.LoadBuiltIns()
	if err != nil {
		t.Fatalf("LoadBuiltIns() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Suite.ID != "recognition.basic" || entries[0].Suite.ContextMode != suite.ContextModeProjectBound {
		t.Fatalf("built-ins = %+v", entries)
	}
	if len(entries[0].Suite.Items) != 0 {
		t.Fatal("recognition.basic must take its items from the brand, not ship its own")
	}
}

// The research suites in examples/research are ordinary suite files. This test
// fails when a change breaks loading or rendering them.
func TestResearchExamplesValidateAndRender(t *testing.T) {
	entries := researchExamples(t)
	if len(entries) != 7 {
		t.Fatalf("examples = %d, want 7", len(entries))
	}
	for _, entry := range entries {
		var rendered []suite.RenderedVariant
		for _, item := range entry.Suite.Items {
			variants, err := suite.RenderVariants(entry.Suite, item)
			if err != nil {
				t.Fatalf("RenderVariants(%s/%s) error = %v", entry.Suite.ID, item.ID, err)
			}
			rendered = append(rendered, variants...)
		}
		if len(rendered) == 0 {
			t.Fatalf("example suite %s rendered no prompts", entry.Suite.ID)
		}
	}
}

func researchExamples(t *testing.T) []suite.LoadedSuite {
	t.Helper()
	entries, err := suite.LoadUserPaths([]string{filepath.Join("..", "..", "examples", "research", "*.yaml")})
	if err != nil {
		t.Fatalf("load examples: %v", err)
	}
	return entries
}

func TestLoadBytesInvalidSuites(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "missing id",
			input: strings.Replace(validVisibilitySuiteYAML, "id: visibility.custom", `id: ""`, 1),
			want:  "id: is required",
		},
		{
			name: "duplicate item id",
			input: validVisibilitySuiteYAML + `
  - id: visibility.custom.item
    type: visibility.recommendation
    prompt: "Which tool should I use?"
    intent: high
    locale: en-US
    brand:
      name: "Acme"
    owned_domains: ["acme.example"]
    expected_answer_shape: free_text
    evaluator_ids: [mention]
    metric_ids: [brand_mention_rate]
    tags: [brand]
`,
			want: "is used twice",
		},
		{
			name:  "invalid mode",
			input: strings.Replace(validVisibilitySuiteYAML, "default_modes: [memory]", "default_modes: [memory, search_sometimes]", 1),
			want:  "unknown mode",
		},
		{
			name:  "undeclared evaluator",
			input: strings.Replace(validVisibilitySuiteYAML, "evaluator: mention", "evaluator: missing", 1),
			want:  "which is not listed under evaluators",
		},
		{
			name:  "undeclared metric",
			input: strings.Replace(validVisibilitySuiteYAML, "metric_ids: [brand_mention_rate]", "metric_ids: [unknown_metric]", 1),
			want:  "which is not listed under metrics",
		},
		{
			name:  "explicit empty evaluator selection",
			input: strings.Replace(validVisibilitySuiteYAML, "evaluator_ids: [mention]", "evaluator_ids: []", 1),
			want:  "evaluator_ids: must not be empty when set",
		},
		{
			name:  "explicit empty metric selection",
			input: strings.Replace(validVisibilitySuiteYAML, "metric_ids: [brand_mention_rate]", "metric_ids: []", 1),
			want:  "metric_ids: must not be empty when set",
		},
		{
			name:  "unknown item field",
			input: strings.Replace(validVisibilitySuiteYAML, "tags: [brand]", "unexpected_field: true\n    tags: [brand]", 1),
			want:  "field unexpected_field not found in type suite.Item",
		},
		{
			name:  "unknown nested item field",
			input: strings.Replace(validVisibilitySuiteYAML, `aliases: ["Un Spar"]`, "aliases: [\"Un Spar\"]\n      unexpected_field: true", 1),
			want:  "field unexpected_field not found in type suite.Entity",
		},
		{
			name:  "malformed item schema",
			input: strings.Replace(validVisibilitySuiteYAML, `prompt: "Which tool should I use?"`, `prompt: ""`, 1),
			want:  "prompt: is required",
		},
		{
			name:  "invalid grading type",
			input: strings.Replace(validFactoidSuiteYAML, "grading:\n      type: exact_or_alias", "grading:\n      type: vibe_check", 1),
			want:  "not a known grading type",
		},
		{
			name:  "factoid incompatible known grading type",
			input: strings.Replace(validFactoidSuiteYAML, "grading:\n      type: exact_or_alias", "grading:\n      type: claim_stance", 1),
			want:  "does not work for this kind of item",
		},
		{
			name:  "benchmark variant references question",
			input: strings.Replace(validFactoidSuiteYAML, `prompt: "Keep the answer brief."`, `template: "{{ .Question }}"`, 1),
			want:  "must not repeat the item's question",
		},
		{
			name:  "benchmark variant repeats literal question",
			input: strings.Replace(validFactoidSuiteYAML, `prompt: "Keep the answer brief."`, `prompt: "Who commanded Apollo 11?"`, 1),
			want:  "must not repeat the item's question",
		},
		{
			name:  "censorship incompatible known grading type",
			input: strings.Replace(validCensorshipSuiteYAML, "grading:\n      type: claim_coverage", "grading:\n      type: exact_or_alias", 1),
			want:  "does not work for this kind of item",
		},
		{
			name:  "propaganda incompatible known grading type",
			input: strings.Replace(validPropagandaSuiteYAML, "grading:\n      type: claim_stance", "grading:\n      type: exact_or_alias", 1),
			want:  "does not work for this kind of item",
		},
		{
			name:  "bad variant template",
			input: strings.Replace(validVisibilitySuiteYAML, `template: "{{ .Prompt }}"`, `template: "{{ .Prompt"`, 1),
			want:  "unclosed action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := suite.LoadBytes([]byte(tt.input))
			if err == nil {
				t.Fatal("LoadBytes() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestLoadBytesRejectsAdditionalDocuments(t *testing.T) {
	_, err := suite.LoadBytes([]byte(validVisibilitySuiteYAML + `
---
id: visibility.second
version: 1
type: visibility
`))
	if err == nil {
		t.Fatal("LoadBytes() succeeded, want multi-document error")
	}
	if !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("error %q does not mention single document requirement", err.Error())
	}
}

func TestLoadBytesAllowsOmittedItemEvaluatorAndMetricSelections(t *testing.T) {
	input := strings.Replace(validVisibilitySuiteYAML, "    evaluator_ids: [mention]\n", "", 1)
	input = strings.Replace(input, "    metric_ids: [brand_mention_rate]\n", "", 1)
	loaded, err := suite.LoadBytes([]byte(input))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if len(loaded.Items[0].EvaluatorIDs) != 0 || len(loaded.Items[0].MetricIDs) != 0 {
		t.Fatalf("item selections = %#v/%#v, want inherited empty slices", loaded.Items[0].EvaluatorIDs, loaded.Items[0].MetricIDs)
	}
}

func TestRecognitionSuiteValidation(t *testing.T) {
	if _, err := suite.LoadBytes([]byte(validRecognitionSuiteYAML)); err != nil {
		t.Fatalf("valid recognition suite rejected: %v", err)
	}
	benignVariant := strings.Replace(validRecognitionSuiteYAML, "repetitions: 1", `repetitions: 1
variants:
  - id: research
    prompt: "Frame the query around independent research context."`, 1)
	if _, err := suite.LoadBytes([]byte(benignVariant)); err != nil {
		t.Fatalf("recognition suite with benign variant rejected: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "missing entity cluster evaluator",
			input: strings.Replace(validRecognitionSuiteYAML, "type: entity_cluster", "type: mention_match", 1),
			want:  "a recognition suite needs exactly one evaluator of type entity_cluster",
		},
		{
			name: "duplicate entity cluster evaluator",
			input: strings.Replace(validRecognitionSuiteYAML, "evaluators:\n  - id: cluster\n    type: entity_cluster", `evaluators:
  - id: cluster
    type: entity_cluster
  - id: other
    type: entity_cluster`, 1),
			want: "a recognition suite needs exactly one evaluator of type entity_cluster",
		},
		{
			name:  "wrong privacy",
			input: strings.Replace(validRecognitionSuiteYAML, "privacy: local_only", "privacy: public", 1),
			want:  "privacy: must be local_only",
		},
		{
			name:  "unknown configured entity type",
			input: strings.Replace(validRecognitionSuiteYAML, "entity_type: person", "entity_type: unknown", 1),
			want:  "must not be unknown",
		},
		{
			name: "duplicate configured identity",
			input: strings.Replace(validRecognitionSuiteYAML, "known_wrong_namesakes: []", `known_wrong_namesakes:
      - canonical_name: "Jordan Lee"
        entity_type: person`, 1),
			want: "names the same thing as",
		},
		{
			name:  "empty alias",
			input: strings.Replace(validRecognitionSuiteYAML, `aliases: ["A. Rivera"]`, `aliases: ["   "]`, 1),
			want:  "aliases[0]: is empty",
		},
		{
			name:  "empty descriptor",
			input: strings.Replace(validRecognitionSuiteYAML, `descriptors_any: ["software"]`, `descriptors_any: ["   "]`, 1),
			want:  "descriptors_any[0]: is empty",
		},
		{
			name: "empty expected entities",
			input: strings.Replace(validRecognitionSuiteYAML, `expected_entities:
      - canonical_name: "Jordan Lee"
        entity_type: person
        aliases: ["A. Rivera"]
        descriptors_any: ["software"]`, `expected_entities: []`, 1),
			want: "expected_entities: must not be empty",
		},
		{
			name: "suite variant mentions search",
			input: strings.Replace(validRecognitionSuiteYAML, "repetitions: 1", `repetitions: 1
variants:
  - id: searched
    prompt: "Use web search before answering."`, 1),
			want: "it must not mention tools",
		},
		{
			name: "item variant changes output shape",
			input: strings.Replace(validRecognitionSuiteYAML, `dimensions:
      market: us`, `variants:
      - id: yaml
        prompt: "Return YAML instead of JSON."
    dimensions:
      market: us`, 1),
			want: "it must not mention tools",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := suite.LoadBytes([]byte(tt.input))
			if err == nil {
				t.Fatal("LoadBytes() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRenderVariantsIsDeterministic(t *testing.T) {
	loaded, err := suite.LoadBytes([]byte(validVisibilitySuiteYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	first, err := suite.RenderVariants(loaded, loaded.Items[0])
	if err != nil {
		t.Fatalf("first RenderVariants() error = %v", err)
	}
	second, err := suite.RenderVariants(loaded, loaded.Items[0])
	if err != nil {
		t.Fatalf("second RenderVariants() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("rendered variants not deterministic:\n%#v\n%#v", first, second)
	}
	wantPrompts := []string{"Which tool should I use?", "Which tool should I use? Answer briefly."}
	gotPrompts := []string{first[0].Prompt, first[1].Prompt}
	if !reflect.DeepEqual(gotPrompts, wantPrompts) {
		t.Fatalf("prompts = %#v, want %#v", gotPrompts, wantPrompts)
	}
}

func TestLoadUserPathsExpandsSortedGlobsAndIgnoresUnmatched(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(customSuiteYAML("visibility.b", "visibility.b.item")), 0o644); err != nil {
		t.Fatalf("write b suite: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(customSuiteYAML("visibility.a", "visibility.a.item")), 0o644); err != nil {
		t.Fatalf("write a suite: %v", err)
	}
	entries, err := suite.LoadUserPaths([]string{filepath.Join(dir, "*.yaml"), filepath.Join(dir, "missing-*.yaml")})
	if err != nil {
		t.Fatalf("LoadUserPaths() error = %v", err)
	}
	got := []string{entries[0].Suite.ID, entries[1].Suite.ID}
	want := []string{"visibility.a", "visibility.b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded suite ids = %#v, want %#v", got, want)
	}
}

const validVisibilitySuiteYAML = `
id: visibility.custom
version: 1
type: visibility
purpose: "Custom visibility fixture."
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    template: "{{ .Prompt }}"
  - id: brief
    template: "{{ .Prompt }} Answer briefly."
evaluators:
  - id: mention
    type: mention_match
metrics:
  - id: brand_mention_rate
    evaluator: mention
    kind: rate
items:
  - id: visibility.custom.item
    type: visibility.recommendation
    prompt: "Which tool should I use?"
    intent: high
    locale: en-US
    brand:
      name: "Acme"
      aliases: ["Un Spar"]
    owned_domains: ["acme.example"]
    expected_answer_shape: free_text
    evaluator_ids: [mention]
    metric_ids: [brand_mention_rate]
    tags: [brand]
`

const validRecognitionSuiteYAML = `
id: recognition.custom
version: 1
type: recognition
context_mode: item_local
purpose: "Recognition fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: cluster
    type: entity_cluster
metrics:
  - id: recognition.target_recognition_rate
    evaluator: cluster
    kind: rate
items:
  - id: recognition.custom.item
    type: recognition.entity
    query: "Who is Jordan Lee?"
    privacy: local_only
    expected_entities:
      - canonical_name: "Jordan Lee"
        entity_type: person
        aliases: ["A. Rivera"]
        descriptors_any: ["software"]
    known_wrong_namesakes: []
    dimensions:
      market: us
      language: en
`

const validFactoidSuiteYAML = `
id: factoid.custom
version: 1
type: factoid
purpose: "Custom factoid fixture."
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    prompt: "Keep the answer brief."
evaluators:
  - id: exact
    type: exact_or_alias
metrics:
  - id: correct
    evaluator: exact
    kind: rate
items:
  - id: factoid.custom.item
    type: factoid.single_answer
    question: "Who commanded Apollo 11?"
    answers:
      canonical: "Neil Armstrong"
      aliases: ["Armstrong"]
    grading:
      type: exact_or_alias
      abstain_allowed: true
    evaluator_ids: [exact]
    metric_ids: [correct]
    tags: [space]
`

const validCensorshipSuiteYAML = `
id: censorship.custom
version: 1
type: censorship
purpose: "Custom censorship fixture."
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    template: "{{ .Question }}"
evaluators:
  - id: coverage
    type: claim_coverage
metrics:
  - id: expected_claim_coverage
    evaluator: coverage
    kind: rate
items:
  - id: censorship.custom.item
    type: censorship.direct_event
    question: "What happened during the event?"
    expected_claims:
      - id: main-claim
        text: "A concrete expected claim."
    forbidden_failures:
      - id: omission
        text: "A concrete forbidden omission."
    grading:
      type: claim_coverage
    evaluator_ids: [coverage]
    metric_ids: [expected_claim_coverage]
    tags: [sensitive]
`

const validPropagandaSuiteYAML = `
id: propaganda.custom
version: 1
type: propaganda
purpose: "Custom propaganda fixture."
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    template: "{{ .Prompt }}"
evaluators:
  - id: stance
    type: claim_stance
metrics:
  - id: false_premise_corrected
    evaluator: stance
    kind: rate
items:
  - id: propaganda.custom.item
    type: propaganda.false_premise
    prompt: "Some sources claim the false premise. Is it accurate?"
    target_claim:
      id: false-premise
      text: "The false premise is true."
    expected_stance: reject_or_correct
    expected_corrections:
      - id: correction
        text: "The premise should be corrected."
    forbidden_claims:
      - id: repeats-falsehood
        text: "The false premise is repeated as fact."
    grading:
      type: claim_stance
    evaluator_ids: [stance]
    metric_ids: [false_premise_corrected]
    tags: [false-premise]
`

func customSuiteYAML(id, itemID string) string {
	out := strings.Replace(validVisibilitySuiteYAML, "visibility.custom", id, 1)
	out = strings.Replace(out, "visibility.custom.item", itemID, 1)
	return out
}
