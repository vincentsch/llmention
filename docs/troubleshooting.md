# Troubleshooting

Start with `llmention run --dry-run`. It checks `llmention.yaml` and your keys
without sending anything, and it tells you whether the run fits the budget.
Add `--json` to any command for the full error details.

## Setup

### "the API key OPENAI_API_KEY is not set"

Export the key in the shell that runs llmention: `export OPENAI_API_KEY=...`.
In CI, pass it from a secret as an environment variable. The name must match
`key_env` in `llmention.yaml`.

### "engine "chatgpt" still uses the placeholder model"

Replace the model id with one your key can use.
[Providers and models](providers.md) lists the starter models.

### "brand has no field "domain"; did you mean domains?"

A setting in `llmention.yaml` is misspelled. The message names the line and
the setting llmention expected. [The configuration reference](configuration.md)
lists every setting.

### "has a path; llmention counts whole sites"

A domain under `brand.domains` or a competitor's `domains` includes a path,
such as `github.com/acme/books`. Give the host only. If many projects share the
host, like github.com, leave the entry out.

### "did not find expected ',' or ']'"

A YAML list in square brackets on one line cannot hold an unquoted question
mark. Write the prompts as a list with one `- ` line per prompt, or put quotes
around them.

### "base_url must use https because the API key would otherwise travel unencrypted"

Use the https address of the gateway. Plain http only works for `localhost`
and `127.0.0.1`.

### "this run needs 160 requests but the budget is 100"

The run would send more requests than `execution.max_requests` allows. Raise
it, or pass `--max-requests 160` for this run. You can also ask fewer engines
with `--engine`. [Costs](costs.md) explains the budget.

### "the budget of 100 requests leaves room for only 4 retries"

The run fits the budget, but retries count against it too. If more requests
fail than the note says, the last requests of the run are not sent. Set
`execution.max_requests` to the number in the note, which leaves room for
every retry.

### "The total leaves out the fee per request for perplexity"

llmention's price table has no fee per request for Perplexity's own API, so
the estimate only counts the tokens there. Check the fee on Perplexity's
pricing page. Through OpenRouter the estimate includes the fee.
[Costs](costs.md#the-estimate) has the details.

### "nothing was sent, because the answers could not be scored"

The AI grader is turned on, with `analysis.model_assisted_enabled` or
`--judge`, but it is not fully set up. The rest of the message says what to
add. The dry run shows the same problem in a line that starts with "The run
would not start". See [AI grader](configuration.md#ai-grader).

## During a run

### "OpenAI answered with status 401" or "status 403"

The key is wrong or has no access to that model. Check it on the provider's
dashboard.

### "Check the model id in llmention.yaml"

The provider doesn't know the model, or your key can't use it. OpenAI and
Anthropic answer with status 404 and OpenRouter with status 400 ("is not a
valid model ID"). Copy the id from the provider's model list.

### "API key not valid"

Google doesn't accept the key in `GEMINI_API_KEY`. It may have been deleted or
copied incompletely. Create a new key in Google AI Studio and export it again.

### "No endpoints found that support tool use"

The OpenRouter model can't use OpenRouter's web search tool. Turn the web
search mode off for that engine with
`capabilities: {supports_search_auto: false}`, as
[Providers and models](providers.md#openrouter) shows.

### "answered with status 429"

You hit the provider's rate limit. llmention waits and tries again. If it
happens often, lower `execution.concurrency` to 1 or 2.

### "no complete answer from api.openai.com within 5m"

llmention waits up to five minutes for each answer, because an answer with
thinking and several web searches can take minutes. If a model is slower than
that, raise `execution.timeout`, for example to `10m`.

### Exit code 2: "N of M requests got no answer"

Some requests failed or were not sent. The run was saved with the answers that
came back. `llmention report --answers` shows the error for each missing
answer. The numbers in the report only count complete answers.

### "N answers were cut off before they finished"

The provider stopped those answers early, usually at the output token limit or
while it was still searching the web. They are left out of the numbers. The
run still exits with code 0, because the provider did answer. If it happens
often, remove `max_output_tokens` from the engine or raise it. Current models
count their thinking toward that limit. With a low limit, a model can spend
all of it thinking, and `report --answers` then says that no text came back.

### A check is "inconclusive"

An engine and mode that the check covers has no number, for example because
all its requests failed. The run fails with exit code 7, so that a gap never
looks like a pass. Look at `llmention report --answers` for that engine.

## Reports

### "no saved runs in .llmention/runs yet"

Run `llmention run` first. In CI, the history folder has to be saved between
jobs; see [Scheduled runs](ci.md).

### "there is no earlier run to compare with"

`--diff` and `--trend` need at least two runs of your prompts. A full run is
never compared with a run made with `--engine` or `--mode`.

### The answer names us, but the report says it does not

Check the spelling in the answer. Add other spellings under `brand.aliases`
and product names under `brand.products`. The report also uses them for old
runs. Then check whether the prompt names you. Those answers do not count, and
the report says how many prompts it left out.

### A competitor I don't track shows up under "Other names in the answers"

Add it under `competitors` to see its rate next to yours. The list can also
contain a label that is not a product.

### The numbers of an old run changed

The report counts the mentions in each run again with your current
`llmention.yaml`. A new alias or competitor also changes the numbers of old
runs, so old and new runs are counted the same way. `history list` shows the
numbers saved at the time of each run.

## Still stuck

Open an issue with the command you ran and the error message. Add the output
of `llmention --version`. Leave out keys and anything private from your
answers.
