# llmention init

Write llmention.yaml for your product

## Usage

```
llmention init
```

## Examples

```
llmention init
llmention init --brand Tabslip --category "AI API cost tracking" --competitors "Helicone (helicone.ai), Langfuse" --engines openai,anthropic
llmention init --dry-run --brand Acme
```

## Flags

- `--brand` Your product or company name
- `--category` What it helps with in a few words, used in the starter prompts
- `--competitors` Competitors, comma-separated; add a site in brackets: "Helicone (helicone.ai), Langfuse"
- `--domain` Your website domains (optional)
- `--engines` Providers to ask: openai, anthropic, google, perplexity, openrouter (all four through one key) or mock (default: the ones with a key in your environment)
- `--force` Overwrite an existing llmention.yaml

## Output

Shows text you can read in your terminal.

Use --json to get JSON for scripts and other programs.

## Preview changes

This command changes things. Add `--dry-run` to see what it would change; nothing changes then.

## Related commands

- llmention run
- llmention quick
