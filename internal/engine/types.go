// Package engine talks to AI providers. Each adapter turns a request into the
// provider's API call and the reply into an Answer with its text, citations
// and search calls. The mock provider writes demo answers for tests and the
// demo command.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vincentsch/llmention/internal/config"
)

// ToolPolicy is a mode: it says whether the model may search the web before
// it answers. The values are the mode names in llmention.yaml.
type ToolPolicy string

const (
	ToolPolicyMemory          ToolPolicy = "memory"
	ToolPolicyProviderDefault ToolPolicy = "provider_default"
	ToolPolicySearchAuto      ToolPolicy = "search_auto"
	ToolPolicySearchRequired  ToolPolicy = "search_required"
	// ToolPolicySearchCacheOnly lets OpenAI search only its cached pages,
	// without fetching anything live.
	ToolPolicySearchCacheOnly ToolPolicy = "search_cache_only"
)

// ParseToolPolicy reads a mode id or one of its short aliases such as
// "search" or "no-search".
func ParseToolPolicy(value string) (ToolPolicy, error) {
	mode, err := config.CanonicalMode(value)
	if err != nil {
		return "", err
	}
	return ToolPolicy(mode), nil
}

func (p ToolPolicy) String() string {
	return string(p)
}

func (p ToolPolicy) Valid() bool {
	switch p {
	case ToolPolicyMemory, ToolPolicyProviderDefault, ToolPolicySearchAuto, ToolPolicySearchRequired, ToolPolicySearchCacheOnly:
		return true
	default:
		return false
	}
}

func (p ToolPolicy) IsSearchMode() bool {
	switch p {
	case ToolPolicySearchAuto, ToolPolicySearchRequired, ToolPolicySearchCacheOnly:
		return true
	default:
		return false
	}
}

func (p ToolPolicy) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("unknown tool policy %q", string(p))
	}
	return []byte(p), nil
}

func (p *ToolPolicy) UnmarshalText(text []byte) error {
	policy, err := ParseToolPolicy(string(text))
	if err != nil {
		return err
	}
	*p = policy
	return nil
}

// CapabilityProfile says what an engine can do. The planner leaves out the
// modes an engine cannot handle, and requests are checked against it before
// they are sent. capabilities in llmention.yaml can change it.
type CapabilityProfile struct {
	Provider                              string `json:"provider"`
	Model                                 string `json:"model"`
	SupportsMemory                        bool   `json:"supports_memory"`
	SupportsSearchAuto                    bool   `json:"supports_search_auto"`
	SupportsSearchRequired                bool   `json:"supports_search_required"`
	SupportsSearchCacheOnly               bool   `json:"supports_search_cache_only"`
	SupportsLiveAccessControl             bool   `json:"supports_live_access_control"`
	SupportsRequiredToolChoice            bool   `json:"supports_required_tool_choice"`
	SupportsSearchAllowDomains            bool   `json:"supports_search_allow_domains"`
	SupportsSearchBlockDomains            bool   `json:"supports_search_block_domains"`
	SupportsSearchLocation                bool   `json:"supports_search_location"`
	SupportsSearchRecency                 bool   `json:"supports_search_recency"`
	SupportsSearchDateFilters             bool   `json:"supports_search_date_filters"`
	SupportsSearchMaxQueries              bool   `json:"supports_search_max_queries"`
	SupportsSourceMetadataRequest         bool   `json:"supports_source_metadata_request"`
	InherentlySearchGrounded              bool   `json:"inherently_search_grounded"`
	ExposesCitations                      bool   `json:"exposes_citations"`
	ExposesConsultedSources               bool   `json:"exposes_consulted_sources"`
	ExposesToolCalls                      bool   `json:"exposes_tool_calls"`
	SupportsStructuredOutput              bool   `json:"supports_structured_output"`
	SupportsStructuredOutputWithTools     bool   `json:"supports_structured_output_with_tools"`
	SupportsStructuredOutputWithCitations bool   `json:"supports_structured_output_with_citations"`
	KnowledgeCutoff                       string `json:"knowledge_cutoff,omitempty"`
}

func (c CapabilityProfile) SupportsMode(mode ToolPolicy) bool {
	switch mode {
	case ToolPolicyProviderDefault:
		return true
	case ToolPolicyMemory:
		return c.SupportsMemory
	case ToolPolicySearchAuto:
		return c.SupportsSearchAuto
	case ToolPolicySearchRequired:
		return c.SupportsSearchRequired
	case ToolPolicySearchCacheOnly:
		return c.SupportsSearchCacheOnly
	default:
		return false
	}
}

func (c CapabilityProfile) ValidateRequest(req Request) error {
	return ValidateRequest(c, req)
}

// Engine is one assistant to ask: a provider and a model.
type Engine interface {
	ID() string
	Provider() string
	Model() string
	Capabilities() CapabilityProfile
	ValidateRequest(req Request) error
	Ask(ctx context.Context, req Request) (Answer, error)
}

const OutputFormatJSONSchema = "json_schema"

// OutputSchema asks the model for a JSON answer in a given shape. With Strict,
// the provider must enforce the shape, and an answer that is not valid JSON
// is an error.
type OutputSchema struct {
	Name       string `json:"name" yaml:"name"`
	Format     string `json:"format" yaml:"format"`
	SchemaJSON []byte `json:"schema_json" yaml:"schema_json"`
	Strict     bool   `json:"strict" yaml:"strict"`
}

// SearchOptions are extra settings for the web search tool. The planner sets
// some of them from the mode, such as RequireSearch for forced web search.
type SearchOptions struct {
	AllowedDomains        []string        `json:"allowed_domains,omitempty" yaml:"allowed_domains"`
	BlockedDomains        []string        `json:"blocked_domains,omitempty" yaml:"blocked_domains"`
	Locale                string          `json:"locale,omitempty" yaml:"locale"`
	UserLocation          *SearchLocation `json:"user_location,omitempty" yaml:"user_location"`
	RecencyDays           int             `json:"recency_days,omitempty" yaml:"recency_days"`
	DateFrom              string          `json:"date_from,omitempty" yaml:"date_from"`
	DateTo                string          `json:"date_to,omitempty" yaml:"date_to"`
	MaxSearches           int             `json:"max_searches,omitempty" yaml:"max_searches"`
	RequireSearch         bool            `json:"require_search,omitempty" yaml:"require_search"`
	CacheOnly             bool            `json:"cache_only,omitempty" yaml:"cache_only"`
	AllowLiveAccess       bool            `json:"allow_live_access,omitempty" yaml:"allow_live_access"`
	IncludeSourceMetadata bool            `json:"include_source_metadata,omitempty" yaml:"include_source_metadata"`
}

func (o *SearchOptions) IsZero() bool {
	if o == nil {
		return true
	}
	return len(o.AllowedDomains) == 0 &&
		len(o.BlockedDomains) == 0 &&
		o.Locale == "" &&
		o.UserLocation == nil &&
		o.RecencyDays == 0 &&
		o.DateFrom == "" &&
		o.DateTo == "" &&
		o.MaxSearches == 0 &&
		!o.RequireSearch &&
		!o.CacheOnly &&
		!o.AllowLiveAccess &&
		!o.IncludeSourceMetadata
}

type SearchLocation struct {
	CountryCode string `json:"country_code,omitempty" yaml:"country_code"`
	Region      string `json:"region,omitempty" yaml:"region"`
	City        string `json:"city,omitempty" yaml:"city"`
	Timezone    string `json:"timezone,omitempty" yaml:"timezone"`
}

func (l *SearchLocation) IsZero() bool {
	if l == nil {
		return true
	}
	return l.CountryCode == "" && l.Region == "" && l.City == "" && l.Timezone == ""
}

// Request is one prompt sent to one engine in one mode.
type Request struct {
	SuiteID         string            `json:"suite_id,omitempty"`
	SuiteVersion    int               `json:"suite_version,omitempty"`
	ItemID          string            `json:"item_id,omitempty"`
	VariantID       string            `json:"variant_id,omitempty"`
	Mode            ToolPolicy        `json:"mode"`
	Prompt          string            `json:"prompt"`
	OutputSchema    *OutputSchema     `json:"output_schema,omitempty"`
	SearchOptions   *SearchOptions    `json:"search_options,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Answer is what a provider sent back, in the same shape for every provider.
type Answer struct {
	Text             string          `json:"text"`
	Parsed           json.RawMessage `json:"parsed,omitempty"`
	Citations        []Citation      `json:"citations,omitempty"`
	SourcesConsulted []Source        `json:"sources_consulted,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolsAvailable   []string        `json:"tools_available,omitempty"`
	ToolsUsed        []string        `json:"tools_used,omitempty"`
	Raw              []byte          `json:"raw,omitempty"`
	StopReason       string          `json:"stop_reason,omitempty"`
	// Unfinished means the provider stopped before the answer was complete,
	// for example at the output token limit or when it paused a long web
	// search. StopReason says which.
	Unfinished       bool              `json:"unfinished,omitempty"`
	ProviderMetadata map[string]string `json:"provider_metadata,omitempty"`
}

// Citation is a page the answer links to.
type Citation struct {
	URL      string            `json:"url,omitempty" yaml:"url"`
	Title    string            `json:"title,omitempty" yaml:"title"`
	Domain   string            `json:"domain,omitempty" yaml:"domain"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata"`
}

// Source is a page the model read while it searched. It may or may not be
// cited in the answer.
type Source struct {
	URL      string            `json:"url,omitempty" yaml:"url"`
	Title    string            `json:"title,omitempty" yaml:"title"`
	Domain   string            `json:"domain,omitempty" yaml:"domain"`
	Kind     string            `json:"kind,omitempty" yaml:"kind"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata"`
}

// ToolCall is one use of a tool, usually a web search, with its query.
type ToolCall struct {
	Type               string `json:"type,omitempty" yaml:"type"`
	Query              string `json:"query,omitempty" yaml:"query"`
	URL                string `json:"url,omitempty" yaml:"url"`
	Status             string `json:"status,omitempty" yaml:"status"`
	ProviderToolCallID string `json:"provider_tool_call_id,omitempty" yaml:"provider_tool_call_id"`
	ErrorCode          string `json:"error_code,omitempty" yaml:"error_code"`
	ErrorMessage       string `json:"error_message,omitempty" yaml:"error_message"`
	Raw                []byte `json:"raw,omitempty" yaml:"raw"`
}
