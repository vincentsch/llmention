package recognition

import (
	"sort"
	"strings"
	"unicode"
)

type ClusterOutput struct {
	Assignments []Assignment
	Warnings    []Warning
}

type SummaryInput struct {
	Cells             []CellPayload
	PlannedWorkKeys   []string
	SelectedEngineIDs []string
	SuiteID           string
	SuiteVersion      int
	ItemID            string
	VariantID         string
	Mode              string
	Dimensions        map[string]string
	ComparisonGroupID string
}

func ClusterResults(config ItemConfig, results []ParsedResult) ClusterOutput {
	var out ClusterOutput
	for _, result := range results {
		assignment, warning := classifyResult(config, result)
		if warning != nil {
			out.Warnings = append(out.Warnings, *warning)
		}
		out.Assignments = append(out.Assignments, assignment)
	}
	sort.SliceStable(out.Assignments, func(i, j int) bool {
		return out.Assignments[i].Position < out.Assignments[j].Position
	})
	out.Warnings = SortWarnings(out.Warnings)
	return out
}

func TargetRecognized(assignments []Assignment) bool {
	for _, assignment := range assignments {
		if assignment.AssignmentClassification == ClassificationTargetEntity {
			return true
		}
	}
	return false
}

func UnresolvedAssignments(assignments []Assignment) []Assignment {
	var out []Assignment
	for _, assignment := range assignments {
		if assignment.AssignmentClassification == ClassificationUnknownNamesake {
			out = append(out, assignment)
		}
	}
	return out
}

func BuildSummary(input SummaryInput) SummaryPayload {
	group := summaryGroup{
		suiteID:           input.SuiteID,
		suiteVersion:      input.SuiteVersion,
		itemID:            input.ItemID,
		variantID:         input.VariantID,
		mode:              input.Mode,
		dimensions:        cloneStringMap(input.Dimensions),
		comparisonGroupID: input.ComparisonGroupID,
	}
	clusterBuilders := map[string]*summaryClusterBuilder{}
	eligibleEngines := map[string]struct{}{}
	recognizingEngines := map[string]struct{}{}
	seenEngineIDs := map[string]struct{}{}
	for _, id := range input.SelectedEngineIDs {
		if strings.TrimSpace(id) != "" {
			seenEngineIDs[id] = struct{}{}
		}
	}
	for _, cell := range input.Cells {
		if cell.EngineID != "" {
			seenEngineIDs[cell.EngineID] = struct{}{}
		}
		if cell.ParserStatus != ParserStatusCompleted {
			continue
		}
		eligibleEngines[cell.EngineID] = struct{}{}
		if cell.TargetRecognized != nil && *cell.TargetRecognized {
			recognizingEngines[cell.EngineID] = struct{}{}
		}
		parsedByID := map[string]ParsedResult{}
		for _, parsed := range cell.ParsedResults {
			parsedByID[parsed.ParsedResultID] = parsed
		}
		for _, assignment := range cell.Assignments {
			builder := clusterBuilders[assignment.ClusterID]
			if builder == nil {
				builder = &summaryClusterBuilder{
					cluster: SummaryCluster{
						ClusterID:                      assignment.ClusterID,
						ClusterName:                    assignment.ClusterName,
						ClusterType:                    assignment.ClusterType,
						AssignmentClassificationCounts: map[string]int{},
					},
					engineIDs:       map[string]struct{}{},
					parsedResultIDs: map[string]struct{}{},
					workKeys:        map[string]struct{}{},
					confidenceMin:   101,
				}
				clusterBuilders[assignment.ClusterID] = builder
			}
			builder.cluster.AssignmentClassificationCounts[assignment.AssignmentClassification]++
			builder.cluster.SupportCount++
			builder.engineIDs[cell.EngineID] = struct{}{}
			builder.parsedResultIDs[assignment.ParsedResultID] = struct{}{}
			builder.workKeys[cell.ResultKey] = struct{}{}
			builder.confidenceSum += assignment.Confidence
			if assignment.Confidence < builder.confidenceMin {
				builder.confidenceMin = assignment.Confidence
			}
			if assignment.Confidence > builder.confidenceMax {
				builder.confidenceMax = assignment.Confidence
			}
			if assignment.AssignmentClassification == ClassificationHallucinationCandidate {
				builder.cluster.HallucinationCandidateSupportCount++
			}
			if assignment.MatchBasis == "judge" {
				builder.cluster.SuccessfulJudgedSupportCount++
			}
			if parsed, ok := parsedByID[assignment.ParsedResultID]; ok && parsed.Snippet != "" {
				builder.snippets = append(builder.snippets, parsed.Snippet)
			}
		}
	}
	var clusters []SummaryCluster
	for _, builder := range clusterBuilders {
		cluster := builder.finish()
		clusters = append(clusters, cluster)
	}
	SortSummaryClusters(clusters)
	unavailable := setSubtract(seenEngineIDs, eligibleEngines)
	return SummaryPayload{
		PayloadType:          PayloadTypeSummary,
		PayloadSchemaVersion: PayloadSchemaVersion,
		SummaryScopeKey: SummaryScopeKey(
			group.suiteID,
			group.suiteVersion,
			group.itemID,
			group.variantID,
			group.mode,
			group.dimensions,
			group.comparisonGroupID,
		),
		SuiteID:              group.suiteID,
		SuiteVersion:         group.suiteVersion,
		ItemID:               group.itemID,
		VariantID:            group.variantID,
		Mode:                 group.mode,
		Dimensions:           cloneStringMap(group.dimensions),
		ComparisonGroupID:    PtrString(group.comparisonGroupID),
		InputSetHash:         InputSetHash(input.PlannedWorkKeys),
		EligibleEngineIDs:    sortedSet(eligibleEngines),
		RecognizingEngineIDs: sortedSet(recognizingEngines),
		UnavailableEngineIDs: sortedSet(unavailable),
		Clusters:             clusters,
		JudgeMetadata:        sharedJudgeMetadata(input.Cells),
	}
}

func sharedJudgeMetadata(cells []CellPayload) *JudgeMetadata {
	var out *JudgeMetadata
	var signature string
	for _, cell := range cells {
		if cell.JudgeMetadata == nil {
			continue
		}
		current := judgeMetadataSignature(cell.JudgeMetadata)
		if signature != "" && current != signature {
			return nil
		}
		signature = current
		copied := *cell.JudgeMetadata
		out = &copied
	}
	return out
}

func judgeMetadataSignature(metadata *JudgeMetadata) string {
	if metadata == nil {
		return ""
	}
	return strings.Join([]string{
		metadata.TemplateID,
		metadata.TemplateVersion,
		metadata.RequestMode,
		metadata.JudgeEngineID,
		metadata.JudgeProvider,
		metadata.JudgeModel,
	}, "\x00")
}

func SortSummaryClusters(clusters []SummaryCluster) {
	sort.SliceStable(clusters, func(i, j int) bool {
		li := classificationOrder(clusters[i].EffectiveClassification)
		lj := classificationOrder(clusters[j].EffectiveClassification)
		if li != lj {
			return li < lj
		}
		leftName := NormalizeForMatch(clusters[i].ClusterName)
		rightName := NormalizeForMatch(clusters[j].ClusterName)
		if leftName != rightName {
			return leftName < rightName
		}
		leftType := NormalizeForMatch(clusters[i].ClusterType)
		rightType := NormalizeForMatch(clusters[j].ClusterType)
		if leftType != rightType {
			return leftType < rightType
		}
		return clusters[i].ClusterID < clusters[j].ClusterID
	})
}

func SortCells(cells []CellPayload) {
	sort.SliceStable(cells, func(i, j int) bool {
		if cells[i].SuiteID != cells[j].SuiteID {
			return cells[i].SuiteID < cells[j].SuiteID
		}
		if cells[i].SuiteVersion != cells[j].SuiteVersion {
			return cells[i].SuiteVersion < cells[j].SuiteVersion
		}
		if cells[i].ItemID != cells[j].ItemID {
			return cells[i].ItemID < cells[j].ItemID
		}
		if cells[i].VariantID != cells[j].VariantID {
			return cells[i].VariantID < cells[j].VariantID
		}
		if cells[i].Mode != cells[j].Mode {
			return cells[i].Mode < cells[j].Mode
		}
		if cells[i].EngineID != cells[j].EngineID {
			return cells[i].EngineID < cells[j].EngineID
		}
		if cells[i].Repetition != cells[j].Repetition {
			return cells[i].Repetition < cells[j].Repetition
		}
		return cells[i].ResultKey < cells[j].ResultKey
	})
}

func classifyResult(config ItemConfig, result ParsedResult) (Assignment, *Warning) {
	if assignment, warning, ok := matchConfigured(config.ExpectedEntities, ClassificationTargetEntity, result); ok {
		return assignment, warning
	}
	if assignment, warning, ok := matchConfigured(config.KnownWrongNamesakes, ClassificationKnownNamesake, result); ok {
		return assignment, warning
	}
	if result.Type != EntityTypeUnknown && incompatibleWithAllTargets(config.ExpectedEntities, result.Type) &&
		!nameMatchesAny(config.ExpectedEntities, result.Name) &&
		!nameMatchesAny(config.KnownWrongNamesakes, result.Name) {
		return assignmentForParsed(result, ClassificationNonTarget, "type_incompatible"), nil
	}
	return assignmentForParsed(result, ClassificationUnknownNamesake, "unresolved"), nil
}

func matchConfigured(configured []ConfiguredEntity, classification string, result ParsedResult) (Assignment, *Warning, bool) {
	var matches []configuredMatch
	for index, entity := range configured {
		nameBasis := configuredNameMatch(entity, result.Name)
		if nameBasis == "" || !typesCompatible(entity.EntityType, result.Type) {
			continue
		}
		if len(entity.DescriptorsAny) > 0 && !descriptorMatches(entity.DescriptorsAny, result.Name+"\n"+result.Snippet) {
			continue
		}
		basis := nameBasis
		if len(entity.DescriptorsAny) > 0 {
			basis = "descriptor"
		}
		matches = append(matches, configuredMatch{Index: index, Entity: entity, Basis: basis})
	}
	if len(matches) == 0 {
		return Assignment{}, nil, false
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Index < matches[j].Index })
	match := matches[0]
	assignment := assignmentForConfigured(result, match.Entity, classification, match.Basis)
	var warning *Warning
	if len(matches) > 1 {
		w := Warning{Code: WarningAmbiguousConfiguredMatch, ParsedResultID: result.ParsedResultID, ClusterID: assignment.ClusterID}
		warning = &w
	}
	return assignment, warning, true
}

type configuredMatch struct {
	Index  int
	Entity ConfiguredEntity
	Basis  string
}

func configuredNameMatch(entity ConfiguredEntity, name string) string {
	normalizedName := NormalizeForMatch(name)
	if NormalizeForMatch(entity.CanonicalName) == normalizedName {
		return "canonical"
	}
	for _, alias := range entity.Aliases {
		if NormalizeForMatch(alias) == normalizedName {
			return "alias"
		}
	}
	return ""
}

func descriptorMatches(descriptors []string, haystack string) bool {
	haystack = NormalizeForMatch(haystack)
	for _, descriptor := range descriptors {
		needle := NormalizeForMatch(descriptor)
		if needle == "" {
			continue
		}
		if boundaryContains(haystack, needle) {
			return true
		}
	}
	return false
}

func boundaryContains(haystack, needle string) bool {
	h := []rune(haystack)
	n := []rune(needle)
	if len(n) == 0 || len(n) > len(h) {
		return false
	}
	for i := 0; i <= len(h)-len(n); i++ {
		matched := true
		for j := range n {
			if h[i+j] != n[j] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		beforeOK := i == 0 || !unicode.IsLetter(h[i-1]) && !unicode.IsDigit(h[i-1])
		afterIndex := i + len(n)
		afterOK := afterIndex == len(h) || !unicode.IsLetter(h[afterIndex]) && !unicode.IsDigit(h[afterIndex])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

// typesCompatible treats the kinds of things you can buy or use as one
// family, because models call the same brand a product one time and a
// company the next. A person never matches one of these.
func typesCompatible(configuredType, parsedType string) bool {
	if configuredType == "" || parsedType == "" || parsedType == EntityTypeUnknown {
		return false
	}
	return configuredType == parsedType || (offeringTypes[configuredType] && offeringTypes[parsedType])
}

var offeringTypes = map[string]bool{
	EntityTypeCompany:      true,
	EntityTypeOrganization: true,
	EntityTypeProduct:      true,
	EntityTypeProject:      true,
	EntityTypeLibrary:      true,
	EntityTypeDomain:       true,
}

func incompatibleWithAllTargets(targets []ConfiguredEntity, parsedType string) bool {
	if parsedType == "" || parsedType == EntityTypeUnknown {
		return false
	}
	for _, target := range targets {
		if typesCompatible(target.EntityType, parsedType) {
			return false
		}
	}
	return true
}

func nameMatchesAny(configured []ConfiguredEntity, name string) bool {
	for _, entity := range configured {
		if configuredNameMatch(entity, name) != "" {
			return true
		}
	}
	return false
}

func assignmentForConfigured(result ParsedResult, entity ConfiguredEntity, classification, basis string) Assignment {
	return Assignment{
		ParsedResultID:           result.ParsedResultID,
		Position:                 result.Position,
		ClusterID:                StableClusterID(entity.CanonicalName, entity.EntityType),
		ClusterName:              SanitizeText(entity.CanonicalName, MaxNameRunes).Text,
		ClusterType:              NormalizeForMatch(entity.EntityType),
		AssignmentClassification: classification,
		MatchBasis:               basis,
		Confidence:               result.Confidence,
	}
}

func assignmentForParsed(result ParsedResult, classification, basis string) Assignment {
	return Assignment{
		ParsedResultID:           result.ParsedResultID,
		Position:                 result.Position,
		ClusterID:                StableClusterID(result.Name, result.Type),
		ClusterName:              result.Name,
		ClusterType:              result.Type,
		AssignmentClassification: classification,
		MatchBasis:               basis,
		Confidence:               result.Confidence,
	}
}

type summaryGroup struct {
	suiteID           string
	suiteVersion      int
	itemID            string
	variantID         string
	mode              string
	dimensions        map[string]string
	comparisonGroupID string
}

type summaryClusterBuilder struct {
	cluster         SummaryCluster
	engineIDs       map[string]struct{}
	parsedResultIDs map[string]struct{}
	workKeys        map[string]struct{}
	confidenceMin   int
	confidenceMax   int
	confidenceSum   int
	snippets        []string
}

func (b *summaryClusterBuilder) finish() SummaryCluster {
	if b.engineIDs == nil {
		b.engineIDs = map[string]struct{}{}
	}
	if b.parsedResultIDs == nil {
		b.parsedResultIDs = map[string]struct{}{}
	}
	if b.workKeys == nil {
		b.workKeys = map[string]struct{}{}
	}
	cluster := b.cluster
	cluster.SupportingEngineIDs = sortedSet(b.engineIDs)
	cluster.SupportingEngineCount = len(cluster.SupportingEngineIDs)
	cluster.SupportingParsedResultIDs = sortedSet(b.parsedResultIDs)
	cluster.SupportingWorkKeys = sortedSet(b.workKeys)
	if cluster.SupportCount > 0 {
		minValue := b.confidenceMin
		maxValue := b.confidenceMax
		mean := float64(b.confidenceSum) / float64(cluster.SupportCount)
		cluster.ConfidenceMin = &minValue
		cluster.ConfidenceMax = &maxValue
		cluster.ConfidenceMean = &mean
	}
	cluster.EffectiveClassification = effectiveClassification(cluster.AssignmentClassificationCounts)
	cluster.Snippets = uniqueSorted(b.snippets)
	return cluster
}

func effectiveClassification(counts map[string]int) string {
	if counts[ClassificationTargetEntity] > 0 {
		return ClassificationTargetEntity
	}
	if counts[ClassificationKnownNamesake] > 0 {
		return ClassificationKnownNamesake
	}
	if counts[ClassificationHallucinationCandidate] > 0 && counts[ClassificationHallucinationCandidate] == sumCounts(counts) {
		return ClassificationHallucinationCandidate
	}
	if counts[ClassificationUnknownNamesake] > 0 || counts[ClassificationHallucinationCandidate] > 0 {
		return ClassificationUnknownNamesake
	}
	return ClassificationNonTarget
}

func classificationOrder(value string) int {
	switch value {
	case ClassificationTargetEntity:
		return 0
	case ClassificationKnownNamesake:
		return 1
	case ClassificationUnknownNamesake:
		return 2
	case ClassificationHallucinationCandidate:
		return 3
	case ClassificationNonTarget:
		return 4
	default:
		return 9
	}
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, value := range counts {
		total += value
	}
	return total
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func setSubtract(left, right map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for value := range left {
		if _, ok := right[value]; !ok {
			out[value] = struct{}{}
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
