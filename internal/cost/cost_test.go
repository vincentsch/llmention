package cost

import (
	"math"
	"slices"
	"testing"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
)

// liveAnswer is one answer from a live run on 2026-10-29, with the usage the
// provider reported. For OpenRouter, cost is the price it reported.
type liveAnswer struct {
	mode                    engine.ToolPolicy
	prompt                  string
	input, output, searches int
	cost                    float64
}

const (
	bestTools    = "What are the best tools for AI API cost tracking?"
	alternatives = "What are good alternatives to Helicone?"
)

var liveRuns = []struct {
	engine  config.EngineConfig
	answers []liveAnswer
}{
	{config.EngineConfig{ID: "chatgpt", Provider: "openai", Model: "gpt-6.1-sol"}, []liveAnswer{
		{mode: engine.ToolPolicyMemory, prompt: bestTools, input: 17, output: 758},
		{mode: engine.ToolPolicyMemory, prompt: alternatives, input: 14, output: 628},
		{mode: engine.ToolPolicySearchAuto, prompt: bestTools, input: 29561, output: 1086, searches: 2},
		{mode: engine.ToolPolicySearchAuto, prompt: alternatives, input: 33851, output: 925, searches: 3},
		{mode: engine.ToolPolicySearchAuto, prompt: bestTools, input: 38088, output: 1202, searches: 3},
	}},
	{config.EngineConfig{ID: "claude", Provider: "anthropic", Model: "claude-sonnet-5-5"}, []liveAnswer{
		{mode: engine.ToolPolicyMemory, prompt: bestTools, input: 24, output: 1391},
		{mode: engine.ToolPolicyMemory, prompt: alternatives, input: 21, output: 1338},
		{mode: engine.ToolPolicySearchAuto, prompt: bestTools, input: 14239, output: 1398, searches: 1},
		{mode: engine.ToolPolicySearchAuto, prompt: alternatives, input: 13999, output: 1352, searches: 1},
		{mode: engine.ToolPolicySearchAuto, prompt: bestTools, input: 14191, output: 1370, searches: 1},
	}},
	{config.EngineConfig{ID: "perplexity", Provider: "openrouter", Model: "perplexity/sonar"}, []liveAnswer{
		{mode: engine.ToolPolicyMemory, prompt: bestTools, cost: 0.0056529},
		{mode: engine.ToolPolicyMemory, prompt: alternatives, cost: 0.0060093},
		{mode: engine.ToolPolicySearchAuto, prompt: bestTools, cost: 0.0057717},
		{mode: engine.ToolPolicySearchAuto, prompt: alternatives, cost: 0.0065637},
	}},
}

func TestEstimateIsCloseToWhatLiveAnswersCost(t *testing.T) {
	for _, live := range liveRuns {
		t.Run(live.engine.ID, func(t *testing.T) {
			price, ok := prices[live.engine.Provider+"/"+live.engine.Model]
			if !ok {
				t.Fatalf("no price for %s", live.engine.Model)
			}
			plan := &run.Plan{}
			spent := 0.0
			for _, a := range live.answers {
				plan.WorkUnits = append(plan.WorkUnits, run.WorkUnit{EngineID: live.engine.ID, Mode: a.mode, Prompt: a.prompt})
				if a.cost > 0 {
					spent += a.cost
					continue
				}
				spent += float64(a.input)/1e6*price.InputPerM + float64(a.output)/1e6*price.OutputPerM + float64(a.searches)*price.PerSearch
			}
			est := ForPlan(plan, []config.EngineConfig{live.engine})
			if ratio := est.Dollars / spent; math.Abs(ratio-1) > 0.25 {
				t.Fatalf("estimate $%.4f, the answers cost $%.4f (%.0f%%)", est.Dollars, spent, ratio*100)
			}
		})
	}
}

func TestEstimateNamesModelsWithoutAPrice(t *testing.T) {
	plan := &run.Plan{WorkUnits: []run.WorkUnit{{EngineID: "x", Mode: engine.ToolPolicyMemory, Prompt: bestTools}}}
	est := ForPlan(plan, []config.EngineConfig{{ID: "x", Provider: "openrouter", Model: "meta-llama/llama-4-maverick"}})
	if est.Dollars != 0 || len(est.UnknownModels) != 1 {
		t.Fatalf("estimate = %#v", est)
	}
}

func TestEstimateSplitsByEngine(t *testing.T) {
	engines := []config.EngineConfig{
		{ID: "mock", Provider: "mock", Model: "demo"},
		{ID: "chatgpt", Provider: "openai", Model: "gpt-6.1-sol"},
		{ID: "perplexity", Provider: "perplexity", Model: "sonar"},
		{ID: "sonar", Provider: "openrouter", Model: "perplexity/sonar"},
		{ID: "llama", Provider: "openrouter", Model: "meta-llama/llama-4-maverick"},
	}
	plan := &run.Plan{}
	for _, e := range engines {
		for _, mode := range []engine.ToolPolicy{engine.ToolPolicyMemory, engine.ToolPolicySearchAuto} {
			plan.WorkUnits = append(plan.WorkUnits, run.WorkUnit{EngineID: e.ID, Mode: mode, Prompt: bestTools})
		}
	}
	est := ForPlan(plan, engines)
	if len(est.Engines) != len(engines) {
		t.Fatalf("engines = %+v", est.Engines)
	}
	sum := 0.0
	for i, e := range est.Engines {
		if e.ID != engines[i].ID {
			t.Fatalf("engine %d is %s, want %s", i, e.ID, engines[i].ID)
		}
		sum += e.Dollars
	}
	if math.Abs(sum-est.Dollars) > 1e-12 {
		t.Fatalf("engines add up to %f, total is %f", sum, est.Dollars)
	}
	mock, chatgpt, perplexity, sonar, llama := est.Engines[0], est.Engines[1], est.Engines[2], est.Engines[3], est.Engines[4]
	if !mock.Free || mock.String() != "no cost" {
		t.Errorf("mock = %+v %q", mock, mock)
	}
	if chatgpt.Free || chatgpt.FeeMissing || chatgpt.Dollars <= 0 {
		t.Errorf("chatgpt = %+v", chatgpt)
	}
	if !perplexity.FeeMissing || perplexity.String() != dollarsText(perplexity.Dollars)+" plus a fee per request" {
		t.Errorf("perplexity = %+v %q", perplexity, perplexity)
	}
	// Through OpenRouter the fee is known: $0.005 on each of the two requests.
	if sonar.FeeMissing || sonar.Dollars < 0.01 {
		t.Errorf("sonar on OpenRouter = %+v", sonar)
	}
	if !llama.PriceUnknown || llama.String() != "unknown" {
		t.Errorf("llama = %+v %q", llama, llama)
	}
	want := []string{
		"The total leaves out llama, because llmention's price table has no price for its model.",
		"The total leaves out the fee per request for perplexity, because llmention's price table doesn't have it.",
	}
	if got := est.LeftOut(); !slices.Equal(got, want) {
		t.Fatalf("LeftOut() = %q", got)
	}
}
