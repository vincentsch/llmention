package gate

import (
	"strings"
	"testing"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/visibility"
)

func TestParse(t *testing.T) {
	for raw, want := range map[string]Gate{
		"mention-rate=40%":                    {Name: "mention-rate", Threshold: 0.4, Filters: map[string]string{}},
		"share-of-voice[engine=chatgpt]=25%":  {Name: "share-of-voice", Threshold: 0.25, Filters: map[string]string{"engine": "chatgpt"}},
		"citation-rate[mode=search_auto]=0.1": {Name: "citation-rate", Threshold: 0.1, Filters: map[string]string{"mode": "search_auto"}},
		"metric:sources.owned_domain_citation_rate[level=suite_mode_engine,engine=claude]=0.2": {
			MetricID: "sources.owned_domain_citation_rate", Threshold: 0.2,
			Filters: map[string]string{"level": "suite_mode_engine", "engine": "claude"},
		},
	} {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if got.Name != want.Name || got.MetricID != want.MetricID || got.Threshold != want.Threshold || len(got.Filters) != len(want.Filters) {
			t.Fatalf("Parse(%q) = %+v, want %+v", raw, got, want)
		}
		for k, v := range want.Filters {
			if got.Filters[k] != v {
				t.Fatalf("Parse(%q) filter %s = %q, want %q", raw, k, got.Filters[k], v)
			}
		}
	}
}

func TestParseErrorsSayWhatIsWrong(t *testing.T) {
	for raw, want := range map[string]string{
		"mention-rate":                  "needs a threshold",
		"mentions=40%":                  "unknown check",
		"mention-rate=lots":             "not a number",
		"mention-rate[engine=a=40%":     "no closing ]",
		"mention-rate[suite=x]=40%":     "suite",
		"mention-rate[engine]=40%":      "key=value",
		"metric:[engine=chatgpt]=1":     "names no metric",
		"mention-rate[mode=a,mode=b]=1": "given twice",
		// A lower rank is better, so "fail under" would point the wrong way.
		"metric:visibility.brand_rank_avg[level=suite_mode_engine]=2": "lower rank is better",
	} {
		_, err := Parse(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want it to mention %q", raw, err, want)
		}
	}
}

func TestCheckCatchesGatesThatCannotMatch(t *testing.T) {
	visibilitySuite := &suite.Suite{ID: "prompts", Type: "visibility"}
	planned := Planned{Suites: []*suite.Suite{visibilitySuite}, Engines: []string{"chatgpt"}, Modes: []string{"memory"}}
	for raw, want := range map[string]string{
		"mention-rate[engine=claude]=40%":       `engine "claude" is not part of this run`,
		"mention-rate[mode=search_auto]=40%":    "this run has no web search mode; its modes are no web search",
		"metric:recognition.recognition_rate=1": "no suite in this run reports",
		"citation-rate=10%":                     "this run asks without web search",
		"citation-rate[mode=no-search]=10%":     "use a web search mode such as mode=search",
	} {
		gates, err := ParseAll([]string{raw})
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(gates, planned); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%q) = %v, want %q", raw, err, want)
		}
	}
	gates, _ := ParseAll([]string{"mention-rate[engine=chatgpt]=40%"})
	if err := Check(gates, planned); err != nil {
		t.Fatalf("a gate that matches the run failed: %v", err)
	}
}

func TestCheckRefusesMetricGatesThatMatchSeveralNumbers(t *testing.T) {
	s := &suite.Suite{ID: "prompts", Type: "visibility", Metrics: []suite.MetricDeclaration{{ID: "sources.owned_domain_citation_rate"}}}
	planned := Planned{Suites: []*suite.Suite{s}, Engines: []string{"chatgpt", "claude"}, Modes: []string{"memory", "search_auto"}}
	for raw, want := range map[string]string{
		"metric:sources.owned_domain_citation_rate=10%":                                        "add level=",
		"metric:sources.owned_domain_citation_rate[level=suite_mode_engine,engine=claude]=10%": "add mode=",
		"metric:sources.owned_domain_citation_rate[level=suite_mode_engine,mode=search]=10%":   "add engine=",
	} {
		gates, err := ParseAll([]string{raw})
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(gates, planned); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%q) = %v, want %q", raw, err, want)
		}
	}
	gates, _ := ParseAll([]string{"metric:sources.owned_domain_citation_rate[level=suite_mode_engine,engine=claude,mode=search]=10%"})
	if err := Check(gates, planned); err != nil {
		t.Fatalf("a gate that picks one number was refused: %v", err)
	}
}

func cellWith(engine, mode string, eligible, mentioned int) visibility.Cell {
	return visibility.Cell{Engine: engine, Mode: mode, Brands: []visibility.BrandStats{{Name: "Acme", You: true, Eligible: eligible, Mentioned: mentioned}}}
}

func TestShortGateIsInconclusiveWhenACellHasNoNumber(t *testing.T) {
	gates, _ := ParseAll([]string{"mention-rate=40%"})
	// chatgpt mentions you every time; every claude request failed.
	failed := cellWith("claude", "memory", 0, 0)
	failed.Failed = 10
	summary := &visibility.Summary{Cells: []visibility.Cell{cellWith("chatgpt", "memory", 10, 10), failed}}
	got := Evaluate(gates, &analysis.Analysis{Visibility: summary})[0]
	if got.Status != StatusInconclusive || !strings.Contains(got.Message, "claude") {
		t.Fatalf("gate = %+v, want inconclusive naming claude", got)
	}
	if !Failed([]scorecard.Gate{got}) {
		t.Fatal("an inconclusive gate let the run pass")
	}
	// An engine below the line still fails the check outright.
	summary.Cells = append(summary.Cells, cellWith("gemini", "memory", 10, 1))
	if got := Evaluate(gates, &analysis.Analysis{Visibility: summary})[0]; got.Status != StatusFail {
		t.Fatalf("gate = %+v, want fail", got)
	}
}

func TestCitationRateGateDividesByEveryAnswerThatCouldCite(t *testing.T) {
	gates, _ := ParseAll([]string{"citation-rate[engine=chatgpt]=30%"})
	cell := cellWith("chatgpt", "search_auto", 5, 2)
	cell.CitationAnswers, cell.YourSiteCited, cell.CitedAny = 5, 2, 2
	memory := cellWith("chatgpt", "memory", 5, 2)
	got := Evaluate(gates, &analysis.Analysis{Visibility: &visibility.Summary{Cells: []visibility.Cell{memory, cell}}})[0]
	if got.Status != StatusPass || got.Value == nil || *got.Value != 0.4 {
		t.Fatalf("gate = %+v, want pass at 40%% (2 of 5 search answers)", got)
	}
	cell.YourSiteCited = 1
	got = Evaluate(gates, &analysis.Analysis{Visibility: &visibility.Summary{Cells: []visibility.Cell{memory, cell}}})[0]
	if got.Status != StatusFail || *got.Value != 0.2 {
		t.Fatalf("gate = %+v, want fail at 20%%", got)
	}
}
