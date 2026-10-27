package engine

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestToolPolicyParseValidationAndText(t *testing.T) {
	tests := []struct {
		input string
		want  ToolPolicy
	}{
		{input: "memory", want: ToolPolicyMemory},
		{input: "provider_default", want: ToolPolicyProviderDefault},
		{input: "search_auto", want: ToolPolicySearchAuto},
		{input: "search_required", want: ToolPolicySearchRequired},
		{input: "search_cache_only", want: ToolPolicySearchCacheOnly},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseToolPolicy(tt.input)
			if err != nil {
				t.Fatalf("ParseToolPolicy() error = %v", err)
			}
			if got != tt.want || got.String() != tt.input || !got.Valid() {
				t.Fatalf("policy = %q valid=%v, want %q", got, got.Valid(), tt.want)
			}
			text, err := got.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText() error = %v", err)
			}
			var roundTrip ToolPolicy
			if err := roundTrip.UnmarshalText(text); err != nil {
				t.Fatalf("UnmarshalText() error = %v", err)
			}
			if roundTrip != got {
				t.Fatalf("roundTrip = %q, want %q", roundTrip, got)
			}
		})
	}
	if _, err := ParseToolPolicy("search_sometimes"); err == nil {
		t.Fatal("ParseToolPolicy() succeeded for unknown policy")
	}
}

func TestCapabilityProfileSupportsMode(t *testing.T) {
	caps := CapabilityProfile{
		SupportsMemory:          true,
		SupportsSearchAuto:      true,
		SupportsSearchRequired:  false,
		SupportsSearchCacheOnly: true,
	}
	tests := []struct {
		mode ToolPolicy
		want bool
	}{
		{mode: ToolPolicyProviderDefault, want: true},
		{mode: ToolPolicyMemory, want: true},
		{mode: ToolPolicySearchAuto, want: true},
		{mode: ToolPolicySearchRequired, want: false},
		{mode: ToolPolicySearchCacheOnly, want: true},
		{mode: ToolPolicy("bad"), want: false},
	}
	for _, tt := range tests {
		if got := caps.SupportsMode(tt.mode); got != tt.want {
			t.Fatalf("SupportsMode(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestSearchOptionsAndLocationZero(t *testing.T) {
	if !((*SearchOptions)(nil)).IsZero() {
		t.Fatal("nil SearchOptions should be zero")
	}
	if !(&SearchOptions{}).IsZero() {
		t.Fatal("empty SearchOptions should be zero")
	}
	if (&SearchOptions{UserLocation: &SearchLocation{}}).IsZero() {
		t.Fatal("non-nil empty SearchLocation should make SearchOptions non-zero")
	}
	if !((*SearchLocation)(nil)).IsZero() || !(&SearchLocation{}).IsZero() {
		t.Fatal("nil and empty SearchLocation should be zero")
	}
	if (&SearchLocation{CountryCode: "US"}).IsZero() {
		t.Fatal("populated SearchLocation should be non-zero")
	}
}

func TestProviderErrorAndRawCapture(t *testing.T) {
	retryAfter := 2 * time.Second
	err := &ProviderError{
		ProviderID:  "openai",
		RequestID:   "req-1",
		StatusCode:  429,
		SafeMessage: "rate limited",
		Class:       ErrorClassTransient,
		Retryable:   true,
		RetryAfter:  &retryAfter,
		SafeSnippet: SafeResponseSnippet([]byte(strings.Repeat("x", MaxProviderErrorSnippetBytes+10))),
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatal("errors.As did not classify ProviderError")
	}
	if err.Error() != "openai: rate limited (request req-1)" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if len(err.SafeSnippet) != MaxProviderErrorSnippetBytes {
		t.Fatalf("snippet len = %d, want %d", len(err.SafeSnippet), MaxProviderErrorSnippetBytes)
	}

	raw := []byte(strings.Repeat("a", MaxRawPayloadBytes+1))
	captured, metadata := CaptureRawPayload(raw, map[string]string{"fixture": "true"})
	if len(captured) != MaxRawPayloadBytes {
		t.Fatalf("captured len = %d, want %d", len(captured), MaxRawPayloadBytes)
	}
	if metadata["raw_truncated"] != "true" || metadata["fixture"] != "true" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
