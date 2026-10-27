package benchmark

import (
	"testing"

	"github.com/vincentsch/llmention/internal/engine"
)

func TestTemporalSourcesRequireCoherentIdentityBeforeCountingDates(t *testing.T) {
	expectation := SourceExpectation{RequiredForModes: []string{"search_required"}, FreshnessReferenceDate: "2026-01-01"}
	for _, tc := range []struct {
		name, url, domain, date string
		met, fresh              bool
		dated, invalid          int
	}{
		{"date only", "", "", "2026-02-01", false, false, 0, 0},
		{"contradictory", "https://other.invalid/page", "good.invalid", "2026-02-01", false, false, 0, 0},
		{"malformed URL with valid domain", "://invalid", "good.invalid", "2026-02-01", false, false, 0, 0},
		{"invalid domain", "https://good.invalid/page", "*.good.invalid", "2026-02-01", false, false, 0, 0},
		{"coherent", "https://GOOD.invalid:443/page", "good.invalid", "2026-02-01", true, true, 1, 0},
		{"domain only", "", "good.invalid", "2026-02-01", true, true, 1, 0},
		{"URL only", "https://good.invalid/page", "", "2026-02-01", true, true, 1, 0},
		{"no date", "https://good.invalid/page", "good.invalid", "", true, false, 0, 0},
		{"invalid date", "https://good.invalid/page", "good.invalid", "yesterday", true, false, 0, 1},
		{"invalid identity and date", "", "", "yesterday", false, false, 0, 0},
		{"stale", "https://good.invalid/page", "good.invalid", "2025-12-31", true, false, 1, 0},
	} {
		for _, channel := range []string{"citation", "consulted"} {
			t.Run(tc.name+"/"+channel, func(t *testing.T) {
				caps := engine.CapabilityProfile{}
				answer := &engine.Answer{}
				metadata := map[string]string{"date": tc.date}
				if channel == "citation" {
					caps.ExposesCitations = true
					answer.Citations = []engine.Citation{{URL: tc.url, Domain: tc.domain, Metadata: metadata}}
				} else {
					caps.ExposesConsultedSources = true
					answer.SourcesConsulted = []engine.Source{{URL: tc.url, Domain: tc.domain, Metadata: metadata}}
				}
				got := DeriveSourceEvidence(answer, caps, "search_required", expectation)
				if (got.RequirementStatus == SourceStatusMet) != tc.met || (got.FreshnessStatus == FreshnessFresh) != tc.fresh || got.ValidDatedSourceCount != tc.dated || got.InvalidDatedSourceCount != tc.invalid {
					t.Fatalf("evidence=%+v", got)
				}
				// Sources count only when the engine reports them and the mode requires them.
				hidden := DeriveSourceEvidence(answer, engine.CapabilityProfile{}, "search_required", expectation)
				if hidden.RequirementStatus != SourceStatusUnavailable || hidden.FreshnessStatus != FreshnessUnavailable || hidden.ValidDatedSourceCount != 0 {
					t.Fatalf("hidden=%+v", hidden)
				}
				optional := DeriveSourceEvidence(answer, caps, "memory", expectation)
				if optional.RequirementStatus != SourceStatusNotRequired || optional.FreshnessStatus != FreshnessNotRequired {
					t.Fatalf("optional=%+v", optional)
				}
			})
		}
	}
	for _, answer := range []*engine.Answer{nil, {}, {Citations: []engine.Citation{}, SourcesConsulted: []engine.Source{}}} {
		got := DeriveSourceEvidence(answer, engine.CapabilityProfile{ExposesCitations: true, ExposesConsultedSources: true}, "search_required", expectation)
		if got.RequirementStatus != SourceStatusMissing || got.FreshnessStatus != FreshnessNoValidDate {
			t.Fatalf("empty evidence=%+v", got)
		}
	}
}
