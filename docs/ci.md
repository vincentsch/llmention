# Scheduled runs

One run shows one moment. If you run the same prompts every week, you see
whether your product comes up more often, and when a model update or a new
article changed that. This page shows how to run llmention on a schedule and
how to save its history between runs.

## What has to be saved between runs

llmention saves each run as one JSON file under `runs_dir` (default
`.llmention/runs`). Next to the runs is an `index.json` that makes listing them
fast. Trends and comparisons read those files, so the folder has to be there
for the next run. A new CI machine starts with an empty folder, so the examples
below save it somewhere.

The files hold the full answers and your prompts. Store them in a private
repository or other private storage. They never contain API keys.

If the index is lost, llmention rebuilds it from the run files. You can also
copy run files into the folder by hand.

## GitHub Actions

Add your provider keys as repository secrets. Then save this as
`.github/workflows/llmention.yml` in the repository that has your
`llmention.yaml`:

```yaml
name: llmention
on:
  schedule: [{cron: "0 6 * * 1"}]   # Mondays at 06:00 UTC
  workflow_dispatch:
permissions:
  contents: write
jobs:
  llmention:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Install llmention
        run: |
          curl -fsSL https://raw.githubusercontent.com/vincentsch/llmention/main/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - name: Ask the assistants
        run: llmention run --fail-under mention-rate=30%
        env:
          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
      - name: Save the run and the summary
        if: always()
        run: |
          llmention report --trend --format markdown >> "$GITHUB_STEP_SUMMARY"
          llmention report --format markdown >> "$GITHUB_STEP_SUMMARY"
          git config user.name "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
          git add .llmention/runs
          git diff --cached --quiet || { git commit -m "Weekly llmention run" && git push; }
```

The last step runs even when the `--fail-under` check fails, so a bad week
also gets saved. `--format markdown` writes tables that GitHub shows on the
run's summary page. It works for the summary and also with `--trend` and
`--diff`. If the run step failed before it saved anything, there is nothing to
commit, and the step ends without an error. To use a fixed llmention version,
set `LLMENTION_VERSION=v0.1.0` in the environment of the install step.

### With one OpenRouter key

`llmention init --engines openrouter` sets up all four assistants through
OpenRouter, so the job needs only one secret. Add your key as the repository
secret `OPENROUTER_API_KEY`, and give the run step only that variable:

```yaml
      - name: Ask the assistants
        run: llmention run --fail-under mention-rate=30%
        env:
          OPENROUTER_API_KEY: ${{ secrets.OPENROUTER_API_KEY }}
```

The rest of the job stays the same.

### Without committing the runs

If you don't want the runs in your repository, store them in a separate
repository or a storage bucket. Copy the folder in before the run and back out
after it. The Actions cache doesn't work well for this. GitHub removes cache
entries that nobody used for a week, and a week is the gap between two weekly
runs.

## Cron on your own machine

```sh
# crontab -e
0 6 * * 1  cd /home/me/acme-marketing && . ./.keys && /home/me/.local/bin/llmention run --fail-under mention-rate=30% >> llmention.log 2>&1
```

`.keys` holds the `export OPENAI_API_KEY=...` lines. With one OpenRouter key,
it is the single line `export OPENROUTER_API_KEY=...`. Make it readable only
by you (`chmod 600 .keys`), and don't commit it.

## Fail when the rate drops

`--fail-under` works on `run` and on `report`. When a check fails, llmention
exits with code 7 and the job turns red. The threshold is a percentage or a
fraction:

| Check | Meaning |
| --- | --- |
| `mention-rate=40%` | every engine and mode must name you in at least 40% of the counted answers |
| `share-of-voice=25%` | your share of all tracked mentions, in every engine and mode |
| `citation-rate=10%` | the share of web search answers that cite your site, in every engine and search mode |
| `mention-rate[engine=chatgpt]=40%` | only the `chatgpt` engine |
| `mention-rate[mode=search]=40%` | only the web search mode |

The check uses the lowest value it finds, and the report says which engine and
mode it was. Repeat `--fail-under` for several checks.

A check only passes when every engine and mode it covers has a number. Say all
requests of one engine failed, or no tracked brand came up, so there is no
share of voice. Then the check is inconclusive. That fails the job like a miss,
with exit code 7, so a broken engine never looks like a pass.

The citation rate counts every answer in a web search mode, including those
that cite nothing. An engine that does not report the pages it cites, such as
a local model, has no citation rate.

You can also check any metric from `llmention report --details` with the long
form `metric:<id>`. The check needs exactly one number, so name the level. If
the run has several engines or modes, also name the engine and mode:

```sh
llmention report --fail-under "metric:sources.owned_domain_citation_rate[level=suite_mode_engine,engine=chatgpt,mode=search]=10%"
```

llmention refuses rank metrics. A lower rank is better, so "fail under" would
point the wrong way. The engines and modes in a check must be part of the run,
so a typo stops the run before any request is sent.

## Alerts

When a check fails during `llmention run`, llmention can run a script or send
the result to a webhook, a URL that accepts messages. Set either one in
`llmention.yaml`:

```yaml
notify:
  command: ./scripts/llmention-alert.sh
  webhook_url_env: LLMENTION_WEBHOOK_URL
```

`command` is an executable file. It gets a JSON summary on standard input.
`webhook_url_env` names an environment variable that holds a URL. llmention
sends the same JSON to that URL as a POST request. The JSON holds:

- the run id and the brand
- the checks that failed
- the mention rate for each engine and mode
- the commands that show the report and the diff

A script can turn that into a chat or email message.

llmention gives each of them ten seconds. If an alert fails, llmention says
so, but the exit code of the run stays the same.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The run finished and every check passed. Some answers may be unfinished; the summary says how many |
| 1 | The command or `llmention.yaml` has a problem, or the budget is too small |
| 2 | Some requests failed or were not sent; the run was saved with the answers that came back |
| 3 | An API key is missing |
| 5 | There is no saved run to show, or no earlier run to compare with |
| 7 | A `--fail-under` check failed or was inconclusive |
| 130 | The run was stopped early; the answers so far were saved |

A dry run exits with 0 whenever `llmention.yaml` loads, also when a key is
missing or the run doesn't fit the budget. To stop a script before the real
run, check the JSON of the dry run. This line fails when anything would stop
the run:

```sh
llmention run --dry-run --json | jq -e '.keys_missing == [] and .fits_budget and .problems == []'
```

`keys_missing` and `problems` are always in the JSON. They are empty lists
when nothing is missing and nothing would stop the run.
