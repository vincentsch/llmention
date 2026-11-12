//go:build conformance

package main

import (
	"github.com/vincentsch/llmention/internal/version"
	rgupdate "github.com/vincentsch/rungrad/update"
)

// Conformance checks run offline, including when built from a release tag.
func init() {
	newReleaseFetcher = func() rgupdate.Fetcher { return conformanceReleaseFetcher{} }
}

type conformanceReleaseFetcher struct{}

func (conformanceReleaseFetcher) Latest() (rgupdate.Release, error) {
	return rgupdate.Release{Version: version.Version}, nil
}
