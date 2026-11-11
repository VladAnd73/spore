package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

func pruneFixtureRunDir(t *testing.T, runID string, age time.Duration, now time.Time) string {
	t.Helper()
	dir, err := RunDir("proj", runID)
	if err != nil {
		t.Fatal(err)
	}
	ts := now.Add(-age)
	if err := os.Chtimes(dir, ts, ts); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pruneFixtureTask(t *testing.T, tasksDir, runID, status string) {
	t.Helper()
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := task.Slugify("dream " + runID)
	m := frontmatter.Meta{Status: status, Slug: slug, Title: "dream " + runID, Project: "proj"}
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A run whose minted task is gone from tasks/ (judged and archived, or
// never minted) and old enough to be past the spawn race is reclaimed.
func TestPruneRemovesAnOldRunWithNoTask(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	tasksDir := filepath.Join(t.TempDir(), "tasks")
	dir := pruneFixtureRunDir(t, "20260101-old0", PruneMinAge+time.Hour, now)

	rep, err := Prune("proj", tasksDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "20260101-old0" {
		t.Fatalf("Removed = %v, want [20260101-old0]", rep.Removed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("run directory %s still exists after prune", dir)
	}
}

// A run whose task is done is exactly as reclaimable as one with no
// task at all: "done or gone" are the same signal.
func TestPruneRemovesAnOldRunWhoseTaskIsDone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	tasksDir := filepath.Join(t.TempDir(), "tasks")
	pruneFixtureRunDir(t, "20260101-done", PruneMinAge+time.Hour, now)
	pruneFixtureTask(t, tasksDir, "20260101-done", "done")

	rep, err := Prune("proj", tasksDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "20260101-done" {
		t.Fatalf("Removed = %v, want [20260101-done]", rep.Removed)
	}
}

// A run whose task is still active must survive no matter its age: the
// digest and known-claims files under it are what a live worker reads.
func TestPruneKeepsAnOldRunWhoseTaskIsStillActive(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	tasksDir := filepath.Join(t.TempDir(), "tasks")
	dir := pruneFixtureRunDir(t, "20260101-live", PruneMinAge+time.Hour, now)
	pruneFixtureTask(t, tasksDir, "20260101-live", "active")

	rep, err := Prune("proj", tasksDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 {
		t.Fatalf("Removed = %v, want none", rep.Removed)
	}
	if len(rep.Kept) != 1 || rep.Kept[0] != "20260101-live" {
		t.Fatalf("Kept = %v, want [20260101-live]", rep.Kept)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("run directory %s was removed: %v", dir, err)
	}
}

// The fleet spawns a worker for a minted task about 0.4 seconds after
// the mint, so a run has to be well past that before its task can even
// plausibly be judged. A fresh run must survive regardless of its
// task's state.
func TestPruneKeepsARunYoungerThanMinAge(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	tasksDir := filepath.Join(t.TempDir(), "tasks")
	pruneFixtureRunDir(t, "20260909-new0", time.Hour, now)

	rep, err := Prune("proj", tasksDir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 {
		t.Fatalf("Removed = %v, want none: a run younger than PruneMinAge must survive", rep.Removed)
	}
	if len(rep.Kept) != 1 || rep.Kept[0] != "20260909-new0" {
		t.Fatalf("Kept = %v, want [20260909-new0]", rep.Kept)
	}
}

func TestPruneOnAProjectWithNoRunsIsANoOp(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rep, err := Prune("proj", filepath.Join(t.TempDir(), "tasks"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 || len(rep.Kept) != 0 {
		t.Fatalf("expected no work, got %+v", rep)
	}
}
