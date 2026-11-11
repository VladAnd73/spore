package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cmd/spore decoded the manifest's created_at itself (dreamListRuns), a
// second copy of the same format Run and Seal write. Moving it here
// means any future manifest change has one reader to keep in sync, not
// two.
func TestListRunsOrdersNewestFirstAndFallsBackToDirectoryMtimeWithoutAManifest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	old, err := RunDir("proj", "20200101-old0")
	if err != nil {
		t.Fatal(err)
	}
	newest, err := RunDir("proj", "20260903-new0")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(newest, []string{target}); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, ts, ts); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns("proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 runs, got %d: %+v", len(runs), runs)
	}
	if runs[0].RunID != "20260903-new0" {
		t.Fatalf("runs are not newest first: %+v", runs)
	}
	if !runs[0].Revertible {
		t.Error("a run with a manifest must be revertible")
	}
	if runs[0].Dated != "manifest created_at" {
		t.Errorf("Dated = %q, want manifest created_at", runs[0].Dated)
	}
	if runs[1].RunID != "20200101-old0" || runs[1].Revertible {
		t.Errorf("a run with no manifest must not claim to be revertible: %+v", runs[1])
	}
	if runs[1].Dated != "directory mtime" {
		t.Errorf("Dated = %q, want directory mtime", runs[1].Dated)
	}
}

func TestListRunsReturnsNoneWhenTheDreamsDirDoesNotExist(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runs, err := ListRuns("proj")
	if err != nil {
		t.Fatal(err)
	}
	if runs != nil {
		t.Fatalf("want no runs, got %+v", runs)
	}
}
