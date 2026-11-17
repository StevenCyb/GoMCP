package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResolveStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"", "a.txt", "sub/a.txt", "sub/../a.txt", "/a.txt"} {
		p, err := resolve(root, rel)
		if err != nil {
			t.Errorf("%q: %v", rel, err)
		} else if r, err := filepath.Rel(root, p); err != nil || filepath.IsAbs(r) || r == ".." {
			t.Errorf("%q -> %s is outside", rel, p)
		}
	}
	bad := []string{"..", "../x", "sub/../../x", "a/../../../etc/passwd"}
	if filepath.Separator == '\\' {
		bad = append(bad, `..\x`) // a backslash is an ordinary character elsewhere
	}
	for _, rel := range bad {
		if _, err := resolve(root, rel); err == nil {
			t.Errorf("%q must be refused", rel)
		}
	}
}

func TestSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Notes.txt", "sub/more-notes.md", "other.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := search(root, "NOTES")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"Notes.txt", "sub/more-notes.md"}) {
		t.Errorf("got %v", got)
	}
}
