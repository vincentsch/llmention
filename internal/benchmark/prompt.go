package benchmark

import (
	"bytes"
	"encoding/json"
	"strings"
)

const memoryModeInstruction = "No tools are available. Do not imply that you searched, looked up, browsed, or verified anything externally."

func BuildPrompt(question, framing, mode string) string {
	var b strings.Builder
	if mode == "memory" {
		b.WriteString(memoryModeInstruction)
		b.WriteString("\n")
	}
	framing = strings.TrimSpace(framing)
	if framing != "" {
		b.WriteString(framing)
		b.WriteString("\n")
	}
	b.WriteString("Question: ")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\n")
	b.WriteString("Return exactly one JSON object that matches ")
	b.WriteString(OutputSchemaName)
	b.WriteString(":\n")
	b.WriteString(`{"answer":"short answer or null","confidence":0}`)
	b.WriteString("\n\n")
	b.WriteString("Use null for answer when you do not know. If you include confidence, use an integer from 0 to 100. Do not include explanations, markdown, citations, or additional fields.")
	return strings.TrimSpace(b.String())
}

func CanonicalSchemaJSON() []byte {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{
				"type": []string{"string", "null"},
			},
			"confidence": map[string]any{
				"type":    []string{"integer", "null"},
				"minimum": 0,
				"maximum": 100,
			},
		},
	}
	data, _ := json.Marshal(schema)
	var buf bytes.Buffer
	_ = json.Indent(&buf, data, "", "  ")
	return buf.Bytes()
}
