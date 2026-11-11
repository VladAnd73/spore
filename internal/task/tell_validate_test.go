package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTaskFile drops a minimal tasks/<slug>.md with the given status.
func writeTaskFile(t *testing.T, projectRoot, slug, status string) {
	t.Helper()
	dir := filepath.Join(projectRoot, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nstatus: " + status + "\nslug: " + slug + "\ntitle: " + slug + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stateFileCount counts every regular file under root, so a test can
// assert that a refused tell left the state tree completely untouched.
func stateFileCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Scenario 1: an unknown slug is refused, names itself in the error, and
// leaves nothing at all behind in the state root.
func TestTellRejectsUnknownSlugAndWritesNothing(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	writeTaskFile(t, project, "realslug", "active")

	err := Tell("definitely-no-such-slug-xyz", "probe")
	if err == nil {
		t.Fatal("Tell(unknown slug) = nil, want an error")
	}
	if !errors.Is(err, ErrUnknownSlug) {
		t.Errorf("Tell error = %v, want it to wrap ErrUnknownSlug", err)
	}
	if !strings.Contains(err.Error(), "definitely-no-such-slug-xyz") {
		t.Errorf("Tell error = %q, want it to name the unknown slug", err)
	}
	if n := stateFileCount(t, state); n != 0 {
		t.Errorf("state root holds %d file(s) after a refused tell, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(state, "spore", filepath.Base(project), "definitely-no-such-slug-xyz")); !os.IsNotExist(err) {
		t.Errorf("refused tell created a slug dir under the state root")
	}
}

// Scenario 2: a task that exists accepts a tell in every status. Status
// gates nothing - queuing for a paused or blocked worker is legitimate.
func TestTellDeliversToAnExistingTaskInAnyStatus(t *testing.T) {
	for _, status := range []string{"active", "paused", "blocked", "draft", "done"} {
		t.Run(status, func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			writeTaskFile(t, project, "realslug", status)

			if err := Tell("realslug", "x"); err != nil {
				t.Fatalf("Tell(%s task) = %v, want nil", status, err)
			}
			inbox := filepath.Join(state, "spore", filepath.Base(project), "realslug", "inbox")
			entries, err := os.ReadDir(inbox)
			if err != nil {
				t.Fatalf("read inbox: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("inbox entries = %d, want 1", len(entries))
			}
		})
	}
}

// Scenario 3: "coordinator" never has a task file and is the single most
// used destination. It must always deliver.
func TestTellAlwaysDeliversToTheCoordinatorSlug(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if err := Tell("coordinator", "escalation"); err != nil {
		t.Fatalf("Tell(coordinator) = %v, want nil", err)
	}
	inbox, err := CoordinatorInboxDirForProject("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inbox, state+string(filepath.Separator)) {
		t.Fatalf("coordinator inbox %s is outside XDG_STATE_HOME %s", inbox, state)
	}
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read coordinator inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("coordinator inbox entries = %d, want 1", len(entries))
	}
}

// Scenario 4: a caller standing in a linked worktree sees no tasks/ of its
// own, but the slug is valid for the project. Delivery must still succeed:
// no valid destination may become unreachable because of where the caller
// stood.
func TestTellFromAWorktreeResolvesTasksFromTheMainRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	parent := t.TempDir()
	main := filepath.Join(parent, "someproject")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitRepo(t, main)
	writeTaskFile(t, main, "realslug", "active")
	worktree := filepath.Join(main, ".worktrees", "other-slug")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, main, "worktree", "add", "-q", worktree, "-b", "wt/other-slug")

	t.Chdir(worktree)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if _, err := os.Stat(filepath.Join(worktree, "tasks")); !os.IsNotExist(err) {
		t.Fatalf("precondition: worktree should have no tasks/ of its own")
	}
	if err := Tell("realslug", "from the worktree"); err != nil {
		t.Fatalf("Tell from worktree cwd = %v, want nil", err)
	}
	inbox := filepath.Join(state, "spore", "someproject", "realslug", "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1", len(entries))
	}
}

// An inbox that already exists is a live destination somebody is draining,
// even when no task file backs it. Keep delivering to it: this is what
// makes the gate non-breaking for destinations that work today.
func TestTellDeliversWhenTheInboxAlreadyExists(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	inbox := filepath.Join(state, "spore", filepath.Base(project), "legacy-destination", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Tell("legacy-destination", "x"); err != nil {
		t.Fatalf("Tell(existing inbox) = %v, want nil", err)
	}
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1", len(entries))
	}
}
