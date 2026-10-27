package evaluate

import (
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

func evaluateBenchmarkCell(resultID, declarationID, version string, plan ResultPlan, item suite.Item, raw run.Result, caps engine.CapabilityProfile) (EvaluationRecord, benchmark.CellPayload) {
	evalID := evaluationID(resultID, declarationID, EvaluatorExactOrAlias, version)
	payload := baseBenchmarkPayload(plan, item, caps, raw.Answer)
	record := EvaluationRecord{
		ID:               evalID,
		ResultID:         resultID,
		DeclarationID:    declarationID,
		EvaluatorType:    EvaluatorExactOrAlias,
		EvaluatorVersion: version,
		PayloadType:      benchmark.PayloadTypeCell,
	}
	confidencePolicy := benchmark.ConfidencePolicyOptional
	if item.Grading != nil && item.Grading.Confidence != "" {
		confidencePolicy = item.Grading.Confidence
	}
	text := ""
	if raw.Answer != nil {
		text = raw.Answer.Text
	}
	parsed, err := benchmark.ParseAnswer(text, confidencePolicy)
	if err != nil {
		payload.ParseStatus = benchmark.ParseStatusError
		payload.Warnings = mergeWarnings(payload.Warnings, parsed.Warnings)
		record.Status = EvaluationStatusError
		record.ReasonCode = benchmark.ReasonExactAnswerParseError
		record.Error = &EvaluationError{Code: benchmark.ReasonExactAnswerParseError, Message: "exact answer output could not be parsed"}
		record.Payload = payload
		record.Warnings = evaluationWarningsFromCodes(payload.Warnings)
		return record, payload
	}
	match := benchmark.MatchAnswer(parsed, benchmarkMatchConfig(item), payload.SuiteFamily)
	payload.ParseStatus = parsed.ParseStatus
	payload.AnswerStatus = stringPtr(match.AnswerStatus)
	payload.SanitizedAnswerPreview = parsed.SanitizedAnswerPreview
	payload.AnswerPreviewTruncated = parsed.AnswerPreviewTruncated
	payload.MatchKind = match.MatchKind
	payload.MatchedConfiguredID = match.MatchedConfiguredID
	payload.MatchedConfiguredValue = match.MatchedConfiguredValue
	payload.FailureClass = match.FailureClass
	payload.Confidence = parsed.Confidence
	payload.OverconfidentWrong = match.OverconfidentWrong
	payload.Warnings = mergeWarnings(payload.Warnings, parsed.Warnings, match.Warnings)
	if payload.Temporal != nil {
		if match.FailureClass == benchmark.FailureClassStaleAnswer {
			payload.Temporal.StaleAnswerID = match.MatchedConfiguredID
		}
		payload.Temporal.CutoffHonest = benchmark.CutoffHonest(payload.Temporal.ExpectedModeBehavior, payload.Temporal.CutoffLabel, match.AnswerStatus)
	}
	record.Status = EvaluationStatusCompleted
	record.Payload = payload
	record.Warnings = evaluationWarningsFromCodes(payload.Warnings)
	return record, payload
}

func skippedBenchmarkCell(resultID, declarationID, version string, plan ResultPlan, item suite.Item, reason string, caps engine.CapabilityProfile) (EvaluationRecord, benchmark.CellPayload) {
	payload := baseBenchmarkPayload(plan, item, caps, nil)
	payload.ParseStatus = benchmark.ParseStatusUnavailable
	record := EvaluationRecord{
		ID:               evaluationID(resultID, declarationID, EvaluatorExactOrAlias, version),
		ResultID:         resultID,
		DeclarationID:    declarationID,
		EvaluatorType:    EvaluatorExactOrAlias,
		EvaluatorVersion: version,
		Status:           EvaluationStatusSkipped,
		ReasonCode:       reason,
		PayloadType:      benchmark.PayloadTypeCell,
		Payload:          payload,
	}
	return record, payload
}

func baseBenchmarkPayload(plan ResultPlan, item suite.Item, caps engine.CapabilityProfile, answer *engine.Answer) benchmark.CellPayload {
	payload := benchmark.CellPayload{
		PayloadType:          benchmark.PayloadTypeCell,
		PayloadSchemaVersion: benchmark.PayloadSchemaVersion,
		SuiteFamily:          suiteFamily(plan.SuiteID, item),
		ParseStatus:          benchmark.ParseStatusUnavailable,
		MatchKind:            benchmark.MatchKindNone,
		Warnings:             []string{},
	}
	switch payload.SuiteFamily {
	case benchmark.SuiteFamilyProgramming:
		stack := benchmark.ProgrammingStack{}
		if item.Stack != nil {
			stack = benchmark.ProgrammingStack{
				Language:  item.Stack.Language,
				Ecosystem: item.Stack.Ecosystem,
				Library:   item.Stack.Library,
				API:       item.Stack.API,
			}
		}
		payload.Programming = &benchmark.ProgrammingExtensionV1{
			Subtype: item.Type,
			Stack:   stack,
		}
	case benchmark.SuiteFamilyTemporal:
		expected := item.ExpectedModeBehavior[plan.Mode]
		if expected == "" {
			expected = benchmark.ExpectedNotRequired
		}
		cutoffLabel, cutoffWarning := benchmark.KnowledgeCutoffLabel(caps.KnowledgeCutoff, item.EventDate)
		if cutoffWarning != "" {
			payload.Warnings = append(payload.Warnings, cutoffWarning)
		}
		sourceStatus := benchmark.DeriveSourceEvidence(answer, caps, plan.Mode, sourceExpectation(item))
		payload.Warnings = mergeWarnings(payload.Warnings, sourceStatus.Warnings)
		payload.Temporal = &benchmark.TemporalExtensionV1{
			TemporalType:                 item.Type,
			EventDate:                    item.EventDate,
			RawKnowledgeCutoff:           caps.KnowledgeCutoff,
			CutoffLabel:                  cutoffLabel,
			ExpectedModeBehavior:         expected,
			SourceRequirementStatus:      sourceStatus.RequirementStatus,
			SourceFreshnessStatus:        sourceStatus.FreshnessStatus,
			ValidDatedSourceCount:        sourceStatus.ValidDatedSourceCount,
			InvalidDatedSourceCount:      sourceStatus.InvalidDatedSourceCount,
			FreshestNormalizedSourceDate: sourceStatus.FreshestNormalizedSourceDate,
			DatedSourceChannels:          append([]string(nil), sourceStatus.DatedSourceChannels...),
		}
	}
	payload.Warnings = uniqueStrings(payload.Warnings)
	return payload
}

func suiteFamily(suiteID string, item suite.Item) string {
	switch {
	case strings.HasPrefix(item.Type, "programming."):
		return benchmark.SuiteFamilyProgramming
	case strings.HasPrefix(item.Type, "temporal."):
		return benchmark.SuiteFamilyTemporal
	case strings.HasPrefix(suiteID, "programming."):
		return benchmark.SuiteFamilyProgramming
	case strings.HasPrefix(suiteID, "temporal."):
		return benchmark.SuiteFamilyTemporal
	default:
		return benchmark.SuiteFamilyFactoid
	}
}

func benchmarkMatchConfig(item suite.Item) benchmark.MatchConfig {
	out := benchmark.MatchConfig{}
	if item.Answers != nil {
		out.Canonical = item.Answers.Canonical
		out.Aliases = append([]string(nil), item.Answers.Aliases...)
	}
	for _, row := range item.KnownWrongAnswers {
		out.KnownIncorrect = append(out.KnownIncorrect, benchmark.ConfiguredAnswer{ID: row.ID, Aliases: append([]string(nil), row.Aliases...), Class: benchmark.FailureClassIncorrect})
	}
	for _, row := range item.FailureCases {
		out.FailureCases = append(out.FailureCases, benchmark.ConfiguredAnswer{ID: row.ID, Aliases: append([]string(nil), row.Aliases...), Class: row.Class})
	}
	for _, row := range item.StaleAnswers {
		out.StaleAnswers = append(out.StaleAnswers, benchmark.ConfiguredAnswer{ID: row.ID, Aliases: append([]string(nil), row.Aliases...), Class: benchmark.FailureClassStaleAnswer})
	}
	return out
}

func sourceExpectation(item suite.Item) benchmark.SourceExpectation {
	if item.SourceExpectation == nil {
		return benchmark.SourceExpectation{}
	}
	return benchmark.SourceExpectation{
		RequiredForModes:       append([]string(nil), item.SourceExpectation.RequiredForModes...),
		FreshnessReferenceDate: item.SourceExpectation.FreshnessReferenceDate,
	}
}

func evaluationWarningsFromCodes(codes []string) []EvaluationWarning {
	out := make([]EvaluationWarning, 0, len(codes))
	for _, code := range codes {
		out = append(out, EvaluationWarning{Code: code, Message: code})
	}
	return out
}

func mergeWarnings(groups ...[]string) []string {
	var all []string
	for _, group := range groups {
		all = append(all, group...)
	}
	return slices.Sorted(slices.Values(uniqueStrings(all)))
}

func exactCellEligible(cell *benchmark.CellPayload) bool {
	if cell == nil || cell.AnswerStatus == nil {
		return false
	}
	return cell.ParseStatus == benchmark.ParseStatusParsed || cell.ParseStatus == benchmark.ParseStatusPlainTextFallback
}

func exactCellCorrect(cell *benchmark.CellPayload) bool {
	return exactCellEligible(cell) && *cell.AnswerStatus == benchmark.AnswerStatusCorrect
}

func samePairIdentity(a, b *resultState) bool {
	return a.Plan.SuiteID == b.Plan.SuiteID &&
		a.Plan.SuiteVersion == b.Plan.SuiteVersion &&
		a.Plan.ItemID == b.Plan.ItemID &&
		a.Plan.VariantID == b.Plan.VariantID &&
		a.Plan.EngineID == b.Plan.EngineID &&
		a.Plan.Repetition == b.Plan.Repetition &&
		a.Plan.ComparisonGroupID == b.Plan.ComparisonGroupID &&
		dimension.Equal(a.Plan.Dimensions, b.Plan.Dimensions)
}

func stringPtr(value string) *string {
	return &value
}
