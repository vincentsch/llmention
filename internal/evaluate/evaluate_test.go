package evaluate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	recognition "github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

func TestEvaluateVisibilitySourceMetrics(t *testing.T) {
	answer := engine.Answer{
		Text: "Rival is often mentioned before Acme Tools.",
		Raw:  []byte(`{"provider":"raw"}`),
		Citations: []engine.Citation{{
			URL:    "https://acme.example/docs",
			Domain: "acme.example",
		}},
		SourcesConsulted: []engine.Source{{
			URL:    "https://rival.example/compare",
			Domain: "rival.example",
		}},
		ToolCalls: []engine.ToolCall{{Type: "web_search", Query: "acme alternatives", Status: "completed"}},
	}
	evaluated := evaluateFixtureRun(t, answer, nil, ModelAssistedPlanInput{})
	// Two of the four prompts name Acme Tools, so only two answers count
	// toward its mention rate. Both mention it.
	assertMetricNumber(t, evaluated.Metrics, "visibility.brand_mention_rate", "suite_mode", 1)
	// Share of voice only uses the two prompts that name no tracked brand:
	// Acme Tools 2, Rival 2.
	assertMetricNumber(t, evaluated.Metrics, "visibility.brand_share_of_voice", "suite_mode", 0.5)
	assertMetricNumber(t, evaluated.Metrics, "visibility.brand_rank_avg", "suite_mode", 2)
	assertMetricNumber(t, evaluated.Metrics, "visibility.competitor_mention_rate", "suite_mode", 1)
	assertMetricNumber(t, evaluated.Metrics, "visibility.competitor_precedes_brand_rate", "suite_mode", 1)
	assertMetricNumber(t, evaluated.Metrics, "sources.owned_domain_citation_rate", "suite_mode", 1)
	assertMetricInteger(t, evaluated.Metrics, "sources.search_query_count", "suite_mode", 4)
	assertMetricUnavailable(t, evaluated.Metrics, "framing.evaluated_rate", "suite_mode")

	if got := len(evaluated.Evaluations); got == 0 {
		t.Fatalf("evaluations missing: %#v", evaluated)
	}
	assertEvaluationCount(t, evaluated.EvaluationCounts, "mention", EvaluationStatusCompleted, "", 4)
	assertEvaluationCount(t, evaluated.EvaluationCounts, "source", EvaluationStatusCompleted, "", 4)
	assertEvaluationCount(t, evaluated.EvaluationCounts, "framing", EvaluationStatusSkipped, ReasonModelAssistedDisabled, 4)
}

func TestEvaluateExactAnswerBenchmarkMetrics(t *testing.T) {
	s := loadEvalSuite(t, `
id: factoid.eval
version: 1
type: factoid
purpose: "Exact answer evaluator fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: exact
    type: exact_or_alias
metrics:
  - id: factoid.correct_rate
    evaluator: exact
    kind: rate
  - id: factoid.overconfident_wrong_rate
    evaluator: exact
    kind: rate
items:
  - id: factoid.eval.item
    type: factoid.single_answer
    question: "What is the largest city in Brazil?"
    answers:
      canonical: "Sao Paulo"
      aliases: ["SP"]
    grading:
      type: exact_or_alias
      confidence: optional
    evaluator_ids: [exact]
    metric_ids: [factoid.correct_rate, factoid.overconfident_wrong_rate]
    tags: [geography]
`)
	evaluated := evaluateCustomSuites(t, []suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}}, engine.Answer{Text: `{"answer":"Rio de Janeiro","confidence":88}`})
	assertMetricNumber(t, evaluated.Metrics, "factoid.correct_rate", "suite_mode", 0)
	assertMetricNumber(t, evaluated.Metrics, "factoid.overconfident_wrong_rate", "suite_mode", 1)

	var sawExact bool
	for _, record := range evaluated.Evaluations {
		if record.PayloadType != benchmark.PayloadTypeCell {
			continue
		}
		sawExact = true
		payload, ok := record.Payload.(benchmark.CellPayload)
		if !ok {
			t.Fatalf("exact payload type = %T", record.Payload)
		}
		if payload.AnswerStatus == nil || *payload.AnswerStatus != benchmark.AnswerStatusIncorrect || payload.Confidence == nil || *payload.Confidence != 88 || !payload.OverconfidentWrong {
			t.Fatalf("exact payload = %#v", payload)
		}
	}
	if !sawExact {
		t.Fatal("missing exact-answer evaluation record")
	}
}

func TestBenchmarkPairMetricPreservesRuntimeUnavailableReason(t *testing.T) {
	s := loadEvalSuite(t, `
id: factoid.pair
version: 1
type: factoid
purpose: "Exact answer pair fixture."
default_modes: [memory, search_auto]
repetitions: 1
evaluators:
  - id: exact
    type: exact_or_alias
metrics:
  - id: factoid.search_recovery_rate
    evaluator: exact
    kind: rate
items:
  - id: factoid.pair.item
    type: factoid.single_answer
    question: "What is the largest city in Brazil?"
    answers:
      canonical: "Sao Paulo"
    grading:
      type: exact_or_alias
    evaluator_ids: [exact]
    metric_ids: [factoid.search_recovery_rate]
    tags: [geography]
`)
	entries := []suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}}
	target, err := engine.NewMockEngine("mock", "fixture", nil)
	if err != nil {
		t.Fatalf("NewMockEngine(target) error = %v", err)
	}
	project := evalTestProject()
	catalog, err := suite.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{target}, run.PlanOptions{
		SuiteIDs: []string{s.ID},
		Modes:    []engine.ToolPolicy{engine.ToolPolicyMemory, engine.ToolPolicySearchAuto},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        SuiteSnapshots(entries),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{target}),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	raw := run.RawSuiteRun{
		RunID:              "run-pair",
		StartedAt:          "2026-08-18T12:00:00Z",
		FinishedAt:         "2026-08-18T12:00:01Z",
		SuiteIDs:           []string{s.ID},
		Modes:              []string{"memory", "search_auto"},
		EngineIDs:          []string{"mock"},
		TargetWorkUnits:    len(plan.WorkUnits),
		CompletedWorkUnits: 1,
		ErroredWorkUnits:   1,
		MaxRequests:        100,
		Retries:            2,
	}
	for _, unit := range plan.WorkUnits {
		result := run.Result{
			WorkUnit: unit,
			Provider: "mock",
			Model:    "fixture",
			Attempts: 1,
		}
		if unit.Mode == engine.ToolPolicyMemory {
			result.Status = run.ResultStatusError
			result.Error = &run.ResultError{Message: "provider failed"}
		} else {
			result.Status = run.ResultStatusSuccess
			result.Answer = &engine.Answer{Text: `{"answer":"Sao Paulo"}`}
		}
		raw.Results = append(raw.Results, result)
	}
	evaluated, err := Evaluate(EvaluationInput{Prepared: prepared, RawRun: raw, Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	for _, metric := range evaluated.Metrics {
		if metric.MetricID != "factoid.search_recovery_rate" || metric.Scope.Level != "suite_mode_engine" {
			continue
		}
		if metric.Status != MetricStatusUnavailable || metric.UnavailableReason != ReasonProviderError {
			t.Fatalf("search recovery metric = %#v, want provider_error unavailable", metric)
		}
		return
	}
	t.Fatalf("search recovery suite_mode_engine metric missing: %#v", evaluated.Metrics)
}

func TestEvaluateFramingJudge(t *testing.T) {
	targetAnswer := engine.Answer{Text: "Acme Tools is excellent."}
	judgeAnswer := engine.Answer{Text: `{"label":"positive","phrase":"clearly favorable","evidence":[{"byte_start":14,"byte_end":23,"quote":"excellent"}]}`}
	project := evalTestProject()
	judgeCfg := &config.JudgeConfig{
		Provider:              "mock",
		Model:                 "judge-fixture",
		AllowAnswerDisclosure: true,
	}
	judgeCaps, err := engine.CapabilityProfileForConfig("mock", "judge-fixture", nil)
	if err != nil {
		t.Fatalf("judge capabilities: %v", err)
	}
	modelAssisted := ModelAssistedPlanInput{
		Enabled:                true,
		EnabledSource:          "flag",
		Judge:                  judgeCfg,
		JudgeCapability:        &judgeCaps,
		MaxJudgeRequests:       20,
		MaxJudgeRequestsSource: "config",
	}
	evaluated := evaluateFixtureRunWithProject(t, project, targetAnswer, &judgeAnswer, modelAssisted)
	assertMetricNumber(t, evaluated.Metrics, "framing.evaluated_rate", "suite_mode", 1)
	assertMetricNumber(t, evaluated.Metrics, "framing.sentiment_positive_rate", "suite_mode", 1)
	assertEvaluationCount(t, evaluated.EvaluationCounts, "framing", EvaluationStatusCompleted, "", 4)

	var privateJudgeRecord bool
	for _, record := range evaluated.Evaluations {
		if record.EvaluatorType == EvaluatorFramingSentiment && record.Status == EvaluationStatusCompleted {
			privateJudgeRecord = record.JudgeRecord != nil && record.JudgeRecord.Prompt != "" && len(record.JudgeRecord.DisclosedInput) > 0
			if record.JudgeRecord != nil {
				var disclosed struct {
					TargetBrandName string `json:"target_brand_name"`
					TargetCategory  string `json:"target_category"`
				}
				if err := json.Unmarshal(record.JudgeRecord.DisclosedInput, &disclosed); err != nil {
					t.Fatalf("judge disclosed input JSON: %v", err)
				}
				if disclosed.TargetBrandName != "Acme Tools" || disclosed.TargetCategory != "developer productivity platform" {
					t.Fatalf("judge disclosed target context = %#v", disclosed)
				}
				if !strings.Contains(record.JudgeRecord.Prompt, "Target brand: Acme Tools") {
					t.Fatalf("judge prompt missing target brand: %q", record.JudgeRecord.Prompt)
				}
			}
		}
	}
	if !privateJudgeRecord {
		t.Fatalf("completed framing record did not retain private judge evidence: %#v", evaluated.Evaluations)
	}
}

func TestPrepareRejectsAliasAndDomainConflicts(t *testing.T) {
	s, err := suite.LoadBytes([]byte(`
id: visibility.conflict
version: 1
type: visibility
purpose: "Conflict fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: mention
    type: mention_match
metrics:
  - id: visibility.brand_share_of_voice
    evaluator: mention
    kind: rate
items:
  - id: visibility.conflict.item
    type: visibility.recommendation
    prompt: "Who should I compare?"
    intent: high
    brand:
      name: "Acme"
      aliases: ["Rival"]
    competitors:
      - name: "Rival"
        domains: ["*.acme.example"]
    owned_domains: ["acme.example"]
    expected_answer_shape: free_text
`))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	project := evalTestProject()
	eng, err := engine.NewMockEngine("mock", "fixture", nil)
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	catalog, err := suite.NewCatalog([]suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}})
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{eng}, run.PlanOptions{SuiteIDs: []string{"visibility.conflict"}, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             []suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}},
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{eng}),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.EvaluationReady {
		t.Fatalf("prepared evaluation ready despite conflicts: %#v", prepared.ReadinessIssues)
	}
	gotCodes := issueCodes(prepared.ReadinessIssues)
	wantCodes := []string{"conflicting_alias", "overlapping_domain_policy"}
	if !containsAll(gotCodes, wantCodes) {
		t.Fatalf("issue codes = %#v, want at least %#v; issues=%#v", gotCodes, wantCodes, prepared.ReadinessIssues)
	}
}

func TestSourceDomainCountsAndCompetitorAttribution(t *testing.T) {
	s := loadEvalSuite(t, `
id: sources.competitors
version: 1
type: sources
purpose: "Source metric regression fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: src
    type: source_domain
metrics:
  - id: sources.cited_domain_count
    evaluator: src
    kind: integer
  - id: sources.consulted_domain_count
    evaluator: src
    kind: integer
  - id: sources.competitor_domain_citation_rate
    evaluator: src
    kind: rate
  - id: sources.competitor_domain_citation_share
    evaluator: src
    kind: rate
items:
  - id: sources.competitors.item
    type: sources.competitor_domain
    prompt: "Which domains are cited?"
    brand:
      name: "Acme Tools"
    owned_domains: ["acme.example"]
    competitors:
      - name: "Rival A"
        domains: ["rival-a.example"]
      - name: "Rival B"
        domains: ["rival-b.example"]
`)
	answer := engine.Answer{
		Text: "Acme Tools and Rival A are discussed.",
		Citations: []engine.Citation{
			{URL: "https://acme.example/docs", Domain: "acme.example"},
			{URL: "https://rival-a.example/post", Domain: "rival-a.example"},
		},
		SourcesConsulted: []engine.Source{
			{URL: "https://rival-a.example/post", Domain: "rival-a.example"},
		},
	}
	evaluated := evaluateCustomSuites(t, []suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}}, answer)
	assertMetricInteger(t, evaluated.Metrics, "sources.cited_domain_count", "suite_mode", 2)
	assertMetricInteger(t, evaluated.Metrics, "sources.consulted_domain_count", "suite_mode", 1)
	assertMetricNumberForEntity(t, evaluated.Metrics, "sources.competitor_domain_citation_rate", "suite_mode", "Rival A", 1)
	assertMetricNumberForEntity(t, evaluated.Metrics, "sources.competitor_domain_citation_rate", "suite_mode", "Rival B", 0)
	assertMetricNumberForEntity(t, evaluated.Metrics, "sources.competitor_domain_citation_share", "suite_mode", "Rival A", 0.5)
	assertMetricNumberForEntity(t, evaluated.Metrics, "sources.competitor_domain_citation_share", "suite_mode", "Rival B", 0)
}

func TestSourceReadinessUsesMetricSpecificContext(t *testing.T) {
	countOnly := loadEvalSuite(t, `
id: sources.counts
version: 1
type: sources
purpose: "Source count readiness fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: src
    type: source_domain
metrics:
  - id: sources.search_query_count
    evaluator: src
    kind: integer
items:
  - id: sources.counts.item
    type: sources.owned_domain
    prompt: "Which searches ran?"
`)
	prepared := prepareCustomSuites(t, []suite.LoadedSuite{{Suite: countOnly, Source: suite.SourceUser}})
	if !prepared.EvaluationReady {
		t.Fatalf("policy-independent source metric was not ready: %#v", prepared.ReadinessIssues)
	}

	missingPolicy := loadEvalSuite(t, `
id: sources.missing-policy
version: 1
type: sources
purpose: "Source policy readiness fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: src
    type: source_domain
metrics:
  - id: sources.preferred_domain_citation_rate
    evaluator: src
    kind: rate
items:
  - id: sources.missing-policy.item
    type: sources.owned_domain
    prompt: "Which domains are cited?"
    owned_domains: ["acme.example"]
`)
	prepared = prepareCustomSuites(t, []suite.LoadedSuite{{Suite: missingPolicy, Source: suite.SourceUser}})
	if prepared.EvaluationReady {
		t.Fatalf("policy metric was ready without required policy context")
	}
	var sawMetricIssue bool
	for _, issue := range prepared.ReadinessIssues {
		if issue.Code == "missing_target_context" && issue.MetricID == "sources.preferred_domain_citation_rate" {
			sawMetricIssue = true
			break
		}
	}
	if !sawMetricIssue {
		t.Fatalf("missing metric-specific readiness issue: %#v", prepared.ReadinessIssues)
	}
}

func TestEvaluateEmitsUnavailableMetricsForFailedResults(t *testing.T) {
	s := loadEvalSuite(t, `
id: visibility.failure
version: 1
type: visibility
purpose: "Failed result metric fixture."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: mention
    type: mention_match
  - id: src
    type: source_domain
metrics:
  - id: visibility.brand_share_of_voice
    evaluator: mention
    kind: rate
  - id: sources.search_query_count
    evaluator: src
    kind: integer
items:
  - id: visibility.failure.item
    type: visibility.recommendation
    prompt: "Which tools are visible?"
    intent: high
    brand:
      name: "Acme Tools"
    expected_answer_shape: free_text
`)
	project := evalTestProject()
	target, err := engine.NewMockEngine("mock", "fixture", nil)
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	entries := []suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}}
	catalog, err := suite.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{target}, run.PlanOptions{SuiteIDs: []string{s.ID}, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        SuiteSnapshots(entries),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{target}),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !prepared.EvaluationReady {
		t.Fatalf("prepared not ready: %#v", prepared.ReadinessIssues)
	}
	raw := run.RawSuiteRun{
		RunID:            "run-failed",
		StartedAt:        "2026-08-18T12:00:00Z",
		FinishedAt:       "2026-08-18T12:00:01Z",
		SuiteIDs:         []string{s.ID},
		Modes:            []string{"memory"},
		EngineIDs:        []string{"mock"},
		TargetWorkUnits:  1,
		ErroredWorkUnits: 1,
		MaxRequests:      100,
		Retries:          2,
		Results: []run.Result{{
			WorkUnit: plan.WorkUnits[0],
			Provider: "mock",
			Model:    "fixture",
			Status:   run.ResultStatusError,
			Attempts: 1,
			Error:    &run.ResultError{Message: "provider failed"},
		}},
	}
	evaluated, err := Evaluate(EvaluationInput{Prepared: prepared, RawRun: raw})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertMetricUnavailable(t, evaluated.Metrics, "visibility.brand_share_of_voice", "suite_mode")
	assertMetricUnavailable(t, evaluated.Metrics, "sources.search_query_count", "suite_mode")
	assertEvaluationCount(t, evaluated.EvaluationCounts, "mention", EvaluationStatusSkipped, ReasonProviderError, 1)
	assertEvaluationCount(t, evaluated.EvaluationCounts, "src", EvaluationStatusSkipped, ReasonProviderError, 1)
}

func TestPreparedEvaluationUsesSuiteLocalIdentifiers(t *testing.T) {
	visibility := loadEvalSuite(t, `
id: visibility.local
version: 1
type: visibility
purpose: "Visibility local ids."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: shared
    type: mention_match
metrics:
  - id: visibility.brand_share_of_voice
    evaluator: shared
    kind: rate
items:
  - id: visibility.local.item
    type: visibility.recommendation
    prompt: "Which tools are visible?"
    intent: high
    brand:
      name: "Acme Tools"
    owned_domains: ["acme.example"]
    expected_answer_shape: free_text
`)
	sources := loadEvalSuite(t, `
id: sources.local
version: 1
type: sources
purpose: "Source local ids."
default_modes: [memory]
repetitions: 1
evaluators:
  - id: shared
    type: source_domain
metrics:
  - id: sources.search_query_count
    evaluator: shared
    kind: integer
items:
  - id: sources.local.item
    type: sources.owned_domain
    prompt: "Which searches ran?"
    owned_domains: ["acme.example"]
`)
	evaluated := evaluateCustomSuites(t, []suite.LoadedSuite{
		{Suite: visibility, Source: suite.SourceUser},
		{Suite: sources, Source: suite.SourceUser},
	}, engine.Answer{
		Text:      "Acme Tools is visible.",
		ToolCalls: []engine.ToolCall{{Type: "web_search", Query: "acme tools", Status: "completed"}},
	})
	var sawMention, sawSource bool
	for _, record := range evaluated.Evaluations {
		if record.EvaluatorType == EvaluatorMentionMatch && record.Status == EvaluationStatusCompleted {
			sawMention = true
		}
		if record.EvaluatorType == EvaluatorSourceDomain && record.Status == EvaluationStatusCompleted {
			sawSource = true
		}
	}
	if !sawMention || !sawSource {
		t.Fatalf("suite-local evaluator records missing mention=%v source=%v records=%#v", sawMention, sawSource, evaluated.Evaluations)
	}
	assertMetricNumber(t, evaluated.Metrics, "visibility.brand_share_of_voice", "suite_mode", 1)
	assertMetricInteger(t, evaluated.Metrics, "sources.search_query_count", "suite_mode", 1)
}

func TestEvaluateRecognitionBoundToTheBrand(t *testing.T) {
	target := recognitionMock(t, "mock")
	entries := boundRecognition(t, evalTestProject())
	if got := len(entries[0].Suite.Items); got != 2 {
		t.Fatalf("recognition items from brand and domain = %d, want 2", got)
	}
	prepared, plan := prepareCustomSuitesWithEngine(t, entries, target)
	if !prepared.EvaluationReady {
		t.Fatalf("prepared not ready: %#v", prepared.ReadinessIssues)
	}
	raw, err := run.Execute(context.Background(), plan, []engine.Engine{target}, run.ExecuteOptions{RunID: "run-recognition", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	evaluated, err := Evaluate(EvaluationInput{
		Context:        context.Background(),
		Prepared:       prepared,
		RawRun:         raw,
		MaxConcurrency: 2,
		Now:            fixedEvalNow(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertMetricNumber(t, evaluated.Metrics, "recognition.target_recognition_rate", "suite_mode", 1)
	assertMetricNumber(t, evaluated.Metrics, "recognition.model_coverage", "suite_mode", 1)
	assertEvaluationCount(t, evaluated.EvaluationCounts, "cluster", EvaluationStatusCompleted, "", 4)

	var cellCount, summaryCount int
	for _, record := range evaluated.Evaluations {
		switch record.PayloadType {
		case recognition.PayloadTypeCell:
			cellCount++
			payload, ok := record.Payload.(recognition.CellPayload)
			if !ok {
				t.Fatalf("cell payload type = %T, want recognition.CellPayload", record.Payload)
			}
			if payload.ParserStatus != recognition.ParserStatusCompleted || payload.TargetRecognized == nil || !*payload.TargetRecognized {
				t.Fatalf("cell payload not completed/recognized: %#v", payload)
			}
		case recognition.PayloadTypeSummary:
			summaryCount++
			payload, ok := record.Payload.(recognition.SummaryPayload)
			if !ok {
				t.Fatalf("summary payload type = %T, want recognition.SummaryPayload", record.Payload)
			}
			if payload.InputSetHash == "" || len(payload.Clusters) == 0 {
				t.Fatalf("summary payload missing hash/clusters: %#v", payload)
			}
		}
	}
	if cellCount != 2 || summaryCount != 2 {
		t.Fatalf("recognition payload coverage cells=%d summaries=%d", cellCount, summaryCount)
	}
}

func TestEvaluateRecognitionSummariesIncludeUnsupportedEngines(t *testing.T) {
	project := evalTestProject()
	project.Engines = []config.EngineConfig{
		{ID: "supported", Provider: "mock", Model: "fixture"},
		{ID: "unsupported", Provider: "mock", Model: "fixture", Capabilities: config.Capabilities{"supports_memory": false}},
	}
	engines, err := engine.NewFactory(engine.FactoryOptions{World: &engine.MockWorld{Brand: engine.MockBrand{Name: "Acme Tools"}, Affinity: map[string]float64{"supported": 1}}}).NewProjectEngines(project)
	if err != nil {
		t.Fatalf("NewProjectEngines() error = %v", err)
	}
	entries := boundRecognition(t, project)
	catalog, err := suite.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, engines.Engines, run.PlanOptions{SuiteIDs: []string{"recognition.basic"}, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if got, want := len(plan.WorkUnits), 2; got != want {
		t.Fatalf("work units = %d, want %d", got, want)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        SuiteSnapshots(entries),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots(engines.Engines),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	raw, err := run.Execute(context.Background(), plan, engines.Engines, run.ExecuteOptions{RunID: "run-recognition-unsupported", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	evaluated, err := Evaluate(EvaluationInput{Context: context.Background(), Prepared: prepared, RawRun: raw, Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	summary := recognitionSummaryForItem(t, evaluated, "brand")
	assertStringSlice(t, summary.EligibleEngineIDs, []string{"supported"})
	assertStringSlice(t, summary.RecognizingEngineIDs, []string{"supported"})
	assertStringSlice(t, summary.UnavailableEngineIDs, []string{"unsupported"})
	wantHash := recognition.InputSetHash([]string{
		"recognition.basic/3/brand/default/memory/supported/1",
		"recognition.basic/3/brand/default/memory/unsupported/1",
	})
	if summary.InputSetHash != wantHash {
		t.Fatalf("summary input_set_hash = %q, want %q", summary.InputSetHash, wantHash)
	}
}

func TestEvaluateRecognitionEmitsUnavailableMetricsForFullyUnsupportedSelection(t *testing.T) {
	project := evalTestProject()
	project.Engines = []config.EngineConfig{
		{ID: "unsupported", Provider: "mock", Model: "fixture", Capabilities: config.Capabilities{"supports_memory": false}},
	}
	engines, err := engine.NewFactory(engine.FactoryOptions{World: &engine.MockWorld{Brand: engine.MockBrand{Name: "Acme Tools"}, Affinity: map[string]float64{"supported": 1}}}).NewProjectEngines(project)
	if err != nil {
		t.Fatalf("NewProjectEngines() error = %v", err)
	}
	entries := boundRecognition(t, project)
	catalog, err := suite.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, engines.Engines, run.PlanOptions{SuiteIDs: []string{"recognition.basic"}, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if got := len(plan.WorkUnits); got != 0 {
		t.Fatalf("work units = %d, want 0", got)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        SuiteSnapshots(entries),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots(engines.Engines),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	raw, err := run.Execute(context.Background(), plan, engines.Engines, run.ExecuteOptions{RunID: "run-recognition-fully-unsupported", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	evaluated, err := Evaluate(EvaluationInput{Context: context.Background(), Prepared: prepared, RawRun: raw, Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	var summaryCount int
	for _, record := range evaluated.Evaluations {
		if record.PayloadType == recognition.PayloadTypeSummary {
			summaryCount++
			payload := record.Payload.(recognition.SummaryPayload)
			assertStringSlice(t, payload.UnavailableEngineIDs, []string{"unsupported"})
			if payload.InputSetHash == "" {
				t.Fatalf("summary input_set_hash is empty for unsupported selected engine: %#v", payload)
			}
		}
	}
	if summaryCount != 2 {
		t.Fatalf("summary count = %d, want 2", summaryCount)
	}
	assertMetricUnavailable(t, evaluated.Metrics, "recognition.target_recognition_rate", "suite_mode")
	assertMetricUnavailable(t, evaluated.Metrics, "recognition.model_coverage", "suite_mode")
}

func TestRecognitionJudgeUnknownDecisionRetainsJudgeState(t *testing.T) {
	reason := "name is plausible but not internally confirmed"
	cell := recognition.CellPayload{
		ParserStatus: recognition.ParserStatusCompleted,
		ParsedResults: []recognition.ParsedResult{{
			ParsedResultID: "parsed-unknown",
			Position:       0,
			Rank:           1,
			Name:           "Jordan Lee",
			Type:           recognition.EntityTypePerson,
			Confidence:     42,
			Snippet:        "An ambiguous person.",
		}},
		Assignments: []recognition.Assignment{{
			ParsedResultID:           "parsed-unknown",
			Position:                 0,
			ClusterID:                recognition.StableClusterID("Jordan Lee", recognition.EntityTypePerson),
			ClusterName:              "Jordan Lee",
			ClusterType:              recognition.EntityTypePerson,
			AssignmentClassification: recognition.ClassificationUnknownNamesake,
			MatchBasis:               "unresolved",
			Confidence:               42,
		}},
		TargetRecognized: recognition.PtrBool(false),
	}
	applyRecognitionJudgeResult(&cell, JudgeResult{
		Status: EvaluationStatusCompleted,
		Record: JudgeRecord{
			TemplateID:      "recognition_hallucination_candidate",
			TemplateVersion: "1",
			RequestMode:     "memory",
			JudgeEngineID:   engine.JudgeEngineID,
			JudgeProvider:   "mock",
			JudgeModel:      "judge-fixture",
		},
		Parsed: JudgeParsedOutput{ParsedObject: json.RawMessage(`{"decisions":[{"parsed_result_id":"parsed-unknown","classification":"unknown_namesake","reason":"` + reason + `"}]}`)},
	})
	assignment := cell.Assignments[0]
	if assignment.AssignmentClassification != recognition.ClassificationUnknownNamesake || assignment.MatchBasis != "judge" || assignment.JudgeDecisionReason == nil || *assignment.JudgeDecisionReason != reason {
		t.Fatalf("judge-retained assignment = %#v", assignment)
	}
	summary := recognition.BuildSummary(recognition.SummaryInput{
		Cells:             []recognition.CellPayload{cell},
		PlannedWorkKeys:   []string{"suite/1/item/default/memory/mock/1"},
		SelectedEngineIDs: []string{"mock"},
		SuiteID:           "suite",
		SuiteVersion:      1,
		ItemID:            "item",
		VariantID:         "default",
		Mode:              "memory",
	})
	if summary.JudgeMetadata == nil || summary.Clusters[0].SuccessfulJudgedSupportCount != 1 {
		t.Fatalf("summary judge metadata/count = %#v clusters=%#v", summary.JudgeMetadata, summary.Clusters)
	}
}

func TestRecognitionJudgeDecisionMismatchUsesDedicatedFallbackReason(t *testing.T) {
	handler := newRecognitionTaskHandler()
	mismatchedAnswer := `{"decisions":[{"parsed_result_id":"parsed-extra","classification":"unknown_namesake","reason":"not enough context"}]}`
	disclosedInput := json.RawMessage(`{"results":[{"parsed_result_id":"parsed-expected"}]}`)
	for name, answer := range map[string]string{
		"unexpected": mismatchedAnswer,
		"duplicate":  `{"decisions":[{"parsed_result_id":"parsed-expected","classification":"unknown_namesake","reason":"first"},{"parsed_result_id":"parsed-expected","classification":"unknown_namesake","reason":"second"}]}`,
		"missing":    `{"decisions":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := handler.ParseAndValidate(answer, disclosedInput)
			if err == nil {
				t.Fatalf("ParseAndValidate() succeeded for mismatched decision IDs")
			}
			var coded judgeReasonCodedError
			if !errors.As(err, &coded) || coded.JudgeReasonCode() != ReasonJudgeDecisionMismatch {
				t.Fatalf("ParseAndValidate() reason = %#v, want %s", err, ReasonJudgeDecisionMismatch)
			}
		})
	}

	judge, err := engine.NewMockEngine("judge", "judge-fixture", &engine.MockFixtureSet{
		Defaults: engine.MockDefaults{Answer: &engine.Answer{Text: mismatchedAnswer}},
	})
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	results, err := ExecuteJudgeTasks(context.Background(), judge, []JudgeTask{{
		ID:              "judge-task",
		EvaluationID:    "eval-recognition",
		TemplateID:      handler.TemplateID(),
		TemplateVersion: handler.TemplateVersion(),
		RequestMode:     engine.ToolPolicyMemory.String(),
		DisclosedInput:  disclosedInput,
		OutputSchema:    handler.OutputSchema(),
		RenderedPrompt:  "prompt",
	}}, handler, JudgeExecuteOptions{Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("ExecuteJudgeTasks() error = %v", err)
	}
	if len(results) != 1 || results[0].ReasonCode != ReasonJudgeDecisionMismatch || results[0].Record.Error == nil || results[0].Record.Error.Code != ReasonJudgeDecisionMismatch {
		t.Fatalf("judge task result = %#v, want mismatch reason", results)
	}

	cell := recognition.CellPayload{
		ParserStatus: recognition.ParserStatusCompleted,
		Assignments: []recognition.Assignment{{
			ParsedResultID:           "parsed-expected",
			AssignmentClassification: recognition.ClassificationUnknownNamesake,
		}},
		TargetRecognized: recognition.PtrBool(false),
	}
	applyRecognitionJudgeResult(&cell, JudgeResult{
		Status:     EvaluationStatusError,
		ReasonCode: ReasonJudgeDecisionMismatch,
		Record: JudgeRecord{
			TemplateID:      "recognition_hallucination_candidate",
			TemplateVersion: "1",
			RequestMode:     "memory",
			JudgeProvider:   "mock",
			JudgeModel:      "judge-fixture",
		},
		Error: &EvaluationError{Code: ReasonJudgeDecisionMismatch, Message: "judge decisions do not match disclosed parsed_result_id set"},
	})
	if cell.AssistStatus != recognition.AssistStatusFallback || cell.AssistReason != recognition.AssistReasonJudgeDecisionMismatch {
		t.Fatalf("assist fallback = %s/%s, want %s/%s", cell.AssistStatus, cell.AssistReason, recognition.AssistStatusFallback, recognition.AssistReasonJudgeDecisionMismatch)
	}
	if cell.JudgeMetadata == nil || cell.JudgeMetadata.TemplateID != "recognition_hallucination_candidate" {
		t.Fatalf("judge metadata missing after mismatch fallback: %#v", cell.JudgeMetadata)
	}
}

func TestMentionEvaluatorUsesTheSharedMatcher(t *testing.T) {
	record, _ := evaluateMention("result", "mention", "1", suite.Item{Brand: &suite.Entity{Name: "Straße"}}, "Best tools?", "STRASSE is mentioned.")
	if record.Values["brand_mentioned"].Boolean == nil || !*record.Values["brand_mentioned"].Boolean {
		t.Fatalf("case-fold equivalent brand was not matched: %#v", record)
	}
	_, analysis := evaluateMention("result", "mention", "1", suite.Item{
		Brand:       &suite.Entity{Name: "Acme"},
		Competitors: []suite.Entity{{Name: "Acme Pro"}},
	}, "Best tools?", "Acme Pro is mentioned.")
	if analysis.Brand.Mentioned || !analysis.Competitors["Acme Pro"].Mentioned {
		t.Fatalf("longest match must win: %#v", analysis)
	}
	_, analysis = evaluateMention("result", "mention", "1", suite.Item{Brand: &suite.Entity{Name: "Acme"}}, "Alternatives to Acme?", "I don't know Acme.")
	if !analysis.Brand.Mentioned || analysis.Brand.Counted() {
		t.Fatalf("a brand named in the prompt must not count: %#v", analysis.Brand)
	}
}

func TestFramingJudgeRedactsAndStrictlyParses(t *testing.T) {
	handler := framingTaskHandler{}
	result := EvaluatedResult{
		ResultID: "result-a",
		Raw: run.Result{
			WorkUnit: run.WorkUnit{Prompt: "Compare api_key=sk-secretprompt123456 Acme Tools."},
			Answer:   &engine.Answer{Text: "token=secret-token-alpha-12345 Acme Tools is excellent."},
		},
	}
	disclosed, err := handler.BuildDisclosedInputWithItem(EvaluationRecord{ID: "eval-a"}, result, suite.Item{Brand: &suite.Entity{Name: "Acme Tools"}})
	if err != nil {
		t.Fatalf("BuildDisclosedInputWithItem() error = %v", err)
	}
	if strings.Contains(string(disclosed), "secret-token-alpha-12345") || strings.Contains(string(disclosed), "sk-secretprompt123456") {
		t.Fatalf("disclosed input was not redacted: %s", disclosed)
	}
	opaqueSecret := "opaque{{registered}}\nsecret"
	disclosed, err = handler.buildDisclosedInput(EvaluationRecord{ID: "eval-secret"}, EvaluatedResult{
		ResultID: "result-secret",
		Raw: run.Result{
			WorkUnit: run.WorkUnit{Prompt: "Prompt contains " + opaqueSecret},
			Answer:   &engine.Answer{Text: "Answer contains " + opaqueSecret},
		},
	}, suite.Item{Brand: &suite.Entity{Name: "Acme Tools"}}, func(value string) string {
		return strings.ReplaceAll(value, opaqueSecret, "[REDACTED]")
	})
	if err != nil {
		t.Fatalf("redacted disclosure: %v", err)
	}
	if strings.Contains(string(disclosed), opaqueSecret) {
		t.Fatalf("registered secret survived disclosure redaction: %s", disclosed)
	}
	if _, _, err := handler.ParseAndValidate(`{"label":"positive","phrase":"ok"}`, disclosed); err == nil {
		t.Fatalf("missing evidence field was accepted")
	}
	if _, _, err := handler.ParseAndValidate(`{"label":"positive","phrase":"ok","evidence":[],"extra":true}`, disclosed); err == nil {
		t.Fatalf("unknown judge output field was accepted")
	}

	baseResult := EvaluatedResult{
		ResultID: "result-b",
		Raw: run.Result{
			Answer: &engine.Answer{Text: "Acme Tools is excellent."},
		},
	}
	firstDisclosure, err := handler.BuildDisclosedInputWithItem(EvaluationRecord{ID: "eval-b"}, baseResult, suite.Item{Brand: &suite.Entity{Name: "Acme Tools"}})
	if err != nil {
		t.Fatalf("first disclosure: %v", err)
	}
	secondDisclosure, err := handler.BuildDisclosedInputWithItem(EvaluationRecord{ID: "eval-c"}, baseResult, suite.Item{Brand: &suite.Entity{Name: "Acme Tools"}})
	if err != nil {
		t.Fatalf("second disclosure: %v", err)
	}
	judgeJSON := `{"label":"positive","phrase":"ok","evidence":[{"byte_start":14,"byte_end":23,"quote":"excellent"}]}`
	first, _, err := handler.ParseAndValidate(judgeJSON, firstDisclosure)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	second, _, err := handler.ParseAndValidate(judgeJSON, secondDisclosure)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if first.Evidence[0].ID == second.Evidence[0].ID {
		t.Fatalf("judge evidence ids collided across evaluations: %s", first.Evidence[0].ID)
	}
}

func evaluateFixtureRun(t *testing.T, answer engine.Answer, judgeAnswer *engine.Answer, modelAssisted ModelAssistedPlanInput) EvaluatedRun {
	t.Helper()
	return evaluateFixtureRunWithProject(t, evalTestProject(), answer, judgeAnswer, modelAssisted)
}

func loadEvalSuite(t *testing.T, data string) *suite.Suite {
	t.Helper()
	s, err := suite.LoadBytes([]byte(data))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	return s
}

func evaluateCustomSuites(t *testing.T, entries []suite.LoadedSuite, answer engine.Answer) EvaluatedRun {
	t.Helper()
	target, err := engine.NewMockEngine("mock", "fixture", &engine.MockFixtureSet{
		Version:  1,
		Defaults: engine.MockDefaults{Answer: &answer},
	})
	if err != nil {
		t.Fatalf("NewMockEngine(target) error = %v", err)
	}
	prepared, plan := prepareCustomSuitesWithEngine(t, entries, target)
	if !prepared.EvaluationReady {
		t.Fatalf("prepared not ready: %#v", prepared.ReadinessIssues)
	}
	raw, err := run.Execute(context.Background(), plan, []engine.Engine{target}, run.ExecuteOptions{RunID: "run-custom", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	evaluated, err := Evaluate(EvaluationInput{
		Context:        context.Background(),
		Prepared:       prepared,
		RawRun:         raw,
		MaxConcurrency: 2,
		Now:            fixedEvalNow(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	return evaluated
}

func prepareCustomSuites(t *testing.T, entries []suite.LoadedSuite) PreparedEvaluation {
	t.Helper()
	target, err := engine.NewMockEngine("mock", "fixture", nil)
	if err != nil {
		t.Fatalf("NewMockEngine() error = %v", err)
	}
	prepared, _ := prepareCustomSuitesWithEngine(t, entries, target)
	return prepared
}

func prepareCustomSuitesWithEngine(t *testing.T, entries []suite.LoadedSuite, target engine.Engine) (PreparedEvaluation, *run.Plan) {
	t.Helper()
	project := evalTestProject()
	catalog, err := suite.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	var suiteIDs []string
	for _, entry := range entries {
		suiteIDs = append(suiteIDs, entry.Suite.ID)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{target}, run.PlanOptions{SuiteIDs: suiteIDs, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             entries,
		BoundSuites:        SuiteSnapshots(entries),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{target}),
		ModelAssisted:      ModelAssistedPlanInput{MaxJudgeRequests: 20},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return prepared, plan
}

func evaluateFixtureRunWithProject(t *testing.T, project *config.Project, answer engine.Answer, judgeAnswer *engine.Answer, modelAssisted ModelAssistedPlanInput) EvaluatedRun {
	t.Helper()
	target, err := engine.NewMockEngine("mock", "fixture", &engine.MockFixtureSet{
		Version:  1,
		Defaults: engine.MockDefaults{Answer: &answer},
	})
	if err != nil {
		t.Fatalf("NewMockEngine(target) error = %v", err)
	}
	catalogEntries, err := suite.LoadCatalog(project)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	bound, bindIssues, err := BindProjectContext(project, catalogEntries.Entries())
	if err != nil {
		t.Fatalf("BindProjectContext() error = %v", err)
	}
	catalog, err := suite.NewCatalog(bound)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{target}, run.PlanOptions{Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	selected := selectedEntries(bound, config.PromptsSuiteID)
	if modelAssisted.MaxJudgeRequests == 0 {
		modelAssisted.MaxJudgeRequests = 20
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             selected,
		BoundSuites:        SuiteSnapshots(selected),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{target}),
		ModelAssisted:      modelAssisted,
		PrecomputedIssues:  filterIssues(bindIssues, config.PromptsSuiteID),
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !prepared.EvaluationReady {
		t.Fatalf("prepared not ready: %#v", prepared.ReadinessIssues)
	}
	raw, err := run.Execute(context.Background(), plan, []engine.Engine{target}, run.ExecuteOptions{RunID: "run-eval", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var judge engine.Engine
	if judgeAnswer != nil {
		judge, err = engine.NewMockEngine(engine.JudgeEngineID, "judge-fixture", &engine.MockFixtureSet{
			Version:  1,
			Defaults: engine.MockDefaults{Answer: judgeAnswer},
		})
		if err != nil {
			t.Fatalf("NewMockEngine(judge) error = %v", err)
		}
	}
	evaluated, err := Evaluate(EvaluationInput{
		Context:        context.Background(),
		Prepared:       prepared,
		RawRun:         raw,
		JudgeEngine:    judge,
		MaxConcurrency: 2,
		Now:            fixedEvalNow(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	return evaluated
}

// evalTestProject has two prompts that do not name the brand and two that do.
func evalTestProject() *config.Project {
	project, err := config.LoadBytes([]byte(`
version: 1
brand: {name: Acme Tools, category: developer productivity platform, aliases: [Acme], domains: [acme.example]}
competitors: [{name: Rival, aliases: [RivalCo], domains: [rival.example]}]
engines: [{id: mock, provider: mock, model: fixture}]
prompts:
  - What are the best developer productivity platform options to consider?
  - What are the best developer productivity platform options to consider? Answer concisely.
  - What are alternatives to Acme Tools for developer productivity platform?
  - What are alternatives to Acme Tools for developer productivity platform? Answer concisely.
modes: [memory]
execution: {max_requests: 100, retries: 2, max_judge_requests: 20}
analysis:
  judge: {provider: mock, model: judge-fixture, allow_answer_disclosure: true}
`))
	if err != nil {
		panic(err)
	}
	return project
}

func boundRecognition(t *testing.T, project *config.Project) []suite.LoadedSuite {
	t.Helper()
	entries, err := suite.LoadBuiltIns()
	if err != nil {
		t.Fatal(err)
	}
	bound, issues, err := BindProjectContext(project, selectedEntries(entries, "recognition.basic"))
	if err != nil || len(issues) > 0 {
		t.Fatalf("bind recognition: %v %v", err, issues)
	}
	return bound
}

// recognitionMock always recognizes Acme Tools.
func recognitionMock(t *testing.T, id string) engine.Engine {
	t.Helper()
	eng, err := engine.NewFactory(engine.FactoryOptions{World: &engine.MockWorld{
		Brand:    engine.MockBrand{Name: "Acme Tools", Domain: "acme.example"},
		Category: "developer productivity",
		Affinity: map[string]float64{id: 1},
	}}).NewEngine(config.EngineConfig{ID: id, Provider: "mock", Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func selectedEntries(entries []suite.LoadedSuite, ids ...string) []suite.LoadedSuite {
	want := map[string]struct{}{}
	for _, id := range ids {
		want[id] = struct{}{}
	}
	var out []suite.LoadedSuite
	for _, entry := range entries {
		if entry.Suite == nil {
			continue
		}
		if _, ok := want[entry.Suite.ID]; ok {
			out = append(out, entry)
		}
	}
	return out
}

func filterIssues(issues []ReadinessIssue, suiteID string) []ReadinessIssue {
	var out []ReadinessIssue
	for _, issue := range issues {
		if issue.SuiteID == suiteID || issue.SuiteID == "" {
			out = append(out, issue)
		}
	}
	return out
}

func assertMetricNumber(t *testing.T, metrics []MetricValue, metricID, level string, want float64) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID != metricID || metric.Scope.Level != level || metric.Scope.Mode != "memory" {
			continue
		}
		if metric.Status != MetricStatusAvailable || metric.Value.Number == nil || *metric.Value.Number != want {
			var got any = nil
			if metric.Value.Number != nil {
				got = *metric.Value.Number
			}
			t.Fatalf("%s/%s = %v (%#v), want number %v", metricID, level, got, metric, want)
		}
		return
	}
	t.Fatalf("metric %s/%s missing from %#v", metricID, level, metrics)
}

func assertMetricNumberForEntity(t *testing.T, metrics []MetricValue, metricID, level, entityID string, want float64) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID != metricID || metric.Scope.Level != level || metric.Scope.Mode != "memory" || metric.Scope.EntityID != entityID {
			continue
		}
		if metric.Status != MetricStatusAvailable || metric.Value.Number == nil || *metric.Value.Number != want {
			var got any = nil
			if metric.Value.Number != nil {
				got = *metric.Value.Number
			}
			t.Fatalf("%s/%s/%s = %v (%#v), want number %v", metricID, level, entityID, got, metric, want)
		}
		return
	}
	t.Fatalf("metric %s/%s/%s missing from %#v", metricID, level, entityID, metrics)
}

func assertMetricInteger(t *testing.T, metrics []MetricValue, metricID, level string, want int64) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID != metricID || metric.Scope.Level != level || metric.Scope.Mode != "memory" {
			continue
		}
		if metric.Status != MetricStatusAvailable || metric.Value.Integer == nil || *metric.Value.Integer != want {
			var got any = nil
			if metric.Value.Integer != nil {
				got = *metric.Value.Integer
			}
			t.Fatalf("%s/%s = %v (%#v), want integer %v", metricID, level, got, metric, want)
		}
		return
	}
	t.Fatalf("metric %s/%s missing from %#v", metricID, level, metrics)
}

func assertMetricUnavailable(t *testing.T, metrics []MetricValue, metricID, level string) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID != metricID || metric.Scope.Level != level || metric.Scope.Mode != "memory" {
			continue
		}
		if metric.Status != MetricStatusUnavailable || metric.Value.Type != "null" {
			t.Fatalf("%s/%s = %#v, want unavailable null", metricID, level, metric)
		}
		return
	}
	t.Fatalf("metric %s/%s missing from %#v", metricID, level, metrics)
}

func recognitionSummaryForItem(t *testing.T, evaluated EvaluatedRun, itemID string) recognition.SummaryPayload {
	t.Helper()
	for _, record := range evaluated.Evaluations {
		if record.PayloadType != recognition.PayloadTypeSummary {
			continue
		}
		payload, ok := record.Payload.(recognition.SummaryPayload)
		if !ok {
			t.Fatalf("summary payload type = %T", record.Payload)
		}
		if payload.ItemID == itemID {
			return payload
		}
	}
	t.Fatalf("recognition summary for item %s missing from %#v", itemID, evaluated.Evaluations)
	return recognition.SummaryPayload{}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice = %#v, want %#v", got, want)
		}
	}
}

func assertEvaluationCount(t *testing.T, counts []EvaluationCount, declarationID string, status EvaluationStatus, reason string, want int) {
	t.Helper()
	for _, count := range counts {
		if count.DeclarationID != declarationID || count.Status != status || count.ReasonCode != reason {
			continue
		}
		if count.Count != want {
			t.Fatalf("evaluation count %s/%s/%s = %d, want %d in %#v", declarationID, status, reason, count.Count, want, counts)
		}
		return
	}
	t.Fatalf("evaluation count %s/%s/%s missing from %#v", declarationID, status, reason, counts)
}

func issueCodes(issues []ReadinessIssue) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, issue := range issues {
		seen[issue.Code] = struct{}{}
	}
	for code := range seen {
		out = append(out, code)
	}
	return out
}

func containsAll(got, want []string) bool {
	gotSet := map[string]struct{}{}
	for _, value := range got {
		gotSet[value] = struct{}{}
	}
	for _, value := range want {
		if _, ok := gotSet[value]; !ok {
			return false
		}
	}
	return true
}

func fixedEvalNow() func() time.Time {
	when := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return when }
}

func TestSelectedEntriesHelper(t *testing.T) {
	entries := []suite.LoadedSuite{{Suite: &suite.Suite{ID: "b"}}, {Suite: &suite.Suite{ID: "a"}}}
	got := selectedEntries(entries, "a")
	if len(got) != 1 || got[0].Suite.ID != "a" {
		t.Fatalf("selectedEntries() = %#v, want only a", got)
	}
}
