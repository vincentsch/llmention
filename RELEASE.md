# Releasing llmention

This page describes how to cut a release. The first public release was v0.1.0.

## Check the candidate

Start from a clean checkout. You need:

- the Go version from `go.mod`
- Python 3 and Make
- a C compiler for the race detector

Then run:

```sh
make test race lint staticcheck docs-check
python3 scripts/docs-links.py
git diff --check
```

Look through `git ls-files` for saved runs and keys. Also look for paths from
your own machine. Then search for secrets and read each match before you
publish:

```sh
git grep -n -i -E 'sk-|_API_KEY=|BEGIN PRIVATE KEY'
```

Placeholders such as `OPENAI_API_KEY=...` in examples are fine.

## Name the version

Add a dated version section below `## Unreleased` in `CHANGELOG.md` and commit
the change. For example, the tag `v0.1.1` produces a binary that reports `0.1.1`.
Check the notes the release will use:

```sh
python3 scripts/release-notes.py 0.1.1
```

The script fails when the dated section is missing. Start a new
`## Unreleased` section for later work.

## Build and try the binaries

```sh
make build-all VERSION=0.1.1 COMMIT="$(git rev-parse HEAD)"
```

`dist/` then holds six binaries and `checksums.txt`:

- `llmention-linux-amd64` and `llmention-linux-arm64`
- `llmention-darwin-amd64` and `llmention-darwin-arm64`
- `llmention-windows-amd64.exe` and `llmention-windows-arm64.exe`

Run `--version` and `demo` with the binary for your own system.
`go test ./scripts` tests the installer against a fake release. It also tests
a wrong checksum, and that a failed download leaves an existing install
alone. To try the installer
by hand, serve a copy of `dist/` under `releases/download/v0.1.0/` with any
static file server and run:

```sh
INSTALL_DIR=/tmp/llmention-install LLMENTION_VERSION=v0.1.1 \
  LLMENTION_DOWNLOAD_BASE=http://127.0.0.1:8765 sh install.sh
```

## Publish

Do this only when publishing is approved. Make the repository public and turn
on private vulnerability reporting. Check that the link in `SECURITY.md`
works. Push `main` and wait until CI passes on Linux, macOS and Windows. Then
push the release tag.

The tag starts `.github/workflows/release.yml`. It runs CI again and builds
the six binaries. Then it adds a build attestation, GitHub's signed record of
where each binary was built. Last, it creates the GitHub release with the
binaries and the checksums. The changelog section becomes the release notes.
Check that the release has exactly those seven files.

## After publishing

On each system you have, open a new shell and run:

```sh
curl -fsSL https://raw.githubusercontent.com/vincentsch/llmention/main/install.sh | sh
llmention --version
llmention demo
gh attestation verify llmention-linux-amd64 --repo vincentsch/llmention
go install github.com/vincentsch/llmention/cmd/llmention@v0.1.1
```

Starting with v0.1.1, `go install` reports the module's release version.
Release binaries also include their build commit.
