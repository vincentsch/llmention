package engine

import (
	"reflect"
	"strings"
	"testing"
)

func fullCaps() CapabilityProfile {
	return CapabilityProfile{
		Provider:                              "mock",
		Model:                                 "fixture",
		SupportsMemory:                        true,
		SupportsSearchAuto:                    true,
		SupportsSearchRequired:                true,
		SupportsSearchCacheOnly:               true,
		SupportsLiveAccessControl:             true,
		SupportsRequiredToolChoice:            true,
		SupportsSearchAllowDomains:            true,
		SupportsSearchBlockDomains:            true,
		SupportsSearchLocation:                true,
		SupportsSearchRecency:                 true,
		SupportsSearchDateFilters:             true,
		SupportsSearchMaxQueries:              true,
		SupportsSourceMetadataRequest:         true,
		ExposesCitations:                      true,
		SupportsStructuredOutput:              true,
		SupportsStructuredOutputWithTools:     true,
		SupportsStructuredOutputWithCitations: true,
	}
}

func validRequest(mode ToolPolicy) Request {
	return Request{
		SuiteID:      "visibility.basic",
		SuiteVersion: 1,
		ItemID:       "visibility.example",
		VariantID:    "direct",
		Mode:         mode,
		Prompt:       "Who mentions Acme?",
	}
}

func TestValidateRequestModeSearchTable(t *testing.T) {
	tests := []struct {
		name string
		mode ToolPolicy
		opts *SearchOptions
		want string
	}{
		{
			name: "memory rejects search options",
			mode: ToolPolicyMemory,
			opts: &SearchOptions{AllowedDomains: []string{"example.com"}},
			want: "not allowed",
		},
		{
			name: "provider default rejects live access",
			mode: ToolPolicyProviderDefault,
			opts: &SearchOptions{AllowLiveAccess: true},
			want: "not allowed",
		},
		{
			name: "search auto rejects require search",
			mode: ToolPolicySearchAuto,
			opts: &SearchOptions{RequireSearch: true},
			want: "require_search is not allowed",
		},
		{
			name: "search auto rejects cache only",
			mode: ToolPolicySearchAuto,
			opts: &SearchOptions{CacheOnly: true},
			want: "cache_only is not allowed",
		},
		{
			name: "search required rejects cache only",
			mode: ToolPolicySearchRequired,
			opts: &SearchOptions{CacheOnly: true},
			want: "cache_only is not allowed",
		},
		{
			name: "search cache only rejects live access",
			mode: ToolPolicySearchCacheOnly,
			opts: &SearchOptions{AllowLiveAccess: true},
			want: "allow_live_access is not allowed",
		},
		{
			name: "search required allows redundant require search",
			mode: ToolPolicySearchRequired,
			opts: &SearchOptions{RequireSearch: true},
		},
		{
			name: "search cache only allows redundant cache only",
			mode: ToolPolicySearchCacheOnly,
			opts: &SearchOptions{CacheOnly: true},
		},
		{
			name: "search auto allows shaped search when capabilities exist",
			mode: ToolPolicySearchAuto,
			opts: &SearchOptions{
				AllowedDomains:        []string{"https://Example.com/path", "example.com."},
				BlockedDomains:        []string{"*.blocked.example"},
				Locale:                "en-US",
				UserLocation:          &SearchLocation{CountryCode: "US", Region: "CA", City: "San Francisco", Timezone: "America/Los_Angeles"},
				RecencyDays:           7,
				DateFrom:              "2026-08-01",
				DateTo:                "2026-08-11",
				MaxSearches:           3,
				AllowLiveAccess:       true,
				IncludeSourceMetadata: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest(tt.mode)
			req.SearchOptions = tt.opts
			err := ValidateRequest(fullCaps(), req)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("ValidateRequest() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRequest() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRequestSearchOptionCapabilityGates(t *testing.T) {
	tests := []struct {
		name       string
		opts       SearchOptions
		disableCap func(*CapabilityProfile)
		want       string
	}{
		{
			name: "allowed domains",
			opts: SearchOptions{AllowedDomains: []string{"example.com"}},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchAllowDomains = false
			},
			want: "allowed search domains",
		},
		{
			name: "blocked domains",
			opts: SearchOptions{BlockedDomains: []string{"example.com"}},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchBlockDomains = false
			},
			want: "blocked search domains",
		},
		{
			name: "locale",
			opts: SearchOptions{Locale: "de-DE"},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchLocation = false
			},
			want: "search locale",
		},
		{
			name: "location",
			opts: SearchOptions{UserLocation: &SearchLocation{CountryCode: "DE"}},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchLocation = false
			},
			want: "user location",
		},
		{
			name: "recency",
			opts: SearchOptions{RecencyDays: 2},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchRecency = false
			},
			want: "recency",
		},
		{
			name: "date filters",
			opts: SearchOptions{DateFrom: "2026-08-01"},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchDateFilters = false
			},
			want: "date filters",
		},
		{
			name: "max searches",
			opts: SearchOptions{MaxSearches: 1},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSearchMaxQueries = false
			},
			want: "max queries",
		},
		{
			name: "source metadata",
			opts: SearchOptions{IncludeSourceMetadata: true},
			disableCap: func(c *CapabilityProfile) {
				c.SupportsSourceMetadataRequest = false
			},
			want: "source metadata",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := fullCaps()
			tt.disableCap(&caps)
			req := validRequest(ToolPolicySearchAuto)
			req.SearchOptions = &tt.opts
			err := ValidateRequest(caps, req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRequest() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRequestRequiredToolChoiceAndUnsupportedMode(t *testing.T) {
	caps := fullCaps()
	caps.SupportsSearchRequired = false
	req := validRequest(ToolPolicySearchRequired)
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "does not support mode") {
		t.Fatalf("unsupported mode error = %v", err)
	}

	caps = fullCaps()
	caps.SupportsRequiredToolChoice = false
	err := ValidateRequest(caps, req)
	if err == nil || !strings.Contains(err.Error(), "force a tool call") {
		t.Fatalf("required tool choice error = %v", err)
	}
}

func TestValidateRequestSearchOptionShapes(t *testing.T) {
	tests := []struct {
		name string
		opts SearchOptions
		want string
	}{
		{name: "bad allowed domain", opts: SearchOptions{AllowedDomains: []string{"bad_domain.example"}}, want: "allowed search domains"},
		{name: "bad locale", opts: SearchOptions{Locale: "en_US"}, want: "locale"},
		{name: "empty location", opts: SearchOptions{UserLocation: &SearchLocation{}}, want: "at least one field"},
		{name: "bad country", opts: SearchOptions{UserLocation: &SearchLocation{CountryCode: "USA"}}, want: "two-letter"},
		{name: "bad recency", opts: SearchOptions{RecencyDays: -1}, want: "recency"},
		{name: "bad date shape", opts: SearchOptions{DateFrom: "2026-8-1"}, want: "YYYY-MM-DD"},
		{name: "bad date order", opts: SearchOptions{DateFrom: "2026-08-11", DateTo: "2026-08-01"}, want: "on or before"},
		{name: "bad max", opts: SearchOptions{MaxSearches: -1}, want: "max searches"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest(ToolPolicySearchAuto)
			req.SearchOptions = &tt.opts
			err := ValidateRequest(fullCaps(), req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRequest() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNormalizeSearchOptionsUsesScoringDomainRules(t *testing.T) {
	opts := &SearchOptions{
		AllowedDomains: []string{"https://Example.com/path", "example.com."},
		BlockedDomains: []string{"*.Blocked.Example."},
		UserLocation:   &SearchLocation{CountryCode: "US"},
	}
	normalized, err := NormalizeSearchOptions(opts)
	if err != nil {
		t.Fatalf("NormalizeSearchOptions() error = %v", err)
	}
	if got, want := normalized.AllowedDomains, []string{"example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed domains = %#v, want %#v", got, want)
	}
	if got, want := normalized.BlockedDomains, []string{"*.blocked.example"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked domains = %#v, want %#v", got, want)
	}
	normalized.UserLocation.CountryCode = "DE"
	if opts.UserLocation.CountryCode != "US" {
		t.Fatalf("NormalizeSearchOptions aliased user location: %#v", opts.UserLocation)
	}
}

func TestValidateRequestOutputSchemaStrictAndNonStrict(t *testing.T) {
	schema := &OutputSchema{
		Name:       "answer",
		Format:     OutputFormatJSONSchema,
		SchemaJSON: []byte(`{"type":"object"}`),
		Strict:     true,
	}
	req := validRequest(ToolPolicySearchAuto)
	req.OutputSchema = schema
	if err := ValidateRequest(fullCaps(), req); err != nil {
		t.Fatalf("strict search schema ValidateRequest() error = %v", err)
	}

	caps := fullCaps()
	caps.SupportsStructuredOutputWithTools = false
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "with tools") {
		t.Fatalf("strict tools error = %v", err)
	}

	caps = fullCaps()
	caps.SupportsStructuredOutputWithCitations = false
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "with citations") {
		t.Fatalf("strict citations error = %v", err)
	}

	caps = fullCaps()
	caps.SupportsStructuredOutput = false
	req.Mode = ToolPolicyMemory
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "strict structured output") {
		t.Fatalf("strict native error = %v", err)
	}

	req.OutputSchema.Strict = false
	if err := ValidateRequest(caps, req); err != nil {
		t.Fatalf("non-strict schema should allow prompt-level fallback: %v", err)
	}
	req.OutputSchema.Format = "xml"
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("schema format error = %v", err)
	}
	req.OutputSchema.Format = OutputFormatJSONSchema
	req.OutputSchema.SchemaJSON = []byte(`{`)
	if err := ValidateRequest(caps, req); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("schema JSON error = %v", err)
	}
}
