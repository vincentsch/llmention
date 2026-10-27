# llmention discover

Suggest buyer questions to use as prompts

## Usage

```
llmention discover
```

## Examples

```
llmention discover
llmention discover --seed "LLM cost tracking" --seed "OpenAI usage dashboard"
llmention discover --from chab --seed "LLM cost tracking" --dry-run
llmention discover --from chab --seed "LLM cost tracking" --out prompts.txt
```

## Flags

- `--country` Country for search data, as a two-letter code
- `--force` Overwrite the --out file
- `--from` Where to find questions: chab, or leave it out for templates
- `--language` Language for search data
- `--limit` Show at most this many suggestions
- `--out` Write the suggestions to this prompts file, one per line
- `--pages` Search result pages to read with --from chab (5 Chab credits each)
- `--seed` A topic or search phrase (repeatable, default: brand.category)

## Output

Shows text you can read in your terminal.

Use --json to get JSON for scripts and other programs.

Use --jq to filter the JSON output with a jq expression.

Use --template to format the output with a Go template.

## Preview changes

This command makes changes. Use `--dry-run` to see what would change without changing anything.

## Metadata

This command can attach request metadata. In advanced-output apps, add `--include-meta` to a supported machine output mode to wrap the result as `{data, meta}`.

## Related commands

- llmention run
- llmention init
