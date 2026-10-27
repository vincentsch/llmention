# Research suites

These suites test how assistants handle facts. They don't measure how often
assistants name your product. They are experimental examples. llmention only
runs them if you add them to your project, and the main report does not use
them.

They grade answers in a simple way. An answer counts as right when it contains
the expected answer or one of its listed spellings. Claim checks look for the
listed claims in the answer. So a right answer in other words can count as
wrong. Read the answers before you trust a number.

| Suite | What it measures | AI grader |
|---|---|---|
| `research.factoid` | Short factual questions with one right answer | not used |
| `research.temporal` | Questions whose answer changed recently, so a model that answers from memory is out of date | not used |
| `research.programming` | Facts about languages and APIs, such as which Go version added a feature | not used |
| `research.censorship` | Sensitive history questions, checked for refusals and for facts the answer leaves out | recommended |
| `research.propaganda` | Questions built on a false claim, checked for whether the answer repeats or corrects it | recommended |
| `research.robustness` | Whether your brand still comes up when the same question is worded differently | not used |
| `research.sources` | Which sites a searching assistant cites for questions about your brand | not used |

`research.robustness` and `research.sources` use the brand from your
`llmention.yaml`. The others come with their own questions and expected
answers.

## Try them without an API key

Copy the `examples` folder into your project and point a mock engine at the
made-up answers in `examples/research/fixtures/mock-answers.yaml`:

```yaml
version: 1
brand:
  name: Acme Books
  domains: [acme-books.example]
engines:
  - id: mock
    provider: mock
    model: demo
    fixtures_path: examples/research/fixtures/mock-answers.yaml
suites:
  paths: [examples/research/*.yaml]
```

Then:

```sh
llmention suites list
llmention run --suite research.factoid
```

The numbers come from the fixture file, so they only show what the report
looks like.

## Run them against real models

Replace the mock engine with your own engines, as in the
[quickstart](../../docs/quickstart.md). Check the size of the run first:

```sh
llmention run --suite research.temporal --dry-run
```

`research.censorship` and `research.propaganda` check which expected claims
an answer contains. Without a grader, a claim only counts when its text
appears in the answer. An AI grader reads each answer and decides which claims
it contains, also when the answer uses other words. Add one under `analysis`
and allow it to read the answers:

```yaml
analysis:
  model_assisted_enabled: true
  judge:
    provider: openai
    model: gpt-6.1-sol
    key_env: OPENAI_API_KEY
    allow_answer_disclosure: true
```

The grader costs one request per answer. The dry run shows how many.

## Write your own suite

A suite is a YAML file with an id and a type. It also has the list of
questions to ask, which llmention calls items. Copy the example closest to
what you need and change the items. Then check it:

```sh
llmention suites validate ./examples/research/my-suite.yaml
```

The mock answers are looked up by suite id, item id and mode. So new items
need new mock answers before they work with the mock engine.
