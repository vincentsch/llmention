// Package store saves runs in a folder, one JSON file per run. A small index
// file makes it fast to list the history and to find the latest run.
package store

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/scorecard"
)

const indexName = "index.json"

// Store is the history folder, runs_dir in llmention.yaml.
type Store struct {
	Root string
}

// New returns the store at root.
func New(root string) Store { return Store{Root: root} }

// Entry is what the index keeps about one run.
type Entry struct {
	ID        string               `json:"id"`
	CreatedAt time.Time            `json:"created_at"`
	Status    string               `json:"status"`
	Suites    []string             `json:"suites"`
	Engines   []string             `json:"engines"`
	Models    []string             `json:"models"`
	Answers   int                  `json:"answers"`
	Planned   int                  `json:"planned"`
	Demo      bool                 `json:"demo,omitempty"`
	Narrowed  bool                 `json:"narrowed,omitempty"`
	Headline  []scorecard.Headline `json:"headline,omitempty"`
	Gates     string               `json:"gates,omitempty"` // pass, fail or empty
}

// Problem is a file in the history directory that could not be read.
type Problem struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// NotFoundError means no run matched.
type NotFoundError struct{ Message string }

func (e *NotFoundError) Error() string { return e.Message }

type index struct {
	Runs []Entry `json:"runs"`
}

// Save writes a scorecard and adds it to the index.
func (s Store) Save(card *scorecard.Scorecard) (string, error) {
	if err := card.Validate(); err != nil {
		return "", err
	}
	if err := s.ensureRoot(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, card.ID+".json")
	if err := config.WriteFileAtomic(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("save run: %w", err)
	}
	entries, _, err := s.entries()
	if err != nil {
		return "", err
	}
	entries = slices.DeleteFunc(entries, func(e Entry) bool { return e.ID == card.ID })
	entries = append(entries, EntryFor(card))
	return path, s.writeIndex(entries)
}

// List returns all runs, newest first, and the files it could not read. It
// trusts the index and only opens files the index does not know.
func (s Store) List() ([]Entry, []Problem, error) {
	entries, problems, err := s.entries()
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(b.ID, a.ID) })
	return entries, problems, err
}

// Last returns the newest run, optionally the newest that ran a suite.
func (s Store) Last(suiteID string) (*scorecard.Scorecard, error) {
	return s.Nth(suiteID, 0)
}

// Nth returns the n-th newest run (0 is the newest) that ran suiteID, or any
// run when suiteID is empty.
func (s Store) Nth(suiteID string, n int) (*scorecard.Scorecard, error) {
	entries, _, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if suiteID != "" && !slices.Contains(e.Suites, suiteID) {
			continue
		}
		if n == 0 {
			return s.Load(e.ID)
		}
		n--
	}
	if suiteID != "" {
		return nil, &NotFoundError{Message: fmt.Sprintf("no saved run in %s asked the suite %q", s.Root, suiteID)}
	}
	return nil, &NotFoundError{Message: fmt.Sprintf("no saved runs in %s yet; start one with `llmention run`", s.Root)}
}

// Load reads a run by id. The start of an id is enough when only one run
// begins with it, so "20261026-1608" finds "20261026-160841-3f9a1c".
func (s Store) Load(id string) (*scorecard.Scorecard, error) {
	id = strings.TrimSuffix(strings.TrimSpace(id), ".json")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return nil, fmt.Errorf("%q is not a run id", id)
	}
	full, err := s.resolve(id)
	if err != nil {
		return nil, err
	}
	data, err := readRegular(filepath.Join(s.Root, full+".json"))
	if err != nil {
		return nil, fmt.Errorf("could not read run %s: %w", full, err)
	}
	return Decode(data)
}

// Decode parses and checks scorecard JSON.
func Decode(data []byte) (*scorecard.Scorecard, error) {
	var card scorecard.Scorecard
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&card); err != nil {
		return nil, fmt.Errorf("the file is not an llmention run: %w", err)
	}
	if err := card.Validate(); err != nil {
		return nil, err
	}
	return &card, nil
}

// Path returns the file that holds a run.
func (s Store) Path(id string) (string, error) {
	full, err := s.resolve(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, full+".json"), nil
}

func (s Store) resolve(id string) (string, error) {
	names, err := s.files()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, name := range names {
		if name == id {
			return name, nil
		}
		if strings.HasPrefix(name, id) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", &NotFoundError{Message: fmt.Sprintf("no saved run %q in %s; see `llmention history list`", id, s.Root)}
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%q matches %d runs; use more of the id", id, len(matches))
}

// files lists the run ids in the folder. It leaves out the index and
// temporary files.
func (s Store) files() ([]string, error) {
	dirEntries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range dirEntries {
		name := d.Name()
		if d.IsDir() || name == indexName || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	return ids, nil
}

// entries brings the index in line with the files in the folder. Files added
// by hand, for example from a CI cache, are read and added to the index.
// Entries whose file is gone are removed.
func (s Store) entries() ([]Entry, []Problem, error) {
	ids, err := s.files()
	if err != nil {
		return nil, nil, err
	}
	var idx index
	if data, err := os.ReadFile(filepath.Join(s.Root, indexName)); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	known := map[string]Entry{}
	for _, e := range idx.Runs {
		known[e.ID] = e
	}
	var out []Entry
	var problems []Problem
	changed := len(idx.Runs) != len(ids)
	for _, id := range ids {
		if e, ok := known[id]; ok {
			out = append(out, e)
			continue
		}
		changed = true
		data, err := readRegular(filepath.Join(s.Root, id+".json"))
		if err != nil {
			problems = append(problems, Problem{File: id + ".json", Message: err.Error()})
			continue
		}
		card, err := Decode(data)
		if err != nil {
			problems = append(problems, Problem{File: id + ".json", Message: err.Error()})
			continue
		}
		if card.ID != id {
			problems = append(problems, Problem{File: id + ".json", Message: fmt.Sprintf("the file holds run %s, so its name must be %s.json", card.ID, card.ID)})
			continue
		}
		out = append(out, EntryFor(card))
	}
	if changed && len(out) > 0 {
		// The index only makes listing faster, so a history folder that
		// cannot be written to still works.
		_ = s.writeIndex(out)
	}
	return out, problems, nil
}

func (s Store) writeIndex(entries []Entry) error {
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.ID, b.ID) })
	data, err := json.MarshalIndent(index{Runs: entries}, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(filepath.Join(s.Root, indexName), append(data, '\n'), 0o600)
}

func (s Store) ensureRoot() error {
	info, err := os.Lstat(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(s.Root, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("the history folder %s is a symlink; point runs_dir at the real folder", s.Root)
	}
	if !info.IsDir() {
		return fmt.Errorf("runs_dir %s is a file, but it must be a folder", s.Root)
	}
	return nil
}

// EntryFor sums up a run for the index.
func EntryFor(card *scorecard.Scorecard) Entry {
	e := Entry{
		ID:        card.ID,
		CreatedAt: card.CreatedAt,
		Status:    card.Status,
		Answers:   card.Counts.Answered,
		Planned:   card.Counts.Planned,
		Demo:      card.Demo(),
		Narrowed:  card.Narrowed,
		Headline:  card.Headline,
	}
	for _, s := range card.Suites {
		e.Suites = append(e.Suites, s.ID)
	}
	for _, eng := range card.Engines {
		e.Engines = append(e.Engines, eng.ID)
		e.Models = append(e.Models, eng.Model)
	}
	for _, g := range card.Gates {
		switch {
		case g.Status != "pass": // fail, inconclusive or error
			e.Gates = "fail"
		case e.Gates == "":
			e.Gates = "pass"
		}
	}
	return e
}

// readRegular reads a file and refuses symlinks and other special files, so
// a link someone planted cannot make llmention read another file.
func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is a link or a special file, so llmention does not read it", filepath.Base(path))
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
