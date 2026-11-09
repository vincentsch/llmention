# llmention quick

Write llmention.yaml and run it in one step

## Usage

```
llmention quick
```

## Examples

```
llmention quick --brand Tabslip --category "AI API cost tracking" --competitors "Helicone,Langfuse" --engines openai,anthropic --dry-run
llmention quick --brand Tabslip --category "AI API cost tracking" --competitors "Helicone,Langfuse" --engines openai,anthropic
llmention quick --brand Acme --category "invoicing for freelancers" --engines mock
```

## Flags

- `--brand` Your product or company name
- `--category` What it helps with in a few words, used in the starter prompts
- `--competitors` Competitors, comma-separated; add a site in brackets: "Helicone (helicone.ai), Langfuse"
- `--domain` Your website domains (optional)
- `--engines` Providers to ask: openai, anthropic, google, perplexity, openrouter (all four through one key) or mock (default: the ones with a key in your environment)
- `--force` Replace an existing llmention.yaml

## Output

Shows text you can read in your terminal.

Use --json to get JSON for scripts and other programs.

Use --jq to filter the JSON output with a jq expression.

Use --template to format the output with a Go template.

## Preview changes

This command changes things. Add `--dry-run` to see what it would change; nothing changes then.

## Request details

Add `--include-meta` to the JSON output to get the result as `{data, meta}`, where `meta` holds details about the request.

## Related commands

- llmention init
- llmention run
- llmention demo
