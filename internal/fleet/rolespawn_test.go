package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/editor"
	"github.com/versality/spore/internal/task"
)

func TestRoleSpawnSpecEngineerArgs(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	spec := EngineerSpec(root, "feature-x")
	args, err := spec.TmuxArgs()
	if err != nil {
		t.Fatalf("TmuxArgs: %v", err)
	}

	wantSession := "spore-role/" + filepath.Base(root) + "/feature-x/engineer"
	if v := flagValue(args, "-s"); v != wantSession {
		t.Errorf("session = %q, want %q", v, wantSession)
	}
	if v := flagValue(args, "-c"); v != filepath.Join(root, ".worktrees", "feature-x") {
		t.Errorf("cwd = %q, want engineer worktree", v)
	}
	if e := envValue(args, "SPORE_ROLE"); e != "engineer" {
		t.Errorf("SPORE_ROLE = %q, want engineer", e)
	}
	if e := envValue(args, "SPORE_REVIEWER_INSTANCE"); e != "" {
		t.Errorf("SPORE_REVIEWER_INSTANCE set on engineer pane: %q", e)
	}
	if e := envValue(args, "SPORE_TASK_DIR"); e != task.RoleTaskDir(root, "feature-x") {
		t.Errorf("SPORE_TASK_DIR = %q, want %q", e, task.RoleTaskDir(root, "feature-x"))
	}
}

func TestRoleSpawnSpecReviewerArgs(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	for _, instance := range []task.ReviewerInstance{task.ReviewerA, task.ReviewerB} {
		spec := ReviewerSpec(root, "feature-x", instance)
		args, err := spec.TmuxArgs()
		if err != nil {
			t.Fatalf("TmuxArgs %s: %v", instance, err)
		}
		wantSession := "spore-role/" + filepath.Base(root) + "/feature-x/reviewer-" + string(instance)
		if v := flagValue(args, "-s"); v != wantSession {
			t.Errorf("%s session = %q, want %q", instance, v, wantSession)
		}
		if v := flagValue(args, "-c"); v != filepath.Join(root, ".worktrees", "feature-x") {
			t.Errorf("%s cwd = %q, want task worktree", instance, v)
		}
		if e := envValue(args, "SPORE_ROLE"); e != "reviewer" {
			t.Errorf("%s SPORE_ROLE = %q, want reviewer", instance, e)
		}
		if e := envValue(args, "SPORE_REVIEWER_INSTANCE"); e != string(instance) {
			t.Errorf("%s SPORE_REVIEWER_INSTANCE = %q, want %q", instance, e, instance)
		}
	}
}

func TestRoleSpawnSpecInjectsAccountTier(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	toml := "[fleet]\naccount_tier = \"max\"\n"
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, spec := range []RoleSpawnSpec{
		EngineerSpec(root, "feature-x"),
		ReviewerSpec(root, "feature-x", task.ReviewerA),
	} {
		args, err := spec.TmuxArgs()
		if err != nil {
			t.Fatalf("TmuxArgs %s: %v", spec.Role, err)
		}
		if e := envValue(args, "SPORE_ACCOUNT_TIER"); e != "max" {
			t.Errorf("%s SPORE_ACCOUNT_TIER = %q, want max", spec.Role, e)
		}
	}
}

func TestRoleSpawnSpecOmitsTierWithoutKnob(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	args, err := EngineerSpec(root, "feature-x").TmuxArgs()
	if err != nil {
		t.Fatalf("TmuxArgs: %v", err)
	}
	if e := envValue(args, "SPORE_ACCOUNT_TIER"); e != "" {
		t.Errorf("SPORE_ACCOUNT_TIER = %q, want unset without the knob", e)
	}
}

func TestRoleSpawnSpecRejectsBadRole(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	spec := RoleSpawnSpec{ProjectRoot: root, Slug: "feature-x", Role: "qa"}
	if _, err := spec.TmuxArgs(); err == nil {
		t.Fatal("expected error on unknown role, got nil")
	}
}

func TestRoleSpawnSpecRejectsMissingInstance(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	spec := RoleSpawnSpec{ProjectRoot: root, Slug: "feature-x", Role: "reviewer"}
	if _, err := spec.TmuxArgs(); err == nil {
		t.Fatal("expected error on reviewer with empty instance, got nil")
	}
}

// flagValue returns the argument following the named flag in argv, or
// the empty string when the flag is absent.
func flagValue(argv []string, flag string) string {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == flag {
			return argv[i+1]
		}
	}
	return ""
}

// envValue returns the value of KEY in `-e KEY=value` pairs, or the
// empty string when absent.
func envValue(argv []string, key string) string {
	prefix := key + "="
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] != "-e" {
			continue
		}
		if strings.HasPrefix(argv[i+1], prefix) {
			return strings.TrimPrefix(argv[i+1], prefix)
		}
	}
	return ""
}

// newGitRepoFor returns a tmpdir initialised as a git repo and
// renamed to projectName, so task.ProjectName can resolve the
// project basename via `git rev-parse --git-common-dir`. The rename
// keeps the repo basename stable across tmpdir randomness.
func newGitRepoFor(t *testing.T, projectName string) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, projectName)
	if err := exec.Command("mkdir", "-p", root).Run(); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

func TestRoleSpawnSpecOmitsAutoConnectIDE(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[editor]\nauto_connect_ide = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(editor.AutoConnectIDEEnv, "1")
	for _, spec := range []RoleSpawnSpec{
		EngineerSpec(root, "feature-x"),
		ReviewerSpec(root, "feature-x", task.ReviewerA),
	} {
		args, err := spec.TmuxArgs()
		if err != nil {
			t.Fatalf("TmuxArgs %s: %v", spec.Role, err)
		}
		if hasEnvArg(args, editor.AutoConnectIDEEnv) {
			t.Errorf("%s spawn carries %s: %v", spec.Role, editor.AutoConnectIDEEnv, args)
		}
	}
}

func hasEnvArg(argv []string, key string) bool {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], key+"=") {
			return true
		}
	}
	return false
}

func TestRoleSpawnPreStartUnsetKeepsArgv(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	spec := EngineerSpec(root, "feature-x")
	args, err := spec.TmuxArgs()
	if err != nil {
		t.Fatal(err)
	}
	body, err := ResolveRoleBody(root, "engineer.md")
	if err != nil {
		t.Fatal(err)
	}
	want := coordinatorShellCommand("claude", body)
	if got := args[len(args)-1]; got != want {
		t.Errorf("shell command = %q, want unchanged %q", got, want)
	}
}

func TestRoleSpawnPreStartWrapsAgent(t *testing.T) {
	root := newGitRepoFor(t, "demo-project")
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet]\npre_start = \"bin/with-services up\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []RoleSpawnSpec{
		EngineerSpec(root, "feature-x"),
		ReviewerSpec(root, "feature-x", task.ReviewerA),
	} {
		args, err := spec.TmuxArgs()
		if err != nil {
			t.Fatal(err)
		}
		got := args[len(args)-1]
		lock := task.PreStartLockPath(root, "feature-x")
		marker := task.PreStartMarkerPath(root, "feature-x", roleKey(spec))
		for _, want := range []string{
			": > " + marker + " && flock " + lock + " timeout 900 sh -c 'bin/with-services up'",
			"exec claude --dangerously-skip-permissions -- ",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q in %s", spec.Role, want, got)
			}
		}
	}
}

func TestRoleShellCommandRuns(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not on PATH")
	}
	failNote := "spore: pre-start hook failed (exit 2): exit 2. Services may be down."
	cases := []struct {
		name, cmd, body, want string
	}{
		{"ok with body", "true", "role body", "role body"},
		{"fail with body", "exit 2", "role body", failNote + "\n\nrole body"},
		{"ok bare", "true", "", ""},
		{"fail bare", "exit 2", "", failNote},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			rolePath := filepath.Join(root, "it's role.md")
			if c.body != "" {
				if err := os.WriteFile(rolePath, []byte(c.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			script := roleShellCommand(task.PreStart{Cmd: c.cmd}.Prelude(root, "feat", "engineer"), "printf %s", "printf %s", rolePath)
			out, err := exec.Command("sh", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v: %s", err, out)
			}
			if string(out) != c.want {
				t.Errorf("agent got %q, want %q", out, c.want)
			}
		})
	}
}

func TestRoleSpawnAgentResolution(t *testing.T) {
	cases := []struct {
		name     string
		agent    string
		overlay  string
		wantBody string
	}{
		{name: "kernel default", wantBody: "exec claude --dangerously-skip-permissions -- "},
		{name: "overlay default", overlay: "codex", wantBody: "exec codex --dangerously-bypass-approvals-and-sandbox "},
		{name: "frontmatter beats overlay", agent: "claude", overlay: "codex", wantBody: "exec claude --dangerously-skip-permissions -- "},
		{name: "frontmatter codex", agent: "codex", wantBody: "exec codex --dangerously-bypass-approvals-and-sandbox "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv(task.CodexBinEnv, "")
			t.Setenv(task.CodexModelEnv, "")
			root := newGitRepoFor(t, "demo-project")
			if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
				t.Fatal(err)
			}
			fm := "---\nstatus: active\nslug: feature-x\n"
			if c.agent != "" {
				fm += "agent: " + c.agent + "\n"
			}
			if err := os.WriteFile(filepath.Join(root, "tasks", "feature-x.md"), []byte(fm+"---\nbody\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if c.overlay != "" {
				if _, err := SetWorkerDefault(root, c.overlay); err != nil {
					t.Fatal(err)
				}
			}
			for _, spec := range []RoleSpawnSpec{EngineerSpec(root, "feature-x"), ReviewerSpec(root, "feature-x", task.ReviewerA)} {
				args, err := spec.TmuxArgs()
				if err != nil {
					t.Fatalf("%s TmuxArgs: %v", spec.Role, err)
				}
				cmd := args[len(args)-1]
				if !strings.Contains(cmd, c.wantBody) {
					t.Errorf("%s command = %q, want %q", spec.Role, cmd, c.wantBody)
				}
				if strings.Contains(c.wantBody, "codex") && !strings.Contains(cmd, `-- "$(cat `) {
					t.Errorf("%s codex command lacks -- before the role body: %q", spec.Role, cmd)
				}
			}
		})
	}
}

func TestRoleSpawnIgnoresWorkerRatio(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet.workers.ratio]\ncodex = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := EngineerSpec(root, "feature-x").TmuxArgs()
	if err != nil {
		t.Fatal(err)
	}
	if cmd := args[len(args)-1]; !strings.Contains(cmd, "exec claude ") {
		t.Errorf("command = %q, want claude (ratio must not drive role panes)", cmd)
	}
}
