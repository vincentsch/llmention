# Quickstart

This page takes you from installing llmention to a report about your own
product. For the last part you need an API key from at least one provider.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vincentsch/llmention/main/install.sh | sh
```

The installer downloads the program for your system and checks its SHA-256
checksum. It installs to `~/.local/bin`, or to `INSTALL_DIR` if you set it. If
it prints a PATH command, run that. Then check that it works:

```sh
llmention --version
```

On Windows without Git Bash, download `llmention-windows-amd64.exe` and
`checksums.txt` from the latest GitHub release. Compare the hash from
`Get-FileHash -Algorithm SHA256` with the line in `checksums.txt`. With Go
installed, `go install github.com/vincentsch/llmention/cmd/llmention@latest`
works on every system.

## Try the demo

```sh
llmention demo
```

The demo needs no key and sends nothing over the network. It makes up a market
where Acme Books competes with Globex Billing and Initech Invoices, and two
demo engines answer ten questions. It prints the same report you get for your
own product, followed by four weeks of made-up history. The report says at the
top that it shows demo data.

To try the other commands, save the demo project in a folder:

```sh
llmention demo --keep ./llmention-demo
cd llmention-demo
llmention report --answers
llmention report --diff
llmention report --html report.html
```

## Describe your product

Export an API key for each provider you want to ask:

```sh
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
```

llmention reads the keys from your environment. It never writes them to a
file, and it removes them from everything it prints. Then write
`llmention.yaml`:

```sh
llmention init
```

`init` asks about your product and your competitors. It suggests the providers
whose keys it finds. You can also pass everything as flags:

```sh
llmention init --brand "Acme Books" --category "invoicing for freelancers" \
  --domain acme-books.example \
  --competitors "Globex Billing (globex.example), Initech Invoices" \
  --engines openai,anthropic
```

Put a competitor's website in brackets after its name. Then the report can tell
which cited sites belong to which competitor.

Each assistant you ask is called an engine in llmention. An engine is a
provider and a model, such as OpenAI with `gpt-6.1-sol`.

## Check the questions

Open `llmention.yaml`. The `prompts` list holds eight starter questions built
from your category. For each of your first two competitors, it also has a
question that asks for alternatives to that competitor. With the two
competitors above, that makes ten questions, such as:

```yaml
prompts:
  - What are the best tools for invoicing for freelancers?
  - I run a small company. What would you recommend for invoicing for freelancers?
  - What are good alternatives to Globex Billing?
```

Rewrite them until they sound like your buyers. Leave your product's name out.
If a question names you, the answer tells you nothing about whether the
assistant would bring you up by itself. So llmention doesn't count those
answers toward your mention rate, the share of answers that name you.
[Writing prompts](prompts.md) has more advice, and `llmention discover`
suggests more questions.

## Preview the run

```sh
llmention run --dry-run
```

```text
Dry run: nothing was sent.
Would send 40 requests: 10 prompts to 2 engines, in no web search and web search modes.
Failed requests are retried. The run stops at 100 requests, its budget.
Estimated cost: about $0.60, from list prices checked on 2026-10-26. Thinking tokens and extra web searches can make it higher.

  chatgpt      openai gpt-6.1-sol, 20 requests
  claude       anthropic claude-sonnet-5-5, 20 requests

Keys found: OPENAI_API_KEY, ANTHROPIC_API_KEY
```

Each prompt goes to each engine once per mode. The two default modes ask once
without web search, so the model answers from what it learned in training, and
once with web search allowed. [Modes](modes.md) explains the difference, and
[Costs](costs.md) explains the estimate and the budget.

## Run it

```sh
llmention run
```

While it runs, a line in the terminal counts the finished requests. Ctrl+C
stops the run and still saves the answers so far. When the run is done,
llmention prints the summary and saves the run under `.llmention/runs`.

`llmention quick` writes `llmention.yaml` from flags and runs it in one
command, without asking you anything. Add `--dry-run` to see the plan first:

```sh
llmention quick --brand "Acme Books" --category "invoicing for freelancers" \
  --competitors "Globex Billing, Initech Invoices"
```

## Read the report

```sh
llmention report                     # the latest run
llmention report --answers           # every answer, grouped by question
llmention report --details           # every metric, for spreadsheets and checks
llmention report --html report.html  # one file with charts to share
llmention history list               # all saved runs
```

[What the numbers mean](metrics.md) goes through each part of the report.

## Next week

Run `llmention run` again. Then:

```sh
llmention report --diff    # what changed since the run before
llmention report --trend   # mention rate across recent runs
```

The comparison uses the questions both runs asked and the brand names in your
current `llmention.yaml`. It only compares answers that came back complete in
both runs. It also tells you when a model changed or a question was reworded
between the runs. To run llmention on a schedule, see
[Scheduled runs](ci.md).
