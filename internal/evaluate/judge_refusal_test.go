package evaluate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/audit"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
)

type syntheticJudgeTransport func(*http.Request) (*http.Response, error)

func (f syntheticJudgeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedJudgeHandler struct {
	JudgeTaskHandler
	parses int
}

func (h *countedJudgeHandler) ParseAndValidate(text string, input json.RawMessage) (JudgeParsedOutput, []EvaluationWarning, error) {
	h.parses++
	return h.JudgeTaskHandler.ParseAndValidate(text, input)
}

func TestJudgeProviderRefusalBeforeAllHandlersAndWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		handler       JudgeTaskHandler
		input, output string
	}{
		{"framing", framingTaskHandler{}, `{"evaluation_id":"synthetic","target_answer":"It is present."}`, `{"label":"positive","phrase":"synthetic","evidence":[]}`},
		{"recognition", newRecognitionTaskHandler(), `{"results":[{"parsed_result_id":"synthetic-one"}]}`, `{"decisions":[{"parsed_result_id":"synthetic-one","classification":"unknown_namesake","reason":"synthetic"}]}`},
		{"coverage", newClaimCoverageJudgeHandler(nil), `{"evaluation_id":"synthetic","answer_text":"It is present.","expected_claims":[{"claim_id":"fact","text":"fact is present"}],"forbidden_failures":[{"claim_id":"wrong","text":"wrong claim"}]}`, `{"answer_behavior":"answered","expected_claims":[{"claim_id":"fact","status":"present","evidence":[{"byte_start":0,"byte_end":14}]}],"forbidden_failures":[{"claim_id":"wrong","status":"absent","evidence":[]}]}`},
		{"stance", newClaimStanceJudgeHandler(nil), `{"evaluation_id":"synthetic","answer_text":"It is present.","expected_corrections":[{"claim_id":"fact","text":"fact is present"}],"forbidden_claims":[{"claim_id":"wrong","text":"wrong claim"}]}`, `{"stance":"corrects","false_premise_repeated":false,"expected_corrections":[{"claim_id":"fact","status":"present","evidence":[]}],"forbidden_claims":[{"claim_id":"wrong","status":"absent","evidence":[]}]}`},
	} {
		for _, stop := range []string{"end_turn", "refusal"} {
			t.Run(tc.name+"/"+stop, func(t *testing.T) {
				calls, waits := 0, 0
				transport := syntheticJudgeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Host != "synthetic.invalid" {
						t.Fatalf("unexpected endpoint %s", r.URL.Host)
					}
					body, err := json.Marshal(map[string]any{"id": "synthetic-response", "model": "synthetic", "stop_reason": stop, "content": []map[string]string{{"type": "text", "text": tc.output}}})
					if err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})
				factory := engine.NewFactory(engine.FactoryOptions{Transport: transport, BaseURLs: map[string]string{"anthropic": "https://synthetic.invalid/v1"}, LookupEnv: func(string) (string, bool) { return "synthetic-judge-refusal-test-sentinel", true }})
				judge, err := factory.NewJudgeEngine(&config.JudgeConfig{Provider: "anthropic", Model: "synthetic", KeyEnv: "SYNTHETIC_JUDGE_TEST", AllowAnswerDisclosure: true, Capabilities: config.Capabilities{"supports_structured_output": true}})
				if err != nil {
					t.Fatal(err)
				}
				handler := &countedJudgeHandler{JudgeTaskHandler: tc.handler}
				task := JudgeTask{ID: "synthetic", EvaluationID: "synthetic", TemplateID: handler.TemplateID(), TemplateVersion: handler.TemplateVersion(), RequestMode: "memory", DisclosedInput: json.RawMessage(tc.input), OutputSchema: handler.OutputSchema(), RenderedPrompt: "Synthetic judge test"}
				results, err := ExecuteJudgeTasks(context.Background(), judge, []JudgeTask{task}, handler, JudgeExecuteOptions{MaxConcurrency: 1, MaxRetries: 2, Now: fixedEvalNow(), Sleep: func(context.Context, time.Duration) error { waits++; return nil }})
				if err != nil {
					t.Fatal(err)
				}
				result := results[0]
				if calls != 1 || waits != 0 || result.Record.Attempts != 1 || len(result.Record.RetryWaitMS) != 0 {
					t.Fatalf("requests=%d waits=%d result=%+v", calls, waits, result)
				}
				if stop == "refusal" {
					if result.Status != EvaluationStatusError || result.ReasonCode != ReasonJudgeError || result.Record.Error == nil || handler.parses != 0 || len(result.Record.ParsedObject) != 0 {
						t.Fatalf("refusal accepted: %+v parses=%d", result, handler.parses)
					}
				} else if result.Status != EvaluationStatusCompleted || handler.parses != 1 {
					t.Fatalf("accepted control failed: %+v parses=%d", result, handler.parses)
				}
			})
		}
	}
}

func TestCoverageJudgeRefusalRetainsDeterministicCompletedEvaluation(t *testing.T) {
	valid := `{"answer_behavior":"answered","expected_claims":[{"claim_id":"fact","status":"present","evidence":[{"byte_start":0,"byte_end":14}]}],"forbidden_failures":[{"claim_id":"wrong","status":"absent","evidence":[]}]}`
	ev := evaluateAuditSuiteWithJudge(t, coverageAuditSuiteYAML(), engine.Answer{Text: "It is present."}, engine.Answer{Text: valid, StopReason: "refusal"})
	record := onlyAuditEvaluation(t, ev, EvaluatorClaimCoverage)
	cell := record.Payload.(audit.ClaimCoverageCell)
	if record.Status != EvaluationStatusCompleted || record.Error != nil || record.JudgeRecord == nil || record.JudgeRecord.Error == nil || record.ReasonCode != ReasonJudgeError || cell.MatchingMethod != audit.MatchingMethodDeterministic || cell.ClaimJudgments[0].EffectiveStatus != audit.ClaimStatusMissing || !slices.Contains(cell.Warnings, audit.WarningJudgeProviderError) {
		t.Fatalf("fallback lost deterministic evidence: %+v cell=%+v", record, cell)
	}
}
