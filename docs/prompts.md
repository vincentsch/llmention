# Writing prompts

The prompts are the questions llmention sends to each assistant. Your mention
rate is only as good as these questions, so it's worth spending some time on
them.

## Ask what a buyer would ask

Write the questions someone asks before they know your product exists:

- "What are the best tools for invoicing for freelancers?"
- "I send about ten invoices a month. What should I use?"
- "What are good alternatives to Globex Billing?"
- "Which invoicing apps work well with a German tax advisor?"

Questions about a problem or a use case are often closer to what people type
than questions about a product category. A question about a competitor shows
whether assistants suggest you as an alternative.

Leave your own product out. An answer to "Is Acme Books good?" names Acme Books
no matter what the model knows, so it tells you nothing about whether the
assistant would recommend you. llmention leaves those answers out of your
numbers and says so under the report. `--answers` still shows them, which helps
when you want to read what assistants say about you. To check whether models
know your product at all, use the built-in recognition suite:
`llmention run --suite recognition.basic`.

Don't let the wording lead the model toward your pitch. If your pitch is
"affordable and simple", don't ask "Which affordable and simple invoicing tools
for freelancers exist?". Ask the way a buyer would.

## How many prompts

Eight to twenty prompts is a good range. Each prompt is sent once per engine
and mode, so twelve prompts on two engines in two modes make 48 requests per
run. More prompts make the rate steadier from week to week. They also cost
more, as the dry run shows.

## Where to put the prompts

The simplest way is a list in `llmention.yaml`:

```yaml
prompts:
  - What are the best tools for invoicing for freelancers?
  - What are good alternatives to Globex Billing?
```

In this form, with one prompt per line, a question mark at the end is fine. If
you write the list in square brackets on one line, YAML needs quotes around
each prompt.

For a longer list, put the prompts in a text file, one prompt per line.
llmention ignores blank lines and lines that start with `#`:

```yaml
prompts_file: prompts.txt
```

Prompts from the file come after the ones in `prompts`.

## Reword a prompt without losing its history

llmention compares runs prompt by prompt. Each prompt gets an id made from its
words and a short code of its full text, such as
`best-tools-invoicing-freelancers-f1d0`. The code tells "Best C++ IDE?" and
"Best C# IDE?" apart. If you reword a prompt, it gets a new id. The next
comparison then treats it as a new question and leaves it out of the diff.

To hold on to a prompt's history, give it a fixed id.
`llmention report --answers` shows the current id of each prompt:

```yaml
prompts:
  - id: best-tools
    prompt: What are the best invoicing tools for freelancers in 2026?
    tags: [category]
```

Tags are optional labels for your own use. They are saved with each answer.

When you reword a prompt that has a fixed id, `--diff` and `--trend` still
compare it and mark it as reworded. The new wording can change the answers by
itself.

## Ask more than once

Assistants don't always give the same answer to the same question.
`repetitions: 2` sends each prompt twice per engine and mode and counts both
answers. That doubles the cost, and a single unusual answer matters less.

## Let llmention suggest prompts

`llmention discover` suggests prompts. Without `--from` it fills question
templates with the topics you pass with `--seed`, or with your
`brand.category`. That costs nothing. `init` already wrote the template
questions for your category, so try other topics:

```sh
llmention discover --seed "invoicing for freelancers" --seed "time tracking"
```

With a [Chab](https://chab.ai) API key it uses search data instead. It reads
the "People also ask" questions and the result titles that Google shows for
your topics. It also asks Chab for related search phrases and how often people
search for them each month. The suggestions come sorted by that number:

```sh
export CHAB_API_KEY=...
llmention discover --from chab --seed "invoicing for freelancers" --dry-run
llmention discover --from chab --seed "invoicing for freelancers" --out prompts.txt
```

The dry run shows how many Chab credits the request uses. The key needs the
`api:search:read` and `api:seo:read` scopes. `discover` leaves out suggestions
that name your brand or repeat a prompt you already have. Read the file before
you use it, and delete the questions a buyer would not ask. So far
`--from chab` has only been tested against made-up Chab answers, not against
Chab's live API.
