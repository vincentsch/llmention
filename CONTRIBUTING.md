# Contributing to llmention

llmention is a Go program and needs nothing else to run. Use the Go version
in [go.mod](go.mod); `GOTOOLCHAIN=auto` fetches it. Release builds use
`CGO_ENABLED=0`.

## Build and check a change

On Linux or macOS with Make:

```sh
make build
./bin/llmention --version
make test race lint staticcheck docs-check
python3 scripts/docs-links.py
```

The race tests (`make race`) need a C compiler that Go supports. On Windows, start with
`go build ./...`, `go test ./...` and `go vet ./...` in PowerShell. CI runs
the build and tests on Linux, macOS and Windows. The other checks run on
Linux only.

## Update the help and the docs together

Run `make docs` after changing a command or a flag. It rewrites the generated
pages under `docs/cli/` and the help files under `cmd/llmention/testdata/help/`.
`make docs-check` fails when they are out of date. The guides under `docs/`
are written by hand; update them when behavior changes.

Write plain English with short sentences and concrete commands. Say what a
reader can do and what a result means. Do not claim more than the code and
the tests show. A number llmention could not measure stays unknown and is
never shown as zero.

## Test without provider keys

Tests use local fake servers and made-up keys. Never add tests that call a
paid provider. The mock provider (`provider: mock`) writes believable answers
for the demo and for end-to-end tests. Test what a user sees, like the
printed output and the exit code.

Never commit keys or saved runs, and don't commit build output. Check the diff
before you send it.

Report security issues through [SECURITY.md](SECURITY.md).
