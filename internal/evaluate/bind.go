package evaluate

import (
	"fmt"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/suite"
)

// BindProjectContext copies the suites and fills project-bound suites with
// the brand and competitors from llmention.yaml. A recognition suite gets one
// item for each name and site under brand.
func BindProjectContext(project *config.Project, entries []suite.LoadedSuite) ([]suite.LoadedSuite, []ReadinessIssue, error) {
	copied, err := cloneLoadedSuites(entries)
	if err != nil {
		return nil, nil, err
	}
	var issues []ReadinessIssue
	for _, entry := range copied {
		s := entry.Suite
		if s == nil || s.ContextMode != suite.ContextModeProjectBound {
			continue
		}
		if project == nil || project.Brand == nil || project.Brand.Name == "" {
			issues = append(issues, readinessIssue("missing_target_context", ReadinessFatal, s.ID, s.Version, "", "", "", "this suite needs brand.name in llmention.yaml"))
			continue
		}
		if s.Type == "recognition" {
			s.Items = recognitionItems(project.Brand)
			continue
		}
		for i := range s.Items {
			item := &s.Items[i]
			if item.Brand != nil || len(item.Competitors) > 0 || len(item.OwnedDomains) > 0 || len(item.CompetitorDomains) > 0 {
				issues = append(issues, readinessIssue("conflicting_target_context", ReadinessFatal, s.ID, s.Version, item.ID, "", "", "this suite takes the brand and competitors from llmention.yaml, so its items must not set their own"))
			}
			bindItem(item, project)
		}
	}
	return copied, issues, nil
}

func bindItem(item *suite.Item, project *config.Project) {
	b := project.Brand
	item.Brand = &suite.Entity{
		Name:      b.Name,
		Category:  b.Category,
		Aliases:   append(append([]string(nil), b.Aliases...), b.Products...),
		Domains:   append([]string(nil), b.Domains...),
		MatchCase: b.MatchCase,
	}
	item.OwnedDomains = append([]string(nil), b.Domains...)
	item.Competitors = nil
	item.CompetitorDomains = nil
	for _, c := range project.Competitors {
		item.Competitors = append(item.Competitors, suite.Entity{
			Name:      c.Name,
			Aliases:   append([]string(nil), c.Aliases...),
			Domains:   append([]string(nil), c.Domains...),
			MatchCase: c.MatchCase,
		})
		item.CompetitorDomains = append(item.CompetitorDomains, c.Domains...)
	}
}

// recognitionItems builds the names a recognition run looks up.
func recognitionItems(b *config.BrandConfig) []suite.Item {
	brand := suite.ExpectedEntity{
		CanonicalName: b.Name,
		EntityType:    "product",
		Aliases:       append(append([]string(nil), b.Aliases...), b.Domains...),
	}
	items := []suite.Item{recognitionItem("brand", b.Name, brand, "brand")}
	for _, product := range b.Products {
		items = append(items, recognitionItem("product-"+slug(product), product, suite.ExpectedEntity{CanonicalName: product, EntityType: "product"}, "product"))
	}
	for _, person := range b.People {
		items = append(items, recognitionItem("person-"+slug(person), person, suite.ExpectedEntity{CanonicalName: person, EntityType: "person"}, "person"))
	}
	for _, domain := range b.Domains {
		items = append(items, recognitionItem("domain-"+slug(domain), domain, brand, "domain"))
	}
	return items
}

func recognitionItem(id, query string, expected suite.ExpectedEntity, tag string) suite.Item {
	return suite.Item{
		ID:               id,
		Type:             "recognition.entity",
		Query:            query,
		ExpectedEntities: []suite.ExpectedEntity{expected},
		Privacy:          "local_only",
		Tags:             []string{tag},
	}
}

func slug(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "x"
	}
	return out
}

func readinessIssue(code, severity, suiteID string, suiteVersion int, itemID, evaluatorType, metricID, message string) ReadinessIssue {
	return ReadinessIssue{
		ID:            issueID(code, suiteID, fmt.Sprintf("%d", suiteVersion), itemID, evaluatorType, metricID, message),
		Code:          code,
		Severity:      severity,
		SuiteID:       suiteID,
		SuiteVersion:  suiteVersion,
		ItemID:        itemID,
		EvaluatorType: evaluatorType,
		MetricID:      metricID,
		Message:       message,
	}
}

// RebindProject swaps the brand and competitors in suites that were bound
// for an earlier run, so old answers are scored with the current names.
// Recognition suites are left as they are, because their items are the names
// the run asked about.
func RebindProject(project *config.Project, entries []suite.LoadedSuite) ([]suite.LoadedSuite, error) {
	copied, err := cloneLoadedSuites(entries)
	if err != nil {
		return nil, err
	}
	if project == nil || project.Brand == nil {
		return copied, nil
	}
	for _, entry := range copied {
		s := entry.Suite
		if s == nil || s.ContextMode != suite.ContextModeProjectBound || s.Type == "recognition" {
			continue
		}
		for i := range s.Items {
			bindItem(&s.Items[i], project)
		}
	}
	return copied, nil
}
