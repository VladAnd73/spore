package dream

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/versality/spore/internal/statefile"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// hashTree walks root and returns path -> content hash for every regular
// file, so a test can assert the whole tree rather than a hand-picked pair.
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffTrees(before, after map[string]string) []string {
	var out []string
	for k, v := range before {
		switch got, ok := after[k]; {
		case !ok:
			out = append(out, "missing after revert: "+k)
		case got != v:
			out = append(out, "content changed: "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			out = append(out, "left behind by the run: "+k)
		}
	}
	sort.Strings(out)
	return out
}

func TestSnapshotThenRevertRestoresBytes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	a := filepath.Join(work, "state.md")
	b := filepath.Join(work, "mem.md")
	write(t, a, "before-a", 0o644)
	write(t, b, "before-b", 0o644)

	dir, err := RunDir("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{a, b}); err != nil {
		t.Fatal(err)
	}

	write(t, a, "after-a", 0o644)
	write(t, b, "after-b", 0o644)

	restored, err := Revert("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("expected 2 files restored, got %d", len(restored))
	}
	if got := read(t, a); got != "before-a" {
		t.Fatalf("a not restored: %q", got)
	}
	if got := read(t, b); got != "before-b" {
		t.Fatalf("b not restored: %q", got)
	}
}

func TestSnapshotRecordsAbsentFilesSoRevertDeletesThem(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	newFile := filepath.Join(work, "created.md")

	dir, err := RunDir("proj", "run-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{newFile}); err != nil {
		t.Fatal(err)
	}
	write(t, newFile, "created by the run", 0o644)

	if _, err := Revert("proj", "run-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("a file the run created must be removed on revert")
	}
}

func TestRevertRestoresOriginalMode(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	private := filepath.Join(work, "private.md")
	write(t, private, "secret", 0o640)

	dir, err := RunDir("proj", "run-3")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{private}); err != nil {
		t.Fatal(err)
	}
	write(t, private, "widened by the run", 0o644)

	if _, err := Revert("proj", "run-3"); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, private); got != 0o640 {
		t.Fatalf("revert must not change the mode: want 0640, got %#o", got)
	}
}

func TestBackupCopyIsNotReadableBeyondOwner(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	private := filepath.Join(work, "private.md")
	write(t, private, "secret", 0o640)

	dir, err := RunDir("proj", "run-3b")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{private}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "backup"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup copy, got %d", len(entries))
	}
	got := mode(t, filepath.Join(dir, "backup", entries[0].Name()))
	if got&0o077 != 0 {
		t.Fatalf("a backup of a private file must not be group- or world-readable: %#o", got)
	}
}

func TestRevertReportsFilesItCouldNotRestore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	first := filepath.Join(work, "first.md")
	blocked := filepath.Join(work, "blocked.md")
	last := filepath.Join(work, "last.md")
	for _, p := range []string{first, blocked, last} {
		write(t, p, "before", 0o644)
	}

	dir, err := RunDir("proj", "run-4")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{first, blocked, last}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{first, last} {
		write(t, p, "after", 0o644)
	}
	// A directory in the target's place cannot be overwritten by any user,
	// so this failure reproduces for root and non-root alike.
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := RevertWithReport("proj", "run-4")
	if err == nil {
		t.Fatal("a partial revert must return an error")
	}
	if len(report.Failed) != 1 || report.Failed[0].Path != blocked {
		t.Fatalf("expected blocked.md reported as failed, got %+v", report.Failed)
	}
	if len(report.Restored) != 2 {
		t.Fatalf("revert must continue past a failure: %+v", report.Restored)
	}
	if got := read(t, last); got != "before" {
		t.Fatalf("the entry after the failure was skipped: %q", got)
	}
}

// RunDir MkdirAlls as a side effect, so a revert that used it to find the
// manifest would create an empty run directory for a run id nobody has
// heard of. RevertWithReport must look without creating.
func TestRevertWithReportRefusesAnUnknownRunWithoutCreatingItsDirectory(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	_, err := RevertWithReport("proj", "run-ghost")
	if err == nil {
		t.Fatal("an unknown run must not revert cleanly")
	}
	if !strings.Contains(err.Error(), "run-ghost") {
		t.Errorf("the error does not name the run: %v", err)
	}

	dir, err := statefile.Path("proj", filepath.Join("dreams", "run-ghost"))
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("RevertWithReport created %s for a run it never heard of", dir)
	}
}

func TestSnapshotDirRemovesAFileCreatedWithAnUnpredictableName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	memory := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memory, 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(memory, "MEMORY.md"), "index", 0o640)

	dir, err := RunDir("proj", "run-6")
	if err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDir(dir, []string{memory}); err != nil {
		t.Fatal(err)
	}
	// The judging worker names the lesson file at write time; no caller
	// could have listed it in advance.
	invented := filepath.Join(memory, "pw_signin_201_and_shared_request_context.md")
	write(t, invented, "a lesson", 0o640)

	if _, err := Revert("proj", "run-6"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(invented); !os.IsNotExist(err) {
		t.Fatal("a memory file the run invented must be removed on revert")
	}
	if _, err := os.Stat(filepath.Join(memory, "MEMORY.md")); err != nil {
		t.Fatalf("a file that predates the run must survive: %v", err)
	}
}

func TestSnapshotDirRestoresAFileTheRunEditedOrDeleted(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	memory := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memory, 0o750); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(memory, "feedback_commit_author.md")
	deleted := filepath.Join(memory, "project_gh_token.md")
	write(t, edited, "the original lesson", 0o650)
	write(t, deleted, "another original lesson", 0o640)

	dir, err := RunDir("proj", "run-6b")
	if err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDir(dir, []string{memory}); err != nil {
		t.Fatal(err)
	}
	// Which existing file the dream rewrites is decided at run time too,
	// so a name-only listing cannot cover it.
	write(t, edited, "rewritten by the dream", 0o644)
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	if _, err := Revert("proj", "run-6b"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, edited); got != "the original lesson" {
		t.Fatalf("an edited memory file was not restored: %q", got)
	}
	if got := read(t, deleted); got != "another original lesson" {
		t.Fatalf("a deleted memory file was not restored: %q", got)
	}
	if got := mode(t, edited); got != 0o650 {
		t.Fatalf("mode not restored: %#o", got)
	}
}

func TestSealedRevertRefusesAFileChangedAfterTheRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	state := filepath.Join(work, "state.md")
	write(t, state, "before the dream", 0o644)

	dir, err := RunDir("proj", "run-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{state}); err != nil {
		t.Fatal(err)
	}
	write(t, state, "written by the dream", 0o644)
	if err := Seal(dir); err != nil {
		t.Fatal(err)
	}
	write(t, state, "written by the coordinator at 03:10", 0o644)

	report, err := RevertWithReport("proj", "run-7")
	if err == nil {
		t.Fatal("revert must refuse a file changed since the run")
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Path != state {
		t.Fatalf("expected state.md reported as skipped, got %+v", report.Skipped)
	}
	if got := read(t, state); got != "written by the coordinator at 03:10" {
		t.Fatalf("revert destroyed another writer's work: %q", got)
	}
}

func TestSealedRevertKeepsAFileCreatedAfterTheRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	memory := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memory, 0o750); err != nil {
		t.Fatal(err)
	}

	dir, err := RunDir("proj", "run-8")
	if err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDir(dir, []string{memory}); err != nil {
		t.Fatal(err)
	}
	byTheDream := filepath.Join(memory, "lesson_from_the_dream.md")
	write(t, byTheDream, "dreamt", 0o640)
	if err := Seal(dir); err != nil {
		t.Fatal(err)
	}
	byAHuman := filepath.Join(memory, "written_by_a_human_at_0900.md")
	write(t, byAHuman, "human work", 0o640)

	report, err := RevertWithReport("proj", "run-8")
	if err == nil {
		t.Fatal("leaving a foreign file in place makes the revert incomplete, which must be reported")
	}
	if _, err := os.Stat(byTheDream); !os.IsNotExist(err) {
		t.Fatal("the dream's own file must be removed")
	}
	if _, err := os.Stat(byAHuman); err != nil {
		t.Fatalf("a file created after the run must survive revert: %v", err)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Path != byAHuman {
		t.Fatalf("the surviving file must be reported as skipped, got %+v", report.Skipped)
	}
}

func TestFullRunRevertLeavesTheTreeByteIdentical(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	memory := filepath.Join(root, "memory")
	if err := os.MkdirAll(memory, 0o750); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state.md")
	write(t, state, "coordinator handover\n", 0o654)
	index := filepath.Join(memory, "MEMORY.md")
	write(t, index, "# index\n- one\n", 0o640)
	for _, n := range []string{"feedback_commit_author.md", "project_gh_token.md", "reference_linear_api.md"} {
		write(t, filepath.Join(memory, n), "old lesson "+n+"\n", 0o650)
	}
	before := hashTree(t, root)

	dir, err := RunDir("proj", "run-9")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{state, index}); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDir(dir, []string{memory}); err != nil {
		t.Fatal(err)
	}

	write(t, state, "rewritten by the dream\n", 0o644)
	write(t, index, "# index\n- one\n- two\n- three\n", 0o644)
	write(t, filepath.Join(memory, "feedback_commit_author.md"), "edited by the dream\n", 0o644)
	for _, n := range []string{"npm_auth_never_project_npmrc.md", "vite_plugin_checker_nested_worktree.md"} {
		write(t, filepath.Join(memory, n), "invented at write time\n", 0o644)
	}

	report, err := RevertWithReport("proj", "run-9")
	if err != nil {
		t.Fatalf("revert: %v (%+v)", err, report)
	}
	if d := diffTrees(before, hashTree(t, root)); len(d) != 0 {
		t.Fatalf("tree not byte-identical after revert:\n%s", strings.Join(d, "\n"))
	}
	if got := mode(t, state); got != 0o654 {
		t.Fatalf("state.md mode not restored: %#o", got)
	}
}
