package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/mention"
)

// fakeChab answers like Chab's API docs describe. Starting an operation
// returns 202, the status turns succeeded on the second check, and the
// result sits under data.result.
func fakeChab(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	checks := map[string]int{}
	results := map[string]string{}
	next := 0
	mux := http.NewServeMux()
	start := func(kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-chab-key" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"bad key","retryable":false}}`))
				return
			}
			if !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "llmention-") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"idempotency_key_required","message":"Add an Idempotency-Key header.","retryable":false}}`))
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			next++
			id := fmt.Sprintf("op_%d", next)
			calls = append(calls, kind)
			switch kind {
			case "ideas":
				results[id] = `{"keywords":[
					{"keyword":"llm cost tracking","metrics":{"volume":{"monthly_searches":1900}}},
					{"keyword":"best llm observability tools","metrics":{"volume":{"monthly_searches":4400}}},
					{"keyword":"tabslip pricing","metrics":{"volume":{"monthly_searches":50}}},
					{"keyword":"openai cost dashboard","metrics":{"volume":{"monthly_searches":null}}}]}`
			case "serp":
				results[id] = `{"query":"` + body["query"].(string) + `","blocks":{
					"organic":[{"title":"How do I track OpenAI API costs? - Dev Blog"},{"title":"Pricing | Helicone"}],
					"people_also_ask":[{"question":"What is the best tool to monitor LLM costs?"},{"question":"How much does LLM observability cost?"}]}}`
			}
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"operation":{"id":%q,"status":"queued","status_url":"/v1/operations/%s","result_url":"/v1/operations/%s/result"}}`, id, id, id)
		}
	}
	mux.HandleFunc("POST /v1/seo/keywords/ideas", start("ideas"))
	mux.HandleFunc("POST /v1/search/serp", start("serp"))
	mux.HandleFunc("GET /v1/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		mu.Lock()
		checks[id]++
		status := "running"
		if checks[id] > 1 {
			status = "succeeded"
		}
		mu.Unlock()
		fmt.Fprintf(w, `{"operation":{"id":%q,"status":%q,"status_url":"/v1/operations/%s","result_url":"/v1/operations/%s/result"}}`, id, status, id, id)
	})
	mux.HandleFunc("GET /v1/operations/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `{"data":{"result":%s,"warnings":[]}}`, results[r.PathValue("id")])
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &calls
}

func TestCollectFromChabRanksRealQuestionsByDemand(t *testing.T) {
	server, calls := fakeChab(t)
	c := &Chab{BaseURL: server.URL, Key: "test-chab-key", Sleep: func(context.Context, time.Duration) error { return nil }}
	got, err := c.Collect(context.Background(), []string{"llm cost tracking"}, "US", "en-US", 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, ",") != "ideas,serp,serp" {
		t.Fatalf("calls = %v", *calls)
	}
	cleaned, dropped := Clean(got, mention.Brand{Name: "Tabslip"}, []string{"What would you recommend for llm cost tracking?"})
	if dropped != 1 {
		t.Fatalf("prompts naming the brand dropped = %d, want 1", dropped)
	}
	if cleaned[0].Prompt != "Best llm observability tools?" || *cleaned[0].MonthlySearches != 4400 {
		t.Fatalf("first candidate = %+v", cleaned[0])
	}
	var sources []string
	for _, c := range cleaned {
		sources = append(sources, c.Source+": "+c.Prompt)
	}
	joined := strings.Join(sources, "\n")
	for _, want := range []string{"people_also_ask: What is the best tool to monitor LLM costs?", "search_result: How do I track OpenAI API costs?", "keyword_idea: What would you recommend for openai cost dashboard?"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "Pricing | Helicone") || strings.Count(joined, "What is the best tool to monitor LLM costs?") != 1 {
		t.Fatalf("titles that are not questions or duplicates leaked:\n%s", joined)
	}
}

func TestChabErrorsSayWhatToDo(t *testing.T) {
	server, _ := fakeChab(t)
	c := &Chab{BaseURL: server.URL, Key: "wrong", Sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := c.KeywordIdeas(context.Background(), []string{"x"}, "US", "en-US", 5)
	if err == nil || !strings.Contains(err.Error(), "status 401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("err = %v", err)
	}
}

func TestTemplatesAndTitles(t *testing.T) {
	got := FromTemplates([]string{"invoicing for freelancers", "time tracking"}, []string{"Globex"})
	if len(got) != 17 || got[0].Source != "template" {
		t.Fatalf("templates = %d %+v", len(got), got[0])
	}
	for title, want := range map[string]string{
		"What is the best LLM cost tracker? - Example Blog": "What is the best LLM cost tracker?",
		"Helicone vs Langfuse | Comparison":                 "",
		"how to cut OpenAI costs":                           "How to cut OpenAI costs?",
	} {
		if got := QuestionFromTitle(title); got != want {
			t.Errorf("QuestionFromTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
