package engine

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/config"
)

func compatibleEngineFor(t *testing.T, baseURL string, timeout time.Duration) Engine {
	t.Helper()
	eng, err := NewFactory(FactoryOptions{Timeout: timeout}).NewEngine(config.EngineConfig{ID: "local", Provider: "openai-compatible", Model: "m", BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestTransportErrorsNameTheCause(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	_, err = compatibleEngineFor(t, "http://"+addr+"/v1", time.Second).Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err == nil || !strings.Contains(err.Error(), "could not reach "+addr) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refused error = %v", err)
	}
	pe := err.(*ProviderError)
	if !pe.Retryable {
		t.Fatalf("a refused connection may be temporary and should be retried: %+v", pe)
	}
}

func TestRequestsTimeOutAndSayHow(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer server.Close()
	defer close(block)
	start := time.Now()
	_, err := compatibleEngineFor(t, server.URL+"/v1", 200*time.Millisecond).Ask(context.Background(), validRequest(ToolPolicyMemory))
	if err == nil || !strings.Contains(err.Error(), "within 200ms") || strings.Contains(err.Error(), "could not reach") {
		t.Fatalf("timeout error = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the request was not cut off by the timeout")
	}
}

func TestRequestsCarryTheUserAgent(t *testing.T) {
	var agent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	if _, err := compatibleEngineFor(t, server.URL+"/v1", time.Second).Ask(context.Background(), validRequest(ToolPolicyMemory)); err != nil {
		t.Fatal(err)
	}
	if agent != "llmention/dev" {
		t.Fatalf("User-Agent = %q", agent)
	}
}

func TestHTTPErrorsKeepTheProviderMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()
	_, err := compatibleEngineFor(t, server.URL+"/v1", time.Second).Ask(context.Background(), validRequest(ToolPolicyMemory))
	pe, ok := err.(*ProviderError)
	if !ok || pe.StatusCode != 429 || !pe.Retryable || !strings.Contains(pe.Error(), "status 429: slow down") {
		t.Fatalf("error = %v", err)
	}
}

func TestTimeoutsAreWrittenLikeTheConfig(t *testing.T) {
	for d, want := range map[time.Duration]string{5 * time.Minute: "5m", 90 * time.Second: "1m30s", 2 * time.Hour: "2h", 200 * time.Millisecond: "200ms"} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%s) = %q, want %q", d, got, want)
		}
	}
}
