// Package cost estimates what a run will cost before it starts. The numbers
// are rough. They multiply typical answer and search sizes by a small price
// table that was copied from the providers' pricing pages on the date below.
package cost

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/run"
)

// PricesCheckedOn is when the price table was last compared with the
// providers' pricing pages.
const PricesCheckedOn = "2026-10-26"

// Price is what one model costs.
type Price struct {
	InputPerM  float64 // dollars per million input tokens
	OutputPerM float64 // dollars per million output tokens
	PerSearch  float64 // dollars per web search or grounded request
	PerRequest float64 // dollars per request on top of the tokens
	// FeesMissing is true when the provider charges a fee per request that
	// the table does not know.
	FeesMissing bool
}

// prices maps provider/model to its price. Models that are not listed are
// reported as unknown. llmention does not guess their price.
var prices = map[string]Price{
	"openai/gpt-6-luna":            {0.10, 0.50, 0.010, 0, false},
	"openai/gpt-6.1-sol":           {2.00, 10.00, 0.010, 0, false},
	"openai/gpt-6-astra":           {10.00, 50.00, 0.010, 0, false},
	"openai/gpt-5-mini":            {0.25, 2.00, 0.010, 0, false},
	"openai/gpt-5.4-mini":          {0.75, 4.50, 0.010, 0, false},
	"openai/gpt-4.1-mini":          {0.40, 1.60, 0.010, 0, false},
	"openai/gpt-4o-mini":           {0.15, 0.60, 0.010, 0, false},
	"anthropic/claude-haiku-5-5":   {0.10, 0.50, 0.010, 0, false},
	"anthropic/claude-sonnet-5-5":  {2.00, 10.00, 0.010, 0, false},
	"anthropic/claude-opus-5-5":    {4.00, 20.00, 0.010, 0, false},
	"anthropic/claude-fable-5-1":   {10.00, 50.00, 0.010, 0, false},
	"anthropic/claude-haiku-4-5":   {1.00, 5.00, 0.010, 0, false},
	"google/gemini-3.8-flash":      {0.75, 3.75, 0.014, 0, false},
	"google/gemini-3.5-flash-lite": {0.30, 2.50, 0.014, 0, false},
	"google/gemini-4-argon":        {2.00, 10.00, 0.014, 0, false},
	"perplexity/sonar":             {0.25, 2.50, 0, 0, true},
	// From OpenRouter's model list. OpenRouter bills Perplexity's fee on
	// every request, also with search turned off.
	"openrouter/perplexity/sonar": {1.00, 1.00, 0, 0.005, false},
	// From OpenRouter's model list on 2026-11-08. Gemini searches with
	// Google's own search there, and OpenRouter passes Google's price on.
	"openrouter/google/gemini-3.8-flash":      {0.825, 4.125, 0.014, 0, false},
	"openrouter/google/gemini-3.5-flash-lite": {0.30, 2.50, 0.014, 0, false},
	"openrouter/google/gemini-4-argon":        {2.00, 10.00, 0.014, 0, false},
	"openrouter/openai/gpt-6.1-sol":           {2.00, 10.00, 0.010, 0, false},
	"openrouter/anthropic/claude-sonnet-5.5":  {2.00, 10.00, 0.010, 0, false},
}

// answerTokens is a typical answer to a buyer question, thinking included.
// Answers in a live run in October 2026 were 500 to 1,400 tokens long.
const (
	answerTokens   = 1000
	promptOverhead = 30
)

// typicalSearch is what one answer in a web search mode adds: the pages the
// model read, which providers bill as input tokens, and the number of
// searches. The numbers come from a live run in October 2026. OpenAI's model
// searched two or three times and read 30,000 to 38,000 tokens of pages per
// answer. Claude searched once and read about 14,000. Perplexity's search is
// paid for by its fee per request.
func typicalSearch(provider, model string) (tokens int, searches float64) {
	switch {
	case provider == "openai", provider == "openrouter" && strings.HasPrefix(model, "openai/"):
		return 32000, 3
	case provider == "anthropic", provider == "openrouter" && strings.HasPrefix(model, "anthropic/"):
		return 14000, 1
	case provider == "perplexity", provider == "openrouter" && strings.HasPrefix(model, "perplexity/"):
		return 0, 0
	}
	return 10000, 1
}

// Estimate is the expected cost of a plan.
type Estimate struct {
	Dollars float64 `json:"dollars"`
	// UnknownModels lists engines whose model is not in the price table.
	UnknownModels []string `json:"unknown_models,omitempty"`
	// FeesMissing lists engines with fees per request that the estimate leaves
	// out.
	FeesMissing []string `json:"fees_missing,omitempty"`
	Free        bool     `json:"free"` // only mock or local engines
}

// ForPlan estimates the cost of sending every work unit once.
func ForPlan(plan *run.Plan, engines []config.EngineConfig) Estimate {
	byID := map[string]config.EngineConfig{}
	for _, e := range engines {
		byID[e.ID] = e
	}
	est := Estimate{Free: true}
	seenUnknown := map[string]bool{}
	for _, unit := range plan.WorkUnits {
		e := byID[unit.EngineID]
		if e.Provider == "mock" || (e.Provider == "openai-compatible" && isLocal(e.BaseURL)) {
			continue
		}
		est.Free = false
		price, ok := prices[e.Provider+"/"+e.Model]
		if !ok {
			if !seenUnknown[e.ID] {
				seenUnknown[e.ID] = true
				est.UnknownModels = append(est.UnknownModels, e.ID)
			}
			continue
		}
		output := answerTokens
		if e.MaxOutputTokens > 0 {
			output = min(output, e.MaxOutputTokens)
		}
		input := len(unit.Prompt)/4 + promptOverhead
		dollars := price.PerRequest
		if config.IsSearchMode(unit.Mode.String()) {
			tokens, searches := typicalSearch(e.Provider, e.Model)
			input += tokens
			dollars += searches * price.PerSearch
		}
		dollars += float64(input)/1e6*price.InputPerM + float64(output)/1e6*price.OutputPerM
		est.Dollars += dollars
		if price.FeesMissing && !slices.Contains(est.FeesMissing, e.ID) {
			est.FeesMissing = append(est.FeesMissing, e.ID)
		}
	}
	return est
}

// String says the estimate in words, for example "about $0.42".
func (e Estimate) String() string {
	if e.Free {
		return "no provider cost"
	}
	var parts []string
	switch {
	case e.Dollars == 0 && len(e.UnknownModels) > 0:
		return "unknown, because llmention has no price for the model of " + config.JoinAnd(e.UnknownModels)
	case e.Dollars < 0.01:
		parts = append(parts, "under $0.01")
	default:
		parts = append(parts, fmt.Sprintf("about $%.2f", e.Dollars))
	}
	if len(e.UnknownModels) > 0 && e.Dollars > 0 {
		parts = append(parts, "plus the cost of "+config.JoinAnd(e.UnknownModels)+", which is not in llmention's price table")
	}
	if len(e.FeesMissing) > 0 {
		parts = append(parts, "plus the fee per request for "+config.JoinAnd(e.FeesMissing))
	}
	return strings.Join(parts, ", ")
}

func isLocal(baseURL string) bool {
	host := baseURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, ":/"); i >= 0 {
		host = host[:i]
	}
	return config.IsLoopbackHost(strings.Trim(host, "[]"))
}
