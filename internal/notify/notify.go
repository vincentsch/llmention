// Package notify runs the alert script and calls the webhook from
// llmention.yaml when a --fail-under check fails.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/scorecard"
)

// Values in the payload and in the hook summaries. They are saved with the
// run and appear in --json output, so they stay stable.
const (
	EventGateFailed = "gate_failed"

	KindCommand = "command"
	KindWebhook = "webhook"

	StatusNotConfigured        = "not_configured"
	StatusSkippedNoFailedGates = "skipped_no_failed_gates"
	StatusSent                 = "sent"
	StatusFailed               = "failed"

	ReasonCommandStartFailed = "command_start_failed"
	ReasonCommandExitNonzero = "command_exit_nonzero"
	ReasonCommandTimeout     = "command_timeout"
	ReasonCommandCancelled   = "command_cancelled"

	ReasonWebhookEnvMissing      = "webhook_env_missing"
	ReasonWebhookURLInvalid      = "webhook_url_invalid"
	ReasonWebhookTransportFailed = "webhook_transport_failed"
	ReasonWebhookStatus          = "webhook_status"
	ReasonWebhookTimeout         = "webhook_timeout"
	ReasonWebhookCancelled       = "webhook_cancelled"
)

// defaultTimeout is how long the script and the webhook each get.
const defaultTimeout = 10 * time.Second

// Config is the notify section of llmention.yaml.
type Config struct {
	Command       string
	WebhookURLEnv string
}

// HookSummary says what happened to the script or the webhook.
type HookSummary struct {
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

// Payload is the JSON a hook receives on stdin or in the webhook body.
type Payload struct {
	Event       string               `json:"event"`
	RunID       string               `json:"run_id"`
	CreatedAt   string               `json:"created_at"`
	Brand       string               `json:"brand,omitempty"`
	Suites      []string             `json:"suites"`
	Engines     []string             `json:"engines"`
	FailedGates []scorecard.Gate     `json:"failed_gates"`
	Headline    []scorecard.Headline `json:"headline,omitempty"`
	// ReportCommand and DiffCommand are ready to paste into a terminal.
	ReportCommand string `json:"report_command"`
	DiffCommand   string `json:"diff_command"`
}

// Options holds what Deliver needs from the caller. Tests replace the
// command runner and the HTTP client.
type Options struct {
	WorkingDir     string
	LookupEnv      func(string) (string, bool)
	RegisterSecret func(string)
	CommandTimeout time.Duration
	WebhookTimeout time.Duration
	CommandRunner  CommandRunner
	HTTPClient     *http.Client
}

// CommandRunner runs the alert script with the payload on its standard
// input.
type CommandRunner interface {
	Run(ctx context.Context, path string, stdin []byte) error
}

// CommandRunnerFunc lets a plain function act as a CommandRunner.
type CommandRunnerFunc func(ctx context.Context, path string, stdin []byte) error

func (fn CommandRunnerFunc) Run(ctx context.Context, path string, stdin []byte) error {
	return fn(ctx, path, stdin)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, path string, stdin []byte) error {
	cmd := exec.CommandContext(ctx, path)
	configureCommandCancellation(cmd)
	// A child process of the script can hold on to its input and output
	// after the script exits. WaitDelay stops os/exec from waiting for it
	// forever.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		_ = cmd.Cancel()
	}
	return err
}

// BuildPayload describes a run whose checks failed.
func BuildPayload(card *scorecard.Scorecard) Payload {
	p := Payload{
		Event:         EventGateFailed,
		RunID:         card.ID,
		CreatedAt:     card.CreatedAt.UTC().Format(time.RFC3339),
		FailedGates:   failedGates(card.Gates),
		Headline:      card.Headline,
		ReportCommand: "llmention report " + card.ID,
		DiffCommand:   "llmention report --diff " + card.ID,
	}
	if card.Brand != nil {
		p.Brand = card.Brand.Name
	}
	for _, s := range card.Suites {
		p.Suites = append(p.Suites, s.ID)
	}
	for _, e := range card.Engines {
		p.Engines = append(p.Engines, e.ID)
	}
	return p
}

// MarshalPayload writes the payload as JSON.
func MarshalPayload(payload Payload) ([]byte, error) {
	return json.Marshal(payload)
}

// Deliver runs the script and calls the webhook when a check failed, and
// says for each one whether it worked. A failed alert never changes the
// run's exit code.
func Deliver(ctx context.Context, cfg Config, card *scorecard.Scorecard, opts Options) []HookSummary {
	failed := failedGates(card.Gates)
	rows := []HookSummary{
		initialSummary(KindCommand, cfg.Command, len(failed)),
		initialSummary(KindWebhook, cfg.WebhookURLEnv, len(failed)),
	}
	if len(failed) == 0 {
		return rows
	}
	payloadBytes, err := MarshalPayload(BuildPayload(card))
	if err != nil {
		rows[0] = failedSummary(KindCommand, ReasonCommandStartFailed)
		rows[1] = failedSummary(KindWebhook, ReasonWebhookTransportFailed)
		return rows
	}
	if strings.TrimSpace(cfg.Command) != "" {
		rows[0] = deliverCommand(ctx, cfg.Command, payloadBytes, opts)
	}
	if strings.TrimSpace(cfg.WebhookURLEnv) != "" {
		rows[1] = deliverWebhook(ctx, cfg.WebhookURLEnv, payloadBytes, opts)
	}
	return rows
}

func deliverCommand(ctx context.Context, command string, payload []byte, opts Options) HookSummary {
	path, err := commandPath(command, opts.WorkingDir)
	if err != nil {
		return failedSummary(KindCommand, ReasonCommandStartFailed)
	}
	runner := opts.CommandRunner
	if runner == nil {
		runner = execCommandRunner{}
	}
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = runner.Run(hookCtx, path, payload)
	if err == nil {
		return HookSummary{Kind: KindCommand, Status: StatusSent}
	}
	if errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
		return failedSummary(KindCommand, ReasonCommandTimeout)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return failedSummary(KindCommand, ReasonCommandTimeout)
	}
	if errors.Is(hookCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return failedSummary(KindCommand, ReasonCommandCancelled)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return failedSummary(KindCommand, ReasonCommandExitNonzero)
	}
	return failedSummary(KindCommand, ReasonCommandStartFailed)
}

func deliverWebhook(ctx context.Context, envName string, payload []byte, opts Options) HookSummary {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	rawURL, ok := lookup(strings.TrimSpace(envName))
	if !ok || strings.TrimSpace(rawURL) == "" {
		return failedSummary(KindWebhook, ReasonWebhookEnvMissing)
	}
	if opts.RegisterSecret != nil {
		opts.RegisterSecret(rawURL)
	}
	if err := validateWebhookURL(rawURL); err != nil {
		return failedSummary(KindWebhook, ReasonWebhookURLInvalid)
	}
	timeout := opts.WebhookTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(hookCtx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return failedSummary(KindWebhook, ReasonWebhookURLInvalid)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
		client.Timeout = timeout
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
			return failedSummary(KindWebhook, ReasonWebhookTimeout)
		}
		if errors.Is(hookCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return failedSummary(KindWebhook, ReasonWebhookCancelled)
		}
		return failedSummary(KindWebhook, ReasonWebhookTransportFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return failedSummary(KindWebhook, ReasonWebhookStatus)
	}
	// llmention does not need the response body. It reads at most 4 KB of
	// it, and a read error or a timeout still counts as a failure.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		if errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
			return failedSummary(KindWebhook, ReasonWebhookTimeout)
		}
		if errors.Is(hookCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return failedSummary(KindWebhook, ReasonWebhookCancelled)
		}
		return failedSummary(KindWebhook, ReasonWebhookTransportFailed)
	}
	return HookSummary{Kind: KindWebhook, Status: StatusSent}
}

func commandPath(command, workingDir string) (string, error) {
	value := strings.TrimSpace(command)
	if value == "" {
		return "", fmt.Errorf("command is empty")
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	cwd := strings.TrimSpace(workingDir)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(cwd) {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		cwd = abs
	}
	return filepath.Join(cwd, value), nil
}

func validateWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		if err != nil {
			return err
		}
		return fmt.Errorf("the URL needs http:// or https:// and a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("the URL must start with http:// or https://")
	}
	if _, err := domain.NormalizeEndpointHost(raw); err != nil {
		return err
	}
	return nil
}

func initialSummary(kind, configured string, failedCount int) HookSummary {
	if strings.TrimSpace(configured) == "" {
		return HookSummary{Kind: kind, Status: StatusNotConfigured}
	}
	if failedCount == 0 {
		return HookSummary{Kind: kind, Status: StatusSkippedNoFailedGates}
	}
	return HookSummary{Kind: kind, Status: StatusFailed}
}

// Describe says in words why the script or the webhook failed, for a reason
// code such as "command_exit_nonzero".
func Describe(reason string) string {
	switch reason {
	case ReasonCommandStartFailed:
		return "the alert script could not be started"
	case ReasonCommandExitNonzero:
		return "the alert script ended with an error"
	case ReasonCommandTimeout:
		return "the alert script did not finish in time"
	case ReasonCommandCancelled:
		return "the alert script was stopped"
	case ReasonWebhookEnvMissing:
		return "the environment variable with the webhook URL is not set"
	case ReasonWebhookURLInvalid:
		return "the webhook URL is not a valid http or https URL"
	case ReasonWebhookTransportFailed:
		return "the webhook could not be reached"
	case ReasonWebhookStatus:
		return "the webhook answered with an error status"
	case ReasonWebhookTimeout:
		return "the webhook did not answer in time"
	case ReasonWebhookCancelled:
		return "the webhook call was stopped"
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func failedSummary(kind, reason string) HookSummary {
	return HookSummary{Kind: kind, Status: StatusFailed, ReasonCode: reason}
}

func failedGates(gates []scorecard.Gate) []scorecard.Gate {
	var out []scorecard.Gate
	for _, g := range gates {
		if g.Status != "pass" {
			out = append(out, g)
		}
	}
	return out
}
