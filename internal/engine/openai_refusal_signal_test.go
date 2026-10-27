package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestOpenAIRefusalSignalForStructuredOutputs(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, text  string
		strict, refused, invalid bool
	}{
		{"refusal_json", "refusal", `{"decision":"present"}`, false, true, false},
		{"strict_refusal_json", "refusal", `{"decision":"present"}`, true, true, false},
		{"strict_refusal_prose", "refusal", "I cannot provide that judgment.", true, true, false},
		{"ordinary_json", "output_text", `{"decision":"present"}`, false, false, false},
		{"strict_ordinary_json", "output_text", `{"decision":"present"}`, true, false, false},
		{"strict_invalid_ordinary", "output_text", "Incomplete ordinary output", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			eng := newOpenAIEngineForTest(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				part := map[string]string{"type": tc.contentType}
				if tc.refused {
					part["refusal"] = tc.text
				} else {
					part["text"] = tc.text
				}
				body, err := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{
					"type": "message", "content": []any{part},
				}}})
				if err != nil {
					t.Fatal(err)
				}
				return jsonResponse(http.StatusOK, string(body)), nil
			}), nil, nil)
			req := validRequest(ToolPolicyMemory)
			req.OutputSchema = &OutputSchema{Name: "decision", Format: OutputFormatJSONSchema,
				SchemaJSON: []byte(`{"type":"object"}`), Strict: tc.strict}
			answer, err := eng.Ask(context.Background(), req)
			if tc.invalid {
				var providerErr *ProviderError
				if !errors.As(err, &providerErr) || providerErr.Retryable {
					t.Fatalf("malformed ordinary structured output accepted or retryable: %#v", err)
				}
			} else {
				if err != nil || answer.Text != tc.text {
					t.Fatalf("answer=%q error=%v", answer.Text, err)
				}
				if tc.refused {
					if answer.StopReason != "refusal" || len(answer.Parsed) != 0 {
						t.Fatalf("declared refusal was parsed or lost: stop=%q parsed=%s", answer.StopReason, answer.Parsed)
					}
				} else if answer.StopReason != "completed" || len(answer.Parsed) == 0 {
					t.Fatalf("ordinary output contract changed: stop=%q parsed=%s", answer.StopReason, answer.Parsed)
				}
			}
			if calls != 1 {
				t.Fatalf("calls=%d, want one attempt", calls)
			}
		})
	}
}
