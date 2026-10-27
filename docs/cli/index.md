# llmention command reference

See how often AI assistants mention your product

## Commands

- [llmention demo](llmention_demo.md) - Try llmention without API keys
- [llmention discover](llmention_discover.md) - Suggest buyer questions to use as prompts
- [llmention history](llmention_history.md) - List and export saved runs
- [llmention history export](llmention_history_export.md) - Copy a saved run to a file
- [llmention history list](llmention_history_list.md) - List saved runs, newest first
- [llmention init](llmention_init.md) - Write llmention.yaml for your product
- [llmention quick](llmention_quick.md) - Write llmention.yaml and run it in one step
- [llmention report](llmention_report.md) - Show what the assistants said and how it changed
- [llmention run](llmention_run.md) - Ask the assistants your prompts and save the answers
- [llmention suites](llmention_suites.md) - List and check suites
- [llmention suites list](llmention_suites_list.md) - List the suites you can run
- [llmention suites validate](llmention_suites_validate.md) - Check a suite and show its prompts
- [llmention update](llmention_update.md) - Check for a newer llmention release

## Global flags

- `--config` Path to the config file
- `--dry-run` Preview changes without performing them
- `--include-meta` Wrap machine output as {data, meta} (commands that expose request metadata)
- `--jq` Transform stable JSON output with a jq expression (commands with machine output)
- `--json` Output stable JSON instead of the human view
- `--no-ansi` Disable all ANSI/control sequences in human output
- `--no-color` Disable color in human output
- `--no-pager` Never use a pager for long human output
- `--no-prompt` Never block on an interactive prompt
- `--plain` Print unstyled, copy-safe text (commands with human output)
- `--quiet` Suppress non-essential output
- `--template` Render stable JSON output through a Go text/template (commands with machine output)
