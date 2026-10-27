package evaluate

import (
	"testing"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/run"
)

func TestModelAssistedPlanCountsGraderRequests(t *testing.T) {
	resultPlans := []ResultPlan{{SuiteID: "visibility.bounds", SuiteVersion: 1, EvaluatorIDs: []string{"one", "two", "three", "four"}}}
	evaluators := map[string]ResolvedEvaluator{}
	for _, id := range resultPlans[0].EvaluatorIDs {
		evaluators[evaluatorKey("visibility.bounds", 1, id)] = ResolvedEvaluator{EvaluatorType: EvaluatorFramingSentiment}
	}
	judge := &config.JudgeConfig{Provider: "mock", Model: "fixture", AllowAnswerDisclosure: true}

	var issues []ReadinessIssue
	plan, err := buildModelAssistedPlan(ModelAssistedPlanInput{Enabled: false, MaxJudgeRequests: 1, Judge: judge}, run.Plan{Retries: 2}, resultPlans, evaluators, &issues)
	if err != nil || plan.Enabled || !plan.JudgeReady || plan.RequestUpperBound != 0 || plan.PlannedCandidates != 4 || len(issues) != 0 {
		t.Fatalf("a disabled grader plans no requests: plan=%+v issues=%v err=%v", plan, issues, err)
	}
	for _, tc := range []struct {
		cap   int
		ready bool
	}{{12, true}, {11, false}} {
		issues = nil
		plan, err := buildModelAssistedPlan(ModelAssistedPlanInput{Enabled: true, MaxJudgeRequests: tc.cap, Judge: judge}, run.Plan{Retries: 2}, resultPlans, evaluators, &issues)
		if err != nil || plan.RequestUpperBound != 12 || plan.JudgeReady != tc.ready {
			t.Fatalf("cap %d: plan=%+v issues=%v err=%v", tc.cap, plan, issues, err)
		}
	}
}
