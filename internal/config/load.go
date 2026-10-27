package config

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type projectFile struct {
	Version     int                `yaml:"version"`
	Brand       *BrandConfig       `yaml:"brand"`
	Competitors []CompetitorConfig `yaml:"competitors"`
	Engines     []EngineConfig     `yaml:"engines"`
	Prompts     []Prompt           `yaml:"prompts"`
	PromptsFile string             `yaml:"prompts_file"`
	Modes       []string           `yaml:"modes"`
	Repetitions *int               `yaml:"repetitions"`
	Suites      SuiteConfig        `yaml:"suites"`
	Execution   executionFile      `yaml:"execution"`
	Analysis    AnalysisConfig     `yaml:"analysis"`
	RunsDir     *string            `yaml:"runs_dir"`
	Notify      NotifyConfig       `yaml:"notify"`
}

type executionFile struct {
	MaxRequests      *int   `yaml:"max_requests"`
	Retries          *int   `yaml:"retries"`
	MaxJudgeRequests *int   `yaml:"max_judge_requests"`
	Timeout          string `yaml:"timeout"`
	Concurrency      *int   `yaml:"concurrency"`
}

// UnmarshalYAML accepts a plain string or a mapping with id, prompt and tags.
func (p *Prompt) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		p.Text = strings.TrimSpace(node.Value)
		return nil
	}
	type plain Prompt
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*p = Prompt(v)
	return nil
}

// MarshalYAML writes prompts without an explicit id as plain strings.
func (p Prompt) MarshalYAML() (any, error) {
	if p.ID == "" && len(p.Tags) == 0 {
		return p.Text, nil
	}
	type plain Prompt
	return plain(p), nil
}

// Load reads and checks a project file. A prompts_file is read relative to
// the project file's directory.
func Load(path string) (*Project, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultProjectConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("there is no project file at %s; run `llmention init` to write one, or pass its path with --config", path)
		}
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Paths in the project file are relative to the file, so --config works
	// from any folder, for example in a CI job.
	dir := filepath.Dir(path)
	cfg.RunsDir = relativeTo(dir, cfg.RunsDir)
	for i, p := range cfg.Suites.Paths {
		cfg.Suites.Paths[i] = relativeTo(dir, p)
	}
	if cfg.PromptsFile != "" {
		fromFile, err := ReadPromptsFile(relativeTo(dir, cfg.PromptsFile))
		if err != nil {
			return nil, fmt.Errorf("%s: prompts_file: %w", path, err)
		}
		cfg.Prompts = append(cfg.Prompts, fromFile...)
	}
	if err := cfg.ValidateShape(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func relativeTo(dir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

// LoadBytes parses and checks project YAML. It ignores prompts_file.
func LoadBytes(data []byte) (*Project, error) {
	cfg, err := parse(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateShape(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parse(data []byte) (*Project, error) {
	var raw projectFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("the project file is empty")
		}
		return nil, friendlyYAMLError(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("the project file holds more than one YAML document; remove the extra --- line and what follows it")
		}
		return nil, err
	}
	cfg := &Project{
		Version:     raw.Version,
		Brand:       raw.Brand,
		Competitors: raw.Competitors,
		Engines:     raw.Engines,
		Prompts:     raw.Prompts,
		PromptsFile: strings.TrimSpace(raw.PromptsFile),
		Modes:       raw.Modes,
		Repetitions: DefaultRepetitions,
		Suites:      raw.Suites,
		Analysis:    raw.Analysis,
		RunsDir:     DefaultRunsDir,
		Notify:      raw.Notify,
		Execution: ExecutionConfig{
			MaxRequests:      DefaultMaxRequests,
			Retries:          DefaultRetries,
			MaxJudgeRequests: DefaultMaxJudgeRequests,
			Timeout:          DefaultTimeout,
			Concurrency:      DefaultConcurrency,
		},
	}
	if raw.Repetitions != nil {
		cfg.Repetitions = *raw.Repetitions
	}
	if raw.RunsDir != nil {
		cfg.RunsDir = strings.TrimSpace(*raw.RunsDir)
	}
	e := raw.Execution
	if e.MaxRequests != nil {
		cfg.Execution.MaxRequests = *e.MaxRequests
	}
	if e.Retries != nil {
		cfg.Execution.Retries = *e.Retries
	}
	if e.MaxJudgeRequests != nil {
		cfg.Execution.MaxJudgeRequests = *e.MaxJudgeRequests
	}
	if e.Concurrency != nil {
		cfg.Execution.Concurrency = *e.Concurrency
	}
	if strings.TrimSpace(e.Timeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(e.Timeout))
		if err != nil {
			return nil, fmt.Errorf("execution.timeout %q is not a duration such as 90s or 5m", e.Timeout)
		}
		cfg.Execution.Timeout = d
	}
	return cfg, nil
}

// ReadPromptsFile reads one prompt per line. Blank lines and lines starting
// with # are left out.
func ReadPromptsFile(path string) ([]Prompt, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Prompt
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, Prompt{Text: line})
	}
	return out, scanner.Err()
}
