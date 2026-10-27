package audit

import "testing"

func TestFindLiteralNormalizesPunctuationWhitespaceAndCase(t *testing.T) {
	answer := "Intro: PERPETUAL\nmotion -- conflicts with established thermodynamics."
	span, ok := FindLiteral(answer, "Perpetual motion conflicts with established thermodynamics.")
	if !ok {
		t.Fatal("expected literal match")
	}
	if got := answer[span.ByteStart:span.ByteEnd]; got != "PERPETUAL\nmotion -- conflicts with established thermodynamics" {
		t.Fatalf("unexpected evidence quote %q", got)
	}
}

func TestFindLiteralRequiresTokenBoundary(t *testing.T) {
	if _, ok := FindLiteral("The aprotests marker is not the claim.", "protests"); ok {
		t.Fatal("matched inside a larger token")
	}
}

func TestContainsRefusalCatalog(t *testing.T) {
	if !ContainsRefusal("I cannot provide information about that topic.") {
		t.Fatal("expected refusal phrase")
	}
	if ContainsRefusal("This answer provides information about that topic.") {
		t.Fatal("unexpected refusal phrase")
	}
	if ContainsRefusal("I cannot answer that directly, but here is context.") {
		t.Fatal("generic refusal outside the fixed catalog matched")
	}
}
