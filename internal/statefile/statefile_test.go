package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPrefersXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg")
	got, err := Path("spore", "dream.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/xdg/spore/spore/dream.json"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	got, err := Path("spore", "dream.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/h/.local/state/spore/spore/dream.json"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPathErrorsWithoutHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := Path("spore", "x.json"); err == nil {
		t.Fatal("expected error when neither XDG_STATE_HOME nor HOME is set")
	}
}

func TestWriteJSONAtomicCreatesDirsAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "deep", "out.json")
	if err := WriteJSONAtomic(p, "test", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{\n  \"a\": 1\n}" {
		t.Fatalf("unexpected content: %q", string(b))
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("expected only the final file, got %d entries", len(entries))
	}
}

// Repeated saves are the normal case for watch state, so a second write
// must replace the first rather than fail on the existing file.
func TestWriteJSONAtomicOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.json")
	if err := WriteJSONAtomic(p, "test", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONAtomic(p, "test", map[string]int{"b": 2}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{\n  \"b\": 2\n}" {
		t.Fatalf("unexpected content: %q", string(b))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected only the final file, got %d entries", len(entries))
	}
}
