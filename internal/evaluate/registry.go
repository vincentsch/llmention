package evaluate

import "sort"

type evaluatorDefinition struct {
	Type          string
	Version       string
	ModelAssisted bool
}

type metricDefinition struct {
	ID             string
	EvaluatorType  string
	Kind           string
	ScopeLevels    []string
	RequiredInputs []string
}

var evaluatorDefinitions = map[string]evaluatorDefinition{
	EvaluatorMentionMatch: {
		Type:    EvaluatorMentionMatch,
		Version: EvaluatorVersionMentionMatch,
	},
	EvaluatorSourceDomain: {
		Type:    EvaluatorSourceDomain,
		Version: EvaluatorVersionSourceDomain,
	},
	EvaluatorStability: {
		Type:    EvaluatorStability,
		Version: EvaluatorVersionStability,
	},
	EvaluatorFramingSentiment: {
		Type:          EvaluatorFramingSentiment,
		Version:       EvaluatorVersionFramingSentiment,
		ModelAssisted: true,
	},
	EvaluatorEntityCluster: {
		Type:          EvaluatorEntityCluster,
		Version:       EvaluatorVersionEntityCluster,
		ModelAssisted: true,
	},
	EvaluatorExactOrAlias: {
		Type:    EvaluatorExactOrAlias,
		Version: EvaluatorVersionExactOrAlias,
	},
	EvaluatorClaimCoverage: {
		Type:          EvaluatorClaimCoverage,
		Version:       EvaluatorVersionClaimCoverage,
		ModelAssisted: true,
	},
	EvaluatorClaimStance: {
		Type:          EvaluatorClaimStance,
		Version:       EvaluatorVersionClaimStance,
		ModelAssisted: true,
	},
}

var ordinaryScopeLevels = []string{
	"suite_mode",
	"suite_mode_engine",
	"suite_mode_engine_item",
	"suite_mode_engine_item_variant",
	"suite_mode_engine_tag",
}

var itemVariantScopeLevels = []string{
	"suite_mode",
	"suite_mode_engine",
	"suite_mode_engine_item",
	"suite_mode_engine_item_variant",
}

var recognitionScopeLevels = []string{
	"suite_mode",
	"suite_mode_engine",
	"suite_mode_engine_item",
	"suite_mode_engine_item_variant",
	"suite_mode_engine_tag",
	"suite_mode_item",
	"suite_mode_item_variant",
}

var crossModeScopeLevels = []string{
	"suite_mode_engine",
	"suite_mode_engine_item",
	"suite_mode_engine_tag",
}

var metricDefinitions = map[string]metricDefinition{
	"factoid.correct_rate": {
		ID: "factoid.correct_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"factoid.incorrect_rate": {
		ID: "factoid.incorrect_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"factoid.not_attempted_rate": {
		ID: "factoid.not_attempted_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"factoid.overconfident_wrong_rate": {
		ID: "factoid.overconfident_wrong_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"factoid.search_recovery_rate": {
		ID: "factoid.search_recovery_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"factoid.search_regression_rate": {
		ID: "factoid.search_regression_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"programming.correct_rate": {
		ID: "programming.correct_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.not_attempted_rate": {
		ID: "programming.not_attempted_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.hallucinated_api_rate": {
		ID: "programming.hallucinated_api_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.version_drift_rate": {
		ID: "programming.version_drift_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.deprecated_as_current_rate": {
		ID: "programming.deprecated_as_current_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.wrong_error_cause_rate": {
		ID: "programming.wrong_error_cause_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"programming.search_recovery_rate": {
		ID: "programming.search_recovery_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"programming.search_regression_rate": {
		ID: "programming.search_regression_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"temporal.correct_rate": {
		ID: "temporal.correct_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"temporal.stale_answer_rate": {
		ID: "temporal.stale_answer_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"temporal.correct_abstention_rate": {
		ID: "temporal.correct_abstention_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"temporal.cutoff_honesty_rate": {
		ID: "temporal.cutoff_honesty_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"temporal.search_correct_rate": {
		ID: "temporal.search_correct_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer"},
	},
	"temporal.search_recovery_rate": {
		ID: "temporal.search_recovery_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"temporal.search_regression_rate": {
		ID: "temporal.search_regression_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "exact_answer"},
	},
	"temporal.source_requirement_met_rate": {
		ID: "temporal.source_requirement_met_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer", "source_metadata"},
	},
	"temporal.source_freshness_rate": {
		ID: "temporal.source_freshness_rate", EvaluatorType: EvaluatorExactOrAlias, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"exact_answer", "source_metadata"},
	},
	"visibility.brand_mention_rate": {
		ID: "visibility.brand_mention_rate", EvaluatorType: EvaluatorMentionMatch, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"answer_text", "target_context"},
	},
	"visibility.brand_share_of_voice": {
		ID: "visibility.brand_share_of_voice", EvaluatorType: EvaluatorMentionMatch, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"answer_text", "target_context"},
	},
	"visibility.brand_rank_avg": {
		ID: "visibility.brand_rank_avg", EvaluatorType: EvaluatorMentionMatch, Kind: "number", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"answer_text", "target_context"},
	},
	"visibility.competitor_mention_rate": {
		ID: "visibility.competitor_mention_rate", EvaluatorType: EvaluatorMentionMatch, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"answer_text", "target_context"},
	},
	"visibility.competitor_precedes_brand_rate": {
		ID: "visibility.competitor_precedes_brand_rate", EvaluatorType: EvaluatorMentionMatch, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"answer_text", "target_context"},
	},
	"visibility.search_recovery_rate": {
		ID: "visibility.search_recovery_rate", EvaluatorType: EvaluatorMentionMatch, Kind: "rate", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "target_context"},
	},
	"sources.owned_domain_citation_rate": {
		ID: "sources.owned_domain_citation_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.competitor_domain_citation_rate": {
		ID: "sources.competitor_domain_citation_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.preferred_domain_citation_rate": {
		ID: "sources.preferred_domain_citation_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.blocked_domain_citation_rate": {
		ID: "sources.blocked_domain_citation_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.owned_domain_citation_share": {
		ID: "sources.owned_domain_citation_share", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.competitor_domain_citation_share": {
		ID: "sources.competitor_domain_citation_share", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.preferred_domain_citation_share": {
		ID: "sources.preferred_domain_citation_share", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.blocked_domain_citation_share": {
		ID: "sources.blocked_domain_citation_share", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "source_policy"},
	},
	"sources.owned_domain_consulted_rate": {
		ID: "sources.owned_domain_consulted_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"consulted_sources", "source_policy"},
	},
	"sources.competitor_domain_consulted_rate": {
		ID: "sources.competitor_domain_consulted_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"consulted_sources", "source_policy"},
	},
	"sources.cited_domain_count": {
		ID: "sources.cited_domain_count", EvaluatorType: EvaluatorSourceDomain, Kind: "integer", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations"},
	},
	"sources.consulted_domain_count": {
		ID: "sources.consulted_domain_count", EvaluatorType: EvaluatorSourceDomain, Kind: "integer", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"consulted_sources"},
	},
	"sources.citation_domain_observation_count": {
		ID: "sources.citation_domain_observation_count", EvaluatorType: EvaluatorSourceDomain, Kind: "integer", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations"},
	},
	"sources.consulted_domain_observation_count": {
		ID: "sources.consulted_domain_observation_count", EvaluatorType: EvaluatorSourceDomain, Kind: "integer", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"consulted_sources"},
	},
	"sources.search_query_count": {
		ID: "sources.search_query_count", EvaluatorType: EvaluatorSourceDomain, Kind: "integer", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"tool_calls"},
	},
	"sources.citation_source_mismatch_rate": {
		ID: "sources.citation_source_mismatch_rate", EvaluatorType: EvaluatorSourceDomain, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"citations", "consulted_sources"},
	},
	"robustness.mention_variance": {
		ID: "robustness.mention_variance", EvaluatorType: EvaluatorStability, Kind: "number", ScopeLevels: itemVariantScopeLevels, RequiredInputs: []string{"mention_match"},
	},
	"robustness.citation_domain_variance": {
		ID: "robustness.citation_domain_variance", EvaluatorType: EvaluatorStability, Kind: "number", ScopeLevels: itemVariantScopeLevels, RequiredInputs: []string{"source_domain"},
	},
	"robustness.consulted_domain_variance": {
		ID: "robustness.consulted_domain_variance", EvaluatorType: EvaluatorStability, Kind: "number", ScopeLevels: itemVariantScopeLevels, RequiredInputs: []string{"source_domain"},
	},
	"framing.evaluated_rate": {
		ID: "framing.evaluated_rate", EvaluatorType: EvaluatorFramingSentiment, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"mention_match", "judge"},
	},
	"framing.sentiment_positive_rate": {
		ID: "framing.sentiment_positive_rate", EvaluatorType: EvaluatorFramingSentiment, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"judge"},
	},
	"framing.sentiment_neutral_rate": {
		ID: "framing.sentiment_neutral_rate", EvaluatorType: EvaluatorFramingSentiment, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"judge"},
	},
	"framing.sentiment_negative_rate": {
		ID: "framing.sentiment_negative_rate", EvaluatorType: EvaluatorFramingSentiment, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"judge"},
	},
	"framing.sentiment_mixed_rate": {
		ID: "framing.sentiment_mixed_rate", EvaluatorType: EvaluatorFramingSentiment, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"judge"},
	},
	"recognition.target_recognition_rate": {
		ID: "recognition.target_recognition_rate", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.known_namesake_rate": {
		ID: "recognition.known_namesake_rate", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.unknown_namesake_rate": {
		ID: "recognition.unknown_namesake_rate", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.hallucination_candidate_rate": {
		ID: "recognition.hallucination_candidate_rate", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.non_target_rate": {
		ID: "recognition.non_target_rate", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.model_coverage": {
		ID: "recognition.model_coverage", EvaluatorType: EvaluatorEntityCluster, Kind: "rate", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.avg_confidence": {
		ID: "recognition.avg_confidence", EvaluatorType: EvaluatorEntityCluster, Kind: "number", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"recognition.strength": {
		ID: "recognition.strength", EvaluatorType: EvaluatorEntityCluster, Kind: "score", ScopeLevels: recognitionScopeLevels, RequiredInputs: []string{"entity_cluster"},
	},
	"censorship.expected_claim_coverage": {
		ID: "censorship.expected_claim_coverage", EvaluatorType: EvaluatorClaimCoverage, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"censorship.forbidden_failure_rate": {
		ID: "censorship.forbidden_failure_rate", EvaluatorType: EvaluatorClaimCoverage, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"censorship.refusal_rate": {
		ID: "censorship.refusal_rate", EvaluatorType: EvaluatorClaimCoverage, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"censorship.omission_score": {
		ID: "censorship.omission_score", EvaluatorType: EvaluatorClaimCoverage, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"censorship.evasion_rate": {
		ID: "censorship.evasion_rate", EvaluatorType: EvaluatorClaimCoverage, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"censorship.direct_indirect_delta": {
		ID: "censorship.direct_indirect_delta", EvaluatorType: EvaluatorClaimCoverage, Kind: "delta", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_variants", "claim_contract"},
	},
	"censorship.memory_search_delta": {
		ID: "censorship.memory_search_delta", EvaluatorType: EvaluatorClaimCoverage, Kind: "delta", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "claim_contract"},
	},
	"propaganda.false_premise_repeated_rate": {
		ID: "propaganda.false_premise_repeated_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"propaganda.false_premise_corrected_rate": {
		ID: "propaganda.false_premise_corrected_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"propaganda.expected_correction_rate": {
		ID: "propaganda.expected_correction_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"propaganda.unsupported_hedge_rate": {
		ID: "propaganda.unsupported_hedge_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "answer_text"},
	},
	"propaganda.non_flagged_source_rate": {
		ID: "propaganda.non_flagged_source_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "source_policy", "source_metadata"},
	},
	"propaganda.low_quality_source_rate": {
		ID: "propaganda.low_quality_source_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "source_policy", "source_metadata"},
	},
	"propaganda.source_policy_violation_rate": {
		ID: "propaganda.source_policy_violation_rate", EvaluatorType: EvaluatorClaimStance, Kind: "rate", ScopeLevels: ordinaryScopeLevels, RequiredInputs: []string{"claim_contract", "source_policy", "source_metadata"},
	},
	"propaganda.memory_search_susceptibility_delta": {
		ID: "propaganda.memory_search_susceptibility_delta", EvaluatorType: EvaluatorClaimStance, Kind: "delta", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_modes", "claim_contract"},
	},
	"propaganda.prompt_framing_sensitivity": {
		ID: "propaganda.prompt_framing_sensitivity", EvaluatorType: EvaluatorClaimStance, Kind: "delta", ScopeLevels: crossModeScopeLevels, RequiredInputs: []string{"paired_variants", "claim_contract"},
	},
}

func InstalledEvaluatorTypes() []string {
	out := make([]string, 0, len(evaluatorDefinitions))
	for typ := range evaluatorDefinitions {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}

func SupportedMetricIDs() []string {
	out := make([]string, 0, len(metricDefinitions))
	for id := range metricDefinitions {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func supportedMetricIDsForEvaluator(evaluatorType string) []string {
	var out []string
	for id, def := range metricDefinitions {
		if def.EvaluatorType == evaluatorType {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
