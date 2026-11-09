# Providers and models

Each entry under `engines` is one assistant to ask. It says which provider and
model to use, and which environment variable holds the API key:

```yaml
engines:
  - id: chatgpt
    provider: openai
    model: gpt-6.1-sol
    key_env: OPENAI_API_KEY
```

The `id` is your name for the engine. Reports show it, and `--engine` uses it.
Don't change it later, because comparisons between runs match engines by id.
Two engines can use the same provider with different models.

## Providers

| `provider` | API | Modes | Starter model |
| --- | --- | --- | --- |
| `openai` | OpenAI Responses API, with its web search tool | all | `gpt-6.1-sol` |
| `anthropic` | Anthropic Messages API, with its web search tool | no web search, web search, provider default | `claude-sonnet-5-5` |
| `google` | Gemini Interactions API, with Google Search | no web search, web search, forced web search, provider default | `gemini-3.8-flash` |
| `perplexity` | Perplexity Sonar API (see [Perplexity](#perplexity)) | no web search, web search, forced web search, provider default | `sonar` |
| `openrouter` | OpenRouter, with its web search tool (see [OpenRouter](#openrouter)) | no web search, web search, provider default | any model on OpenRouter |
| `openai-compatible` | Any Chat Completions API at `base_url` | no web search, provider default | the server's model name |
| `mock` | Made-up answers for demos and tests, no network | all | `demo` |

`llmention init` and `quick` write the starter models. They were current on the
providers' model pages in October 2026. Providers release new models every few
months, and you can change the id to any model your key can use.

For cheaper runs, llmention's price table also knows these smaller models:

- `gpt-6-luna` from OpenAI
- `claude-haiku-5-5` from Anthropic
- `gemini-3.5-flash-lite` from Google

If you want answers close to a consumer app, pick the model the app uses. The
apps choose their models themselves and don't always say which one answered,
so this only gets you close. [Modes](modes.md) explains what else differs.

## Which providers were checked with real requests

On October 29, 2026, the starter models of OpenAI and Anthropic and
Perplexity's `sonar` through OpenRouter answered real requests, with and
without web search. Their citations, answers cut off at the token limit and
errors for a wrong model id all came out as these pages describe.

On November 8, 2026, Gemini (`google/gemini-3.8-flash`) answered real
requests through OpenRouter, and so did `openai/gpt-6.1-sol` and
`anthropic/claude-sonnet-5.5` with web search. All three searched with their
own provider's search there.

The `google` provider, which talks to Google's own API, has so far only been
checked against a rejected key. The `perplexity` provider, which talks to
Perplexity's own API, is only tested against made-up answers. So when `init`
or `quick` set up either one, they say that OpenRouter is better tested. One
OpenRouter key reaches all four assistants, as shown under
[One OpenRouter key for all four assistants](#one-openrouter-key-for-all-four-assistants).

## Keys

`key_env` names the environment variable that holds the key. You can use any
name, for example when you have several keys for one provider. llmention reads
the key from the environment when it runs. It never writes the key to a file,
and it removes it from everything it prints. The dry run lists which keys are
set without printing them.

## Perplexity

llmention sends the requests of Perplexity engines to the Sonar API at
`/v1/sonar`. Perplexity says that support for Sonar Chat Completions ended on
September 27, 2026. Requests that wait for the full answer, which is what
llmention sends, still work. Perplexity is moving them over to its newer Agent
API, one model at a time.

llmention stays on Sonar until the Agent API has been tested with real
requests. Until then a Perplexity engine still works. But its answers can
change while Perplexity moves it over, and the price for `sonar` in
llmention's price table may no longer match your bill. If the Perplexity column
of the trend jumps, compare the date with Perplexity's changelog.

The pages a Sonar answer cites come from its `search_results` list. These are
the numbered sources behind the [1] and [2] in the text, and llmention counts
them as the answer's citations.

## Gemini

Gemini engines use the Interactions API (`/v1beta/interactions`) with the
Google Search tool. When an answer is based on a search, it lists its sources
as `url_citation` annotations. Some links go through a Google redirect
(`vertexaisearch.cloud.google.com`) and have the site's name as their title.
llmention counts those for the site named in the title.

## OpenRouter

One OpenRouter key reaches models from many providers:

```yaml
engines:
  - id: llama
    provider: openrouter
    model: meta-llama/llama-4-maverick
    key_env: OPENROUTER_API_KEY
```

Copy the model id from OpenRouter's model list. Web search goes through
OpenRouter's search tool. OpenAI and Anthropic models get their provider's own
search there, and Gemini gets Google Search. Other models get a search service
that OpenRouter picks, so their results can differ from what the model's own
provider would find.

### One OpenRouter key for all four assistants

If you don't have a key from each provider, one OpenRouter key is enough.
`llmention init --engines openrouter` writes these engines, and `init` and
`quick` do the same on their own when `OPENROUTER_API_KEY` is the only key
they find:

```yaml
engines:
  - id: chatgpt
    provider: openrouter
    model: openai/gpt-6.1-sol
    key_env: OPENROUTER_API_KEY
  - id: claude
    provider: openrouter
    model: anthropic/claude-sonnet-5.5
    key_env: OPENROUTER_API_KEY
  - id: gemini
    provider: openrouter
    model: google/gemini-3.8-flash
    key_env: OPENROUTER_API_KEY
  - id: perplexity
    provider: openrouter
    model: perplexity/sonar
    key_env: OPENROUTER_API_KEY
```

In the web search mode the model decides whether to search. In the live
check, Gemini answered two general questions from memory and searched for
two questions about recent releases and prices. Gemini's citations link
through a Google redirect (`vertexaisearch.cloud.google.com`), and llmention
counts each one for the site named in its title.

If your OpenRouter account has its own provider keys stored (OpenRouter calls
this BYOK), OpenRouter sends those requests with your key, and the provider
bills you directly.

Perplexity's Sonar models work the same way, with `model: perplexity/sonar`.
They search the web before every answer and can't use OpenRouter's search
tool, so llmention sends them the question without it. For the no web search
mode, llmention sends Perplexity's `disable_search` switch, which OpenRouter
passes on. Their citations come back as links in the answer, and llmention
counts them like any other citations. Perplexity charges a fee for every
request, also when it doesn't search. For `perplexity/sonar` it was $0.005 in
October 2026.

Some models on OpenRouter can't use tools at all. A request in the web search
mode then fails with "No endpoints found that support tool use". Turn web
search off for such an engine:

```yaml
  - id: small
    provider: openrouter
    model: some/model-without-tools
    key_env: OPENROUTER_API_KEY
    capabilities:
      supports_search_auto: false
```

## Gateways and other hosts with base_url

`base_url` sends an engine's requests somewhere other than the provider's
public API, for example to a gateway that passes them on to the provider. The
key travels with each request, so `base_url` must be an https URL. Plain http
only works on your own machine (`localhost` or `127.0.0.1`).

For `provider: openai`, llmention sends requests to `<base_url>/responses`, so
the host must support OpenAI's Responses API. Azure OpenAI's v1 API does:

```yaml
  - id: azure
    provider: openai
    model: my-gpt-deployment     # your deployment name
    key_env: AZURE_OPENAI_API_KEY
    base_url: https://my-resource.openai.azure.com/openai/v1
```

For `provider: anthropic`, llmention sends requests to `<base_url>/messages`,
so include `/v1` in the URL, as in `https://api.anthropic.com/v1`.

If you want a signed receipt with the cost of each request, point `base_url` at
a local [Tabslip](https://github.com/vincentsch/tabslip) gateway, such as
`http://127.0.0.1:4545/openai/v1` for OpenAI or
`http://127.0.0.1:4545/anthropic/v1` for Anthropic.

## Chat Completions services and local models

Providers that offer an OpenAI-style Chat Completions API work with
`provider: openai-compatible`. llmention sends requests to
`<base_url>/chat/completions`:

| Service | `base_url` |
| --- | --- |
| DeepSeek | `https://api.deepseek.com` |
| xAI | `https://api.x.ai/v1` |
| Groq | `https://api.groq.com/openai/v1` |
| Mistral | `https://api.mistral.ai/v1` |
| Ollama on your machine | `http://localhost:11434/v1` |

```yaml
  - id: deepseek
    provider: openai-compatible
    model: deepseek-chat
    key_env: DEEPSEEK_API_KEY
    base_url: https://api.deepseek.com
```

Take the model id from the service's model list. A local server without a key
needs no `key_env`. These engines only answer from memory. Each service turns
on web search in its own way, so llmention leaves the search modes out for
them. The cost estimate lists them as unknown, except when they run on your own
machine.

## Limits of each provider

- Anthropic and OpenRouter let the model decide whether to search, so
  llmention leaves forced web search out for them. Anthropic can force a tool
  with `tool_choice`, but its newest models, Claude Sonnet 5.5 and Opus 5.5,
  refuse that with an error.
- Anthropic engines use the basic web search tool (`web_search_20250305`),
  which every current Claude model accepts.
- Anthropic's and OpenRouter's search tools may run up to five searches per
  answer. The provider bills each search.
- Perplexity charges a fee per request on top of the tokens. llmention's price
  table has this fee for `perplexity/sonar` on OpenRouter, but not for
  Perplexity's own API. For the `perplexity` provider the estimate leaves the
  fee out and says so.
- An engine's `max_output_tokens` limits the length of each answer, including
  the model's thinking. If you don't set it, llmention sends no limit. The
  exception is Anthropic, which requires one, so llmention sends 16,000 tokens
  there. That leaves the model room to think and still answer. You only pay
  for the tokens a model uses. A low limit cuts answers off, and llmention then
  marks them unfinished and leaves them out of the numbers.

## Turning features off

If a gateway doesn't support everything the provider does, turn the missing
features off for that engine:

```yaml
  - id: gateway
    provider: openai
    model: gpt-6.1-sol
    key_env: GATEWAY_KEY
    base_url: https://gateway.example.com/v1
    capabilities:
      supports_search_auto: false
      supports_search_required: false
```

llmention then leaves those modes out for the engine. `llmention run
--dry-run` shows what it will send.
