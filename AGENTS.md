[Changing llmention itself? Read CONTRIBUTING.md](CONTRIBUTING.md)

# Set up llmention for a product

llmention asks AI assistants buyer questions through their APIs and reports how
often they mention a product and its competitors. Follow these steps when a
user asks you to set it up. Also follow the rules of the project you work in.

The setup prompt is:

```text
Set up llmention for this product.
Use https://github.com/vincentsch/llmention: read its AGENTS.md and follow
the steps. Write llmention.yaml with our competitors, show me the dry run
and its cost, then run it once and show me the report.
```

## 1. Learn about the product

Read the repository's README and its package metadata. Also read the website
copy. Write down:

- the product name as buyers write it, plus other spellings or old names
- what it helps with, in a few words a buyer would use ("invoicing for
  freelancers", not "a modern billing platform")
- the domains of its website and docs
- two to five competitors, with their domains when you know them

Take competitors from the project itself, such as comparison pages or
"alternatives" sections. Do not invent them. If the project names none, ask
the user. Ask in one message, together with anything else you could not find.

## 2. Check that llmention is installed

Run `llmention --version`. Only a command-not-found result means it is
missing. Install it with:

```sh
curl -fsSL https://raw.githubusercontent.com/vincentsch/llmention/main/install.sh | sh
```

The installer works on Linux and macOS, and on Windows through Git Bash. It
installs to `INSTALL_DIR` when that is set, otherwise to `~/.local/bin`. If
the shell still can't find the command, add that directory to PATH for this
process and tell the user. Do not edit shell profiles unless asked. With Go
available, `go install github.com/vincentsch/llmention/cmd/llmention@latest`
also works.

## 3. Check which API keys are set

llmention reads provider keys from environment variables. Check which are set
without printing their values:

```sh
env | cut -d= -f1 | grep -E '^(OPENAI|ANTHROPIC|GEMINI|PERPLEXITY|OPENROUTER)_API_KEY$'
```

Never copy a key into a file and never use keys from another project.

Only a real run needs keys. These commands work without any key and cost
nothing:

- `llmention init`, which writes `llmention.yaml`
- `llmention run --dry-run`, which shows the requests and the cost
- `llmention run --mock`, which shows the report for this product with
  made-up answers
- `llmention demo`, which shows a report for a made-up market

If no key is set, or the user wants to spend nothing yet, go on with steps 4
and 5. Pass the providers the user wants to `--engines`. If the user has no
preference, use `--engines openrouter`, because one OpenRouter key covers all
four assistants. Then follow [Show the report without a key](#show-the-report-without-a-key)
instead of step 6, and tell the user which variables to export.

## 4. Write llmention.yaml

Work in the directory the user picked, or the repository root. Pass the
providers that have keys to `--engines`:

| Key | `--engines` name | Tested with real requests |
| --- | --- | --- |
| `OPENAI_API_KEY` | `openai` | yes |
| `ANTHROPIC_API_KEY` | `anthropic` | yes |
| `GEMINI_API_KEY` | `google` | less than OpenRouter |
| `PERPLEXITY_API_KEY` | `perplexity` | less than OpenRouter |
| `OPENROUTER_API_KEY` | `openrouter` | yes, for all four assistants |

The direct `google` and `perplexity` connections are less tested than
OpenRouter, which covers all four assistants with one key.
[Providers and models](docs/providers.md#which-providers-were-checked-with-real-requests)
says what was checked. If the user has an OpenRouter key, use it for Gemini and
Perplexity. For example, `--engines openai,anthropic,openrouter` asks ChatGPT
and Claude directly and the other two through OpenRouter.

```sh
llmention init --brand "Acme Books" --category "invoicing for freelancers" \
  --domain acme-books.example \
  --competitors "Globex Billing (globex.example), Initech Invoices" \
  --engines openai,anthropic
```

Then open `llmention.yaml`:

- Add other names for the product under `brand.aliases` and product names
  under `brand.products`. Names match without regard to capitals and only as
  whole words. If the product or a competitor has a name that is also an
  everyday word, like Bonsai, set `match_case: true` on it. Then only "Bonsai"
  with a capital B counts.
  [Names that are also everyday words](docs/metrics.md#names-that-are-also-everyday-words)
  explains it.
- Rewrite the starter prompts so they sound like this product's buyers. Use
  eight to fifteen. Leave the product's own name out of every prompt, because
  answers to a prompt that names the product do not count toward its mention
  rate. Questions about a competitor ("What are good alternatives to Globex
  Billing?") are good prompts.
- The starter prompts are in English. If the buyers ask in another language,
  write the prompts in that language. For a second language, make a second
  project in its own folder, as the
  [FAQ](docs/faq.md#does-it-work-for-other-languages) explains.
- Leave the model ids as they are unless the user asked for others. They are
  listed in [Providers and models](docs/providers.md).

[Writing prompts](docs/prompts.md) has more advice.

## 5. Show the dry run

```sh
llmention run --dry-run
```

It sends nothing. Show the user how many requests it would send to which
engines, and the estimated cost. The setup prompt allows one run, so do not
ask again unless the estimate is above $2 or the dry run reports a problem.
These count as problems:

- a "Keys missing" line
- a run that needs more requests than the budget
- a line that starts with "The run would not start"

Fix them in `llmention.yaml`, or ask the user to export a missing key. Then run
the dry run again. If a note says that the budget leaves little room for
retries, set `execution.max_requests` to the number the note gives.

The dry run exits with code 0 even when it finds a problem. In the output of
`llmention run --dry-run --json`, the real run can start when:

- `keys_missing` is an empty list
- `fits_budget` is `true`
- there is no `problems` list

## Show the report without a key

```sh
llmention run --mock
llmention report --mock --html preview.html
```

`--mock` lets the built-in mock engine answer for every engine in
`llmention.yaml`. It needs no key and sends nothing. The report uses the
product's own names and questions, so the user sees what a real report will
look like. Every answer and every number in it is made up, and the report says
so at the top. Tell the user that too.

The mock runs are saved in `.llmention/runs/mock`, apart from the real runs,
so they never mix with the real history. To see them again, add `--mock` to
`llmention report`. Stop here until the user has exported the keys, then go
on with step 6.

## 6. Run once and show the report

```sh
llmention run
```

A run of 40 requests usually takes a few minutes. Show the user the summary it
prints. Point out the mention rate per engine and the competitor table. Then
go through the "What stands out" list. Offer `llmention report --html
report.html` for a page they can share.

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | The run finished. Some answers may be unfinished; the summary says how many |
| 1 | The command or `llmention.yaml` has a problem; the message says what |
| 2 | Some requests failed or were not sent; the run was saved with the answers that came back |
| 3 | An API key is missing |
| 5 | There is no saved run to show, or no earlier run to compare with |
| 7 | A `--fail-under` check failed or was inconclusive |
| 130 | The run was stopped early; the answers so far were saved |

For exit code 2, read `llmention report --answers` to see the provider errors
before you send anything again. Answers marked unfinished were cut off by the
provider. They do not change the exit code, but they are left out of the
numbers. Add `--json` to any command for output you can parse. The full metric
table is large, so it is only in `llmention report --details --json`.

## 7. Tell the user what changed

Tell the user:

- where `llmention.yaml` is and that the keys stay in their environment
- that runs are saved under `.llmention/runs` and hold the full answers
- how to run it again: `llmention run`, then `llmention report --diff`
- how to run it every week, from [Scheduled runs](docs/ci.md)
- how to undo the setup: delete `llmention.yaml` and `.llmention/`

Ask before adding `.llmention/` to version control or to `.gitignore`. Ask
before setting up a scheduled job.
