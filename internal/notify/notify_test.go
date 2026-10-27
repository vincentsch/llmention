package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/scorecard"
)

func TestBuildPayloadUsesSafeFailedGateShape(t *testing.T) {
	card := notificationCard()
	payload := BuildPayload(card)
	data, err := MarshalPayload(payload)
	if err != nil {
		t.Fatalf("MarshalPayload() error = %v", err)
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		t.Fatalf("payload should be compact JSON without trailing newline: %q", data)
	}
	text := string(data)
	for _, forbidden := range []string{"WEBHOOK", "notify.sh", "/tmp/", "raw_response", "answer_text"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("payload leaked %q:\n%s", forbidden, text)
		}
	}
	var decoded Payload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("payload JSON: %v\n%s", err, data)
	}
	if decoded.Event != EventGateFailed || decoded.RunID != card.ID || decoded.Brand != "Acme" || len(decoded.FailedGates) != 1 {
		t.Fatalf("payload = %#v", decoded)
	}
	if decoded.CreatedAt != "2026-08-18T12:30:00Z" || decoded.ReportCommand != "llmention report "+card.ID || decoded.DiffCommand != "llmention report --diff "+card.ID {
		t.Fatalf("payload commands = %#v", decoded)
	}
	if decoded.FailedGates[0].Selector != "mention-rate=40%" {
		t.Fatalf("failed gate = %#v", decoded.FailedGates[0])
	}
}

func TestDeliverAttemptsCommandThenWebhookWithIdenticalBody(t *testing.T) {
	card := notificationCard()
	var commandPath string
	var commandBody []byte
	var webhookBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read webhook body: %v", err)
		}
		webhookBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var registered []string
	rows := Deliver(context.Background(), Config{
		Command:       "notify.sh",
		WebhookURLEnv: "LLMENTION_NOTIFY_WEBHOOK_URL",
	}, card, Options{
		WorkingDir: "/work",
		LookupEnv: func(name string) (string, bool) {
			if name != "LLMENTION_NOTIFY_WEBHOOK_URL" {
				t.Fatalf("lookup env = %q", name)
			}
			return server.URL, true
		},
		RegisterSecret: func(value string) {
			registered = append(registered, value)
		},
		CommandRunner: CommandRunnerFunc(func(ctx context.Context, path string, stdin []byte) error {
			commandPath = path
			commandBody = append([]byte(nil), stdin...)
			return nil
		}),
	})
	if got, want := rows, []HookSummary{{Kind: KindCommand, Status: StatusSent}, {Kind: KindWebhook, Status: StatusSent}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	wantPath, _ := filepath.Abs(filepath.Join("/work", "notify.sh"))
	if commandPath != wantPath && commandPath != filepath.Join("/work", "notify.sh") {
		t.Fatalf("command path = %q", commandPath)
	}
	if len(commandBody) == 0 || !reflect.DeepEqual(commandBody, webhookBody) {
		t.Fatalf("command/webhook bodies differ:\n%s\n---\n%s", commandBody, webhookBody)
	}
	if !reflect.DeepEqual(registered, []string{server.URL}) {
		t.Fatalf("registered secrets = %#v", registered)
	}
}

func TestDeliverSkipsConfiguredHooksWhenNoFailedGates(t *testing.T) {
	card := notificationCard()
	card.Gates[0].Status = "pass"
	var lookedUp bool
	var ran bool
	rows := Deliver(context.Background(), Config{Command: "notify.sh", WebhookURLEnv: "WEBHOOK_ENV"}, card, Options{
		LookupEnv: func(string) (string, bool) {
			lookedUp = true
			return "", false
		},
		CommandRunner: CommandRunnerFunc(func(context.Context, string, []byte) error {
			ran = true
			return nil
		}),
	})
	if ran || lookedUp {
		t.Fatalf("delivery attempted side effects for no failed gates")
	}
	want := []HookSummary{
		{Kind: KindCommand, Status: StatusSkippedNoFailedGates},
		{Kind: KindWebhook, Status: StatusSkippedNoFailedGates},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
}

func TestDeliverMapsSafeFailureReasons(t *testing.T) {
	card := notificationCard()
	rows := Deliver(context.Background(), Config{Command: "notify.sh", WebhookURLEnv: "WEBHOOK_ENV"}, card, Options{
		WorkingDir: "/work",
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
		CommandTimeout: time.Nanosecond,
		CommandRunner: CommandRunnerFunc(func(ctx context.Context, _ string, _ []byte) error {
			<-ctx.Done()
			return ctx.Err()
		}),
	})
	want := []HookSummary{
		{Kind: KindCommand, Status: StatusFailed, ReasonCode: ReasonCommandTimeout},
		{Kind: KindWebhook, Status: StatusFailed, ReasonCode: ReasonWebhookEnvMissing},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}

	var registered []string
	rows = Deliver(context.Background(), Config{WebhookURLEnv: "WEBHOOK_ENV"}, card, Options{
		LookupEnv: func(string) (string, bool) {
			return "notaurl", true
		},
		RegisterSecret: func(value string) {
			registered = append(registered, value)
		},
	})
	if rows[1].ReasonCode != ReasonWebhookURLInvalid {
		t.Fatalf("invalid URL rows = %#v", rows)
	}
	if !reflect.DeepEqual(registered, []string{"notaurl"}) {
		t.Fatalf("registered = %#v, want invalid URL registered before validation", registered)
	}
}

func TestDeliverDisablesWebhookRedirects(t *testing.T) {
	card := notificationCard()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()
	rows := Deliver(context.Background(), Config{WebhookURLEnv: "WEBHOOK_ENV"}, card, Options{
		LookupEnv: func(string) (string, bool) {
			return server.URL, true
		},
	})
	if rows[1].Status != StatusFailed || rows[1].ReasonCode != ReasonWebhookStatus {
		t.Fatalf("redirect rows = %#v", rows)
	}
}

func TestDeliverWebhookResponseReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		reason  string
	}{
		{"truncated", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("short"))
		}, ReasonWebhookTransportFailed},
		{"timeout after headers", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}, ReasonWebhookTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			rows := Deliver(context.Background(), Config{WebhookURLEnv: "SYNTHETIC_WEBHOOK"}, notificationCard(), Options{
				LookupEnv:      func(string) (string, bool) { return server.URL, true },
				WebhookTimeout: 100 * time.Millisecond,
			})
			if rows[1].Status != StatusFailed || rows[1].ReasonCode != tc.reason {
				t.Fatalf("webhook summary = %#v, want failed/%s", rows[1], tc.reason)
			}
		})
	}
}

func notificationCard() *scorecard.Scorecard {
	value := 0.2
	return &scorecard.Scorecard{
		ID:        "20260818-123000-0a1b2c",
		CreatedAt: time.Date(2026, 8, 18, 12, 30, 0, 0, time.UTC),
		Brand:     &config.BrandConfig{Name: "Acme"},
		Suites:    []scorecard.Suite{{ID: "prompts"}},
		Engines:   []scorecard.Engine{{ID: "mock", Provider: "mock", Model: "demo"}},
		Gates: []scorecard.Gate{{
			Selector:  "mention-rate=40%",
			Status:    "fail",
			Value:     &value,
			Threshold: 0.4,
			Scope:     "mock (no web search)",
			Message:   "mention rate 20% for mock (no web search) is below 40%",
		}},
	}
}
