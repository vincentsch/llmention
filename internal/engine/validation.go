package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/domain"
)

var (
	exactDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	localePattern    = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	countryPattern   = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

// ValidateRequest checks a request against what the engine can do, before
// anything is sent.
func ValidateRequest(profile CapabilityProfile, req Request) error {
	if !req.Mode.Valid() {
		return fmt.Errorf("request mode %q is not supported", req.Mode)
	}
	if !profile.SupportsMode(req.Mode) {
		return fmt.Errorf("provider %q model %q does not support mode %q", profile.Provider, profile.Model, req.Mode)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("request prompt must not be empty")
	}
	if req.MaxOutputTokens < 0 {
		return fmt.Errorf("request max output tokens must be positive when set")
	}
	if err := validateOutputSchema(profile, req); err != nil {
		return err
	}
	if err := validateSearchOptions(profile, req.Mode, req.SearchOptions); err != nil {
		return err
	}
	return nil
}

func validateOutputSchema(profile CapabilityProfile, req Request) error {
	schema := req.OutputSchema
	if schema == nil {
		return nil
	}
	if strings.TrimSpace(schema.Name) == "" {
		return fmt.Errorf("output schema name must not be empty")
	}
	if schema.Format != OutputFormatJSONSchema {
		return fmt.Errorf("output schema format %q is unsupported; supported format is %q", schema.Format, OutputFormatJSONSchema)
	}
	if len(schema.SchemaJSON) == 0 {
		return fmt.Errorf("output schema JSON must not be empty")
	}
	if !json.Valid(schema.SchemaJSON) {
		return fmt.Errorf("output schema JSON is invalid")
	}
	if !schema.Strict {
		return nil
	}
	if !profile.SupportsStructuredOutput {
		return fmt.Errorf("provider %q model %q cannot enforce strict structured output", profile.Provider, profile.Model)
	}
	if req.Mode.IsSearchMode() && !profile.SupportsStructuredOutputWithTools {
		return fmt.Errorf("provider %q model %q cannot enforce strict structured output with tools", profile.Provider, profile.Model)
	}
	if strictOutputMayIncludeCitations(profile, req.Mode) && !profile.SupportsStructuredOutputWithCitations {
		return fmt.Errorf("provider %q model %q cannot enforce strict structured output with citations", profile.Provider, profile.Model)
	}
	return nil
}

func strictOutputMayIncludeCitations(profile CapabilityProfile, mode ToolPolicy) bool {
	if !profile.ExposesCitations {
		return false
	}
	if mode.IsSearchMode() {
		return true
	}
	return mode == ToolPolicyProviderDefault && profile.InherentlySearchGrounded
}

func validateSearchOptions(profile CapabilityProfile, mode ToolPolicy, opts *SearchOptions) error {
	if opts == nil {
		if mode == ToolPolicySearchRequired && !profile.SupportsRequiredToolChoice {
			return fmt.Errorf("mode %q needs a provider that can force a tool call", mode)
		}
		return nil
	}
	if err := validateSearchOptionShape(opts); err != nil {
		return err
	}

	switch mode {
	case ToolPolicyMemory, ToolPolicyProviderDefault:
		if !opts.IsZero() {
			return fmt.Errorf("search options are not allowed in %q mode", mode)
		}
		return nil
	case ToolPolicySearchAuto:
		if opts.RequireSearch {
			return fmt.Errorf("require_search is not allowed in %q mode; use %q", mode, ToolPolicySearchRequired)
		}
		if opts.CacheOnly {
			return fmt.Errorf("cache_only is not allowed in %q mode; use %q", mode, ToolPolicySearchCacheOnly)
		}
	case ToolPolicySearchRequired:
		if !profile.SupportsRequiredToolChoice {
			return fmt.Errorf("mode %q needs a provider that can force a tool call", mode)
		}
		if opts.CacheOnly {
			return fmt.Errorf("cache_only is not allowed in %q mode; use %q", mode, ToolPolicySearchCacheOnly)
		}
	case ToolPolicySearchCacheOnly:
		if opts.AllowLiveAccess {
			return fmt.Errorf("allow_live_access is not allowed in %q mode", mode)
		}
		if opts.RequireSearch && !profile.SupportsRequiredToolChoice {
			return fmt.Errorf("require_search in %q mode needs a provider that can force a tool call", mode)
		}
	}

	if len(opts.AllowedDomains) > 0 && !profile.SupportsSearchAllowDomains {
		return fmt.Errorf("allowed search domains are not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if len(opts.BlockedDomains) > 0 && !profile.SupportsSearchBlockDomains {
		return fmt.Errorf("blocked search domains are not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.Locale != "" && !profile.SupportsSearchLocation {
		return fmt.Errorf("search locale is not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.UserLocation != nil && !profile.SupportsSearchLocation {
		return fmt.Errorf("search user location is not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.RecencyDays > 0 && !profile.SupportsSearchRecency {
		return fmt.Errorf("search recency is not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if (opts.DateFrom != "" || opts.DateTo != "") && !profile.SupportsSearchDateFilters {
		return fmt.Errorf("search date filters are not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.MaxSearches > 0 && !profile.SupportsSearchMaxQueries {
		return fmt.Errorf("search max queries are not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.CacheOnly && !profile.SupportsSearchCacheOnly {
		return fmt.Errorf("cache-only search is not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.AllowLiveAccess && !profile.SupportsLiveAccessControl {
		return fmt.Errorf("live access control is not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	if opts.IncludeSourceMetadata && !profile.SupportsSourceMetadataRequest {
		return fmt.Errorf("source metadata requests are not supported by provider %q model %q", profile.Provider, profile.Model)
	}
	return nil
}

func validateSearchOptionShape(opts *SearchOptions) error {
	if _, err := NormalizeSearchOptions(opts); err != nil {
		return err
	}
	if opts.Locale != "" && !localePattern.MatchString(opts.Locale) {
		return fmt.Errorf("search locale %q is invalid", opts.Locale)
	}
	if opts.UserLocation != nil {
		if err := validateSearchLocation(opts.UserLocation); err != nil {
			return err
		}
	}
	if opts.RecencyDays < 0 {
		return fmt.Errorf("search recency days must be positive when set")
	}
	if opts.MaxSearches < 0 {
		return fmt.Errorf("search max searches must be positive when set")
	}
	from, err := parseSearchDate("date_from", opts.DateFrom)
	if err != nil {
		return err
	}
	to, err := parseSearchDate("date_to", opts.DateTo)
	if err != nil {
		return err
	}
	if from != nil && to != nil && from.After(*to) {
		return fmt.Errorf("search date_from must be on or before date_to")
	}
	return nil
}

// NormalizeSearchOptions returns a copy of the options with the domain lists
// cleaned up, or nil when there are none.
func NormalizeSearchOptions(opts *SearchOptions) (*SearchOptions, error) {
	if opts == nil {
		return nil, nil
	}
	normalized, err := opts.Normalized()
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func (o SearchOptions) Normalized() (SearchOptions, error) {
	out := o
	allowed, err := domain.NormalizeScoringDomains(o.AllowedDomains)
	if err != nil {
		return SearchOptions{}, fmt.Errorf("allowed search domains are invalid: %w", err)
	}
	blocked, err := domain.NormalizeScoringDomains(o.BlockedDomains)
	if err != nil {
		return SearchOptions{}, fmt.Errorf("blocked search domains are invalid: %w", err)
	}
	out.AllowedDomains = allowed
	out.BlockedDomains = blocked
	if o.UserLocation != nil {
		location := *o.UserLocation
		out.UserLocation = &location
	}
	return out, nil
}

func validateSearchLocation(location *SearchLocation) error {
	if location.IsZero() {
		return fmt.Errorf("search user location must include at least one field")
	}
	if strings.TrimSpace(location.CountryCode) != location.CountryCode ||
		strings.TrimSpace(location.Region) != location.Region ||
		strings.TrimSpace(location.City) != location.City ||
		strings.TrimSpace(location.Timezone) != location.Timezone {
		return fmt.Errorf("search user location fields must not contain surrounding whitespace")
	}
	if location.CountryCode != "" && !countryPattern.MatchString(location.CountryCode) {
		return fmt.Errorf("search user location country code %q must be a two-letter country code", location.CountryCode)
	}
	return nil
}

func parseSearchDate(field, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if !exactDatePattern.MatchString(value) {
		return nil, fmt.Errorf("search %s %q must use YYYY-MM-DD", field, value)
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, fmt.Errorf("search %s %q is invalid: %w", field, value, err)
	}
	return &parsed, nil
}
