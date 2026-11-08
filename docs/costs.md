# Costs and the request budget

llmention itself is free. Each request goes to a provider with your key, and
the provider bills you as usual.

## The estimate

`llmention run --dry-run` prints an estimate before anything is sent:

```text
Would send 40 requests: 10 prompts to 2 engines, in no web search and web search modes.
Failed requests are retried. The run stops at 100 requests, its budget.
Estimated cost: about $1.72, from list prices checked on 2026-10-26. Longer answers and extra web searches can make it higher.
```

Providers bill by the token, a short piece of text that is often part of a
word. The estimate assumes that each answer is about 1,000 tokens long,
including the tokens the model spends thinking. In a web search mode it also
counts the pages the model reads, which providers bill as input, and the fee
for each search.

Those amounts differ by provider, so the estimate uses what answers needed in
a live run in October 2026. OpenAI's model searched two or three times per
answer and read 30,000 to 38,000 tokens of pages. Claude searched once and
read about 14,000 tokens. For other providers the estimate assumes one search
and 10,000 tokens. Perplexity's models charge a fee for each request instead,
also when they don't search.

The prices come from a table inside llmention. It was copied from the
providers' pricing pages on the date shown. When a model is not in that table,
the estimate names it and leaves it out. It also says when it leaves out
Perplexity's fee per request. The prices for models on OpenRouter come from
OpenRouter's model list.

What you pay in the end depends on how long the answers are and how many
searches a model runs, and both change from question to question. Answers
with web search cost the most. With the starter models, an OpenAI answer with
web search costs about ten times as much as one without. Check your
provider's usage page after the first run. Each saved run also records the
tokens and searches that the provider reported for every answer.

Some examples at the October 2026 prices of the starter models:

| Run | Requests | Estimate |
| --- | --- | --- |
| 10 prompts, OpenAI and Anthropic, two modes | 40 | about $1.72 |
| 10 prompts, all four starter providers, two modes | 80 | about $2.06, plus Perplexity's request fees |

Weekly runs of the first size come to about $90 a year.

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
- Ask without web search: `llmention run --mode no-search`. Most of the cost
  is in the web search answers.
- Use a smaller model. The [providers page](providers.md) names some.
- Leave `repetitions` at 1 unless the numbers jump around a lot.
- Remove prompts whose answers never name any brand you track.
