# llmention suites list

List the suites you can run

## Usage

```
llmention suites list
```

## Examples

```
llmention suites list
llmention suites list --json
```

## Output

Shows text you can read in your terminal.

Use --json to get JSON for scripts and other programs.

Use --jq to filter the JSON output with a jq expression.

Use --template to format the output with a Go template.

## Request details

Add `--include-meta` to the JSON output to get the result as `{data, meta}`, where `meta` holds details about the request.

## Related commands

- llmention suites validate
- llmention run
