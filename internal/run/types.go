// Package run turns suites into requests and sends them. Each prompt goes to
// each engine once per mode, or more often with repetitions. A run stops at
// its request budget and tries a request again when it fails for a temporary
// reason.
package run

import (
	"time"

	"github.com/vincentsch/llmention/internal/engine"
)

// Result statuses. An unfinished result holds an answer the provider cut
// off, for example at its token limit. Its text is saved so you can read it,
// but it does not count as an answer.
const (
	ResultStatusSuccess    = "success"
	ResultStatusUnfinished = "unfinished"
	ResultStatusError      = "error"
	ResultStatusCancelled  = "cancelled"
	ResultStatusNotStarted = "not_started"
)

// WorkUnit is one request to send: a rendered prompt for one engine in one
// mode.
type WorkUnit struct {
	SuiteID           string                `json:"suite_id"`
	SuiteVersion      int                   `json:"suite_version"`
	ItemID            string                `json:"item_id"`
	VariantID         string                `json:"variant_id"`
	Dimensions        map[string]string     `json:"dimensions,omitempty"`
	ComparisonGroupID string                `json:"comparison_group_id,omitempty"`
	Mode              engine.ToolPolicy     `json:"mode"`
	EngineID          string                `json:"engine_id"`
	Repetition        int                   `json:"repetition"`
	Prompt            string                `json:"prompt"`
	OutputSchema      *engine.OutputSchema  `json:"output_schema,omitempty"`
	SearchOptions     *engine.SearchOptions `json:"search_options,omitempty"`
}

// Plan is everything a run will send, worked out before any key is read.
type Plan struct {
	Suites           []SuitePlan         `json:"suites"`
	WorkUnits        []WorkUnit          `json:"work_units"`
	EngineModeMatrix []EngineModeSupport `json:"engine_mode_matrix"`
	UnsupportedPairs []UnsupportedPair   `json:"unsupported_pairs"`
	RequiredKeyEnv   []string            `json:"required_key_env"`
	// Retries is how often one failed request may be tried again.
	Retries int `json:"retries"`
	// MaxRequests caps the requests sent, retries included.
	MaxRequests int `json:"max_requests"`
	// MaxRequestsWithRetries is how many requests the run would send if every
	// request failed until its last retry, before the cap applies.
	MaxRequestsWithRetries int `json:"max_requests_with_retries"`
	Concurrency            int `json:"concurrency"`

	engineDefaults   map[string]defaults
	engineIdentities map[string]identity
}

// SuitePlan summarizes one selected suite.
type SuitePlan struct {
	ID               string   `json:"id"`
	Version          int      `json:"version"`
	Type             string   `json:"type"`
	Source           string   `json:"source"`
	Path             string   `json:"path,omitempty"`
	ItemCount        int      `json:"item_count"`
	VariantCount     int      `json:"variant_count"`
	Modes            []string `json:"modes"`
	Repetitions      int      `json:"repetitions"`
	ValidWorkUnits   int      `json:"valid_work_units"`
	SkippedWorkUnits int      `json:"skipped_work_units"`
}

// EngineModeSupport says whether an engine can answer in a mode.
type EngineModeSupport struct {
	EngineID  string `json:"engine_id"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Mode      string `json:"mode"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// UnsupportedPair is an engine and mode that the run leaves out, with the
// reason.
type UnsupportedPair struct {
	SuiteID          string `json:"suite_id"`
	EngineID         string `json:"engine_id"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Mode             string `json:"mode"`
	Reason           string `json:"reason"`
	WorkUnitsSkipped int    `json:"work_units_skipped"`
}

// RawSuiteRun is what came back from the providers, before any scoring.
type RawSuiteRun struct {
	RunID               string            `json:"run_id"`
	StartedAt           string            `json:"started_at"`
	FinishedAt          string            `json:"finished_at"`
	ElapsedMS           int64             `json:"elapsed_ms"`
	SuiteIDs            []string          `json:"suite_ids"`
	Modes               []string          `json:"modes"`
	EngineIDs           []string          `json:"engine_ids"`
	TargetWorkUnits     int               `json:"target_work_units"`
	CompletedWorkUnits  int               `json:"completed_work_units"`
	UnfinishedWorkUnits int               `json:"unfinished_work_units"`
	ErroredWorkUnits    int               `json:"errored_work_units"`
	CancelledWorkUnits  int               `json:"cancelled_work_units"`
	NotStartedWorkUnits int               `json:"not_started_work_units"`
	RequestsSent        int               `json:"requests_sent"`
	MaxRequests         int               `json:"max_requests"`
	Retries             int               `json:"retries"`
	Interrupted         bool              `json:"interrupted"`
	BudgetExhausted     bool              `json:"budget_exhausted"`
	UnsupportedPairs    []UnsupportedPair `json:"unsupported_pairs"`
	Results             []Result          `json:"results"`
}

// Result is the outcome of one work unit.
type Result struct {
	WorkUnit    WorkUnit       `json:"work_unit"`
	Provider    string         `json:"provider"`
	Model       string         `json:"model"`
	Status      string         `json:"status"`
	Attempts    int            `json:"attempts"`
	ElapsedMS   int64          `json:"elapsed_ms"`
	Answer      *engine.Answer `json:"answer,omitempty"`
	Error       *ResultError   `json:"error,omitempty"`
	RetryWaitMS []int64        `json:"retry_wait_ms,omitempty"`
}

// ResultError is the last error of a failed work unit, safe to store.
type ResultError struct {
	Message      string `json:"message"`
	Class        string `json:"class,omitempty"`
	Retryable    bool   `json:"retryable,omitempty"`
	ProviderID   string `json:"provider_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	RetryAfterMS *int64 `json:"retry_after_ms,omitempty"`
	SafeSnippet  string `json:"safe_snippet,omitempty"`
}

type defaults struct {
	maxOutputTokens int
}

type identity struct {
	provider string
	model    string
}

func durationMillis(d time.Duration) int64 { return int64(d / time.Millisecond) }
