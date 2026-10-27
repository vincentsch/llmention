# Modes

A mode says whether the model may search the web before it answers. Each
prompt goes to each engine once per mode. So one run can show what a model
knows from training next to what it says after a search.

| Mode in llmention.yaml | Short name for `--mode` | In the report | What it sends |
| --- | --- | --- | --- |
| `memory` | `no-search` | no web search | The question with web search turned off |
| `search_auto` | `search` | web search | The question with the provider's web search tool, which the model may use |
| `search_required` | `forced-search` | forced web search | The question with the search tool, and the model must use it |
| `provider_default` | `default` | provider default | The question as it is, with whatever the provider does by default |

The default is `modes: [memory, search_auto]`. In `llmention.yaml` you can use
the name or the short name.

## Which mode to watch

Without web search, the model answers from what it learned in training. That
changes when a provider releases a new model, which happens every few months. A
low rate here usually means your product was rare or missing in the text the
model learned from.

With web search, the answers are closer to what people get in the ChatGPT or
Perplexity apps when they ask about products. Those apps often search the web
before they answer such questions. This mode reacts to changes on the web
within days or weeks. For a newer product, the rate with web search is often
higher than without.

Forced web search makes the model search before it answers. That makes engines
easier to compare when one of them would rarely search by itself. llmention
leaves this mode out for Anthropic and OpenRouter. OpenRouter has no way to
force its search tool, and Anthropic's newest models refuse a forced tool call.
The dry run lists the modes it leaves out.

Provider default sends the question without saying anything about search.
Perplexity's Sonar models then search the web, and the other providers answer
from memory.

To run one mode only:

```sh
llmention run --mode search
```

## API answers differ from the app

llmention asks the models through their APIs. A person who types the same
question into a consumer app such as ChatGPT or Claude can get a different
answer, for several reasons:

- The apps add their own instructions to each conversation and can use what
  they remember about the user.
- The apps may send a question to a different model than the one you set up.
- The app's search and the API's search tool can find different pages.
- The person's location and language can change the answer.
- A model doesn't always give the same answer, even to the same request.

llmention does not use the consumer apps. It sends the same questions the same
way each time. So the numbers show best how your rate changes from week to
week, and how far you are from your competitors. The rate itself is measured
under these conditions. It is not a count of what all app users see.

To make a single unusual answer matter less, ask each prompt more than once
with `repetitions: 2` or `--repetitions 3`. This multiplies the cost.

## What llmention sends to the search tool

llmention offers the provider's own search tool and sends no location or site
filters with it. The provider decides which pages to read. If the answers
should fit a region, write the prompts in your buyers' language and use the
place names they use.
