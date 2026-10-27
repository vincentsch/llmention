package evaluate

import (
	"testing"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/mention"
	"github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

// Each case builds two answers and checks which of them a metric counts.
func TestMetricsCountTheRightAnswers(t *testing.T) {
	counted := mention.Result{Mentioned: true, Rank: 2}
	named := mention.Result{Mentioned: true, NamedInPrompt: true}
	for _, tc := range []struct {
		metric                 string
		numerator, denominator float64
		setup                  func(*resultState, *resultState)
	}{
		{"visibility.brand_mention_rate", 1, 1, func(a, b *resultState) {
			a.Mention = &mentionAnalysis{Eligible: true, Brand: counted}
			b.Mention = &mentionAnalysis{Eligible: true, Brand: named}
		}},
		{"visibility.brand_rank_avg", 2, 1, func(a, b *resultState) {
			a.Mention = &mentionAnalysis{Eligible: true, Brand: counted}
			b.Mention = &mentionAnalysis{Eligible: true}
		}},
		// The second prompt names Acme, so its answer is left out of the share.
		{"visibility.brand_share_of_voice", 1, 2, func(a, b *resultState) {
			a.Mention = &mentionAnalysis{Eligible: true, Brand: counted, Competitors: map[string]mention.Result{"Rival": counted}}
			b.Mention = &mentionAnalysis{Eligible: true, Brand: named, Competitors: map[string]mention.Result{"Rival": counted}}
		}},
		{"sources.owned_domain_citation_rate", 1, 2, eligibleSourceCells},
		{"sources.owned_domain_citation_share", 1, 1, eligibleSourceCells},
		{"framing.evaluated_rate", 1, 2, func(a, b *resultState) {
			a.Framing = &framingAnalysis{Applicable: true, Valid: true, Label: "positive"}
			b.Framing = &framingAnalysis{Applicable: true}
		}},
		{"framing.sentiment_positive_rate", 1, 1, func(a, b *resultState) {
			a.Framing = &framingAnalysis{Applicable: true, Valid: true, Label: "positive"}
			b.Framing = &framingAnalysis{Applicable: true}
		}},
		{"recognition.target_recognition_rate", 1, 2, eligibleRecognitionCells},
		{"programming.wrong_error_cause_rate", 1, 1, func(a, b *resultState) {
			status := benchmark.AnswerStatusIncorrect
			a.Benchmark = &benchmark.CellPayload{SuiteFamily: benchmark.SuiteFamilyProgramming, ParseStatus: benchmark.ParseStatusParsed, AnswerStatus: &status, FailureClass: benchmark.FailureClassWrongErrorCause, Programming: &benchmark.ProgrammingExtensionV1{Subtype: "programming.error_diagnosis"}}
			other := *a.Benchmark
			other.Programming = &benchmark.ProgrammingExtensionV1{Subtype: "programming.version_fact"}
			b.Benchmark = &other
		}},
	} {
		t.Run(tc.metric, func(t *testing.T) {
			plan := MetricPlan{SuiteID: "synthetic.eligibility", SuiteVersion: 1, MetricID: tc.metric, Kind: "rate", ScopeLevels: []string{"suite_mode_engine"}}
			cell := func(id string) *resultState {
				return &resultState{
					Result:    EvaluatedResult{ResultID: id, Raw: run.Result{Status: run.ResultStatusSuccess}},
					Plan:      ResultPlan{SuiteID: plan.SuiteID, SuiteVersion: 1, Mode: "memory", EngineID: "mock", WorkKey: id},
					Item:      suite.Item{Brand: &suite.Entity{Name: "Acme"}, Competitors: []suite.Entity{{Name: "Rival"}}},
					MetricIDs: []string{tc.metric},
				}
			}
			a, b := cell("one"), cell("two")
			tc.setup(a, b)
			prepared := PreparedEvaluation{metricPlanByKey: map[string]MetricPlan{metricKey(plan.SuiteID, 1, tc.metric): plan}}
			metrics := aggregateMetrics(prepared, &evaluationState{results: []*resultState{a, b}})
			if len(metrics) != 1 || metrics[0].Status != MetricStatusAvailable || metrics[0].Numerator == nil || *metrics[0].Numerator != tc.numerator || *metrics[0].Denominator != tc.denominator {
				t.Fatalf("metrics = %+v, want %g/%g", metrics, tc.numerator, tc.denominator)
			}
		})
	}
}

func eligibleSourceCells(a, b *resultState) {
	a.Item.OwnedDomains = []string{"good.invalid"}
	b.Item.OwnedDomains = a.Item.OwnedDomains
	a.Source = &sourceAnalysis{CitationSupported: true, Citations: []domainObservation{{Domain: "good.invalid", Evidence: EvidenceRecord{ID: "synthetic-source-evidence"}}}}
	b.Source = &sourceAnalysis{CitationSupported: true}
}

func eligibleRecognitionCells(a, b *resultState) {
	yes, no := true, false
	a.Recognition = &recognitionAnalysis{Cell: recognition.CellPayload{ParserStatus: recognition.ParserStatusCompleted, TargetRecognized: &yes, Assignments: []recognition.Assignment{{AssignmentClassification: recognition.ClassificationTargetEntity, Confidence: 90}}}}
	b.Recognition = &recognitionAnalysis{Cell: recognition.CellPayload{ParserStatus: recognition.ParserStatusCompleted, TargetRecognized: &no, Assignments: []recognition.Assignment{{AssignmentClassification: recognition.ClassificationNonTarget, Confidence: 90}}}}
}
