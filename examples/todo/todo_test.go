package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadSave(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todo.json")

	got, err := load(file)
	if err != nil || len(got) != 0 {
		t.Fatalf("a missing file is an empty list, got %v, %v", got, err)
	}

	want := []Item{{ID: 1, Text: "a"}, {ID: 2, Text: "b", Done: true}}
	if err := save(file, want); err != nil {
		t.Fatal(err)
	}
	got, err = load(file)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("round trip: got %v, %v", got, err)
	}
	if o := open(got); len(o) != 1 || o[0].ID != 1 {
		t.Errorf("open: %v", o)
	}
}

func TestLoadRejectsBrokenFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todo.json")
	if err := os.WriteFile(file, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(file); err == nil {
		t.Error("a broken file must stop the start, not be overwritten")
	}
}
