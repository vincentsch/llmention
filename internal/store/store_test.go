package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
)

func card(id, suiteID string) *scorecard.Scorecard {
	return &scorecard.Scorecard{
		ID:        id,
		CreatedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		Status:    scorecard.StatusComplete,
		Suites:    []scorecard.Suite{{ID: suiteID, Definition: &suite.Suite{ID: suiteID}}},
		Engines:   []scorecard.Engine{{ID: "chatgpt", Provider: "openai", Model: "gpt-test"}},
	}
}

func TestSaveListAndLast(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "runs"))
	if _, err := s.Last(""); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("empty history: err = %v", err)
	}
	for _, c := range []*scorecard.Scorecard{
		card("20261019-090000-aaaaaa", "prompts"),
		card("20261026-090000-bbbbbb", "prompts"),
		card("20261026-100000-cccccc", "recognition.basic"),
	} {
		if _, err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	entries, problems, err := s.List()
	if err != nil || len(problems) != 0 || len(entries) != 3 || entries[0].ID != "20261026-100000-cccccc" {
		t.Fatalf("List = %+v %v %v", entries, problems, err)
	}
	last, err := s.Last("prompts")
	if err != nil || last.ID != "20261026-090000-bbbbbb" {
		t.Fatalf("Last(prompts) = %v, %v", last, err)
	}
	previous, err := s.Nth("prompts", 1)
	if err != nil || previous.ID != "20261019-090000-aaaaaa" {
		t.Fatalf("Nth(prompts, 1) = %v, %v", previous, err)
	}
	if got, err := s.Load("20261019"); err != nil || got.ID != "20261019-090000-aaaaaa" {
		t.Fatalf("Load by prefix = %v, %v", got, err)
	}
	if _, err := s.Load("20261026"); err == nil {
		t.Fatal("an ambiguous prefix must fail")
	}
	if _, err := s.Load("../secrets"); err == nil {
		t.Fatal("a path must not be accepted as a run id")
	}
}

func TestRunsCopiedInByHandAreIndexed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runs")
	s := New(root)
	if _, err := s.Save(card("20261019-090000-aaaaaa", "prompts")); err != nil {
		t.Fatal(err)
	}
	// A CI cache restores a run file without the index entry.
	other := New(t.TempDir())
	path, err := other.Save(card("20261026-090000-bbbbbb", "prompts"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(root, "20261026-090000-bbbbbb.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A file whose name does not match the run inside is reported and left out.
	if err := os.WriteFile(filepath.Join(root, "20261020-090000-dddddd.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, problems, err := s.List()
	if err != nil || len(entries) != 2 || entries[0].ID != "20261026-090000-bbbbbb" || len(problems) != 1 {
		t.Fatalf("List = %+v %+v %v", entries, problems, err)
	}
}

func TestSymlinkedRunIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	root := filepath.Join(t.TempDir(), "runs")
	s := New(root)
	path, err := s.Save(card("20261019-090000-aaaaaa", "prompts"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "20261026-090000-eeeeee.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("20261026-090000-eeeeee"); err == nil {
		t.Fatal("a symlinked run file must not be read")
	}
}
