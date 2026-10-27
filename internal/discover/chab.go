package discover

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/engine"
)

// DefaultChabURL is Chab's public API.
const DefaultChabURL = "https://www.chab.ai"

// Chab credit prices from Chab's API reference, for the dry run.
const (
	KeywordIdeasCredits = 10
	SERPCredits         = 5
)

// Chab talks to the Chab API. Each call starts an operation on Chab's side,
// waits until it is done and then reads its result.
type Chab struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
	// Wait is how long to wait for one operation, 3 minutes when zero.
	Wait time.Duration
	// Sleep pauses between status checks; tests make it instant.
	Sleep func(context.Context, time.Duration) error
}

// Keyword is a search phrase and how often people search for it.
type Keyword struct {
	Keyword         string
	MonthlySearches *int
}

// SERP holds what a search results page showed for a query.
type SERP struct {
	PeopleAlsoAsk []string
	Titles        []string
}

// Collect turns Chab data into suggested prompts. It asks for related search
// phrases with their monthly search volume. Then it reads the search results
// for the seeds and the most searched phrases, and takes the "People also
// ask" questions and the titles that read like a question. pages caps how
// many search result pages it reads.
func (c *Chab) Collect(ctx context.Context, seeds []string, country, language string, pages int) ([]Candidate, error) {
	if len(seeds) > 5 {
		seeds = seeds[:5]
	}
	ideas, err := c.KeywordIdeas(ctx, seeds, country, language, 25)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	volumes := map[string]*int{}
	for _, k := range ideas {
		volumes[k.Keyword] = k.MonthlySearches
		if q := QuestionFromKeyword(k.Keyword); q != "" {
			out = append(out, Candidate{Prompt: q, Source: "keyword_idea", Seed: k.Keyword, MonthlySearches: k.MonthlySearches})
		}
	}
	queries := append([]string(nil), seeds...)
	byVolume := append([]Keyword(nil), ideas...)
	slices.SortStableFunc(byVolume, func(a, b Keyword) int { return cmp.Compare(deref(b.MonthlySearches), deref(a.MonthlySearches)) })
	for _, k := range byVolume {
		if !slices.Contains(queries, k.Keyword) {
			queries = append(queries, k.Keyword)
		}
	}
	if len(queries) > pages {
		queries = queries[:pages]
	}
	for _, q := range queries {
		page, err := c.SearchResults(ctx, q, country, language)
		if err != nil {
			return out, err
		}
		for _, question := range page.PeopleAlsoAsk {
			out = append(out, Candidate{Prompt: question, Source: "people_also_ask", Seed: q, MonthlySearches: volumes[q]})
		}
		for _, title := range page.Titles {
			if question := QuestionFromTitle(title); question != "" {
				out = append(out, Candidate{Prompt: question, Source: "search_result", Seed: q, MonthlySearches: volumes[q]})
			}
		}
	}
	return out, nil
}

func deref(v *int) int {
	if v == nil {
		return -1
	}
	return *v
}

// KeywordIdeas asks for phrases related to up to five seeds, with their
// monthly search volume.
func (c *Chab) KeywordIdeas(ctx context.Context, seeds []string, country, language string, limit int) ([]Keyword, error) {
	body := map[string]any{
		"seeds":           seeds,
		"engine":          "google",
		"location":        map[string]string{"country": country},
		"language":        language,
		"metric_families": []string{"volume"},
		"limit":           limit,
	}
	var result struct {
		Keywords []struct {
			Keyword string `json:"keyword"`
			Metrics struct {
				Volume struct {
					MonthlySearches *int `json:"monthly_searches"`
				} `json:"volume"`
			} `json:"metrics"`
		} `json:"keywords"`
	}
	if err := c.operation(ctx, "/v1/seo/keywords/ideas", body, &result); err != nil {
		return nil, err
	}
	var out []Keyword
	for _, k := range result.Keywords {
		out = append(out, Keyword{Keyword: k.Keyword, MonthlySearches: k.Metrics.Volume.MonthlySearches})
	}
	return out, nil
}

// SearchResults reads the first results page for a query: the "People also
// ask" questions and the organic result titles.
func (c *Chab) SearchResults(ctx context.Context, query, country, language string) (SERP, error) {
	body := map[string]any{
		"query":    query,
		"engine":   "google",
		"location": map[string]string{"country": country},
		"language": language,
		"limit":    10,
		"blocks":   []string{"organic", "people_also_ask"},
	}
	var result struct {
		Blocks struct {
			Organic []struct {
				Title string `json:"title"`
			} `json:"organic"`
			PeopleAlsoAsk []struct {
				Question string `json:"question"`
			} `json:"people_also_ask"`
		} `json:"blocks"`
	}
	if err := c.operation(ctx, "/v1/search/serp", body, &result); err != nil {
		return SERP{}, err
	}
	var out SERP
	for _, q := range result.Blocks.PeopleAlsoAsk {
		if q.Question != "" {
			out.PeopleAlsoAsk = append(out.PeopleAlsoAsk, q.Question)
		}
	}
	for _, o := range result.Blocks.Organic {
		if o.Title != "" {
			out.Titles = append(out.Titles, o.Title)
		}
	}
	return out, nil
}

type operationEnvelope struct {
	Operation struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		StatusURL       string `json:"status_url"`
		ResultURL       string `json:"result_url"`
		PublicErrorCode string `json:"public_error_code"`
	} `json:"operation"`
}

// operation starts a Chab operation, waits for it and decodes data.result
// into out.
func (c *Chab) operation(ctx context.Context, path string, body any, out any) error {
	start, _, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	var op operationEnvelope
	if err := json.Unmarshal(start, &op); err != nil || op.Operation.ID == "" {
		return fmt.Errorf("chab: could not read the answer to %s", path)
	}
	wait := c.Wait
	if wait <= 0 {
		wait = 3 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for !terminal(op.Operation.Status) {
		if time.Now().After(deadline) {
			return fmt.Errorf("chab: operation %s did not finish within %s; try again later", op.Operation.ID, wait)
		}
		statusPath := op.Operation.StatusURL
		if statusPath == "" {
			statusPath = "/v1/operations/" + op.Operation.ID
		}
		raw, retryAfter, err := c.do(ctx, http.MethodGet, statusPath+"?wait=20s", nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &op); err != nil {
			return fmt.Errorf("chab: could not read the status of %s", path)
		}
		if !terminal(op.Operation.Status) {
			if err := c.pause(ctx, max(retryAfter, time.Second)); err != nil {
				return err
			}
		}
	}
	if op.Operation.Status != "succeeded" {
		code := op.Operation.PublicErrorCode
		if code == "" {
			code = op.Operation.Status
		}
		return fmt.Errorf("chab: operation %s ended with %s", op.Operation.ID, code)
	}
	resultPath := op.Operation.ResultURL
	if resultPath == "" {
		resultPath = "/v1/operations/" + op.Operation.ID + "/result"
	}
	raw, _, err := c.do(ctx, http.MethodGet, resultPath, nil)
	if err != nil {
		return err
	}
	var envelope struct {
		Data struct {
			Result json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data.Result) == 0 {
		return fmt.Errorf("chab: could not read the result of %s", path)
	}
	return json.Unmarshal(envelope.Data.Result, out)
}

func terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "timed_out", "expired":
		return true
	}
	return false
}

// do sends one request and tries it again up to two times when the
// connection drops or Chab is busy. Starting an operation needs an
// Idempotency-Key. A retry sends the same key, so Chab does not start the
// operation twice.
func (c *Chab) do(ctx context.Context, method, path string, body any) ([]byte, time.Duration, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultChabURL
	}
	target := base + path
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// The API key only goes to the Chab host.
		if !strings.HasPrefix(path, base+"/") {
			return nil, 0, fmt.Errorf("chab: the answer links to another host, and llmention does not send your key there")
		}
		target = path
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, 0, err
		}
	}
	idempotencyKey := ""
	if method == http.MethodPost {
		idempotencyKey = newIdempotencyKey()
	}
	client := c.HTTP
	if client == nil {
		client = engine.NewHTTPClient(time.Minute)
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", engine.UserAgent())
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if attempt < 3 {
				if err := c.pause(ctx, time.Duration(attempt)*2*time.Second); err != nil {
					return nil, 0, err
				}
				continue
			}
			return nil, 0, fmt.Errorf("chab: could not reach %s: %w", base, unwrapURL(err))
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("chab: could not read the answer: %w", err)
		}
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return raw, retryAfter, nil
		}
		apiErr := decodeChabError(resp.StatusCode, raw)
		if apiErr.retryable && attempt < 3 {
			if err := c.pause(ctx, max(retryAfter, time.Duration(attempt)*2*time.Second)); err != nil {
				return nil, 0, err
			}
			continue
		}
		return nil, 0, apiErr
	}
}

// ChabError is an error answer from the Chab API.
type ChabError struct {
	Status    int
	Code      string
	Message   string
	retryable bool
}

func (e *ChabError) Error() string {
	text := fmt.Sprintf("Chab answered with status %d", e.Status)
	if e.Code != "" {
		text += " (" + e.Code + ")"
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	switch e.Code {
	case "api_scope_missing":
		text += "; give the key the api:search:read and api:seo:read scopes"
	case "insufficient_credits":
		text += "; add credits in the Chab dashboard"
	}
	return text
}

func decodeChabError(status int, raw []byte) *ChabError {
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	e := &ChabError{Status: status, Code: envelope.Error.Code, Message: envelope.Error.Message, retryable: envelope.Error.Retryable}
	if status == http.StatusTooManyRequests || status >= 500 {
		e.retryable = true
	}
	if status == http.StatusUnauthorized && e.Message == "" {
		e.Message = "the API key was rejected; check CHAB_API_KEY"
	}
	return e
}

func (c *Chab) pause(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 {
		return 0
	}
	return min(time.Duration(seconds)*time.Second, time.Minute)
}

func newIdempotencyKey() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "llmention-" + hex.EncodeToString(b[:])
}

func unwrapURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
