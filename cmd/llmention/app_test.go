package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	rungrad "github.com/vincentsch/rungrad"
	"github.com/vincentsch/rungrad/testutil"
	rgupdate "github.com/vincentsch/rungrad/update"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/report"
	"github.com/vincentsch/llmention/internal/store"
)

const mockProject = `version: 1
brand: {name: Acme Books, category: invoicing for freelancers, domains: [acme-books.example]}
competitors:
  - {name: Globex Billing, domains: [globex.example]}
  - {name: Initech Invoices}
engines:
  - {id: chatgpt-demo, provider: mock, model: demo}
  - {id: claude-demo, provider: mock, model: demo}
prompts:
  - What are the best tools for invoicing for freelancers?
  - Which invoicing app would you recommend to a freelancer?
  - What are good alternatives to Acme Books?
modes: [memory, search]
`

// noKeys makes sure no test reads a real key from the environment.
func noKeys(string) (string, bool) { return "", false }

func inTempProject(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if yaml != "" {
		if err := os.WriteFile("llmention.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runApp(t *testing.T, args ...string) testutil.Result {
	t.Helper()
	return testutil.RunWith(newApp(), testutil.Options{LookupEnv: noKeys}, args...)
}

func TestDryRunExplainsThePlanWithoutSending(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "run", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s %s", res.Exit, res.Stdout, res.Stderr)
	}
	for _, want := range []string{"Dry run: nothing was sent", "Would send 12 requests: 3 prompts to 2 engines, in no web search and web search modes.", "Estimated cost: nothing, because these engines are free."} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("dry run misses %q:\n%s", want, res.Stdout)
		}
	}
	if _, err := os.Stat(".llmention"); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the history directory")
	}
	res = runApp(t, "run", "--dry-run", "--json")
	var plan planPreview
	if err := json.Unmarshal([]byte(res.Stdout), &plan); err != nil {
		t.Fatalf("dry run JSON: %v\n%s", err, res.Stdout)
	}
	if !plan.DryRun || plan.Requests != 12 || plan.WorstCase != 36 || !plan.FitsBudget || !plan.EstimatedCost.Free {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDryRunNotesStandApartFromTheEngineList(t *testing.T) {
	inTempProject(t, strings.Replace(mockProject, ", domains: [acme-books.example]}", "}", 1))
	res := runApp(t, "run", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "6 requests\n\nNote: add brand.domains") {
		t.Fatalf("exit %d:\n%s", res.Exit, res.Stdout)
	}
}

func TestDryRunNamesProblemsThatStopTheRun(t *testing.T) {
	inTempProject(t, mockProject+"analysis:\n  model_assisted_enabled: true\n  judge: {provider: openai, model: gpt-6-luna, key_env: OPENAI_API_KEY}\n")
	res := runApp(t, "run", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "The run would not start: the AI grader reads the answers it grades") || strings.Contains(res.Stdout, "Note: the AI grader") {
		t.Fatalf("exit %d:\n%s", res.Exit, res.Stdout)
	}
	var plan planPreview
	res = runApp(t, "run", "--dry-run", "--json")
	if err := json.Unmarshal([]byte(res.Stdout), &plan); err != nil || len(plan.Problems) != 1 {
		t.Fatalf("plan problems = %v (%v)", plan.Problems, err)
	}
	if res := runApp(t, "run"); res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "allow_answer_disclosure") {
		t.Fatalf("the run itself must stop: exit %d %s", res.Exit, res.Stderr)
	}
}

func TestDryRunRejectsPlaceholderModels(t *testing.T) {
	inTempProject(t, strings.Replace(mockProject, "{id: claude-demo, provider: mock, model: demo}", "{id: chatgpt, provider: openai, model: replace-with-openai-model, key_env: OPENAI_API_KEY}", 1))
	res := runApp(t, "run", "--dry-run")
	if res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "placeholder model") {
		t.Fatalf("exit %d stderr %s", res.Exit, res.Stderr)
	}
}

func TestDryRunShowsCostAndMissingKeys(t *testing.T) {
	inTempProject(t, strings.Replace(mockProject, "{id: claude-demo, provider: mock, model: demo}", "{id: claude, provider: anthropic, model: claude-sonnet-5-5, key_env: ANTHROPIC_API_KEY}", 1))
	res := runApp(t, "run", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Estimated cost: about $") || !strings.Contains(res.Stdout, "Keys missing: ANTHROPIC_API_KEY") {
		t.Fatalf("dry run:\n%s", res.Stdout)
	}
}

func TestRunSavesAndReportsTheSummary(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "run")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s %s", res.Exit, res.Stdout, res.Stderr)
	}
	for _, want := range []string{"How often assistants mention Acme Books", "Demo data", "Mention rate next to your competitors", "Saved as run"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("run output misses %q:\n%s", want, res.Stdout)
		}
	}
	entries, _, err := store.New(".llmention/runs").List()
	if err != nil || len(entries) != 1 || entries[0].Answers != 12 || len(entries[0].Headline) != 4 {
		t.Fatalf("history = %+v err=%v", entries, err)
	}

	res = runApp(t, "report", "--json")
	var r report.Report
	if err := json.Unmarshal([]byte(res.Stdout), &r); err != nil {
		t.Fatalf("report JSON: %v\n%s", err, res.Stdout)
	}
	if r.Visibility == nil || len(r.Visibility.Cells) != 4 || len(r.Answers) != 12 || !r.Run.Demo {
		t.Fatalf("report = %+v", r.Run)
	}
	// The third prompt names the brand, so each cell counts two answers.
	if got := r.Visibility.Cells[0].Brands[0].Eligible; got != 2 {
		t.Fatalf("eligible answers = %d, want 2", got)
	}
	if len(r.Visibility.PromptsNamingYou) != 1 {
		t.Fatalf("prompts naming you = %v", r.Visibility.PromptsNamingYou)
	}

	for args, want := range map[string]string{
		"report --answers":                    "What are good alternatives to Acme Books?",
		"report --details":                    "visibility.brand_mention_rate",
		"report --format markdown":            "| Engine | Mode | Mentioned |",
		"history list":                        "Mention rate",
		"suites list":                         "prompts",
		"suites validate prompts":             "prompts is valid: visibility suite with 3 prompts",
		"report --html report.html":           "Wrote the report",
		"report --fail-under mention-rate=0%": "PASS",
	} {
		res := runApp(t, strings.Fields(args)...)
		if res.Exit != 0 || !strings.Contains(res.Stdout, want) {
			t.Fatalf("%s: exit %d, missing %q:\n%s\n%s", args, res.Exit, want, res.Stdout, res.Stderr)
		}
	}
	page, err := os.ReadFile("report.html")
	if err != nil || !strings.Contains(string(page), "Acme Books in AI answers") || strings.Contains(string(page), "<script src") {
		t.Fatalf("html report: %v", err)
	}
}

func TestProjectWithoutDomainsStillRuns(t *testing.T) {
	inTempProject(t, `version: 1
brand: {name: Acme Books}
competitors: [{name: Globex Billing}]
engines: [{id: demo, provider: mock, model: demo}]
prompts: ["What are the best tools for invoicing for freelancers?"]
modes: [search]
`)
	res := runApp(t, "run", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "add brand.domains to measure citations of your site") {
		t.Fatalf("dry run without domains: exit %d\n%s\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	res = runApp(t, "run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "How often assistants mention Acme Books") {
		t.Fatalf("run without domains: exit %d\n%s\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	// Nothing was edited, so the report must not claim llmention.yaml
	// changed. It did when a brand without domains was saved as null and
	// read back as an empty list.
	for _, args := range [][]string{{"report"}, {"report", "--answers"}} {
		if res := runApp(t, args...); strings.Contains(res.Stdout, "changed since this run") {
			t.Fatalf("%v claims the project changed:\n%s", args, res.Stdout)
		}
	}
}

func TestOneOffRunsAreNotTheBaseline(t *testing.T) {
	inTempProject(t, mockProject)
	for i, args := range [][]string{{"run"}, {"run", "--engine", "claude-demo", "--mode", "search"}, {"run"}} {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond) // run ids sort by second
		}
		if res := runApp(t, args...); res.Exit != 0 {
			t.Fatalf("%v: %s", args, res.Stderr)
		}
	}
	entries, _, _ := store.New(".llmention/runs").List()
	if len(entries) != 3 || !entries[1].Narrowed || entries[0].Narrowed {
		t.Fatalf("entries = %+v", entries)
	}
	res := runApp(t, "report", "--diff")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "compared with run "+entries[2].ID) || strings.Contains(res.Stdout, "is new in this run") {
		t.Fatalf("the diff did not skip the one-off run:\n%s%s", res.Stdout, res.Stderr)
	}
	// Two run ids compare the older one with the newer one, in either order.
	for _, ids := range [][]string{{entries[2].ID, entries[0].ID}, {entries[0].ID, entries[2].ID}} {
		res := runApp(t, "report", ids[0], ids[1], "--diff")
		if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "Acme Books: run "+entries[0].ID) {
			t.Fatalf("report %v --diff:\n%s%s", ids, res.Stdout, res.Stderr)
		}
	}
	res = runApp(t, "report", "--trend")
	if !strings.Contains(res.Stdout, "over 2 runs") || !strings.Contains(res.Stdout, "Left out 1 run made with --engine or --mode") {
		t.Fatalf("trend:\n%s", res.Stdout)
	}
	// The README's CI job appends this to the GitHub job summary, which
	// renders Markdown, so it must be a real table.
	res = runApp(t, "report", "--trend", "--format", "markdown")
	if !strings.Contains(res.Stdout, "| Run |") || !strings.Contains(res.Stdout, "|---|") {
		t.Fatalf("trend markdown:\n%s", res.Stdout)
	}
	res = runApp(t, "report", "--diff", "--format", "markdown")
	if !strings.Contains(res.Stdout, "| Engine | Mode | Before | Now |") {
		t.Fatalf("diff markdown:\n%s", res.Stdout)
	}
	if res := runApp(t, "report", "--html", "report.html"); res.Exit != 0 {
		t.Fatalf("html: %s", res.Stderr)
	}
	if info, err := os.Stat("report.html"); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("report.html must be private like the runs: %v %v", info.Mode(), err)
	}
}

func TestDiffAndTrendSurviveConfigEdits(t *testing.T) {
	inTempProject(t, mockProject)
	if res := runApp(t, "run"); res.Exit != 0 {
		t.Fatalf("first run: %s", res.Stderr)
	}
	// A new competitor and a new prompt between runs must not turn off the
	// comparison; the shared prompts are compared and the change is named.
	edited := strings.Replace(mockProject, "  - {name: Initech Invoices}", "  - {name: Initech Invoices}\n  - {name: Umbrella Pay}", 1)
	edited = strings.Replace(edited, "prompts:\n", "prompts:\n  - What is the cheapest invoicing tool?\n", 1)
	if err := os.WriteFile("llmention.yaml", []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // run ids sort by second
	if res := runApp(t, "run"); res.Exit != 0 {
		t.Fatalf("second run: %s", res.Stderr)
	}
	res := runApp(t, "report", "--diff")
	if res.Exit != 0 {
		t.Fatalf("diff exit %d: %s", res.Exit, res.Stderr)
	}
	for _, want := range []string{"Compared on the 3 prompts both runs asked", "1 prompt is only in the newer run, so it is left out", "Mention rate of Acme Books", "Umbrella Pay"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("diff misses %q:\n%s", want, res.Stdout)
		}
	}
	res = runApp(t, "report", "--trend")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "over 2 runs") || !strings.Contains(res.Stdout, "prompts changed (1 added, 0 removed)") {
		t.Fatalf("trend exit %d:\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
}

func TestEngineAndModeFilters(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "run", "--engine", "claude-demo", "--mode", "no-search", "--json")
	var out runResult
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || res.Exit != 0 {
		t.Fatalf("exit %d: %v %s", res.Exit, err, res.Stderr)
	}
	if len(out.Report.Answers) != 3 || out.Report.Answers[0].Engine != "claude-demo" || out.Report.Answers[0].Mode != "memory" {
		t.Fatalf("answers = %+v", out.Report.Answers)
	}
	res = runApp(t, "run", "--engine", "gemini")
	if res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "configured engines: chatgpt-demo, claude-demo") {
		t.Fatalf("unknown engine: exit %d %s", res.Exit, res.Stderr)
	}
}

func TestGatesAreCheckedBeforeAnyRequest(t *testing.T) {
	inTempProject(t, mockProject)
	for _, gate := range []string{"mention-rate=140%", "metric:visibility.brand_mention_rat=0.4", "mention-rate[engine=gemini]=10%", "loudness=3"} {
		res := runApp(t, "run", "--fail-under", gate)
		if res.Exit != rungrad.ExitUsage {
			t.Fatalf("%s: exit %d, want usage error: %s", gate, res.Exit, res.Stderr)
		}
	}
	if _, err := os.Stat(".llmention"); !os.IsNotExist(err) {
		t.Fatal("a rejected gate must stop the run before anything is saved")
	}
	res := runApp(t, "run", "--fail-under", "mention-rate=100%", "--fail-under", "metric:visibility.brand_mention_rate[level=suite_mode_engine,engine=chatgpt-demo,mode=memory]=0")
	if res.Exit != exitGateFailed || !strings.Contains(res.Stdout, "FAIL  mention-rate=100%") || !strings.Contains(res.Stdout, "PASS  metric:") {
		t.Fatalf("exit %d:\n%s\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	entries, _, _ := store.New(".llmention/runs").List()
	if len(entries) != 1 || entries[0].Gates != "fail" {
		t.Fatalf("a failed gate still saves the run: %+v", entries)
	}
}

func TestBudgetTooSmallSaysHowToFixIt(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "run", "--max-requests", "5")
	if res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "needs 12 requests but the budget is 5") || !strings.Contains(res.Stderr, "--max-requests 12") {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
}

func TestMissingKeyStopsBeforeSending(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer server.Close()
	inTempProject(t, strings.Replace(mockProject, "{id: claude-demo, provider: mock, model: demo}", "{id: chatgpt, provider: openai, model: gpt-6.1-sol, key_env: OPENAI_API_KEY, base_url: \""+server.URL+"/v1\"}", 1))
	res := runApp(t, "run")
	if res.Exit != rungrad.ExitAuth || !strings.Contains(res.Stderr, "export OPENAI_API_KEY=") || hits != 0 {
		t.Fatalf("exit %d hits %d: %s", res.Exit, hits, res.Stderr)
	}
}

func TestOpenAIBaseURLReachesTheGateway(t *testing.T) {
	var mu sync.Mutex
	var path, auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"resp","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Try Acme Books or Globex Billing."}]}]}`))
	}))
	defer server.Close()
	inTempProject(t, strings.Replace(mockProject, "{id: claude-demo, provider: mock, model: demo}", "{id: gateway, provider: openai, model: gpt-6.1-sol, key_env: GATEWAY_KEY, base_url: \""+server.URL+"/v1\"}", 1))
	res := testutil.RunWith(newApp(), testutil.Options{LookupEnv: func(name string) (string, bool) {
		if name == "GATEWAY_KEY" {
			return "test-gateway-key", true
		}
		return "", false
	}}, "run", "--engine", "gateway", "--mode", "no-search", "--json")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "/v1/responses" || auth != "Bearer test-gateway-key" {
		t.Fatalf("gateway saw %s with %q", path, auth)
	}
	testutil.AssertRedacted(t, res, "test-gateway-key")
}

func TestInterruptedRunKeepsFinishedAnswers(t *testing.T) {
	arrived := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice when the client hangs up.
		_, _ = io.ReadAll(r.Body)
		arrived <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()
	inTempProject(t, strings.Replace(mockProject, "{id: claude-demo, provider: mock, model: demo}", "{id: slow, provider: openai-compatible, model: m, base_url: \""+server.URL+"/v1\"}", 1))
	project, err := config.Load("llmention.yaml")
	if err != nil {
		t.Fatal(err)
	}
	app := newApp()
	f := app.Factory()
	f.LookupEnv = noKeys
	f.Stderr = &strings.Builder{}
	p, err := prepareRun(f, project, "llmention.yaml", runOptions{Modes: []string{"no-search"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		time.Sleep(200 * time.Millisecond) // let the fast mock answers finish
		cancel()
	}()
	outcome, err := execute(f, p, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.interrupted || outcome.card.Counts.Answered != 3 {
		t.Fatalf("interrupted=%v counts=%+v", outcome.interrupted, outcome.card.Counts)
	}
	if _, err := os.Stat(outcome.path); err != nil {
		t.Fatalf("the partial run was not saved: %v", err)
	}
	if exitFor(outcome).(*rungrad.Error).Code != exitInterrupted {
		t.Fatal("an interrupted run exits 130")
	}
	// The engine whose requests were all stopped still has its row and says why
	// it has no numbers.
	var summary strings.Builder
	report.WriteText(&summary, report.Build(outcome.analysis), report.Style{})
	if !regexp.MustCompile(`(?m)^  slow\s+no web search\s+–\s+–\s+–\s+–\s+no answers to count: 3 stopped$`).MatchString(summary.String()) {
		t.Fatalf("summary has no row for the stopped engine:\n%s", summary.String())
	}
}

// chatServer answers Chat Completions requests with whatever reply returns
// for the question asked.
func chatServer(t *testing.T, reply func(question string) (status int, body string)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		question := ""
		if n := len(req.Messages); n > 0 {
			question = req.Messages[n-1].Content
		}
		status, body := reply(question)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func chatAnswer(text, finish string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}, "finish_reason": finish}}})
	return string(b)
}

func localEngineProject(url string) string {
	return `version: 1
brand: {name: Acme Books}
competitors: [{name: Globex Billing}]
engines:
  - {id: local, provider: openai-compatible, model: m, base_url: "` + url + `/v1"}
prompts:
  - What are the best tools for invoicing for freelancers?
  - Which invoicing app would you recommend to a freelancer?
modes: [memory]
execution: {retries: 0}
`
}

func TestUnfinishedAnswersDoNotFailTheRunButFailedRequestsDo(t *testing.T) {
	server := chatServer(t, func(q string) (int, string) {
		if strings.Contains(q, "recommend") {
			return http.StatusOK, chatAnswer("Globex Billing is a good", "length")
		}
		return http.StatusOK, chatAnswer("1. Acme Books\n2. Globex Billing", "stop")
	})
	inTempProject(t, localEngineProject(server.URL))
	res := runApp(t, "run")
	if res.Exit != 0 {
		t.Fatalf("a run with an unfinished answer exits %d, want 0: %s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "1 answer was cut off before it finished") {
		t.Fatalf("the summary has no note about the unfinished answer:\n%s", res.Stdout)
	}
	entries, _, err := store.New(".llmention/runs").List()
	if err != nil || len(entries) != 1 || entries[0].Status != "complete" {
		t.Fatalf("saved runs = %+v, %v; want one complete run", entries, err)
	}

	failing := chatServer(t, func(q string) (int, string) {
		if strings.Contains(q, "recommend") {
			return http.StatusBadRequest, `{"error":{"message":"bad request"}}`
		}
		return http.StatusOK, chatAnswer("Globex Billing is a good", "length")
	})
	inTempProject(t, localEngineProject(failing.URL))
	res = runApp(t, "run")
	if res.Exit != rungrad.ExitAPI || !strings.Contains(res.Stderr, "1 of 2 requests got no answer") {
		t.Fatalf("a run with a failed request exits %d, want %d: %s", res.Exit, rungrad.ExitAPI, res.Stderr)
	}
}

func TestRecognitionReportSaysWhatEachAssistantKnew(t *testing.T) {
	server := chatServer(t, func(q string) (int, string) {
		switch {
		case strings.Contains(q, `"Acme Books"`):
			return http.StatusOK, chatAnswer(`{"results":[{"rank":1,"name":"Acme Bookshop","type":"organization","confidence":60,"snippet":"A bookshop in Leeds."}]}`, "stop")
		case strings.Contains(q, `"Acme Ledger"`):
			return http.StatusOK, chatAnswer("I am not sure what that is.", "stop")
		}
		return http.StatusOK, chatAnswer(`{"results":[{"rank":1,"name":"Jane Doe","type":"person","confidence":80,"snippet":"Founder of Acme Books."}]}`, "stop")
	})
	inTempProject(t, `version: 1
brand: {name: Acme Books, products: [Acme Ledger], people: [Jane Doe]}
engines:
  - {id: local, provider: openai-compatible, model: m, base_url: "`+server.URL+`/v1"}
prompts: ["Which invoicing app is best?"]
`)
	res := runApp(t, "run", "--suite", "recognition.basic")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	for _, want := range []string{
		`Do assistants know Acme Books\?`,
		`(?m)^  Acme Books\s+brand\s+something else$`,
		`(?m)^  Acme Ledger\s+product\s+unreadable answer$`,
		`(?m)^  Jane Doe\s+person\s+knew it$`,
		`(?m)^  Knew\s+1 of 2$`,
		`without web search`,
		`1 answer was not in the JSON format the question asked for`,
	} {
		if !regexp.MustCompile(want).MatchString(res.Stdout) {
			t.Fatalf("summary does not match %q:\n%s", want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "recognition.") {
		t.Fatalf("summary shows metric ids:\n%s", res.Stdout)
	}

	answers := runApp(t, "report", "--answers")
	if !strings.Contains(answers.Stdout, `Who or what is "Acme Books"?`) || !strings.Contains(answers.Stdout, "1. Acme Bookshop (organization, 60% sure): A bookshop in Leeds.") {
		t.Fatalf("answers are not readable:\n%s", answers.Stdout)
	}
	if strings.Contains(answers.Stdout, "Canonical JSON Schema") || strings.Contains(answers.Stdout, `{"results"`) {
		t.Fatalf("answers show the prompt template or raw JSON:\n%s", answers.Stdout)
	}

	md := runApp(t, "report", "--format", "markdown")
	if !strings.Contains(md.Stdout, "| Acme Books | brand | something else |") {
		t.Fatalf("markdown:\n%s", md.Stdout)
	}
	if res := runApp(t, "report", "--html", "report.html"); res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	page, err := os.ReadFile("report.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<h2>Do assistants know Acme Books?</h2>") || strings.Contains(string(page), "Canonical JSON Schema") {
		t.Fatalf("HTML has no readable recognition section")
	}

	var parsed struct {
		Recognition []report.Recognition `json:"recognition"`
	}
	out := runApp(t, "report", "--json")
	if err := json.Unmarshal([]byte(out.Stdout), &parsed); err != nil || len(parsed.Recognition) != 1 {
		t.Fatalf("JSON recognition = %+v, %v\n%s", parsed.Recognition, err, out.Stdout)
	}
	if got := parsed.Recognition[0].Totals[0]; got.Knew != 1 || got.SomethingElse != 1 || got.Unreadable != 1 {
		t.Fatalf("JSON totals = %+v", got)
	}
}

func TestInitWritesARunnableProject(t *testing.T) {
	inTempProject(t, "")
	res := runApp(t, "init", "--no-prompt", "--brand", "Tabslip", "--category", "AI API cost tracking tool", "--competitors", "Helicone (helicone.ai), Langfuse", "--engines", "openai,anthropic")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	project, err := config.Load("llmention.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if project.Brand.Name != "Tabslip" || len(project.Competitors) != 2 || project.Competitors[0].Domains[0] != "helicone.ai" || len(project.Engines) != 2 {
		t.Fatalf("project = %+v", project)
	}
	for _, p := range project.Prompts {
		if strings.Contains(p.Text, "Tabslip") {
			t.Fatalf("starter prompt names the brand: %q", p.Text)
		}
	}
	if !strings.Contains(res.Stdout, "export OPENAI_API_KEY=") {
		t.Fatalf("init should say which keys to export:\n%s", res.Stdout)
	}
	if res := runApp(t, "run", "--dry-run"); res.Exit != 0 {
		t.Fatalf("the starter project must pass a dry run: %s", res.Stderr)
	}
	if res := runApp(t, "init", "--no-prompt", "--brand", "X"); res.Exit != rungrad.ExitUsage {
		t.Fatalf("init must not overwrite without --force: %d", res.Exit)
	}
}

func TestInitAsksQuestionsInATerminal(t *testing.T) {
	inTempProject(t, "")
	answers := "Acme Books\ninvoicing for freelancers\nacme-books.example\nGlobex Billing (globex.example)\nmock\n"
	res := testutil.RunWith(newApp(), testutil.Options{Stdin: strings.NewReader(answers), Terminal: true, TerminalSet: true, LookupEnv: noKeys}, "init")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	project, err := config.Load("llmention.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if project.Brand.Category != "invoicing for freelancers" || project.Brand.Domains[0] != "acme-books.example" || project.Engines[0].Provider != "mock" {
		t.Fatalf("project = %+v %+v", project.Brand, project.Engines)
	}
	if res := runApp(t, "init", "--no-prompt", "--force"); res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "--brand") {
		t.Fatalf("non-interactive init without --brand: %d %s", res.Exit, res.Stderr)
	}
}

func TestInitAsksAgainInsteadOfFailing(t *testing.T) {
	inTempProject(t, "")
	// A site with a path and a provider llmention does not know are asked
	// again; "chatgpt, claude" is what people type for OpenAI and Anthropic.
	answers := "Acme Books\ninvoicing\nhttps://github.com/acme/books\nacme-books.example\n\nbing\nchatgpt, claude\n"
	res := testutil.RunWith(newApp(), testutil.Options{Stdin: strings.NewReader(answers), Terminal: true, TerminalSet: true, LookupEnv: noKeys}, "init")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	project, err := config.Load("llmention.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Engines) != 2 || project.Engines[0].Provider != "openai" || project.Engines[1].Provider != "anthropic" || project.Brand.Domains[0] != "acme-books.example" {
		t.Fatalf("project = %+v %+v", project.Brand, project.Engines)
	}
	if !strings.Contains(res.Stderr, "has a path") || !strings.Contains(res.Stderr, "Please name providers") {
		t.Fatalf("init did not explain the retries:\n%s", res.Stderr)
	}

	// After a refused answer, Enter must take the old default again. It used
	// to offer the refused answer as the default, so Enter repeated it forever.
	if err := os.Remove("llmention.yaml"); err != nil {
		t.Fatal(err)
	}
	answers = "Acme Books\ninvoicing\nhttps://github.com/acme/books\n\n\nbing\n\n"
	res = testutil.RunWith(newApp(), testutil.Options{Stdin: strings.NewReader(answers), Terminal: true, TerminalSet: true, LookupEnv: noKeys}, "init")
	if res.Exit != 0 || strings.Contains(res.Stderr, "[https://github.com/acme/books]") || strings.Contains(res.Stderr, "[bing]") {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	project, err = config.Load("llmention.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Brand.Domains) != 0 || len(project.Engines) != 2 || project.Engines[0].Provider != "openai" {
		t.Fatalf("project = %+v %+v", project.Brand, project.Engines)
	}
}

func TestQuickWritesTheProjectAndRuns(t *testing.T) {
	dir := inTempProject(t, "")
	res := runApp(t, "quick", "--brand", "Acme Books", "--category", "invoicing for freelancers", "--competitors", "Globex Billing,Initech Invoices", "--engines", "mock")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "How often assistants mention Acme Books") {
		t.Fatalf("exit %d:\n%s\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".llmention", "runs", "index.json")); err != nil {
		t.Fatalf("quick did not save the run: %v", err)
	}
	if res := runApp(t, "quick"); res.Exit != 0 {
		t.Fatalf("quick without flags reruns the existing project: %s", res.Stderr)
	}
}

func TestQuickWithoutKeysWritesNothing(t *testing.T) {
	inTempProject(t, "")
	args := []string{"quick", "--brand", "Acme Books", "--category", "invoicing for freelancers", "--engines", "openai"}
	res := runApp(t, args...)
	if res.Exit != rungrad.ExitAuth || !strings.Contains(res.Stderr, "OPENAI_API_KEY") {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	// Writing the file first made the same command fail the second time,
	// because the file then existed.
	if _, err := os.Stat("llmention.yaml"); !os.IsNotExist(err) {
		t.Fatal("quick wrote llmention.yaml although it could not run")
	}
}

func TestDemoNeedsNothing(t *testing.T) {
	inTempProject(t, "")
	res := runApp(t, "demo")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	for _, want := range []string{"Acme Books", "Demo data", "over 4 runs", "Trend"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("demo misses %q:\n%s", want, res.Stdout)
		}
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatalf("demo without --keep must not write here: %v", entries)
	}
	// Nothing was saved, so the demo must not suggest reading a saved run.
	if strings.Contains(res.Stdout, "llmention report ") {
		t.Fatalf("demo suggests a command that cannot work:\n%s", res.Stdout)
	}
	if res := runApp(t, "demo", "--keep", "demo"); res.Exit != 0 {
		t.Fatalf("demo --keep: %s", res.Stderr)
	}
	t.Chdir("demo")
	if res := runApp(t, "report", "--diff"); res.Exit != 0 || !strings.Contains(res.Stdout, "Compared on the 10 prompts") {
		t.Fatalf("diff in kept demo: %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
}

func TestReportWithoutHistoryExplains(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "report")
	if res.Exit != rungrad.ExitNotFound || !strings.Contains(res.Stderr, "llmention run") {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
}

func TestDiscoverSuggestsPromptsWithoutAnAccount(t *testing.T) {
	inTempProject(t, mockProject)
	res := runApp(t, "discover", "--out", "prompts.txt")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "What do most teams use for invoicing for freelancers?") {
		t.Fatalf("exit %d:\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	data, err := os.ReadFile("prompts.txt")
	if err != nil || strings.Contains(string(data), "What are the best tools for invoicing for freelancers?") {
		t.Fatalf("prompts already in llmention.yaml must not be suggested again: %v\n%s", err, data)
	}
	if res := runApp(t, "discover", "--from", "chab"); res.Exit != rungrad.ExitAuth || !strings.Contains(res.Stderr, "CHAB_API_KEY") {
		t.Fatalf("missing chab key: %d %s", res.Exit, res.Stderr)
	}
	if res := runApp(t, "discover", "--from", "chab", "--dry-run"); res.Exit != 0 || !strings.Contains(res.Stdout, "about 25 Chab credits") {
		t.Fatalf("chab dry run: %d %s %s", res.Exit, res.Stdout, res.Stderr)
	}
	// init already writes the template questions, so discover has nothing
	// new and should say why.
	inTempProject(t, "")
	if res := runApp(t, "init", "--brand", "Acme", "--category", "invoicing for freelancers", "--engines", "mock"); res.Exit != 0 {
		t.Fatalf("init: %d %s", res.Exit, res.Stderr)
	}
	if res := runApp(t, "discover"); res.Exit != 0 || !strings.Contains(res.Stdout, `already has the template questions for "invoicing for freelancers"`) {
		t.Fatalf("discover after init: %d %s", res.Exit, res.Stdout)
	}
}

func TestUpdateCheckAnswersInASentence(t *testing.T) {
	for _, tc := range []struct {
		result rgupdate.Result
		want   string
	}{
		{rgupdate.Result{Current: "0.1.0", Latest: "0.1.0", Status: rgupdate.StatusUpToDate}, "llmention 0.1.0 is the latest release."},
		{rgupdate.Result{Current: "0.1.0", Latest: "0.2.0", Status: rgupdate.StatusUpdateAvailable}, "llmention 0.2.0 is out; you have 0.1.0."},
		{rgupdate.Result{Current: "dev", Status: rgupdate.StatusDevelopmentBuild}, "This is a development build (dev)"},
		{rgupdate.Result{Current: "0.3.0", Latest: "0.2.0", Status: rgupdate.StatusNewerThanLatest}, "newer than the latest release (0.2.0)"},
		{rgupdate.Result{Current: "0.1.0", Status: rgupdate.StatusUnknownLatest}, "could not tell which release is the latest"},
	} {
		if got := updateText(tc.result); !strings.Contains(got, tc.want) || strings.Contains(got, "_") {
			t.Errorf("updateText(%s) = %q, want it to contain %q", tc.result.Status, got, tc.want)
		}
	}
}

func TestUsageErrorsSayWhatToDo(t *testing.T) {
	inTempProject(t, mockProject)
	if res := runApp(t, "run"); res.Exit != 0 {
		t.Fatalf("run: %d %s", res.Exit, res.Stderr)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"history", "export", "2"}, "--output run.json"},
		{[]string{"report", "--format", "html"}, "--html report.html"},
		{[]string{"report", "--diff", "--fail-under", "mention-rate=30%"}, "do not work with --trend or --diff"},
		{[]string{"report", "--trend", "--html", "t.html"}, "do not work with --trend or --diff"},
		{[]string{"report", "--answers", "--format", "markdown"}, "not with --answers or --details"},
	} {
		res := runApp(t, tc.args...)
		if res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, tc.want) {
			t.Errorf("%v: exit %d, %s", tc.args, res.Exit, res.Stderr)
		}
	}
}

func TestQuickWithoutACategoryWritesNothing(t *testing.T) {
	inTempProject(t, "")
	res := runApp(t, "quick", "--brand", "Acme", "--engines", "mock")
	if res.Exit != rungrad.ExitUsage || !strings.Contains(res.Stderr, "pass --category") {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat("llmention.yaml"); !os.IsNotExist(err) {
		t.Fatal("quick wrote llmention.yaml although it could not run")
	}
}

func TestInitWithoutACategoryAsksForPrompts(t *testing.T) {
	inTempProject(t, "")
	res := runApp(t, "init", "--brand", "Acme", "--engines", "mock")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "with 0 prompts") || !strings.Contains(res.Stdout, "Add the questions your buyers ask under prompts:") {
		t.Fatalf("exit %d:\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	data, _ := os.ReadFile("llmention.yaml")
	if !strings.Contains(string(data), "#     - What are the best tools") || !strings.Contains(string(data), "prompts: []") {
		t.Fatalf("llmention.yaml:\n%s", data)
	}
	if _, err := config.Load("llmention.yaml"); err != nil {
		t.Fatalf("the file init wrote does not load: %v", err)
	}
}
