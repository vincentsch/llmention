package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseNotes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("the release job runs this script on Linux")
	}
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## Unreleased\n\n- Next thing.\n\n## 0.2.0 - 2026-11-02\n\n- Weekly trend.\n\n## 0.1.0 - 2026-10-30\n\n- First release.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(python, "release-notes.py", "0.2.0", "--changelog", changelog).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "- Weekly trend." {
		t.Fatalf("notes for 0.2.0: %v: %q", err, out)
	}
	if out, err := exec.Command(python, "release-notes.py", "0.3.0", "--changelog", changelog).CombinedOutput(); err == nil {
		t.Fatalf("a version without a dated section must fail: %q", out)
	}
}
