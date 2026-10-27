package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileAtomic writes data to a temporary file and renames it into place,
// so readers never see a half-written file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := ValidateWritePath(path); err != nil {
		return err
	}
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := ValidateWritePath(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".llmention-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ValidateWritePath refuses a destination that is a symlink, a folder or a
// special file. Missing folders are fine, because WriteFileAtomic creates
// them.
func ValidateWritePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Symlinks in the parent folders are normal (macOS keeps temporary
	// folders under a link, and many home folders are links). Only the file
	// itself must not be a link, so a planted link cannot redirect the write.
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if parent, err := os.Stat(filepath.Dir(abs)); err == nil && !parent.IsDir() {
			return fmt.Errorf("%s is not a folder", filepath.Dir(path))
		}
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; write to the real file instead", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is a folder or a special file, so llmention cannot write to it", path)
	}
	return nil
}
