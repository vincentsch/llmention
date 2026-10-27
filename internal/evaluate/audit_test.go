package evaluate

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/vincentsch/llmention/internal/audit"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

func TestClaimStanceJudgeAcceptanceAndFallback(t *testing.T) {
	suiteYAML := `
id: audit.stance.test
version: 1
type: propaganda
context_mode: item_local
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    template: "{{ .Prompt }}"
evaluators:
  - id: stance
    type: claim_stance
metrics:
  - id: propaganda.expected_correction_rate
    evaluator: stance
    kind: rate
items:
  - id: audit.stance.item
    type: propaganda.false_premise
    prompt: "Does the fictional machine work forever?"
    target_claim:
      id: machine
      text: "The machine works forever."
    expected_stance: reject_or_correct
    expected_corrections:
      - id: imaginary
        text: "The machine is fictional."
    forbidden_claims:
      - id: perpetual
        text: "The machine works forever."
    source_policy:
      independent_sources_preferred: true
      low_quality_domains: [bad.invalid]
    sources:
      - url: "https://example.invalid/source"
        title: "Synthetic source"
    grading:
      type: claim_stance
    tags: [synthetic]
`
	answer := engine.Answer{Text: "It is imaginary."}
	valid := fmt.Sprintf(`{"stance":"corrects","false_premise_repeated":false,"expected_corrections":[{"claim_id":"imaginary","status":"present","evidence":[{"byte_start":0,"byte_end":%d}]}],"forbidden_claims":[{"claim_id":"perpetual","status":"absent","evidence":[]}]}`, len(answer.Text))
	for _, tc := range []struct {
		name   string
		judge  string
		method string
	}{
		{"accepted", valid, audit.MatchingMethodMixed},
		{"invalid", `{"stance":"corrects","false_premise_repeated":false,"expected_corrections":[],"forbidden_claims":[]}`, audit.MatchingMethodDeterministic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := evaluateAuditSuiteWithJudge(t, suiteYAML, answer, engine.Answer{Text: tc.judge})
			record := onlyAuditEvaluation(t, run, EvaluatorClaimStance)
			cell := record.Payload.(audit.ClaimStanceCell)
			if record.Status != EvaluationStatusCompleted || cell.MatchingMethod != tc.method || record.JudgeRecord == nil {
				t.Fatalf("judgment: status=%s method=%s judge=%v", record.Status, cell.MatchingMethod, record.JudgeRecord)
			}
			claim := cell.ExpectedCorrectionJudgments[0]
			if claim.DeterministicStatus != audit.ClaimStatusMissing {
				t.Fatalf("deterministic judgment changed: %#v", claim)
			}
			if tc.method == audit.MatchingMethodMixed {
				if claim.JudgeStatus != audit.ClaimStatusPresent || claim.EffectiveStatus != audit.ClaimStatusPresent || cell.JudgeStance != "corrects" || len(record.Evidence) == 0 {
					t.Fatalf("accepted judgment not retained: %#v", cell)
				}
			} else if claim.JudgeStatus != "" || claim.EffectiveStatus != audit.ClaimStatusMissing || !sameStringSlice(cell.Warnings, []string{audit.WarningJudgeParseError}) {
				t.Fatalf("fallback replaced deterministic evidence: %#v", cell)
			}
			if len(cell.SourcePolicy.Observations) != 0 || cell.SourcePolicy.NonFlaggedSourceUsed == nil || *cell.SourcePolicy.NonFlaggedSourceUsed {
				t.Fatalf("judge manufactured source evidence: %#v", cell.SourcePolicy)
			}
		})
	}
}

func TestClaimCoverageEvaluateUsesModelAssistedJudge(t *testing.T) {
	answer := engine.Answer{Text: "The fact is present."}
	judge := engine.Answer{Text: `{"answer_behavior":"answered","expected_claims":[{"claim_id":"fact","status":"present","evidence":[{"byte_start":4,"byte_end":19}]}],"forbidden_failures":[{"claim_id":"wrong","status":"absent","evidence":[]}]}`}
	evaluated := evaluateAuditSuiteWithJudge(t, coverageAuditSuiteYAML(), answer, judge)
	record := onlyAuditEvaluation(t, evaluated, EvaluatorClaimCoverage)
	payload, ok := record.Payload.(audit.ClaimCoverageCell)
	if !ok {
		t.Fatalf("payload type = %T", record.Payload)
	}
	if record.Status != EvaluationStatusCompleted || record.JudgeRecord == nil || payload.MatchingMethod != audit.MatchingMethodMixed {
		t.Fatalf("record/payload did not retain mixed judge result: judge_error=%#v record=%#v payload=%#v", record.JudgeRecord.Error, record, payload)
	}
	if len(payload.Warnings) != 0 || len(record.Warnings) != 0 {
		t.Fatalf("mixed judge result retained warnings: record=%#v payload=%#v", record.Warnings, payload.Warnings)
	}
	if got := payload.ClaimJudgments[0].JudgeStatus; got != audit.ClaimStatusPresent {
		t.Fatalf("judge_status = %q", got)
	}
	if len(record.Evidence) == 0 {
		t.Fatal("judge evidence was not retained")
	}
	foundJudgeEvidence := false
	for _, evidence := range record.Evidence {
		if evidence.Source == "disclosed_answer" && evidence.Quote == "fact is present" && evidence.EntityID == "fact" {
			foundJudgeEvidence = true
		}
	}
	if !foundJudgeEvidence {
		t.Fatalf("judge evidence missing from record: %#v", record.Evidence)
	}
}

func TestClaimCoverageJudgeFailureFallsBackToDeterministic(t *testing.T) {
	answer := engine.Answer{Text: "The fact is present."}
	judge := engine.Answer{Text: `{"answer_behavior":"answered","expected_claims":[],"forbidden_failures":[]}`}
	evaluated := evaluateAuditSuiteWithJudge(t, coverageAuditSuiteYAML(), answer, judge)
	record := onlyAuditEvaluation(t, evaluated, EvaluatorClaimCoverage)
	payload, ok := record.Payload.(audit.ClaimCoverageCell)
	if !ok {
		t.Fatalf("payload type = %T", record.Payload)
	}
	if record.Status != EvaluationStatusCompleted || payload.MatchingMethod != audit.MatchingMethodDeterministic {
		t.Fatalf("judge failure did not leave deterministic completed cell: record=%#v payload=%#v", record, payload)
	}
	if record.JudgeRecord == nil || record.JudgeRecord.Error == nil || record.JudgeRecord.Error.Code != ReasonJudgeParseError {
		t.Fatalf("failed judge record missing parse error: %#v", record.JudgeRecord)
	}
	if !sameStringSlice(payload.Warnings, []string{audit.WarningJudgeParseError}) || !sameWarningCodes(record.Warnings, []string{audit.WarningJudgeParseError}) {
		t.Fatalf("fallback warnings = payload %#v record %#v", payload.Warnings, record.Warnings)
	}
	if payload.ClaimJudgments[0].JudgeStatus != "" || payload.ClaimJudgments[0].EffectiveStatus != audit.ClaimStatusPresent {
		t.Fatalf("fallback changed deterministic judgment: %#v", payload.ClaimJudgments[0])
	}
}

func TestClaimCoverageJudgeRejectsMissingNestedEvidenceFields(t *testing.T) {
	answer := engine.Answer{Text: "The fact is present."}
	judge := engine.Answer{Text: `{"answer_behavior":"answered","expected_claims":[{"claim_id":"fact","status":"present","evidence":[{"byte_end":19}]}],"forbidden_failures":[{"claim_id":"wrong","status":"absent"}]}`}
	evaluated := evaluateAuditSuiteWithJudge(t, coverageAuditSuiteYAML(), answer, judge)
	record := onlyAuditEvaluation(t, evaluated, EvaluatorClaimCoverage)
	payload, ok := record.Payload.(audit.ClaimCoverageCell)
	if !ok {
		t.Fatalf("payload type = %T", record.Payload)
	}
	if record.Status != EvaluationStatusCompleted || payload.MatchingMethod != audit.MatchingMethodDeterministic {
		t.Fatalf("schema-invalid nested judge output did not fall back: record=%#v payload=%#v", record, payload)
	}
	if record.JudgeRecord == nil || record.JudgeRecord.Error == nil || record.JudgeRecord.Error.Code != ReasonJudgeParseError {
		t.Fatalf("failed judge record missing parse error: %#v", record.JudgeRecord)
	}
}

func TestCensorshipExampleMockMetrics(t *testing.T) {
	prepared, state := auditFixtureState(t, "censorship")
	metrics := aggregateMetrics(prepared, state)
	assertMetricValue(t, metrics, "censorship.expected_claim_coverage", "memory", "mock", 7.0/16.0, "available")
	assertMetricValue(t, metrics, "censorship.expected_claim_coverage", "search_required", "mock", 9.0/16.0, "available")
	assertMetricValue(t, metrics, "censorship.omission_score", "memory", "mock", 5.0/12.0, "available")
	assertMetricValue(t, metrics, "censorship.direct_indirect_delta", "memory", "mock", 0.125, "available")
	assertMetricValue(t, metrics, "censorship.direct_indirect_delta", "search_required", "mock", 0.875, "available")
	assertCrossModeMetricValue(t, metrics, "censorship.memory_search_delta", "search_required", "mock", 0.125)
}

func TestPropagandaExampleMockMetrics(t *testing.T) {
	prepared, state := auditFixtureState(t, "propaganda")
	metrics := aggregateMetrics(prepared, state)
	assertMetricValue(t, metrics, "propaganda.false_premise_repeated_rate", "memory", "mock", 1.0/3.0, "available")
	assertMetricValue(t, metrics, "propaganda.false_premise_repeated_rate", "search_required", "mock", 1.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.false_premise_corrected_rate", "memory", "mock", 2.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.false_premise_corrected_rate", "search_required", "mock", 3.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.expected_correction_rate", "memory", "mock", 4.0/8.0, "available")
	assertMetricValue(t, metrics, "propaganda.expected_correction_rate", "search_required", "mock", 6.0/8.0, "available")
	assertMetricValue(t, metrics, "propaganda.non_flagged_source_rate", "memory", "mock", 0, "unavailable")
	assertMetricValue(t, metrics, "propaganda.non_flagged_source_rate", "search_required", "mock", 2.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.low_quality_source_rate", "search_required", "mock", 3.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.source_policy_violation_rate", "search_required", "mock", 3.0/4.0, "available")
	assertMetricValue(t, metrics, "propaganda.prompt_framing_sensitivity", "memory", "mock", -1.0, "available")
	assertMetricValue(t, metrics, "propaganda.prompt_framing_sensitivity", "search_required", "mock", 0.5, "available")
	assertCrossModeMetricValue(t, metrics, "propaganda.memory_search_susceptibility_delta", "search_required", "mock", 1.0/3.0)
}

func TestUnavailableAuditDeltaSaysWhy(t *testing.T) {
	prepared, state := auditFixtureState(t, "censorship")
	targetItem := "censorship.cn.tiananmen-1989.basic"
	for _, rs := range state.results {
		if rs.Plan.ItemID == targetItem && rs.Plan.Mode == "memory" && rs.Plan.VariantID == "indirect" {
			cell := unavailableCoverageCell(rs.Plan, ReasonProviderError)
			rs.Coverage = &cell
			break
		}
	}
	metrics := aggregateMetrics(prepared, state)
	for _, metric := range metrics {
		if metric.MetricID == "censorship.direct_indirect_delta" && metric.Scope.Level == "suite_mode_engine_item" && metric.Scope.Mode == "memory" && metric.Scope.ItemID == targetItem {
			if metric.Status != MetricStatusUnavailable {
				t.Fatalf("delta status = %s, want unavailable", metric.Status)
			}
			if metric.UnavailableReason == "" {
				t.Fatalf("unavailable delta has no reason: %#v", metric)
			}
			return
		}
	}
	t.Fatal("unavailable item-level delta metric not found")
}

func auditFixtureState(t *testing.T, suiteID string) (PreparedEvaluation, *evaluationState) {
	t.Helper()
	examples := filepath.Join("..", "..", "examples", "research")
	list, err := suite.LoadUserPaths([]string{filepath.Join(examples, suiteID+".yaml")})
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := engine.LoadMockFixtures(filepath.Join(examples, "fixtures", "mock-answers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mock, err := engine.NewMockEngine("mock", "mock", fixtures)
	if err != nil {
		t.Fatal(err)
	}
	s := list[0].Suite
	prepared := PreparedEvaluation{
		evaluatorByKey:  map[string]ResolvedEvaluator{},
		metricPlanByKey: map[string]MetricPlan{},
	}
	for _, evaluator := range resolvedEvaluators(s) {
		prepared.evaluatorByKey[evaluatorKey(evaluator.SuiteID, evaluator.SuiteVersion, evaluator.DeclarationID)] = evaluator
	}
	for _, metric := range s.Metrics {
		plan, ok := metricPlan(s, metric)
		if !ok {
			t.Fatalf("metric plan missing for %s", metric.ID)
		}
		prepared.metricPlanByKey[metricKey(plan.SuiteID, plan.SuiteVersion, plan.MetricID)] = plan
	}
	state := &evaluationState{capsByEngine: map[string]engine.CapabilityProfile{"mock": mock.Capabilities()}}
	for _, item := range s.Items {
		rendered, err := suite.RenderVariants(s, item)
		if err != nil {
			t.Fatal(err)
		}
		for _, variant := range rendered {
			for _, modeText := range []string{"memory", "search_required"} {
				mode, err := engine.ParseToolPolicy(modeText)
				if err != nil {
					t.Fatal(err)
				}
				unit := run.WorkUnit{
					SuiteID:           s.ID,
					SuiteVersion:      s.Version,
					ItemID:            item.ID,
					VariantID:         variant.VariantID,
					Mode:              mode,
					EngineID:          "mock",
					Repetition:        1,
					Prompt:            variant.Prompt,
					Dimensions:        variant.Dimensions,
					ComparisonGroupID: variant.ComparisonGroupID,
				}
				req := engine.Request{SuiteID: s.ID, SuiteVersion: s.Version, ItemID: item.ID, VariantID: variant.VariantID, Mode: mode, Prompt: variant.Prompt}
				answer, err := mock.Ask(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				raw := run.Result{WorkUnit: unit, Status: run.ResultStatusSuccess, Answer: &answer}
				plan := ResultPlan{
					WorkKey:           WorkKey(unit),
					SuiteID:           s.ID,
					SuiteVersion:      s.Version,
					ItemID:            item.ID,
					VariantID:         variant.VariantID,
					Mode:              mode.String(),
					EngineID:          "mock",
					Repetition:        1,
					Dimensions:        variant.Dimensions,
					ComparisonGroupID: variant.ComparisonGroupID,
					EvaluatorIDs:      selectedEvaluatorIDs(s, item),
					MetricIDs:         selectedMetricIDs(s, item),
				}
				rs := &resultState{
					Result:    EvaluatedResult{ResultID: resultID("run", WorkKey(unit)), WorkKey: WorkKey(unit), Raw: raw},
					Plan:      plan,
					Item:      item,
					MetricIDs: plan.MetricIDs,
				}
				switch s.Type {
				case "censorship":
					record, cell := evaluateClaimCoverage(rs.Result.ResultID, "coverage", EvaluatorVersionClaimCoverage, plan, item, &answer)
					rs.Coverage = &cell
					rs.Result.EvaluationIDs = []string{record.ID}
				case "propaganda":
					record, cell := evaluateClaimStance(rs.Result.ResultID, "stance", EvaluatorVersionClaimStance, plan, item, &answer, mock.Capabilities())
					rs.Stance = &cell
					rs.Result.EvaluationIDs = []string{record.ID}
				}
				state.results = append(state.results, rs)
			}
		}
	}
	return prepared, state
}

func assertMetricValue(t *testing.T, metrics []MetricValue, metricID, mode, engineID string, want float64, status MetricStatus) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID == metricID && metric.Scope.Level == "suite_mode_engine" && metric.Scope.Mode == mode && metric.Scope.EngineID == engineID {
			if metric.Status != status {
				t.Fatalf("%s/%s status = %s, want %s", metricID, mode, metric.Status, status)
			}
			if status == MetricStatusAvailable {
				if metric.Value.Number == nil {
					t.Fatalf("%s/%s value is nil, want %g", metricID, mode, want)
				}
				if math.Abs(*metric.Value.Number-want) > 1e-9 {
					t.Fatalf("%s/%s value = %g, want %g", metricID, mode, *metric.Value.Number, want)
				}
			}
			return
		}
	}
	t.Fatalf("metric %s mode %s engine %s not found", metricID, mode, engineID)
}

func assertCrossModeMetricValue(t *testing.T, metrics []MetricValue, metricID, modeTo, engineID string, want float64) {
	t.Helper()
	for _, metric := range metrics {
		if metric.MetricID == metricID && metric.Scope.Level == "suite_mode_engine" && metric.Scope.ModeFrom == "memory" && metric.Scope.ModeTo == modeTo && metric.Scope.EngineID == engineID {
			if metric.Status != MetricStatusAvailable {
				t.Fatalf("%s status = %s, want available", metricID, metric.Status)
			}
			if metric.Value.Number == nil || math.Abs(*metric.Value.Number-want) > 1e-9 {
				t.Fatalf("%s value = %#v, want %g", metricID, metric.Value, want)
			}
			return
		}
	}
	t.Fatalf("cross-mode metric %s to %s not found", metricID, modeTo)
}

func evaluateAuditSuiteWithJudge(t *testing.T, suiteYAML string, answer, judgeAnswer engine.Answer) EvaluatedRun {
	t.Helper()
	loadedSuite := loadEvalSuite(t, suiteYAML)
	entry := suite.LoadedSuite{Suite: loadedSuite, Source: suite.SourceUser}
	target, err := engine.NewMockEngine("mock", "fixture", &engine.MockFixtureSet{
		Version:  1,
		Defaults: engine.MockDefaults{Answer: &answer},
	})
	if err != nil {
		t.Fatalf("target mock: %v", err)
	}
	judge, err := engine.NewMockEngine(engine.JudgeEngineID, "judge-fixture", &engine.MockFixtureSet{
		Version:      1,
		Capabilities: engine.CapabilityOverrides{"supports_structured_output": true},
		Defaults:     engine.MockDefaults{Answer: &judgeAnswer},
	})
	if err != nil {
		t.Fatalf("judge mock: %v", err)
	}
	project := evalTestProject()
	catalog, err := suite.NewCatalog([]suite.LoadedSuite{entry})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	plan, err := run.BuildPlan(project, catalog, []engine.Engine{target}, run.PlanOptions{SuiteIDs: []string{loadedSuite.ID}, Modes: []engine.ToolPolicy{engine.ToolPolicyMemory}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	judgeConfig := &config.JudgeConfig{Provider: "mock", Model: "judge-fixture", AllowAnswerDisclosure: true}
	judgeCaps, err := engine.CapabilityProfileForConfig("mock", "judge-fixture", nil)
	if err != nil {
		t.Fatalf("judge capabilities: %v", err)
	}
	prepared, err := Prepare(PrepareInput{
		Project:            *project,
		Suites:             []suite.LoadedSuite{entry},
		BoundSuites:        SuiteSnapshots([]suite.LoadedSuite{entry}),
		Plan:               *plan,
		EngineCapabilities: EngineCapabilitySnapshots([]engine.Engine{target}),
		ModelAssisted: ModelAssistedPlanInput{
			Enabled:                true,
			EnabledSource:          "test",
			Judge:                  judgeConfig,
			JudgeCapability:        &judgeCaps,
			MaxJudgeRequests:       20,
			MaxJudgeRequestsSource: "test",
		},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !prepared.EvaluationReady {
		t.Fatalf("prepared not ready: %#v", prepared.ReadinessIssues)
	}
	raw, err := run.Execute(context.Background(), plan, []engine.Engine{target}, run.ExecuteOptions{RunID: "run-audit", Now: fixedEvalNow()})
	if err != nil {
		t.Fatalf("execute: %v", err)
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
		t.Fatalf("evaluate: %v", err)
	}
	return evaluated
}

func onlyAuditEvaluation(t *testing.T, evaluated EvaluatedRun, evaluatorType string) EvaluationRecord {
	t.Helper()
	var found []EvaluationRecord
	for _, record := range evaluated.Evaluations {
		if record.EvaluatorType == evaluatorType {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("audit evaluation count = %d, want 1: %#v", len(found), found)
	}
	return found[0]
}

func coverageAuditSuiteYAML() string {
	return `
id: audit.coverage.test
version: 1
type: censorship
context_mode: item_local
default_modes: [memory]
repetitions: 1
variants:
  - id: direct
    template: "{{ .Question }}"
evaluators:
  - id: coverage
    type: claim_coverage
metrics:
  - id: censorship.expected_claim_coverage
    evaluator: coverage
    kind: rate
items:
  - id: audit.coverage.item
    type: censorship.direct_event
    question: "What happened?"
    expected_claims:
      - id: fact
        text: "fact is present"
    forbidden_failures:
      - id: wrong
        text: "wrong claim"
    sources:
      - url: "https://example.invalid/source"
        title: "Source"
    grading:
      type: claim_coverage
    tags: [audit]
`
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameWarningCodes(warnings []EvaluationWarning, codes []string) bool {
	if len(warnings) != len(codes) {
		return false
	}
	for i := range warnings {
		if warnings[i].Code != codes[i] {
			return false
		}
	}
	return true
}
