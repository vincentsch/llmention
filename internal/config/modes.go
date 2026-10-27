package config

import (
	"fmt"
	"strings"
)

// Answer modes. Each prompt is sent once per mode, so one run can show what a
// model says from memory and what it says after a web search.
const (
	ModeMemory          = "memory"
	ModeProviderDefault = "provider_default"
	ModeSearchAuto      = "search_auto"
	ModeSearchRequired  = "search_required"
	ModeSearchCacheOnly = "search_cache_only"
)

var modeAliases = map[string]string{
	"memory":            ModeMemory,
	"no-search":         ModeMemory,
	"no_search":         ModeMemory,
	"provider_default":  ModeProviderDefault,
	"provider-default":  ModeProviderDefault,
	"default":           ModeProviderDefault,
	"search_auto":       ModeSearchAuto,
	"search-auto":       ModeSearchAuto,
	"search":            ModeSearchAuto,
	"search_required":   ModeSearchRequired,
	"search-required":   ModeSearchRequired,
	"forced-search":     ModeSearchRequired,
	"search_cache_only": ModeSearchCacheOnly,
	"search-cache-only": ModeSearchCacheOnly,
}

var modeLabels = map[string]string{
	ModeMemory:          "no web search",
	ModeProviderDefault: "provider default",
	ModeSearchAuto:      "web search",
	ModeSearchRequired:  "forced web search",
	ModeSearchCacheOnly: "cached search",
}

// CanonicalMode turns a mode name or one of its short aliases ("search",
// "no-search", "forced-search", "default") into the stored mode id.
func CanonicalMode(value string) (string, error) {
	mode, ok := modeAliases[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return "", fmt.Errorf("unknown mode %q; use memory (no-search), search_auto (search), search_required (forced-search) or provider_default (default)", value)
	}
	return mode, nil
}

// ModeLabel is the plain-words name of a mode for reports.
func ModeLabel(mode string) string {
	if label, ok := modeLabels[mode]; ok {
		return label
	}
	return mode
}

// IsSearchMode reports whether a mode lets the model search the web.
func IsSearchMode(mode string) bool {
	return mode == ModeSearchAuto || mode == ModeSearchRequired || mode == ModeSearchCacheOnly
}
