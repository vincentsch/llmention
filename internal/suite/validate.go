package suite

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/recognition"
)

var (
	suiteIDPattern                       = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	itemIDPattern                        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	variantIDPattern                     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	stackSlugPattern                     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	localePattern                        = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	recognitionVariantProhibitedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\btools?\b`),
		regexp.MustCompile(`\bbrows(?:e|ed|es|ing)\b`),
		regexp.MustCompile(`\bsearch(?:ed|es|ing)?\b`),
		regexp.MustCompile(`\blook\s+up\b`),
		regexp.MustCompile(`\blookup\b`),
		regexp.MustCompile(`\bverif(?:y|ied|ies|ying)\b`),
		regexp.MustCompile(`\bjson\b`),
		regexp.MustCompile(`\bschema\b`),
		regexp.MustCompile(`\boutput\b`),
		regexp.MustCompile(`\breturn\b`),
		regexp.MustCompile(`\brespond\b`),
		regexp.MustCompile(`\bresults?\b`),
		regexp.MustCompile(`\brank(?:ed|ing|s)?\b`),
		regexp.MustCompile(`\bconfidence\b`),
		regexp.MustCompile(`\bsnippets?\b`),
		regexp.MustCompile(`\btasks?\b`),
		regexp.MustCompile(`\binstructions?\b`),
		regexp.MustCompile(`\bignore\s+previous\b`),
	}
)

var knownSuiteTypes = map[string]struct{}{
	"visibility":  {},
	"recognition": {},
	"factoid":     {},
	"temporal":    {},
	"programming": {},
	"sources":     {},
	"censorship":  {},
	"propaganda":  {},
	"robustness":  {},
}

var knownContextModes = map[string]struct{}{
	ContextModeItemLocal:    {},
	ContextModeProjectBound: {},
}

var itemTypesBySuiteType = map[string]map[string]struct{}{
	"visibility": {
		"visibility.prompt":             {},
		"visibility.recommendation":     {},
		"visibility.alternatives":       {},
		"visibility.comparison":         {},
		"visibility.category_authority": {},
		"visibility.problem_solution":   {},
	},
	"recognition": {
		"recognition.entity": {},
	},
	"factoid": {
		"factoid.single_answer": {},
	},
	"temporal": {
		"temporal.post_cutoff_fact": {},
		"temporal.pre_cutoff_fact":  {},
		"temporal.stale_trap":       {},
		"temporal.recent_event":     {},
	},
	"programming": {
		"programming.language_fact":   {},
		"programming.version_fact":    {},
		"programming.api_existence":   {},
		"programming.deprecation":     {},
		"programming.error_diagnosis": {},
	},
	"sources": {
		"sources.owned_domain":      {},
		"sources.competitor_domain": {},
		"sources.preferred_domain":  {},
		"sources.blocked_domain":    {},
		"sources.source_freshness":  {},
	},
	"censorship": {
		"censorship.direct_event": {},
	},
	"propaganda": {
		"propaganda.false_premise": {},
	},
	"robustness": {
		"robustness.paraphrase_set": {},
	},
}

var knownEvaluatorTypes = map[string]struct{}{
	"mention_match":     {},
	"source_domain":     {},
	"exact_or_alias":    {},
	"structured_json":   {},
	"claim_coverage":    {},
	"claim_stance":      {},
	"entity_cluster":    {},
	"framing_sentiment": {},
	"stability":         {},
}

var knownMetricKinds = map[string]struct{}{
	"boolean":      {},
	"count":        {},
	"delta":        {},
	"distribution": {},
	"integer":      {},
	"number":       {},
	"rank":         {},
	"rate":         {},
	"score":        {},
	"string":       {},
}

var knownGradingTypes = map[string]struct{}{
	"claim_coverage":  {},
	"claim_stance":    {},
	"exact_or_alias":  {},
	"mention_match":   {},
	"source_domain":   {},
	"stability":       {},
	"structured_json": {},
}

var knownEntityTypes = map[string]struct{}{
	"person":       {},
	"company":      {},
	"product":      {},
	"project":      {},
	"library":      {},
	"concept":      {},
	"domain":       {},
	"organization": {},
	"unknown":      {},
}

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ValidationError struct {
	Issues []Issue `json:"issues"`
}

func (e ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "the suite is not valid"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Path == "" {
			parts = append(parts, issue.Message)
			continue
		}
		parts = append(parts, issue.Path+": "+issue.Message)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, "; ")
}

func (s *Suite) Validate() error {
	issues := ValidateSuite(s)
	if len(issues) > 0 {
		return ValidationError{Issues: issues}
	}
	return nil
}

// ValidateSuite checks a suite and returns every problem it finds.
func ValidateSuite(s *Suite) []Issue {
	var issues []Issue
	if s == nil {
		return []Issue{{Message: "suite is nil"}}
	}
	trimSuite(s)
	add := func(path, message string) {
		issues = append(issues, Issue{Path: path, Message: message})
	}

	if s.ID == "" {
		add("id", "is required")
	} else if !suiteIDPattern.MatchString(s.ID) {
		add("id", "must be lowercase parts joined by dots, such as research.factoid")
	}
	if s.Version <= 0 {
		add("version", "must be positive")
	}
	if s.Type == "" {
		add("type", "is required")
	} else if _, ok := knownSuiteTypes[s.Type]; !ok {
		add("type", fmt.Sprintf("%q is not a known suite type", s.Type))
	}
	if s.ContextMode == "" {
		s.ContextMode = ContextModeItemLocal
	}
	if _, ok := knownContextModes[s.ContextMode]; !ok {
		add("context_mode", fmt.Sprintf("%q is not a known context mode", s.ContextMode))
	}
	if len(s.DefaultModes) == 0 {
		add("default_modes", "must not be empty")
	}
	seenModes := map[string]struct{}{}
	for i, mode := range s.DefaultModes {
		path := fmt.Sprintf("default_modes[%d]", i)
		canonical, err := config.CanonicalMode(mode)
		if err != nil {
			add(path, err.Error())
			continue
		}
		mode = canonical
		s.DefaultModes[i] = mode
		if _, dup := seenModes[mode]; dup {
			add(path, fmt.Sprintf("lists the mode %q twice", mode))
		}
		seenModes[mode] = struct{}{}
	}
	if s.Repetitions <= 0 {
		add("repetitions", "must be positive")
	}
	issues = append(issues, validateVariantDeclarations("variants", s.Variants, false)...)
	if s.Type == "recognition" {
		issues = append(issues, validateRecognitionVariants("variants", s.Variants)...)
	}
	if isBenchmarkSuiteType(s.Type) {
		issues = append(issues, validateBenchmarkVariants("variants", s.Variants, "")...)
	}

	evaluatorIDs := map[string]struct{}{}
	for i, evaluator := range s.Evaluators {
		path := fmt.Sprintf("evaluators[%d]", i)
		if evaluator.ID == "" {
			add(path+".id", "is required")
		} else {
			if !itemIDPattern.MatchString(evaluator.ID) {
				add(path+".id", "may only use lowercase letters, digits, dots, dashes and underscores")
			}
			if _, dup := evaluatorIDs[evaluator.ID]; dup {
				add(path+".id", fmt.Sprintf("the id %q is used twice", evaluator.ID))
			}
			evaluatorIDs[evaluator.ID] = struct{}{}
		}
		if evaluator.Type == "" {
			add(path+".type", "is required")
		} else if _, ok := knownEvaluatorTypes[evaluator.Type]; !ok {
			add(path+".type", fmt.Sprintf("%q is not a known evaluator type", evaluator.Type))
		}
	}
	if len(s.Evaluators) == 0 {
		add("evaluators", "must not be empty")
	}
	if s.Type == "censorship" {
		if count := suiteEvaluatorTypeCount(s, "claim_coverage"); count != 1 {
			add("evaluators", "a censorship suite needs exactly one evaluator of type claim_coverage")
		}
	}
	if s.Type == "propaganda" {
		if count := suiteEvaluatorTypeCount(s, "claim_stance"); count != 1 {
			add("evaluators", "a propaganda suite needs exactly one evaluator of type claim_stance")
		}
	}

	metricIDs := map[string]struct{}{}
	for i, metric := range s.Metrics {
		path := fmt.Sprintf("metrics[%d]", i)
		if metric.ID == "" {
			add(path+".id", "is required")
		} else {
			if !itemIDPattern.MatchString(metric.ID) {
				add(path+".id", "may only use lowercase letters, digits, dots, dashes and underscores")
			}
			if _, dup := metricIDs[metric.ID]; dup {
				add(path+".id", fmt.Sprintf("the id %q is used twice", metric.ID))
			}
			metricIDs[metric.ID] = struct{}{}
		}
		if metric.Evaluator == "" {
			add(path+".evaluator", "is required")
		} else if _, ok := evaluatorIDs[metric.Evaluator]; !ok {
			add(path+".evaluator", fmt.Sprintf("names the evaluator %q, which is not listed under evaluators", metric.Evaluator))
		}
		if metric.Kind == "" {
			add(path+".kind", "is required")
		} else if _, ok := knownMetricKinds[metric.Kind]; !ok {
			add(path+".kind", fmt.Sprintf("%q is not a known metric kind", metric.Kind))
		}
	}
	if len(s.Metrics) == 0 {
		add("metrics", "must not be empty")
	}
	if s.Type == "recognition" {
		issues = append(issues, validateRecognitionSuite(s)...)
	}
	if isBenchmarkSuiteType(s.Type) {
		issues = append(issues, validateBenchmarkSuite(s)...)
	}

	// A project-bound recognition suite gets its items from the brand in
	// llmention.yaml, so it may ship without any.
	if len(s.Items) == 0 && !(s.Type == "recognition" && s.ContextMode == ContextModeProjectBound) {
		add("items", "must not be empty")
	}
	itemIDs := map[string]struct{}{}
	for i := range s.Items {
		item := &s.Items[i]
		path := fmt.Sprintf("items[%d]", i)
		issues = append(issues, validateItem(s, item, path, evaluatorIDs, metricIDs)...)
		if item.ID == "" {
			continue
		}
		if _, dup := itemIDs[item.ID]; dup {
			add(path+".id", fmt.Sprintf("the id %q is used twice", item.ID))
		}
		itemIDs[item.ID] = struct{}{}
	}
	issues = append(issues, validateComparisonGroups(s)...)

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Path == issues[j].Path {
			return issues[i].Message < issues[j].Message
		}
		return issues[i].Path < issues[j].Path
	})
	return issues
}

func validateItem(s *Suite, item *Item, path string, evaluatorIDs, metricIDs map[string]struct{}) []Issue {
	var issues []Issue
	add := func(sub, message string) {
		if sub == "" {
			issues = append(issues, Issue{Path: path, Message: message})
			return
		}
		issues = append(issues, Issue{Path: path + "." + sub, Message: message})
	}

	trimItem(item)
	if item.ID == "" {
		add("id", "is required")
	} else if !itemIDPattern.MatchString(item.ID) {
		add("id", "may only use lowercase letters, digits, dots, dashes and underscores")
	}
	if item.Type == "" && s.Type == "visibility" {
		item.Type = "visibility.prompt"
	}
	if item.Type == "" {
		add("type", "is required")
	} else if allowed, ok := itemTypesBySuiteType[s.Type]; ok {
		if _, ok := allowed[item.Type]; !ok {
			add("type", fmt.Sprintf("%q is not valid for suite type %q", item.Type, s.Type))
		}
	}
	if item.Locale != "" && !localePattern.MatchString(item.Locale) {
		add("locale", fmt.Sprintf("%q is not a locale such as en-US", item.Locale))
	}
	if normalized, err := dimension.NormalizeMap(item.Dimensions); err != nil {
		add("dimensions", err.Error())
	} else {
		item.Dimensions = normalized
	}
	if item.Locale != "" && item.Dimensions[dimension.Locale] != "" {
		if normalized, err := dimension.CanonicalLocale(item.Locale); err == nil && normalized != item.Dimensions[dimension.Locale] {
			add("locale", fmt.Sprintf("must match dimensions.locale %q", item.Dimensions[dimension.Locale]))
		}
	}
	if groupID, err := dimension.NormalizeComparisonGroupID(item.ComparisonGroupID); err != nil {
		add("comparison_group_id", err.Error())
	} else {
		item.ComparisonGroupID = groupID
	}
	if item.evaluatorIDsSet && len(item.EvaluatorIDs) == 0 {
		add("evaluator_ids", "must not be empty when set")
	}
	if item.metricIDsSet && len(item.MetricIDs) == 0 {
		add("metric_ids", "must not be empty when set")
	}
	for i, id := range item.EvaluatorIDs {
		if _, ok := evaluatorIDs[id]; !ok {
			add(fmt.Sprintf("evaluator_ids[%d]", i), fmt.Sprintf("names the evaluator %q, which is not listed under evaluators", id))
		}
	}
	for i, id := range item.MetricIDs {
		if _, ok := metricIDs[id]; !ok {
			add(fmt.Sprintf("metric_ids[%d]", i), fmt.Sprintf("names the metric %q, which is not listed under metrics", id))
		}
	}
	for i, tag := range item.Tags {
		if tag == "" {
			add(fmt.Sprintf("tags[%d]", i), "must not be empty")
		}
	}

	switch s.Type {
	case "visibility":
		issues = append(issues, validateVisibilityItem(item, path)...)
	case "recognition":
		issues = append(issues, validateRecognitionItem(item, path)...)
	case "factoid":
		issues = append(issues, validateFactoidLikeItem(item, path, false, "exact_or_alias")...)
	case "temporal":
		issues = append(issues, validateTemporalItem(item, path)...)
	case "programming":
		issues = append(issues, validateProgrammingItem(item, path)...)
	case "sources":
		issues = append(issues, validateSourceItem(item, path)...)
	case "censorship":
		issues = append(issues, validateCensorshipItem(s, item, path)...)
	case "propaganda":
		issues = append(issues, validatePropagandaItem(s, item, path)...)
	case "robustness":
		issues = append(issues, validateRobustnessItem(item, path)...)
	}
	issues = append(issues, validateVariantDeclarations(path+".variants", item.Variants, s.Type == "robustness")...)
	if s.Type == "recognition" {
		issues = append(issues, validateRecognitionVariants(path+".variants", item.Variants)...)
	}
	if isBenchmarkSuiteType(s.Type) {
		benchmarkVariants := item.Variants
		if len(benchmarkVariants) == 0 {
			benchmarkVariants = s.Variants
		}
		issues = append(issues, validateBenchmarkVariants(path+".variants", benchmarkVariants, item.Question)...)
	}
	issues = append(issues, validateLegacyVariantDimensions(item, path)...)
	renderItem := *item
	if s.ContextMode == ContextModeProjectBound {
		fillProjectBoundValidationContext(&renderItem)
	}
	if rendered, err := RenderVariants(s, renderItem); err != nil {
		add("variants", err.Error())
	} else if isAuditSuiteType(s.Type) {
		issues = append(issues, validateAuditDeltaVariantSelections(s, item, path, rendered)...)
	}
	return issues
}

func validateLegacyVariantDimensions(item *Item, path string) []Issue {
	if len(item.Dimensions) == 0 || len(item.Variants) == 0 {
		return nil
	}
	var issues []Issue
	for i, variant := range item.Variants {
		if variant.Locale != "" && item.Dimensions[dimension.Locale] != "" {
			if normalized, err := dimension.CanonicalLocale(variant.Locale); err == nil && normalized != item.Dimensions[dimension.Locale] {
				issues = append(issues, Issue{
					Path:    fmt.Sprintf("%s.variants[%d].locale", path, i),
					Message: fmt.Sprintf("must match dimensions.locale %q", item.Dimensions[dimension.Locale]),
				})
			}
		}
		if variant.Persona != "" && item.Dimensions[dimension.Persona] != "" {
			if normalized, err := dimension.NormalizeValue(dimension.Persona, variant.Persona); err == nil && normalized != item.Dimensions[dimension.Persona] {
				issues = append(issues, Issue{
					Path:    fmt.Sprintf("%s.variants[%d].persona", path, i),
					Message: fmt.Sprintf("must match dimensions.persona %q", item.Dimensions[dimension.Persona]),
				})
			}
		}
	}
	return issues
}

func validateComparisonGroups(s *Suite) []Issue {
	if s == nil {
		return nil
	}
	seen := map[string]string{}
	var issues []Issue
	for i, item := range s.Items {
		if item.ComparisonGroupID == "" {
			continue
		}
		variantIDs := effectiveVariantIDs(s, item)
		for _, variantID := range variantIDs {
			if variantID == "" {
				continue
			}
			key := item.ComparisonGroupID + "\x00" + dimension.TupleKey(item.Dimensions) + "\x00" + variantID
			if previous, dup := seen[key]; dup {
				issues = append(issues, Issue{
					Path:    fmt.Sprintf("items[%d].comparison_group_id", i),
					Message: fmt.Sprintf("item %q already uses this comparison group with the same dimensions and variant", previous),
				})
				continue
			}
			seen[key] = item.ID
		}
	}
	return issues
}

func effectiveVariantIDs(s *Suite, item Item) []string {
	var variants []Variant
	switch {
	case len(item.Variants) > 0:
		variants = item.Variants
	case s != nil && len(s.Variants) > 0:
		variants = s.Variants
	default:
		return []string{"default"}
	}
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		out = append(out, strings.TrimSpace(variant.ID))
	}
	return out
}

func validateAuditDeltaVariantSelections(s *Suite, item *Item, path string, rendered []RenderedVariant) []Issue {
	if s == nil || item == nil {
		return nil
	}
	selectedMetrics := item.MetricIDs
	if !item.metricIDsSet {
		selectedMetrics = nil
		for _, metric := range s.Metrics {
			selectedMetrics = append(selectedMetrics, metric.ID)
		}
	}
	variantIDs := map[string]struct{}{}
	for _, variant := range rendered {
		variantIDs[variant.VariantID] = struct{}{}
	}
	hasVariant := func(id string) bool {
		_, ok := variantIDs[id]
		return ok
	}
	var issues []Issue
	addMissing := func(metricID, left, right string) {
		if !slices.Contains(selectedMetrics, metricID) {
			return
		}
		if !hasVariant(left) || !hasVariant(right) {
			issues = append(issues, Issue{
				Path:    path + ".variants",
				Message: fmt.Sprintf("metric %q needs the variants %q and %q", metricID, left, right),
			})
		}
	}
	switch s.Type {
	case "censorship":
		addMissing("censorship.direct_indirect_delta", "direct", "indirect")
	case "propaganda":
		addMissing("propaganda.prompt_framing_sensitivity", "direct", "attributed")
	}
	return issues
}

func validateVisibilityItem(item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Prompt == "" {
		add("prompt", "is required")
	}
	if item.Brand != nil {
		issues = append(issues, validateEntity(path+".brand", item.Brand, false)...)
	}
	for i := range item.Competitors {
		issues = append(issues, validateEntity(fmt.Sprintf("%s.competitors[%d]", path, i), &item.Competitors[i], false)...)
	}
	normalizeDomains(&issues, path+".owned_domains", &item.OwnedDomains)
	return issues
}

func validateRecognitionItem(item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Query == "" {
		add("query", "is required")
	}
	if item.Privacy != "local_only" {
		add("privacy", "must be local_only")
	}
	if len(item.ExpectedEntities) == 0 {
		add("expected_entities", "must not be empty")
	}
	for i := range item.ExpectedEntities {
		issues = append(issues, validateExpectedEntity(fmt.Sprintf("%s.expected_entities[%d]", path, i), &item.ExpectedEntities[i])...)
	}
	for i := range item.KnownWrongNamesakes {
		issues = append(issues, validateExpectedEntity(fmt.Sprintf("%s.known_wrong_namesakes[%d]", path, i), &item.KnownWrongNamesakes[i])...)
	}
	issues = append(issues, validateRecognitionConfiguredEntities(item, path)...)
	return issues
}

func validateRecognitionSuite(s *Suite) []Issue {
	var issues []Issue
	count := 0
	for i, evaluator := range s.Evaluators {
		if evaluator.Type == recognition.EvaluatorTypeEntityCluster {
			count++
			if strings.TrimSpace(evaluator.ID) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("evaluators[%d].id", i), Message: "is required"})
			}
		}
	}
	if count != 1 {
		issues = append(issues, Issue{Path: "evaluators", Message: "a recognition suite needs exactly one evaluator of type entity_cluster"})
	}
	return issues
}

func validateRecognitionConfiguredEntities(item *Item, path string) []Issue {
	var issues []Issue
	seen := map[string]string{}
	check := func(kind string, index int, entity ExpectedEntity) {
		base := fmt.Sprintf("%s.%s[%d]", path, kind, index)
		normalizedName, normalizedType := recognition.NormalizeConfiguredIdentity(entity.CanonicalName, entity.EntityType)
		if normalizedName == "" {
			issues = append(issues, Issue{Path: base + ".canonical_name", Message: "is empty"})
		}
		if normalizedType == recognition.EntityTypeUnknown {
			issues = append(issues, Issue{Path: base + ".entity_type", Message: "must not be unknown; pick a kind such as company or product"})
		}
		if normalizedType != "" && !recognition.ConfigurableEntityType(normalizedType) {
			issues = append(issues, Issue{Path: base + ".entity_type", Message: fmt.Sprintf("%q is not a kind you can use here", entity.EntityType)})
		}
		identity := normalizedName + "\x00" + normalizedType
		if normalizedName != "" && normalizedType != "" {
			if previous, dup := seen[identity]; dup {
				issues = append(issues, Issue{Path: base, Message: fmt.Sprintf("names the same thing as %s", previous)})
			} else {
				seen[identity] = base
			}
		}
		for i, alias := range entity.Aliases {
			if recognition.NormalizeForMatch(alias) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("%s.aliases[%d]", base, i), Message: "is empty"})
			}
		}
		for i, descriptor := range entity.DescriptorsAny {
			if recognition.NormalizeForMatch(descriptor) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("%s.descriptors_any[%d]", base, i), Message: "is empty"})
			}
		}
	}
	for i, entity := range item.ExpectedEntities {
		check("expected_entities", i, entity)
	}
	for i, entity := range item.KnownWrongNamesakes {
		check("known_wrong_namesakes", i, entity)
	}
	return issues
}

func isBenchmarkSuiteType(value string) bool {
	switch value {
	case "factoid", "programming", "temporal":
		return true
	default:
		return false
	}
}

func validateBenchmarkSuite(s *Suite) []Issue {
	var issues []Issue
	count := 0
	for i, evaluator := range s.Evaluators {
		if evaluator.Type == benchmark.EvaluatorTypeExactOrAlias {
			count++
			if strings.TrimSpace(evaluator.ID) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("evaluators[%d].id", i), Message: "is required"})
			}
		}
	}
	if count != 1 {
		issues = append(issues, Issue{Path: "evaluators", Message: "this suite type needs exactly one evaluator of type exact_or_alias"})
	}
	return issues
}

func validateBenchmarkVariants(path string, variants []Variant, question string) []Issue {
	var issues []Issue
	normalizedQuestion := normalizeBenchmarkQuestionReference(question)
	for i, variant := range variants {
		source := strings.ToLower(strings.TrimSpace(variant.Prompt + "\n" + variant.Template))
		if source == "" {
			continue
		}
		if containsRecognitionVariantProhibition(source) {
			issues = append(issues, Issue{
				Path:    fmt.Sprintf("%s[%d].prompt", path, i),
				Message: "a variant may only change how the question is framed; it must not mention tools, search or the answer format",
			})
		}
		if strings.Contains(source, ".question") || (normalizedQuestion != "" && strings.Contains(normalizeBenchmarkQuestionReference(source), normalizedQuestion)) {
			issues = append(issues, Issue{
				Path:    fmt.Sprintf("%s[%d].prompt", path, i),
				Message: "a variant must not repeat the item's question; llmention adds the question itself",
			})
		}
	}
	return issues
}

func normalizeBenchmarkQuestionReference(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func validateFactoidLikeItem(item *Item, path string, eventDateRequired bool, allowedGradingTypes ...string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Question == "" {
		add("question", "is required")
	}
	if eventDateRequired {
		if item.EventDate == "" {
			add("event_date", "is required")
		} else if _, err := time.Parse("2006-01-02", item.EventDate); err != nil {
			add("event_date", "must use YYYY-MM-DD")
		}
	}
	issues = append(issues, validateAnswers(path+".answers", item.Answers)...)
	issues = append(issues, validateGrading(path+".grading", item.Grading, allowedGradingTypes...)...)
	issues = append(issues, validateKnownWrongAnswers(path+".known_wrong_answers", item.KnownWrongAnswers)...)
	for i, source := range item.Sources {
		issues = append(issues, validateReferenceSource(fmt.Sprintf("%s.sources[%d]", path, i), source)...)
	}
	issues = append(issues, validateBenchmarkAnswerCollisions(item, path)...)
	if len(item.Tags) == 0 {
		add("tags", "must not be empty")
	}
	return issues
}

func validateTemporalItem(item *Item, path string) []Issue {
	issues := validateFactoidLikeItem(item, path, true, "exact_or_alias")
	add := issueAppender(&issues, path)
	for mode, behavior := range item.ExpectedModeBehavior {
		if canonical, err := config.CanonicalMode(mode); err != nil || canonical != mode {
			add("expected_mode_behavior."+mode, "uses an unknown mode")
		}
		if _, ok := temporalExpectedBehaviors[strings.TrimSpace(behavior)]; !ok {
			add("expected_mode_behavior."+mode, fmt.Sprintf("%q is not a known temporal behavior", behavior))
		}
		if behavior == benchmark.ExpectedAbstainOrUnknown {
			if mode != "memory" {
				add("expected_mode_behavior."+mode, "abstain_or_unknown only works for the memory mode")
			}
			if item.Grading == nil || !item.Grading.AbstainAllowed {
				add("expected_mode_behavior."+mode, "abstain_or_unknown needs grading.abstain_allowed: true")
			}
		}
		if behavior == benchmark.ExpectedCorrectWithSource {
			if !searchModeAllowed(mode) {
				add("expected_mode_behavior."+mode, "correct_with_source only works for search modes")
			}
			if item.SourceExpectation == nil || !slices.Contains(item.SourceExpectation.RequiredForModes, mode) {
				add("expected_mode_behavior."+mode, "correct_with_source must be listed in source_expectation.required_for_modes")
			}
		}
	}
	if item.Type == "temporal.stale_trap" && len(item.StaleAnswers) == 0 {
		add("stale_answers", "must not be empty for temporal.stale_trap")
	}
	issues = append(issues, validateStaleAnswers(path+".stale_answers", item.StaleAnswers)...)
	if item.SourceExpectation != nil {
		issues = append(issues, validateSourceExpectation(path+".source_expectation", item.SourceExpectation)...)
		for i, mode := range item.SourceExpectation.RequiredForModes {
			if item.ExpectedModeBehavior[mode] != benchmark.ExpectedCorrectWithSource {
				issues = append(issues, Issue{
					Path:    fmt.Sprintf("%s.source_expectation.required_for_modes[%d]", path, i),
					Message: "mode must have expected_mode_behavior correct_with_source",
				})
			}
		}
	}
	return issues
}

var temporalExpectedBehaviors = map[string]struct{}{
	benchmark.ExpectedShouldKnow:        {},
	benchmark.ExpectedAbstainOrUnknown:  {},
	benchmark.ExpectedCorrectWithSource: {},
	benchmark.ExpectedNotRequired:       {},
}

func validateProgrammingItem(item *Item, path string) []Issue {
	issues := validateFactoidLikeItem(item, path, false, "exact_or_alias")
	add := issueAppender(&issues, path)
	if item.Stack == nil {
		add("stack.language", "is required")
	} else {
		normalizeStackField(&item.Stack.Language)
		normalizeStackField(&item.Stack.Ecosystem)
		normalizeStackField(&item.Stack.Library)
		normalizeStackField(&item.Stack.API)
		if item.Stack.Language == "" {
			add("stack.language", "is required")
		} else if !stackSlugPattern.MatchString(item.Stack.Language) {
			add("stack.language", "may only use lowercase letters, digits, dots, dashes and underscores, up to 64 characters")
		}
		for _, field := range []struct {
			path  string
			value string
		}{
			{"stack.ecosystem", item.Stack.Ecosystem},
			{"stack.library", item.Stack.Library},
			{"stack.api", item.Stack.API},
		} {
			if field.value != "" && !stackSlugPattern.MatchString(field.value) {
				add(field.path, "may only use lowercase letters, digits, dots, dashes and underscores, up to 64 characters")
			}
		}
	}
	issues = append(issues, validateFailureCases(path+".failure_cases", item.FailureCases)...)
	return issues
}

func validateKnownWrongAnswers(path string, rows []KnownWrongAnswer) []Issue {
	var issues []Issue
	seenIDs := map[string]struct{}{}
	for i := range rows {
		row := &rows[i]
		row.ID = strings.TrimSpace(row.ID)
		trimStringSlice(&row.Aliases)
		rowPath := fmt.Sprintf("%s[%d]", path, i)
		if row.ID == "" {
			issues = append(issues, Issue{Path: rowPath + ".id", Message: "is required"})
		} else {
			if !itemIDPattern.MatchString(row.ID) {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: "may only use lowercase letters, digits, dots, dashes and underscores"})
			}
			if _, dup := seenIDs[row.ID]; dup {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: fmt.Sprintf("the id %q is used twice", row.ID)})
			}
			seenIDs[row.ID] = struct{}{}
		}
		if len(row.Aliases) == 0 {
			issues = append(issues, Issue{Path: rowPath + ".aliases", Message: "must not be empty"})
		}
		for j, alias := range row.Aliases {
			if benchmark.NormalizeForMatch(alias) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("%s.aliases[%d]", rowPath, j), Message: "is empty"})
			}
		}
	}
	return issues
}

func validateFailureCases(path string, rows []FailureCase) []Issue {
	var issues []Issue
	seenIDs := map[string]struct{}{}
	for i := range rows {
		row := &rows[i]
		row.ID = strings.TrimSpace(row.ID)
		row.Class = strings.TrimSpace(row.Class)
		trimStringSlice(&row.Aliases)
		rowPath := fmt.Sprintf("%s[%d]", path, i)
		if row.ID == "" {
			issues = append(issues, Issue{Path: rowPath + ".id", Message: "is required"})
		} else {
			if !itemIDPattern.MatchString(row.ID) {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: "may only use lowercase letters, digits, dots, dashes and underscores"})
			}
			if _, dup := seenIDs[row.ID]; dup {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: fmt.Sprintf("the id %q is used twice", row.ID)})
			}
			seenIDs[row.ID] = struct{}{}
		}
		if _, ok := programmingFailureClasses[row.Class]; !ok {
			issues = append(issues, Issue{Path: rowPath + ".class", Message: fmt.Sprintf("%q is not a known failure class", row.Class)})
		}
		if len(row.Aliases) == 0 {
			issues = append(issues, Issue{Path: rowPath + ".aliases", Message: "must not be empty"})
		}
		for j, alias := range row.Aliases {
			if benchmark.NormalizeForMatch(alias) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("%s.aliases[%d]", rowPath, j), Message: "is empty"})
			}
		}
	}
	return issues
}

var programmingFailureClasses = map[string]struct{}{
	benchmark.FailureClassWrongVersion:        {},
	benchmark.FailureClassHallucinatedAPI:     {},
	benchmark.FailureClassDeprecatedAsCurrent: {},
	benchmark.FailureClassOutdatedAnswer:      {},
	benchmark.FailureClassWrongLibrary:        {},
	benchmark.FailureClassWrongErrorCause:     {},
	benchmark.FailureClassOtherIncorrect:      {},
}

func validateStaleAnswers(path string, rows []StaleAnswer) []Issue {
	var issues []Issue
	seenIDs := map[string]struct{}{}
	for i := range rows {
		row := &rows[i]
		row.ID = strings.TrimSpace(row.ID)
		row.StaleAsOf = strings.TrimSpace(row.StaleAsOf)
		trimStringSlice(&row.Aliases)
		rowPath := fmt.Sprintf("%s[%d]", path, i)
		if row.ID == "" {
			issues = append(issues, Issue{Path: rowPath + ".id", Message: "is required"})
		} else {
			if !itemIDPattern.MatchString(row.ID) {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: "may only use lowercase letters, digits, dots, dashes and underscores"})
			}
			if _, dup := seenIDs[row.ID]; dup {
				issues = append(issues, Issue{Path: rowPath + ".id", Message: fmt.Sprintf("the id %q is used twice", row.ID)})
			}
			seenIDs[row.ID] = struct{}{}
		}
		if len(row.Aliases) == 0 {
			issues = append(issues, Issue{Path: rowPath + ".aliases", Message: "must not be empty"})
		}
		if row.StaleAsOf == "" {
			issues = append(issues, Issue{Path: rowPath + ".stale_as_of", Message: "is required"})
		} else if _, err := time.Parse("2006-01-02", row.StaleAsOf); err != nil {
			issues = append(issues, Issue{Path: rowPath + ".stale_as_of", Message: "must use YYYY-MM-DD"})
		}
		for j, alias := range row.Aliases {
			if benchmark.NormalizeForMatch(alias) == "" {
				issues = append(issues, Issue{Path: fmt.Sprintf("%s.aliases[%d]", rowPath, j), Message: "is empty"})
			}
		}
	}
	return issues
}

func validateSourceExpectation(path string, expectation *SourceExpectation) []Issue {
	var issues []Issue
	seenModes := map[string]struct{}{}
	for i, mode := range expectation.RequiredForModes {
		mode = strings.TrimSpace(mode)
		expectation.RequiredForModes[i] = mode
		if !searchModeAllowed(mode) {
			issues = append(issues, Issue{Path: fmt.Sprintf("%s.required_for_modes[%d]", path, i), Message: "must be a search mode"})
		}
		if _, dup := seenModes[mode]; dup {
			issues = append(issues, Issue{Path: fmt.Sprintf("%s.required_for_modes[%d]", path, i), Message: fmt.Sprintf("lists the mode %q twice", mode)})
		}
		seenModes[mode] = struct{}{}
	}
	expectation.FreshnessReferenceDate = strings.TrimSpace(expectation.FreshnessReferenceDate)
	if expectation.FreshnessReferenceDate != "" {
		if _, err := time.Parse("2006-01-02", expectation.FreshnessReferenceDate); err != nil {
			issues = append(issues, Issue{Path: path + ".freshness_reference_date", Message: "must use YYYY-MM-DD"})
		}
	}
	return issues
}

func validateBenchmarkAnswerCollisions(item *Item, path string) []Issue {
	var issues []Issue
	seen := map[string]string{}
	addValue := func(ownerPath, label, value string) {
		normalized := benchmark.NormalizeForMatch(value)
		if normalized == "" {
			return
		}
		if previous, dup := seen[normalized]; dup && previous != label {
			issues = append(issues, Issue{Path: ownerPath, Message: fmt.Sprintf("matches %s once case and spacing are ignored", previous)})
			return
		}
		seen[normalized] = label
	}
	if item.Answers != nil {
		addValue(path+".answers.canonical", "answers.canonical", item.Answers.Canonical)
		for i, alias := range item.Answers.Aliases {
			addValue(fmt.Sprintf("%s.answers.aliases[%d]", path, i), "answers.aliases", alias)
		}
	}
	for id, phrase := range benchmark.AbstentionCatalogNormalized() {
		addValue(path+".grading", "abstention."+id, phrase)
	}
	for i, row := range item.KnownWrongAnswers {
		for j, alias := range row.Aliases {
			addValue(fmt.Sprintf("%s.known_wrong_answers[%d].aliases[%d]", path, i, j), "known_wrong_answers."+row.ID, alias)
		}
	}
	for i, row := range item.FailureCases {
		for j, alias := range row.Aliases {
			addValue(fmt.Sprintf("%s.failure_cases[%d].aliases[%d]", path, i, j), "failure_cases."+row.ID, alias)
		}
	}
	for i, row := range item.StaleAnswers {
		for j, alias := range row.Aliases {
			addValue(fmt.Sprintf("%s.stale_answers[%d].aliases[%d]", path, i, j), "stale_answers."+row.ID, alias)
		}
	}
	return issues
}

func searchModeAllowed(mode string) bool {
	switch mode {
	case "search_auto", "search_required", "search_cache_only":
		return true
	default:
		return false
	}
}

func normalizeStackField(value *string) {
	*value = strings.ToLower(strings.TrimSpace(*value))
}

func validateSourceItem(item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Prompt == "" {
		add("prompt", "is required")
	}
	normalizeDomains(&issues, path+".owned_domains", &item.OwnedDomains)
	normalizeDomains(&issues, path+".competitor_domains", &item.CompetitorDomains)
	normalizeDomains(&issues, path+".preferred_domains", &item.PreferredDomains)
	normalizeDomains(&issues, path+".blocked_domains", &item.BlockedDomains)
	return issues
}

func validateCensorshipItem(s *Suite, item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Question == "" {
		add("question", "is required")
	}
	if item.TargetClaim != nil {
		add("target_claim", "is not valid for censorship items")
	}
	if item.ExpectedStance != "" {
		add("expected_stance", "is not valid for censorship items")
	}
	if len(item.ExpectedCorrections) > 0 {
		add("expected_corrections", "is not valid for censorship items")
	}
	if len(item.ForbiddenClaims) > 0 {
		add("forbidden_claims", "is not valid for censorship items")
	}
	if item.SourcePolicy != nil {
		add("source_policy", "is not valid for censorship items")
	}
	if len(item.ExpectedClaims) == 0 {
		add("expected_claims", "must not be empty")
	}
	if len(item.ForbiddenFailures) == 0 {
		add("forbidden_failures", "must not be empty")
	}
	if len(item.Sources) == 0 {
		add("sources", "must not be empty")
	}
	if len(item.Tags) == 0 {
		add("tags", "must not be empty")
	}
	for i := range item.ExpectedClaims {
		issues = append(issues, validateClaim(fmt.Sprintf("%s.expected_claims[%d]", path, i), &item.ExpectedClaims[i])...)
	}
	for i := range item.ForbiddenFailures {
		issues = append(issues, validateClaim(fmt.Sprintf("%s.forbidden_failures[%d]", path, i), &item.ForbiddenFailures[i])...)
	}
	for i, source := range item.Sources {
		issues = append(issues, validateReferenceSource(fmt.Sprintf("%s.sources[%d]", path, i), source)...)
	}
	issues = append(issues, validateUniqueAuditClaimIDs(path, []claimSet{
		{field: "expected_claims", claims: item.ExpectedClaims},
		{field: "forbidden_failures", claims: item.ForbiddenFailures},
	})...)
	if count := effectiveEvaluatorTypeCount(s, item, "claim_coverage"); count != 1 {
		add("evaluator_ids", "must name exactly one evaluator of type claim_coverage")
	}
	issues = append(issues, validateGrading(path+".grading", item.Grading, "claim_coverage")...)
	return issues
}

func validatePropagandaItem(s *Suite, item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.Prompt == "" {
		add("prompt", "is required")
	}
	if item.Question != "" {
		add("question", "is not valid for propaganda items")
	}
	if len(item.ExpectedClaims) > 0 {
		add("expected_claims", "is not valid for propaganda items")
	}
	if len(item.ForbiddenFailures) > 0 {
		add("forbidden_failures", "is not valid for propaganda items")
	}
	if item.TargetClaim == nil {
		add("target_claim", "is required")
	} else {
		issues = append(issues, validateClaim(path+".target_claim", item.TargetClaim)...)
	}
	if item.ExpectedStance != "reject_or_correct" {
		add("expected_stance", "must be reject_or_correct")
	}
	if len(item.ExpectedCorrections) == 0 {
		add("expected_corrections", "must not be empty")
	}
	if len(item.ForbiddenClaims) == 0 {
		add("forbidden_claims", "must not be empty")
	}
	if len(item.Sources) == 0 {
		add("sources", "must not be empty")
	}
	if item.SourcePolicy == nil {
		add("source_policy", "is required")
	}
	if len(item.Tags) == 0 {
		add("tags", "must not be empty")
	}
	for i := range item.ExpectedCorrections {
		issues = append(issues, validateClaim(fmt.Sprintf("%s.expected_corrections[%d]", path, i), &item.ExpectedCorrections[i])...)
	}
	for i := range item.ForbiddenClaims {
		issues = append(issues, validateClaim(fmt.Sprintf("%s.forbidden_claims[%d]", path, i), &item.ForbiddenClaims[i])...)
	}
	for i, source := range item.Sources {
		issues = append(issues, validateReferenceSource(fmt.Sprintf("%s.sources[%d]", path, i), source)...)
	}
	if item.SourcePolicy != nil {
		normalizeDomains(&issues, path+".source_policy.low_quality_domains", &item.SourcePolicy.LowQualityDomains)
	}
	claimSets := []claimSet{
		{field: "expected_corrections", claims: item.ExpectedCorrections},
		{field: "forbidden_claims", claims: item.ForbiddenClaims},
	}
	if item.TargetClaim != nil {
		claimSets = append(claimSets, claimSet{field: "target_claim", claims: []Claim{*item.TargetClaim}})
	}
	issues = append(issues, validateUniqueAuditClaimIDs(path, claimSets)...)
	if count := effectiveEvaluatorTypeCount(s, item, "claim_stance"); count != 1 {
		add("evaluator_ids", "must name exactly one evaluator of type claim_stance")
	}
	issues = append(issues, validateGrading(path+".grading", item.Grading, "claim_stance")...)
	return issues
}

type claimSet struct {
	field  string
	claims []Claim
}

func validateUniqueAuditClaimIDs(path string, sets []claimSet) []Issue {
	seen := map[string]string{}
	var issues []Issue
	for _, set := range sets {
		for i, claim := range set.claims {
			id := strings.TrimSpace(claim.ID)
			if id == "" {
				continue
			}
			claimPath := path + "." + set.field
			if set.field != "target_claim" {
				claimPath = fmt.Sprintf("%s.%s[%d]", path, set.field, i)
			}
			if previous, dup := seen[id]; dup {
				issues = append(issues, Issue{
					Path:    claimPath + ".id",
					Message: fmt.Sprintf("the claim id %q is already used at %s", id, previous),
				})
				continue
			}
			seen[id] = claimPath
		}
	}
	return issues
}

func effectiveEvaluatorTypeCount(s *Suite, item *Item, evaluatorType string) int {
	if s == nil || item == nil {
		return 0
	}
	typesByID := map[string]string{}
	for _, evaluator := range s.Evaluators {
		typesByID[evaluator.ID] = evaluator.Type
	}
	ids := item.EvaluatorIDs
	if !item.evaluatorIDsSet {
		ids = nil
		for _, evaluator := range s.Evaluators {
			ids = append(ids, evaluator.ID)
		}
	}
	count := 0
	for _, id := range ids {
		if typesByID[id] == evaluatorType {
			count++
		}
	}
	return count
}

func suiteEvaluatorTypeCount(s *Suite, evaluatorType string) int {
	if s == nil {
		return 0
	}
	count := 0
	for _, evaluator := range s.Evaluators {
		if evaluator.Type == evaluatorType {
			count++
		}
	}
	return count
}

func validateRobustnessItem(item *Item, path string) []Issue {
	var issues []Issue
	add := issueAppender(&issues, path)
	if item.BaseItem == "" {
		add("base_item", "is required")
	}
	if len(item.Variants) < 2 {
		add("variants", "needs at least two variants")
	}
	return issues
}

func validateVariantDeclarations(path string, variants []Variant, promptRequired bool) []Issue {
	var issues []Issue
	seen := map[string]struct{}{}
	for i, variant := range variants {
		vpath := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(variant.ID) == "" {
			issues = append(issues, Issue{Path: vpath + ".id", Message: "is required"})
		} else {
			id := strings.TrimSpace(variant.ID)
			if !variantIDPattern.MatchString(id) {
				issues = append(issues, Issue{Path: vpath + ".id", Message: "may only use lowercase letters, digits, dashes and underscores"})
			}
			if _, dup := seen[id]; dup {
				issues = append(issues, Issue{Path: vpath + ".id", Message: fmt.Sprintf("the id %q is used twice", id)})
			}
			seen[id] = struct{}{}
		}
		if variant.Locale != "" && !localePattern.MatchString(strings.TrimSpace(variant.Locale)) {
			issues = append(issues, Issue{Path: vpath + ".locale", Message: fmt.Sprintf("%q is not a locale such as en-US", variant.Locale)})
		}
		if promptRequired && strings.TrimSpace(variant.Prompt) == "" && strings.TrimSpace(variant.Template) == "" {
			issues = append(issues, Issue{Path: vpath + ".prompt", Message: "is required"})
		}
	}
	return issues
}

func validateRecognitionVariants(path string, variants []Variant) []Issue {
	var issues []Issue
	for i, variant := range variants {
		source := strings.ToLower(strings.TrimSpace(variant.Prompt + "\n" + variant.Template))
		if source == "" {
			continue
		}
		if containsRecognitionVariantProhibition(source) {
			issues = append(issues, Issue{
				Path:    fmt.Sprintf("%s[%d].prompt", path, i),
				Message: "a variant may only change how the question is framed; it must not mention tools, search or the answer format",
			})
		}
	}
	return issues
}

func containsRecognitionVariantProhibition(source string) bool {
	for _, pattern := range recognitionVariantProhibitedPatterns {
		if pattern.MatchString(source) {
			return true
		}
	}
	return false
}

func validateEntity(path string, entity *Entity, domainsRequired bool) []Issue {
	var issues []Issue
	if entity == nil {
		return []Issue{{Path: path, Message: "is required"}}
	}
	entity.Name = strings.TrimSpace(entity.Name)
	entity.Category = strings.TrimSpace(entity.Category)
	if entity.Name == "" {
		issues = append(issues, Issue{Path: path + ".name", Message: "is required"})
	}
	trimStringSlice(&entity.Aliases)
	normalizeDomains(&issues, path+".domains", &entity.Domains)
	if domainsRequired && len(entity.Domains) == 0 {
		issues = append(issues, Issue{Path: path + ".domains", Message: "must not be empty"})
	}
	return issues
}

func validateExpectedEntity(path string, entity *ExpectedEntity) []Issue {
	var issues []Issue
	entity.CanonicalName = strings.TrimSpace(entity.CanonicalName)
	entity.EntityType = strings.TrimSpace(entity.EntityType)
	trimStringSlicePreserve(&entity.Aliases)
	trimStringSlicePreserve(&entity.DescriptorsAny)
	if entity.CanonicalName == "" {
		issues = append(issues, Issue{Path: path + ".canonical_name", Message: "is required"})
	}
	if entity.EntityType == "" {
		issues = append(issues, Issue{Path: path + ".entity_type", Message: "is required"})
	} else if _, ok := knownEntityTypes[entity.EntityType]; !ok {
		issues = append(issues, Issue{Path: path + ".entity_type", Message: fmt.Sprintf("%q is not a known entity type", entity.EntityType)})
	}
	return issues
}

func validateAnswers(path string, answers *AnswerSet) []Issue {
	if answers == nil {
		return []Issue{{Path: path, Message: "is required"}}
	}
	answers.Canonical = strings.TrimSpace(answers.Canonical)
	trimStringSlice(&answers.Aliases)
	if answers.Canonical == "" {
		return []Issue{{Path: path + ".canonical", Message: "is required"}}
	}
	return nil
}

func validateGrading(path string, grading *Grading, allowed ...string) []Issue {
	if grading == nil {
		return []Issue{{Path: path, Message: "is required"}}
	}
	var issues []Issue
	grading.Type = strings.TrimSpace(grading.Type)
	if grading.Type == "" {
		return []Issue{{Path: path + ".type", Message: "is required"}}
	}
	if _, ok := knownGradingTypes[grading.Type]; !ok {
		return []Issue{{Path: path + ".type", Message: fmt.Sprintf("%q is not a known grading type", grading.Type)}}
	}
	if len(allowed) > 0 {
		matched := false
		for _, candidate := range allowed {
			if grading.Type == candidate {
				matched = true
				break
			}
		}
		if !matched {
			issues = append(issues, Issue{Path: path + ".type", Message: fmt.Sprintf("%q does not work for this kind of item", grading.Type)})
		}
	}
	grading.Confidence = strings.TrimSpace(grading.Confidence)
	if grading.Confidence != "" {
		switch grading.Confidence {
		case benchmark.ConfidencePolicyNone, benchmark.ConfidencePolicyOptional, benchmark.ConfidencePolicyRequired:
		default:
			issues = append(issues, Issue{Path: path + ".confidence", Message: fmt.Sprintf("%q is not a known confidence policy", grading.Confidence)})
		}
	}
	return issues
}

func validateReferenceSource(path string, source ReferenceSource) []Issue {
	var issues []Issue
	if strings.TrimSpace(source.URL) == "" {
		issues = append(issues, Issue{Path: path + ".url", Message: "is required"})
		return issues
	}
	u, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		issues = append(issues, Issue{Path: path + ".url", Message: "must be an absolute URL"})
		return issues
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		issues = append(issues, Issue{Path: path + ".url", Message: "must use http or https"})
	}
	return issues
}

func validateClaim(path string, claim *Claim) []Issue {
	var issues []Issue
	if claim == nil {
		return []Issue{{Path: path, Message: "is required"}}
	}
	claim.ID = strings.TrimSpace(claim.ID)
	claim.Text = strings.TrimSpace(claim.Text)
	if claim.ID == "" {
		issues = append(issues, Issue{Path: path + ".id", Message: "is required"})
	} else if !itemIDPattern.MatchString(claim.ID) {
		issues = append(issues, Issue{Path: path + ".id", Message: "may only use lowercase letters, digits, dots, dashes and underscores"})
	}
	if claim.Text == "" {
		issues = append(issues, Issue{Path: path + ".text", Message: "is required"})
	} else {
		if !utf8.ValidString(claim.Text) {
			issues = append(issues, Issue{Path: path + ".text", Message: "must be valid UTF-8"})
		}
		if len([]byte(claim.Text)) > 1024 {
			issues = append(issues, Issue{Path: path + ".text", Message: "must be 1024 UTF-8 bytes or fewer"})
		}
		if strings.HasPrefix(claim.Text, "[") && strings.HasSuffix(claim.Text, "]") {
			issues = append(issues, Issue{Path: path + ".text", Message: "must not be a placeholder"})
		}
	}
	return issues
}

func normalizeDomains(issues *[]Issue, path string, domains *[]string) {
	if domains == nil || len(*domains) == 0 {
		return
	}
	normalized, err := domain.NormalizeScoringDomains(*domains)
	if err != nil {
		*issues = append(*issues, Issue{Path: path, Message: err.Error()})
		return
	}
	*domains = normalized
}

func issueAppender(issues *[]Issue, base string) func(string, string) {
	return func(sub, message string) {
		path := base
		if sub != "" {
			path += "." + sub
		}
		*issues = append(*issues, Issue{Path: path, Message: message})
	}
}

func trimSuite(s *Suite) {
	s.ID = strings.TrimSpace(s.ID)
	s.Type = strings.TrimSpace(s.Type)
	s.ContextMode = strings.TrimSpace(s.ContextMode)
	s.Purpose = strings.TrimSpace(s.Purpose)
	trimStringSlicePreserve(&s.DefaultModes)
	for i := range s.Variants {
		trimVariant(&s.Variants[i])
	}
	for i := range s.Evaluators {
		s.Evaluators[i].ID = strings.TrimSpace(s.Evaluators[i].ID)
		s.Evaluators[i].Type = strings.TrimSpace(s.Evaluators[i].Type)
	}
	for i := range s.Metrics {
		s.Metrics[i].ID = strings.TrimSpace(s.Metrics[i].ID)
		s.Metrics[i].Evaluator = strings.TrimSpace(s.Metrics[i].Evaluator)
		s.Metrics[i].Kind = strings.TrimSpace(s.Metrics[i].Kind)
	}
}

func fillProjectBoundValidationContext(item *Item) {
	if item.Brand == nil {
		item.Brand = &Entity{}
	}
	if item.Brand.Name == "" {
		item.Brand.Name = "Configured Brand"
	}
	if item.Brand.Category == "" {
		item.Brand.Category = "configured category"
	}
	if len(item.OwnedDomains) == 0 {
		item.OwnedDomains = []string{"configured.example"}
	}
}

func trimItem(item *Item) {
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(item.Type)
	item.Prompt = strings.TrimSpace(item.Prompt)
	item.Question = strings.TrimSpace(item.Question)
	item.Query = strings.TrimSpace(item.Query)
	item.Intent = strings.TrimSpace(item.Intent)
	item.Locale = strings.TrimSpace(item.Locale)
	item.ExpectedAnswerShape = strings.TrimSpace(item.ExpectedAnswerShape)
	item.Privacy = strings.TrimSpace(item.Privacy)
	item.EventDate = strings.TrimSpace(item.EventDate)
	item.ExpectedStance = strings.TrimSpace(item.ExpectedStance)
	item.BaseItem = strings.TrimSpace(item.BaseItem)
	item.ComparisonGroupID = strings.TrimSpace(item.ComparisonGroupID)
	trimStringSlicePreserve(&item.OwnedDomains)
	trimStringSlicePreserve(&item.CompetitorDomains)
	trimStringSlicePreserve(&item.PreferredDomains)
	trimStringSlicePreserve(&item.BlockedDomains)
	trimStringSlicePreserve(&item.EvaluatorIDs)
	trimStringSlicePreserve(&item.MetricIDs)
	trimStringSlicePreserve(&item.Tags)
	if item.Grading != nil {
		item.Grading.Type = strings.TrimSpace(item.Grading.Type)
		item.Grading.Confidence = strings.TrimSpace(item.Grading.Confidence)
	}
	for i := range item.KnownWrongAnswers {
		item.KnownWrongAnswers[i].ID = strings.TrimSpace(item.KnownWrongAnswers[i].ID)
		trimStringSlice(&item.KnownWrongAnswers[i].Aliases)
	}
	for i := range item.FailureCases {
		item.FailureCases[i].ID = strings.TrimSpace(item.FailureCases[i].ID)
		item.FailureCases[i].Class = strings.TrimSpace(item.FailureCases[i].Class)
		trimStringSlice(&item.FailureCases[i].Aliases)
	}
	if item.Stack != nil {
		normalizeStackField(&item.Stack.Language)
		normalizeStackField(&item.Stack.Ecosystem)
		normalizeStackField(&item.Stack.Library)
		normalizeStackField(&item.Stack.API)
	}
	for i := range item.StaleAnswers {
		item.StaleAnswers[i].ID = strings.TrimSpace(item.StaleAnswers[i].ID)
		item.StaleAnswers[i].StaleAsOf = strings.TrimSpace(item.StaleAnswers[i].StaleAsOf)
		trimStringSlice(&item.StaleAnswers[i].Aliases)
	}
	if item.SourceExpectation != nil {
		trimStringSlicePreserve(&item.SourceExpectation.RequiredForModes)
		item.SourceExpectation.FreshnessReferenceDate = strings.TrimSpace(item.SourceExpectation.FreshnessReferenceDate)
	}
	for i := range item.Variants {
		trimVariant(&item.Variants[i])
	}
}

func trimVariant(variant *Variant) {
	variant.ID = strings.TrimSpace(variant.ID)
	variant.Prompt = strings.TrimSpace(variant.Prompt)
	variant.Template = strings.TrimSpace(variant.Template)
	variant.Description = strings.TrimSpace(variant.Description)
	variant.Locale = strings.TrimSpace(variant.Locale)
	variant.Persona = strings.TrimSpace(variant.Persona)
}

func trimStringSlice(values *[]string) {
	if len(*values) == 0 {
		return
	}
	out := make([]string, 0, len(*values))
	for _, value := range *values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	*values = out
}

func trimStringSlicePreserve(values *[]string) {
	for i, value := range *values {
		(*values)[i] = strings.TrimSpace(value)
	}
}
