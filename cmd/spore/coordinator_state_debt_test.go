package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const staleLessonState = "# state\n\n## CRITICAL LESSON: repo root lesson\n\n2020-01-01 learned the hard way.\n"

func stateDebtRepo(t *testing.T) (root, worktree string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	root = filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree = filepath.Join(root, ".worktrees", "alpha")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=T", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "-b", "wt/alpha", worktree},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "state.md"), []byte(staleLessonState), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", t.TempDir())
	return root, worktree
}

func TestCoordinatorStateDebtDefaultsToMainRepoState(t *testing.T) {
	root, worktree := stateDebtRepo(t)
	for name, dir := range map[string]string{"repo": root, "worktree": worktree} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(dir)
			code, stdout, stderr := captureHook(t, "", func() int { return runCoordinatorStateDebt(nil) })
			if code != 2 || !strings.Contains(stdout, "repo root lesson") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestCoordinatorStateDebtProjectAndStateFile(t *testing.T) {
	root, worktree := stateDebtRepo(t)
	t.Chdir(t.TempDir())

	code, stdout, stderr := captureHook(t, "", func() int {
		return runCoordinatorStateDebt([]string{"--project", worktree})
	})
	if code != 2 || !strings.Contains(stdout, "repo root lesson") {
		t.Fatalf("--project: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	other := filepath.Join(t.TempDir(), "other.md")
	if err := os.WriteFile(other, []byte("# clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = captureHook(t, "", func() int {
		return runCoordinatorStateDebt([]string{"--project", root, "--state-file", other})
	})
	if code != 0 || stdout != "" {
		t.Fatalf("--state-file over --project: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, _, stderr = captureHook(t, "", func() int {
		return runCoordinatorStateDebt([]string{"--project", filepath.Join(root, "missing/")})
	})
	if code != 1 || !strings.Contains(stderr, "not a directory") {
		t.Fatalf("bad --project: code=%d stderr=%q", code, stderr)
	}
}

func TestCoordinatorStateDebtRejectsPositionalBeforeProject(t *testing.T) {
	root, _ := stateDebtRepo(t)
	t.Chdir(t.TempDir())

	code, stdout, stderr := captureHook(t, "", func() int {
		return runCoordinatorStateDebt([]string{"foo", "--project", root})
	})
	if code != 2 || stdout != "" || !strings.Contains(stderr, `unexpected argument "foo"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
