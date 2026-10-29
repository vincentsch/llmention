package report

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/visibility"
)

var acme = visibility.Brands{You: visibility.Brand{Name: "Acme"}, Competitors: []visibility.Brand{{Name: "Globex"}}}

func card(id string) *scorecard.Scorecard {
	return &scorecard.Scorecard{ID: id, Engines: []scorecard.Engine{{ID: "e", Model: "m"}}}
}

func answer(prompt, text string) visibility.Answer {
	return visibility.Answer{PromptID: prompt, Prompt: prompt + "?", Engine: "e", Mode: "memory", Repetition: 1, Text: text}
}

func TestDiffComparesOnlyAnswersBothRunsHave(t *testing.T) {
	old := []visibility.Answer{answer("p1", "Globex only."), answer("p2", "Acme.")}
	failed := answer("p1", "")
	failed.Failed = true
	now := []visibility.Answer{failed, answer("p2", "Acme.")}
	d := BuildDiff(card("old"), card("new"), old, now, acme)
	// Before the fix the failed request dropped p1 from the new run only,
	// and the rate "rose" from 50% to 100% with no answer changing.
	row := d.Rows[0]
	if *row.OldRate != 1 || *row.NewRate != 1 || d.LeftOut != 1 || d.Compared != 1 {
		t.Fatalf("row = %v → %v, left out %d, compared %d", *row.OldRate, *row.NewRate, d.LeftOut, d.Compared)
	}
	unfinished := answer("p1", "Acme is")
	unfinished.Unfinished = true
	d = BuildDiff(card("old"), card("new"), old, []visibility.Answer{unfinished, answer("p2", "Acme.")}, acme)
	if d.LeftOut != 1 || *d.Rows[0].OldRate != 1 {
		t.Fatalf("an unfinished answer was compared: %+v", d)
	}
}

func TestDiffAndTrendFlagRewordedPrompts(t *testing.T) {
	old := []visibility.Answer{answer("best", "Acme.")}
	reworded := answer("best", "Globex.")
	reworded.Prompt = "Best tools for large banks?"
	d := BuildDiff(card("old"), card("new"), old, []visibility.Answer{reworded}, acme)
	if len(d.Reworded) != 1 || d.Reworded[0].ID != "best" {
		t.Fatalf("reworded = %+v", d.Reworded)
	}
	var b bytes.Buffer
	WriteDiffText(&b, d, Style{})
	if !strings.Contains(b.String(), "Prompt best was reworded") {
		t.Fatalf("diff text:\n%s", b.String())
	}
	before := visibility.Summarize(acme, old)
	after := visibility.Summarize(acme, []visibility.Answer{reworded})
	tr := BuildTrend("Acme", []TrendRun{{Card: card("1"), Summary: &before}, {Card: card("2"), Summary: &after}})
	if !tr.Points[1].Changed || !strings.Contains(strings.Join(tr.Points[1].Changes, ";"), "reworded") {
		t.Fatalf("trend point = %+v", tr.Points[1])
	}
}

func TestUnknownShareOfVoiceIsADashEverywhere(t *testing.T) {
	s := visibility.Summarize(acme, []visibility.Answer{answer("p", "Use a spreadsheet.")})
	tr := BuildTrend("Acme", []TrendRun{{Card: card("1"), Summary: &s}})
	if tr.Points[0].ShareOfVoice[0] != nil {
		t.Fatalf("trend share of voice = %v, want null", *tr.Points[0].ShareOfVoice[0])
	}
	r := Report{Run: RunInfo{ID: "1", CreatedAt: time.Now(), Brand: "Acme"}, Visibility: &s}
	var html, text, md bytes.Buffer
	if err := WriteHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	WriteText(&text, r, Style{})
	WriteMarkdown(&md, r)
	if strings.Contains(html.String(), `<div class="value">0%</div>`) && !strings.Contains(html.String(), "Share of voice</div><div class=\"value\">–") {
		t.Fatalf("HTML shows 0%% for an unknown share of voice")
	}
	if !strings.Contains(html.String(), `Share of voice</div><div class="value">–`) {
		t.Fatalf("HTML share of voice tile:\n%s", html.String())
	}
	for name, out := range map[string]string{"text": text.String(), "markdown": md.String()} {
		if !regexp.MustCompile(`0%\**\s+\|?\s*–`).MatchString(out) {
			t.Fatalf("%s does not show a dash:\n%s", name, out)
		}
	}
}

func TestAnswersShowUnfinishedAnswersWithTheirReason(t *testing.T) {
	r := Report{Run: RunInfo{ID: "1"}, Answers: []Answer{{Suite: "prompts", PromptID: "p", Prompt: "Best?", Engine: "claude", Mode: "search_auto", Status: "unfinished", StopReason: "pause_turn", Text: "Let me search"}}}
	var b bytes.Buffer
	WriteAnswers(&b, r, Style{})
	if !strings.Contains(b.String(), "unfinished: the provider paused a long web search") || !strings.Contains(b.String(), "Let me search") {
		t.Fatalf("answers:\n%s", b.String())
	}
}

// In a live run, Claude used a 200-token limit for thinking and returned no
// text. The answer view said why it was unfinished and then showed nothing.
func TestAnUnfinishedAnswerWithoutTextSaysSo(t *testing.T) {
	r := Report{Run: RunInfo{ID: "1"}, Answers: []Answer{{Suite: "prompts", PromptID: "p", Prompt: "Best?", Engine: "claude", Mode: "memory", Status: "unfinished", StopReason: "max_tokens"}}}
	var text, html bytes.Buffer
	WriteAnswers(&text, r, Style{})
	if err := WriteHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "reached the output token limit; left out of the numbers)\n    (no text came back before the provider stopped)") {
		t.Fatalf("answers:\n%s", text.String())
	}
	if !strings.Contains(html.String(), "<p>No text came back before the provider stopped.</p>") {
		t.Fatalf("HTML:\n%s", html.String())
	}
}

func TestAnEngineWithoutAnswersKeepsItsRowWithTheReason(t *testing.T) {
	stopped := answer("p", "")
	stopped.Engine, stopped.Failed, stopped.Stopped = "slow", true, true
	failed := answer("p", "")
	failed.Engine, failed.Failed = "broken", true
	later := answer("p", "Globex.")
	later.Engine = "later"
	s := visibility.Summarize(acme, []visibility.Answer{answer("p", "Acme and Globex."), stopped, failed, later})
	r := Report{Run: RunInfo{ID: "1", CreatedAt: time.Now(), Brand: "Acme"}, Visibility: &s}
	var html, text, md bytes.Buffer
	if err := WriteHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	WriteText(&text, r, Style{})
	WriteMarkdown(&md, r)
	for name, out := range map[string]string{"text": text.String(), "markdown": md.String(), "html": html.String()} {
		// Twice each: the mention table and the competitor table.
		if strings.Count(out, "no answers to count: 1 stopped") != 2 || strings.Count(out, "no answers to count: 1 failed") != 2 {
			t.Fatalf("%s does not show why slow and broken have no numbers:\n%s", name, out)
		}
	}
	if !regexp.MustCompile(`(?m)^  slow\s+no web search\s+–\s+–\s+–\s+–\s+no answers to count: 1 stopped$`).MatchString(text.String()) {
		t.Fatalf("text row for slow:\n%s", text.String())
	}
	// A row without numbers must not break the columns of the rows after it.
	lines := strings.Split(text.String(), "\n")
	header := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  Engine ") })
	rate := strings.Index(lines[header], "Rate")
	laterRow := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  later ") })
	if laterRow < 0 || strings.Index(lines[laterRow], "0%") != rate {
		t.Fatalf("the row after the rows without numbers is not aligned:\n%s", text.String())
	}
	matrix := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  Engine · mode") })
	globex := strings.Index(lines[matrix], "Globex")
	laterMatrix := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  later · ") })
	if laterMatrix < 0 || strings.Index(lines[laterMatrix], "100%") != globex {
		t.Fatalf("the competitor table is not aligned:\n%s", text.String())
	}
}

func TestDiffNamesSeveralCompetitorsInOneList(t *testing.T) {
	brands := visibility.Brands{You: visibility.Brand{Name: "Acme"}, Competitors: []visibility.Brand{{Name: "Globex"}, {Name: "Initech"}, {Name: "Umbrella"}}}
	d := BuildDiff(card("old"), card("new"), []visibility.Answer{answer("p", "Globex, Initech and Umbrella.")}, []visibility.Answer{answer("p", "Acme.")}, brands)
	var b bytes.Buffer
	WriteDiffText(&b, d, Style{})
	if !strings.Contains(b.String(), "before: named Globex, Initech and Umbrella, not Acme") {
		t.Fatalf("diff text:\n%s", b.String())
	}
	b.Reset()
	WriteDiffMarkdown(&b, d)
	if !strings.Contains(b.String(), "\n  - before: named Globex, Initech and Umbrella, not Acme\n  - now: named Acme at #1\n") {
		t.Fatalf("diff markdown:\n%s", b.String())
	}
}

func TestHTMLNotesShowCommandsAsCode(t *testing.T) {
	s := visibility.Summarize(acme, []visibility.Answer{answer("p", "Acme.")})
	r := Report{Run: RunInfo{ID: "1", CreatedAt: time.Now(), Brand: "Acme"}, Visibility: &s,
		Notes: []string{"Some requests failed; `llmention report --answers` shows <the> errors."}}
	var b bytes.Buffer
	if err := WriteHTML(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Some requests failed; <code>llmention report --answers</code> shows &lt;the&gt; errors.") {
		t.Fatalf("note in HTML:\n%s", b.String())
	}
}
