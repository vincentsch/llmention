package suite

import (
	"github.com/vincentsch/llmention/internal/config"
)

// Visibility metrics every prompts suite reports. The mention metrics leave
// out answers to prompts that already name the brand they measure.
var visibilityMetrics = []MetricDeclaration{
	{ID: "visibility.brand_mention_rate", Evaluator: "mention", Kind: "rate"},
	{ID: "visibility.brand_share_of_voice", Evaluator: "mention", Kind: "rate"},
	{ID: "visibility.brand_rank_avg", Evaluator: "mention", Kind: "number"},
	{ID: "visibility.competitor_mention_rate", Evaluator: "mention", Kind: "rate"},
	{ID: "visibility.competitor_precedes_brand_rate", Evaluator: "mention", Kind: "rate"},
	{ID: "visibility.search_recovery_rate", Evaluator: "mention", Kind: "rate"},
	{ID: "sources.owned_domain_citation_rate", Evaluator: "source", Kind: "rate"},
	{ID: "sources.competitor_domain_citation_rate", Evaluator: "source", Kind: "rate"},
	{ID: "sources.owned_domain_citation_share", Evaluator: "source", Kind: "rate"},
	{ID: "sources.cited_domain_count", Evaluator: "source", Kind: "integer"},
	{ID: "sources.search_query_count", Evaluator: "source", Kind: "integer"},
}

var framingMetrics = []MetricDeclaration{
	{ID: "framing.evaluated_rate", Evaluator: "framing", Kind: "rate"},
	{ID: "framing.sentiment_positive_rate", Evaluator: "framing", Kind: "rate"},
	{ID: "framing.sentiment_neutral_rate", Evaluator: "framing", Kind: "rate"},
	{ID: "framing.sentiment_negative_rate", Evaluator: "framing", Kind: "rate"},
	{ID: "framing.sentiment_mixed_rate", Evaluator: "framing", Kind: "rate"},
}

// PromptsSuite turns the prompts in llmention.yaml into a visibility suite,
// so a plain list of questions needs no suite file. It returns nil when the
// project has no prompts. The AI grader that judges how answers describe the
// brand is added only when analysis.judge is set.
func PromptsSuite(project *config.Project) *Suite {
	if project == nil || len(project.Prompts) == 0 {
		return nil
	}
	s := &Suite{
		ID:           config.PromptsSuiteID,
		Version:      1,
		Type:         "visibility",
		Purpose:      "Your prompts from llmention.yaml.",
		ContextMode:  ContextModeProjectBound,
		DefaultModes: append([]string(nil), project.Modes...),
		Repetitions:  project.Repetitions,
		Evaluators: []EvaluatorDeclaration{
			{ID: "mention", Type: "mention_match"},
			{ID: "source", Type: "source_domain"},
		},
		Metrics: append([]MetricDeclaration(nil), visibilityMetrics...),
	}
	if project.Analysis.Judge != nil {
		s.Evaluators = append(s.Evaluators, EvaluatorDeclaration{ID: "framing", Type: "framing_sentiment"})
		s.Metrics = append(s.Metrics, framingMetrics...)
	}
	for _, p := range project.Prompts {
		s.Items = append(s.Items, Item{
			ID:     p.ID,
			Type:   "visibility.prompt",
			Prompt: p.Text,
			Tags:   append([]string(nil), p.Tags...),
		})
	}
	return s
}
