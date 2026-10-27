# llmention.yaml reference

llmention reads `./llmention.yaml` unless you pass `--config <path>`. Paths
inside the file, such as `runs_dir` and `prompts_file`, are relative to the
file's folder. `llmention init` writes a commented starter file.

A complete example:

```yaml
version: 1

brand:
  name: Acme Books
  category: invoicing for freelancers
  aliases: [AcmeBooks]
  domains: [acme-books.example]
  products: [Acme Invoice Scanner]
  people: [Jordan Lee]

competitors:
  - name: Globex Billing
    domains: [globex.example]
  - name: Initech Invoices
    aliases: [Initech]

engines:
  - id: chatgpt
    provider: openai
    model: gpt-6.1-sol
    key_env: OPENAI_API_KEY
  - id: claude
    provider: anthropic
    model: claude-sonnet-5-5
    key_env: ANTHROPIC_API_KEY

prompts:
  - What are the best tools for invoicing for freelancers?
  - id: alternatives-globex
    prompt: What are good alternatives to Globex Billing?
    tags: [competitor]
prompts_file: prompts.txt

modes: [memory, search_auto]
repetitions: 1

execution:
  max_requests: 100
  retries: 2
  timeout: 5m
  concurrency: 4

runs_dir: ./.llmention/runs
```

## brand

| Field | Meaning |
| --- | --- |
| `name` | Your product as buyers write it. Required. |
| `category` | What it helps with in a few words. `init` and `discover` build prompts from it. |
| `aliases` | Other names that count as a mention of you. |
| `domains` | Your sites. Citations of these count as citations of you. |
| `products` | Product names that count as a mention of you. |
| `people` | Founders or authors. Only the recognition suite uses them. |

## competitors

The brands to compare yourself with. Each one has a `name`. It can also have
`aliases`, which count as a mention of it, and `domains`. Only list the
products your buyers compare you with. Each competitor lowers your share of
voice, because share of voice is your part of all the mentions of tracked
brands.

## engines

| Field | Meaning |
| --- | --- |
| `id` | Your label for the engine. Reports and `--engine` use it. |
| `provider` | One of the providers in [Providers and models](providers.md). |
| `model` | The provider's model id. |
| `key_env` | The environment variable that holds the key. |
| `base_url` | Send requests to a gateway or another host instead of the public API. |
| `max_output_tokens` | The longest answer the model may write, in tokens, thinking included. Not sent by default, except 16,000 for Anthropic, which requires it. A low limit cuts answers off, and llmention leaves them out of the numbers. |
| `capabilities` | Turn off features a gateway lacks, such as `supports_search_auto: false`. |
| `fixtures_path` | Mock only: a file of fixed answers, as in the research examples. |

[Providers and models](providers.md) has examples for each provider.

## prompts and prompts_file

`prompts` is a list of questions. An entry is either the question text, or a
`prompt` with an optional `id` and `tags`. `prompts_file` names a text file
with one prompt per line. Lines that start with `#` are ignored. llmention uses
the prompts from both, and a question that appears twice is asked once. See
[Writing prompts](prompts.md).

## modes and repetitions

`modes` lists the [modes](modes.md) to ask in. You can use the names or the
short names from the modes table. The default is `[memory, search_auto]`.

`repetitions` asks each prompt this many times per engine and mode. The
default is 1.

## execution

| Field | Default | Meaning |
| --- | --- | --- |
| `max_requests` | 100 | The most requests one run may send, retries included |
| `retries` | 2 | How often a failed request is tried again |
| `timeout` | `5m` | The longest wait for one answer |
| `concurrency` | 4 | Requests sent at the same time |
| `max_judge_requests` | 100 | The most requests the AI grader may get in one run |

See [Costs and the request budget](costs.md).

## runs_dir

The folder for saved runs, `./.llmention/runs` by default.
[Scheduled runs](ci.md) shows how to save it between CI runs.

## suites

```yaml
suites:
  paths: [examples/research/*.yaml]
```

Extra suite files to load. A suite is a set of questions plus the rules that
score their answers. Your prompts form the `prompts` suite, which runs by
default. Other suites run with `llmention run --suite <id>`. The
[research examples](../examples/research/README.md) show the format.

## notify

`command` and `webhook_url_env` set up alerts when a `--fail-under` check
fails. See [Scheduled runs](ci.md#alerts).

## AI grader

Without a grader, llmention counts mentions by matching names. A grader is a
second model that reads each answer and labels how it describes you. The
results appear as `framing.*` metrics in `llmention report --details`. The
[research suites](../examples/research/README.md) that check claims also work
better with a grader.

```yaml
analysis:
  model_assisted_enabled: true
  judge:
    provider: openai
    model: gpt-6-luna
    key_env: OPENAI_API_KEY
    allow_answer_disclosure: true
```

`allow_answer_disclosure: true` confirms that the grader's provider may read
the answers. `model_assisted_enabled` turns the grader on for every run.
`llmention run --judge` or `--judge=false` turns it on or off for one run. The
grader needs a model that can reply in a fixed JSON format, which providers
call structured output. OpenAI and Google models can do that. Each graded
answer costs one more request, and the dry run shows how many.
