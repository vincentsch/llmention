package evaluate

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	auditpkg "github.com/vincentsch/llmention/internal/audit"
	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

type evaluationState struct {
	resultsByID  map[string]*resultState
	results      []*resultState
	evaluations  []EvaluationRecord
	capsByEngine map[string]engine.CapabilityProfile
}

type resultState struct {
	Result      EvaluatedResult
	Plan        ResultPlan
	Item        suite.Item
	Mention     *mentionAnalysis
	Source      *sourceAnalysis
	Framing     *framingAnalysis
	Recognition *recognitionAnalysis
	Benchmark   *benchmark.CellPayload
	Coverage    *auditpkg.ClaimCoverageCell
	Stance      *auditpkg.ClaimStanceCell
	MetricIDs   []string
}

type framingAnalysis struct {
	Applicable bool
	Valid      bool
	Label      string
	Status     EvaluationStatus
	ReasonCode string
}

func Evaluate(input EvaluationInput) (EvaluatedRun, error) {
	prepared := input.Prepared
	if prepared.resultPlansByKey == nil || prepared.itemsByWorkKey == nil {
		return EvaluatedRun{}, fmt.Errorf("prepared evaluation is missing internal indexes; call Prepare before Evaluate")
	}
	if hasFatalIssues(prepared.ReadinessIssues) {
		return EvaluatedRun{}, fmt.Errorf("prepared evaluation %s is not ready", prepared.ID)
	}
	state := evaluationState{
		resultsByID:  map[string]*resultState{},
		capsByEngine: map[string]engine.CapabilityProfile{},
	}
	for _, snapshot := range prepared.EngineCapabilities {
		state.capsByEngine[snapshot.EngineID] = snapshot.Capabilities
	}
	out := EvaluatedRun{
		RunID:                input.RawRun.RunID,
		PreparedEvaluationID: prepared.ID,
		SuiteSnapshots:       append([]SuiteSnapshot(nil), prepared.BoundSuites...),
		EngineCapabilities:   append([]EngineCapabilitySnapshot(nil), prepared.EngineCapabilities...),
	}

	seenWorkKeys := map[string]struct{}{}
	for _, rawResult := range input.RawRun.Results {
		workKey := WorkKey(rawResult.WorkUnit)
		resultPlan, ok := prepared.resultPlansByKey[workKey]
		if !ok {
			return EvaluatedRun{}, fmt.Errorf("raw result work key %q was not in prepared evaluation", workKey)
		}
		if _, dup := seenWorkKeys[workKey]; dup {
			return EvaluatedRun{}, fmt.Errorf("raw result work key %q appeared more than once", workKey)
		}
		seenWorkKeys[workKey] = struct{}{}
		item := prepared.itemsByWorkKey[workKey]
		resID := resultID(input.RawRun.RunID, workKey)
		evaluated := EvaluatedResult{
			ResultID: resID,
			WorkKey:  workKey,
			Raw:      cloneRawResult(rawResult),
		}
		rs := &resultState{
			Result:    evaluated,
			Plan:      resultPlan,
			Item:      item,
			MetricIDs: resultPlan.MetricIDs,
		}
		state.results = append(state.results, rs)
		state.resultsByID[resID] = rs
	}

	for _, rs := range state.results {
		runPrimitiveEvaluators(&state, prepared, rs)
	}
	if err := runAuditJudgeEvaluators(contextOrBackground(input), &state, prepared, input); err != nil {
		return EvaluatedRun{}, err
	}
	if err := runRecognitionEvaluator(contextOrBackground(input), &state, prepared, input); err != nil {
		return EvaluatedRun{}, err
	}
	if err := runFramingEvaluator(contextOrBackground(input), &state, prepared, input); err != nil {
		return EvaluatedRun{}, err
	}
	addStabilityRecords(&state, prepared)

	out.Results = make([]EvaluatedResult, 0, len(state.results))
	for _, rs := range state.results {
		out.Results = append(out.Results, rs.Result)
	}
	out.Evaluations = append([]EvaluationRecord(nil), state.evaluations...)
	out.EvaluationCounts = summarizeEvaluationCounts(out.Evaluations)
	out.Metrics = aggregateMetrics(prepared, &state)
	sortEvaluatedRun(&out)
	out.EvaluationID = "evaluation_" + stableHash(struct {
		RunID    string
		Prepared string
		Results  []EvaluatedResult
		Records  []EvaluationRecord
		Counts   []EvaluationCount
		Metrics  []MetricValue
	}{RunID: out.RunID, Prepared: out.PreparedEvaluationID, Results: out.Results, Records: out.Evaluations, Counts: out.EvaluationCounts, Metrics: out.Metrics})
	return out, nil
}

func runPrimitiveEvaluators(state *evaluationState, prepared PreparedEvaluation, rs *resultState) {
	for _, evaluatorID := range orderedEvaluatorIDs(rs.Plan.EvaluatorIDs, prepared, rs.Plan, []string{EvaluatorMentionMatch, EvaluatorSourceDomain, EvaluatorExactOrAlias, EvaluatorClaimCoverage, EvaluatorClaimStance}) {
		resolved, ok := evaluatorForResult(prepared, rs.Plan, evaluatorID)
		if !ok {
			continue
		}
		if rs.Result.Raw.Status != run.ResultStatusSuccess {
			if resolved.EvaluatorType == EvaluatorSourceDomain {
				caps := state.capsByEngine[rs.Plan.EngineID]
				record, analysis := evaluateSources(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Item, nil, caps)
				record.ReasonCode = runtimeSkipReason(rs.Result.Raw.Status)
				analysis.UnavailableReason = record.ReasonCode
				record.Payload = sourcePayload(rs.Item, analysis)
				rs.Source = &analysis
				state.addRecord(rs, record)
				continue
			}
			if resolved.EvaluatorType == EvaluatorExactOrAlias {
				caps := state.capsByEngine[rs.Plan.EngineID]
				record, cell := skippedBenchmarkCell(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, rs.Item, runtimeSkipReason(rs.Result.Raw.Status), caps)
				rs.Benchmark = &cell
				state.addRecord(rs, record)
				continue
			}
			if resolved.EvaluatorType == EvaluatorClaimCoverage {
				record, cell := skippedClaimCoverage(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, runtimeSkipReason(rs.Result.Raw.Status))
				rs.Coverage = &cell
				state.addRecord(rs, record)
				continue
			}
			if resolved.EvaluatorType == EvaluatorClaimStance {
				record, cell := skippedClaimStance(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, rs.Item, runtimeSkipReason(rs.Result.Raw.Status))
				rs.Stance = &cell
				state.addRecord(rs, record)
				continue
			}
			state.addRecord(rs, skippedRecord(rs.Result.ResultID, resolved, runtimeSkipReason(rs.Result.Raw.Status)))
			continue
		}
		switch resolved.EvaluatorType {
		case EvaluatorMentionMatch:
			record, analysis := evaluateMention(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Item, rs.Result.Raw.WorkUnit.Prompt, answerText(rs.Result.Raw))
			rs.Mention = &analysis
			state.addRecord(rs, record)
		case EvaluatorSourceDomain:
			caps := state.capsByEngine[rs.Plan.EngineID]
			record, analysis := evaluateSources(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Item, rs.Result.Raw.Answer, caps)
			rs.Source = &analysis
			state.addRecord(rs, record)
		case EvaluatorExactOrAlias:
			caps := state.capsByEngine[rs.Plan.EngineID]
			record, cell := evaluateBenchmarkCell(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, rs.Item, rs.Result.Raw, caps)
			rs.Benchmark = &cell
			state.addRecord(rs, record)
		case EvaluatorClaimCoverage:
			record, cell := evaluateClaimCoverage(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, rs.Item, rs.Result.Raw.Answer)
			rs.Coverage = &cell
			state.addRecord(rs, record)
		case EvaluatorClaimStance:
			caps := state.capsByEngine[rs.Plan.EngineID]
			record, cell := evaluateClaimStance(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorVersion, rs.Plan, rs.Item, rs.Result.Raw.Answer, caps)
			rs.Stance = &cell
			state.addRecord(rs, record)
		}
	}
	for _, evaluatorID := range orderedEvaluatorIDs(rs.Plan.EvaluatorIDs, prepared, rs.Plan, []string{EvaluatorFramingSentiment}) {
		resolved, ok := evaluatorForResult(prepared, rs.Plan, evaluatorID)
		if !ok {
			continue
		}
		if rs.Result.Raw.Status != run.ResultStatusSuccess {
			state.addRecord(rs, skippedRecord(rs.Result.ResultID, resolved, runtimeSkipReason(rs.Result.Raw.Status)))
		}
	}
}

func runFramingEvaluator(ctx context.Context, state *evaluationState, prepared PreparedEvaluation, input EvaluationInput) error {
	handler := framingTaskHandler{}
	type scheduledTask struct {
		task   JudgeTask
		result *resultState
		record EvaluationRecord
	}
	var scheduled []scheduledTask
	for _, rs := range state.results {
		for _, evaluatorID := range orderedEvaluatorIDs(rs.Plan.EvaluatorIDs, prepared, rs.Plan, []string{EvaluatorFramingSentiment}) {
			resolved, ok := evaluatorForResult(prepared, rs.Plan, evaluatorID)
			if !ok {
				continue
			}
			if rs.Result.Raw.Status != run.ResultStatusSuccess {
				continue
			}
			base := EvaluationRecord{
				ID:               evaluationID(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorType, resolved.EvaluatorVersion),
				ResultID:         rs.Result.ResultID,
				DeclarationID:    resolved.DeclarationID,
				EvaluatorType:    resolved.EvaluatorType,
				EvaluatorVersion: resolved.EvaluatorVersion,
				ModelAssisted:    true,
				PayloadType:      EvaluatorFramingSentiment,
			}
			if !prepared.ModelAssisted.Enabled {
				base.Status = EvaluationStatusSkipped
				base.ReasonCode = ReasonModelAssistedDisabled
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			if rs.Mention == nil || !rs.Mention.Brand.Mentioned {
				base.Status = EvaluationStatusSkipped
				base.ReasonCode = ReasonEvaluatorNotApplicable
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			base.Status = EvaluationStatusSkipped
			base.ReasonCode = ReasonJudgeError
			disclosed, err := handler.buildDisclosedInput(base, rs.Result, rs.Item, input.DisclosureRedactor)
			if err != nil {
				base.Error = &EvaluationError{Code: ReasonJudgeError, Message: err.Error()}
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Applicable: true, Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			if len(disclosed) > maxDisclosedInputBytes {
				base.Status = EvaluationStatusSkipped
				base.ReasonCode = ReasonDisclosedInputTooLarge
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Applicable: true, Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			prompt, err := handler.RenderPrompt(disclosed)
			if err != nil {
				base.Error = &EvaluationError{Code: ReasonJudgeError, Message: err.Error()}
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Applicable: true, Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			if len(prompt) > maxJudgePromptBytes {
				base.Status = EvaluationStatusSkipped
				base.ReasonCode = ReasonJudgePromptTooLarge
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Applicable: true, Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			if input.JudgeEngine == nil {
				base.Status = EvaluationStatusError
				base.ReasonCode = ReasonJudgeError
				base.Error = &EvaluationError{Code: ReasonJudgeError, Message: "judge engine is not available"}
				base.Payload = framingPayload(base, nil)
				rs.Framing = &framingAnalysis{Applicable: true, Status: base.Status, ReasonCode: base.ReasonCode}
				state.addRecord(rs, base)
				continue
			}
			task := JudgeTask{
				ID:              "judge_task_" + stableID(base.ID, handler.TemplateID(), handler.TemplateVersion()),
				EvaluationID:    base.ID,
				TemplateID:      handler.TemplateID(),
				TemplateVersion: handler.TemplateVersion(),
				RequestMode:     engine.ToolPolicyMemory.String(),
				DisclosedInput:  disclosed,
				OutputSchema:    handler.OutputSchema(),
				RenderedPrompt:  prompt,
			}
			scheduled = append(scheduled, scheduledTask{task: task, result: rs, record: base})
		}
	}
	if len(scheduled) == 0 {
		return nil
	}
	tasks := make([]JudgeTask, 0, len(scheduled))
	for _, scheduledTask := range scheduled {
		tasks = append(tasks, scheduledTask.task)
	}
	results, err := ExecuteJudgeTasks(ctx, input.JudgeEngine, tasks, handler, JudgeExecuteOptions{
		MaxConcurrency: effectiveJudgeConcurrency(input.MaxConcurrency),
		MaxRetries:     prepared.ModelAssisted.RetryAttemptsPerWorkUnit,
		Now:            input.Now,
		Sleep:          input.Sleep,
	})
	if err != nil {
		return err
	}
	byTask := map[string]JudgeResult{}
	for _, result := range results {
		byTask[result.TaskID] = result
	}
	for _, scheduledTask := range scheduled {
		judgeResult := byTask[scheduledTask.task.ID]
		record := scheduledTask.record
		record.Status = judgeResult.Status
		record.ReasonCode = judgeResult.ReasonCode
		record.Values = judgeResult.Parsed.NormalizedOutput
		record.Evidence = judgeResult.Parsed.Evidence
		record.Warnings = judgeResult.Record.Warnings
		record.Error = judgeResult.Error
		record.JudgeRecord = &judgeResult.Record
		record.Payload = framingPayload(record, &judgeResult.Record)
		framing := &framingAnalysis{Applicable: true, Status: record.Status, ReasonCode: record.ReasonCode}
		if record.Status == EvaluationStatusCompleted {
			if scalar, ok := record.Values["label"]; ok && scalar.String != nil {
				framing.Valid = true
				framing.Label = *scalar.String
			}
		}
		scheduledTask.result.Framing = framing
		state.addRecord(scheduledTask.result, record)
	}
	return nil
}

func addStabilityRecords(state *evaluationState, prepared PreparedEvaluation) {
	type stabilityGroup struct {
		Record  EvaluationRecord
		Payload StabilityPayload
	}
	groups := map[string]*stabilityGroup{}
	for _, rs := range state.results {
		for _, evaluatorID := range rs.Plan.EvaluatorIDs {
			resolved, ok := evaluatorForResult(prepared, rs.Plan, evaluatorID)
			if !ok || resolved.EvaluatorType != EvaluatorStability {
				continue
			}
			for _, scope := range ordinaryScopes(rs, itemVariantScopeLevels) {
				scopeKey := stabilityScopeKey(scope, resolved.DeclarationID, resolved.EvaluatorType)
				group := groups[scopeKey]
				if group == nil {
					group = &stabilityGroup{
						Record: EvaluationRecord{
							ID:                "eval_" + stableID(state.results[0].Result.ResultID, scopeKey),
							SuiteID:           rs.Plan.SuiteID,
							SuiteVersion:      rs.Plan.SuiteVersion,
							ScopeKey:          scopeKey,
							Dimensions:        dimension.Map(scope.Dimensions),
							ComparisonGroupID: scope.ComparisonGroupID,
							DeclarationID:     resolved.DeclarationID,
							EvaluatorType:     resolved.EvaluatorType,
							EvaluatorVersion:  resolved.EvaluatorVersion,
							Status:            EvaluationStatusCompleted,
							PayloadType:       EvaluatorStability,
						},
						Payload: StabilityPayload{Status: "completed"},
					}
					groups[scopeKey] = group
				}
				group.Payload.ComparedResultIDs = append(group.Payload.ComparedResultIDs, rs.Result.ResultID)
				group.Payload.ComparedVariants = append(group.Payload.ComparedVariants, rs.Plan.VariantID)
				group.Payload.ComparedRepetitions = append(group.Payload.ComparedRepetitions, rs.Plan.Repetition)
			}
		}
	}
	var keys []string
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		group.Payload.ComparedResultIDs = uniqueStrings(slices.Sorted(slices.Values(group.Payload.ComparedResultIDs)))
		group.Payload.ComparedVariants = uniqueStrings(slices.Sorted(slices.Values(group.Payload.ComparedVariants)))
		group.Payload.ComparedRepetitions = uniqueInts(group.Payload.ComparedRepetitions)
		group.Record.Payload = group.Payload
		state.evaluations = append(state.evaluations, group.Record)
	}
}

func (s *evaluationState) addRecord(rs *resultState, record EvaluationRecord) {
	if rs != nil {
		if record.SuiteID == "" {
			record.SuiteID = rs.Plan.SuiteID
		}
		if record.SuiteVersion == 0 {
			record.SuiteVersion = rs.Plan.SuiteVersion
		}
		if record.ScopeKey == "" {
			record.ScopeKey = rs.Plan.WorkKey
		}
		record.Dimensions = dimension.Map(rs.Plan.Dimensions)
		record.ComparisonGroupID = rs.Plan.ComparisonGroupID
	}
	s.evaluations = append(s.evaluations, record)
	if rs != nil {
		rs.Result.EvaluationIDs = append(rs.Result.EvaluationIDs, record.ID)
	}
}

func skippedRecord(resultID string, evaluator ResolvedEvaluator, reason string) EvaluationRecord {
	return EvaluationRecord{
		ID:               evaluationID(resultID, evaluator.DeclarationID, evaluator.EvaluatorType, evaluator.EvaluatorVersion),
		ResultID:         resultID,
		DeclarationID:    evaluator.DeclarationID,
		EvaluatorType:    evaluator.EvaluatorType,
		EvaluatorVersion: evaluator.EvaluatorVersion,
		Status:           EvaluationStatusSkipped,
		ReasonCode:       reason,
		PayloadType:      evaluator.EvaluatorType,
		ModelAssisted:    evaluator.ModelAssisted,
	}
}

func framingPayload(record EvaluationRecord, judge *JudgeRecord) FramingSentimentPayload {
	payload := FramingSentimentPayload{
		ParseStatus: "not_run",
		ReasonCode:  record.ReasonCode,
		EvidenceIDs: evidenceIDs(record.Evidence),
	}
	if judge != nil && record.Status == EvaluationStatusCompleted {
		payload.ParseStatus = "parsed"
	} else if record.Status == EvaluationStatusError {
		if record.ReasonCode == ReasonJudgeParseError {
			payload.ParseStatus = "schema_mismatch"
		} else {
			payload.ParseStatus = "judge_error"
		}
	}
	if scalar, ok := record.Values["label"]; ok && scalar.String != nil {
		payload.Label = *scalar.String
	}
	if scalar, ok := record.Values["phrase"]; ok && scalar.String != nil {
		payload.Phrase = *scalar.String
	}
	return payload
}

func stabilityScopeKey(scope MetricScope, declarationID, evaluatorType string) string {
	return strings.Join([]string{
		"declaration_id=" + declarationID,
		"evaluator_type=" + evaluatorType,
		"metric_scope=" + scope.Key,
	}, "|")
}

func orderedEvaluatorIDs(ids []string, prepared PreparedEvaluation, plan ResultPlan, types []string) []string {
	typeOrder := map[string]int{}
	for i, typ := range types {
		typeOrder[typ] = i
	}
	var out []string
	for _, id := range ids {
		evaluator, ok := evaluatorForResult(prepared, plan, id)
		if !ok {
			continue
		}
		if _, ok := typeOrder[evaluator.EvaluatorType]; ok {
			out = append(out, id)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, _ := evaluatorForResult(prepared, plan, out[i])
		right, _ := evaluatorForResult(prepared, plan, out[j])
		if typeOrder[left.EvaluatorType] == typeOrder[right.EvaluatorType] {
			return left.DeclarationID < right.DeclarationID
		}
		return typeOrder[left.EvaluatorType] < typeOrder[right.EvaluatorType]
	})
	return out
}

func evaluatorForResult(prepared PreparedEvaluation, plan ResultPlan, declarationID string) (ResolvedEvaluator, bool) {
	evaluator, ok := prepared.evaluatorByKey[evaluatorKey(plan.SuiteID, plan.SuiteVersion, declarationID)]
	return evaluator, ok
}

func runtimeSkipReason(status string) string {
	switch status {
	case run.ResultStatusUnfinished:
		return ReasonUnfinishedAnswer
	case run.ResultStatusError:
		return ReasonProviderError
	case run.ResultStatusCancelled:
		return ReasonCancelled
	case run.ResultStatusNotStarted:
		return ReasonNotStarted
	default:
		return ReasonEvaluatorNotApplicable
	}
}

func answerText(result run.Result) string {
	if result.Answer == nil {
		return ""
	}
	return result.Answer.Text
}

func contextOrBackground(input EvaluationInput) context.Context {
	if input.Context != nil {
		return input.Context
	}
	return context.Background()
}

func effectiveJudgeConcurrency(value int) int {
	if value <= 0 {
		value = 1
	}
	if value > 2 {
		value = 2
	}
	return value
}

func summarizeEvaluationCounts(records []EvaluationRecord) []EvaluationCount {
	type key struct {
		declarationID    string
		evaluatorType    string
		evaluatorVersion string
		status           EvaluationStatus
		reasonCode       string
	}
	counts := map[key]int{}
	for _, record := range records {
		k := key{
			declarationID:    record.DeclarationID,
			evaluatorType:    record.EvaluatorType,
			evaluatorVersion: record.EvaluatorVersion,
			status:           record.Status,
			reasonCode:       record.ReasonCode,
		}
		counts[k]++
	}
	out := make([]EvaluationCount, 0, len(counts))
	for k, count := range counts {
		out = append(out, EvaluationCount{
			DeclarationID:    k.declarationID,
			EvaluatorType:    k.evaluatorType,
			EvaluatorVersion: k.evaluatorVersion,
			Status:           k.status,
			ReasonCode:       k.reasonCode,
			Count:            count,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DeclarationID != out[j].DeclarationID {
			return out[i].DeclarationID < out[j].DeclarationID
		}
		if out[i].EvaluatorType != out[j].EvaluatorType {
			return out[i].EvaluatorType < out[j].EvaluatorType
		}
		if out[i].EvaluatorVersion != out[j].EvaluatorVersion {
			return out[i].EvaluatorVersion < out[j].EvaluatorVersion
		}
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].ReasonCode < out[j].ReasonCode
	})
	return out
}

func sortEvaluatedRun(out *EvaluatedRun) {
	sort.SliceStable(out.Results, func(i, j int) bool { return out.Results[i].WorkKey < out.Results[j].WorkKey })
	for i := range out.Results {
		sort.Strings(out.Results[i].EvaluationIDs)
	}
	sort.SliceStable(out.Evaluations, func(i, j int) bool {
		if out.Evaluations[i].DeclarationID == out.Evaluations[j].DeclarationID {
			return out.Evaluations[i].ID < out.Evaluations[j].ID
		}
		return out.Evaluations[i].DeclarationID < out.Evaluations[j].DeclarationID
	})
	sort.SliceStable(out.Metrics, func(i, j int) bool {
		if out.Metrics[i].MetricID == out.Metrics[j].MetricID {
			return out.Metrics[i].ID < out.Metrics[j].ID
		}
		return out.Metrics[i].MetricID < out.Metrics[j].MetricID
	})
	sort.SliceStable(out.EvaluationCounts, func(i, j int) bool {
		if out.EvaluationCounts[i].DeclarationID != out.EvaluationCounts[j].DeclarationID {
			return out.EvaluationCounts[i].DeclarationID < out.EvaluationCounts[j].DeclarationID
		}
		if out.EvaluationCounts[i].Status != out.EvaluationCounts[j].Status {
			return out.EvaluationCounts[i].Status < out.EvaluationCounts[j].Status
		}
		return out.EvaluationCounts[i].ReasonCode < out.EvaluationCounts[j].ReasonCode
	})
}

func nowFunc(fn func() time.Time) func() time.Time {
	if fn != nil {
		return fn
	}
	return time.Now
}
