package evaluate

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/mention"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

type SuiteExecutionSummary struct {
	ExecutionStatus      string   `json:"execution_status"`
	ReadinessReasonCodes []string `json:"readiness_reason_codes,omitempty"`
}

type SuiteExecutionReport struct {
	ID                   string   `json:"id"`
	Version              int      `json:"version"`
	ExecutionStatus      string   `json:"execution_status"`
	ReadinessReasonCodes []string `json:"readiness_reason_codes,omitempty"`
}

func Prepare(input PrepareInput) (PreparedEvaluation, error) {
	if len(input.Suites) == 0 {
		return PreparedEvaluation{}, fmt.Errorf("prepare requires bound suites")
	}
	snapshots := input.BoundSuites
	if len(snapshots) == 0 {
		snapshots = SuiteSnapshots(input.Suites)
	}
	prepared := PreparedEvaluation{
		BoundSuites:        snapshots,
		EngineCapabilities: append([]EngineCapabilitySnapshot(nil), input.EngineCapabilities...),
		ReadinessIssues:    append([]ReadinessIssue(nil), input.PrecomputedIssues...),
		suitesByKey:        map[string]*suite.Suite{},
		itemsByWorkKey:     map[string]suite.Item{},
		resultPlansByKey:   map[string]ResultPlan{},
		evaluatorByKey:     map[string]ResolvedEvaluator{},
		metricPlanByKey:    map[string]MetricPlan{},
		metricDeclByKey:    map[string]suite.MetricDeclaration{},
		metricEvaluatorKey: map[string]string{},
	}

	suiteByID := map[string]suite.LoadedSuite{}
	for _, entry := range input.Suites {
		if entry.Suite == nil {
			continue
		}
		key := suiteKey(entry.Suite.ID, entry.Suite.Version)
		prepared.suitesByKey[key] = entry.Suite
		suiteByID[entry.Suite.ID] = entry
		prepared.ReadinessIssues = append(prepared.ReadinessIssues, suiteReadinessIssues(&input.Project, entry)...)
		prepared.EvaluatorDeclarations = append(prepared.EvaluatorDeclarations, resolvedEvaluators(entry.Suite)...)
		for _, metric := range entry.Suite.Metrics {
			plan, ok := metricPlan(entry.Suite, metric)
			if !ok {
				continue
			}
			prepared.MetricPlans = append(prepared.MetricPlans, plan)
			key := metricKey(entry.Suite.ID, entry.Suite.Version, metric.ID)
			prepared.metricPlanByKey[key] = plan
			prepared.metricDeclByKey[key] = metric
			prepared.metricEvaluatorKey[key] = evaluatorKey(entry.Suite.ID, entry.Suite.Version, metric.Evaluator)
		}
	}
	sortResolvedEvaluators(prepared.EvaluatorDeclarations)
	for _, declaration := range prepared.EvaluatorDeclarations {
		prepared.evaluatorByKey[evaluatorKey(declaration.SuiteID, declaration.SuiteVersion, declaration.DeclarationID)] = declaration
	}

	for _, unit := range input.Plan.WorkUnits {
		entry, ok := suiteByID[unit.SuiteID]
		if !ok || entry.Suite == nil {
			return PreparedEvaluation{}, fmt.Errorf("plan references suite %q that is not in prepared suites", unit.SuiteID)
		}
		item, ok := findItem(entry.Suite, unit.ItemID)
		if !ok {
			return PreparedEvaluation{}, fmt.Errorf("plan references item %q that is not in suite %q", unit.ItemID, unit.SuiteID)
		}
		evaluatorIDs := selectedEvaluatorIDs(entry.Suite, item)
		metricIDs := selectedMetricIDs(entry.Suite, item)
		for _, metricID := range metricIDs {
			evaluatorLookupKey := prepared.metricEvaluatorKey[metricKey(entry.Suite.ID, entry.Suite.Version, metricID)]
			evaluatorID := declarationIDFromKey(evaluatorLookupKey)
			if evaluatorLookupKey != "" && !slices.Contains(evaluatorIDs, evaluatorID) {
				prepared.ReadinessIssues = append(prepared.ReadinessIssues, readinessIssue(
					"missing_target_context",
					ReadinessFatal,
					entry.Suite.ID,
					entry.Suite.Version,
					item.ID,
					"",
					metricID,
					fmt.Sprintf("metric %q needs the evaluator %q, which this item does not use; add it to the item's evaluator_ids", metricID, evaluatorID),
				))
			}
		}
		workKey := WorkKey(unit)
		resultPlan := ResultPlan{
			WorkKey:           workKey,
			SuiteID:           unit.SuiteID,
			SuiteVersion:      unit.SuiteVersion,
			ItemID:            unit.ItemID,
			VariantID:         unit.VariantID,
			Dimensions:        dimension.Map(unit.Dimensions),
			ComparisonGroupID: unit.ComparisonGroupID,
			Mode:              unit.Mode.String(),
			EngineID:          unit.EngineID,
			Repetition:        unit.Repetition,
			EvaluatorIDs:      evaluatorIDs,
			MetricIDs:         metricIDs,
		}
		if len(resultPlan.Dimensions) == 0 {
			resultPlan.Dimensions = dimension.Map(item.Dimensions)
		}
		if resultPlan.ComparisonGroupID == "" {
			resultPlan.ComparisonGroupID = item.ComparisonGroupID
		}
		if _, dup := prepared.resultPlansByKey[workKey]; dup {
			return PreparedEvaluation{}, fmt.Errorf("duplicate prepared work key %q", workKey)
		}
		prepared.ResultPlans = append(prepared.ResultPlans, resultPlan)
		prepared.resultPlansByKey[workKey] = resultPlan
		prepared.itemsByWorkKey[workKey] = item
	}
	prepared.recognitionSummaryPlans = buildRecognitionSummaryPlans(input.Plan, input.Suites)

	modelAssisted, err := buildModelAssistedPlan(input.ModelAssisted, input.Plan, prepared.ResultPlans, prepared.evaluatorByKey, &prepared.ReadinessIssues)
	if err != nil {
		return PreparedEvaluation{}, err
	}
	prepared.ModelAssisted = modelAssisted
	sortPrepared(&prepared)
	prepared.EvaluationReady = !hasFatalIssues(prepared.ReadinessIssues)
	prepared.ID = "prep_" + stableHash(struct {
		Suites      []SuiteSnapshot
		Evaluators  []ResolvedEvaluator
		Metrics     []MetricPlan
		Engines     []EngineCapabilitySnapshot
		ResultPlans []ResultPlan
		Recognition []RecognitionSummaryPlan
		Model       ModelAssistedPlan
	}{
		Suites:      prepared.BoundSuites,
		Evaluators:  prepared.EvaluatorDeclarations,
		Metrics:     prepared.MetricPlans,
		Engines:     prepared.EngineCapabilities,
		ResultPlans: prepared.ResultPlans,
		Recognition: prepared.recognitionSummaryPlans,
		Model:       prepared.ModelAssisted,
	})
	return prepared, nil
}

func buildRecognitionSummaryPlans(plan run.Plan, entries []suite.LoadedSuite) []RecognitionSummaryPlan {
	suitePlans := map[string]run.SuitePlan{}
	for _, suitePlan := range plan.Suites {
		suitePlans[suiteKey(suitePlan.ID, suitePlan.Version)] = suitePlan
	}
	var out []RecognitionSummaryPlan
	for _, entry := range entries {
		if entry.Suite == nil || entry.Suite.Type != "recognition" {
			continue
		}
		suitePlan, ok := suitePlans[suiteKey(entry.Suite.ID, entry.Suite.Version)]
		if !ok {
			continue
		}
		for _, item := range entry.Suite.Items {
			rendered, err := suite.RenderVariants(entry.Suite, item)
			if err != nil {
				continue
			}
			metricIDs := selectedMetricIDs(entry.Suite, item)
			for _, mode := range suitePlan.Modes {
				engineIDs := selectedEngineIDsForMode(plan, mode)
				for _, variant := range rendered {
					summary := RecognitionSummaryPlan{
						SuiteID:           entry.Suite.ID,
						SuiteVersion:      entry.Suite.Version,
						ItemID:            item.ID,
						VariantID:         variant.VariantID,
						Mode:              mode,
						Dimensions:        dimension.Map(variant.Dimensions),
						ComparisonGroupID: variant.ComparisonGroupID,
						SelectedEngineIDs: append([]string(nil), engineIDs...),
						MetricIDs:         append([]string(nil), metricIDs...),
						Tags:              append([]string(nil), item.Tags...),
					}
					for _, engineID := range engineIDs {
						for repetitionIndex := 0; repetitionIndex < suitePlan.Repetitions; repetitionIndex++ {
							repetition := repetitionIndex + 1
							summary.PlannedWorkKeys = append(summary.PlannedWorkKeys, WorkKey(run.WorkUnit{
								SuiteID:           entry.Suite.ID,
								SuiteVersion:      entry.Suite.Version,
								ItemID:            item.ID,
								VariantID:         variant.VariantID,
								Dimensions:        dimension.Map(variant.Dimensions),
								ComparisonGroupID: variant.ComparisonGroupID,
								Mode:              engine.ToolPolicy(mode),
								EngineID:          engineID,
								Repetition:        repetition,
							}))
						}
					}
					out = append(out, summary)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SuiteID != out[j].SuiteID {
			return out[i].SuiteID < out[j].SuiteID
		}
		if out[i].ItemID != out[j].ItemID {
			return out[i].ItemID < out[j].ItemID
		}
		if out[i].VariantID != out[j].VariantID {
			return out[i].VariantID < out[j].VariantID
		}
		return out[i].Mode < out[j].Mode
	})
	return out
}

func selectedEngineIDsForMode(plan run.Plan, mode string) []string {
	seen := map[string]struct{}{}
	for _, row := range plan.EngineModeMatrix {
		if row.Mode == mode && row.EngineID != "" {
			seen[row.EngineID] = struct{}{}
		}
	}
	for _, unit := range plan.WorkUnits {
		if unit.Mode.String() == mode && unit.EngineID != "" {
			seen[unit.EngineID] = struct{}{}
		}
	}
	for _, pair := range plan.UnsupportedPairs {
		if pair.Mode == mode && pair.EngineID != "" {
			seen[pair.EngineID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func SuiteExecutionStatus(project *config.Project, entry suite.LoadedSuite) SuiteExecutionSummary {
	return SuiteExecutionStatusWithIssues(project, entry, nil)
}

func SuiteExecutionStatusWithIssues(project *config.Project, entry suite.LoadedSuite, precomputed []ReadinessIssue) SuiteExecutionSummary {
	issues := append([]ReadinessIssue(nil), suiteReadinessIssues(project, entry)...)
	issues = append(issues, issuesForSuite(precomputed, entry)...)
	issues = dedupeIssues(issues)
	return suiteExecutionSummaryFromIssues(issues)
}

func SuiteExecutionReports(snapshots []SuiteSnapshot, issues []ReadinessIssue) []SuiteExecutionReport {
	bySuite := map[string][]ReadinessIssue{}
	for _, issue := range issues {
		if issue.SuiteID == "" {
			continue
		}
		key := suiteKey(issue.SuiteID, issue.SuiteVersion)
		bySuite[key] = append(bySuite[key], issue)
	}
	out := make([]SuiteExecutionReport, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summary := suiteExecutionSummaryFromIssues(dedupeIssues(bySuite[suiteKey(snapshot.ID, snapshot.Version)]))
		out = append(out, SuiteExecutionReport{
			ID:                   snapshot.ID,
			Version:              snapshot.Version,
			ExecutionStatus:      summary.ExecutionStatus,
			ReadinessReasonCodes: summary.ReadinessReasonCodes,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func suiteExecutionSummaryFromIssues(issues []ReadinessIssue) SuiteExecutionSummary {
	status := "runnable"
	reasons := uniqueIssueCodes(issues)
	for _, issue := range issues {
		if issue.Code == "unsupported_evaluator_implementation" {
			status = "schema_only"
			break
		}
		if issue.Severity == ReadinessFatal {
			status = "not_ready"
		}
	}
	return SuiteExecutionSummary{ExecutionStatus: status, ReadinessReasonCodes: reasons}
}

func issuesForSuite(issues []ReadinessIssue, entry suite.LoadedSuite) []ReadinessIssue {
	if entry.Suite == nil {
		return nil
	}
	var out []ReadinessIssue
	for _, issue := range issues {
		if issue.SuiteID == entry.Suite.ID && issue.SuiteVersion == entry.Suite.Version {
			out = append(out, issue)
		}
	}
	return out
}

func suiteReadinessIssues(project *config.Project, entry suite.LoadedSuite) []ReadinessIssue {
	if entry.Suite == nil {
		return nil
	}
	s := entry.Suite
	var issues []ReadinessIssue
	evaluatorTypeByID := map[string]string{}
	for _, evaluator := range s.Evaluators {
		evaluatorTypeByID[evaluator.ID] = evaluator.Type
		if _, ok := evaluatorDefinitions[evaluator.Type]; !ok {
			issues = append(issues, readinessIssue("unsupported_evaluator_implementation", ReadinessFatal, s.ID, s.Version, "", evaluator.Type, "", fmt.Sprintf("llmention has no evaluator of type %q", evaluator.Type)))
		}
	}
	for _, metric := range s.Metrics {
		evaluatorType := evaluatorTypeByID[metric.Evaluator]
		def, ok := metricDefinitions[metric.ID]
		if !ok {
			issues = append(issues, readinessIssue("unsupported_metric_id", ReadinessFatal, s.ID, s.Version, "", evaluatorType, metric.ID, fmt.Sprintf("llmention does not know the metric %q", metric.ID)))
			continue
		}
		if evaluatorType != "" && def.EvaluatorType != evaluatorType {
			issues = append(issues, readinessIssue("unsupported_metric_id", ReadinessFatal, s.ID, s.Version, "", evaluatorType, metric.ID, fmt.Sprintf("metric %q needs an evaluator of type %q, but its evaluator has type %q", metric.ID, def.EvaluatorType, evaluatorType)))
		}
		if metric.Kind != def.Kind {
			issues = append(issues, readinessIssue("unsupported_metric_kind", ReadinessFatal, s.ID, s.Version, "", evaluatorType, metric.ID, fmt.Sprintf("metric %q must have kind %q", metric.ID, def.Kind)))
		}
	}
	for _, item := range s.Items {
		issues = append(issues, itemContextIssues(s, item, evaluatorTypeByID)...)
	}
	if project != nil && s.ContextMode == suite.ContextModeProjectBound && (project.Brand == nil || project.Brand.Name == "") {
		issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, "", "", "", "this suite needs brand.name in llmention.yaml"))
	}
	return dedupeIssues(issues)
}

func itemContextIssues(s *suite.Suite, item suite.Item, evaluatorTypeByID map[string]string) []ReadinessIssue {
	var issues []ReadinessIssue
	evaluatorIDs := selectedEvaluatorIDs(s, item)
	metricIDs := selectedMetricIDs(s, item)
	needsMention := false
	for _, id := range evaluatorIDs {
		switch evaluatorTypeByID[id] {
		case EvaluatorMentionMatch, EvaluatorFramingSentiment:
			needsMention = true
		case EvaluatorStability:
			for _, metricID := range metricIDs {
				switch metricID {
				case "robustness.mention_variance":
					needsMention = true
				}
			}
		}
	}
	if needsMention && (item.Brand == nil || item.Brand.Name == "") {
		issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, item.ID, EvaluatorMentionMatch, "", "counting mentions needs a brand; set brand.name in llmention.yaml, or brand in the item"))
	}
	issues = append(issues, sourceMetricContextIssues(s, item, metricIDs)...)
	issues = append(issues, aliasConflictIssues(s, item)...)
	issues = append(issues, domainOverlapIssues(s, item)...)
	if containsEvaluatorType(evaluatorIDs, evaluatorTypeByID, EvaluatorStability) {
		if containsMetric(metricIDs, "robustness.mention_variance") && !containsEvaluatorType(evaluatorIDs, evaluatorTypeByID, EvaluatorMentionMatch) {
			issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, item.ID, EvaluatorStability, "robustness.mention_variance", "robustness.mention_variance needs a mention_match evaluator on this item"))
		}
		if (containsMetric(metricIDs, "robustness.citation_domain_variance") || containsMetric(metricIDs, "robustness.consulted_domain_variance")) && !containsEvaluatorType(evaluatorIDs, evaluatorTypeByID, EvaluatorSourceDomain) {
			issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, item.ID, EvaluatorStability, "", "the domain variance metrics need a source_domain evaluator on this item"))
		}
	}
	return issues
}

func sourceMetricContextIssues(s *suite.Suite, item suite.Item, metricIDs []string) []ReadinessIssue {
	var issues []ReadinessIssue
	for _, metricID := range metricIDs {
		def, ok := metricDefinitions[metricID]
		if !ok || def.EvaluatorType != EvaluatorSourceDomain {
			continue
		}
		switch {
		// Without your domains there is nothing to count for citations of your
		// site, but the rest of the run still works, so this is only a warning.
		case strings.Contains(metricID, "owned_domain") && len(item.OwnedDomains) == 0:
			issues = append(issues, readinessIssue("missing_target_context", ReadinessWarning, s.ID, s.Version, item.ID, EvaluatorSourceDomain, metricID, "add brand.domains to measure citations of your site"))
		case strings.Contains(metricID, "competitor_domain") && !hasCompetitorDomainPolicy(item):
			issues = append(issues, readinessIssue("missing_target_context", ReadinessWarning, s.ID, s.Version, item.ID, EvaluatorSourceDomain, metricID, "add competitor domains to measure citations of their sites"))
		case strings.Contains(metricID, "preferred_domain") && len(item.PreferredDomains) == 0:
			issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, item.ID, EvaluatorSourceDomain, metricID, fmt.Sprintf("metric %q needs at least one entry under preferred_domains", metricID)))
		case strings.Contains(metricID, "blocked_domain") && len(item.BlockedDomains) == 0:
			issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, item.ID, EvaluatorSourceDomain, metricID, fmt.Sprintf("metric %q needs at least one entry under blocked_domains", metricID)))
		}
	}
	return issues
}

func hasCompetitorDomainPolicy(item suite.Item) bool {
	if len(item.CompetitorDomains) > 0 {
		return true
	}
	for _, competitor := range item.Competitors {
		if len(competitor.Domains) > 0 {
			return true
		}
	}
	return false
}

func resolvedEvaluators(s *suite.Suite) []ResolvedEvaluator {
	var out []ResolvedEvaluator
	for _, evaluator := range s.Evaluators {
		def, ok := evaluatorDefinitions[evaluator.Type]
		if !ok {
			continue
		}
		out = append(out, ResolvedEvaluator{
			SuiteID:            s.ID,
			SuiteVersion:       s.Version,
			DeclarationID:      evaluator.ID,
			EvaluatorType:      evaluator.Type,
			EvaluatorVersion:   def.Version,
			ModelAssisted:      def.ModelAssisted,
			SupportedMetricIDs: supportedMetricIDsForEvaluator(evaluator.Type),
		})
	}
	return out
}

func metricPlan(s *suite.Suite, metric suite.MetricDeclaration) (MetricPlan, bool) {
	def, ok := metricDefinitions[metric.ID]
	if !ok {
		return MetricPlan{}, false
	}
	return MetricPlan{
		SuiteID:        s.ID,
		SuiteVersion:   s.Version,
		MetricID:       metric.ID,
		DeclarationID:  metric.Evaluator,
		Kind:           metric.Kind,
		ScopeLevels:    append([]string(nil), def.ScopeLevels...),
		RequiredInputs: append([]string(nil), def.RequiredInputs...),
	}, true
}

func buildModelAssistedPlan(input ModelAssistedPlanInput, plan run.Plan, resultPlans []ResultPlan, evaluators map[string]ResolvedEvaluator, issues *[]ReadinessIssue) (ModelAssistedPlan, error) {
	out := ModelAssistedPlan{
		Enabled:                  input.Enabled,
		EnabledSource:            input.EnabledSource,
		RetryAttemptsPerWorkUnit: plan.Retries,
		MaxJudgeRequests:         input.MaxJudgeRequests,
		MaxJudgeRequestsSource:   input.MaxJudgeRequestsSource,
		JudgeConfigured:          input.Judge != nil,
		JudgeEngineID:            engine.JudgeEngineID,
	}
	if input.Judge != nil {
		out.JudgeProvider = input.Judge.Provider
		out.JudgeModel = input.Judge.Model
		out.JudgeKeyEnv = input.Judge.KeyEnv
		out.AllowAnswerDisclosure = input.Judge.AllowAnswerDisclosure
	}
	candidateEvaluators := map[string]struct{}{}
	for _, resultPlan := range resultPlans {
		for _, evaluatorID := range resultPlan.EvaluatorIDs {
			resolved, ok := evaluators[evaluatorKey(resultPlan.SuiteID, resultPlan.SuiteVersion, evaluatorID)]
			if ok && modelAssistedEvaluator(resolved.EvaluatorType) {
				if out.PlannedCandidates == math.MaxInt {
					return ModelAssistedPlan{}, fmt.Errorf("judge candidate count: integer overflow")
				}
				out.PlannedCandidates++
				candidateEvaluators[resultPlan.SuiteID+"/"+evaluatorID] = struct{}{}
			}
		}
	}
	for id := range candidateEvaluators {
		out.CandidateEvaluatorIDs = append(out.CandidateEvaluatorIDs, id)
	}
	sort.Strings(out.CandidateEvaluatorIDs)
	if !out.Enabled {
		// With the grader off the candidates are still counted, but no grader
		// request is planned, so its settings and request cap are not checked.
		out.JudgeReady = true
		return out, nil
	}
	out.RequestUpperBound = out.PlannedCandidates * (1 + plan.Retries)
	if out.PlannedCandidates == 0 {
		out.JudgeReady = true
		return out, nil
	}
	judgeIssueStart := len(*issues)
	if input.Judge == nil {
		*issues = append(*issues, readinessIssue("missing_judge_config", ReadinessFatal, "", 0, "", "", "", "the AI grader is turned on but not set up; add analysis.judge to llmention.yaml, or turn the grader off"))
	} else {
		if !input.Judge.AllowAnswerDisclosure {
			*issues = append(*issues, readinessIssue("answer_disclosure_not_allowed", ReadinessFatal, "", 0, "", "", "", "the AI grader reads the answers it grades; set analysis.judge.allow_answer_disclosure: true in llmention.yaml to allow that"))
		}
		caps := input.JudgeCapability
		if caps == nil {
			if derived, err := engine.CapabilityProfileForConfig(input.Judge.Provider, input.Judge.Model, input.Judge.Capabilities); err == nil {
				caps = &derived
			} else {
				*issues = append(*issues, readinessIssue("unsupported_judge_capability", ReadinessFatal, "", 0, "", "", "", err.Error()))
			}
		}
		if caps != nil && (!caps.SupportsMemory || !caps.SupportsStructuredOutput) {
			*issues = append(*issues, readinessIssue("unsupported_judge_capability", ReadinessFatal, "", 0, "", "", "", "the AI grader needs a model that supports structured output without web search"))
		}
	}
	if input.MaxJudgeRequests <= 0 {
		*issues = append(*issues, readinessIssue("invalid_judge_cap", ReadinessFatal, "", 0, "", "", "", "set execution.max_judge_requests to the most requests the AI grader may get"))
	} else if out.RequestUpperBound > input.MaxJudgeRequests {
		*issues = append(*issues, readinessIssue("judge_cap_exceeded", ReadinessFatal, "", 0, "", "", "", fmt.Sprintf("the AI grader may need %d requests but execution.max_judge_requests is %d; raise it", out.RequestUpperBound, input.MaxJudgeRequests)))
	}
	out.JudgeReady = true
	for _, issue := range (*issues)[judgeIssueStart:] {
		if issue.Severity == ReadinessFatal {
			out.JudgeReady = false
			break
		}
	}
	return out, nil
}

func modelAssistedEvaluator(evaluatorType string) bool {
	switch evaluatorType {
	case EvaluatorFramingSentiment, EvaluatorEntityCluster, EvaluatorClaimCoverage, EvaluatorClaimStance:
		return true
	default:
		return false
	}
}

func selectedEvaluatorIDs(s *suite.Suite, item suite.Item) []string {
	if len(item.EvaluatorIDs) > 0 {
		return uniqueStrings(item.EvaluatorIDs)
	}
	out := make([]string, 0, len(s.Evaluators))
	for _, evaluator := range s.Evaluators {
		out = append(out, evaluator.ID)
	}
	return uniqueStrings(out)
}

func selectedMetricIDs(s *suite.Suite, item suite.Item) []string {
	if len(item.MetricIDs) > 0 {
		return uniqueStrings(item.MetricIDs)
	}
	out := make([]string, 0, len(s.Metrics))
	for _, metric := range s.Metrics {
		out = append(out, metric.ID)
	}
	return uniqueStrings(out)
}

func findItem(s *suite.Suite, id string) (suite.Item, bool) {
	for _, item := range s.Items {
		if item.ID == id {
			return item, true
		}
	}
	return suite.Item{}, false
}

func containsMetric(values []string, want string) bool {
	return slices.Contains(values, want)
}

func containsEvaluatorType(evaluatorIDs []string, evaluatorTypeByID map[string]string, want string) bool {
	for _, id := range evaluatorIDs {
		if evaluatorTypeByID[id] == want {
			return true
		}
	}
	return false
}

func aliasConflictIssues(s *suite.Suite, item suite.Item) []ReadinessIssue {
	type owner struct {
		entityID string
		label    string
	}
	seen := map[string]owner{}
	var issues []ReadinessIssue
	addAliases := func(entityID, label string, aliases []string) {
		local := map[string]struct{}{}
		for _, alias := range aliases {
			key := normalizedAliasKey(alias)
			if key == "" {
				continue
			}
			if _, dup := local[key]; dup {
				continue
			}
			local[key] = struct{}{}
			if previous, ok := seen[key]; ok && previous.entityID != entityID {
				issues = append(issues, readinessIssue("conflicting_alias", ReadinessFatal, s.ID, s.Version, item.ID, "", "", fmt.Sprintf("the name %q counts for both %s and %s; give it to only one of them", alias, previous.label, label)))
				continue
			}
			seen[key] = owner{entityID: entityID, label: label}
		}
	}
	if item.Brand != nil {
		aliases := append([]string{item.Brand.Name}, item.Brand.Aliases...)
		aliases = append(aliases, item.Brand.Domains...)
		aliases = append(aliases, item.OwnedDomains...)
		addAliases("brand", "your brand", aliases)
	}
	for i, competitor := range item.Competitors {
		entityID := fmt.Sprintf("competitor:%d", i)
		label := "competitor " + competitor.Name
		aliases := append([]string{competitor.Name}, competitor.Aliases...)
		aliases = append(aliases, competitor.Domains...)
		addAliases(entityID, label, aliases)
	}
	return issues
}

func domainOverlapIssues(s *suite.Suite, item suite.Item) []ReadinessIssue {
	var issues []ReadinessIssue
	competitorDomains := append([]string(nil), item.CompetitorDomains...)
	for _, competitor := range item.Competitors {
		competitorDomains = append(competitorDomains, competitor.Domains...)
	}
	competitorDomains = uniqueStrings(competitorDomains)
	for _, owned := range item.OwnedDomains {
		for _, competitor := range competitorDomains {
			overlap, err := domain.PoliciesOverlap(owned, competitor)
			if err != nil {
				continue
			}
			if overlap {
				issues = append(issues, readinessIssue("overlapping_domain_policy", ReadinessFatal, s.ID, s.Version, item.ID, "", "", fmt.Sprintf("your site %q overlaps the competitor site %q, so a citation could count for both", owned, competitor)))
			}
		}
	}
	for _, preferred := range item.PreferredDomains {
		for _, blocked := range item.BlockedDomains {
			overlap, err := domain.PoliciesOverlap(preferred, blocked)
			if err != nil {
				continue
			}
			if overlap {
				issues = append(issues, readinessIssue("overlapping_domain_policy", ReadinessFatal, s.ID, s.Version, item.ID, "", "", fmt.Sprintf("the preferred domain %q overlaps the blocked domain %q", preferred, blocked)))
			}
		}
	}
	return issues
}

func hasFatalIssues(issues []ReadinessIssue) bool {
	for _, issue := range issues {
		if issue.Severity == ReadinessFatal {
			return true
		}
	}
	return false
}

func dedupeIssues(issues []ReadinessIssue) []ReadinessIssue {
	seen := map[string]struct{}{}
	var out []ReadinessIssue
	for _, issue := range issues {
		key := issue.Code + "\x00" + issue.SuiteID + "\x00" + issue.ItemID + "\x00" + issue.EvaluatorType + "\x00" + issue.MetricID + "\x00" + issue.Message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, issue)
	}
	return out
}

func uniqueIssueCodes(issues []ReadinessIssue) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, issue := range issues {
		if _, ok := seen[issue.Code]; ok {
			continue
		}
		seen[issue.Code] = struct{}{}
		out = append(out, issue.Code)
	}
	sort.Strings(out)
	return out
}

func sortPrepared(prepared *PreparedEvaluation) {
	sort.SliceStable(prepared.BoundSuites, func(i, j int) bool {
		if prepared.BoundSuites[i].ID == prepared.BoundSuites[j].ID {
			return prepared.BoundSuites[i].Version < prepared.BoundSuites[j].Version
		}
		return prepared.BoundSuites[i].ID < prepared.BoundSuites[j].ID
	})
	sortResolvedEvaluators(prepared.EvaluatorDeclarations)
	sort.SliceStable(prepared.MetricPlans, func(i, j int) bool {
		if prepared.MetricPlans[i].SuiteID != prepared.MetricPlans[j].SuiteID {
			return prepared.MetricPlans[i].SuiteID < prepared.MetricPlans[j].SuiteID
		}
		if prepared.MetricPlans[i].SuiteVersion != prepared.MetricPlans[j].SuiteVersion {
			return prepared.MetricPlans[i].SuiteVersion < prepared.MetricPlans[j].SuiteVersion
		}
		if prepared.MetricPlans[i].DeclarationID == prepared.MetricPlans[j].DeclarationID {
			return prepared.MetricPlans[i].MetricID < prepared.MetricPlans[j].MetricID
		}
		return prepared.MetricPlans[i].DeclarationID < prepared.MetricPlans[j].DeclarationID
	})
	sort.SliceStable(prepared.EngineCapabilities, func(i, j int) bool {
		return prepared.EngineCapabilities[i].EngineID < prepared.EngineCapabilities[j].EngineID
	})
	sort.SliceStable(prepared.ResultPlans, func(i, j int) bool {
		return prepared.ResultPlans[i].WorkKey < prepared.ResultPlans[j].WorkKey
	})
	prepared.ReadinessIssues = dedupeIssues(prepared.ReadinessIssues)
	sort.SliceStable(prepared.ReadinessIssues, func(i, j int) bool {
		if prepared.ReadinessIssues[i].Code == prepared.ReadinessIssues[j].Code {
			return prepared.ReadinessIssues[i].ID < prepared.ReadinessIssues[j].ID
		}
		return prepared.ReadinessIssues[i].Code < prepared.ReadinessIssues[j].Code
	})
}

func sortResolvedEvaluators(values []ResolvedEvaluator) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].SuiteID != values[j].SuiteID {
			return values[i].SuiteID < values[j].SuiteID
		}
		if values[i].SuiteVersion != values[j].SuiteVersion {
			return values[i].SuiteVersion < values[j].SuiteVersion
		}
		if values[i].DeclarationID == values[j].DeclarationID {
			return values[i].EvaluatorType < values[j].EvaluatorType
		}
		return values[i].DeclarationID < values[j].DeclarationID
	})
}

func normalizedAliasKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return mention.Normalize(value)
}
