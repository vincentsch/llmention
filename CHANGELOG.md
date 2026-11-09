# Changelog

## Unreleased

First release of llmention.

- Ask the models of four providers your buyers' questions through their APIs, with your own keys. Each question is asked with and without web search.
- Report the mention rate and share of voice for your product and each competitor, per engine and mode, with the average rank.
- Leave answers to prompts that name a brand out of that brand's numbers, and work out share of voice from prompts that name no tracked brand.
- Save answers that the provider cut off with an "unfinished" label, and leave them out of the numbers.
- List the sites the answers cite and the names you do not track yet.
- List what stands out in the numbers, without guessing at the causes.
- Compare a run with the one before it on answers both runs have, and show the mention rate across recent runs. A full run is never compared with a run made with `--engine` or `--mode`.
- Write the report as text or Markdown, or as a single HTML file with charts. Every command also has JSON output.
- Count the mentions in saved runs again with the current brand names, so a new alias also fixes old runs.
- Count a name that is also an everyday word, like Bonsai, only with the same capital letters when you set `match_case: true` on it.
- Estimate the cost before a run, in total and for each engine, and stop at a request budget that includes retries. The dry run warns when the budget leaves little room for retries, and `init` sets a budget that fits a full run with all its retries. It also gives the cost of a year of weekly runs, and its JSON lists missing keys under `keys_missing`.
- Save the answers so far when a run is stopped with Ctrl+C.
- Fail CI jobs with `--fail-under` checks. A check also fails when an engine has no number to check.
- Send an alert by script or webhook when a check fails.
- Ask models through OpenRouter or a gateway such as Azure OpenAI. Perplexity's Sonar models on OpenRouter work with and without web search.
- Ask ChatGPT and Claude through one OpenRouter key, and also Gemini and Perplexity. `init` and `quick` set this up when `OPENROUTER_API_KEY` is the only key they find, and they say when the direct Gemini or Perplexity connection they set up is less tested.
- Ask hosted and local models through Chat Completions services such as DeepSeek or Ollama.
- Suggest buyer questions from templates or from Chab search data.
- Check whether models know your brand with `llmention run --suite recognition.basic`, with one row per name and one column per engine.
- Try everything without a key in `llmention demo`. `llmention run --mock` shows the report for your own product with made-up answers, and saves it apart from your real runs.
- Include research suites for factual accuracy as examples.
