package evaluate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/engine"
	recognition "github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

type recognitionAnalysis struct {
	Cell recognition.CellPayload
}

type recognitionScheduledTask struct {
	task   JudgeTask
	result *resultState
	record EvaluationRecord
}

func runRecognitionEvaluator(ctx context.Context, state *evaluationState, prepared PreparedEvaluation, input EvaluationInput) error {
	handler := newRecognitionTaskHandler()
	var records []EvaluationRecord
	var scheduled []recognitionScheduledTask
	for _, rs := range state.results {
		for _, evaluatorID := range orderedEvaluatorIDs(rs.Plan.EvaluatorIDs, prepared, rs.Plan, []string{EvaluatorEntityCluster}) {
			resolved, ok := evaluatorForResult(prepared, rs.Plan, evaluatorID)
			if !ok {
				continue
			}
			record, cell := evaluateRecognitionCell(rs, resolved)
			rs.Recognition = &recognitionAnalysis{Cell: cell}
			if shouldScheduleRecognitionAssist(prepared, cell) {
				task, fallbackReason, ok := buildRecognitionJudgeTask(handler, record, rs, input)
				if ok {
					scheduled = append(scheduled, recognitionScheduledTask{task: task, result: rs, record: record})
					continue
				}
				cell.AssistStatus = recognition.AssistStatusFallback
				cell.AssistReason = fallbackReason
				record.Payload = cell
				rs.Recognition.Cell = cell
			}
			records = append(records, record)
		}
	}
	if len(scheduled) > 0 {
		results, err := ExecuteJudgeTasks(ctx, input.JudgeEngine, recognitionTasks(scheduled), handler, JudgeExecuteOptions{
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
			cell := scheduledTask.result.Recognition.Cell
			applyRecognitionJudgeResult(&cell, judgeResult)
			record.Payload = cell
			record.JudgeRecord = &judgeResult.Record
			scheduledTask.result.Recognition.Cell = cell
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].SuiteID != records[j].SuiteID {
			return records[i].SuiteID < records[j].SuiteID
		}
		if records[i].ScopeKey != records[j].ScopeKey {
			return records[i].ScopeKey < records[j].ScopeKey
		}
		return records[i].ID < records[j].ID
	})
	for _, record := range records {
		state.evaluations = append(state.evaluations, record)
		if record.ResultID != "" {
			if rs := state.resultsByID[record.ResultID]; rs != nil {
				rs.Result.EvaluationIDs = append(rs.Result.EvaluationIDs, record.ID)
			}
		}
	}
	addRecognitionSummaries(state, prepared)
	return nil
}

func evaluateRecognitionCell(rs *resultState, resolved ResolvedEvaluator) (EvaluationRecord, recognition.CellPayload) {
	record := EvaluationRecord{
		ID:                evaluationID(rs.Result.ResultID, resolved.DeclarationID, resolved.EvaluatorType, resolved.EvaluatorVersion),
		SuiteID:           rs.Plan.SuiteID,
		SuiteVersion:      rs.Plan.SuiteVersion,
		ResultID:          rs.Result.ResultID,
		ScopeKey:          rs.Plan.WorkKey,
		Dimensions:        dimension.Map(rs.Plan.Dimensions),
		ComparisonGroupID: rs.Plan.ComparisonGroupID,
		DeclarationID:     resolved.DeclarationID,
		EvaluatorType:     resolved.EvaluatorType,
		EvaluatorVersion:  resolved.EvaluatorVersion,
		PayloadType:       recognition.PayloadTypeCell,
		ModelAssisted:     resolved.ModelAssisted,
	}
	cell := baseRecognitionCell(rs)
	if rs.Result.Raw.Status != run.ResultStatusSuccess {
		reason := runtimeSkipReason(rs.Result.Raw.Status)
		record.Status = EvaluationStatusSkipped
		record.ReasonCode = reason
		cell.ParserStatus = recognition.ParserStatusUnavailable
		cell.UnavailableReason = recognition.PtrString(reason)
		cell.AssistStatus = recognition.AssistStatusNotApplicable
		cell.AssistReason = recognition.AssistReasonNone
		record.Payload = cell
		return record, cell
	}
	parsed, err := recognition.ParseAnswer(answerText(rs.Result.Raw), rs.Plan.WorkKey)
	if err != nil {
		record.Status = EvaluationStatusError
		record.ReasonCode = ReasonRecognitionParseError
		record.Error = &EvaluationError{Code: ReasonRecognitionParseError, Message: "recognition answer did not match the required JSON schema"}
		parseError := recognition.SafeParseError(err)
		cell.ParserStatus = recognition.ParserStatusError
		cell.UnavailableReason = recognition.PtrString(ReasonRecognitionParseError)
		cell.ParseError = &parseError
		cell.AssistStatus = recognition.AssistStatusNotApplicable
		cell.AssistReason = recognition.AssistReasonNone
		record.Payload = cell
		return record, cell
	}
	config := recognitionConfigFromItem(rs.Item)
	clustered := recognition.ClusterResults(config, parsed.Results)
	targetRecognized := recognition.TargetRecognized(clustered.Assignments)
	cell.ParserStatus = recognition.ParserStatusCompleted
	cell.ParserWarnings = parsed.Warnings
	cell.ClusterWarnings = clustered.Warnings
	cell.ParsedResults = parsed.Results
	cell.Assignments = clustered.Assignments
	cell.TargetRecognized = recognition.PtrBool(targetRecognized)
	if len(recognition.UnresolvedAssignments(cell.Assignments)) == 0 {
		cell.AssistStatus = recognition.AssistStatusNotApplicable
		cell.AssistReason = recognition.AssistReasonNone
	} else {
		cell.AssistStatus = recognition.AssistStatusDisabled
		cell.AssistReason = recognition.AssistReasonModelAssistedDisabled
	}
	record.Status = EvaluationStatusCompleted
	record.Payload = cell
	return record, cell
}

func baseRecognitionCell(rs *resultState) recognition.CellPayload {
	return recognition.CellPayload{
		PayloadType:          recognition.PayloadTypeCell,
		PayloadSchemaVersion: recognition.PayloadSchemaVersion,
		ResultKey:            rs.Plan.WorkKey,
		SuiteID:              rs.Plan.SuiteID,
		SuiteVersion:         rs.Plan.SuiteVersion,
		ItemID:               rs.Plan.ItemID,
		VariantID:            rs.Plan.VariantID,
		Mode:                 rs.Plan.Mode,
		EngineID:             rs.Plan.EngineID,
		Repetition:           rs.Plan.Repetition,
		Dimensions:           dimension.Map(rs.Plan.Dimensions),
		ComparisonGroupID:    recognition.PtrString(rs.Plan.ComparisonGroupID),
		ParserWarnings:       []recognition.Warning{},
		ClusterWarnings:      []recognition.Warning{},
		ParsedResults:        []recognition.ParsedResult{},
		Assignments:          []recognition.Assignment{},
		AssistReason:         recognition.AssistReasonNone,
	}
}

func shouldScheduleRecognitionAssist(prepared PreparedEvaluation, cell recognition.CellPayload) bool {
	return prepared.ModelAssisted.Enabled &&
		cell.ParserStatus == recognition.ParserStatusCompleted &&
		len(recognition.UnresolvedAssignments(cell.Assignments)) > 0
}

func buildRecognitionJudgeTask(handler *recognitionTaskHandler, record EvaluationRecord, rs *resultState, input EvaluationInput) (JudgeTask, string, bool) {
	handler.cells[record.ID] = rs.Recognition.Cell
	handler.items[record.ID] = rs.Item
	disclosed, err := handler.BuildDisclosedInput(record, rs.Result)
	if err != nil || len(disclosed) > maxDisclosedInputBytes {
		return JudgeTask{}, recognition.AssistReasonJudgeInputTooLarge, false
	}
	prompt, err := handler.RenderPrompt(disclosed)
	if err != nil || len(prompt) > maxJudgePromptBytes {
		return JudgeTask{}, recognition.AssistReasonJudgePromptTooLarge, false
	}
	task := JudgeTask{
		ID:              "judge_task_" + stableID(record.ID, handler.TemplateID(), handler.TemplateVersion()),
		EvaluationID:    record.ID,
		TemplateID:      handler.TemplateID(),
		TemplateVersion: handler.TemplateVersion(),
		RequestMode:     engine.ToolPolicyMemory.String(),
		DisclosedInput:  disclosed,
		OutputSchema:    handler.OutputSchema(),
		RenderedPrompt:  prompt,
	}
	return task, recognition.AssistReasonNone, true
}

func recognitionTasks(scheduled []recognitionScheduledTask) []JudgeTask {
	out := make([]JudgeTask, 0, len(scheduled))
	for _, task := range scheduled {
		out = append(out, task.task)
	}
	return out
}

func applyRecognitionJudgeResult(cell *recognition.CellPayload, judgeResult JudgeResult) {
	cell.JudgeMetadata = recognitionJudgeMetadata(judgeResult.Record)
	switch judgeResult.Status {
	case EvaluationStatusCompleted:
		decisions := recognitionJudgeDecisions(judgeResult.Parsed.ParsedObject)
		for i := range cell.Assignments {
			decision, ok := decisions[cell.Assignments[i].ParsedResultID]
			if !ok || cell.Assignments[i].AssignmentClassification != recognition.ClassificationUnknownNamesake {
				continue
			}
			cell.Assignments[i].MatchBasis = "judge"
			cell.Assignments[i].JudgeDecisionReason = recognition.PtrString(decision.Reason)
			if decision.Classification == recognition.ClassificationHallucinationCandidate {
				cell.Assignments[i].AssignmentClassification = recognition.ClassificationHallucinationCandidate
			}
		}
		target := recognition.TargetRecognized(cell.Assignments)
		cell.TargetRecognized = recognition.PtrBool(target)
		cell.AssistStatus = recognition.AssistStatusCompleted
		cell.AssistReason = recognition.AssistReasonNone
	case EvaluationStatusSkipped:
		cell.AssistStatus = recognition.AssistStatusFallback
		if judgeResult.ReasonCode == ReasonCancelled {
			cell.AssistReason = recognition.AssistReasonJudgeCancelled
		} else {
			cell.AssistReason = recognition.AssistReasonJudgeRuntimeError
		}
	default:
		cell.AssistStatus = recognition.AssistStatusFallback
		switch judgeResult.ReasonCode {
		case ReasonJudgeDecisionMismatch:
			cell.AssistReason = recognition.AssistReasonJudgeDecisionMismatch
		case ReasonJudgeParseError:
			cell.AssistReason = recognition.AssistReasonJudgeParseError
		default:
			cell.AssistReason = recognition.AssistReasonJudgeRuntimeError
		}
	}
}

func recognitionJudgeMetadata(record JudgeRecord) *recognition.JudgeMetadata {
	if record.TemplateID == "" {
		return nil
	}
	return &recognition.JudgeMetadata{
		TemplateID:      record.TemplateID,
		TemplateVersion: record.TemplateVersion,
		RequestMode:     record.RequestMode,
		JudgeEngineID:   record.JudgeEngineID,
		JudgeProvider:   record.JudgeProvider,
		JudgeModel:      record.JudgeModel,
	}
}

type recognitionJudgeDecision struct {
	Classification string
	Reason         string
}

func recognitionJudgeDecisions(raw json.RawMessage) map[string]recognitionJudgeDecision {
	var parsed struct {
		Decisions []struct {
			ParsedResultID string `json:"parsed_result_id"`
			Classification string `json:"classification"`
			Reason         string `json:"reason"`
		} `json:"decisions"`
	}
	_ = json.Unmarshal(raw, &parsed)
	out := map[string]recognitionJudgeDecision{}
	for _, decision := range parsed.Decisions {
		out[decision.ParsedResultID] = recognitionJudgeDecision{Classification: decision.Classification, Reason: decision.Reason}
	}
	return out
}

func addRecognitionSummaries(state *evaluationState, prepared PreparedEvaluation) {
	groups := map[string]*recognitionSummaryAccumulator{}
	for _, plan := range prepared.recognitionSummaryPlans {
		key := recognitionSummaryPlanKey(plan)
		groups[key] = &recognitionSummaryAccumulator{
			SuiteID:           plan.SuiteID,
			SuiteVersion:      plan.SuiteVersion,
			ItemID:            plan.ItemID,
			VariantID:         plan.VariantID,
			Mode:              plan.Mode,
			Dimensions:        dimension.Map(plan.Dimensions),
			ComparisonGroupID: plan.ComparisonGroupID,
			PlannedWorkKeys:   append([]string(nil), plan.PlannedWorkKeys...),
			SelectedEngineIDs: stringSet(plan.SelectedEngineIDs),
		}
	}
	for _, rs := range state.results {
		if rs.Recognition == nil {
			continue
		}
		key := recognitionSummaryGroupKey(rs.Plan)
		group := groups[key]
		if group == nil {
			group = &recognitionSummaryAccumulator{
				SuiteID:           rs.Plan.SuiteID,
				SuiteVersion:      rs.Plan.SuiteVersion,
				ItemID:            rs.Plan.ItemID,
				VariantID:         rs.Plan.VariantID,
				Mode:              rs.Plan.Mode,
				Dimensions:        dimension.Map(rs.Plan.Dimensions),
				ComparisonGroupID: rs.Plan.ComparisonGroupID,
				SelectedEngineIDs: map[string]struct{}{},
			}
			groups[key] = group
		}
		group.Cells = append(group.Cells, rs.Recognition.Cell)
		group.PlannedWorkKeys = append(group.PlannedWorkKeys, rs.Plan.WorkKey)
		group.SelectedEngineIDs[rs.Plan.EngineID] = struct{}{}
	}
	var keys []string
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		payload := recognition.BuildSummary(recognition.SummaryInput{
			Cells:             group.Cells,
			PlannedWorkKeys:   group.PlannedWorkKeys,
			SelectedEngineIDs: sortedStringSet(group.SelectedEngineIDs),
			SuiteID:           group.SuiteID,
			SuiteVersion:      group.SuiteVersion,
			ItemID:            group.ItemID,
			VariantID:         group.VariantID,
			Mode:              group.Mode,
			Dimensions:        group.Dimensions,
			ComparisonGroupID: group.ComparisonGroupID,
		})
		resolved := firstEntityClusterEvaluator(prepared, group.SuiteID, group.SuiteVersion)
		if resolved.DeclarationID == "" {
			continue
		}
		record := EvaluationRecord{
			ID:                "eval_" + stableID(payload.SummaryScopeKey, resolved.DeclarationID, resolved.EvaluatorType, resolved.EvaluatorVersion),
			SuiteID:           group.SuiteID,
			SuiteVersion:      group.SuiteVersion,
			ScopeKey:          payload.SummaryScopeKey,
			Dimensions:        dimension.Map(group.Dimensions),
			ComparisonGroupID: group.ComparisonGroupID,
			DeclarationID:     resolved.DeclarationID,
			EvaluatorType:     resolved.EvaluatorType,
			EvaluatorVersion:  resolved.EvaluatorVersion,
			Status:            EvaluationStatusCompleted,
			PayloadType:       recognition.PayloadTypeSummary,
			Payload:           payload,
			ModelAssisted:     resolved.ModelAssisted,
		}
		state.evaluations = append(state.evaluations, record)
	}
}

type recognitionSummaryAccumulator struct {
	SuiteID           string
	SuiteVersion      int
	ItemID            string
	VariantID         string
	Mode              string
	Dimensions        map[string]string
	ComparisonGroupID string
	Cells             []recognition.CellPayload
	PlannedWorkKeys   []string
	SelectedEngineIDs map[string]struct{}
}

func recognitionSummaryGroupKey(plan ResultPlan) string {
	return strings.Join([]string{
		plan.SuiteID,
		fmt.Sprintf("%d", plan.SuiteVersion),
		plan.ItemID,
		plan.VariantID,
		plan.Mode,
		dimension.TupleKey(plan.Dimensions),
		plan.ComparisonGroupID,
	}, "\x00")
}

func recognitionSummaryPlanKey(plan RecognitionSummaryPlan) string {
	return strings.Join([]string{
		plan.SuiteID,
		fmt.Sprintf("%d", plan.SuiteVersion),
		plan.ItemID,
		plan.VariantID,
		plan.Mode,
		dimension.TupleKey(plan.Dimensions),
		plan.ComparisonGroupID,
	}, "\x00")
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func firstEntityClusterEvaluator(prepared PreparedEvaluation, suiteID string, suiteVersion int) ResolvedEvaluator {
	for _, evaluator := range prepared.EvaluatorDeclarations {
		if evaluator.SuiteID == suiteID && evaluator.SuiteVersion == suiteVersion && evaluator.EvaluatorType == EvaluatorEntityCluster {
			return evaluator
		}
	}
	return ResolvedEvaluator{}
}

func recognitionConfigFromItem(item suite.Item) recognition.ItemConfig {
	return recognition.ItemConfig{
		Query:               item.Query,
		ExpectedEntities:    recognitionEntities(item.ExpectedEntities),
		KnownWrongNamesakes: recognitionEntities(item.KnownWrongNamesakes),
		Dimensions:          dimension.Map(item.Dimensions),
		ComparisonGroupID:   item.ComparisonGroupID,
	}
}

func recognitionEntities(values []suite.ExpectedEntity) []recognition.ConfiguredEntity {
	out := make([]recognition.ConfiguredEntity, 0, len(values))
	for _, value := range values {
		out = append(out, recognition.ConfiguredEntity{
			CanonicalName:  value.CanonicalName,
			EntityType:     value.EntityType,
			Aliases:        append([]string(nil), value.Aliases...),
			DescriptorsAny: append([]string(nil), value.DescriptorsAny...),
		})
	}
	return out
}

type recognitionTaskHandler struct {
	cells map[string]recognition.CellPayload
	items map[string]suite.Item
}

func newRecognitionTaskHandler() *recognitionTaskHandler {
	return &recognitionTaskHandler{
		cells: map[string]recognition.CellPayload{},
		items: map[string]suite.Item{},
	}
}

func (recognitionTaskHandler) TemplateID() string { return "recognition_hallucination_candidate" }

func (recognitionTaskHandler) TemplateVersion() string { return "1" }

func (recognitionTaskHandler) OutputSchema() engine.OutputSchema {
	return engine.OutputSchema{
		Name:       "recognition_hallucination_candidate",
		Format:     engine.OutputFormatJSONSchema,
		Strict:     true,
		SchemaJSON: []byte(`{"type":"object","additionalProperties":false,"required":["decisions"],"properties":{"decisions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["parsed_result_id","classification","reason"],"properties":{"parsed_result_id":{"type":"string"},"classification":{"type":"string","enum":["unknown_namesake","hallucination_candidate"]},"reason":{"type":"string","minLength":1,"maxLength":240}}}}}}`),
	}
}

func (handler *recognitionTaskHandler) BuildDisclosedInput(record EvaluationRecord, _ EvaluatedResult) (json.RawMessage, error) {
	cell, ok := handler.cells[record.ID]
	if !ok {
		return nil, fmt.Errorf("recognition cell %s is not registered", record.ID)
	}
	item := handler.items[record.ID]
	parsedByID := map[string]recognition.ParsedResult{}
	for _, parsed := range cell.ParsedResults {
		parsedByID[parsed.ParsedResultID] = parsed
	}
	type disclosedResult struct {
		ParsedResultID string `json:"parsed_result_id"`
		Position       int    `json:"position"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Confidence     int    `json:"confidence"`
		Snippet        string `json:"snippet"`
	}
	payload := struct {
		Query   string            `json:"query"`
		Results []disclosedResult `json:"results"`
	}{
		Query: recognition.SanitizeText(item.Query, recognition.MaxNameRunes).Text,
	}
	for _, assignment := range cell.Assignments {
		if assignment.AssignmentClassification != recognition.ClassificationUnknownNamesake {
			continue
		}
		parsed, ok := parsedByID[assignment.ParsedResultID]
		if !ok {
			continue
		}
		payload.Results = append(payload.Results, disclosedResult{
			ParsedResultID: parsed.ParsedResultID,
			Position:       parsed.Position,
			Name:           parsed.Name,
			Type:           parsed.Type,
			Confidence:     parsed.Confidence,
			Snippet:        parsed.Snippet,
		})
	}
	return json.Marshal(payload)
}

func (recognitionTaskHandler) RenderPrompt(disclosedInput json.RawMessage) (string, error) {
	var payload struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(disclosedInput, &payload); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Evaluate only the disclosed recognition rows for internal incoherence or likely fabrication from the row text itself.\n")
	b.WriteString("Do not browse, search, verify existence externally, or infer from private configuration.\n")
	b.WriteString("Return one decision for every parsed_result_id using only unknown_namesake or hallucination_candidate.\n")
	b.WriteString("Query: ")
	b.WriteString(payload.Query)
	b.WriteString("\nDisclosed input JSON:\n")
	b.Write(disclosedInput)
	return b.String(), nil
}

func (recognitionTaskHandler) ParseAndValidate(answerText string, disclosedInput json.RawMessage) (JudgeParsedOutput, []EvaluationWarning, error) {
	var disclosed struct {
		Results []struct {
			ParsedResultID string `json:"parsed_result_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(disclosedInput, &disclosed); err != nil {
		return JudgeParsedOutput{}, nil, err
	}
	expected := map[string]struct{}{}
	for _, row := range disclosed.Results {
		expected[row.ParsedResultID] = struct{}{}
	}
	var parsed struct {
		Decisions []struct {
			ParsedResultID string `json:"parsed_result_id"`
			Classification string `json:"classification"`
			Reason         string `json:"reason"`
		} `json:"decisions"`
	}
	dec := json.NewDecoder(strings.NewReader(answerText))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return JudgeParsedOutput{}, nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return JudgeParsedOutput{}, nil, fmt.Errorf("judge JSON has trailing data")
		}
		return JudgeParsedOutput{}, nil, err
	}
	seen := map[string]struct{}{}
	for i, decision := range parsed.Decisions {
		if _, ok := expected[decision.ParsedResultID]; !ok {
			return JudgeParsedOutput{}, nil, recognitionJudgeDecisionMismatch("judge decision %d references unexpected parsed_result_id", i)
		}
		if _, dup := seen[decision.ParsedResultID]; dup {
			return JudgeParsedOutput{}, nil, recognitionJudgeDecisionMismatch("judge decision %d duplicates parsed_result_id", i)
		}
		seen[decision.ParsedResultID] = struct{}{}
		switch decision.Classification {
		case recognition.ClassificationUnknownNamesake, recognition.ClassificationHallucinationCandidate:
		default:
			return JudgeParsedOutput{}, nil, fmt.Errorf("judge decision %d has unsupported classification", i)
		}
		if recognition.SanitizeText(decision.Reason, 240).Text == "" {
			return JudgeParsedOutput{}, nil, fmt.Errorf("judge decision %d reason is required", i)
		}
		parsed.Decisions[i].Reason = recognition.SanitizeText(decision.Reason, 240).Text
	}
	if len(seen) != len(expected) {
		return JudgeParsedOutput{}, nil, recognitionJudgeDecisionMismatch("judge decisions do not match disclosed parsed_result_id set")
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		return JudgeParsedOutput{}, nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return JudgeParsedOutput{}, nil, err
	}
	return JudgeParsedOutput{ParsedObject: compact.Bytes()}, nil, nil
}

type recognitionJudgeDecisionMismatchError struct {
	message string
}

func (err recognitionJudgeDecisionMismatchError) Error() string {
	return err.message
}

func (recognitionJudgeDecisionMismatchError) JudgeReasonCode() string {
	return ReasonJudgeDecisionMismatch
}

func recognitionJudgeDecisionMismatch(format string, args ...any) error {
	return recognitionJudgeDecisionMismatchError{message: fmt.Sprintf(format, args...)}
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
