package suite

import (
	"testing"

	"github.com/vincentsch/llmention/internal/config"
)

func TestPromptsSuiteFromConfig(t *testing.T) {
	if PromptsSuite(&config.Project{}) != nil {
		t.Fatal("a project without prompts has no prompts suite")
	}
	project := &config.Project{
		Modes:       []string{"memory", "search_auto"},
		Repetitions: 2,
		Prompts: []config.Prompt{
			{ID: "best-invoicing-tools", Text: "What are the best invoicing tools?", Tags: []string{"category"}},
			{ID: "alternatives-globex", Text: "What are good alternatives to Globex Billing?"},
		},
	}
	s := PromptsSuite(project)
	if s.ID != config.PromptsSuiteID || s.Type != "visibility" || len(s.Items) != 2 || s.Repetitions != 2 {
		t.Fatalf("suite = %+v", s)
	}
	if s.Items[0].ID != "best-invoicing-tools" || s.Items[0].Prompt != "What are the best invoicing tools?" || s.Items[0].Tags[0] != "category" {
		t.Fatalf("first item = %+v", s.Items[0])
	}
	for _, e := range s.Evaluators {
		if e.ID == "framing" {
			t.Fatal("framing needs a judge")
		}
	}
	project.Analysis.Judge = &config.JudgeConfig{Provider: "mock", Model: "demo"}
	withJudge := PromptsSuite(project)
	if len(withJudge.Evaluators) != 3 || len(withJudge.Metrics) != len(visibilityMetrics)+len(framingMetrics) {
		t.Fatalf("judge suite = %+v", withJudge.Evaluators)
	}
	// Changing the project's modes later must not change the suite.
	project.Modes[0] = "changed"
	if s.DefaultModes[0] != "memory" {
		t.Fatal("the suite shares the modes slice with the project")
	}
}
