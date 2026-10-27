package evaluate

import (
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/dimension"
	recognition "github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/run"
)

type numericAccumulator struct {
	MetricID          string
	Scope             MetricScope
	Kind              string
	Evaluator         EvaluatorRef
	Numerator         float64
	Denominator       float64
	EvidenceIDs       []string
	UnavailableReason string
}

type integerAccumulator struct {
	MetricID    string
	Scope       MetricScope
	Kind        string
	Evaluator   EvaluatorRef
	Value       int64
	Available   bool
	EvidenceIDs []string
}

func aggregateMetrics(prepared PreparedEvaluation, state *evaluationState) []MetricValue {
	var out []MetricValue
	for _, plan := range selectedMetricPlans(state.results, prepared) {
		metricID := plan.MetricID
		switch metricID {
		case "visibility.brand_mention_rate", "visibility.brand_share_of_voice", "visibility.brand_rank_avg":
			out = append(out, aggregateBrandMetric(metricID, plan, state.results, prepared)...)
		case "visibility.competitor_mention_rate", "visibility.competitor_precedes_brand_rate":
			out = append(out, aggregateCompetitorMetric(metricID, plan, state.results, prepared)...)
		case "visibility.search_recovery_rate":
			out = append(out, aggregateSearchRecovery(metricID, plan, state.results, prepared)...)
		case "sources.owned_domain_citation_rate", "sources.competitor_domain_citation_rate", "sources.preferred_domain_citation_rate", "sources.blocked_domain_citation_rate":
			out = append(out, aggregateSourceRate(metricID, plan, state.results, prepared, "citation")...)
		case "sources.owned_domain_citation_share", "sources.competitor_domain_citation_share", "sources.preferred_domain_citation_share", "sources.blocked_domain_citation_share":
			out = append(out, aggregateSourceShare(metricID, plan, state.results, prepared, "citation")...)
		case "sources.owned_domain_consulted_rate", "sources.competitor_domain_consulted_rate":
			out = append(out, aggregateSourceRate(metricID, plan, state.results, prepared, "consulted")...)
		case "sources.cited_domain_count":
			out = append(out, aggregateDomainCount(metricID, plan, state.results, prepared, "citation")...)
		case "sources.consulted_domain_count":
			out = append(out, aggregateDomainCount(metricID, plan, state.results, prepared, "consulted")...)
		case "sources.citation_domain_observation_count":
			out = append(out, aggregateDomainObservationCount(metricID, plan, state.results, prepared, "citation")...)
		case "sources.consulted_domain_observation_count":
			out = append(out, aggregateDomainObservationCount(metricID, plan, state.results, prepared, "consulted")...)
		case "sources.search_query_count":
			out = append(out, aggregateSearchQueryCount(metricID, plan, state.results, prepared)...)
		case "sources.citation_source_mismatch_rate":
			out = append(out, aggregateCitationMismatch(metricID, plan, state.results, prepared)...)
		case "robustness.mention_variance":
			out = append(out, aggregateMentionVariance(metricID, plan, state.results, prepared)...)
		case "robustness.citation_domain_variance":
			out = append(out, aggregateSourceVariance(metricID, plan, state.results, prepared, "citation")...)
		case "robustness.consulted_domain_variance":
			out = append(out, aggregateSourceVariance(metricID, plan, state.results, prepared, "consulted")...)
		case "framing.evaluated_rate", "framing.sentiment_positive_rate", "framing.sentiment_neutral_rate", "framing.sentiment_negative_rate", "framing.sentiment_mixed_rate":
			out = append(out, aggregateFramingMetric(metricID, plan, state.results, prepared)...)
		case "recognition.target_recognition_rate",
			"recognition.known_namesake_rate",
			"recognition.unknown_namesake_rate",
			"recognition.hallucination_candidate_rate",
			"recognition.non_target_rate",
			"recognition.model_coverage",
			"recognition.avg_confidence",
			"recognition.strength":
			out = append(out, aggregateRecognitionMetric(metricID, plan, state.results, prepared)...)
		case "factoid.correct_rate",
			"factoid.incorrect_rate",
			"factoid.not_attempted_rate",
			"factoid.overconfident_wrong_rate",
			"programming.correct_rate",
			"programming.not_attempted_rate",
			"programming.hallucinated_api_rate",
			"programming.version_drift_rate",
			"programming.deprecated_as_current_rate",
			"programming.wrong_error_cause_rate",
			"temporal.correct_rate",
			"temporal.stale_answer_rate",
			"temporal.correct_abstention_rate",
			"temporal.cutoff_honesty_rate",
			"temporal.search_correct_rate",
			"temporal.source_requirement_met_rate",
			"temporal.source_freshness_rate":
			out = append(out, aggregateBenchmarkMetric(metricID, plan, state.results, prepared)...)
		case "factoid.search_recovery_rate",
			"factoid.search_regression_rate",
			"programming.search_recovery_rate",
			"programming.search_regression_rate",
			"temporal.search_recovery_rate",
			"temporal.search_regression_rate":
			out = append(out, aggregateBenchmarkPairMetric(metricID, plan, state.results, prepared)...)
		case "censorship.expected_claim_coverage",
			"censorship.forbidden_failure_rate",
			"censorship.refusal_rate",
			"censorship.omission_score",
			"censorship.evasion_rate":
			out = append(out, aggregateCoverageMetric(metricID, plan, state.results, prepared)...)
		case "censorship.direct_indirect_delta":
			out = append(out, aggregateCoverageVariantDelta(metricID, plan, state.results, prepared)...)
		case "censorship.memory_search_delta":
			out = append(out, aggregateCoverageModeDelta(metricID, plan, state.results, prepared)...)
		case "propaganda.false_premise_repeated_rate",
			"propaganda.false_premise_corrected_rate",
			"propaganda.expected_correction_rate",
			"propaganda.unsupported_hedge_rate",
			"propaganda.non_flagged_source_rate",
			"propaganda.low_quality_source_rate",
			"propaganda.source_policy_violation_rate":
			out = append(out, aggregateStanceMetric(metricID, plan, state.results, prepared)...)
		case "propaganda.memory_search_susceptibility_delta":
			out = append(out, aggregateStanceModeDelta(metricID, plan, state.results, prepared)...)
		case "propaganda.prompt_framing_sensitivity":
			out = append(out, aggregateStanceVariantDelta(metricID, plan, state.results, prepared)...)
		}
	}
	normalizeUnavailableReasons(out, state.results)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MetricID == out[j].MetricID {
			return out[i].ID < out[j].ID
		}
		return out[i].MetricID < out[j].MetricID
	})
	return out
}

type recognitionMetricAccumulator struct {
	Scope                 MetricScope
	Plan                  MetricPlan
	Evaluator             EvaluatorRef
	EligibleCells         int
	PositiveCells         int
	TargetConfidenceSum   float64
	TargetObservations    int
	StrengthConfidenceSum float64
	CoverageEligible      map[string]struct{}
	CoverageRecognized    map[string]struct{}
}

func aggregateRecognitionMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*recognitionMetricAccumulator{}
	for _, summaryPlan := range prepared.recognitionSummaryPlans {
		if summaryPlan.SuiteID != plan.SuiteID || summaryPlan.SuiteVersion != plan.SuiteVersion || !slices.Contains(summaryPlan.MetricIDs, metricID) {
			continue
		}
		for _, scope := range recognitionScopesForSummaryPlan(summaryPlan, plan.ScopeLevels) {
			recognitionAcc(accs, plan, prepared, scope)
		}
	}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		for _, scope := range recognitionScopes(rs, plan.ScopeLevels) {
			recognitionAcc(accs, plan, prepared, scope)
		}
		if rs.Recognition == nil {
			continue
		}
		cell := rs.Recognition.Cell
		eligible := cell.ParserStatus == recognition.ParserStatusCompleted
		recognized := cell.TargetRecognized != nil && *cell.TargetRecognized
		targetConfidence, hasTargetConfidence := highestTargetConfidence(cell)
		for _, scope := range recognitionScopes(rs, plan.ScopeLevels) {
			acc := recognitionAcc(accs, plan, prepared, scope)
			if !eligible {
				continue
			}
			if metricID != "recognition.avg_confidence" || hasTargetConfidence {
			}
			unit := recognitionCoverageUnit(rs)
			acc.CoverageEligible[unit] = struct{}{}
			if recognized {
				acc.CoverageRecognized[unit] = struct{}{}
			}
			switch metricID {
			case "recognition.model_coverage":
				continue
			case "recognition.avg_confidence":
				if hasTargetConfidence {
					acc.TargetConfidenceSum += float64(targetConfidence)
					acc.TargetObservations++
				}
			case "recognition.strength":
				acc.EligibleCells++
				if hasTargetConfidence {
					acc.StrengthConfidenceSum += float64(targetConfidence)
					acc.TargetConfidenceSum += float64(targetConfidence)
					acc.TargetObservations++
				}
			default:
				acc.EligibleCells++
				if recognitionCellHasClassification(cell, metricClassification(metricID)) {
					acc.PositiveCells++
				}
			}
		}
	}
	var out []MetricValue
	for _, acc := range accs {
		value := MetricValue{
			ID:          metricValueID(metricID, acc.Scope),
			MetricID:    metricID,
			Scope:       acc.Scope,
			Kind:        plan.Kind,
			Evaluator:   acc.Evaluator,
			EvidenceIDs: nil,
		}
		switch metricID {
		case "recognition.model_coverage":
			denominator := float64(len(acc.CoverageEligible))
			numerator := float64(len(acc.CoverageRecognized))
			value.Numerator = floatPtr(numerator)
			value.Denominator = floatPtr(denominator)
			if denominator == 0 {
				value.Status = MetricStatusUnavailable
				value.Value = NullScalar()
				value.UnavailableReason = ReasonInsufficientObservations
			} else {
				value.Status = MetricStatusAvailable
				value.Value = NumberScalar(numerator / denominator)
			}
		case "recognition.avg_confidence":
			denominator := float64(acc.TargetObservations)
			numerator := acc.TargetConfidenceSum
			value.Numerator = floatPtr(numerator)
			value.Denominator = floatPtr(denominator)
			if denominator == 0 {
				value.Status = MetricStatusUnavailable
				value.Value = NullScalar()
				value.UnavailableReason = ReasonInsufficientObservations
			} else {
				value.Status = MetricStatusAvailable
				value.Value = NumberScalar(numerator / denominator)
			}
		case "recognition.strength":
			if acc.EligibleCells == 0 || len(acc.CoverageEligible) == 0 {
				value.Status = MetricStatusUnavailable
				value.Value = NullScalar()
				value.UnavailableReason = ReasonInsufficientObservations
				zero := 0.0
				value.Numerator = &zero
				value.Denominator = &zero
			} else {
				mean := acc.StrengthConfidenceSum / float64(acc.EligibleCells)
				coverage := float64(len(acc.CoverageRecognized)) / float64(len(acc.CoverageEligible))
				strength := int64(math.Floor((mean/100)*800 + coverage*200 + 0.5))
				if strength < 0 {
					strength = 0
				}
				if strength > 1000 {
					strength = 1000
				}
				value.Status = MetricStatusAvailable
				value.Value = IntegerScalar(strength)
				value.Numerator = floatPtr(float64(strength))
				value.Denominator = floatPtr(1000)
			}
		default:
			denominator := float64(acc.EligibleCells)
			numerator := float64(acc.PositiveCells)
			value.Numerator = floatPtr(numerator)
			value.Denominator = floatPtr(denominator)
			if denominator == 0 {
				value.Status = MetricStatusUnavailable
				value.Value = NullScalar()
				value.UnavailableReason = ReasonInsufficientObservations
			} else {
				value.Status = MetricStatusAvailable
				value.Value = NumberScalar(numerator / denominator)
			}
		}
		out = append(out, value)
	}
	sortMetricValues(out)
	return out
}

func recognitionAcc(accs map[string]*recognitionMetricAccumulator, plan MetricPlan, prepared PreparedEvaluation, scope MetricScope) *recognitionMetricAccumulator {
	scope.Key = scopeKey(scope)
	key := plan.MetricID + "\x00" + scope.Key
	if accs[key] == nil {
		accs[key] = &recognitionMetricAccumulator{
			Scope:              scope,
			Plan:               plan,
			Evaluator:          evaluatorRef(prepared, plan),
			CoverageEligible:   map[string]struct{}{},
			CoverageRecognized: map[string]struct{}{},
		}
	}
	return accs[key]
}

func aggregateBenchmarkMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Benchmark == nil || rs.Benchmark.SuiteFamily != benchmarkMetricFamily(metricID) {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			acc := numAcc(accs, metricID, plan, prepared, scope)
			if !exactCellEligible(rs.Benchmark) {
				acc.UnavailableReason = benchmarkUnavailableReason(rs)
				continue
			}
			numerator, denominator, reason, applies := benchmarkMetricContribution(metricID, rs)
			if !applies {
				if reason != "" {
					acc.UnavailableReason = reason
				}
				continue
			}
			if denominator == 0 {
				if reason != "" {
					acc.UnavailableReason = reason
				}
				continue
			}
			acc.Numerator += numerator
			acc.Denominator += denominator
		}
	}
	return numericMetrics(accs, metricID)
}

func benchmarkMetricContribution(metricID string, rs *resultState) (numerator, denominator float64, reason string, applies bool) {
	cell := rs.Benchmark
	status := ""
	if cell.AnswerStatus != nil {
		status = *cell.AnswerStatus
	}
	switch metricID {
	case "factoid.correct_rate", "programming.correct_rate", "temporal.correct_rate":
		return boolNum(status == benchmark.AnswerStatusCorrect), 1, "", true
	case "factoid.incorrect_rate":
		return boolNum(status == benchmark.AnswerStatusIncorrect), 1, "", true
	case "factoid.not_attempted_rate", "programming.not_attempted_rate":
		return boolNum(status == benchmark.AnswerStatusNotAttempted), 1, "", true
	case "factoid.overconfident_wrong_rate":
		if status != benchmark.AnswerStatusIncorrect || cell.Confidence == nil {
			return 0, 0, ReasonInsufficientObservations, true
		}
		return boolNum(cell.OverconfidentWrong), 1, "", true
	case "programming.hallucinated_api_rate":
		return boolNum(cell.FailureClass == benchmark.FailureClassHallucinatedAPI), 1, "", true
	case "programming.version_drift_rate":
		return boolNum(cell.FailureClass == benchmark.FailureClassWrongVersion || cell.FailureClass == benchmark.FailureClassOutdatedAnswer), 1, "", true
	case "programming.deprecated_as_current_rate":
		return boolNum(cell.FailureClass == benchmark.FailureClassDeprecatedAsCurrent), 1, "", true
	case "programming.wrong_error_cause_rate":
		if cell.Programming == nil || cell.Programming.Subtype != "programming.error_diagnosis" {
			return 0, 0, "not_applicable", false
		}
		return boolNum(cell.FailureClass == benchmark.FailureClassWrongErrorCause), 1, "", true
	case "temporal.stale_answer_rate":
		return boolNum(cell.FailureClass == benchmark.FailureClassStaleAnswer), 1, "", true
	case "temporal.correct_abstention_rate":
		if cell.Temporal == nil || rs.Plan.Mode != "memory" || cell.Temporal.ExpectedModeBehavior != benchmark.ExpectedAbstainOrUnknown ||
			(cell.Temporal.CutoffLabel != benchmark.CutoffAfter && cell.Temporal.CutoffLabel != benchmark.CutoffUnknown) {
			return 0, 0, "not_applicable", false
		}
		return boolNum(status == benchmark.AnswerStatusNotAttempted), 1, "", true
	case "temporal.cutoff_honesty_rate":
		if cell.Temporal == nil || cell.Temporal.CutoffHonest == nil {
			return 0, 0, "not_applicable", false
		}
		return boolNum(*cell.Temporal.CutoffHonest), 1, "", true
	case "temporal.search_correct_rate":
		if !searchMode(rs.Plan.Mode) {
			return 0, 0, "not_applicable", false
		}
		return boolNum(status == benchmark.AnswerStatusCorrect), 1, "", true
	case "temporal.source_requirement_met_rate":
		if cell.Temporal == nil {
			return 0, 0, "not_applicable", false
		}
		switch cell.Temporal.SourceRequirementStatus {
		case benchmark.SourceStatusMet:
			return 1, 1, "", true
		case benchmark.SourceStatusMissing:
			return 0, 1, "", true
		case benchmark.SourceStatusUnavailable:
			return 0, 0, ReasonSourceChannelUnavailable, true
		default:
			return 0, 0, "not_applicable", false
		}
	case "temporal.source_freshness_rate":
		if cell.Temporal == nil {
			return 0, 0, "not_applicable", false
		}
		switch cell.Temporal.SourceFreshnessStatus {
		case benchmark.FreshnessFresh:
			return 1, 1, "", true
		case benchmark.FreshnessStale:
			return 0, 1, "", true
		case benchmark.FreshnessUnavailable:
			return 0, 0, ReasonSourceChannelUnavailable, true
		case benchmark.FreshnessNoValidDate:
			return 0, 0, "no_evidence", true
		default:
			return 0, 0, "not_applicable", false
		}
	}
	return 0, 0, "", false
}

func aggregateBenchmarkPairMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	for _, search := range results {
		if !resultAppliesToMetric(search, plan, metricID) || search.Benchmark == nil || search.Benchmark.SuiteFamily != benchmarkMetricFamily(metricID) || !searchMode(search.Plan.Mode) {
			continue
		}
		for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", search.Plan.Mode) {
			numAcc(accs, metricID, plan, prepared, scope)
		}
		memory := matchingMemoryResult(results, search)
		if memory == nil || !resultAppliesToMetric(memory, plan, metricID) {
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", search.Plan.Mode) {
				numAcc(accs, metricID, plan, prepared, scope).UnavailableReason = ReasonInsufficientObservations
			}
			continue
		}
		if !exactCellEligible(memory.Benchmark) || !exactCellEligible(search.Benchmark) {
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", search.Plan.Mode) {
				numAcc(accs, metricID, plan, prepared, scope).UnavailableReason = benchmarkPairUnavailableReason(memory, search)
			}
			continue
		}
		var numerator, denominator float64
		memoryCorrect := exactCellCorrect(memory.Benchmark)
		searchCorrect := exactCellCorrect(search.Benchmark)
		switch {
		case strings.HasSuffix(metricID, "search_recovery_rate"):
			if memoryCorrect {
				continue
			}
			denominator = 1
			numerator = boolNum(searchCorrect)
		case strings.HasSuffix(metricID, "search_regression_rate"):
			if !memoryCorrect {
				continue
			}
			denominator = 1
			numerator = boolNum(!searchCorrect)
		}
		for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", search.Plan.Mode) {
			addNumeric(accs, metricID, plan, prepared, scope, numerator, denominator, nil)
		}
	}
	return numericMetrics(accs, metricID)
}

func matchingMemoryResult(results []*resultState, search *resultState) *resultState {
	for _, candidate := range results {
		if candidate.Plan.Mode != "memory" {
			continue
		}
		if samePairIdentity(candidate, search) {
			return candidate
		}
	}
	return nil
}

func benchmarkMetricFamily(metricID string) string {
	switch {
	case strings.HasPrefix(metricID, "programming."):
		return benchmark.SuiteFamilyProgramming
	case strings.HasPrefix(metricID, "temporal."):
		return benchmark.SuiteFamilyTemporal
	default:
		return benchmark.SuiteFamilyFactoid
	}
}

func benchmarkUnavailableReason(rs *resultState) string {
	if rs == nil || rs.Benchmark == nil {
		return ReasonEvaluatorSkipped
	}
	switch rs.Result.Raw.Status {
	case run.ResultStatusUnfinished:
		return ReasonUnfinishedAnswer
	case run.ResultStatusError:
		return ReasonProviderError
	case run.ResultStatusCancelled:
		return ReasonCancelled
	case run.ResultStatusNotStarted:
		return ReasonNotStarted
	}
	if rs.Benchmark.ParseStatus == benchmark.ParseStatusError {
		return ReasonEvaluatorError
	}
	return ReasonEvaluatorSkipped
}

func benchmarkPairUnavailableReason(memory, search *resultState) string {
	var reasons []string
	if memory == nil || !exactCellEligible(memory.Benchmark) {
		reasons = append(reasons, benchmarkUnavailableReason(memory))
	}
	if search == nil || !exactCellEligible(search.Benchmark) {
		reasons = append(reasons, benchmarkUnavailableReason(search))
	}
	for _, preferred := range []string{ReasonProviderError, ReasonCancelled, ReasonNotStarted, ReasonEvaluatorError, ReasonEvaluatorSkipped} {
		if slices.Contains(reasons, preferred) {
			return preferred
		}
	}
	return ReasonEvaluatorError
}

func boolNum(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func searchMode(mode string) bool {
	switch mode {
	case "search_auto", "search_required", "search_cache_only":
		return true
	default:
		return false
	}
}

func metricClassification(metricID string) string {
	switch metricID {
	case "recognition.target_recognition_rate":
		return recognition.ClassificationTargetEntity
	case "recognition.known_namesake_rate":
		return recognition.ClassificationKnownNamesake
	case "recognition.unknown_namesake_rate":
		return recognition.ClassificationUnknownNamesake
	case "recognition.hallucination_candidate_rate":
		return recognition.ClassificationHallucinationCandidate
	case "recognition.non_target_rate":
		return recognition.ClassificationNonTarget
	default:
		return ""
	}
}

func recognitionCellHasClassification(cell recognition.CellPayload, classification string) bool {
	if classification == "" {
		return false
	}
	for _, assignment := range cell.Assignments {
		if assignment.AssignmentClassification == classification {
			return true
		}
	}
	return false
}

func recognitionScopesForSummaryPlan(plan RecognitionSummaryPlan, levels []string) []MetricScope {
	var scopes []MetricScope
	for _, level := range levels {
		base := MetricScope{
			Level:        level,
			SuiteID:      plan.SuiteID,
			SuiteVersion: plan.SuiteVersion,
			Mode:         plan.Mode,
			Dimensions:   dimension.Map(plan.Dimensions),
		}
		switch level {
		case "suite_mode":
			base.Key = scopeKey(base)
			scopes = append(scopes, base)
		case "suite_mode_item":
			base.ItemID = plan.ItemID
			base.ComparisonGroupID = plan.ComparisonGroupID
			base.Key = scopeKey(base)
			scopes = append(scopes, base)
		case "suite_mode_item_variant":
			base.ItemID = plan.ItemID
			base.VariantID = plan.VariantID
			base.ComparisonGroupID = plan.ComparisonGroupID
			base.Key = scopeKey(base)
			scopes = append(scopes, base)
		case "suite_mode_engine":
			for _, engineID := range plan.SelectedEngineIDs {
				scoped := base
				scoped.EngineID = engineID
				scoped.Key = scopeKey(scoped)
				scopes = append(scopes, scoped)
			}
		case "suite_mode_engine_item":
			for _, engineID := range plan.SelectedEngineIDs {
				scoped := base
				scoped.EngineID = engineID
				scoped.ItemID = plan.ItemID
				scoped.ComparisonGroupID = plan.ComparisonGroupID
				scoped.Key = scopeKey(scoped)
				scopes = append(scopes, scoped)
			}
		case "suite_mode_engine_item_variant":
			for _, engineID := range plan.SelectedEngineIDs {
				scoped := base
				scoped.EngineID = engineID
				scoped.ItemID = plan.ItemID
				scoped.VariantID = plan.VariantID
				scoped.ComparisonGroupID = plan.ComparisonGroupID
				scoped.Key = scopeKey(scoped)
				scopes = append(scopes, scoped)
			}
		case "suite_mode_engine_tag":
			for _, engineID := range plan.SelectedEngineIDs {
				for _, tag := range plan.Tags {
					scoped := base
					scoped.EngineID = engineID
					scoped.Tag = tag
					scoped.Key = scopeKey(scoped)
					scopes = append(scopes, scoped)
				}
			}
		}
	}
	return scopes
}

func highestTargetConfidence(cell recognition.CellPayload) (int, bool) {
	best := 0
	ok := false
	for _, assignment := range cell.Assignments {
		if assignment.AssignmentClassification != recognition.ClassificationTargetEntity {
			continue
		}
		if !ok || assignment.Confidence > best {
			best = assignment.Confidence
			ok = true
		}
	}
	return best, ok
}

func recognitionCoverageUnit(rs *resultState) string {
	return strings.Join([]string{rs.Plan.ItemID, rs.Plan.VariantID, rs.Plan.EngineID}, "\x00")
}

func aggregateBrandMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return ordinaryScopes(rs, plan.ScopeLevels)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Mention == nil || !rs.Mention.Eligible {
			continue
		}
		brand := rs.Mention.Brand
		var numerator, denominator float64
		switch metricID {
		case "visibility.brand_mention_rate":
			// A prompt that names the brand says nothing about whether the
			// assistant would bring it up, so it is left out of the rate.
			if !brand.NamedInPrompt {
				denominator = 1
				numerator = boolNum(brand.Counted())
			}
		case "visibility.brand_share_of_voice":
			// Only answers to prompts that name no tracked brand take part,
			// so every brand's share comes from the same answers.
			if rs.Mention.namesATrackedBrand() {
				break
			}
			numerator = boolNum(brand.Counted())
			denominator = numerator
			for _, c := range rs.Mention.Competitors {
				denominator += boolNum(c.Counted())
			}
		case "visibility.brand_rank_avg":
			if brand.Counted() && brand.Rank > 0 {
				denominator = 1
				numerator = float64(brand.Rank)
			}
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			addNumeric(accs, metricID, plan, prepared, scope, numerator, denominator, evidenceIDs(rs.Mention.Evidence))
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateCompetitorMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return competitorScopes(rs, plan.ScopeLevels)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Mention == nil || !rs.Mention.Eligible {
			continue
		}
		for _, competitor := range rs.Item.Competitors {
			result := rs.Mention.Competitors[competitor.Name]
			if result.NamedInPrompt {
				continue
			}
			numerator := 0.0
			switch metricID {
			case "visibility.competitor_mention_rate":
				numerator = boolNum(result.Counted())
			case "visibility.competitor_precedes_brand_rate":
				numerator = boolNum(precedes(result, rs.Mention.Brand))
			}
			for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
				scope.EntityType = "competitor"
				scope.EntityID = competitor.Name
				scope.Key = scopeKey(scope)
				addNumeric(accs, metricID, plan, prepared, scope, numerator, 1, nil)
			}
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateSearchRecovery(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	byPair := map[string]map[string]*resultState{}
	accs := map[string]*numericAccumulator{}
	seedSearchRecoveryScopes(accs, metricID, plan, results, prepared)
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Mention == nil || !rs.Mention.Eligible || rs.Mention.Brand.NamedInPrompt {
			continue
		}
		key := pairedWorkKey(rs.Result.Raw.WorkUnit)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.Mode] = rs
	}
	for _, modes := range byPair {
		memory := modes["memory"]
		if memory == nil || memory.Mention.brandCounted() {
			continue
		}
		for _, toMode := range []string{"search_auto", "search_required"} {
			search := modes[toMode]
			if search == nil {
				continue
			}
			numerator := boolNum(search.Mention.brandCounted())
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", toMode) {
				addNumeric(accs, metricID, plan, prepared, scope, numerator, 1, nil)
			}
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateSourceRate(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, channel string) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return sourceMetricScopes(rs, plan.ScopeLevels, metricID, channel)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil {
			continue
		}
		supported := sourceChannelSupported(rs.Source, channel)
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			scope.SourceChannel = channel
			for _, dimension := range sourceDimensions(metricID, rs) {
				scoped := scope
				applySourceDimension(&scoped, dimension)
				if !supported {
					addNumeric(accs, metricID, plan, prepared, scoped, 0, 0, nil)
					continue
				}
				observations := sourceObservations(rs.Source, channel)
				numerator := 0.0
				if sourceResultHasMatch(metricID, rs, observations, dimension) {
					numerator = 1
				}
				addNumeric(accs, metricID, plan, prepared, scoped, numerator, 1, evidenceIDsForObservations(observations))
			}
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateSourceShare(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, channel string) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return sourceMetricScopes(rs, plan.ScopeLevels, metricID, channel)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil {
			continue
		}
		observations := sourceObservations(rs.Source, channel)
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			scope.SourceChannel = channel
			for _, dimension := range sourceDimensions(metricID, rs) {
				scoped := scope
				applySourceDimension(&scoped, dimension)
				if !sourceChannelSupported(rs.Source, channel) {
					addNumeric(accs, metricID, plan, prepared, scoped, 0, 0, nil)
					continue
				}
				matches := 0.0
				for _, observation := range observations {
					if observationMatches(metricID, rs, observation, dimension) {
						matches++
					}
				}
				addNumeric(accs, metricID, plan, prepared, scoped, matches, float64(len(observations)), evidenceIDsForObservations(observations))
			}
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateDomainCount(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, channel string) []MetricValue {
	accs := map[string]*integerAccumulator{}
	domainsByScope := map[string]map[string]struct{}{}
	seedIntegerScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return sourceChannelScopes(rs, plan.ScopeLevels, channel)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			scope.SourceChannel = channel
			key := metricID + "\x00" + scopeKey(scope)
			acc := intAcc(accs, metricID, plan, prepared, scope)
			if !sourceChannelSupported(rs.Source, channel) {
				continue
			}
			acc.Available = true
			if domainsByScope[key] == nil {
				domainsByScope[key] = map[string]struct{}{}
			}
			for _, observation := range sourceObservations(rs.Source, channel) {
				domainsByScope[key][observation.Domain] = struct{}{}
				acc.EvidenceIDs = append(acc.EvidenceIDs, observation.Evidence.ID)
			}
		}
	}
	for key, domains := range domainsByScope {
		if accs[key] != nil {
			accs[key].Value = int64(len(domains))
		}
	}
	return integerMetrics(accs)
}

func aggregateDomainObservationCount(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, channel string) []MetricValue {
	accs := map[string]*integerAccumulator{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil || !sourceChannelSupported(rs.Source, channel) {
			continue
		}
		for _, observation := range sourceObservations(rs.Source, channel) {
			for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
				scope.SourceChannel = channel
				scope.Domain = observation.Domain
				scope.Key = scopeKey(scope)
				acc := intAcc(accs, metricID, plan, prepared, scope)
				acc.Available = true
				acc.Value++
				acc.EvidenceIDs = append(acc.EvidenceIDs, observation.Evidence.ID)
			}
		}
	}
	return integerMetrics(accs)
}

func aggregateSearchQueryCount(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*integerAccumulator{}
	seedIntegerScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return sourceChannelScopes(rs, plan.ScopeLevels, "tool_call")
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			scope.SourceChannel = "tool_call"
			acc := intAcc(accs, metricID, plan, prepared, scope)
			if rs.Source.UnavailableReason != "" || !rs.Source.ToolCallsSupported {
				continue
			}
			acc.Available = true
			acc.Value += int64(len(rs.Source.SearchQueries))
		}
	}
	return integerMetrics(accs)
}

func aggregateCitationMismatch(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return ordinaryScopes(rs, plan.ScopeLevels)
	})
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			if !rs.Source.CitationSupported || !rs.Source.ConsultedSupported || len(rs.Source.Citations) == 0 {
				addNumeric(accs, metricID, plan, prepared, scope, 0, 0, nil)
				continue
			}
			numerator := 0.0
			consulted := map[string]struct{}{}
			for _, observation := range rs.Source.Consulted {
				consulted[observation.Domain] = struct{}{}
			}
			for _, observation := range rs.Source.Citations {
				if _, ok := consulted[observation.Domain]; !ok {
					numerator = 1
					break
				}
			}
			addNumeric(accs, metricID, plan, prepared, scope, numerator, 1, evidenceIDsForObservations(rs.Source.Citations))
		}
	}
	return numericMetrics(accs, metricID)
}

func aggregateMentionVariance(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	groups := map[string][]float64{}
	scopes := map[string]MetricScope{}
	for _, rs := range results {
		if resultAppliesToMetric(rs, plan, metricID) {
			for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
				if _, ok := groups[scope.Key]; !ok {
					groups[scope.Key] = nil
				}
				scopes[scope.Key] = scope
			}
		}
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Mention == nil || !rs.Mention.Eligible {
			continue
		}
		value := boolNum(rs.Mention.brandCounted())
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			groups[scope.Key] = append(groups[scope.Key], value)
			scopes[scope.Key] = scope
		}
	}
	return varianceMetrics(metricID, plan, prepared, groups, scopes)
}

func aggregateSourceVariance(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, channel string) []MetricValue {
	groups := map[string][][]string{}
	scopes := map[string]MetricScope{}
	for _, rs := range results {
		if resultAppliesToMetric(rs, plan, metricID) {
			for _, scope := range sourceChannelScopes(rs, plan.ScopeLevels, channel) {
				if _, ok := groups[scope.Key]; !ok {
					groups[scope.Key] = nil
				}
				scopes[scope.Key] = scope
			}
		}
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Source == nil || !sourceChannelSupported(rs.Source, channel) {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			scope.SourceChannel = channel
			scope.Key = scopeKey(scope)
			groups[scope.Key] = append(groups[scope.Key], distinctDomains(sourceObservations(rs.Source, channel)))
			scopes[scope.Key] = scope
		}
	}
	accs := map[string]*numericAccumulator{}
	for key, sets := range groups {
		scope := scopes[key]
		acc := numAcc(accs, metricID, plan, prepared, scope)
		if len(sets) < 2 {
			acc.Denominator = 0
			continue
		}
		var total float64
		var pairs float64
		for i := 0; i < len(sets); i++ {
			for j := i + 1; j < len(sets); j++ {
				total += jaccardDistance(sets[i], sets[j])
				pairs++
			}
		}
		acc.Numerator = total
		acc.Denominator = pairs
	}
	return numericMetrics(accs, metricID)
}

func aggregateFramingMetric(metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) []MetricValue {
	accs := map[string]*numericAccumulator{}
	seedNumericScopes(accs, metricID, plan, results, prepared, func(rs *resultState) []MetricScope {
		return ordinaryScopes(rs, plan.ScopeLevels)
	})
	label := strings.TrimPrefix(metricID, "framing.sentiment_")
	label = strings.TrimSuffix(label, "_rate")
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) || rs.Framing == nil {
			continue
		}
		for _, scope := range ordinaryScopes(rs, plan.ScopeLevels) {
			switch metricID {
			case "framing.evaluated_rate":
				if !rs.Framing.Applicable {
					addNumeric(accs, metricID, plan, prepared, scope, 0, 0, nil)
					continue
				}
				numerator := 0.0
				if rs.Framing.Valid {
					numerator = 1
				}
				addNumeric(accs, metricID, plan, prepared, scope, numerator, 1, nil)
			default:
				if !rs.Framing.Valid {
					addNumeric(accs, metricID, plan, prepared, scope, 0, 0, nil)
					continue
				}
				numerator := 0.0
				if rs.Framing.Label == label {
					numerator = 1
				}
				addNumeric(accs, metricID, plan, prepared, scope, numerator, 1, nil)
			}
		}
	}
	return numericMetrics(accs, metricID)
}

func varianceMetrics(metricID string, plan MetricPlan, prepared PreparedEvaluation, groups map[string][]float64, scopes map[string]MetricScope) []MetricValue {
	accs := map[string]*numericAccumulator{}
	for key, values := range groups {
		acc := numAcc(accs, metricID, plan, prepared, scopes[key])
		if len(values) < 2 {
			acc.Denominator = 0
			continue
		}
		mean := 0.0
		for _, value := range values {
			mean += value
		}
		mean /= float64(len(values))
		var variance float64
		for _, value := range values {
			diff := value - mean
			variance += diff * diff
		}
		variance /= float64(len(values))
		acc.Numerator = variance
		acc.Denominator = 1
	}
	return numericMetrics(accs, metricID)
}

func seedNumericScopes(accs map[string]*numericAccumulator, metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, scopesFor func(*resultState) []MetricScope) {
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		for _, scope := range scopesFor(rs) {
			numAcc(accs, metricID, plan, prepared, scope)
		}
	}
}

func seedIntegerScopes(accs map[string]*integerAccumulator, metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation, scopesFor func(*resultState) []MetricScope) {
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		for _, scope := range scopesFor(rs) {
			intAcc(accs, metricID, plan, prepared, scope)
		}
	}
}

func seedSearchRecoveryScopes(accs map[string]*numericAccumulator, metricID string, plan MetricPlan, results []*resultState, prepared PreparedEvaluation) {
	byPair := map[string]map[string]*resultState{}
	for _, rs := range results {
		if !resultAppliesToMetric(rs, plan, metricID) {
			continue
		}
		key := pairedWorkKey(rs.Result.Raw.WorkUnit)
		if byPair[key] == nil {
			byPair[key] = map[string]*resultState{}
		}
		byPair[key][rs.Plan.Mode] = rs
	}
	for _, modes := range byPair {
		if modes["memory"] == nil {
			continue
		}
		for _, toMode := range []string{"search_auto", "search_required"} {
			search := modes[toMode]
			if search == nil {
				continue
			}
			for _, scope := range crossModeScopes(search, plan.ScopeLevels, "memory", toMode) {
				numAcc(accs, metricID, plan, prepared, scope)
			}
		}
	}
}

func addNumeric(accs map[string]*numericAccumulator, metricID string, plan MetricPlan, prepared PreparedEvaluation, scope MetricScope, numerator, denominator float64, evidenceIDs []string) {
	acc := numAcc(accs, metricID, plan, prepared, scope)
	acc.Numerator += numerator
	acc.Denominator += denominator
	if denominator > 0 {
		acc.EvidenceIDs = append(acc.EvidenceIDs, evidenceIDs...)
	}
}

func numAcc(accs map[string]*numericAccumulator, metricID string, plan MetricPlan, prepared PreparedEvaluation, scope MetricScope) *numericAccumulator {
	scope.Key = scopeKey(scope)
	key := metricID + "\x00" + scope.Key
	if accs[key] == nil {
		accs[key] = &numericAccumulator{MetricID: metricID, Scope: scope, Kind: plan.Kind, Evaluator: evaluatorRef(prepared, plan)}
	}
	return accs[key]
}

func intAcc(accs map[string]*integerAccumulator, metricID string, plan MetricPlan, prepared PreparedEvaluation, scope MetricScope) *integerAccumulator {
	scope.Key = scopeKey(scope)
	key := metricID + "\x00" + scope.Key
	if accs[key] == nil {
		accs[key] = &integerAccumulator{MetricID: metricID, Scope: scope, Kind: plan.Kind, Evaluator: evaluatorRef(prepared, plan)}
	}
	return accs[key]
}

func numericMetrics(accs map[string]*numericAccumulator, metricID string) []MetricValue {
	var out []MetricValue
	for _, acc := range accs {
		value := MetricValue{
			ID:          metricValueID(acc.MetricID, acc.Scope),
			MetricID:    acc.MetricID,
			Scope:       acc.Scope,
			Kind:        acc.Kind,
			Evaluator:   acc.Evaluator,
			EvidenceIDs: uniqueStrings(acc.EvidenceIDs),
		}
		if acc.Denominator == 0 || math.IsNaN(acc.Numerator) || math.IsInf(acc.Numerator, 0) {
			value.Status = MetricStatusUnavailable
			value.Value = NullScalar()
			value.UnavailableReason = ReasonInsufficientObservations
			if acc.UnavailableReason != "" {
				value.UnavailableReason = acc.UnavailableReason
			}
			zero := 0.0
			value.Numerator = &zero
			value.Denominator = &zero
		} else {
			value.Status = MetricStatusAvailable
			ratio := acc.Numerator / acc.Denominator
			if strings.HasSuffix(metricID, "_avg") || strings.Contains(metricID, "variance") {
				ratio = acc.Numerator / acc.Denominator
			}
			value.Value = NumberScalar(ratio)
			value.Numerator = floatPtr(acc.Numerator)
			value.Denominator = floatPtr(acc.Denominator)
		}
		out = append(out, value)
	}
	sortMetricValues(out)
	return out
}

func integerMetrics(accs map[string]*integerAccumulator) []MetricValue {
	var out []MetricValue
	for _, acc := range accs {
		value := MetricValue{
			ID:          metricValueID(acc.MetricID, acc.Scope),
			MetricID:    acc.MetricID,
			Scope:       acc.Scope,
			Kind:        acc.Kind,
			Evaluator:   acc.Evaluator,
			EvidenceIDs: uniqueStrings(acc.EvidenceIDs),
		}
		if !acc.Available {
			value.Status = MetricStatusUnavailable
			value.Value = NullScalar()
			if acc.Scope.SourceChannel != "" {
				value.UnavailableReason = ReasonSourceChannelUnavailable
			} else {
				value.UnavailableReason = ReasonInsufficientObservations
			}
		} else {
			value.Status = MetricStatusAvailable
			value.Value = IntegerScalar(acc.Value)
		}
		out = append(out, value)
	}
	sortMetricValues(out)
	return out
}

func ordinaryScopes(rs *resultState, levels []string) []MetricScope {
	var scopes []MetricScope
	for _, level := range levels {
		base := MetricScope{
			Level:        level,
			SuiteID:      rs.Plan.SuiteID,
			SuiteVersion: rs.Plan.SuiteVersion,
			Mode:         rs.Plan.Mode,
			Dimensions:   dimension.Map(rs.Plan.Dimensions),
		}
		switch level {
		case "suite_mode":
		case "suite_mode_engine":
			base.EngineID = rs.Plan.EngineID
		case "suite_mode_engine_item":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
		case "suite_mode_engine_item_variant":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
			base.VariantID = rs.Plan.VariantID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
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

func recognitionScopes(rs *resultState, levels []string) []MetricScope {
	var scopes []MetricScope
	for _, level := range levels {
		base := MetricScope{
			Level:        level,
			SuiteID:      rs.Plan.SuiteID,
			SuiteVersion: rs.Plan.SuiteVersion,
			Mode:         rs.Plan.Mode,
			Dimensions:   dimension.Map(rs.Plan.Dimensions),
		}
		switch level {
		case "suite_mode":
		case "suite_mode_engine":
			base.EngineID = rs.Plan.EngineID
		case "suite_mode_engine_item":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
		case "suite_mode_engine_item_variant":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
			base.VariantID = rs.Plan.VariantID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
		case "suite_mode_engine_tag":
			base.EngineID = rs.Plan.EngineID
			for _, tag := range rs.Item.Tags {
				scoped := base
				scoped.Tag = tag
				scoped.Key = scopeKey(scoped)
				scopes = append(scopes, scoped)
			}
			continue
		case "suite_mode_item":
			base.ItemID = rs.Plan.ItemID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
		case "suite_mode_item_variant":
			base.ItemID = rs.Plan.ItemID
			base.VariantID = rs.Plan.VariantID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
		default:
			continue
		}
		base.Key = scopeKey(base)
		scopes = append(scopes, base)
	}
	return scopes
}

func competitorScopes(rs *resultState, levels []string) []MetricScope {
	var out []MetricScope
	for _, scope := range ordinaryScopes(rs, levels) {
		for _, competitor := range rs.Item.Competitors {
			scoped := scope
			scoped.EntityType = "competitor"
			scoped.EntityID = competitor.Name
			scoped.Key = scopeKey(scoped)
			out = append(out, scoped)
		}
	}
	return out
}

func sourceChannelScopes(rs *resultState, levels []string, channel string) []MetricScope {
	var out []MetricScope
	for _, scope := range ordinaryScopes(rs, levels) {
		scope.SourceChannel = channel
		scope.Key = scopeKey(scope)
		out = append(out, scope)
	}
	return out
}

func sourceMetricScopes(rs *resultState, levels []string, metricID, channel string) []MetricScope {
	var out []MetricScope
	for _, scope := range ordinaryScopes(rs, levels) {
		scope.SourceChannel = channel
		for _, dimension := range sourceDimensions(metricID, rs) {
			scoped := scope
			applySourceDimension(&scoped, dimension)
			out = append(out, scoped)
		}
	}
	return out
}

func crossModeScopes(rs *resultState, levels []string, from, to string) []MetricScope {
	var scopes []MetricScope
	for _, level := range levels {
		base := MetricScope{
			Level:        level,
			SuiteID:      rs.Plan.SuiteID,
			SuiteVersion: rs.Plan.SuiteVersion,
			ModeFrom:     from,
			ModeTo:       to,
			Dimensions:   dimension.Map(rs.Plan.Dimensions),
		}
		switch level {
		case "suite_mode_engine":
			base.EngineID = rs.Plan.EngineID
		case "suite_mode_engine_item":
			base.EngineID = rs.Plan.EngineID
			base.ItemID = rs.Plan.ItemID
			base.ComparisonGroupID = rs.Plan.ComparisonGroupID
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

func scopeKey(scope MetricScope) string {
	parts := []string{
		"level=" + scope.Level,
		"suite_id=" + scope.SuiteID,
		"suite_version=" + intString(scope.SuiteVersion),
		"mode=" + scope.Mode,
		"mode_from=" + scope.ModeFrom,
		"mode_to=" + scope.ModeTo,
		"engine_id=" + scope.EngineID,
		"item_id=" + scope.ItemID,
		"variant_id=" + scope.VariantID,
		"tag=" + scope.Tag,
		"entity_type=" + scope.EntityType,
		"entity_id=" + scope.EntityID,
		"domain=" + scope.Domain,
		"source_channel=" + scope.SourceChannel,
	}
	if len(scope.Dimensions) > 0 {
		var keys []string
		for key := range scope.Dimensions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, "dimension."+key+"="+scope.Dimensions[key])
		}
	}
	return strings.Join(parts, "|")
}

// normalizeUnavailableReasons gives each metric without a value a reason,
// and clears the reason from metrics that have a value.
func normalizeUnavailableReasons(metrics []MetricValue, results []*resultState) {
	for i := range metrics {
		m := &metrics[i]
		if m.Status == MetricStatusAvailable {
			m.UnavailableReason = ""
			continue
		}
		if m.UnavailableReason == "" && auditDeltaMetricID(m.MetricID) {
			m.UnavailableReason = ReasonInsufficientObservations
		}
		if m.UnavailableReason == "" {
			m.UnavailableReason = unavailableReasonForScope(m.Scope, results)
		}
	}
}

func resultContributesToScope(rs *resultState, scope MetricScope) bool {
	if rs.Plan.SuiteID != scope.SuiteID || rs.Plan.SuiteVersion != scope.SuiteVersion {
		return false
	}
	if scope.Mode != "" && rs.Plan.Mode != scope.Mode {
		return false
	}
	if scope.ModeFrom != "" || scope.ModeTo != "" {
		if rs.Plan.Mode != scope.ModeFrom && rs.Plan.Mode != scope.ModeTo {
			return false
		}
	}
	if scope.EngineID != "" && rs.Plan.EngineID != scope.EngineID {
		return false
	}
	if scope.ItemID != "" && rs.Plan.ItemID != scope.ItemID {
		return false
	}
	if scope.VariantID != "" && rs.Plan.VariantID != scope.VariantID {
		return false
	}
	if scope.Tag != "" && !slices.Contains(rs.Item.Tags, scope.Tag) {
		return false
	}
	if !dimension.Equal(scope.Dimensions, rs.Plan.Dimensions) {
		return false
	}
	if scope.ComparisonGroupID != "" && rs.Plan.ComparisonGroupID != scope.ComparisonGroupID {
		return false
	}
	return true
}

func unavailableReasonForScope(scope MetricScope, results []*resultState) string {
	hasMatching := false
	for _, rs := range results {
		if !resultContributesToScope(rs, scope) {
			continue
		}
		hasMatching = true
		switch rs.Result.Raw.Status {
		case run.ResultStatusUnfinished:
			return ReasonUnfinishedAnswer
		case run.ResultStatusError:
			return ReasonProviderError
		case run.ResultStatusCancelled:
			return ReasonCancelled
		case run.ResultStatusNotStarted:
			return ReasonNotStarted
		}
	}
	if scope.SourceChannel != "" {
		return ReasonSourceChannelUnavailable
	}
	if !hasMatching {
		return ReasonNotStarted
	}
	return ReasonInsufficientObservations
}

func evaluatorRef(prepared PreparedEvaluation, plan MetricPlan) EvaluatorRef {
	evaluator := prepared.evaluatorByKey[evaluatorKey(plan.SuiteID, plan.SuiteVersion, plan.DeclarationID)]
	declarationID := plan.DeclarationID
	return EvaluatorRef{DeclarationID: declarationID, EvaluatorType: evaluator.EvaluatorType, EvaluatorVersion: evaluator.EvaluatorVersion}
}

func selectedMetricPlans(results []*resultState, prepared PreparedEvaluation) []MetricPlan {
	seen := map[string]struct{}{}
	var out []MetricPlan
	for _, rs := range results {
		for _, metricID := range rs.MetricIDs {
			key := metricKey(rs.Plan.SuiteID, rs.Plan.SuiteVersion, metricID)
			if _, ok := seen[key]; ok {
				continue
			}
			plan, ok := prepared.metricPlanByKey[key]
			if !ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, plan)
		}
	}
	for _, summaryPlan := range prepared.recognitionSummaryPlans {
		for _, metricID := range summaryPlan.MetricIDs {
			key := metricKey(summaryPlan.SuiteID, summaryPlan.SuiteVersion, metricID)
			if _, ok := seen[key]; ok {
				continue
			}
			plan, ok := prepared.metricPlanByKey[key]
			if !ok || !strings.HasPrefix(plan.MetricID, "recognition.") {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, plan)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SuiteID != out[j].SuiteID {
			return out[i].SuiteID < out[j].SuiteID
		}
		if out[i].SuiteVersion != out[j].SuiteVersion {
			return out[i].SuiteVersion < out[j].SuiteVersion
		}
		return out[i].MetricID < out[j].MetricID
	})
	return out
}

func resultAppliesToMetric(rs *resultState, plan MetricPlan, metricID string) bool {
	return rs.Plan.SuiteID == plan.SuiteID &&
		rs.Plan.SuiteVersion == plan.SuiteVersion &&
		resultHasMetric(rs, metricID)
}

func resultHasMetric(rs *resultState, metricID string) bool {
	for _, id := range rs.MetricIDs {
		if id == metricID {
			return true
		}
	}
	return false
}

func sourceChannelSupported(source *sourceAnalysis, channel string) bool {
	if source == nil {
		return false
	}
	if source.UnavailableReason != "" {
		return false
	}
	switch channel {
	case "citation":
		return source.CitationSupported
	case "consulted":
		return source.ConsultedSupported
	default:
		return false
	}
}

func sourceObservations(source *sourceAnalysis, channel string) []domainObservation {
	if source == nil {
		return nil
	}
	switch channel {
	case "citation":
		return source.Citations
	case "consulted":
		return source.Consulted
	default:
		return nil
	}
}

type sourceDimension struct {
	EntityType string
	EntityID   string
	Domain     string
}

func sourceDimensions(metricID string, rs *resultState) []sourceDimension {
	switch {
	case strings.Contains(metricID, "competitor_domain"):
		var out []sourceDimension
		for _, competitor := range rs.Item.Competitors {
			out = append(out, sourceDimension{EntityType: "competitor", EntityID: competitor.Name})
		}
		if len(out) == 0 && len(rs.Item.CompetitorDomains) > 0 {
			return []sourceDimension{{EntityType: "competitor"}}
		}
		return out
	case strings.Contains(metricID, "preferred_domain"):
		return dimensionsForDomains(rs.Item.PreferredDomains)
	case strings.Contains(metricID, "blocked_domain"):
		return dimensionsForDomains(rs.Item.BlockedDomains)
	default:
		return []sourceDimension{{}}
	}
}

func dimensionsForDomains(domains []string) []sourceDimension {
	if len(domains) == 0 {
		return []sourceDimension{{}}
	}
	var out []sourceDimension
	for _, domain := range domains {
		out = append(out, sourceDimension{Domain: domain})
	}
	return out
}

func applySourceDimension(scope *MetricScope, dimension sourceDimension) {
	scope.EntityType = dimension.EntityType
	scope.EntityID = dimension.EntityID
	scope.Domain = dimension.Domain
	scope.Key = scopeKey(*scope)
}

func sourceResultHasMatch(metricID string, rs *resultState, observations []domainObservation, dimension sourceDimension) bool {
	seen := map[string]struct{}{}
	for _, observation := range observations {
		if _, ok := seen[observation.Domain]; ok {
			continue
		}
		seen[observation.Domain] = struct{}{}
		if observationMatches(metricID, rs, observation, dimension) {
			return true
		}
	}
	return false
}

func observationMatches(metricID string, rs *resultState, observation domainObservation, dimension sourceDimension) bool {
	switch {
	case strings.Contains(metricID, "owned_domain"):
		return policyMatches(observation.Domain, rs.Item.OwnedDomains)
	case strings.Contains(metricID, "competitor_domain"):
		for _, competitor := range rs.Item.Competitors {
			if dimension.EntityID != "" && competitor.Name != dimension.EntityID {
				continue
			}
			if policyMatches(observation.Domain, competitor.Domains) {
				return true
			}
		}
		if dimension.EntityID != "" {
			return false
		}
		return policyMatches(observation.Domain, rs.Item.CompetitorDomains)
	case strings.Contains(metricID, "preferred_domain"):
		if dimension.Domain != "" {
			return policyMatches(observation.Domain, []string{dimension.Domain})
		}
		return policyMatches(observation.Domain, rs.Item.PreferredDomains)
	case strings.Contains(metricID, "blocked_domain"):
		if dimension.Domain != "" {
			return policyMatches(observation.Domain, []string{dimension.Domain})
		}
		return policyMatches(observation.Domain, rs.Item.BlockedDomains)
	default:
		return false
	}
}

func evidenceIDs(records []EvidenceRecord) []string {
	var out []string
	for _, record := range records {
		out = append(out, record.ID)
	}
	return out
}

func evidenceIDsForObservations(observations []domainObservation) []string {
	var out []string
	for _, observation := range observations {
		out = append(out, observation.Evidence.ID)
	}
	return out
}

func jaccardDistance(a, b []string) float64 {
	left := map[string]struct{}{}
	right := map[string]struct{}{}
	for _, value := range a {
		left[value] = struct{}{}
	}
	for _, value := range b {
		right[value] = struct{}{}
	}
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	intersection := 0
	for value := range left {
		if _, ok := right[value]; ok {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	return 1 - float64(intersection)/float64(union)
}

func floatPtr(value float64) *float64 {
	return &value
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	n := value
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func sortMetricValues(values []MetricValue) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].MetricID == values[j].MetricID {
			return values[i].ID < values[j].ID
		}
		return values[i].MetricID < values[j].MetricID
	})
}
