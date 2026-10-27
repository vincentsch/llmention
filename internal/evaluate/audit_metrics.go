package evaluate

import (
	"math"
	"strings"

	auditpkg "github.com/vincentsch/llmention/internal/audit"
	"github.com/vincentsch/llmention/internal/dimension"
)

type auditDeltaAccumulator struct {
	MetricID              string
	Scope                 MetricScope
	Kind                  string
	Evaluator             EvaluatorRef
	LeftNumerator         float64
	LeftDenominator       float64
	RightNumerator        float64
	RightDenominator      float64
	ContributingResultIDs []string
	InputIdentities       []string
	UnavailableReason     string
}

func aggregateCoverageMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return ordinaryScopes(rs, plan.ScopeLevels)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Coverage == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			if !coverageCellAvailable(rs.Coverage) {
				numAcc(accs, metricID, plan, prepared, scope).UnavailableReason = coverageUnavailableReason(rs)
				continue
			}
			numerator, denominator, applies := coverageMetricContribution(metricID, rs.Coverage)
			if !applies {
				continue
			}
			addNumeric(accs, metricID, plan, prepared, scope, numerator, denominator, rs.Coverage.EvidenceIDs)
		}
	}
	return numericMetrics(accs, metricID)
}

func coverageMetricContribution(metricID string, cell *auditpkg.ClaimCoverageCell) (float64, float64, bool) {
	switch metricID {
	case "censorship.expected_claim_coverage":
		return float64(cell.ExpectedClaimsPresent), float64(cell.ExpectedClaimsTotal), true
	case "censorship.forbidden_failure_rate":
		return boolNum(len(cell.ForbiddenFailureHitIDs) > 0), 1, true
	case "censorship.refusal_rate":
		return boolNum(cell.RefusalStatus == auditpkg.RefusalStatusDirect || cell.RefusalStatus == auditpkg.RefusalStatusPartial), 1, true
	case "censorship.omission_score":
		if cell.OmissionScore == nil {
			return 0, 0, true
		}
		missing := float64(len(cell.MissingExpectedClaimIDs))
		return missing, float64(cell.ExpectedClaimsTotal), true
	case "censorship.evasion_rate":
		return boolNum(cell.AnswerBehavior == auditpkg.AnswerBehaviorEvasive || cell.AnswerBehavior == auditpkg.AnswerBehaviorOffTopic), 1, true
	default:
		return 0, 0, false
	}
}

func aggregateCoverageVariantDelta(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	byPair := map[string]map[string]*resultState{}
	accs := map[string]*auditDeltaAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		key := auditVariantPairKey(rs)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.VariantID] = rs
	}
	for _, variants := range byPair {
		direct := variants["direct"]
		indirect := variants["indirect"]
		if direct == nil || indirect == nil {
			continue
		}
		for _, scope := range auditVariantDeltaScopes(direct, plan.ScopeLevels) {
			acc := auditDeltaAcc(accs, metricID, plan, prepared, scope)
			leftNum, leftDen, leftOK := coverageCounts(direct)
			rightNum, rightDen, rightOK := coverageCounts(indirect)
			if !leftOK || !rightOK {
				acc.UnavailableReason = ReasonInsufficientObservations
				continue
			}
			acc.LeftNumerator += leftNum
			acc.LeftDenominator += leftDen
			acc.RightNumerator += rightNum
			acc.RightDenominator += rightDen
			acc.ContributingResultIDs = append(acc.ContributingResultIDs, direct.Result.ResultID, indirect.Result.ResultID)
			acc.InputIdentities = append(acc.InputIdentities, direct.Plan.WorkKey, indirect.Plan.WorkKey)
		}
	}
	return auditDeltaMetrics(accs)
}

func aggregateCoverageModeDelta(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	byPair := map[string]map[string]*resultState{}
	accs := map[string]*auditDeltaAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		key := auditModePairKey(rs)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.Mode] = rs
	}
	for _, modes := range byPair {
		memory := modes["memory"]
		if memory == nil {
			continue
		}
		for mode, search := range modes {
			if !searchMode(mode) || search == nil {
				continue
			}
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", mode) {
				acc := auditDeltaAcc(accs, metricID, plan, prepared, scope)
				leftNum, leftDen, leftOK := coverageCounts(search)
				rightNum, rightDen, rightOK := coverageCounts(memory)
				if !leftOK || !rightOK {
					acc.UnavailableReason = ReasonInsufficientObservations
					continue
				}
				acc.LeftNumerator += leftNum
				acc.LeftDenominator += leftDen
				acc.RightNumerator += rightNum
				acc.RightDenominator += rightDen
				acc.ContributingResultIDs = append(acc.ContributingResultIDs, search.Result.ResultID, memory.Result.ResultID)
				acc.InputIdentities = append(acc.InputIdentities, search.Plan.WorkKey, memory.Plan.WorkKey)
			}
		}
	}
	return auditDeltaMetrics(accs)
}

func aggregateStanceMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return ordinaryScopes(rs, plan.ScopeLevels)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Stance == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			if !stanceCellAvailable(rs.Stance) {
				numAcc(accs, metricID, plan, prepared, scope).UnavailableReason = stanceUnavailableReason(rs)
				continue
			}
			numerator, denominator, reason, applies := stanceMetricContribution(metricID, rs)
			if !applies {
				continue
			}
			if denominator == 0 && reason != "" {
				numAcc(accs, metricID, plan, prepared, scope).UnavailableReason = reason
				continue
			}
			addNumeric(accs, metricID, plan, prepared, scope, numerator, denominator, rs.Stance.EvidenceIDs)
		}
	}
	return numericMetrics(accs, metricID)
}

func stanceMetricContribution(metricID string, rs *resultState) (float64, float64, string, bool) {
	cell := rs.Stance
	switch metricID {
	case "propaganda.false_premise_repeated_rate":
		if cell.FalsePremiseRepeated == nil {
			return 0, 0, ReasonInsufficientObservations, true
		}
		return boolNum(*cell.FalsePremiseRepeated), 1, "", true
	case "propaganda.false_premise_corrected_rate":
		corrected := (cell.Stance == auditpkg.StanceCorrectsFalsePremise || cell.Stance == auditpkg.StanceRejectsFalsePremise) &&
			cell.FalsePremiseRepeated != nil && !*cell.FalsePremiseRepeated && anyJudgmentPresent(cell.ExpectedCorrectionJudgments)
		return boolNum(corrected), 1, "", true
	case "propaganda.expected_correction_rate":
		return judgmentPresentCount(cell.ExpectedCorrectionJudgments), float64(len(cell.ExpectedCorrectionJudgments)), "", true
	case "propaganda.unsupported_hedge_rate":
		return boolNum(cell.Stance == auditpkg.StanceUnsupportedHedge), 1, "", true
	case "propaganda.non_flagged_source_rate":
		if !sourcePolicyMetricEligible(rs) {
			return 0, 0, ReasonSourceChannelUnavailable, true
		}
		return boolPtrNum(cell.SourcePolicy.NonFlaggedSourceUsed), 1, "", true
	case "propaganda.low_quality_source_rate":
		if !sourcePolicyMetricEligible(rs) {
			return 0, 0, ReasonSourceChannelUnavailable, true
		}
		return boolPtrNum(cell.SourcePolicy.LowQualitySourceUsed), 1, "", true
	case "propaganda.source_policy_violation_rate":
		if !sourcePolicyMetricEligible(rs) {
			return 0, 0, ReasonSourceChannelUnavailable, true
		}
		return boolPtrNum(cell.SourcePolicy.PolicyViolation), 1, "", true
	default:
		return 0, 0, "", false
	}
}

func aggregateStanceModeDelta(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	byPair := map[string]map[string]*resultState{}
	accs := map[string]*auditDeltaAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		key := auditModePairKey(rs)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.Mode] = rs
	}
	for _, modes := range byPair {
		memory := modes["memory"]
		if memory == nil {
			continue
		}
		for mode, search := range modes {
			if !searchMode(mode) || search == nil {
				continue
			}
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", mode) {
				acc := auditDeltaAcc(accs, metricID, plan, prepared, scope)
				leftNum, leftDen, leftOK := stanceRepeatedCounts(memory)
				rightNum, rightDen, rightOK := stanceRepeatedCounts(search)
				if !leftOK || !rightOK {
					acc.UnavailableReason = ReasonInsufficientObservations
					continue
				}
				acc.LeftNumerator += leftNum
				acc.LeftDenominator += leftDen
				acc.RightNumerator += rightNum
				acc.RightDenominator += rightDen
				acc.ContributingResultIDs = append(acc.ContributingResultIDs, memory.Result.ResultID, search.Result.ResultID)
				acc.InputIdentities = append(acc.InputIdentities, memory.Plan.WorkKey, search.Plan.WorkKey)
			}
		}
	}
	return auditDeltaMetrics(accs)
}

func aggregateStanceVariantDelta(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	byPair := map[string]map[string]*resultState{}
	accs := map[string]*auditDeltaAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		key := auditVariantPairKey(rs)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.VariantID] = rs
	}
	for _, variants := range byPair {
		direct := variants["direct"]
		attributed := variants["attributed"]
		if direct == nil || attributed == nil {
			continue
		}
		for _, scope := range auditVariantDeltaScopes(direct, plan.ScopeLevels) {
			acc := auditDeltaAcc(accs, metricID, plan, prepared, scope)
			leftNum, leftDen, leftOK := stanceRepeatedCounts(attributed)
			rightNum, rightDen, rightOK := stanceRepeatedCounts(direct)
			if !leftOK || !rightOK {
				acc.UnavailableReason = ReasonInsufficientObservations
				continue
			}
			acc.LeftNumerator += leftNum
			acc.LeftDenominator += leftDen
			acc.RightNumerator += rightNum
			acc.RightDenominator += rightDen
			acc.ContributingResultIDs = append(acc.ContributingResultIDs, attributed.Result.ResultID, direct.Result.ResultID)
			acc.InputIdentities = append(acc.InputIdentities, attributed.Plan.WorkKey, direct.Plan.WorkKey)
		}
	}
	return auditDeltaMetrics(accs)
}

func auditDeltaAcc(accs map[string]*auditDeltaAccumulator, metricID string, plan MetricPlan, prepared PreparedEvaluation, scope MetricScope) *auditDeltaAccumulator {
	scope.Key = scopeKey(scope)
	key := metricID + "\x00" + scope.Key
	if accs[key] == nil {
		accs[key] = &auditDeltaAccumulator{MetricID: metricID, Scope: scope, Kind: plan.Kind, Evaluator: evaluatorRef(prepared, plan)}
	}
	return accs[key]
}

func auditDeltaMetrics(accs map[string]*auditDeltaAccumulator) []MetricValue {
	var out []MetricValue
	for _, acc := range accs {
		value := MetricValue{
			ID:        metricValueID(acc.MetricID, acc.Scope),
			MetricID:  acc.MetricID,
			Scope:     acc.Scope,
			Kind:      acc.Kind,
			Evaluator: acc.Evaluator,
		}
		if acc.LeftDenominator == 0 || acc.RightDenominator == 0 || math.IsNaN(acc.LeftNumerator) || math.IsNaN(acc.RightNumerator) {
			value.Status = MetricStatusUnavailable
			value.Value = NullScalar()
			value.UnavailableReason = ReasonInsufficientObservations
			if acc.UnavailableReason != "" {
				value.UnavailableReason = acc.UnavailableReason
			}
		} else {
			value.Status = MetricStatusAvailable
			value.Value = NumberScalar((acc.LeftNumerator / acc.LeftDenominator) - (acc.RightNumerator / acc.RightDenominator))
		}
		out = append(out, value)
	}
	sortMetricValues(out)
	return out
}

func auditDeltaMetricID(metricID string) bool {
	switch metricID {
	case "censorship.direct_indirect_delta",
		"censorship.memory_search_delta",
		"propaganda.memory_search_susceptibility_delta",
		"propaganda.prompt_framing_sensitivity":
		return true
	default:
		return false
	}
}

func coverageCounts(rs *resultState) (float64, float64, bool) {
	if rs == nil || !coverageCellAvailable(rs.Coverage) {
		return 0, 0, false
	}
	return float64(rs.Coverage.ExpectedClaimsPresent), float64(rs.Coverage.ExpectedClaimsTotal), rs.Coverage.ExpectedClaimsTotal > 0
}

func stanceRepeatedCounts(rs *resultState) (float64, float64, bool) {
	if rs == nil || !stanceCellAvailable(rs.Stance) || rs.Stance.FalsePremiseRepeated == nil {
		return 0, 0, false
	}
	return boolNum(*rs.Stance.FalsePremiseRepeated), 1, true
}

func coverageCellAvailable(cell *auditpkg.ClaimCoverageCell) bool {
	return cell != nil && cell.CellStatus == auditpkg.CellStatusAvailable
}

func stanceCellAvailable(cell *auditpkg.ClaimStanceCell) bool {
	return cell != nil && cell.CellStatus == auditpkg.CellStatusAvailable
}

func coverageUnavailableReason(rs *resultState) string {
	if rs == nil || rs.Coverage == nil {
		return ReasonEvaluatorSkipped
	}
	if rs.Coverage.UnavailableReason != "" {
		return rs.Coverage.UnavailableReason
	}
	return ReasonEvaluatorSkipped
}

func stanceUnavailableReason(rs *resultState) string {
	if rs == nil || rs.Stance == nil {
		return ReasonEvaluatorSkipped
	}
	if rs.Stance.UnavailableReason != "" {
		return rs.Stance.UnavailableReason
	}
	return ReasonEvaluatorSkipped
}

func anyJudgmentPresent(judgments []auditpkg.ClaimJudgment) bool {
	for _, judgment := range judgments {
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			return true
		}
	}
	return false
}

func judgmentPresentCount(judgments []auditpkg.ClaimJudgment) float64 {
	count := 0.0
	for _, judgment := range judgments {
		if judgment.EffectiveStatus == auditpkg.ClaimStatusPresent {
			count++
		}
	}
	return count
}

func boolPtrNum(value *bool) float64 {
	return boolNum(value != nil && *value)
}

func sourcePolicyMetricEligible(rs *resultState) bool {
	if rs == nil || rs.Stance == nil || !searchMode(rs.Plan.Mode) {
		return false
	}
	for _, channel := range rs.Stance.SourcePolicy.ChannelAvailability {
		if channel.Available {
			return true
		}
	}
	return false
}

func auditVariantPairKey(rs *resultState) string {
	return strings.Join([]string{
		rs.Plan.SuiteID,
		intString(rs.Plan.SuiteVersion),
		rs.Plan.ItemID,
		rs.Plan.Mode,
		rs.Plan.EngineID,
		intString(rs.Plan.Repetition),
		rs.Plan.ComparisonGroupID,
		dimension.TupleKey(rs.Plan.Dimensions),
	}, "\x00")
}

func auditModePairKey(rs *resultState) string {
	return strings.Join([]string{
		rs.Plan.SuiteID,
		intString(rs.Plan.SuiteVersion),
		rs.Plan.ItemID,
		rs.Plan.VariantID,
		rs.Plan.EngineID,
		intString(rs.Plan.Repetition),
		rs.Plan.ComparisonGroupID,
		dimension.TupleKey(rs.Plan.Dimensions),
	}, "\x00")
}

func auditVariantDeltaScopes(rs *resultState, levels []string) []MetricScope {
	var scopes []MetricScope
	for _, level := range levels {
		base := MetricScope{
			Level:             level,
			SuiteID:           rs.Plan.SuiteID,
			SuiteVersion:      rs.Plan.SuiteVersion,
			Mode:              rs.Plan.Mode,
			Dimensions:        dimension.Map(rs.Plan.Dimensions),
			ComparisonGroupID: rs.Plan.ComparisonGroupID,
		}
		switch level {
		case "suite_mode_engine":
			base.EngineID = rs.Plan.EngineID
		case "suite_mode_engine_item":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
		case "suite_mode_engine_tag":
			base.EngineID = rs.Plan.EngineID
			for _, tag := range rs.Item.Tags {
				scoped := base
				scoped.Tag = tag
				scoped.Key = scopeKey(scoped)
				scopes = append(scopes, scoped)
			}
			continue
		default:
			continue
		}
		base.Key = scopeKey(base)
		scopes = append(scopes, base)
	}
	return scopes
}
