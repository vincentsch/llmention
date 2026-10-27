package suite

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/suite/builtin"
)

type Catalog struct {
	entries []LoadedSuite
	byID    map[string]LoadedSuite
}

func LoadBytes(data []byte) (*Suite, error) {
	var s Suite
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("a suite file must hold exactly one YAML document")
	} else if err != io.EOF {
		return nil, err
	}
	if err := markItemSelectionPresence(data, &s); err != nil {
		return nil, err
	}
	presenceIssues, err := auditSourcePolicyPresenceIssues(data, &s)
	if err != nil {
		return nil, err
	}
	issues := ValidateSuite(&s)
	issues = append(issues, presenceIssues...)
	if len(issues) > 0 {
		sort.SliceStable(issues, func(i, j int) bool {
			if issues[i].Path == issues[j].Path {
				return issues[i].Message < issues[j].Message
			}
			return issues[i].Path < issues[j].Path
		})
		return nil, ValidationError{Issues: issues}
	}
	return &s, nil
}

func LoadFile(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("suite file %s: %w", path, err)
	}
	s, err := LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("suite file %s: %w", path, err)
	}
	return s, nil
}

func LoadBuiltIn(id string) (LoadedSuite, error) {
	data, err := builtin.Read(id)
	if err != nil {
		return LoadedSuite{}, err
	}
	s, err := LoadBytes(data)
	if err != nil {
		return LoadedSuite{}, fmt.Errorf("load built-in suite %s: %w", id, err)
	}
	if s.ID != id {
		return LoadedSuite{}, fmt.Errorf("built-in suite %s declares id %q", id, s.ID)
	}
	return LoadedSuite{Suite: s, Source: SourceBuiltIn}, nil
}

func BuiltInExists(id string) bool {
	return builtin.Exists(strings.TrimSpace(id))
}

func LoadBuiltIns() ([]LoadedSuite, error) {
	ids := builtin.IDs()
	out := make([]LoadedSuite, 0, len(ids))
	for _, id := range ids {
		loaded, err := LoadBuiltIn(id)
		if err != nil {
			return nil, err
		}
		out = append(out, loaded)
	}
	return out, nil
}

func LoadUserPaths(paths []string) ([]LoadedSuite, error) {
	files, err := expandPaths(paths)
	if err != nil {
		return nil, err
	}
	out := make([]LoadedSuite, 0, len(files))
	for _, path := range files {
		s, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, LoadedSuite{Suite: s, Source: SourceUser, Path: path})
	}
	return out, nil
}

// LoadCatalog collects the built-in suites and the suite files under
// suites.paths, and adds the suite made from the prompts in llmention.yaml.
// project may be nil.
func LoadCatalog(project *config.Project) (Catalog, error) {
	entries, err := LoadBuiltIns()
	if err != nil {
		return Catalog{}, err
	}
	if project == nil {
		return NewCatalog(entries)
	}
	users, err := LoadUserPaths(project.Suites.Paths)
	if err != nil {
		return Catalog{}, err
	}
	entries = append(entries, users...)
	if prompts := PromptsSuite(project); prompts != nil {
		if err := prompts.Validate(); err != nil {
			return Catalog{}, fmt.Errorf("prompts in llmention.yaml: %w", err)
		}
		entries = append(entries, LoadedSuite{Suite: prompts, Source: SourceProject})
	}
	return NewCatalog(entries)
}

func NewCatalog(entries []LoadedSuite) (Catalog, error) {
	entries = append([]LoadedSuite(nil), entries...)
	byID := make(map[string]LoadedSuite, len(entries))
	for _, entry := range entries {
		if entry.Suite == nil {
			return Catalog{}, fmt.Errorf("catalog contains nil suite")
		}
		if existing, ok := byID[entry.Suite.ID]; ok {
			return Catalog{}, fmt.Errorf("the suite id %q is used twice, by %s and %s", entry.Suite.ID, sourceLabel(existing), sourceLabel(entry))
		}
		byID[entry.Suite.ID] = entry
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Suite.ID < entries[j].Suite.ID
	})
	return Catalog{entries: entries, byID: byID}, nil
}

func (c Catalog) Entries() []LoadedSuite {
	out := make([]LoadedSuite, len(c.entries))
	copy(out, c.entries)
	return out
}

func (c Catalog) Find(id string) (LoadedSuite, bool) {
	entry, ok := c.byID[strings.TrimSpace(id)]
	return entry, ok
}

// ResolveTarget finds a suite by its id or by the path of a suite file.
func ResolveTarget(target string, project *config.Project) (LoadedSuite, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return LoadedSuite{}, fmt.Errorf("give a suite id or the path of a suite file")
	}
	if builtin.Exists(target) {
		return LoadBuiltIn(target)
	}
	if looksLikePath(target) {
		s, err := LoadFile(target)
		if err != nil {
			return LoadedSuite{}, err
		}
		return LoadedSuite{Suite: s, Source: SourceUser, Path: target}, nil
	}
	catalog, err := LoadCatalog(project)
	if err != nil {
		return LoadedSuite{}, err
	}
	if entry, ok := catalog.Find(target); ok {
		return entry, nil
	}
	return LoadedSuite{}, fmt.Errorf("there is no suite %q; `llmention suites list` shows the suites you can use", target)
}

func expandPaths(paths []string) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if hasGlobMeta(path) {
			matches, err := filepath.Glob(path)
			if err != nil {
				return nil, fmt.Errorf("the suite path pattern %q is not valid: %w", path, err)
			}
			sort.Strings(matches)
			for _, match := range matches {
				if err := addPath(match, seen, &files); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := addPath(path, seen, &files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func addPath(path string, seen map[string]struct{}, files *[]string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("suite path %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("suite path %s is a folder; list the YAML files in it, or use a pattern such as %s", path, filepath.Join(path, "*.yaml"))
	}
	clean := filepath.Clean(path)
	if _, ok := seen[clean]; ok {
		return nil
	}
	seen[clean] = struct{}{}
	*files = append(*files, path)
	return nil
}

func looksLikePath(value string) bool {
	if filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(value))
	return ext == ".yaml" || ext == ".yml"
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, `*?[`)
}

func sourceLabel(entry LoadedSuite) string {
	switch {
	case entry.Path != "":
		return entry.Path
	case entry.Source == SourceBuiltIn:
		return "a built-in suite"
	case entry.Source == SourceProject:
		return "the prompts in llmention.yaml"
	}
	return string(entry.Source)
}
