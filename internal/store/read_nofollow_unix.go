//go:build !windows

package store

import (
	"os"
	"syscall"
)

// openNoFollow opens a file but refuses a symlink, even one that appears
// after readRegular checked the path.
func openNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
