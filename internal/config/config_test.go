package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

const validProjectYAML = `
version: 1
brand:
  name: Tabslip
  category: AI API cost tracking
  aliases: ["Tabslip CLI"]
  domains: ["https://Tabslip.com:443/", "www.tabslip.com."]
  products: ["Tabslip Receipts"]
competitors:
  - name: Helicone
    domains: ["helicone.ai"]
  - name: Langfuse
engines:
  - id: chatgpt
    provider: openai
    model: gpt-6.1-sol
    key_env: OPENAI_API_KEY
    max_output_tokens: 900
    capabilities:
      knowledge_cutoff: "2026-06"
  - id: claude
    provider: anthropic
    model: claude-sonnet-5-5
    key_env: ANTHROPIC_API_KEY
    base_url: https://gateway.example.com/anthropic
  - id: ollama
    provider: openai-compatible
    model: llama4
    base_url: http://localhost:11434/v1
  - id: mock
    provider: mock
    model: demo
prompts:
  - What are the best tools for AI API cost tracking?
  - id: alternatives
    prompt: What are good alternatives to Helicone?
    tags: [alternatives]
  - What are the best tools for AI API cost tracking?
modes: [no-search, search]
execution:
  max_requests: 60
  retries: 0
  timeout: 45s
analysis:
  judge:
    provider: anthropic
    model: claude-haiku-5-5
    key_env: ANTHROPIC_API_KEY
`

func TestLoadBytesNormalizesTheProject(t *testing.T) {
	cfg, err := LoadBytes([]byte(validProjectYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if got, want := cfg.Brand.Domains, []string{"tabslip.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("brand domains = %#v, want %#v", got, want)
	}
	if got, want := cfg.Modes, []string{ModeMemory, ModeSearchAuto}; !reflect.DeepEqual(got, want) {
		t.Fatalf("modes = %#v, want %#v", got, want)
	}
	var ids []string
	for _, p := range cfg.Prompts {
		ids = append(ids, p.ID)
	}
	// The repeated question is asked once.
	want := []string{"best-tools-ai-api-cost-tracking-4323", "alternatives"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("prompt ids = %#v, want %#v", ids, want)
	}
	if cfg.Execution.MaxRequests != 60 || cfg.Execution.Retries != 0 || cfg.Execution.Timeout != 45*time.Second {
		t.Fatalf("execution = %#v", cfg.Execution)
	}
	if cfg.Execution.Concurrency != DefaultConcurrency || cfg.RunsDir != DefaultRunsDir || cfg.Repetitions != 1 {
		t.Fatalf("defaults not applied: %#v", cfg)
	}
	if err := cfg.ValidateForRun(); err != nil {
		t.Fatalf("ValidateForRun() error = %v", err)
	}
}

func TestRequestsWaitFiveMinutesUnlessTheProjectSaysOtherwise(t *testing.T) {
	cfg, err := LoadBytes([]byte("version: 1\nbrand: {name: Acme}\nprompts: [\"Which invoicing app is best?\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Execution.Timeout != 5*time.Minute {
		t.Fatalf("default timeout = %s, want 5m", cfg.Execution.Timeout)
	}
	p, err := StarterProject(Starter{Brand: "Acme", Category: "invoicing", Providers: []string{"mock"}})
	if err != nil {
		t.Fatal(err)
	}
	if data := string(StarterYAML(p)); !strings.Contains(data, "  timeout: 5m  # how long to wait for one answer\n") {
		t.Fatalf("starter YAML does not write the 5m timeout:\n%s", data)
	}
	cfg, err = LoadBytes([]byte("version: 1\nbrand: {name: Acme}\nprompts: [\"Which invoicing app is best?\"]\nexecution: {timeout: 10m}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Execution.Timeout != 10*time.Minute {
		t.Fatalf("configured timeout = %s, want 10m", cfg.Execution.Timeout)
	}
}

func TestLoadReadsThePromptsFileNextToTheProject(t *testing.T) {
	dir := t.TempDir()
	project := "version: 1\nbrand: {name: Acme}\nprompts_file: prompts.txt\nprompts: [\"Inline question?\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "llmention.yaml"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts.txt"), []byte("# buyer questions\n\nWhich invoicing app is best?\n  Cheapest invoicing tool?  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(filepath.Join(dir, "llmention.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var texts []string
	for _, p := range cfg.Prompts {
		texts = append(texts, p.Text)
	}
	want := []string{"Inline question?", "Which invoicing app is best?", "Cheapest invoicing tool?"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("prompts = %#v, want %#v", texts, want)
	}
}

func TestLoadExplainsAMissingProjectFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "llmention.yaml"))
	if err == nil || !strings.Contains(err.Error(), "llmention init") {
		t.Fatalf("error = %v, want a hint to run init", err)
	}
}

func TestLoadBytesRejectsBadProjects(t *testing.T) {
	cases := map[string]string{
		"version":                     "version: 2\n",
		"unknown field":               "version: 1\nbrnad: {name: X}\n",
		"trailing document":           "version: 1\n---\nversion: 1\n",
		"brand without name":          "version: 1\nbrand: {category: x}\n",
		"duplicate competitor":        "version: 1\nbrand: {name: A}\ncompetitors: [{name: B}, {name: b}]\n",
		"competitor is brand":         "version: 1\nbrand: {name: Acme}\ncompetitors: [{name: acme}]\n",
		"unknown provider":            "version: 1\nengines: [{id: x, provider: bard, model: m}]\n",
		"missing key env":             "version: 1\nengines: [{id: x, provider: openai, model: m}]\n",
		"compatible without base url": "version: 1\nengines: [{id: x, provider: openai-compatible, model: m}]\n",
		"key over plain http":         "version: 1\nengines: [{id: x, provider: openai, model: m, key_env: K, base_url: \"http://gateway.example.com/v1\"}]\n",
		"bad engine id":               "version: 1\nengines: [{id: \"a b\", provider: mock, model: m}]\n",
		"duplicate engine id":         "version: 1\nengines: [{id: a, provider: mock, model: m}, {id: a, provider: mock, model: n}]\n",
		"prompts without brand":       "version: 1\nprompts: [Question?]\n",
		"duplicate prompt id":         "version: 1\nbrand: {name: A}\nprompts: [{id: q, prompt: x}, {id: q, prompt: y}]\n",
		"empty prompt":                "version: 1\nbrand: {name: A}\nprompts: [\"  \"]\n",
		"unknown mode":                "version: 1\nmodes: [offline]\n",
		"bad timeout":                 "version: 1\nexecution: {timeout: soon}\n",
		"zero max requests":           "version: 1\nexecution: {max_requests: 0}\n",
		"unknown capability":          "version: 1\nengines: [{id: a, provider: mock, model: m, capabilities: {flies: true}}]\n",
		"bad webhook env":             "version: 1\nnotify: {webhook_url_env: \"not valid\"}\n",
	}
	for name, input := range cases {
		if _, err := LoadBytes([]byte(input)); err == nil {
			t.Errorf("%s: LoadBytes accepted %q", name, input)
		}
	}
}

func TestCheckModelsRejectsPlaceholders(t *testing.T) {
	cfg, err := LoadBytes([]byte("version: 1\nengines: [{id: chatgpt, provider: openai, model: replace-with-openai-model, key_env: K}, {id: mock, provider: mock, model: demo}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.CheckModels(nil); err == nil || !strings.Contains(err.Error(), "chatgpt") {
		t.Fatalf("CheckModels() = %v, want placeholder error", err)
	}
	if err := cfg.CheckModels([]string{"mock"}); err != nil {
		t.Fatalf("CheckModels(mock) = %v", err)
	}
}

func TestStarterProjectRoundTripsThroughYAML(t *testing.T) {
	p, err := StarterProject(Starter{
		Brand:       "Tabslip",
		Category:    "AI API cost tracking tool",
		Domains:     []string{"tabslip.com"},
		Competitors: []CompetitorConfig{{Name: "Helicone", Domains: []string{"helicone.ai"}}, {Name: "Langfuse"}},
		Providers:   []string{"openai", "anthropic"},
	})
	if err != nil {
		t.Fatalf("StarterProject() error = %v", err)
	}
	data := StarterYAML(p)
	loaded, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("starter YAML does not load: %v\n%s", err, data)
	}
	if len(loaded.Prompts) != 10 || len(loaded.Engines) != 2 {
		t.Fatalf("prompts=%d engines=%d\n%s", len(loaded.Prompts), len(loaded.Engines), data)
	}
	for _, prompt := range loaded.Prompts {
		if strings.Contains(prompt.Text, "Tabslip") {
			t.Fatalf("starter prompt names the brand: %q", prompt.Text)
		}
		if strings.Contains(prompt.Text, "tool tool") || strings.Contains(prompt.Text, "tracking tool?") {
			t.Fatalf("starter prompt reads badly: %q", prompt.Text)
		}
	}
	// The default project fits the default request budget with every retry.
	firstAttempts := len(loaded.Prompts) * len(loaded.Engines) * len(loaded.Modes)
	if firstAttempts > loaded.Execution.MaxRequests {
		t.Fatalf("starter project needs %d requests, budget is %d", firstAttempts, loaded.Execution.MaxRequests)
	}
	if !strings.Contains(string(data), "can use any model your key has access to") {
		t.Fatalf("starter YAML should say models are easy to change:\n%s", data)
	}
}

func TestStarterYAMLQuotesAwkwardNames(t *testing.T) {
	p, err := StarterProject(Starter{Brand: "Yes: Inc #1", Category: "billing", Competitors: []CompetitorConfig{{Name: "[Bracket], Co"}}, Providers: []string{"mock"}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBytes(StarterYAML(p))
	if err != nil {
		t.Fatalf("awkward names broke the YAML: %v\n%s", err, StarterYAML(p))
	}
	if loaded.Brand.Name != "Yes: Inc #1" || loaded.Competitors[0].Name != "[Bracket], Co" {
		t.Fatalf("names changed: %q %q", loaded.Brand.Name, loaded.Competitors[0].Name)
	}
}

func TestKeyDiscoveryAndResolution(t *testing.T) {
	cfg, err := LoadBytes([]byte(validProjectYAML))
	if err != nil {
		t.Fatal(err)
	}
	envs, err := cfg.RequiredKeyEnvVars(KeySelection{EngineIDs: []string{"chatgpt", "mock"}, IncludeJudge: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"}; !reflect.DeepEqual(envs, want) {
		t.Fatalf("envs = %#v, want %#v", envs, want)
	}
	var registered []string
	keys, err := cfg.ResolveKeys(KeySelection{EngineIDs: []string{"chatgpt"}}, func(name string) (string, bool) {
		return "sk-test", name == "OPENAI_API_KEY"
	}, func(secret string) { registered = append(registered, secret) })
	if err != nil || keys["OPENAI_API_KEY"] != "sk-test" || !reflect.DeepEqual(registered, []string{"sk-test"}) {
		t.Fatalf("keys=%v registered=%v err=%v", keys, registered, err)
	}
	if _, err := cfg.ResolveKeys(KeySelection{EngineIDs: []string{"claude"}}, func(string) (string, bool) { return "", false }, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestProjectConfigPathUsesOnlyExplicitConfigFlag(t *testing.T) {
	root := &cobra.Command{Use: "llmention"}
	root.PersistentFlags().String("config", "", "Path to the config file")
	child := &cobra.Command{Use: "init"}
	root.AddCommand(child)
	if got, _ := ProjectConfigPath(child, nil); got != DefaultProjectConfigPath {
		t.Fatalf("default path = %q", got)
	}
	flag := root.PersistentFlags().Lookup("config")
	_ = flag.Value.Set("./custom.yaml")
	flag.Changed = true
	if got, _ := ProjectConfigPath(child, nil); got != "./custom.yaml" {
		t.Fatalf("explicit path = %q", got)
	}
}

func TestAtomicWriteRejectsSymlinksAndPreservesDestinations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require privileges on Windows")
	}
	dir := t.TempDir()
	realPath := filepath.Join(dir, "original.yaml")
	if err := os.WriteFile(realPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.yaml")
	if err := os.Symlink(realPath, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(link, []byte("replacement"), 0o600); err == nil {
		t.Fatal("atomic writer replaced a symlink destination")
	}
	if data, _ := os.ReadFile(realPath); string(data) != "original" {
		t.Fatalf("symlink target changed: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("failed write left temporary files: %v", entries)
	}
}

func TestPromptIDsKeepQuestionsWithTheSameWordsApart(t *testing.T) {
	cpp, csharp := PromptID("What is the best C++ IDE?"), PromptID("What is the best C# IDE?")
	if cpp == csharp {
		t.Fatalf("C++ and C# questions share the id %q", cpp)
	}
	// Removing one of them must not hand its id to the other, which happened
	// when collisions were resolved with a -2 suffix in file order.
	cfg, err := LoadBytes([]byte("version: 1\nbrand: {name: Acme}\nprompts: [\"What is the best C# IDE?\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prompts[0].ID != csharp {
		t.Fatalf("id = %q, want %q", cfg.Prompts[0].ID, csharp)
	}
	if PromptID("What is the best  c# IDE?") != csharp {
		t.Fatal("case and spacing changed the id")
	}
}

func TestSitesWithAPathAreRefused(t *testing.T) {
	for _, site := range []string{"https://github.com/acme/books", "github.com/acme/books"} {
		_, err := LoadBytes([]byte("version: 1\nbrand: {name: Acme, domains: [\"" + site + "\"]}\n"))
		if err == nil || !strings.Contains(err.Error(), "has a path") {
			t.Fatalf("%s: err = %v", site, err)
		}
	}
	cfg, err := LoadBytes([]byte("version: 1\nbrand: {name: Acme, domains: [\"https://www.acme.example/\"]}\n"))
	if err != nil || cfg.Brand.Domains[0] != "acme.example" {
		t.Fatalf("domains = %v, err = %v", cfg.Brand.Domains, err)
	}
}

func TestUnknownFieldsSuggestTheRightKey(t *testing.T) {
	_, err := LoadBytes([]byte("version: 1\nbrand:\n  name: Acme\n  domain: [acme.example]\n"))
	if err == nil || !strings.Contains(err.Error(), `line 4: brand has no field "domain"; did you mean domains?`) || strings.Contains(err.Error(), "config.") {
		t.Fatalf("err = %v", err)
	}
	_, err = LoadBytes([]byte("version: 1\nexecution: {max_request: 5}\n"))
	if err == nil || !strings.Contains(err.Error(), "did you mean max_requests?") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnquotedQuestionMarksInABracketListNameTheRightLine(t *testing.T) {
	for _, yaml := range []string{
		"version: 1\nbrand: {name: Acme}\n\nprompts: [What is best?, Second?]\n",
		"prompts: [What is best?]\n\n\nversion: 1\n",
	} {
		want := strings.Count(yaml[:strings.Index(yaml, "[")], "\n") + 1
		_, err := LoadBytes([]byte(yaml))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("line %d: did not find expected ',' or ']'. In a list in square brackets, put quotes", want)) {
			t.Fatalf("err = %v, want line %d", err, want)
		}
	}
}

func TestJoinAndReadsLikeASentence(t *testing.T) {
	for want, items := range map[string][]string{
		"":              nil,
		"a":             {"a"},
		"a and b":       {"a", "b"},
		"a, b, c and d": {"a", "b", "c", "d"},
	} {
		if got := JoinAnd(items); got != want {
			t.Fatalf("JoinAnd(%q) = %q, want %q", items, got, want)
		}
	}
}
