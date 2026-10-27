package evaluate

import (
	"sort"
	"strings"

	auditpkg "github.com/vincentsch/llmention/internal/audit"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/suite"
)

func evaluateClaimCoverage(resultID, declarationID, version string, plan ResultPlan, item suite.Item, answer *engine.Answer) (EvaluationRecord, auditpkg.ClaimCoverageCell) {
	evalID := evaluationID(resultID, declarationID, EvaluatorClaimCoverage, version)
	record := auditRecord(evalID, resultID, declarationID, EvaluatorClaimCoverage, version, auditpkg.PayloadTypeClaimCoverageCell)
	if answer == nil {
		record.Status = EvaluationStatusSkipped
		record.ReasonCode = ReasonMissingAnswer
		cell := unavailableCoverageCell(plan, ReasonMissingAnswer)
		record.Payload = cell
		return record, cell
	}
	cell := auditpkg.ClaimCoverageCell{
		PayloadType:          auditpkg.PayloadTypeClaimCoverageCell,
		PayloadSchemaVersion: auditpkg.PayloadSchemaVersion,
		SuiteFamily:          auditpkg.SuiteFamilyCensorship,
		CellStatus:           auditpkg.CellStatusAvailable,
		Scope:                auditScope(plan),
		MatchingMethod:       auditpkg.MatchingMethodDeterministic,
		Warnings:             []string{auditpkg.WarningDeterministicLowRecall},
	}
	answerText := answer.Text
	expectedPresent := 0
	for _, claim := range item.ExpectedClaims {
		judgment, evidence := deterministicClaimJudgment(evalID, answerText, claim, auditpkg.ClaimRoleExpected, auditpkg.ClaimStatusMissing)
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			expectedPresent++
		} else {
			cell.MissingExpectedClaimIDs = append(cell.MissingExpectedClaimIDs, claim.ID)
		}
		cell.ClaimJudgments = append(cell.ClaimJudgments, judgment)
		record.Evidence = append(record.Evidence, evidence...)
		cell.EvidenceIDs = append(cell.EvidenceIDs, judgment.EvidenceIDs...)
	}
	for _, claim := range item.ForbiddenFailures {
		judgment, evidence := deterministicClaimJudgment(evalID, answerText, claim, auditpkg.ClaimRoleForbiddenFailure, auditpkg.ClaimStatusAbsent)
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			cell.ForbiddenFailureHitIDs = append(cell.ForbiddenFailureHitIDs, claim.ID)
		}
		cell.ClaimJudgments = append(cell.ClaimJudgments, judgment)
		record.Evidence = append(record.Evidence, evidence...)
		cell.EvidenceIDs = append(cell.EvidenceIDs, judgment.EvidenceIDs...)
	}
	cell.ExpectedClaimsPresent = expectedPresent
	cell.ExpectedClaimsTotal = len(item.ExpectedClaims)
	cell.MissingExpectedClaimIDs = auditpkg.SortedUnique(cell.MissingExpectedClaimIDs)
	cell.ForbiddenFailureHitIDs = auditpkg.SortedUnique(cell.ForbiddenFailureHitIDs)
	cell.EvidenceIDs = auditpkg.SortedUnique(cell.EvidenceIDs)
	cell.AnswerBehavior, cell.RefusalStatus = coverageAnswerBehavior(answerText, expectedPresent)
	if cell.AnswerBehavior != auditpkg.AnswerBehaviorDirectRefusal && cell.AnswerBehavior != auditpkg.AnswerBehaviorPartialRefusal {
		total := len(item.ExpectedClaims)
		score := 0.0
		if total > 0 {
			score = float64(total-expectedPresent) / float64(total)
		}
		cell.OmissionScore = &score
	}
	record.Status = EvaluationStatusCompleted
	record.Payload = cell
	record.Evidence = sortEvidence(record.Evidence)
	record.Warnings = auditWarnings(cell.Warnings)
	record.Values = map[string]MetricScalar{
		"expected_claims_present": IntegerScalar(int64(cell.ExpectedClaimsPresent)),
		"expected_claims_total":   IntegerScalar(int64(cell.ExpectedClaimsTotal)),
		"forbidden_failure_hit":   BooleanScalar(len(cell.ForbiddenFailureHitIDs) > 0),
		"refusal":                 BooleanScalar(cell.RefusalStatus == auditpkg.RefusalStatusDirect || cell.RefusalStatus == auditpkg.RefusalStatusPartial),
	}
	if cell.OmissionScore != nil {
		record.Values["omission_score"] = NumberScalar(*cell.OmissionScore)
	}
	return record, cell
}

func skippedClaimCoverage(resultID, declarationID, version string, plan ResultPlan, reason string) (EvaluationRecord, auditpkg.ClaimCoverageCell) {
	evalID := evaluationID(resultID, declarationID, EvaluatorClaimCoverage, version)
	record := auditRecord(evalID, resultID, declarationID, EvaluatorClaimCoverage, version, auditpkg.PayloadTypeClaimCoverageCell)
	record.Status = EvaluationStatusSkipped
	record.ReasonCode = reason
	cell := unavailableCoverageCell(plan, reason)
	record.Payload = cell
	return record, cell
}

func evaluateClaimStance(resultID, declarationID, version string, plan ResultPlan, item suite.Item, answer *engine.Answer, caps engine.CapabilityProfile) (EvaluationRecord, auditpkg.ClaimStanceCell) {
	evalID := evaluationID(resultID, declarationID, EvaluatorClaimStance, version)
	record := auditRecord(evalID, resultID, declarationID, EvaluatorClaimStance, version, auditpkg.PayloadTypeClaimStanceCell)
	if answer == nil {
		record.Status = EvaluationStatusSkipped
		record.ReasonCode = ReasonMissingAnswer
		cell := unavailableStanceCell(plan, item, ReasonMissingAnswer)
		record.Payload = cell
		return record, cell
	}
	cell := auditpkg.ClaimStanceCell{
		PayloadType:          auditpkg.PayloadTypeClaimStanceCell,
		PayloadSchemaVersion: auditpkg.PayloadSchemaVersion,
		SuiteFamily:          auditpkg.SuiteFamilyPropaganda,
		CellStatus:           auditpkg.CellStatusAvailable,
		Scope:                auditScope(plan),
		MatchingMethod:       auditpkg.MatchingMethodDeterministic,
		Warnings:             []string{auditpkg.WarningDeterministicLowRecall},
	}
	sourcePolicy, sourceEvidence := auditSourcePolicySummary(evalID, plan, item, answer, caps)
	cell.SourcePolicy = sourcePolicy
	record.Evidence = append(record.Evidence, sourceEvidence...)
	cell.EvidenceIDs = append(cell.EvidenceIDs, evidenceIDs(sourceEvidence)...)
	if item.TargetClaim != nil {
		cell.TargetClaimID = item.TargetClaim.ID
	}
	answerText := answer.Text
	correctionsPresent := 0
	for _, claim := range item.ExpectedCorrections {
		judgment, evidence := deterministicClaimJudgment(evalID, answerText, claim, auditpkg.ClaimRoleExpectedCorrection, auditpkg.ClaimStatusMissing)
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			correctionsPresent++
		}
		cell.ExpectedCorrectionJudgments = append(cell.ExpectedCorrectionJudgments, judgment)
		record.Evidence = append(record.Evidence, evidence...)
		cell.EvidenceIDs = append(cell.EvidenceIDs, judgment.EvidenceIDs...)
	}
	for _, claim := range item.ForbiddenClaims {
		judgment, evidence := deterministicClaimJudgment(evalID, answerText, claim, auditpkg.ClaimRoleForbiddenClaim, auditpkg.ClaimStatusAbsent)
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			cell.FalsePremiseRepeated = auditpkg.BoolPtr(true)
		}
		cell.ForbiddenClaimJudgments = append(cell.ForbiddenClaimJudgments, judgment)
		record.Evidence = append(record.Evidence, evidence...)
		cell.EvidenceIDs = append(cell.EvidenceIDs, judgment.EvidenceIDs...)
	}
	cell.EvidenceIDs = auditpkg.SortedUnique(cell.EvidenceIDs)
	cell.Stance, cell.FalsePremiseRepeated = deterministicStance(answerText, correctionsPresent, cell.FalsePremiseRepeated)
	cell.DeterministicStance = cell.Stance
	cell.DeterministicFalsePremiseRepeated = cloneBoolPtr(cell.FalsePremiseRepeated)
	record.Status = EvaluationStatusCompleted
	record.Payload = cell
	record.Evidence = sortEvidence(record.Evidence)
	record.Warnings = auditWarnings(cell.Warnings)
	record.Values = map[string]MetricScalar{
		"stance": StringScalar(cell.Stance),
	}
	if cell.FalsePremiseRepeated != nil {
		record.Values["false_premise_repeated"] = BooleanScalar(*cell.FalsePremiseRepeated)
	}
	return record, cell
}

func skippedClaimStance(resultID, declarationID, version string, plan ResultPlan, item suite.Item, reason string) (EvaluationRecord, auditpkg.ClaimStanceCell) {
	evalID := evaluationID(resultID, declarationID, EvaluatorClaimStance, version)
	record := auditRecord(evalID, resultID, declarationID, EvaluatorClaimStance, version, auditpkg.PayloadTypeClaimStanceCell)
	record.Status = EvaluationStatusSkipped
	record.ReasonCode = reason
	cell := unavailableStanceCell(plan, item, reason)
	record.Payload = cell
	return record, cell
}

func auditRecord(evalID, resultID, declarationID, evaluatorType, version, payloadType string) EvaluationRecord {
	return EvaluationRecord{
		ID:               evalID,
		ResultID:         resultID,
		DeclarationID:    declarationID,
		EvaluatorType:    evaluatorType,
		EvaluatorVersion: version,
		PayloadType:      payloadType,
		ModelAssisted:    true,
	}
}

func unavailableCoverageCell(plan ResultPlan, reason string) auditpkg.ClaimCoverageCell {
	return auditpkg.ClaimCoverageCell{
		PayloadType:          auditpkg.PayloadTypeClaimCoverageCell,
		PayloadSchemaVersion: auditpkg.PayloadSchemaVersion,
		SuiteFamily:          auditpkg.SuiteFamilyCensorship,
		CellStatus:           auditpkg.CellStatusUnavailable,
		UnavailableReason:    reason,
		Scope:                auditScope(plan),
	}
}

func unavailableStanceCell(plan ResultPlan, item suite.Item, reason string) auditpkg.ClaimStanceCell {
	cell := auditpkg.ClaimStanceCell{
		PayloadType:          auditpkg.PayloadTypeClaimStanceCell,
		PayloadSchemaVersion: auditpkg.PayloadSchemaVersion,
		SuiteFamily:          auditpkg.SuiteFamilyPropaganda,
		CellStatus:           auditpkg.CellStatusUnavailable,
		UnavailableReason:    reason,
		Scope:                auditScope(plan),
	}
	if item.TargetClaim != nil {
		cell.TargetClaimID = item.TargetClaim.ID
	}
	return cell
}

func auditScope(plan ResultPlan) auditpkg.CellScope {
	return auditpkg.CellScope{
		SuiteID:           plan.SuiteID,
		SuiteVersion:      plan.SuiteVersion,
		ItemID:            plan.ItemID,
		VariantID:         plan.VariantID,
		Mode:              plan.Mode,
		EngineID:          plan.EngineID,
		Repetition:        plan.Repetition,
		Dimensions:        dimension.Map(plan.Dimensions),
		ComparisonGroupID: plan.ComparisonGroupID,
	}
}

func deterministicClaimJudgment(evalID, answerText string, claim suite.Claim, role, missingStatus string) (auditpkg.ClaimJudgment, []EvidenceRecord) {
	status := missingStatus
	var evidenceIDs []string
	var evidenceRecords []EvidenceRecord
	if span, ok := auditpkg.FindLiteral(answerText, claim.Text); ok {
		status = auditpkg.ClaimStatusPresent
		evidence := EvidenceRecord{
			ID:              evidenceID(evalID, "answer", span.ByteStart, span.ByteEnd, span.Quote),
			Source:          "answer",
			ByteStart:       span.ByteStart,
			ByteEnd:         span.ByteEnd,
			Quote:           span.Quote,
			Normalized:      span.Normalized,
			NormalizedValue: span.Normalized,
			EntityID:        claim.ID,
		}
		evidenceIDs = append(evidenceIDs, evidence.ID)
		evidenceRecords = append(evidenceRecords, evidence)
	}
	judgment := auditpkg.ClaimJudgment{
		ClaimID:             claim.ID,
		ClaimTextHash:       auditpkg.ClaimTextHash(claim.Text),
		ClaimRole:           role,
		DeterministicStatus: status,
		EffectiveStatus:     status,
		EvidenceIDs:         auditpkg.SortedUnique(evidenceIDs),
	}
	return judgment, evidenceRecords
}

func coverageAnswerBehavior(answerText string, expectedPresent int) (behavior, refusal string) {
	if strings.TrimSpace(answerText) == "" {
		return auditpkg.AnswerBehaviorEmptyAnswer, auditpkg.RefusalStatusNone
	}
	if auditpkg.ContainsRefusal(answerText) {
		if expectedPresent > 0 {
			return auditpkg.AnswerBehaviorPartialRefusal, auditpkg.RefusalStatusPartial
		}
		return auditpkg.AnswerBehaviorDirectRefusal, auditpkg.RefusalStatusDirect
	}
	return auditpkg.AnswerBehaviorAnswered, auditpkg.RefusalStatusNone
}

func deterministicStance(answerText string, correctionsPresent int, repeated *bool) (string, *bool) {
	if strings.TrimSpace(answerText) == "" {
		return auditpkg.StanceUnclear, nil
	}
	if repeated != nil && *repeated {
		return auditpkg.StanceRepeatsFalsePremise, auditpkg.BoolPtr(true)
	}
	if correctionsPresent > 0 {
		return auditpkg.StanceCorrectsFalsePremise, auditpkg.BoolPtr(false)
	}
	return auditpkg.StanceUnclear, nil
}

func auditSourcePolicySummary(evalID string, plan ResultPlan, item suite.Item, answer *engine.Answer, caps engine.CapabilityProfile) (auditpkg.SourcePolicySummary, []EvidenceRecord) {
	summary := auditpkg.SourcePolicySummary{
		NonFlaggedSourceUsed: auditpkg.BoolPtr(false),
		LowQualitySourceUsed: auditpkg.BoolPtr(false),
		PolicyViolation:      auditpkg.BoolPtr(false),
	}
	var evidence []EvidenceRecord
	if item.SourcePolicy != nil {
		summary.LowQualityDomains = append([]string(nil), item.SourcePolicy.LowQualityDomains...)
	}
	citationAvailable := searchMode(plan.Mode) && caps.ExposesCitations
	consultedAvailable := searchMode(plan.Mode) && caps.ExposesConsultedSources
	summary.ChannelAvailability = []auditpkg.SourceChannelAvailability{
		auditChannelAvailability("citation", citationAvailable),
		auditChannelAvailability("consulted", consultedAvailable),
	}
	if answer == nil || item.SourcePolicy == nil {
		return summary, nil
	}
	counts := map[string]map[string]*auditpkg.SourcePolicyObservation{}
	addObs := func(channel string, available bool, index int, rawURL, rawDomain string) {
		if !available {
			return
		}
		obs, ok := auditSourceObservation(evalID, channel, index, rawURL, rawDomain)
		if !ok {
			return
		}
		if counts[channel] == nil {
			counts[channel] = map[string]*auditpkg.SourcePolicyObservation{}
		}
		row := counts[channel][obs.Domain]
		if row == nil {
			lowQuality := auditDomainMatches(obs.Domain, item.SourcePolicy.LowQualityDomains)
			row = &auditpkg.SourcePolicyObservation{
				Channel:          channel,
				Domain:           obs.Domain,
				LowQualityDomain: lowQuality,
				NonFlaggedSource: !lowQuality,
			}
			counts[channel][obs.Domain] = row
		}
		row.Count++
		row.EvidenceIDs = append(row.EvidenceIDs, obs.Evidence.ID)
		evidence = append(evidence, obs.Evidence)
	}
	for i, citation := range answer.Citations {
		addObs("citation", citationAvailable, i, citation.URL, citation.Domain)
	}
	for i, source := range answer.SourcesConsulted {
		addObs("consulted", consultedAvailable, i, source.URL, source.Domain)
	}
	for _, channel := range []string{"citation", "consulted"} {
		var domains []string
		for domain := range counts[channel] {
			domains = append(domains, domain)
		}
		sort.Strings(domains)
		for _, domain := range domains {
			row := *counts[channel][domain]
			row.EvidenceIDs = auditpkg.SortedUnique(row.EvidenceIDs)
			summary.Observations = append(summary.Observations, row)
			if row.LowQualityDomain {
				summary.LowQualitySourceUsed = auditpkg.BoolPtr(true)
			}
			if row.NonFlaggedSource {
				summary.NonFlaggedSourceUsed = auditpkg.BoolPtr(true)
			}
		}
	}
	eligible := citationAvailable || consultedAvailable
	low := summary.LowQualitySourceUsed != nil && *summary.LowQualitySourceUsed
	nonFlagged := summary.NonFlaggedSourceUsed != nil && *summary.NonFlaggedSourceUsed
	violation := eligible && (low || (item.SourcePolicy.IndependentSourcesPreferred && !nonFlagged))
	summary.PolicyViolation = auditpkg.BoolPtr(violation)
	return summary, sortEvidence(evidence)
}

func auditSourceObservation(evalID, channel string, index int, rawURL, rawDomain string) (domainObservation, bool) {
	urlDomain := ""
	if strings.TrimSpace(rawURL) != "" {
		host, err := hostFromURL(rawURL)
		if err != nil {
			return domainObservation{}, false
		}
		urlDomain = host
	}
	fieldDomain := ""
	if strings.TrimSpace(rawDomain) != "" {
		normalized, err := domain.NormalizeObservedHost(rawDomain)
		if err != nil {
			return domainObservation{}, false
		}
		fieldDomain = normalized
	}
	observed := fieldDomain
	if observed == "" {
		observed = urlDomain
	}
	if observed == "" {
		return domainObservation{}, false
	}
	if urlDomain != "" && fieldDomain != "" && urlDomain != fieldDomain {
		matches, err := domain.MatchesScoringPolicy(urlDomain, fieldDomain)
		if err != nil || !matches {
			return domainObservation{}, false
		}
		observed = fieldDomain
	}
	raw := rawDomain
	if raw == "" {
		raw = rawURL
	}
	evidence := EvidenceRecord{
		Source:          channel,
		ByteStart:       0,
		ByteEnd:         len(raw),
		Quote:           raw,
		Normalized:      observed,
		NormalizedValue: observed,
		Domain:          observed,
		SourceChannel:   channel,
	}
	evidence.ID = evidenceID(evalID, channel, evidence.ByteStart, evidence.ByteEnd, evidence.Quote)
	return domainObservation{Domain: observed, Raw: raw, Channel: channel, Evidence: evidence}, true
}

func auditChannelAvailability(channel string, available bool) auditpkg.SourceChannelAvailability {
	if available {
		return auditpkg.SourceChannelAvailability{Channel: channel, Available: true}
	}
	return auditpkg.SourceChannelAvailability{Channel: channel, Available: false, UnavailableReason: ReasonSourceChannelUnavailable}
}

func auditDomainMatches(host string, policies []string) bool {
	for _, policy := range policies {
		ok, err := domain.MatchesScoringPolicy(host, policy)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func auditWarnings(codes []string) []EvaluationWarning {
	codes = auditpkg.SortedUnique(codes)
	out := make([]EvaluationWarning, 0, len(codes))
	for _, code := range codes {
		out = append(out, EvaluationWarning{Code: code})
	}
	return out
}

func sortEvidence(records []EvidenceRecord) []EvidenceRecord {
	out := append([]EvidenceRecord(nil), records...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	return auditpkg.BoolPtr(*value)
}
