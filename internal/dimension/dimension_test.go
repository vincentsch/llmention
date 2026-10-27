package dimension

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeMapCanonicalizesSixKeys(t *testing.T) {
	dims, err := NormalizeMap(map[string]string{
		Market:     " US ",
		Region:     "North_America",
		Locale:     "EN_us",
		Language:   "en",
		Persona:    " CMO ",
		BuyerStage: "Late_Eval",
	})
	if err != nil {
		t.Fatalf("NormalizeMap() error = %v", err)
	}
	want := map[string]string{
		Market:     "us",
		Region:     "north_america",
		Locale:     "en-US",
		Language:   "en",
		Persona:    "cmo",
		BuyerStage: "late_eval",
	}
	if !reflect.DeepEqual(dims, want) {
		t.Fatalf("dims = %#v, want %#v", dims, want)
	}
	wantTuple := "market=us|region=north_america|locale=en-US|language=en|persona=cmo|buyer_stage=late_eval"
	if got := TupleKey(dims); got != wantTuple {
		t.Fatalf("TupleKey() = %q, want %q", got, wantTuple)
	}
}

func TestNormalizeMapRejectsUnknownOrInconsistentDimensions(t *testing.T) {
	tests := []map[string]string{
		{"unknown": "us"},
		{Language: "en", Locale: "de-DE"},
		{Market: "United States"},
		{Persona: "cto!"},
		{BuyerStage: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, input := range tests {
		if _, err := NormalizeMap(input); err == nil {
			t.Fatalf("NormalizeMap(%#v) succeeded, want error", input)
		}
	}
}

func TestNormalizeFiltersAndMatch(t *testing.T) {
	filters, err := NormalizeFilters(map[string][]string{
		Market: {"US", "de", "us"},
		Locale: {"en-us"},
	})
	if err != nil {
		t.Fatalf("NormalizeFilters() error = %v", err)
	}
	want := Filters{
		Market: {"de", "us"},
		Locale: {"en-US"},
	}
	if !reflect.DeepEqual(filters, want) {
		t.Fatalf("filters = %#v, want %#v", filters, want)
	}
	if !Match(filters, map[string]string{Market: "US", Locale: "en-US"}) {
		t.Fatalf("Match() rejected equivalent dimensions")
	}
	if Match(filters, map[string]string{Market: "US"}) {
		t.Fatalf("Match() accepted dimensions missing locale")
	}
	if Match(filters, map[string]string{Market: "fr", Locale: "en-US"}) {
		t.Fatalf("Match() accepted disallowed market")
	}
}

func TestNormalizeComparisonGroupIDGrammar(t *testing.T) {
	valid := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got, err := NormalizeComparisonGroupID(valid); err != nil || got != valid {
		t.Fatalf("NormalizeComparisonGroupID() = %q, %v", got, err)
	}
	withPunctuation := "family.v1:market-us_stage"
	if got, err := NormalizeComparisonGroupID(withPunctuation); err != nil || got != withPunctuation {
		t.Fatalf("NormalizeComparisonGroupID() punctuation = %q, %v", got, err)
	}
	if _, err := NormalizeComparisonGroupID("bad/value"); err == nil {
		t.Fatalf("NormalizeComparisonGroupID() accepted slash")
	}
	if _, err := NormalizeComparisonGroupID("a" + strings.Repeat("b", 128)); err == nil {
		t.Fatalf("NormalizeComparisonGroupID() accepted overlength value")
	}
}

func TestAggregateScopeCombinesCanonicalDimensionValues(t *testing.T) {
	scope := AggregateScope([]map[string]string{
		{Market: "US", Locale: "en-US"},
		{Market: "de", Locale: "de-DE"},
		{Persona: "CTO"},
	})
	if scope.Kind != "aggregate" {
		t.Fatalf("scope kind = %q", scope.Kind)
	}
	if got, want := scope.CombinedValues[Market], []string{"de", "us"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("market combined values = %#v, want %#v", got, want)
	}
	if got, want := scope.CombinedValues[Locale], []string{"de-DE", "en-US"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("locale combined values = %#v, want %#v", got, want)
	}
	if got, want := scope.CombinedValues[Persona], []string{"cto"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persona combined values = %#v, want %#v", got, want)
	}
	for _, key := range []string{Market, Locale, Persona} {
		if !scope.IncludesMissing[key] {
			t.Fatalf("scope missing flag for %s = false", key)
		}
	}
	if scope.IncludesUnscoped {
		t.Fatalf("scope includes unscoped dimensions")
	}
}

func TestExactScopeDistinguishesUnscopedRows(t *testing.T) {
	scope := ExactScope(nil)
	if scope.Kind != "unscoped" || !scope.IncludesUnscoped {
		t.Fatalf("unscoped exact scope = %#v", scope)
	}
	if scope.Dimensions == nil || scope.CombinedValues == nil || scope.IncludesMissing == nil {
		t.Fatalf("unscoped scope maps must be non-nil: %#v", scope)
	}
}
