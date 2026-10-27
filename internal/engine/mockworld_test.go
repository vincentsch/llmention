package engine

import (
	"strings"
	"testing"
)

func testWorld() *MockWorld {
	return &MockWorld{
		Brand:       MockBrand{Name: "Acme Books", Domain: "acme-books.example"},
		Category:    "invoicing for freelancers",
		Competitors: []MockBrand{{Name: "Globex Billing", Domain: "globex.example"}, {Name: "Initech Invoices"}},
		Affinity:    map[string]float64{"always": 1, "never": 0},
	}
}

func TestMockWorldAnswersAreStableAndVaried(t *testing.T) {
	w := testWorld()
	req := Request{Prompt: "What are the best tools for invoicing for freelancers?", Mode: ToolPolicyMemory}
	first, ok := w.answer("chatgpt", "demo", req)
	if !ok || first.Text == "" {
		t.Fatalf("no answer: %+v", first)
	}
	again, _ := w.answer("chatgpt", "demo", req)
	if again.Text != first.Text {
		t.Fatal("the same request must give the same answer")
	}
	texts := map[string]bool{}
	for _, prompt := range []string{"What are the best tools for invoicing for freelancers?", "Which invoicing app would you recommend?", "What do freelancers use to send invoices?", "What are good free options?"} {
		a, _ := w.answer("chatgpt", "demo", Request{Prompt: prompt, Mode: ToolPolicyMemory})
		texts[a.Text] = true
	}
	if len(texts) < 3 {
		t.Fatalf("answers should differ between prompts, got %d distinct", len(texts))
	}
}

func TestMockWorldFollowsAffinity(t *testing.T) {
	w := testWorld()
	for i := range 10 {
		req := Request{Prompt: "What are the best tools for invoicing for freelancers?", Mode: ToolPolicyMemory, Metadata: map[string]string{"repetition": string(rune('0' + i))}}
		always, _ := w.answer("always", "demo", req)
		never, _ := w.answer("never", "demo", req)
		if !strings.Contains(always.Text, "Acme Books") {
			t.Fatalf("an engine with affinity 1 left the brand out:\n%s", always.Text)
		}
		if strings.Contains(never.Text, "Acme Books") {
			t.Fatalf("an engine with affinity 0 named the brand:\n%s", never.Text)
		}
		if !strings.Contains(never.Text, "Globex Billing") && !strings.Contains(never.Text, "Initech Invoices") && !strings.Contains(never.Text, "Northwind") {
			t.Fatalf("the answer names no product:\n%s", never.Text)
		}
	}
}

func TestMockWorldSearchAnswersCite(t *testing.T) {
	w := testWorld()
	a, _ := w.answer("always", "demo", Request{Prompt: "What are the best tools for invoicing for freelancers?", Mode: ToolPolicySearchAuto})
	if len(a.Citations) == 0 {
		t.Fatal("a search answer should cite sources")
	}
	if a2, _ := w.answer("always", "demo", Request{Prompt: "What are the best tools for invoicing for freelancers?", Mode: ToolPolicyMemory}); len(a2.Citations) != 0 {
		t.Fatal("a memory answer should not cite sources")
	}
}

func TestMockWithoutWorldDeclines(t *testing.T) {
	var w *MockWorld
	if _, ok := w.answer("chatgpt", "demo", Request{Prompt: "x"}); ok {
		t.Fatal("a nil world must not answer")
	}
}
