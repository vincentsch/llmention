package recognition

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAnswerAcceptsEmptyResults(t *testing.T) {
	parsed, err := ParseAnswer(`{"results":[]}`, "suite/1/item/default/memory/mock/1")
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v", err)
	}
	if len(parsed.Results) != 0 || len(parsed.Warnings) != 0 {
		t.Fatalf("parsed empty results = %#v, want no rows or warnings", parsed)
	}
}

func TestParseAnswerSanitizesAndWarns(t *testing.T) {
	workKey := "suite/1/item/default/memory/mock/1"
	parsed, err := ParseAnswer(`{"results":[{"rank":1,"name":"Acme\u0000Tool","type":"robot","confidence":120,"snippet":"line\u0000one"},{"rank":1,"name":"Known","type":"person","confidence":-5,"snippet":"ok"}]}`, workKey)
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v", err)
	}
	if got, want := len(parsed.Results), 2; got != want {
		t.Fatalf("result count = %d, want %d", got, want)
	}
	if parsed.Results[0].ParsedResultID != StableResultID(workKey, 0) || parsed.Results[1].ParsedResultID != StableResultID(workKey, 1) {
		t.Fatalf("stable parsed ids = %#v", parsed.Results)
	}
	if parsed.Results[0].Name != "Acme Tool" || parsed.Results[0].Type != EntityTypeUnknown || parsed.Results[0].Confidence != 100 {
		t.Fatalf("first result not sanitized/clamped: %#v", parsed.Results[0])
	}
	if parsed.Results[1].Confidence != 0 {
		t.Fatalf("second confidence = %d, want 0", parsed.Results[1].Confidence)
	}
	wantCodes := []string{
		WarningConfidenceClamped,
		WarningConfidenceClamped,
		WarningControlCharactersSanitized,
		WarningControlCharactersSanitized,
		WarningDuplicateRank,
		WarningUnknownEntityType,
	}
	if got := warningCodes(parsed.Warnings); !reflect.DeepEqual(got, wantCodes) {
		t.Fatalf("warning codes = %#v, want %#v; warnings=%#v", got, wantCodes, parsed.Warnings)
	}
	if got := parsed.Results[1].Warnings; len(got) != 2 || got[0] != WarningConfidenceClamped || got[1] != WarningDuplicateRank {
		t.Fatalf("second result warnings = %#v", got)
	}
}

func TestParseAnswerRejectsInvalidJSONShapes(t *testing.T) {
	tests := []struct {
		name string
		text string
		code string
	}{
		{name: "markdown fence", text: "```json\n{\"results\":[]}\n```", code: "invalid_json"},
		{name: "trailing token", text: `{"results":[]} []`, code: "trailing_tokens"},
		{name: "duplicate key", text: `{"results":[],"results":[]}`, code: "duplicate_key"},
		{name: "unknown root field", text: `{"results":[],"extra":true}`, code: "unknown_field"},
		{name: "missing results", text: `{}`, code: "missing_field"},
		{name: "fractional confidence", text: `{"results":[{"rank":1,"name":"Acme","type":"person","confidence":99.5,"snippet":"ok"}]}`, code: "integer_required"},
		{name: "string confidence", text: `{"results":[{"rank":1,"name":"Acme","type":"person","confidence":"99","snippet":"ok"}]}`, code: "number_required"},
		{name: "row unknown field", text: `{"results":[{"rank":1,"name":"Acme","type":"person","confidence":99,"snippet":"ok","extra":true}]}`, code: "unknown_field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAnswer(tt.text, "suite/1/item/default/memory/mock/1")
			if err == nil {
				t.Fatal("ParseAnswer() succeeded, want error")
			}
			safe := SafeParseError(err)
			if safe.MessageCode != tt.code {
				t.Fatalf("message code = %q, want %q; safe=%#v err=%v", safe.MessageCode, tt.code, safe, err)
			}
			if safe.ReasonCode != ReasonRecognitionParseError || safe.ParserStage == "" || safe.FieldPath == "" {
				t.Fatalf("safe parse error missing required fields: %#v", safe)
			}
		})
	}
}

func TestClusterResultsClassifiesConfiguredAndUnknownEntities(t *testing.T) {
	config := ItemConfig{
		ExpectedEntities: []ConfiguredEntity{{
			CanonicalName:  "Jordan Lee",
			EntityType:     EntityTypePerson,
			Aliases:        []string{"A. Rivera"},
			DescriptorsAny: []string{"software"},
		}},
		KnownWrongNamesakes: []ConfiguredEntity{{
			CanonicalName:  "Jordan Lee footballer",
			EntityType:     EntityTypePerson,
			Aliases:        []string{"Jordan Lee"},
			DescriptorsAny: []string{"footballer"},
		}},
	}
	results := []ParsedResult{
		{ParsedResultID: "parsed-target", Position: 0, Rank: 1, Name: "Jordan Lee", Type: EntityTypePerson, Confidence: 90, Snippet: "Synthetic software fixture."},
		{ParsedResultID: "parsed-known", Position: 1, Rank: 2, Name: "Jordan Lee", Type: EntityTypePerson, Confidence: 80, Snippet: "Footballer and striker namesake."},
		{ParsedResultID: "parsed-domain", Position: 2, Rank: 3, Name: "alexrivera.example", Type: EntityTypeDomain, Confidence: 40, Snippet: "A domain."},
		{ParsedResultID: "parsed-unknown", Position: 3, Rank: 4, Name: "Jordan Lee", Type: EntityTypePerson, Confidence: 30, Snippet: "Another person."},
	}

	clustered := ClusterResults(config, results)
	got := map[string]string{}
	for _, assignment := range clustered.Assignments {
		got[assignment.ParsedResultID] = assignment.AssignmentClassification
	}
	want := map[string]string{
		"parsed-target":  ClassificationTargetEntity,
		"parsed-known":   ClassificationKnownNamesake,
		"parsed-domain":  ClassificationNonTarget,
		"parsed-unknown": ClassificationUnknownNamesake,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("classifications = %#v, want %#v; assignments=%#v", got, want, clustered.Assignments)
	}
	if !TargetRecognized(clustered.Assignments) {
		t.Fatalf("TargetRecognized() = false, want true")
	}
	unresolved := UnresolvedAssignments(clustered.Assignments)
	if len(unresolved) != 1 || unresolved[0].ParsedResultID != "parsed-unknown" {
		t.Fatalf("unresolved assignments = %#v", unresolved)
	}
}

func TestBuildSummaryAggregatesCompletedCellsAndUnavailableEngines(t *testing.T) {
	workKey := "suite/1/item/default/memory/engine-a/1"
	result := ParsedResult{ParsedResultID: StableResultID(workKey, 0), Position: 0, Rank: 1, Name: "Acme", Type: EntityTypeProduct, Confidence: 86, Snippet: "AI writing product."}
	assignment := Assignment{
		ParsedResultID:           result.ParsedResultID,
		Position:                 0,
		ClusterID:                StableClusterID("Acme", EntityTypeProduct),
		ClusterName:              "Acme",
		ClusterType:              EntityTypeProduct,
		AssignmentClassification: ClassificationTargetEntity,
		MatchBasis:               "canonical",
		Confidence:               86,
	}
	summary := BuildSummary(SummaryInput{
		SuiteID:           "suite",
		SuiteVersion:      1,
		ItemID:            "item",
		VariantID:         "default",
		Mode:              "memory",
		PlannedWorkKeys:   []string{workKey, "suite/1/item/default/memory/engine-b/1"},
		SelectedEngineIDs: []string{"engine-a", "engine-b", "engine-c"},
		Cells: []CellPayload{
			{
				ResultKey:        workKey,
				EngineID:         "engine-a",
				ParserStatus:     ParserStatusCompleted,
				ParsedResults:    []ParsedResult{result},
				Assignments:      []Assignment{assignment},
				TargetRecognized: PtrBool(true),
			},
			{
				ResultKey:         "suite/1/item/default/memory/engine-b/1",
				EngineID:          "engine-b",
				ParserStatus:      ParserStatusUnavailable,
				UnavailableReason: PtrString("provider_error"),
			},
		},
	})
	if got, want := summary.EligibleEngineIDs, []string{"engine-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("eligible engines = %#v, want %#v", got, want)
	}
	if got, want := summary.RecognizingEngineIDs, []string{"engine-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recognizing engines = %#v, want %#v", got, want)
	}
	if got, want := summary.UnavailableEngineIDs, []string{"engine-b", "engine-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unavailable engines = %#v, want %#v", got, want)
	}
	if len(summary.Clusters) != 1 || summary.Clusters[0].EffectiveClassification != ClassificationTargetEntity || summary.Clusters[0].SupportCount != 1 {
		t.Fatalf("summary clusters = %#v", summary.Clusters)
	}
	if summary.InputSetHash == "" || !strings.HasPrefix(summary.InputSetHash, "sha256:") {
		t.Fatalf("input set hash = %q", summary.InputSetHash)
	}
}

func warningCodes(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, warning.Code)
	}
	return out
}
