// Package version holds the build version, set at release time with -ldflags.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the release version without a leading v, or "dev".
var Version = "dev"

// Commit is the short git commit of the build.
var Commit = "unknown"

// A binary from "go install ...@v0.1.0" has no -ldflags, so it takes the
// version from the module instead of reporting dev.
func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

// String describes the build for --version.
func String() string { return Version + " (commit " + Commit + ", " + runtime.Version() + ")" }
