package run

import (
	"errors"
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/suite"
)

const plannerProject = `
version: 1
brand: {name: Acme, category: invoicing, domains: [acme.example]}
competitors: [{name: Globex, domains: [globex.example]}]
engines:
  - {id: mock-all, provider: mock, model: demo}
  - {id: mock-memory, provider: mock, model: demo, capabilities: {supports_search_auto: false}}
  - {id: chatgpt, provider: openai, model: gpt-test, key_env: OPENAI_API_KEY}
prompts:
  - Best invoicing tools?
  - Cheapest invoicing tool?
modes: [memory, search]
repetitions: 2
`

func plannerSetup(t *testing.T, yaml string) (*config.Project, suite.Catalog, []engine.Engine) {
	t.Helper()
	project, err := config.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := suite.LoadCatalog(project)
	if err != nil {
		t.Fatal(err)
	}
	engines, err := engine.NewFactory(engine.FactoryOptions{
		LookupEnv: func(name string) (string, bool) {
			t.Fatalf("planning read the key %s", name)
			return "", false
		},
	}).NewTargetEngines(project)
	if err != nil {
		t.Fatal(err)
	}
	return project, catalog, engines
}

func TestBuildPlanExpandsPromptsByModeEngineAndRepetition(t *testing.T) {
	project, catalog, engines := plannerSetup(t, plannerProject)
	plan, err := BuildPlan(project, catalog, engines, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 2 prompts x 2 modes x 3 engines x 2 repetitions, minus mock-memory's
	// 4 search requests.
	if got := len(plan.WorkUnits); got != 20 {
		t.Fatalf("work units = %d, want 20", got)
	}
	if plan.MaxRequestsWithRetries != 60 || plan.MaxRequests != config.DefaultMaxRequests {
		t.Fatalf("bounds = %d / %d", plan.MaxRequestsWithRetries, plan.MaxRequests)
	}
	if len(plan.UnsupportedPairs) != 1 || plan.UnsupportedPairs[0].EngineID != "mock-memory" || plan.UnsupportedPairs[0].WorkUnitsSkipped != 4 {
		t.Fatalf("unsupported = %+v", plan.UnsupportedPairs)
	}
	if !strings.Contains(plan.UnsupportedPairs[0].Reason, "web search") {
		t.Fatalf("reason should name the mode in words: %q", plan.UnsupportedPairs[0].Reason)
	}
	first := plan.WorkUnits[0]
	if first.SuiteID != config.PromptsSuiteID || first.Prompt != "Best invoicing tools?" || first.Mode != engine.ToolPolicyMemory {
		t.Fatalf("first unit = %+v", first)
	}
	if len(plan.RequiredKeyEnv) != 1 || plan.RequiredKeyEnv[0] != "OPENAI_API_KEY" {
		t.Fatalf("keys = %v", plan.RequiredKeyEnv)
	}
}

func TestBuildPlanFiltersEnginesAndModes(t *testing.T) {
	project, catalog, engines := plannerSetup(t, plannerProject)
	plan, err := BuildPlan(project, catalog, engines, PlanOptions{EngineIDs: []string{"mock-all"}, Modes: []engine.ToolPolicy{engine.ToolPolicySearchAuto}, Repetitions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.WorkUnits) != 2 || len(plan.RequiredKeyEnv) != 0 {
		t.Fatalf("plan = %d units, keys %v", len(plan.WorkUnits), plan.RequiredKeyEnv)
	}
	_, err = BuildPlan(project, catalog, engines, PlanOptions{EngineIDs: []string{"gemini"}})
	if err == nil || !strings.Contains(err.Error(), "mock-all, mock-memory, chatgpt") {
		t.Fatalf("unknown engine error = %v", err)
	}
}

func TestBuildPlanExplainsMissingPrompts(t *testing.T) {
	project, catalog, engines := plannerSetup(t, "version: 1\nengines: [{id: m, provider: mock, model: demo}]\n")
	_, err := BuildPlan(project, catalog, engines, PlanOptions{})
	if err == nil || !strings.Contains(err.Error(), "no prompts") {
		t.Fatalf("err = %v", err)
	}
	if _, err := BuildPlan(project, catalog, engines, PlanOptions{SuiteIDs: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), "suites list") {
		t.Fatalf("unknown suite err = %v", err)
	}
}

func TestCheckBudgetCountsFirstAttempts(t *testing.T) {
	project, catalog, engines := plannerSetup(t, plannerProject)
	plan, err := BuildPlan(project, catalog, engines, PlanOptions{MaxRequests: 19})
	if err != nil {
		t.Fatal(err)
	}
	var budget BudgetError
	if err := CheckBudget(plan); !errors.As(err, &budget) || !strings.Contains(err.Error(), "--max-requests 20") {
		t.Fatalf("CheckBudget = %v", err)
	}
	plan.MaxRequests = 20
	if err := CheckBudget(plan); err != nil {
		t.Fatalf("a plan whose first attempts fit must pass, retries come out of what is left: %v", err)
	}
}

func TestBuildPlanBuildsRecognitionPromptsWithSchema(t *testing.T) {
	project, _, engines := plannerSetup(t, plannerProject)
	s := &suite.Suite{
		ID: "recognition.local", Version: 1, Type: "recognition", ContextMode: suite.ContextModeItemLocal,
		DefaultModes: []string{"memory"}, Repetitions: 1,
		Evaluators: []suite.EvaluatorDeclaration{{ID: "cluster", Type: "entity_cluster"}},
		Metrics:    []suite.MetricDeclaration{{ID: "recognition.target_recognition_rate", Evaluator: "cluster", Kind: "rate"}},
		Items: []suite.Item{{ID: "acme", Type: "recognition.entity", Query: "Acme", Privacy: "local_only",
			ExpectedEntities: []suite.ExpectedEntity{{CanonicalName: "Acme", EntityType: "product"}}}},
	}
	catalog, err := suite.NewCatalog([]suite.LoadedSuite{{Suite: s, Source: suite.SourceUser}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(project, catalog, engines[:1], PlanOptions{SuiteIDs: []string{"recognition.local"}})
	if err != nil {
		t.Fatal(err)
	}
	unit := plan.WorkUnits[0]
	if !strings.Contains(unit.Prompt, recognition.MemoryInstruction) || unit.OutputSchema == nil || unit.OutputSchema.Name != recognition.OutputSchemaName {
		t.Fatalf("recognition unit = %+v", unit)
	}
}
