package mention

import (
	"reflect"
	"testing"
)

func TestFindRespectsWordBoundariesAndNormalization(t *testing.T) {
	brand := Brand{ID: "you", Name: "Tabslip", Aliases: []string{"tabslip.com"}}
	cases := []struct {
		text string
		want bool
	}{
		{"Try Tabslip for receipts.", true},
		{"tabslip's receipts are signed.", true},
		{"See TABSLIP.COM for docs.", true},
		{"Tabslipper is a different tool.", false},
		{"No match here.", false},
	}
	for _, tc := range cases {
		if got := Contains(tc.text, brand); got != tc.want {
			t.Errorf("Contains(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
	if !Contains("STRASSE is mentioned.", Brand{Name: "Straße"}) {
		t.Error("case folding did not match ß and SS")
	}
	if !Contains("Café is mentioned.", Brand{Name: "Café"}) {
		t.Error("combining accent did not match the composed form")
	}
}

func TestFindPrefersTheLongerBrandAtTheSameStart(t *testing.T) {
	brands := []Brand{{ID: "acme", Name: "Acme"}, {ID: "pro", Name: "Acme Pro"}}
	got := Find("Acme Pro is mentioned. Later Acme alone.", brands)
	if len(got) != 2 || got[0].BrandID != "pro" || got[1].BrandID != "acme" || got[1].Start != 29 {
		t.Fatalf("Find = %#v", got)
	}
}

func TestParseListReadsTopLevelItemsAndNames(t *testing.T) {
	answer := `Here are good options:

1. **Helicone** - hosted logging with cost dashboards.
   - Pricing: free tier
2. Langfuse: open-source tracing.
3. [LiteLLM](https://litellm.ai) proxy with budgets
4. Use a spreadsheet if you only have one app.

Pros:
- cheap`
	items := ParseList(answer)
	var names []string
	for _, item := range items {
		names = append(names, item.Name)
	}
	want := []string{"Helicone", "Langfuse", "LiteLLM", ""}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
	if items[1].Position != 2 {
		t.Fatalf("position = %d", items[1].Position)
	}
}

func TestParseListUsesHeadingsWhenPresent(t *testing.T) {
	answer := "### 1. Helicone\nHosted.\n- Pros: fast\n### 2. Tabslip\nLocal receipts."
	items := ParseList(answer)
	if len(items) != 2 || items[1].Name != "Tabslip" {
		t.Fatalf("items = %#v", items)
	}
}

func TestAnalyzeSkipsBrandsNamedInThePrompt(t *testing.T) {
	brands := []Brand{{ID: "you", Name: "Tabslip"}, {ID: "helicone", Name: "Helicone"}}
	got := Analyze("What are alternatives to Tabslip?", "I'm not familiar with Tabslip. Helicone is popular.", brands)
	you, helicone := got.Brands[0], got.Brands[1]
	if !you.Mentioned || !you.NamedInPrompt || you.Counted() {
		t.Fatalf("brand named in the prompt must not count: %#v", you)
	}
	// Tabslip comes first in the text but was named in the prompt, so it
	// takes no place in the order.
	if !helicone.Counted() || helicone.Rank != 1 {
		t.Fatalf("competitor = %#v", helicone)
	}
}

func TestRankIgnoresSectionHeadings(t *testing.T) {
	brands := []Brand{{ID: "acme", Name: "Acme"}, {ID: "other", Name: "Other"}}
	for _, answer := range []string{
		"## Recommendations\n1. Other - fine\n2. Acme - also fine",
		"## Recommendations\n1. Other - fine\n2. Acme - also fine\n## Summary\nPick one.",
		"## Best tools\n### 1. Other\nHosted.\n### 2. Acme\nLocal.\n## Summary\nBoth work.",
	} {
		got := Analyze("Best tools?", answer, brands)
		if got.Brands[0].Rank != 2 || got.Brands[1].Rank != 1 {
			t.Errorf("%q: ranks = %d, %d", answer, got.Brands[0].Rank, got.Brands[1].Rank)
		}
	}
	// Headings that look like product names still work as a list.
	got := Analyze("Best tools?", "## Other\nHosted.\n## Acme\nLocal.\n## Summary\nBoth.", brands)
	if got.Brands[0].Rank != 2 {
		t.Fatalf("rank from name headings = %d", got.Brands[0].Rank)
	}
}

func TestRankSkipsTheEntryForABrandNamedInThePrompt(t *testing.T) {
	brands := []Brand{{ID: "you", Name: "Tabslip"}, {ID: "helicone", Name: "Helicone"}, {ID: "langfuse", Name: "Langfuse"}}
	answer := "1. **Helicone** - what you use now\n2. **Langfuse** - tracing\n3. **Tabslip** - receipts"
	got := Analyze("What are good alternatives to Helicone?", answer, brands)
	if got.Brands[0].Rank != 2 || got.Brands[2].Rank != 1 || got.Brands[1].Rank != 0 {
		t.Fatalf("ranks = %d, %d, %d", got.Brands[0].Rank, got.Brands[1].Rank, got.Brands[2].Rank)
	}
}

func TestOverlappingNamesAreMatchedTogetherEverywhere(t *testing.T) {
	brands := []Brand{{ID: "acme", Name: "Acme"}, {ID: "books", Name: "Acme Books"}}
	got := Analyze("What are alternatives to Acme Books?", "1. Acme Books\n2. Acme\n3. Other", brands)
	acme, books := got.Brands[0], got.Brands[1]
	if acme.NamedInPrompt || !books.NamedInPrompt {
		t.Fatalf("a prompt naming Acme Books also named Acme: %#v %#v", acme, books)
	}
	if acme.Rank != 1 {
		t.Fatalf("Acme rank = %d, want 1 (the Acme Books entry belongs to the prompt's brand)", acme.Rank)
	}
	got = Analyze("Best invoicing tools?", "1. Acme Books\n2. Acme", brands)
	if got.Brands[0].Rank != 2 || got.Brands[1].Rank != 1 {
		t.Fatalf("ranks = %d, %d; Acme Books' entry was counted as Acme", got.Brands[0].Rank, got.Brands[1].Rank)
	}
}

func TestUnknownLeavesOutLabelsAndReadsTables(t *testing.T) {
	answer := `**Quick answer:** most freelancers pick one of these.

### Paid
1. **FreshInvoice** - simple
2. **Pro tip**: export to a spreadsheet
- **Open-source options**
- **German VAT**: needs a tax number
- **EU data residency** matters
- **Cost attribution** per client
- **Automatic budget alerts**

| Tool | Price |
|---|---|
| CostGuard | $10 |
| Bank of America Merchant | free |
`
	got := Analyze("q", answer, nil).Unknown(answer, nil)
	want := []string{"FreshInvoice", "CostGuard", "Bank of America Merchant"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown = %q, want %q", got, want)
	}
}

func TestAnalyzeRanksByListPosition(t *testing.T) {
	brands := []Brand{{ID: "you", Name: "Tabslip"}, {ID: "helicone", Name: "Helicone"}}
	answer := "Tabslip is often compared with these:\n1. **Portkey** - gateway\n2. **Helicone** - logging\n3. **Tabslip** - receipts"
	got := Analyze("Best LLM cost trackers?", answer, brands)
	if got.Brands[0].Rank != 3 || got.Brands[1].Rank != 2 {
		t.Fatalf("ranks = %d, %d", got.Brands[0].Rank, got.Brands[1].Rank)
	}
	unknown := got.Unknown(answer, brands)
	if !reflect.DeepEqual(unknown, []string{"Portkey"}) {
		t.Fatalf("unknown = %q", unknown)
	}
}

func TestAnalyzeLeavesRankEmptyWhenMentionedOutsideTheList(t *testing.T) {
	brands := []Brand{{ID: "you", Name: "Tabslip"}}
	answer := "If Tabslip does not fit, look at:\n- Helicone\n- Langfuse"
	got := Analyze("Cost tracking tools?", answer, brands)
	if !got.Brands[0].Counted() || got.Brands[0].Rank != 0 {
		t.Fatalf("result = %#v", got.Brands[0])
	}
}

func TestUnknownIgnoresSectionLabels(t *testing.T) {
	answer := "1. **Pricing**: varies\n2. **OpenMeter** - metering\n3. Best for teams: Langfuse\n\nAlso **Portkey** works."
	got := Analyze("q", answer, nil).Unknown(answer, nil)
	if !reflect.DeepEqual(got, []string{"OpenMeter", "Portkey"}) {
		t.Fatalf("unknown = %q", got)
	}
}

func TestExcerptShowsTheSentenceAroundTheMention(t *testing.T) {
	text := "Many teams start with logs. 3. **Tabslip** - a local CLI that keeps signed receipts for each call. It is free."
	m := Find(text, []Brand{{ID: "you", Name: "Tabslip"}})[0]
	got := Excerpt(text, m, 200)
	if got != "Tabslip - a local CLI that keeps signed receipts for each call." {
		t.Fatalf("excerpt = %q", got)
	}
	if short := Excerpt(text, m, 30); len(short) > 40 || short[len(short)-3:] != "…" {
		t.Fatalf("short excerpt = %q", short)
	}
}
