// Package version holds the build version, set at release time with -ldflags.
package version

import "runtime"

// Version is the release version without a leading v, or "dev".
var Version = "dev"

// Commit is the short git commit of the build.
var Commit = "unknown"

// String describes the build for --version.
func String() string { return Version + " (commit " + Commit + ", " + runtime.Version() + ")" }
