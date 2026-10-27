package suite

import (
	"bytes"
	"fmt"
	"maps"
	"strings"
	"text/template"
	"text/template/parse"
)

func RenderVariants(s *Suite, item Item) ([]RenderedVariant, error) {
	if s == nil {
		return nil, fmt.Errorf("suite is nil")
	}
	if s.ContextMode == ContextModeProjectBound {
		fillProjectBoundRenderContext(&item)
	}
	if s.Type == "recognition" {
		return renderRecognitionVariants(s, item)
	}
	if isBenchmarkSuiteType(s.Type) {
		return renderBenchmarkVariants(s, item)
	}
	if isAuditSuiteType(s.Type) {
		return renderAuditVariants(s, item)
	}
	itemVariants := item.Variants
	if len(itemVariants) == 0 {
		itemVariants = s.Variants
	}
	if len(itemVariants) == 0 {
		prompt := primaryPrompt(item)
		if prompt == "" {
			return nil, fmt.Errorf("item %q has no prompt, question, query, or variants", item.ID)
		}
		return []RenderedVariant{{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         "default",
			Prompt:            prompt,
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		}}, nil
	}

	rendered := make([]RenderedVariant, 0, len(itemVariants))
	for _, variant := range itemVariants {
		prompt, err := renderVariantPrompt(variant, item)
		if err != nil {
			return nil, fmt.Errorf("variant %q: %w", variant.ID, err)
		}
		rendered = append(rendered, RenderedVariant{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         variant.ID,
			Prompt:            prompt,
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		})
	}
	return rendered, nil
}

func renderAuditVariants(s *Suite, item Item) ([]RenderedVariant, error) {
	itemVariants := item.Variants
	if len(itemVariants) == 0 {
		itemVariants = s.Variants
	}
	if len(itemVariants) == 0 {
		prompt := ""
		if s.Type == "censorship" {
			prompt = item.Question
		} else {
			prompt = item.Prompt
		}
		if strings.TrimSpace(prompt) == "" {
			field := "prompt"
			if s.Type == "censorship" {
				field = "question"
			}
			return nil, fmt.Errorf("item %q has no %s", item.ID, field)
		}
		return []RenderedVariant{{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         "default",
			Prompt:            strings.TrimSpace(prompt),
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		}}, nil
	}
	allowedField := "Question"
	if s.Type == "propaganda" {
		allowedField = "Prompt"
	}
	rendered := make([]RenderedVariant, 0, len(itemVariants))
	for _, variant := range itemVariants {
		prompt, err := renderAuditVariantPrompt(variant, item, allowedField)
		if err != nil {
			return nil, fmt.Errorf("variant %q: %w", variant.ID, err)
		}
		rendered = append(rendered, RenderedVariant{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         variant.ID,
			Prompt:            prompt,
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		})
	}
	return rendered, nil
}

func renderAuditVariantPrompt(variant Variant, item Item, allowedField string) (string, error) {
	source := variant.Template
	if source == "" {
		source = variant.Prompt
	}
	if strings.TrimSpace(source) == "" {
		switch allowedField {
		case "Question":
			source = "{{ .Question }}"
		default:
			source = "{{ .Prompt }}"
		}
	}
	if err := validateAuditTemplateSource(source, allowedField); err != nil {
		return "", err
	}
	tmpl, err := template.New("audit_variant").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	data := map[string]string{
		"Question": item.Question,
		"Prompt":   item.Prompt,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(buf.String())
	if prompt == "" {
		return "", fmt.Errorf("the prompt is empty once the template is filled in")
	}
	return prompt, nil
}

func validateAuditTemplateSource(source, allowedField string) error {
	tmpl, err := template.New("audit_variant").Parse(source)
	if err != nil {
		return err
	}
	for _, t := range tmpl.Templates() {
		if t == nil || t.Root == nil {
			continue
		}
		if err := validateAuditTemplateNode(t.Root, allowedField); err != nil {
			return err
		}
	}
	return nil
}

func validateAuditTemplateNode(node parse.Node, allowedField string) error {
	switch n := node.(type) {
	case *parse.ListNode:
		for _, child := range n.Nodes {
			if err := validateAuditTemplateNode(child, allowedField); err != nil {
				return err
			}
		}
	case *parse.TextNode:
		return nil
	case *parse.ActionNode:
		if n.Pipe == nil || len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
			return fmt.Errorf("a variant template may only use {{.%s}}", allowedField)
		}
		cmd := n.Pipe.Cmds[0]
		if len(cmd.Args) != 1 {
			return fmt.Errorf("a variant template may only use {{.%s}}", allowedField)
		}
		field, ok := cmd.Args[0].(*parse.FieldNode)
		if !ok || len(field.Ident) != 1 || field.Ident[0] != allowedField {
			return fmt.Errorf("a variant template may only use {{.%s}}", allowedField)
		}
	default:
		return fmt.Errorf("a variant template may only hold text and {{.%s}}", allowedField)
	}
	return nil
}

func renderBenchmarkVariants(s *Suite, item Item) ([]RenderedVariant, error) {
	itemVariants := item.Variants
	if len(itemVariants) == 0 {
		itemVariants = s.Variants
	}
	if len(itemVariants) == 0 {
		itemVariants = []Variant{{ID: "default"}}
	}
	rendered := make([]RenderedVariant, 0, len(itemVariants))
	for _, variant := range itemVariants {
		prompt, err := renderBenchmarkFraming(variant, item)
		if err != nil {
			return nil, fmt.Errorf("variant %q: %w", variant.ID, err)
		}
		rendered = append(rendered, RenderedVariant{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         variant.ID,
			Prompt:            prompt,
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		})
	}
	return rendered, nil
}

func renderBenchmarkFraming(variant Variant, item Item) (string, error) {
	source := variant.Template
	if source == "" {
		source = variant.Prompt
	}
	if strings.TrimSpace(source) == "" {
		return "", nil
	}
	tmpl, err := template.New("benchmark_variant").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, benchmarkVariantData(variant, item)); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func renderRecognitionVariants(s *Suite, item Item) ([]RenderedVariant, error) {
	itemVariants := item.Variants
	if len(itemVariants) == 0 {
		itemVariants = s.Variants
	}
	if len(itemVariants) == 0 {
		itemVariants = []Variant{{ID: "default"}}
	}
	rendered := make([]RenderedVariant, 0, len(itemVariants))
	for _, variant := range itemVariants {
		prompt, err := renderRecognitionFraming(variant, item)
		if err != nil {
			return nil, fmt.Errorf("variant %q: %w", variant.ID, err)
		}
		rendered = append(rendered, RenderedVariant{
			SuiteID:           s.ID,
			SuiteVersion:      s.Version,
			ItemID:            item.ID,
			VariantID:         variant.ID,
			Prompt:            prompt,
			Dimensions:        maps.Clone(item.Dimensions),
			ComparisonGroupID: item.ComparisonGroupID,
		})
	}
	return rendered, nil
}

func renderRecognitionFraming(variant Variant, item Item) (string, error) {
	source := variant.Template
	if source == "" {
		source = variant.Prompt
	}
	if strings.TrimSpace(source) == "" {
		return "", nil
	}
	tmpl, err := template.New("recognition_variant").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, variantData(item)); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func fillProjectBoundRenderContext(item *Item) {
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

func renderVariantPrompt(variant Variant, item Item) (string, error) {
	source := variant.Template
	if source == "" {
		source = variant.Prompt
	}
	if source == "" {
		source = primaryPrompt(item)
	}
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("the prompt template is empty")
	}
	tmpl, err := template.New("suite_variant").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, variantData(item)); err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(buf.String())
	if strings.Contains(prompt, "{{") {
		tmpl, err := template.New("suite_prompt").Option("missingkey=error").Parse(prompt)
		if err != nil {
			return "", err
		}
		buf.Reset()
		if err := tmpl.Execute(&buf, variantData(item)); err != nil {
			return "", err
		}
		prompt = strings.TrimSpace(buf.String())
	}
	if prompt == "" {
		return "", fmt.Errorf("the prompt is empty once the template is filled in")
	}
	return prompt, nil
}

func variantData(item Item) map[string]any {
	prompt := primaryPrompt(item)
	data := map[string]any{
		"ID":          item.ID,
		"ItemID":      item.ID,
		"Type":        item.Type,
		"Prompt":      prompt,
		"Question":    item.Question,
		"Query":       item.Query,
		"Intent":      item.Intent,
		"Locale":      item.Locale,
		"Brand":       item.Brand,
		"Competitors": item.Competitors,
		"Answers":     item.Answers,
		"BaseItem":    item.BaseItem,
		"Tags":        item.Tags,
		"Dimensions":  item.Dimensions,
		"id":          item.ID,
		"item_id":     item.ID,
		"type":        item.Type,
		"prompt":      prompt,
		"question":    item.Question,
		"query":       item.Query,
		"intent":      item.Intent,
		"locale":      item.Locale,
		"brand":       item.Brand,
		"competitors": item.Competitors,
		"answers":     item.Answers,
		"base_item":   item.BaseItem,
		"tags":        item.Tags,
		"dimensions":  item.Dimensions,
	}
	return data
}

func benchmarkVariantData(variant Variant, item Item) map[string]any {
	return map[string]any{
		"ID":                  item.ID,
		"ItemID":              item.ID,
		"Type":                item.Type,
		"Question":            item.Question,
		"VariantID":           variant.ID,
		"Tags":                item.Tags,
		"Dimensions":          item.Dimensions,
		"ComparisonGroupID":   item.ComparisonGroupID,
		"id":                  item.ID,
		"item_id":             item.ID,
		"type":                item.Type,
		"question":            item.Question,
		"variant_id":          variant.ID,
		"tags":                item.Tags,
		"dimensions":          item.Dimensions,
		"comparison_group_id": item.ComparisonGroupID,
	}
}

func isAuditSuiteType(value string) bool {
	return value == "censorship" || value == "propaganda"
}

func primaryPrompt(item Item) string {
	for _, value := range []string{item.Prompt, item.Question, item.Query} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
