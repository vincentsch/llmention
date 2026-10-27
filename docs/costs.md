# Costs and the request budget

llmention itself is free. Each request goes to a provider with your key, and
the provider bills you as usual.

## The estimate

`llmention run --dry-run` prints an estimate before anything is sent:

```text
Would send 40 requests: 10 prompts to 2 engines, in no web search and web search modes.
Failed requests are retried. The run stops at 100 requests, its budget.
Estimated cost: about $0.60, from list prices checked on 2026-10-26. Thinking tokens and extra web searches can make it higher.
```

Providers bill by the token, a short piece of text that is often part of a
word. The estimate assumes that each answer is about 600 tokens long. In a web
search mode, it also assumes one search per answer. That search adds about
4,000 tokens of web pages, which providers bill as input, plus the provider's
fee for the search.

The prices come from a table inside llmention. It was copied from the
providers' pricing pages on the date shown. When a model is not in that table,
the estimate names it and leaves it out. It also says when it leaves out
Perplexity's fee per request.

What you pay in the end depends on how long the answers are and how many
searches a model runs. Two things push the bill above the estimate. Current
models think before they answer, and providers bill those thinking tokens as
output. Also, Anthropic's and OpenRouter's search tools may run up to five
searches for one answer. So for search modes, expect to pay at least the
estimate, and check your provider's usage page after the first run.

Some examples at the October 2026 prices of the starter models:

| Run | Requests | Estimate |
| --- | --- | --- |
| 10 prompts, OpenAI and Anthropic, two modes | 40 | about $0.60 |
| 10 prompts, all four starter providers, two modes | 80 | about $0.86, plus Perplexity's request fees |

Weekly runs of the first size come to about $30 a year.

## The budget

`execution.max_requests` (default 100) is the most requests one run may send,
retries included. Before a run starts, llmention checks that each request fits
into this budget once. If not, it doesn't start, and it tells you which number
to use:

```text
Error: this run needs 160 requests but the budget is 100 (execution.max_requests); ask fewer prompts or engines, or raise the budget with --max-requests 160
```

During the run, each retry also counts against the budget. When the budget is
used up, the run stops and saves the answers it has.

## Retries and timeouts

If a request fails for a temporary reason, such as a rate limit or a server
error, llmention tries again up to `execution.retries` times (default 2). It
waits about one second before the first retry, and the wait doubles each time.
When the provider says how long to wait, llmention waits that long, but never
more than a minute. llmention does not retry errors such as a wrong key or an
unknown model.

`execution.timeout` (default `5m`) is how long llmention waits for each answer.
The default is long because an answer with thinking and several web searches
can take minutes. `execution.concurrency` (default 4) sets how many requests
run at the same time.

Ctrl+C stops a run. llmention saves the answers that came back, and the report
marks the run as stopped early.

## The AI grader

An [AI grader](configuration.md#ai-grader) is a second model that reads the
answers and judges them, for example whether a mention was positive. Each
graded answer costs one more request. `execution.max_judge_requests` (default
100) limits how many requests the grader gets, and the dry run shows how many
a run may need. The cost estimate leaves them out.

## Spending less

- Ask fewer engines or one mode: `llmention run --engine chatgpt --mode search`.
  Such a run is for a quick look. `--trend` leaves it out, and `--diff` never
  compares the next full run with it.
- Use a smaller model. The [providers page](providers.md) names some.
- Leave `repetitions` at 1 unless the numbers jump around a lot.
- Remove prompts whose answers never name any brand you track.
