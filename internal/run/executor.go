package run

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vincentsch/llmention/internal/engine"
)

// How long to wait before a retry. A provider that asks for a longer pause
// than maxRetryWait gets maxRetryWait, so one Retry-After header cannot stall
// a run for hours.
const (
	baseRetryWait = time.Second
	maxRetryWait  = time.Minute
)

// ExecuteOptions controls Execute. Empty fields use the real clock and a
// real sleep.
type ExecuteOptions struct {
	RunID string
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
	// Progress is called after each work unit finishes, from any goroutine.
	Progress func(done, total int, result Result)
}

// Execute sends every work unit. It returns when all are done, or earlier
// when the budget runs out or ctx is cancelled. Units that never started are
// marked not_started, so the caller can still save the answers that came
// back.
func Execute(ctx context.Context, plan *Plan, engines []engine.Engine, opts ExecuteOptions) (RawSuiteRun, error) {
	if plan == nil {
		return RawSuiteRun{}, fmt.Errorf("run plan is nil")
	}
	if err := CheckBudget(plan); err != nil {
		return RawSuiteRun{}, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	start := now().UTC()
	runID := opts.RunID
	if runID == "" {
		runID = "run_" + start.Format("20060102T150405.000000000Z")
	}
	engineByID := map[string]engine.Engine{}
	for _, eng := range engines {
		engineByID[eng.ID()] = eng
	}
	results := make([]Result, len(plan.WorkUnits))
	for i, unit := range plan.WorkUnits {
		ident := plan.engineIdentities[unit.EngineID]
		results[i] = Result{WorkUnit: unit, Provider: ident.provider, Model: ident.model, Status: ResultStatusNotStarted}
	}
	budget := &requestBudget{left: int64(plan.MaxRequests)}
	concurrency := max(1, min(plan.Concurrency, len(plan.WorkUnits)))
	var done atomic.Int64
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				unit := plan.WorkUnits[i]
				if eng := engineByID[unit.EngineID]; eng == nil {
					results[i].Status = ResultStatusError
					results[i].Error = resultError(fmt.Errorf("engine %q is not available", unit.EngineID))
				} else {
					results[i] = executeOne(ctx, plan, eng, unit, budget, now, sleep)
				}
				if opts.Progress != nil {
					opts.Progress(int(done.Add(1)), len(plan.WorkUnits), results[i])
				}
			}
		}()
	}
schedule:
	for i := range plan.WorkUnits {
		select {
		case <-ctx.Done():
			break schedule
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	finished := now().UTC()
	out := RawSuiteRun{
		RunID:            runID,
		StartedAt:        start.Format(time.RFC3339Nano),
		FinishedAt:       finished.Format(time.RFC3339Nano),
		ElapsedMS:        durationMillis(finished.Sub(start)),
		TargetWorkUnits:  len(plan.WorkUnits),
		MaxRequests:      plan.MaxRequests,
		Retries:          plan.Retries,
		Interrupted:      ctx.Err() != nil,
		BudgetExhausted:  budget.exhausted.Load(),
		UnsupportedPairs: slices.Clone(plan.UnsupportedPairs),
		Results:          results,
	}
	for _, s := range plan.Suites {
		out.SuiteIDs = append(out.SuiteIDs, s.ID)
		for _, m := range s.Modes {
			if !slices.Contains(out.Modes, m) {
				out.Modes = append(out.Modes, m)
			}
		}
	}
	for _, s := range plan.EngineModeMatrix {
		if !slices.Contains(out.EngineIDs, s.EngineID) {
			out.EngineIDs = append(out.EngineIDs, s.EngineID)
		}
	}
	for _, r := range results {
		out.RequestsSent += r.Attempts
		switch r.Status {
		case ResultStatusSuccess:
			out.CompletedWorkUnits++
		case ResultStatusUnfinished:
			out.UnfinishedWorkUnits++
		case ResultStatusError:
			out.ErroredWorkUnits++
		case ResultStatusCancelled:
			out.CancelledWorkUnits++
		case ResultStatusNotStarted:
			out.NotStartedWorkUnits++
		}
	}
	return out, nil
}

// requestBudget counts requests across all workers.
type requestBudget struct {
	left      int64
	mu        sync.Mutex
	exhausted atomic.Bool
}

func (b *requestBudget) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.left <= 0 {
		b.exhausted.Store(true)
		return false
	}
	b.left--
	return true
}

func executeOne(ctx context.Context, plan *Plan, eng engine.Engine, unit WorkUnit, budget *requestBudget, now func() time.Time, sleep func(context.Context, time.Duration) error) Result {
	start := now()
	result := Result{WorkUnit: unit, Provider: eng.Provider(), Model: eng.Model(), Status: ResultStatusNotStarted}
	req := RequestForWorkUnit(unit, plan)
	for attempt := 1; attempt <= 1+plan.Retries; attempt++ {
		if err := ctx.Err(); err != nil {
			if attempt > 1 {
				result.Status = ResultStatusCancelled
			}
			return result
		}
		if !budget.take() {
			if attempt == 1 {
				result.Error = &ResultError{Message: "not sent: the request budget (execution.max_requests) was used up"}
			}
			return result
		}
		result.Attempts = attempt
		answer, err := eng.Ask(ctx, req)
		result.ElapsedMS = durationMillis(now().Sub(start))
		if err == nil {
			// An unfinished answer is not retried, because the same limit
			// would also cut off the next one.
			result.Status = ResultStatusSuccess
			if answer.Unfinished {
				result.Status = ResultStatusUnfinished
			}
			result.Answer = &answer
			result.Error = nil
			return result
		}
		result.Error = resultError(err)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			result.Status = ResultStatusCancelled
			return result
		}
		result.Status = ResultStatusError
		wait, retry := retryWait(err, attempt)
		if !retry || attempt == 1+plan.Retries {
			return result
		}
		result.RetryWaitMS = append(result.RetryWaitMS, durationMillis(wait))
		if err := sleep(ctx, wait); err != nil {
			result.Status = ResultStatusCancelled
			return result
		}
	}
	return result
}

// retryWait decides whether to try again and how long to wait first. It uses
// the provider's Retry-After when there is one. Otherwise the wait doubles
// each time (about 1s, 2s, 4s) plus a random part, so parallel requests do
// not all retry at the same moment. It never waits longer than maxRetryWait.
func retryWait(err error, attempt int) (time.Duration, bool) {
	var providerErr *engine.ProviderError
	if !errors.As(err, &providerErr) {
		return 0, false
	}
	if !providerErr.Retryable && providerErr.Class != engine.ErrorClassTransient && providerErr.StatusCode != 429 {
		return 0, false
	}
	if providerErr.RetryAfter != nil && *providerErr.RetryAfter > 0 {
		return min(*providerErr.RetryAfter, maxRetryWait), true
	}
	wait := baseRetryWait << (attempt - 1)
	wait += time.Duration(rand.Int64N(int64(wait / 2)))
	return min(wait, maxRetryWait), true
}

func resultError(err error) *ResultError {
	if err == nil {
		return nil
	}
	out := &ResultError{Message: err.Error()}
	var providerErr *engine.ProviderError
	if !errors.As(err, &providerErr) {
		return out
	}
	out.Class = string(providerErr.Class)
	out.Retryable = providerErr.Retryable
	out.ProviderID = providerErr.ProviderID
	out.RequestID = providerErr.RequestID
	out.StatusCode = providerErr.StatusCode
	out.SafeSnippet = providerErr.SafeSnippet
	if providerErr.RetryAfter != nil {
		ms := durationMillis(*providerErr.RetryAfter)
		out.RetryAfterMS = &ms
	}
	return out
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
