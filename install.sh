#!/bin/sh
# Usage: curl -fsSL https://raw.githubusercontent.com/vincentsch/llmention/main/install.sh | sh
set -eu

fail() { printf '%s\n' "llmention: $*" >&2; exit 1; }
work=
staged=
cleanup() {
    [ -z "$staged" ] || rm -f "$staged"
    [ -z "$work" ] || rm -rf "$work"
    return 0
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

os=$(uname -s)
case "$os" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    MINGW*|MSYS*|CYGWIN*) os=windows ;;
    *) fail "there is no llmention build for this operating system: $os" ;;
esac
arch=$(uname -m)
case "$arch" in
    x86_64|amd64|AMD64) arch=amd64 ;;
    aarch64|arm64|ARM64) arch=arm64 ;;
    *) fail "there is no llmention build for this processor: $arch (builds exist for amd64 and arm64)" ;;
esac
asset=llmention-$os-$arch
binary=llmention
if [ "$os" = windows ]; then asset=$asset.exe; binary=$binary.exe; fi
base=${LLMENTION_DOWNLOAD_BASE:-https://github.com/vincentsch/llmention}
if [ -n "${LLMENTION_VERSION:-}" ]; then
    url=${base%/}/releases/download/$LLMENTION_VERSION
else
    url=${base%/}/releases/latest/download
fi
command -v curl >/dev/null 2>&1 || fail 'the installer needs curl to download llmention'
work=$(mktemp -d) || fail 'could not create a temporary folder for the download'
curl -fsSL "$url/$asset" -o "$work/$asset" || fail "could not download $url/$asset"
curl -fsSL "$url/checksums.txt" -o "$work/checksums.txt" || fail "could not download $url/checksums.txt"
expected=$(awk -v name="$asset" '$2 == name {print $1}' "$work/checksums.txt")
[ -n "$expected" ] || fail "checksums.txt has no line for $asset"
[ "${#expected}" -eq 64 ] || fail "the line for $asset in checksums.txt is not a SHA-256 checksum"
case "$expected" in *[!0-9a-fA-F]*) fail "the line for $asset in checksums.txt is not a SHA-256 checksum" ;; esac
if command -v sha256sum >/dev/null 2>&1; then
    digest=$(sha256sum "$work/$asset") || fail 'could not work out the SHA-256 checksum of the download'
elif command -v shasum >/dev/null 2>&1; then
    digest=$(shasum -a 256 "$work/$asset") || fail 'could not work out the SHA-256 checksum of the download'
else
    fail 'the installer needs sha256sum or shasum to check the download'
fi
actual=${digest%% *}
[ "$actual" = "$expected" ] || fail "the download of $asset does not match its checksum, so nothing was installed"

install_dir=${INSTALL_DIR:-$HOME/.local/bin}
mkdir -p "$install_dir" || fail "could not create $install_dir; set INSTALL_DIR to a folder you can write to"
# Copy next to the destination first. The last step is then a rename on the
# same disk, so a failed install never leaves a half-written program.
staged=$(mktemp "$install_dir/.llmention.XXXXXX") || fail "could not write to $install_dir; set INSTALL_DIR to a folder you can write to"
cp "$work/$asset" "$staged" || fail "could not copy llmention into $install_dir"
chmod 755 "$staged" || fail 'could not make llmention executable'
mv -f "$staged" "$install_dir/$binary" || fail 'could not replace the installed llmention; if it is running, stop it and try again'
staged=
printf 'Installed %s to %s.\n' "$binary" "$install_dir/$binary"
case ":${PATH:-}:" in
    *":$install_dir:"*) ;;
    *) printf 'That folder is not on your PATH yet. Run this line to use llmention in this shell, and add it to your shell profile for new shells:\n  export PATH="%s:$PATH"\n' "$install_dir" ;;
esac
printf 'Next: llmention demo\n'
