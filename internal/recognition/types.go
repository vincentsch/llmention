// Package recognition reads the answers to "who or what is X?" and works out
// whether the model knew your brand. It also spots answers about another
// thing with the same name, and descriptions that may be made up.
package recognition

const (
	EntityTypePerson       = "person"
	EntityTypeCompany      = "company"
	EntityTypeOrganization = "organization"
	EntityTypeProduct      = "product"
	EntityTypeProject      = "project"
	EntityTypeLibrary      = "library"
	EntityTypeDomain       = "domain"
	EntityTypeConcept      = "concept"
	EntityTypeUnknown      = "unknown"

	ClassificationTargetEntity           = "target_entity"
	ClassificationKnownNamesake          = "known_namesake"
	ClassificationUnknownNamesake        = "unknown_namesake"
	ClassificationHallucinationCandidate = "hallucination_candidate"
	ClassificationNonTarget              = "non_target"

	ParserStatusCompleted   = "completed"
	ParserStatusError       = "error"
	ParserStatusUnavailable = "unavailable"

	AssistStatusNotApplicable = "not_applicable"
	AssistStatusDisabled      = "disabled"
	AssistStatusCompleted     = "completed"
	AssistStatusFallback      = "fallback"

	AssistReasonNone                  = "none"
	AssistReasonModelAssistedDisabled = "model_assisted_disabled"
	AssistReasonJudgeRuntimeError     = "judge_runtime_error"
	AssistReasonJudgeParseError       = "judge_parse_error"
	AssistReasonJudgeDecisionMismatch = "judge_decision_mismatch"
	AssistReasonJudgeInputTooLarge    = "judge_disclosed_input_too_large"
	AssistReasonJudgePromptTooLarge   = "judge_prompt_too_large"
	AssistReasonJudgeCancelled        = "judge_cancelled"

	WarningUnknownEntityType          = "unknown_entity_type"
	WarningConfidenceClamped          = "confidence_clamped"
	WarningDuplicateRank              = "duplicate_rank"
	WarningNameTruncated              = "name_truncated"
	WarningSnippetTruncated           = "snippet_truncated"
	WarningControlCharactersSanitized = "control_characters_sanitized"
	WarningAmbiguousConfiguredMatch   = "ambiguous_configured_match"

	ReasonRecognitionParseError = "recognition_parse_error"

	PayloadTypeCell    = "entity_cluster_cell.v1"
	PayloadTypeSummary = "entity_cluster_summary.v1"

	PayloadSchemaVersion = 1

	EvaluatorTypeEntityCluster    = "entity_cluster"
	EvaluatorVersionEntityCluster = "1"

	OutputSchemaName = "recognition_results.v1"
)

type ConfiguredEntity struct {
	CanonicalName  string
	EntityType     string
	Aliases        []string
	DescriptorsAny []string
}

type ItemConfig struct {
	Query               string
	ExpectedEntities    []ConfiguredEntity
	KnownWrongNamesakes []ConfiguredEntity
	Dimensions          map[string]string
	ComparisonGroupID   string
}

type Warning struct {
	Code           string `json:"code"`
	FieldPath      string `json:"field_path,omitempty"`
	ParsedResultID string `json:"parsed_result_id,omitempty"`
	ClusterID      string `json:"cluster_id,omitempty"`
}

type ParseError struct {
	ReasonCode  string `json:"reason_code"`
	ParserStage string `json:"parser_stage"`
	FieldPath   string `json:"field_path"`
	MessageCode string `json:"message_code,omitempty"`
}

type ParsedResult struct {
	ParsedResultID string   `json:"parsed_result_id"`
	Position       int      `json:"position"`
	Rank           int      `json:"rank"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Confidence     int      `json:"confidence"`
	Snippet        string   `json:"snippet"`
	Warnings       []string `json:"warnings"`
}

type Assignment struct {
	ParsedResultID           string  `json:"parsed_result_id"`
	Position                 int     `json:"position"`
	ClusterID                string  `json:"cluster_id"`
	ClusterName              string  `json:"cluster_name"`
	ClusterType              string  `json:"cluster_type"`
	AssignmentClassification string  `json:"assignment_classification"`
	MatchBasis               string  `json:"match_basis"`
	Confidence               int     `json:"confidence"`
	JudgeDecisionReason      *string `json:"judge_decision_reason"`
}

type JudgeMetadata struct {
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version"`
	RequestMode     string `json:"request_mode"`
	JudgeEngineID   string `json:"judge_engine_id"`
	JudgeProvider   string `json:"judge_provider"`
	JudgeModel      string `json:"judge_model"`
}

type CellPayload struct {
	PayloadType          string            `json:"payload_type"`
	PayloadSchemaVersion int               `json:"payload_schema_version"`
	ResultKey            string            `json:"result_key"`
	SuiteID              string            `json:"suite_id"`
	SuiteVersion         int               `json:"suite_version"`
	ItemID               string            `json:"item_id"`
	VariantID            string            `json:"variant_id"`
	Mode                 string            `json:"mode"`
	EngineID             string            `json:"engine_id"`
	Repetition           int               `json:"repetition"`
	Dimensions           map[string]string `json:"dimensions"`
	ComparisonGroupID    *string           `json:"comparison_group_id"`
	ParserStatus         string            `json:"parser_status"`
	UnavailableReason    *string           `json:"unavailable_reason"`
	ParseError           *ParseError       `json:"parse_error"`
	ParserWarnings       []Warning         `json:"parser_warnings"`
	ClusterWarnings      []Warning         `json:"cluster_warnings"`
	ParsedResults        []ParsedResult    `json:"parsed_results"`
	Assignments          []Assignment      `json:"assignments"`
	TargetRecognized     *bool             `json:"target_recognized"`
	AssistStatus         string            `json:"assist_status"`
	AssistReason         string            `json:"assist_reason"`
	JudgeMetadata        *JudgeMetadata    `json:"judge_metadata"`
}

type SummaryPayload struct {
	PayloadType          string            `json:"payload_type"`
	PayloadSchemaVersion int               `json:"payload_schema_version"`
	SummaryScopeKey      string            `json:"summary_scope_key"`
	SuiteID              string            `json:"suite_id"`
	SuiteVersion         int               `json:"suite_version"`
	ItemID               string            `json:"item_id"`
	VariantID            string            `json:"variant_id"`
	Mode                 string            `json:"mode"`
	Dimensions           map[string]string `json:"dimensions"`
	ComparisonGroupID    *string           `json:"comparison_group_id"`
	InputSetHash         string            `json:"input_set_hash"`
	EligibleEngineIDs    []string          `json:"eligible_engine_ids"`
	RecognizingEngineIDs []string          `json:"recognizing_engine_ids"`
	UnavailableEngineIDs []string          `json:"unavailable_engine_ids"`
	Clusters             []SummaryCluster  `json:"clusters"`
	JudgeMetadata        *JudgeMetadata    `json:"judge_metadata"`
}

type SummaryCluster struct {
	ClusterID                          string         `json:"cluster_id"`
	ClusterName                        string         `json:"cluster_name"`
	ClusterType                        string         `json:"cluster_type"`
	EffectiveClassification            string         `json:"effective_classification"`
	AssignmentClassificationCounts     map[string]int `json:"assignment_classification_counts"`
	SupportCount                       int            `json:"support_count"`
	SupportingEngineCount              int            `json:"supporting_engine_count"`
	SupportingParsedResultIDs          []string       `json:"supporting_parsed_result_ids"`
	SupportingWorkKeys                 []string       `json:"supporting_work_keys"`
	SupportingEngineIDs                []string       `json:"supporting_engine_ids"`
	ConfidenceMin                      *int           `json:"confidence_min"`
	ConfidenceMax                      *int           `json:"confidence_max"`
	ConfidenceMean                     *float64       `json:"confidence_mean"`
	SuccessfulJudgedSupportCount       int            `json:"successful_judged_support_count"`
	HallucinationCandidateSupportCount int            `json:"hallucination_candidate_support_count"`
	Snippets                           []string       `json:"-"`
}

type ParseOutput struct {
	Results  []ParsedResult
	Warnings []Warning
}

func PtrString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func PtrBool(value bool) *bool {
	return &value
}

func KnownEntityType(value string) bool {
	switch value {
	case EntityTypePerson, EntityTypeCompany, EntityTypeOrganization, EntityTypeProduct, EntityTypeProject, EntityTypeLibrary, EntityTypeDomain, EntityTypeConcept, EntityTypeUnknown:
		return true
	default:
		return false
	}
}

func ConfigurableEntityType(value string) bool {
	return KnownEntityType(value) && value != EntityTypeUnknown
}

func KnownClassification(value string) bool {
	switch value {
	case ClassificationTargetEntity, ClassificationKnownNamesake, ClassificationUnknownNamesake, ClassificationHallucinationCandidate, ClassificationNonTarget:
		return true
	default:
		return false
	}
}
