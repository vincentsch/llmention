# What the numbers mean

`llmention run` and `llmention report` print the same summary. This page goes
through it from top to bottom. The examples come from `llmention demo`, where
Acme Books competes with Globex Billing and Initech Invoices.

## Which answers count

A brand's mention rate is the share of answers that name it. For each brand,
llmention leaves out the answers to prompts that name that brand. If a prompt
asks "What are good alternatives to Globex Billing?", the answer counts for
Acme Books and Initech Invoices but not for Globex Billing. An assistant
repeats the names in a question even when it knows nothing about them.
Counting those answers would make the rate look better than it is. When some
prompts name your product, a line under the report says how many were left
out.

Failed requests don't count. Unfinished answers also don't count. The provider
cut them off, for example because the answer reached the output token limit
(the most text the model may write) or because the model was still searching
the web. A cut-off answer may stop before your name would have come up, so it
tells you nothing. `llmention report --answers` shows the errors and the
unfinished answers with a label.

## How often assistants mention you

```text
  Engine        Mode           Mentioned  Rate  Share of voice  Avg. rank
  chatgpt-demo  no web search  9 of 10    90%   37%             1.9
  claude-demo   web search     6 of 10    60%   24%             1.7
```

There is one row for each engine and [mode](modes.md). When none of an
engine's answers in a mode can be counted, its row shows dashes and says why.
For example, it says `no answers to count: 3 stopped` after Ctrl+C, or
`no answers to count: 3 failed` when the provider returned errors.

Mentioned is the number of answers that name you, out of the answers that
count. Rate is the same number as a percentage. This is your mention rate, the
main number to watch.

Share of voice is your part of all the mentions of the brands you track. Say
the answers name Acme Books 3 times and its two competitors 7 times together.
Then Acme Books has a share of voice of 3 out of 10, or 30%. Each answer counts
at most once per brand. If an answer names you and one competitor, you get one
of its two mentions. Share of voice only uses answers to prompts that name none
of the tracked brands, so every brand's share comes from the same answers. Only
the brands in `llmention.yaml` take part, so adding a competitor lowers
everyone's share. A dash means that no tracked brand was mentioned, so there is
nothing to share.

Avg. rank is your average place in the answers that name you, where 1 means
first. When an answer is a list and you have your own entry, your place is the
position of that entry, counting every entry. Section headings such as
"## Summary" are not entries, and llmention also leaves out an entry about a
brand that the prompt named. When an answer has no list, your place is the
order in which the tracked brands first appear in the text. A dash means no
answer gave you a place. Either none named you, or the ones that did named you
outside the list.

## How a mention is found

llmention looks for your `brand.name` and your `brand.domains` in each answer.
It also looks for each entry in `brand.aliases` and `brand.products`.
Competitors work the same way with their name, aliases and domains, so
"globex.example" in an answer counts as a mention of Globex Billing.

Upper and lower case don't matter, so "acme books" and "ACME BOOKS" both
count for Acme Books. For a name that is also an everyday word, you can turn
that off, as [described below](#names-that-are-also-everyday-words). Accents
do matter, so "Café" doesn't match "Cafe". Add the other spelling as an alias
if answers use it.

A name only matches as whole words. llmention checks the character right
before and right after the name. If that character is a letter or a digit, the
name is part of a longer word and doesn't count. Any other character ends a
word, such as a space or an apostrophe. So "Acme Books" matches "Acme Books's
pricing" and "**Acme Books**", but not "Acme Bookstore". A plural
doesn't match either, so "two Globex Billings" doesn't count for Globex
Billing.

The characters inside a name have to match as you wrote them, apart from
case. "Acme Books" doesn't match "AcmeBooks" or "Acme-Books", so add those as
aliases. A domain matches wherever it appears in the text, also as part of a
link. "acme-books.example" also matches "www.acme-books.example" and
"app.acme-books.example/pricing", because a dot ends a word.

When one tracked name contains another, the longer one wins. Say you also track
a competitor called Acme. An answer that says "Acme Books" then counts only for
Acme Books, and a prompt about Acme Books doesn't count as naming Acme.

Add the spellings people use:

```yaml
brand:
  name: Acme Books
  aliases: [AcmeBooks, acme-books.example]
  products: [Acme Invoice Scanner]
```

### Names that are also everyday words

Some product names are also normal words, like Bonsai or Linear. Without help,
llmention can't tell the product from the word. "Bonsai" matches "Bonsai sends
reminders", but also "a bonsai tree".

Set `match_case: true` on your brand or on a competitor. Then its name only
counts when an answer writes it with the same capital letters as
`llmention.yaml` does:

```yaml
competitors:
  - name: Bonsai
    domains: [hellobonsai.com]
    match_case: true
```

Now "Bonsai sends reminders" counts, but "a bonsai tree" and "BONSAI" don't.
The setting applies to the name and its aliases, and for your brand also to
`products`. Domains still match in any case, so "HelloBonsai.com" in an answer
is still a mention of Bonsai.

Capitals don't solve everything. A sentence that starts with "Bonsai trees
need light" still counts, and so does another product that is also called
Bonsai. German writes every noun with a capital letter, so the tree, "ein
Bonsai", also counts there. For German prompts, `match_case` doesn't help at
all. So check it after the first run. Search the output of
`llmention report --answers` for the word and read the lines that contain it.

If many of them are about something else, change the name to a longer form,
such as "Bonsai invoicing", and put the competitor's site under `domains`.
That stops the wrong matches. But an answer that only says "Bonsai" then no
longer counts as a mention, so the rate can come out lower than it is. The
domain always counts, so "hellobonsai.com" in an answer is still a mention.

Each time you open a saved run, the report counts the mentions again with the
brand and competitors in your current `llmention.yaml`. So a new alias also
fixes old runs, and `report --diff` compares two runs with the same names.

## Mention rate next to your competitors

```text
  Engine · mode                 Acme Books (you)  Globex Billing  Initech Invoices
  claude-demo · no web search   0%                89%             67%
```

This table shows the mention rate of each tracked brand. Each competitor's rate
leaves out the prompts that name that competitor.

## Sites the answers cited

```text
  Site                Citations  In answers  Whose
  globex.example      11         11          Globex Billing
  acme-books.example  10         10          yours
  youtube.com         10         10
```

Answers with web search come with sources. The Citations column counts each
link to the site. In answers counts the answers that cite the site at least
once. Whose says which tracked brand the site belongs to, going by
`brand.domains` and each competitor's `domains`.

A site counts as a whole. `acme-books.example` covers `www.acme-books.example`
and `docs.acme-books.example`, and it makes no difference whether you write
`www.` in front. llmention refuses a domain entry with a path, such as
`github.com/acme/books`, because it would have to count every GitHub page as
yours. Answers without web search have no sources, so this table stays empty
unless you use a search mode.

A [--fail-under check](ci.md#fail-when-the-rate-drops) can also use your
citation rate. That is the share of web search answers that cite your site.
Answers that cite no site at all count as answers that don't cite you.

## Other names in the answers

This list shows names in the answers that match none of your tracked brands.
llmention finds them in lists and tables, and it also picks up names in bold
text. It leaves out labels such as "Pricing" or "Pro tip". The filter is
simple, so some entries may not be products. If a name shows up in several
answers, it is often a competitor you should add to `llmention.yaml`.

## What the assistants said

This part quotes up to three answers. Up to two are answers that name you, and
the rest are answers that named a competitor but not you. llmention picks them
from different engines where it can. `--answers` prints every answer in full.

## What stands out

This part lists up to five things the numbers show, for example:

- an engine that never named you
- an engine that mentioned you more often with web search on than off
- prompts where no assistant named you but at least one named a competitor
- the most cited sites that belong to no tracked brand
- names you do not track that came up in several answers

These lines describe what the answers showed. They do not say why an assistant
answered that way.

## Over time

`llmention report --trend` prints your mention rate for each engine and mode
across recent runs, with a small chart per column:

```text
  Run                chatgpt-demo · no web search  claude-demo · web search
  5 Oct 2026 18:58   50%                           10%
  12 Oct 2026 18:58  60%                           20%
  19 Oct 2026 18:58  60%                           40%
  26 Oct 2026 18:58  90%                           60%
  Trend              ▅▅▅▇                          ▂▂▄▅
```

`llmention report --diff` compares the latest run with the one before it, on
the prompts both runs asked. It only compares answers that came back complete
in both runs, so a failed request cannot change a rate. It lists the answers
that started or stopped naming you. It also tells you when an engine's model
changed or a prompt was reworded, because either one can change the numbers by
itself.

A run made with `--engine` or `--mode` asks only part of the project.
llmention never compares the next full run with it, and `--trend` leaves it
out.

## All metrics

`llmention report --details` prints every metric with its id, such as
`visibility.brand_mention_rate` or `sources.owned_domain_citation_rate`. You can
use these ids in [--fail-under checks](ci.md#fail-when-the-rate-drops).
`llmention report --details --json` also has them.

## Recognition

`llmention run --suite recognition.basic` asks each model, without web search,
who or what your brand is. It also asks about each product and person under
`brand`, and about each of your sites. The summary has one row per name and one
column per engine:

```text
Do assistants know Acme Books?
  Name                Kind     chatgpt-demo     claude-demo
  Acme Books          brand    did not know it  knew it
  Acme Ledger         product  knew it          knew it
  Jane Doe            person   knew it          knew it
  acme-books.example  site     knew it          knew it
  Knew                         3 of 4           4 of 4
```

| In a cell | What it means |
| --- | --- |
| knew it | The answer described the one you meant. |
| something else | The answer only described other things with the same name. |
| did not know it | The model named nothing. |
| unreadable answer | The answer was not in the JSON format the question asked for, so llmention could not read it. |
| possibly made up | The [AI grader](configuration.md#ai-grader) is on and judged the description to be invented. |
| no answer | The request got no complete answer. |

When a name was asked more than once, a cell reads like `knew it 2 of 3`. The
Knew row counts the answers that described yours, out of the answers llmention
could read. `llmention report --answers` lists what each model said.
`llmention report --details` has the rates behind the table, such as
`recognition.target_recognition_rate`.
