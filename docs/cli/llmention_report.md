# llmention report

Show what the assistants said and how it changed

## Usage

```
llmention report [run-id] [run-id]
```

## Examples

```
llmention report
llmention report --answers
llmention report --diff
llmention report --trend
llmention report --html report.html
llmention report --format markdown >> "$GITHUB_STEP_SUMMARY"
llmention report 20261026-1608 --details
llmention report --fail-under mention-rate=30%
llmention report --mock --html preview.html
```

## Flags

- `--answers` Print every answer instead of the summary
- `--details` Print every metric of the run
- `--diff` Compare with the run before it, or compare two run ids
- `--fail-under` Fail with exit code 7 when a number is below a threshold, for example mention-rate=40% (repeatable)
- `--format` Summary format: text or markdown
- `--html` Write the report as one HTML file to this path
- `--mock` Show the runs made with llmention run --mock
- `--runs` How many recent runs --trend and --html include
- `--suite` Only look at runs of this suite (default: your prompts)
- `--trend` Show your mention rate across recent runs

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
- llmention history list
