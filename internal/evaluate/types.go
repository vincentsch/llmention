// Package evaluate scores saved answers with the checks a suite declares,
// such as brand mentions or cited domains, and builds the metric table from
// them. A configured AI grader can add checks that need judgment.
package evaluate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

const (
	EvaluatorMentionMatch     = "mention_match"
	EvaluatorSourceDomain     = "source_domain"
	EvaluatorStability        = "stability"
	EvaluatorFramingSentiment = "framing_sentiment"
	EvaluatorEntityCluster    = "entity_cluster"
	EvaluatorExactOrAlias     = "exact_or_alias"
	EvaluatorClaimCoverage    = "claim_coverage"
	EvaluatorClaimStance      = "claim_stance"

	EvaluatorVersionMentionMatch     = "1"
	EvaluatorVersionSourceDomain     = "1"
	EvaluatorVersionStability        = "1"
	EvaluatorVersionFramingSentiment = "1"
	EvaluatorVersionEntityCluster    = "1"
	EvaluatorVersionExactOrAlias     = "1"
	EvaluatorVersionClaimCoverage    = "1"
	EvaluatorVersionClaimStance      = "1"

	EvaluationStatusCompleted EvaluationStatus = "completed"
	EvaluationStatusSkipped   EvaluationStatus = "skipped"
	EvaluationStatusError     EvaluationStatus = "error"

	MetricStatusAvailable   MetricStatus = "available"
	MetricStatusUnavailable MetricStatus = "unavailable"

	ReadinessFatal   = "fatal"
	ReadinessWarning = "warning"

	ReasonProviderError            = "provider_error"
	ReasonCancelled                = "cancelled"
	ReasonNotStarted               = "not_started"
	ReasonModelAssistedDisabled    = "model_assisted_disabled"
	ReasonEvaluatorNotApplicable   = "evaluator_not_applicable"
	ReasonEvaluatorError           = "evaluator_error"
	ReasonEvaluatorSkipped         = "evaluator_skipped"
	ReasonSourceChannelUnavailable = "source_channel_unavailable"
	ReasonMissingAnswer            = "missing_answer"
	ReasonUnfinishedAnswer         = "unfinished_answer"
	ReasonInsufficientObservations = "insufficient_observations"
	ReasonJudgeError               = "judge_error"
	ReasonJudgeParseError          = "judge_parse_error"
	ReasonJudgeDecisionMismatch    = "judge_decision_mismatch"
	ReasonJudgeInputTooLarge       = "judge_input_too_large"
	ReasonDisclosedInputTooLarge   = "disclosed_input_too_large"
	ReasonJudgePromptTooLarge      = "judge_prompt_too_large"
	ReasonRecognitionParseError    = "recognition_parse_error"
	ReasonExactAnswerParseError    = "exact_answer_parse_error"
)

type EvaluationStatus string

type MetricStatus string

type PrepareInput struct {
	Project            config.Project             `json:"project"`
	Suites             []suite.LoadedSuite        `json:"-"`
	BoundSuites        []SuiteSnapshot            `json:"bound_suites"`
	Plan               run.Plan                   `json:"plan"`
	EngineCapabilities []EngineCapabilitySnapshot `json:"engine_capabilities"`
	ModelAssisted      ModelAssistedPlanInput     `json:"model_assisted"`
	PrecomputedIssues  []ReadinessIssue           `json:"precomputed_issues,omitempty"`
}

type ModelAssistedPlanInput struct {
	Enabled                bool                      `json:"enabled"`
	EnabledSource          string                    `json:"enabled_source,omitempty"`
	Judge                  *config.JudgeConfig       `json:"-"`
	JudgeCapability        *engine.CapabilityProfile `json:"judge_capability,omitempty"`
	MaxJudgeRequests       int                       `json:"max_judge_requests"`
	MaxJudgeRequestsSource string                    `json:"max_judge_requests_source,omitempty"`
}

type PreparedEvaluation struct {
	ID                    string                     `json:"id"`
	BoundSuites           []SuiteSnapshot            `json:"bound_suites"`
	EvaluatorDeclarations []ResolvedEvaluator        `json:"evaluator_declarations"`
	MetricPlans           []MetricPlan               `json:"metric_plans"`
	EngineCapabilities    []EngineCapabilitySnapshot `json:"engine_capabilities"`
	ResultPlans           []ResultPlan               `json:"result_plans"`
	ReadinessIssues       []ReadinessIssue           `json:"readiness_issues"`
	EvaluationReady       bool                       `json:"evaluation_ready"`
	ModelAssisted         ModelAssistedPlan          `json:"model_assisted"`

	suitesByKey        map[string]*suite.Suite
	itemsByWorkKey     map[string]suite.Item
	resultPlansByKey   map[string]ResultPlan
	evaluatorByKey     map[string]ResolvedEvaluator
	metricPlanByKey    map[string]MetricPlan
	metricDeclByKey    map[string]suite.MetricDeclaration
	metricEvaluatorKey map[string]string

	recognitionSummaryPlans []RecognitionSummaryPlan
}

type ModelAssistedPlan struct {
	Enabled                  bool     `json:"enabled"`
	EnabledSource            string   `json:"enabled_source,omitempty"`
	PlannedCandidates        int      `json:"planned_candidates"`
	RetryAttemptsPerWorkUnit int      `json:"retry_attempts_per_work_unit"`
	RequestUpperBound        int      `json:"request_upper_bound"`
	MaxJudgeRequests         int      `json:"max_judge_requests"`
	MaxJudgeRequestsSource   string   `json:"max_judge_requests_source,omitempty"`
	JudgeConfigured          bool     `json:"judge_configured"`
	JudgeEngineID            string   `json:"judge_engine_id,omitempty"`
	JudgeProvider            string   `json:"judge_provider,omitempty"`
	JudgeModel               string   `json:"judge_model,omitempty"`
	JudgeKeyEnv              string   `json:"judge_key_env,omitempty"`
	AllowAnswerDisclosure    bool     `json:"allow_answer_disclosure"`
	JudgeReady               bool     `json:"judge_ready"`
	CandidateEvaluatorIDs    []string `json:"candidate_evaluator_ids,omitempty"`
}

type SuiteSnapshot struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Type        string `json:"type"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	ContextMode string `json:"context_mode"`
	Hash        string `json:"hash"`
}

type EngineCapabilitySnapshot struct {
	EngineID     string                   `json:"engine_id"`
	Provider     string                   `json:"provider"`
	Model        string                   `json:"model"`
	Capabilities engine.CapabilityProfile `json:"capabilities"`
}

type ResolvedEvaluator struct {
	SuiteID            string   `json:"suite_id"`
	SuiteVersion       int      `json:"suite_version"`
	DeclarationID      string   `json:"declaration_id"`
	EvaluatorType      string   `json:"evaluator_type"`
	EvaluatorVersion   string   `json:"evaluator_version"`
	ModelAssisted      bool     `json:"model_assisted"`
	SupportedMetricIDs []string `json:"supported_metric_ids"`
}

type MetricPlan struct {
	SuiteID        string   `json:"suite_id"`
	SuiteVersion   int      `json:"suite_version"`
	MetricID       string   `json:"metric_id"`
	DeclarationID  string   `json:"declaration_id"`
	Kind           string   `json:"kind"`
	ScopeLevels    []string `json:"scope_levels"`
	RequiredInputs []string `json:"required_inputs"`
}

type ResultPlan struct {
	WorkKey           string            `json:"work_key"`
	SuiteID           string            `json:"suite_id"`
	SuiteVersion      int               `json:"suite_version"`
	ItemID            string            `json:"item_id"`
	VariantID         string            `json:"variant_id"`
	Dimensions        map[string]string `json:"dimensions"`
	ComparisonGroupID string            `json:"comparison_group_id,omitempty"`
	Mode              string            `json:"mode"`
	EngineID          string            `json:"engine_id"`
	Repetition        int               `json:"repetition"`
	EvaluatorIDs      []string          `json:"evaluator_ids"`
	MetricIDs         []string          `json:"metric_ids"`
}

type RecognitionSummaryPlan struct {
	SuiteID           string
	SuiteVersion      int
	ItemID            string
	VariantID         string
	Mode              string
	Dimensions        map[string]string
	ComparisonGroupID string
	SelectedEngineIDs []string
	PlannedWorkKeys   []string
	MetricIDs         []string
	Tags              []string
}

type ReadinessIssue struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Severity      string `json:"severity"`
	SuiteID       string `json:"suite_id,omitempty"`
	SuiteVersion  int    `json:"suite_version,omitempty"`
	ItemID        string `json:"item_id,omitempty"`
	EvaluatorType string `json:"evaluator_type,omitempty"`
	MetricID      string `json:"metric_id,omitempty"`
	Message       string `json:"message"`
}

type EvaluationInput struct {
	Context            context.Context                            `json:"-"`
	Prepared           PreparedEvaluation                         `json:"prepared"`
	RawRun             run.RawSuiteRun                            `json:"raw_run"`
	JudgeEngine        engine.Engine                              `json:"-"`
	MaxConcurrency     int                                        `json:"max_concurrency,omitempty"`
	Now                func() time.Time                           `json:"-"`
	Sleep              func(context.Context, time.Duration) error `json:"-"`
	DisclosureRedactor func(string) string                        `json:"-"`
}

// EvaluatedRun holds the scores of one run: one evaluation per answer and the
// metrics built from them.
type EvaluatedRun struct {
	RunID                string                     `json:"run_id"`
	Results              []EvaluatedResult          `json:"results"`
	EvaluationID         string                     `json:"evaluation_id"`
	PreparedEvaluationID string                     `json:"prepared_evaluation_id"`
	Evaluations          []EvaluationRecord         `json:"evaluations"`
	EvaluationCounts     []EvaluationCount          `json:"evaluation_counts"`
	Metrics              []MetricValue              `json:"metrics"`
	SuiteSnapshots       []SuiteSnapshot            `json:"suite_snapshots"`
	EngineCapabilities   []EngineCapabilitySnapshot `json:"engine_capabilities"`
}

type EvaluatedResult struct {
	ResultID      string     `json:"result_id"`
	WorkKey       string     `json:"work_key"`
	Raw           run.Result `json:"raw"`
	EvaluationIDs []string   `json:"evaluation_ids"`
}

type EvaluationRecord struct {
	ID                string                  `json:"id"`
	SuiteID           string                  `json:"suite_id,omitempty"`
	SuiteVersion      int                     `json:"suite_version,omitempty"`
	ResultID          string                  `json:"result_id,omitempty"`
	ScopeKey          string                  `json:"scope_key,omitempty"`
	Dimensions        map[string]string       `json:"dimensions"`
	ComparisonGroupID string                  `json:"comparison_group_id,omitempty"`
	DeclarationID     string                  `json:"declaration_id"`
	EvaluatorType     string                  `json:"evaluator_type"`
	EvaluatorVersion  string                  `json:"evaluator_version"`
	Status            EvaluationStatus        `json:"status"`
	ReasonCode        string                  `json:"reason_code,omitempty"`
	PayloadType       string                  `json:"payload_type,omitempty"`
	Payload           any                     `json:"payload,omitempty"`
	Values            map[string]MetricScalar `json:"values,omitempty"`
	Evidence          []EvidenceRecord        `json:"evidence,omitempty"`
	Warnings          []EvaluationWarning     `json:"warnings,omitempty"`
	Error             *EvaluationError        `json:"error,omitempty"`
	ModelAssisted     bool                    `json:"model_assisted"`
	JudgeRecord       *JudgeRecord            `json:"judge_record,omitempty"`
}

type EvaluationCount struct {
	DeclarationID    string           `json:"declaration_id"`
	EvaluatorType    string           `json:"evaluator_type"`
	EvaluatorVersion string           `json:"evaluator_version"`
	Status           EvaluationStatus `json:"status"`
	ReasonCode       string           `json:"reason_code,omitempty"`
	Count            int              `json:"count"`
}

type EvidenceRecord struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	ByteStart       int    `json:"byte_start"`
	ByteEnd         int    `json:"byte_end"`
	Quote           string `json:"quote"`
	Normalized      string `json:"normalized,omitempty"`
	NormalizedValue string `json:"normalized_value,omitempty"`
	EntityID        string `json:"entity_id,omitempty"`
	Domain          string `json:"domain,omitempty"`
	SourceChannel   string `json:"source_channel,omitempty"`
}

type MetricValue struct {
	ID                string       `json:"id"`
	MetricID          string       `json:"metric_id"`
	Scope             MetricScope  `json:"scope"`
	Kind              string       `json:"kind"`
	Status            MetricStatus `json:"status"`
	Value             MetricScalar `json:"value"`
	Numerator         *float64     `json:"numerator,omitempty"`
	Denominator       *float64     `json:"denominator,omitempty"`
	UnavailableReason string       `json:"unavailable_reason,omitempty"`
	Evaluator         EvaluatorRef `json:"evaluator"`
	EvidenceIDs       []string     `json:"evidence_ids,omitempty"`
}

type MetricScope struct {
	Level             string            `json:"level"`
	Key               string            `json:"key"`
	SuiteID           string            `json:"suite_id"`
	SuiteVersion      int               `json:"suite_version"`
	Mode              string            `json:"mode,omitempty"`
	ModeFrom          string            `json:"mode_from,omitempty"`
	ModeTo            string            `json:"mode_to,omitempty"`
	EngineID          string            `json:"engine_id,omitempty"`
	ItemID            string            `json:"item_id,omitempty"`
	VariantID         string            `json:"variant_id,omitempty"`
	Tag               string            `json:"tag,omitempty"`
	EntityType        string            `json:"entity_type,omitempty"`
	EntityID          string            `json:"entity_id,omitempty"`
	Domain            string            `json:"domain,omitempty"`
	SourceChannel     string            `json:"source_channel,omitempty"`
	Dimensions        map[string]string `json:"dimensions,omitempty"`
	ComparisonGroupID string            `json:"comparison_group_id,omitempty"`
}

type EvaluatorRef struct {
	DeclarationID    string `json:"declaration_id"`
	EvaluatorType    string `json:"evaluator_type"`
	EvaluatorVersion string `json:"evaluator_version"`
}

type EvaluationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EvaluationError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	SafeSnippet string `json:"safe_snippet,omitempty"`
}

type MetricScalar struct {
	Type    string   `json:"type"`
	Number  *float64 `json:"number,omitempty"`
	Integer *int64   `json:"integer,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
	String  *string  `json:"string,omitempty"`
}

type JudgeTask struct {
	ID              string              `json:"id"`
	EvaluationID    string              `json:"evaluation_id"`
	TemplateID      string              `json:"template_id"`
	TemplateVersion string              `json:"template_version"`
	RequestMode     string              `json:"request_mode"`
	DisclosedInput  json.RawMessage     `json:"disclosed_input"`
	OutputSchema    engine.OutputSchema `json:"output_schema"`
	RenderedPrompt  string              `json:"rendered_prompt"`
}

type JudgeTaskHandler interface {
	TemplateID() string
	TemplateVersion() string
	OutputSchema() engine.OutputSchema
	BuildDisclosedInput(EvaluationRecord, EvaluatedResult) (json.RawMessage, error)
	RenderPrompt(json.RawMessage) (string, error)
	ParseAndValidate(answerText string, disclosedInput json.RawMessage) (JudgeParsedOutput, []EvaluationWarning, error)
}

type JudgeResult struct {
	TaskID     string            `json:"task_id"`
	Record     JudgeRecord       `json:"record"`
	Status     EvaluationStatus  `json:"status"`
	ReasonCode string            `json:"reason_code,omitempty"`
	Parsed     JudgeParsedOutput `json:"parsed,omitempty"`
	Error      *EvaluationError  `json:"error,omitempty"`
}

type JudgeParsedOutput struct {
	ParsedObject     json.RawMessage         `json:"parsed_object"`
	NormalizedOutput map[string]MetricScalar `json:"normalized_output"`
	Evidence         []EvidenceRecord        `json:"evidence,omitempty"`
}

type JudgeRecord struct {
	TemplateID        string                  `json:"template_id"`
	TemplateVersion   string                  `json:"template_version"`
	RequestMode       string                  `json:"request_mode"`
	JudgeEngineID     string                  `json:"judge_engine_id"`
	JudgeProvider     string                  `json:"judge_provider"`
	JudgeModel        string                  `json:"judge_model"`
	DisclosedInput    json.RawMessage         `json:"disclosed_input"`
	Prompt            string                  `json:"prompt"`
	Attempts          int                     `json:"attempts"`
	RetryWaitMS       []int64                 `json:"retry_wait_ms,omitempty"`
	ElapsedMS         int64                   `json:"elapsed_ms"`
	ProviderRequestID string                  `json:"provider_request_id,omitempty"`
	RawAnswerText     string                  `json:"raw_answer_text"`
	ParsedObject      json.RawMessage         `json:"parsed_object,omitempty"`
	NormalizedOutput  map[string]MetricScalar `json:"normalized_output,omitempty"`
	Warnings          []EvaluationWarning     `json:"warnings,omitempty"`
	Error             *EvaluationError        `json:"error,omitempty"`
}

type MentionMatchPayload struct {
	Brand                     MentionEntityRow    `json:"brand"`
	Competitors               []MentionEntityRow  `json:"competitors"`
	OrderedMentionedEntityIDs []string            `json:"ordered_mentioned_entity_ids"`
	EvidenceIDsByEntityID     map[string][]string `json:"evidence_ids_by_entity_id"`
}

type MentionEntityRow struct {
	EntityID      string   `json:"entity_id"`
	Name          string   `json:"name"`
	EntityType    string   `json:"entity_type"`
	Mentioned     bool     `json:"mentioned"`
	NamedInPrompt bool     `json:"named_in_prompt"`
	Rank          *int     `json:"rank"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type SourceDomainPayload struct {
	ChannelAvailability []SourceChannelAvailability `json:"channel_availability"`
	Observations        []SourceObservation         `json:"observations"`
	Counts              []SourceDomainCount         `json:"counts"`
	PolicyFlags         []SourcePolicyFlag          `json:"policy_flags"`
	EvidenceIDs         []string                    `json:"evidence_ids"`
}

type SourceChannelAvailability struct {
	Channel           string `json:"channel"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type SourceObservation struct {
	Channel          string   `json:"channel"`
	Domain           string   `json:"domain"`
	Count            int      `json:"count"`
	EvidenceIDs      []string `json:"evidence_ids"`
	OwnedDomain      bool     `json:"owned_domain"`
	CompetitorDomain bool     `json:"competitor_domain"`
	PreferredDomain  bool     `json:"preferred_domain"`
	BlockedDomain    bool     `json:"blocked_domain"`
}

type SourceDomainCount struct {
	Channel string `json:"channel"`
	Domain  string `json:"domain"`
	Count   int    `json:"count"`
}

type SourcePolicyFlag struct {
	Domain           string `json:"domain"`
	OwnedDomain      bool   `json:"owned_domain"`
	CompetitorDomain bool   `json:"competitor_domain"`
	PreferredDomain  bool   `json:"preferred_domain"`
	BlockedDomain    bool   `json:"blocked_domain"`
}

type FramingSentimentPayload struct {
	ParseStatus     string   `json:"parse_status"`
	Label           string   `json:"label,omitempty"`
	Phrase          string   `json:"phrase,omitempty"`
	PhraseTruncated bool     `json:"phrase_truncated"`
	ReasonCode      string   `json:"reason_code,omitempty"`
	EvidenceIDs     []string `json:"evidence_ids,omitempty"`
}

type StabilityPayload struct {
	ComparedResultIDs   []string `json:"compared_result_ids"`
	ComparedVariants    []string `json:"compared_variants"`
	ComparedRepetitions []int    `json:"compared_repetitions"`
	Status              string   `json:"status"`
	EvidenceIDs         []string `json:"evidence_ids,omitempty"`
}

func NumberScalar(value float64) MetricScalar {
	return MetricScalar{Type: "number", Number: &value}
}

func IntegerScalar(value int64) MetricScalar {
	return MetricScalar{Type: "integer", Integer: &value}
}

func BooleanScalar(value bool) MetricScalar {
	return MetricScalar{Type: "boolean", Boolean: &value}
}

func StringScalar(value string) MetricScalar {
	return MetricScalar{Type: "string", String: &value}
}

func NullScalar() MetricScalar {
	return MetricScalar{Type: "null"}
}
