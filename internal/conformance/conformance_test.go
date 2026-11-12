// Package conformance_test checks the built binary against the rules rungrad
// sets for its command-line tools.
package conformance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vincentsch/rungrad/conformance"
)

func TestCLIRequiredRunGradConformance(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "llmention")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// Exercise release update checks even when this checkout is a development build.
	build := exec.Command("go", "build", "-tags", "conformance", "-ldflags",
		"-X github.com/vincentsch/llmention/internal/version.Version=1.2.3", "-o", bin, "./cmd/llmention")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build llmention: %v\n%s", err, out)
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "llmention.yaml")
	runsDir := filepath.Join(tmp, "runs")
	if err := os.WriteFile(configPath, []byte(conformanceProjectYAML(runsDir)), 0o644); err != nil {
		t.Fatalf("write conformance config: %v", err)
	}

	runner, err := conformance.NewRunner(conformance.Target{
		Path:         bin,
		Read:         []string{"suites", "list", "--config", configPath},
		Mutate:       []string{"init", "--config", filepath.Join(tmp, "generated.yaml"), "--no-prompt", "--brand", "Acme Tools", "--engines", "mock"},
		NotFound:     []string{"report", "20260818-120000-aaaaaa", "--config", configPath},
		HasUpdate:    true,
		ManifestMode: conformance.ManifestModeRequired,
	})
	if err != nil {
		t.Fatalf("new conformance runner: %v", err)
	}
	defer runner.Close()
	if err := runner.DiscoverManifest(); err != nil {
		t.Fatalf("discover manifest: %v", err)
	}
	ruleset, err := conformance.DefaultRuleset()
	if err != nil {
		t.Fatalf("default ruleset: %v", err)
	}
	score := runner.Score(ruleset)
	if score.Manifest.Status != conformance.ManifestPresent {
		t.Fatalf("manifest status = %s (%s)", score.Manifest.Status, score.Manifest.Reason)
	}
	if failures := score.RequiredFailures(); len(failures) > 0 {
		t.Fatalf("required conformance failures: %s\n%s", strings.Join(failures, ", "), score.Report())
	}
}

// quoteYAML writes value as a YAML double-quoted string. Backslashes are
// escaped too, because Windows paths like C:\Users would otherwise read as
// YAML escape sequences.
func quoteYAML(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func conformanceProjectYAML(runsDir string) string {
	return `version: 1
brand: {name: Acme Tools, category: developer productivity, domains: [acme.example]}
competitors: [{name: Rival, domains: [rival.example]}]
engines: [{id: mock, provider: mock, model: demo}]
prompts: ["What are the best developer productivity tools?"]
runs_dir: ` + quoteYAML(runsDir) + `
`
}
