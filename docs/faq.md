# FAQ

## How is this different from hosted trackers like Peec AI or Profound?

Hosted trackers run the questions for you and store the results on their
servers. They charge a monthly fee. llmention runs on your computer with your
own API keys. You only pay the providers, which usually comes to well under a
dollar per run. The answers are saved in files on your computer. Each command
also has JSON output, so you can use llmention in scripts and CI jobs.

The downside is that llmention asks through the providers' APIs. So it measures
the models and their API search tools, and it does not open the consumer apps.
See [API answers differ from the app](modes.md#api-answers-differ-from-the-app).

## Does it send my data anywhere?

Only to the providers in your `llmention.yaml`. If you set up an AI grader or
an alert, those also get data. llmention collects no usage data, and there is
no account. `llmention update --check` asks GitHub for the latest release, but
only when you run it. `llmention run --dry-run` and `llmention run --mock` make
no network connection at all.

## Why don't answers to some of my prompts count?

A prompt that names your product gets an answer that names your product,
whatever the model knows. Counting it would make your rate look better than
it is. llmention leaves those answers out of your numbers and shows them with
`--answers`. The same rule applies to each competitor. [What the numbers
mean](metrics.md#which-answers-count) has the details.

## My mention rate changed, but nothing on my side did. Why?

Models answer differently from one request to the next, and providers update
their models and search indexes without notice. With ten prompts, one answer
more or less moves the rate by ten points. More prompts and `repetitions: 2`
make the numbers steadier. `llmention report --diff` shows which answers
changed, and it tells you when a model changed.

## Which models should I use?

Use the models closest to what your buyers use. The starter models are each
provider's current mid-sized model. A cheaper model answers differently from
the large one in the app. While you watch a trend, don't change the model, so
each run is compared with answers from the same model.

## Does it work for other languages?

Yes. Write the prompts in the language your buyers use. Name matching works
with accented letters and other writing systems. It ignores upper and lower
case unless you set [`match_case`](metrics.md#names-that-are-also-everyday-words).

`match_case` doesn't help in a language that writes every noun with a capital
letter, like German. A competitor called Bonsai then also counts in "ein
Bonsai", the tree. Check the answers for such a name after the first run.
[Names that are also everyday words](metrics.md#names-that-are-also-everyday-words)
says what to do when many of them are about something else.

The starter questions from `init` and `quick` are English templates. They stay
English when your category is in another language, so replace them with
questions in your buyers' language. `discover` fills the same templates. With
`--from chab --country DE --language de-DE` it suggests questions from German
search data instead.

To measure two languages, make a second project in its own folder, with its
own `llmention.yaml` and its own questions. Then each language gets its own
mention rate and its own trend. Tags on prompts don't split the report. So if
you put the questions for both languages into one project, the report gives
you one mention rate for both together. If you give a question the same `id`
in both projects, such as `best-tools`, you can compare its answers across the
two reports.

## Can I track several products?

Yes. Give each product its own folder with its own `llmention.yaml`. Runs are
saved next to that file, so each product has its own history. If you put
several config files in one folder, give each one its own `runs_dir`, and pass
`--config acme.yaml` to each command.

## Can I measure Google AI Overviews or Microsoft Copilot?

No. llmention asks the models through the APIs on the
[providers page](providers.md). Google and Microsoft have no official API that
returns the AI Overview or the Copilot answer for a search. Some third-party
search APIs do, but llmention does not support them.

## How do I share the results?

`llmention report --html report.html` writes one file with charts that opens
in any browser. `llmention report --format markdown` writes tables for a
GitHub job summary or a pull request. `llmention history export <run> --output
run.json` copies a saved run as JSON.

## Can I change llmention.yaml after a few runs?

Yes. Saved runs hold the answers, and the report counts the mentions again
with your current brand and competitors, aliases included. You can also change
prompts and engines. Comparisons use the prompts and engines that both runs
have, and they say what changed. If you reword a prompt, it gets a new id and
counts as a new prompt. To hold on to its history, give it its old id.
`llmention report --answers` shows the id, and you set it as the prompt's `id`
in `llmention.yaml`. Reports then compare the answers and mark the prompt as
reworded.

## Is it free?

Yes. llmention is open source under the [MIT license](../LICENSE).
