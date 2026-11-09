package visibility

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

var brands = Brands{
	You:         Brand{Name: "Tabslip", Domains: []string{"tabslip.com"}},
	Competitors: []Brand{{Name: "Helicone", Domains: []string{"helicone.ai"}}, {Name: "Langfuse"}},
}

func TestSummarizeSkipsPromptsThatNameTheBrand(t *testing.T) {
	answers := []Answer{
		{PromptID: "best", Prompt: "Best LLM cost trackers?", Engine: "claude", Mode: "memory", Text: "1. **Helicone** - logs\n2. **Langfuse** - traces"},
		{PromptID: "alt", Prompt: "Alternatives to Tabslip?", Engine: "claude", Mode: "memory", Text: "I'm not familiar with Tabslip. Try Helicone."},
		{PromptID: "best", Prompt: "Best LLM cost trackers?", Engine: "chatgpt", Mode: "memory", Text: "1. **Helicone**\n2. **Tabslip**\n3. **Portkey**"},
		{PromptID: "alt", Prompt: "Alternatives to Tabslip?", Engine: "chatgpt", Mode: "memory", Text: "Helicone and Portkey."},
	}
	s := Summarize(brands, answers)
	claude, chatgpt := s.Cells[0], s.Cells[1]
	if you := claude.You(); you.Eligible != 1 || you.Mentioned != 0 {
		t.Fatalf("claude: an answer to a prompt naming the brand counted: %+v", you)
	}
	if rate, ok := chatgpt.You().MentionRate(); !ok || rate != 1 {
		t.Fatalf("chatgpt mention rate = %v %v", rate, ok)
	}
	if rank, ok := chatgpt.You().AvgRank(); !ok || rank != 2 {
		t.Fatalf("chatgpt rank = %v %v", rank, ok)
	}
	helicone := chatgpt.Brands[1]
	if helicone.Eligible != 2 || helicone.Mentioned != 2 {
		t.Fatalf("helicone = %+v", helicone)
	}
	// Share of voice only uses the answer to "best", which names no tracked
	// brand: Tabslip 1, Helicone 1, Langfuse 0.
	if sov, ok := chatgpt.You().SOV(); !ok || math.Abs(sov-0.5) > 1e-9 {
		t.Fatalf("share of voice = %v %v", sov, ok)
	}
	if len(s.PromptsNamingYou) != 1 || s.PromptsNamingYou[0] != "alt" {
		t.Fatalf("prompts naming you = %v", s.PromptsNamingYou)
	}
	if len(s.Unknown) != 1 || s.Unknown[0].Name != "Portkey" || s.Unknown[0].Answers != 1 {
		t.Fatalf("unknown = %+v", s.Unknown)
	}
}

func TestSummarizeClassifiesCitedDomains(t *testing.T) {
	answers := []Answer{{
		PromptID: "best", Prompt: "Best trackers?", Engine: "chatgpt", Mode: "search_auto",
		Text: "Helicone is popular.",
		Citations: []Citation{
			{URL: "https://www.helicone.ai/pricing", Domain: "www.helicone.ai"},
			{URL: "https://www.helicone.ai/docs", Domain: "www.helicone.ai"},
			{URL: "https://docs.tabslip.com/", Domain: "docs.tabslip.com"},
			{URL: "https://www.reddit.com/r/x", Domain: "reddit.com"},
		},
	}}
	s := Summarize(brands, answers)
	got := map[string]DomainCount{}
	for _, d := range s.Domains {
		got[d.Domain] = d
	}
	if d := got["helicone.ai"]; d.Owner != "Helicone" || d.Citations != 2 || d.Answers != 1 {
		t.Fatalf("helicone.ai = %+v", d)
	}
	if got["docs.tabslip.com"].Owner != "you" || got["reddit.com"].Owner != "" {
		t.Fatalf("domains = %+v", s.Domains)
	}
	if s.Cells[0].YourSiteCited != 1 || s.Cells[0].CitedAny != 1 {
		t.Fatalf("cell = %+v", s.Cells[0])
	}
}

func TestSummarizeCountsFailuresSeparately(t *testing.T) {
	s := Summarize(brands, []Answer{
		{PromptID: "p", Prompt: "q", Engine: "e", Mode: "memory", Failed: true},
		{PromptID: "p", Prompt: "q", Engine: "e", Mode: "memory", Text: "Tabslip.", Repetition: 2},
	})
	if s.Failed != 1 || s.Answers != 1 || s.Cells[0].You().Eligible != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestHighlightsNameTheGaps(t *testing.T) {
	answers := []Answer{
		{PromptID: "best", Prompt: "Best trackers?", Engine: "claude", Mode: "memory", Text: "1. **Helicone**\n2. **Portkey**"},
		{PromptID: "cheap", Prompt: "Cheap trackers?", Engine: "claude", Mode: "memory", Text: "1. **Helicone**\n2. **Portkey**"},
	}
	steps := strings.Join(Highlights(Summarize(brands, answers), false), "\n")
	for _, want := range []string{"claude never mentioned Tabslip in 2 answers. It named Helicone 2 times.", "On 2 of 2 prompts, no assistant named Tabslip", "Portkey (2 answers). If they compete with you, add them to competitors"} {
		if !strings.Contains(steps, want) {
			t.Fatalf("next steps miss %q:\n%s", want, steps)
		}
	}
	one := strings.Join(Highlights(Summarize(brands, answers[:1]), false), "\n")
	if !strings.Contains(one, "claude did not mention Tabslip in its only answer. It named Helicone once.") {
		t.Fatalf("highlight for a single answer:\n%s", one)
	}
}

// A mock run told you to add Contoso and Fabrikam to your competitors, but
// the mock engine made those names up.
func TestMadeUpAnswersGetNoAdviceAboutOtherNames(t *testing.T) {
	answers := []Answer{
		{PromptID: "best", Prompt: "Best trackers?", Engine: "claude", Mode: "memory", Text: "1. **Helicone**\n2. **Portkey**"},
		{PromptID: "cheap", Prompt: "Cheap trackers?", Engine: "claude", Mode: "memory", Text: "1. **Helicone**\n2. **Portkey**"},
	}
	steps := strings.Join(Highlights(Summarize(brands, answers), true), "\n")
	if !strings.Contains(steps, "Names you don't track came up in several answers: Portkey (2 answers).") || strings.Contains(steps, "add them to competitors") {
		t.Fatalf("highlights for made-up answers:\n%s", steps)
	}
}

func TestShareOfVoiceOnlyUsesPromptsThatNameNoTrackedBrand(t *testing.T) {
	acme := Brands{You: Brand{Name: "Acme"}, Competitors: []Brand{{Name: "Globex"}}}
	base := []Answer{
		{PromptID: "best", Prompt: "Best invoicing app?", Engine: "e", Mode: "memory", Text: "Acme or Globex."},
	}
	before := Summarize(acme, base).Cells[0].You()
	// A question that names Acme, answered with a competitor, must not move
	// Acme's share: it says nothing about whether Acme comes up on its own.
	after := Summarize(acme, append(base, Answer{PromptID: "good", Prompt: "Is Acme good?", Engine: "e", Mode: "memory", Text: "Globex is better."})).Cells[0].You()
	b, _ := before.SOV()
	a, _ := after.SOV()
	if a != b || a != 0.5 {
		t.Fatalf("share of voice moved from %v to %v", b, a)
	}
	if after.Eligible != before.Eligible || after.Mentioned != before.Mentioned {
		t.Fatalf("mention counts changed: %+v -> %+v", before, after)
	}
}

func TestShareOfVoiceIsUnknownWhenNoBrandIsMentioned(t *testing.T) {
	s := Summarize(brands, []Answer{{PromptID: "p", Prompt: "q?", Engine: "e", Mode: "memory", Text: "Use a spreadsheet."}})
	if _, ok := s.Cells[0].You().SOV(); ok || s.Cells[0].You().ShareOfVoice != nil {
		t.Fatalf("share of voice = %v, want unknown", s.Cells[0].You().ShareOfVoice)
	}
	data, _ := json.Marshal(s.Cells[0].You())
	if !strings.Contains(string(data), `"share_of_voice":null`) || !strings.Contains(string(data), `"mention_rate":0`) {
		t.Fatalf("json = %s", data)
	}
}

func TestUnfinishedAnswersAreKeptOutOfTheNumbers(t *testing.T) {
	s := Summarize(brands, []Answer{
		{PromptID: "p", Prompt: "q?", Engine: "e", Mode: "memory", Text: "Helicone is", Unfinished: true},
		{PromptID: "p", Prompt: "q?", Engine: "e", Mode: "memory", Text: "Tabslip.", Repetition: 2},
	})
	cell := s.Cells[0]
	if s.Unfinished != 1 || cell.Unfinished != 1 || cell.Answers != 1 || cell.You().Eligible != 1 || cell.Brands[1].Mentioned != 0 {
		t.Fatalf("cell = %+v", cell)
	}
}

func TestCitationRateCountsEveryAnswerThatCouldCite(t *testing.T) {
	var answers []Answer
	for i := range 10 {
		a := Answer{PromptID: "p", Prompt: "q?", Engine: "e", Mode: "search_auto", Repetition: i + 1, Text: "Some text.", CitationsReported: true}
		if i == 0 {
			a.Citations = []Citation{{URL: "https://tabslip.com/docs"}}
		}
		answers = append(answers, a)
	}
	rate, ok := Summarize(brands, answers).Cells[0].CitationRate()
	if !ok || rate != 0.1 {
		t.Fatalf("citation rate = %v %v, want 10%% (1 of 10 answers)", rate, ok)
	}
	// An engine that does not report citations has no rate at all.
	for i := range answers {
		answers[i].CitationsReported = false
	}
	if _, ok := Summarize(brands, answers).Cells[0].CitationRate(); ok {
		t.Fatal("citation rate known for an engine that reports no citations")
	}
}

func TestSitesWrittenWithWWWMatchCitations(t *testing.T) {
	b := Brands{You: Brand{Name: "Tabslip", Domains: []string{"www.tabslip.com"}}}
	s := Summarize(b, []Answer{{PromptID: "p", Prompt: "q?", Engine: "e", Mode: "search_auto", Text: "x", CitationsReported: true, Citations: []Citation{
		{URL: "https://www.tabslip.com/docs", Domain: "www.tabslip.com"},
		{URL: "https://tabslip.com/blog"},
	}}})
	if len(s.Domains) != 1 || s.Domains[0].Owner != "you" || s.Domains[0].Citations != 2 || s.Cells[0].YourSiteCited != 1 {
		t.Fatalf("domains = %+v, cell = %+v", s.Domains, s.Cells[0])
	}
}
