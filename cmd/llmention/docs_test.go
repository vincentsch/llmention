package main

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/vincentsch/rungrad/testutil"
)

var updateGoldenFiles = flag.Bool("update", false, "rewrite generated CLI docs and help goldens")

func TestGeneratedDocsInSync(t *testing.T) {
	testutil.AssertDocsInSync(t, newApp, *updateGoldenFiles, filepath.Join("..", "..", "docs", "cli"))
}

func TestHelpGoldensInSync(t *testing.T) {
	testutil.AssertHelpGoldens(t, newApp, *updateGoldenFiles, filepath.Join("testdata", "help"))
}

func TestHelpDocsManifestCatalogConsistent(t *testing.T) {
	testutil.AssertConsistent(t, newApp)
}
