# llmention run

Ask the assistants your prompts and save the answers

## Usage

```
llmention run
```

## Examples

```
llmention run --dry-run
llmention run
llmention run --mock
llmention run --engine chatgpt --mode search
llmention run --fail-under mention-rate=40%
llmention run --suite recognition.basic
llmention run --json
```

## Flags

- `--engine` Only ask these engines (ids from llmention.yaml, comma-separated)
- `--fail-under` Fail with exit code 7 when a number is below a threshold, for example mention-rate=40% (repeatable)
- `--judge` Use the AI grader set up under analysis.judge
- `--max-requests` The most requests this run may send, retries included (default execution.max_requests)
- `--mock` Let the mock engine make up every answer, and save the run apart from real runs
- `--mode` Only use these modes (no-search, search, forced-search, default)
- `--repetitions` Ask each prompt this many times (default from llmention.yaml)
- `--suite` Run these suites instead of your prompts, for example recognition.basic

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

- llmention report
- llmention init
