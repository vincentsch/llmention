// Package suite loads and checks suites: sets of prompts plus the checks and
// metrics that score their answers. Your prompts in llmention.yaml become the
// suite "prompts"; other suites come from YAML files.
package suite

type Source string

const (
	SourceBuiltIn Source = "built_in"
	SourceUser    Source = "user"
	// SourceProject marks the suite built from the prompts in llmention.yaml.
	SourceProject Source = "project"
)

const (
	ContextModeItemLocal    = "item_local"
	ContextModeProjectBound = "project_bound"
)

type Suite struct {
	ID           string                 `json:"id" yaml:"id"`
	Version      int                    `json:"version" yaml:"version"`
	Type         string                 `json:"type" yaml:"type"`
	Purpose      string                 `json:"purpose,omitempty" yaml:"purpose"`
	ContextMode  string                 `json:"context_mode" yaml:"context_mode"`
	DefaultModes []string               `json:"default_modes" yaml:"default_modes"`
	Repetitions  int                    `json:"repetitions" yaml:"repetitions"`
	Variants     []Variant              `json:"variants,omitempty" yaml:"variants"`
	Evaluators   []EvaluatorDeclaration `json:"evaluators" yaml:"evaluators"`
	Metrics      []MetricDeclaration    `json:"metrics" yaml:"metrics"`
	Items        []Item                 `json:"items" yaml:"items"`
}

type LoadedSuite struct {
	Suite  *Suite `json:"suite"`
	Source Source `json:"source"`
	Path   string `json:"path,omitempty"`
}

type Variant struct {
	ID          string `json:"id" yaml:"id"`
	Prompt      string `json:"prompt,omitempty" yaml:"prompt"`
	Template    string `json:"template,omitempty" yaml:"template"`
	Description string `json:"description,omitempty" yaml:"description"`
	Locale      string `json:"locale,omitempty" yaml:"locale"`
	Persona     string `json:"persona,omitempty" yaml:"persona"`
}

type EvaluatorDeclaration struct {
	ID   string `json:"id" yaml:"id"`
	Type string `json:"type" yaml:"type"`
}

type MetricDeclaration struct {
	ID        string `json:"id" yaml:"id"`
	Evaluator string `json:"evaluator" yaml:"evaluator"`
	Kind      string `json:"kind" yaml:"kind"`
}

type Item struct {
	ID                   string             `json:"id" yaml:"id"`
	Type                 string             `json:"type" yaml:"type"`
	Prompt               string             `json:"prompt,omitempty" yaml:"prompt"`
	Question             string             `json:"question,omitempty" yaml:"question"`
	Query                string             `json:"query,omitempty" yaml:"query"`
	Intent               string             `json:"intent,omitempty" yaml:"intent"`
	Locale               string             `json:"locale,omitempty" yaml:"locale"`
	Brand                *Entity            `json:"brand,omitempty" yaml:"brand"`
	Competitors          []Entity           `json:"competitors,omitempty" yaml:"competitors"`
	OwnedDomains         []string           `json:"owned_domains,omitempty" yaml:"owned_domains"`
	CompetitorDomains    []string           `json:"competitor_domains,omitempty" yaml:"competitor_domains"`
	PreferredDomains     []string           `json:"preferred_domains,omitempty" yaml:"preferred_domains"`
	BlockedDomains       []string           `json:"blocked_domains,omitempty" yaml:"blocked_domains"`
	ExpectedAnswerShape  string             `json:"expected_answer_shape,omitempty" yaml:"expected_answer_shape"`
	ExpectedEntities     []ExpectedEntity   `json:"expected_entities,omitempty" yaml:"expected_entities"`
	KnownWrongNamesakes  []ExpectedEntity   `json:"known_wrong_namesakes,omitempty" yaml:"known_wrong_namesakes"`
	Privacy              string             `json:"privacy,omitempty" yaml:"privacy"`
	Answers              *AnswerSet         `json:"answers,omitempty" yaml:"answers"`
	Grading              *Grading           `json:"grading,omitempty" yaml:"grading"`
	KnownWrongAnswers    []KnownWrongAnswer `json:"known_wrong_answers,omitempty" yaml:"known_wrong_answers"`
	FailureCases         []FailureCase      `json:"failure_cases,omitempty" yaml:"failure_cases"`
	Stack                *ProgrammingStack  `json:"stack,omitempty" yaml:"stack"`
	Sources              []ReferenceSource  `json:"sources,omitempty" yaml:"sources"`
	EventDate            string             `json:"event_date,omitempty" yaml:"event_date"`
	ExpectedModeBehavior map[string]string  `json:"expected_mode_behavior,omitempty" yaml:"expected_mode_behavior"`
	StaleAnswers         []StaleAnswer      `json:"stale_answers,omitempty" yaml:"stale_answers"`
	SourceExpectation    *SourceExpectation `json:"source_expectation,omitempty" yaml:"source_expectation"`
	ExpectedClaims       []Claim            `json:"expected_claims,omitempty" yaml:"expected_claims"`
	ForbiddenFailures    []Claim            `json:"forbidden_failures,omitempty" yaml:"forbidden_failures"`
	TargetClaim          *Claim             `json:"target_claim,omitempty" yaml:"target_claim"`
	ExpectedStance       string             `json:"expected_stance,omitempty" yaml:"expected_stance"`
	ExpectedCorrections  []Claim            `json:"expected_corrections,omitempty" yaml:"expected_corrections"`
	ForbiddenClaims      []Claim            `json:"forbidden_claims,omitempty" yaml:"forbidden_claims"`
	SourcePolicy         *SourcePolicy      `json:"source_policy,omitempty" yaml:"source_policy"`
	BaseItem             string             `json:"base_item,omitempty" yaml:"base_item"`
	Variants             []Variant          `json:"variants,omitempty" yaml:"variants"`
	EvaluatorIDs         []string           `json:"evaluator_ids,omitempty" yaml:"evaluator_ids,omitempty"`
	MetricIDs            []string           `json:"metric_ids,omitempty" yaml:"metric_ids,omitempty"`
	Dimensions           map[string]string  `json:"dimensions,omitempty" yaml:"dimensions"`
	ComparisonGroupID    string             `json:"comparison_group_id,omitempty" yaml:"comparison_group_id,omitempty"`
	Tags                 []string           `json:"tags,omitempty" yaml:"tags"`
	evaluatorIDsSet      bool
	metricIDsSet         bool
}

type Entity struct {
	Name     string   `json:"name" yaml:"name"`
	Category string   `json:"category,omitempty" yaml:"category,omitempty"`
	Aliases  []string `json:"aliases,omitempty" yaml:"aliases"`
	Domains  []string `json:"domains,omitempty" yaml:"domains"`
	// MatchCase counts the name and aliases only with the same capitals.
	MatchCase bool `json:"match_case,omitempty" yaml:"match_case"`
}

type ExpectedEntity struct {
	CanonicalName  string   `json:"canonical_name" yaml:"canonical_name"`
	EntityType     string   `json:"entity_type" yaml:"entity_type"`
	Aliases        []string `json:"aliases,omitempty" yaml:"aliases"`
	DescriptorsAny []string `json:"descriptors_any,omitempty" yaml:"descriptors_any"`
}

type AnswerSet struct {
	Canonical string   `json:"canonical" yaml:"canonical"`
	Aliases   []string `json:"aliases,omitempty" yaml:"aliases"`
}

type Grading struct {
	Type           string `json:"type" yaml:"type"`
	AbstainAllowed bool   `json:"abstain_allowed,omitempty" yaml:"abstain_allowed"`
	Confidence     string `json:"confidence,omitempty" yaml:"confidence"`
}

type ReferenceSource struct {
	URL   string `json:"url" yaml:"url"`
	Title string `json:"title,omitempty" yaml:"title"`
}

type KnownWrongAnswer struct {
	ID      string   `json:"id" yaml:"id"`
	Aliases []string `json:"aliases" yaml:"aliases"`
}

type FailureCase struct {
	ID      string   `json:"id" yaml:"id"`
	Class   string   `json:"class" yaml:"class"`
	Aliases []string `json:"aliases" yaml:"aliases"`
}

type ProgrammingStack struct {
	Language  string `json:"language" yaml:"language"`
	Ecosystem string `json:"ecosystem,omitempty" yaml:"ecosystem"`
	Library   string `json:"library,omitempty" yaml:"library"`
	API       string `json:"api,omitempty" yaml:"api"`
}

type StaleAnswer struct {
	ID        string   `json:"id" yaml:"id"`
	Aliases   []string `json:"aliases" yaml:"aliases"`
	StaleAsOf string   `json:"stale_as_of" yaml:"stale_as_of"`
}

type SourceExpectation struct {
	RequiredForModes       []string `json:"required_for_modes,omitempty" yaml:"required_for_modes"`
	FreshnessReferenceDate string   `json:"freshness_reference_date,omitempty" yaml:"freshness_reference_date"`
}

type Claim struct {
	ID   string `json:"id" yaml:"id"`
	Text string `json:"text" yaml:"text"`
}

type SourcePolicy struct {
	IndependentSourcesPreferred bool     `json:"independent_sources_preferred,omitempty" yaml:"independent_sources_preferred"`
	LowQualityDomains           []string `json:"low_quality_domains,omitempty" yaml:"low_quality_domains"`
}

type RenderedVariant struct {
	SuiteID           string            `json:"suite_id"`
	SuiteVersion      int               `json:"suite_version"`
	ItemID            string            `json:"item_id"`
	VariantID         string            `json:"variant_id"`
	Prompt            string            `json:"prompt"`
	Dimensions        map[string]string `json:"dimensions"`
	ComparisonGroupID string            `json:"comparison_group_id,omitempty"`
}
