package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateWritePathAllowsLinkedFoldersButNotLinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	real := t.TempDir()
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	// macOS keeps temporary folders behind a link, so a linked parent is normal.
	if err := ValidateWritePath(filepath.Join(linked, "report.html")); err != nil {
		t.Fatalf("linked parent folder: %v", err)
	}
	if err := ValidateWritePath(filepath.Join(linked, "new", "report.html")); err != nil {
		t.Fatalf("missing folder: %v", err)
	}
	target := filepath.Join(real, "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(real, "report.html")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWritePath(link); err == nil {
		t.Fatal("a symlinked file must be refused")
	}
	if err := ValidateWritePath(real); err == nil {
		t.Fatal("a folder must be refused as a file")
	}
	if err := ValidateWritePath(filepath.Join(target, "x")); err == nil {
		t.Fatal("a file used as a folder must be refused")
	}
}
