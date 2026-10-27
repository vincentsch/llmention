//go:build windows

package store

import "os"

// Windows has no O_NOFOLLOW. readRegular has already refused links with
// Lstat before it gets here.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
