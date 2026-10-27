// Package scripts holds tests for the release scripts.
package scripts

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var releaseAssets = []string{
	"llmention-linux-amd64", "llmention-linux-arm64",
	"llmention-darwin-amd64", "llmention-darwin-arm64",
	"llmention-windows-amd64.exe", "llmention-windows-arm64.exe",
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// releaseTree writes fake release assets under both GitHub download layouts.
func releaseTree(t *testing.T) (root, manifest string) {
	t.Helper()
	root = t.TempDir()
	var m strings.Builder
	bodies := map[string]string{}
	for _, asset := range releaseAssets {
		body := "#!/bin/sh\nprintf 'fixture 0.1.0\\n'\n# " + asset + "\n"
		bodies[asset] = body
		fmt.Fprintf(&m, "%x  %s\n", sha256.Sum256([]byte(body)), asset)
	}
	for _, layout := range []string{"releases/latest/download", "releases/download/v0.1.0"} {
		for asset, body := range bodies {
			put(t, filepath.Join(root, layout, asset), body)
		}
		put(t, filepath.Join(root, layout, "checksums.txt"), m.String())
	}
	return root, m.String()
}

func TestInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	script, err := filepath.Abs("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, os, arch, version, asset, failure string
	}{
		{name: "latest", os: "Linux", arch: "x86_64", asset: releaseAssets[0]},
		{name: "pinned", os: "Linux", arch: "amd64", version: "v0.1.0", asset: releaseAssets[0]},
		{name: "linux arm", os: "Linux", arch: "aarch64", asset: releaseAssets[1]},
		{name: "mac arm", os: "Darwin", arch: "arm64", asset: releaseAssets[3]},
		{name: "git bash", os: "MINGW64_NT-10.0", arch: "x86_64", asset: releaseAssets[4]},
		{name: "wrong checksum", os: "Linux", arch: "x86_64", failure: "does not match its checksum"},
		{name: "missing entry", os: "Linux", arch: "x86_64", failure: "has no line for"},
		{name: "unsupported arch", os: "Linux", arch: "riscv64", failure: "no llmention build for this processor"},
		{name: "unsupported os", os: "FreeBSD", arch: "x86_64", failure: "no llmention build for this operating system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := releaseTree(t)
			checksums := filepath.Join(root, "releases/latest/download/checksums.txt")
			switch tc.name {
			case "wrong checksum":
				put(t, checksums, strings.Repeat("0", 64)+"  "+releaseAssets[0]+"\n")
			case "missing entry":
				put(t, checksums, "")
			}
			server := httptest.NewServer(http.FileServer(http.Dir(root)))
			defer server.Close()
			prefix, shim, tmp := t.TempDir(), t.TempDir(), t.TempDir()
			for _, name := range []string{"curl", "awk", "mktemp", "rm", "cp", "chmod", "mv", "mkdir", "sha256sum", "shasum"} {
				if path, err := exec.LookPath(name); err == nil {
					if err := os.Symlink(path, filepath.Join(shim, name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			put(t, filepath.Join(shim, "uname"), fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo '%s';; -m) echo '%s';; esac\n", tc.os, tc.arch))
			binary := "llmention"
			if strings.HasSuffix(tc.asset, ".exe") {
				binary += ".exe"
			}
			destination := filepath.Join(prefix, binary)
			put(t, destination, "existing install")
			cmd := exec.Command("/bin/sh", script)
			cmd.Env = append(os.Environ(), "PATH="+shim, "TMPDIR="+tmp, "INSTALL_DIR="+prefix,
				"LLMENTION_DOWNLOAD_BASE="+server.URL, "LLMENTION_VERSION="+tc.version)
			output, err := cmd.CombinedOutput()
			got, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.failure != "" {
				if err == nil || !strings.Contains(string(output), tc.failure) || string(got) != "existing install" {
					t.Fatalf("a failed install must leave the old binary: %v: %s", err, output)
				}
			} else {
				want, _ := os.ReadFile(filepath.Join(root, "releases/latest/download", tc.asset))
				if err != nil || string(got) != string(want) || !strings.Contains(string(output), "Next: llmention demo") {
					t.Fatalf("install failed: %v: %s", err, output)
				}
				// Following "Next" before fixing PATH fails, so the PATH hint
				// comes first, and the output says where the binary went.
				out := string(output)
				if !strings.Contains(out, destination) || strings.Index(out, "export PATH=") > strings.Index(out, "Next: llmention demo") {
					t.Fatalf("install output order:\n%s", out)
				}
			}
			if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
				t.Fatal("temporary download files were left behind")
			}
			if entries, err := os.ReadDir(prefix); err != nil || len(entries) != 1 {
				t.Fatal("staged install files were left behind")
			}
		})
	}
}
