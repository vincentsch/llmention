package run

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/engine"
)

func TestExecuteKeepsAnswersInPlanOrder(t *testing.T) {
	first := engine.Answer{
		Text:      "first",
		Citations: []engine.Citation{{URL: "https://example.com/a", Domain: "example.com", Title: "A"}},
		ToolCalls: []engine.ToolCall{{Type: "web_search", Query: "first", Status: "completed"}},
	}
	eng := newCountingEngine("mock")
	eng.answersByItem = map[string]engine.Answer{"item-1": first, "item-2": {Text: "second"}}
	raw, err := Execute(context.Background(), manualPlan(2, 2, 10), []engine.Engine{eng}, ExecuteOptions{RunID: "run-test", Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if raw.CompletedWorkUnits != 2 || raw.RequestsSent != 2 {
		t.Fatalf("run = %+v", raw)
	}
	if raw.Results[0].WorkUnit.ItemID != "item-1" || !reflect.DeepEqual(*raw.Results[0].Answer, first) {
		t.Fatalf("first result = %+v", raw.Results[0])
	}
}

func TestExecuteKeepsUnfinishedAnswersApartWithoutRetrying(t *testing.T) {
	eng := newCountingEngine("mock")
	eng.answersByItem = map[string]engine.Answer{"item-1": {Text: "Helicone is", StopReason: "max_tokens", Unfinished: true}}
	raw, err := Execute(context.Background(), manualPlan(1, 2, 10), []engine.Engine{eng}, ExecuteOptions{Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	r := raw.Results[0]
	if r.Status != ResultStatusUnfinished || r.Attempts != 1 || r.Answer == nil || r.Answer.Text != "Helicone is" {
		t.Fatalf("result = %+v", r)
	}
	if raw.UnfinishedWorkUnits != 1 || raw.CompletedWorkUnits != 0 {
		t.Fatalf("run = %+v", raw)
	}
}

func TestExecuteRetriesTransientErrorsWithBackoff(t *testing.T) {
	longWait := 10 * time.Hour
	eng := newCountingEngine("mock")
	eng.errors = []error{
		&engine.ProviderError{Class: engine.ErrorClassTransient, SafeMessage: "overloaded", Retryable: true},
		&engine.ProviderError{Class: engine.ErrorClassTransient, SafeMessage: "rate limited", StatusCode: 429, RetryAfter: &longWait},
	}
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	raw, err := Execute(context.Background(), manualPlan(1, 2, 10), []engine.Engine{eng}, ExecuteOptions{Now: fixedNow(), Sleep: sleep})
	if err != nil {
		t.Fatal(err)
	}
	r := raw.Results[0]
	if r.Status != ResultStatusSuccess || r.Attempts != 3 || r.Error != nil {
		t.Fatalf("result = %+v", r)
	}
	if len(waits) != 2 {
		t.Fatalf("waits = %v", waits)
	}
	// Without Retry-After the first wait is about a second, plus a random part.
	if waits[0] < time.Second || waits[0] > 1500*time.Millisecond {
		t.Fatalf("first backoff = %v", waits[0])
	}
	// A huge Retry-After is capped so one header cannot stall the run.
	if waits[1] != maxRetryWait {
		t.Fatalf("capped Retry-After = %v, want %v", waits[1], maxRetryWait)
	}
}

func TestExecuteDoesNotRetryCallerErrors(t *testing.T) {
	eng := newCountingEngine("mock")
	eng.errors = []error{&engine.ProviderError{Class: engine.ErrorClassCaller, SafeMessage: "bad request", StatusCode: 400}}
	raw, err := Execute(context.Background(), manualPlan(2, 2, 10), []engine.Engine{eng}, ExecuteOptions{Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Results[0].Status != ResultStatusError || raw.Results[0].Attempts != 1 || raw.Results[1].Status != ResultStatusSuccess {
		t.Fatalf("results = %+v", raw.Results)
	}
	if !strings.Contains(raw.Results[0].Error.Message, "bad request") {
		t.Fatalf("error = %+v", raw.Results[0].Error)
	}
}

func TestExecuteStopsAtTheRequestBudgetCountingRetries(t *testing.T) {
	eng := newCountingEngine("mock")
	for range 10 {
		eng.errors = append(eng.errors, &engine.ProviderError{Class: engine.ErrorClassTransient, Retryable: true, SafeMessage: "down"})
	}
	plan := manualPlan(3, 2, 4)
	plan.Concurrency = 1
	noSleep := func(context.Context, time.Duration) error { return nil }
	raw, err := Execute(context.Background(), plan, []engine.Engine{eng}, ExecuteOptions{Now: fixedNow(), Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	if raw.RequestsSent != 4 || eng.callCount() != 4 || !raw.BudgetExhausted {
		t.Fatalf("sent %d, engine saw %d, exhausted %v", raw.RequestsSent, eng.callCount(), raw.BudgetExhausted)
	}
	if raw.NotStartedWorkUnits != 1 {
		t.Fatalf("not started = %d, want 1: %+v", raw.NotStartedWorkUnits, raw.Results)
	}
}

func TestExecuteRejectsAPlanLargerThanTheBudget(t *testing.T) {
	eng := newCountingEngine("mock")
	_, err := Execute(context.Background(), manualPlan(3, 0, 2), []engine.Engine{eng}, ExecuteOptions{})
	var budget BudgetError
	if !errors.As(err, &budget) || eng.callCount() != 0 {
		t.Fatalf("err = %v, calls = %d", err, eng.callCount())
	}
}

func TestExecuteKeepsFinishedAnswersWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eng := newCountingEngine("mock")
	var once sync.Once
	eng.onAsk = func(req engine.Request) {
		if req.ItemID == "item-3" {
			once.Do(cancel)
		}
	}
	plan := manualPlan(6, 0, 10)
	plan.Concurrency = 1
	raw, err := Execute(ctx, plan, []engine.Engine{eng}, ExecuteOptions{Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	if !raw.Interrupted || raw.CompletedWorkUnits != 2 || raw.CancelledWorkUnits != 1 || raw.NotStartedWorkUnits != 3 {
		t.Fatalf("run = %+v", raw)
	}
	if raw.Results[0].Answer == nil || raw.Results[0].Answer.Text != "ok" {
		t.Fatalf("finished answer lost: %+v", raw.Results[0])
	}
}

func TestExecuteReportsProgress(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	progress := func(done, total int, _ Result) {
		mu.Lock()
		seen = append(seen, done)
		mu.Unlock()
		if total != 3 {
			t.Errorf("total = %d", total)
		}
	}
	if _, err := Execute(context.Background(), manualPlan(3, 0, 10), []engine.Engine{newCountingEngine("mock")}, ExecuteOptions{Progress: progress}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("progress calls = %v", seen)
	}
}

type countingEngine struct {
	id            string
	answersByItem map[string]engine.Answer
	errors        []error
	onAsk         func(engine.Request)
	mu            sync.Mutex
	calls         int
}

func newCountingEngine(id string) *countingEngine { return &countingEngine{id: id} }

func (e *countingEngine) ID() string       { return e.id }
func (e *countingEngine) Provider() string { return "mock" }
func (e *countingEngine) Model() string    { return "fixture" }
func (e *countingEngine) Capabilities() engine.CapabilityProfile {
	return engine.CapabilityProfile{Provider: "mock", Model: "fixture", SupportsMemory: true, SupportsSearchAuto: true}
}
func (e *countingEngine) ValidateRequest(engine.Request) error { return nil }

func (e *countingEngine) Ask(ctx context.Context, req engine.Request) (engine.Answer, error) {
	e.mu.Lock()
	index := e.calls
	e.calls++
	onAsk := e.onAsk
	e.mu.Unlock()
	if onAsk != nil {
		onAsk(req)
	}
	if err := ctx.Err(); err != nil {
		return engine.Answer{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if index < len(e.errors) && e.errors[index] != nil {
		return engine.Answer{}, e.errors[index]
	}
	if answer, ok := e.answersByItem[req.ItemID]; ok {
		return answer, nil
	}
	return engine.Answer{Text: "ok"}, nil
}

func (e *countingEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func manualPlan(workUnits, retries, budget int) *Plan {
	plan := &Plan{
		MaxRequests:            budget,
		Retries:                retries,
		MaxRequestsWithRetries: workUnits * (1 + retries),
		Concurrency:            4,
		engineDefaults:         map[string]defaults{"mock": {}},
		engineIdentities:       map[string]identity{"mock": {provider: "mock", model: "fixture"}},
		Suites:                 []SuitePlan{{ID: "suite.basic", Version: 1, Type: "visibility", Modes: []string{"memory"}, Repetitions: 1}},
	}
	for i := range workUnits {
		plan.WorkUnits = append(plan.WorkUnits, WorkUnit{
			SuiteID: "suite.basic", SuiteVersion: 1, ItemID: fmt.Sprintf("item-%d", i+1), VariantID: "default",
			Mode: engine.ToolPolicyMemory, EngineID: "mock", Repetition: 1, Prompt: "prompt",
		})
	}
	return plan
}

func fixedNow() func() time.Time {
	when := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return when }
}
