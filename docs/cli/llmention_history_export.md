# llmention history export

Copy a saved run to a file

## Usage

```
llmention history export <run-id>
```

## Examples

```
llmention history export 20261026-1608 --output run.json
llmention history export 20261026-1608 --output run.json --dry-run
```

## Flags

- `--force` Overwrite the output file
- `--output` File to write

## Output

Shows text you can read in your terminal.

Use --json to get JSON for scripts and other programs.

## Preview changes

This command makes changes. Use `--dry-run` to see what would change without changing anything.

## Related commands

- llmention history list
