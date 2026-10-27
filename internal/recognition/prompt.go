package recognition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const MemoryInstruction = "No tools are available. Do not imply that you searched, looked up, browsed, or verified anything externally. If the name is ambiguous, return distinct likely people, organizations, products, projects, or entities rather than blending them. Do not invent entries to fill the list. Return only JSON."

func BuildPrompt(query, variantFraming, mode string) string {
	var b strings.Builder
	if mode == "memory" {
		b.WriteString(MemoryInstruction)
		b.WriteString("\n\n")
	}
	b.WriteString("Identify the entity or entities that this query could refer to.\n")
	b.WriteString("Query JSON: ")
	queryJSON, err := json.Marshal(query)
	if err != nil {
		queryJSON = []byte(`""`)
	}
	b.Write(queryJSON)
	b.WriteString("\n\n")
	b.WriteString("If the query is ambiguous, return distinct likely people, organizations,\n")
	b.WriteString("products, projects, libraries, domains, concepts, or other entities rather\n")
	b.WriteString("than blending them. Do not invent entries to fill the list.\n\n")
	if strings.TrimSpace(variantFraming) != "" {
		b.WriteString(strings.TrimSpace(variantFraming))
		b.WriteString("\n\n")
	}
	b.WriteString("Return only JSON that matches this schema.\n")
	b.WriteString("Output schema name: ")
	b.WriteString(OutputSchemaName)
	b.WriteString("\n")
	b.WriteString("Canonical JSON Schema:\n")
	b.Write(CanonicalSchemaJSON())
	return b.String()
}

func CanonicalSchemaJSON() []byte {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"results"},
		"properties": map[string]any{
			"results": map[string]any{
				"type":     "array",
				"minItems": 0,
				"maxItems": 10,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"rank", "name", "type", "confidence", "snippet"},
					"properties": map[string]any{
						"rank": map[string]any{
							"type":    "integer",
							"minimum": 1,
						},
						"name": map[string]any{
							"type":      "string",
							"minLength": 1,
							"maxLength": 160,
						},
						"type": map[string]any{
							"type": "string",
							"enum": []string{
								EntityTypePerson,
								EntityTypeCompany,
								EntityTypeOrganization,
								EntityTypeProduct,
								EntityTypeProject,
								EntityTypeLibrary,
								EntityTypeDomain,
								EntityTypeConcept,
								EntityTypeUnknown,
							},
						},
						"confidence": map[string]any{
							"type":    "integer",
							"minimum": 0,
							"maximum": 100,
						},
						"snippet": map[string]any{
							"type":      "string",
							"minLength": 1,
							"maxLength": 320,
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return []byte(`{"type":"object","additionalProperties":false,"required":["results"],"properties":{"results":{"type":"array","minItems":0,"maxItems":10}}}`)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return data
	}
	return buf.Bytes()
}

func OutputSchemaFields() (name, format string, strict bool, schemaJSON []byte) {
	return OutputSchemaName, "json_schema", false, CanonicalSchemaJSON()
}

func ValidatePromptMode(mode string) error {
	switch mode {
	case "memory", "provider_default", "search_auto", "search_required", "search_cache_only":
		return nil
	default:
		return fmt.Errorf("unknown recognition mode %q", mode)
	}
}
