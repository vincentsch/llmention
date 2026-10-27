// Package benchmark grades short factual answers against an answer key. The
// research suites with one right answer per question use it.
package benchmark

import "github.com/vincentsch/llmention/internal/engine"

const (
	EvaluatorTypeExactOrAlias    = "exact_or_alias"
	EvaluatorVersionExactOrAlias = "1"

	OutputSchemaName = "exact_answer.v1"
	PayloadTypeCell  = "exact_answer_cell.v1"

	PayloadSchemaVersion = 1

	SuiteFamilyFactoid     = "factoid"
	SuiteFamilyProgramming = "programming"
	SuiteFamilyTemporal    = "temporal"

	ParseStatusParsed            = "parsed"
	ParseStatusPlainTextFallback = "plain_text_fallback"
	ParseStatusError             = "error"
	ParseStatusUnavailable       = "unavailable"

	AnswerStatusCorrect      = "correct"
	AnswerStatusIncorrect    = "incorrect"
	AnswerStatusNotAttempted = "not_attempted"

	MatchKindCanonical           = "canonical"
	MatchKindAlias               = "alias"
	MatchKindConfiguredIncorrect = "configured_incorrect"
	MatchKindAbstention          = "abstention"
	MatchKindNone                = "none"

	ConfidencePolicyNone     = "none"
	ConfidencePolicyOptional = "optional"
	ConfidencePolicyRequired = "required"

	FailureClassIncorrect           = "incorrect"
	FailureClassOtherIncorrect      = "other_incorrect"
	FailureClassWrongVersion        = "wrong_version"
	FailureClassHallucinatedAPI     = "hallucinated_api"
	FailureClassDeprecatedAsCurrent = "deprecated_as_current"
	FailureClassOutdatedAnswer      = "outdated_answer"
	FailureClassWrongLibrary        = "wrong_library"
	FailureClassWrongErrorCause     = "wrong_error_cause"
	FailureClassStaleAnswer         = "stale_answer"

	WarningAmbiguousFailureMatch      = "ambiguous_failure_match"
	WarningAnswerTruncated            = "answer_truncated"
	WarningConfidenceClamped          = "confidence_clamped"
	WarningControlCharactersSanitized = "control_characters_sanitized"
	WarningInvalidKnowledgeCutoff     = "invalid_knowledge_cutoff"
	WarningInvalidSourceDateMetadata  = "invalid_source_date_metadata"
	WarningPlainTextFallback          = "plain_text_fallback"

	ReasonExactAnswerParseError = "exact_answer_parse_error"

	CutoffBefore  = "before_cutoff"
	CutoffAfter   = "after_cutoff"
	CutoffUnknown = "unknown_cutoff"

	ExpectedShouldKnow        = "should_know"
	ExpectedAbstainOrUnknown  = "abstain_or_unknown"
	ExpectedCorrectWithSource = "correct_with_source"
	ExpectedNotRequired       = "not_required"

	SourceStatusNotRequired = "not_required"
	SourceStatusMet         = "met"
	SourceStatusMissing     = "missing"
	SourceStatusUnavailable = "unavailable"

	FreshnessNotRequired = "not_required"
	FreshnessFresh       = "fresh"
	FreshnessStale       = "stale"
	FreshnessNoValidDate = "no_valid_date"
	FreshnessUnavailable = "unavailable"
)

type ConfiguredAnswer struct {
	ID      string
	Aliases []string
	Class   string
}

type MatchConfig struct {
	Canonical        string
	Aliases          []string
	KnownIncorrect   []ConfiguredAnswer
	FailureCases     []ConfiguredAnswer
	StaleAnswers     []ConfiguredAnswer
	ConfidencePolicy string
}

type ParsedAnswer struct {
	Answer                 *string
	Confidence             *int
	ParseStatus            string
	SanitizedAnswer        string
	SanitizedAnswerPreview string
	AnswerPreviewTruncated bool
	Warnings               []string
}

type MatchResult struct {
	AnswerStatus           string
	MatchKind              string
	MatchedConfiguredID    string
	MatchedConfiguredValue string
	FailureClass           string
	OverconfidentWrong     bool
	Warnings               []string
}

type CellPayload struct {
	PayloadType            string                  `json:"payload_type"`
	PayloadSchemaVersion   int                     `json:"payload_schema_version"`
	SuiteFamily            string                  `json:"suite_family"`
	ParseStatus            string                  `json:"parse_status"`
	AnswerStatus           *string                 `json:"answer_status"`
	SanitizedAnswerPreview string                  `json:"sanitized_answer_preview"`
	AnswerPreviewTruncated bool                    `json:"answer_preview_truncated"`
	MatchKind              string                  `json:"match_kind"`
	MatchedConfiguredID    string                  `json:"matched_configured_id"`
	MatchedConfiguredValue string                  `json:"matched_configured_value"`
	FailureClass           string                  `json:"failure_class"`
	Confidence             *int                    `json:"confidence"`
	OverconfidentWrong     bool                    `json:"overconfident_wrong"`
	Warnings               []string                `json:"warnings"`
	Programming            *ProgrammingExtensionV1 `json:"programming"`
	Temporal               *TemporalExtensionV1    `json:"temporal"`
}

type ProgrammingExtensionV1 struct {
	Subtype string           `json:"subtype"`
	Stack   ProgrammingStack `json:"stack"`
}

type ProgrammingStack struct {
	Language  string `json:"language"`
	Ecosystem string `json:"ecosystem"`
	Library   string `json:"library"`
	API       string `json:"api"`
}

type TemporalExtensionV1 struct {
	TemporalType                 string   `json:"temporal_type"`
	EventDate                    string   `json:"event_date"`
	RawKnowledgeCutoff           string   `json:"raw_knowledge_cutoff"`
	CutoffLabel                  string   `json:"cutoff_label"`
	ExpectedModeBehavior         string   `json:"expected_mode_behavior"`
	CutoffHonest                 *bool    `json:"cutoff_honest"`
	StaleAnswerID                string   `json:"stale_answer_id"`
	SourceRequirementStatus      string   `json:"source_requirement_status"`
	SourceFreshnessStatus        string   `json:"source_freshness_status"`
	ValidDatedSourceCount        int      `json:"valid_dated_source_count"`
	InvalidDatedSourceCount      int      `json:"invalid_dated_source_count"`
	FreshestNormalizedSourceDate string   `json:"freshest_normalized_source_date"`
	DatedSourceChannels          []string `json:"dated_source_channels"`
}

type SourceExpectation struct {
	RequiredForModes       []string
	FreshnessReferenceDate string
}

type SourceEvidenceStatus struct {
	RequirementStatus            string
	FreshnessStatus              string
	ValidDatedSourceCount        int
	InvalidDatedSourceCount      int
	FreshestNormalizedSourceDate string
	DatedSourceChannels          []string
	Warnings                     []string
}

func OutputSchemaFields() (name, format string, strict bool, schemaJSON []byte) {
	return OutputSchemaName, engine.OutputFormatJSONSchema, false, CanonicalSchemaJSON()
}
