package fleet

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/task"
)

func TestLoadReviewersConfig(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		overlay string
		want    ReviewersConfig
	}{
		{
			name: "defaults A to code-review, B to fleet",
			want: ReviewersConfig{
				A: ReviewerSlot{Mode: "code-review", DefaultMode: true},
				B: ReviewerSlot{Mode: "fleet", DefaultMode: true},
			},
		},
		{
			name: "explicit fleet on A honored",
			toml: "[fleet.reviewers.A]\nmode = \"fleet\"\n",
			want: ReviewersConfig{A: ReviewerSlot{Mode: "fleet"}, B: ReviewerSlot{Mode: "fleet", DefaultMode: true}},
		},
		{
			name: "implicit A with codex slot agent loads, spawn settles it",
			toml: "[fleet.reviewers.A]\nagent = \"codex\"\n",
			want: ReviewersConfig{
				A: ReviewerSlot{Mode: "code-review", Agent: "codex", DefaultMode: true},
				B: ReviewerSlot{Mode: "fleet", DefaultMode: true},
			},
		},
		{
			name: "per-slot mode agent effort",
			toml: "[fleet]\naccount_tier = \"max\"\n[fleet.reviewers.A]\nmode = \"code-review\"\neffort = \"high\" # bug hunt\n[fleet.reviewers.B]\nmode = \"fleet\"\nagent = \"codex\"\n",
			want: ReviewersConfig{
				A: ReviewerSlot{Mode: "code-review", Effort: "high"},
				B: ReviewerSlot{Mode: "fleet", Agent: "codex"},
			},
		},
		{
			name: "claude-code alias",
			toml: "[fleet.reviewers.A]\nmode = \"code-review\"\nagent = \"claude-code\"\n",
			want: ReviewersConfig{A: ReviewerSlot{Mode: "code-review", Agent: "claude"}, B: ReviewerSlot{Mode: "fleet", DefaultMode: true}},
		},
		{
			name:    "overlay wins key by key",
			toml:    "[fleet.reviewers.A]\nmode = \"code-review\"\neffort = \"low\"\n",
			overlay: "[fleet.workers]\ndefault = \"codex\"\n[fleet.reviewers.A]\neffort = \"max\"\n[fleet.reviewers.B]\nagent = \"codex\"\n",
			want: ReviewersConfig{
				A: ReviewerSlot{Mode: "code-review", Effort: "max"},
				B: ReviewerSlot{Mode: "fleet", Agent: "codex", DefaultMode: true},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			root := newGitRepoFor(t, "demo-project")
			if c.toml != "" {
				writeSporeTOML(t, root, c.toml)
			}
			if c.overlay != "" {
				writeWorkersOverlay(t, root, c.overlay)
			}
			got, err := LoadReviewersConfig(root)
			if err != nil {
				t.Fatalf("LoadReviewersConfig: %v", err)
			}
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestLoadReviewersConfigRejects(t *testing.T) {
	cases := []struct {
		name, toml, overlay, wantErr string
	}{
		{name: "code-review on codex", toml: "[fleet.reviewers.A]\nmode = \"code-review\"\nagent = \"codex\"\n", wantErr: "needs agent claude"},
		{name: "overlay flips code-review slot to codex", toml: "[fleet.reviewers.B]\nmode = \"code-review\"\n", overlay: "[fleet.reviewers.B]\nagent = \"codex\"\n", wantErr: "[fleet.reviewers.B]"},
		{name: "unknown mode", toml: "[fleet.reviewers.A]\nmode = \"lgtm\"\n", wantErr: "mode \"lgtm\""},
		{name: "unknown effort", toml: "[fleet.reviewers.A]\nmode = \"code-review\"\neffort = \"huge\"\n", wantErr: "effort \"huge\""},
		{name: "unknown key", toml: "[fleet.reviewers.A]\nmodel = \"x\"\n", wantErr: "unknown key \"model\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			root := newGitRepoFor(t, "demo-project")
			writeSporeTOML(t, root, c.toml)
			if c.overlay != "" {
				writeWorkersOverlay(t, root, c.overlay)
			}
			_, err := LoadReviewersConfig(root)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

// writeRoleBodies drops placeholder role bodies so code-review spawns
// pass the body presence check.
func writeRoleBodies(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "bootstrap", "roles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"engineer.md", "reviewer.md", "reviewer-code-review.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeRoleTask(t *testing.T, root, agent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nstatus: active\nslug: feature-x\n"
	if agent != "" {
		fm += "agent: " + agent + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "feature-x.md"), []byte(fm+"---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRoleSpawnReviewerModes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(task.CodexBinEnv, "")
	t.Setenv(task.CodexModelEnv, "")
	root := newGitRepoFor(t, "demo-project")
	writeRoleBodies(t, root)
	// The task pins codex: slot A must still run claude (code-review is
	// claude-only), slot B follows the task, the engineer is untouched.
	writeRoleTask(t, root, "codex")
	writeSporeTOML(t, root, "[fleet.reviewers.A]\nmode = \"code-review\"\neffort = \"high\"\n[fleet.reviewers.B]\nmode = \"fleet\"\n")

	argsA, err := ReviewerSpec(root, "feature-x", task.ReviewerA).TmuxArgs()
	if err != nil {
		t.Fatalf("A TmuxArgs: %v", err)
	}
	cmdA := argsA[len(argsA)-1]
	if !strings.Contains(cmdA, "exec claude --dangerously-skip-permissions -- ") {
		t.Errorf("A command = %q, want claude", cmdA)
	}
	if !strings.Contains(cmdA, filepath.Join("bootstrap", "roles", "reviewer-code-review.md")) {
		t.Errorf("A command = %q, want the code-review body", cmdA)
	}
	if e := envValue(argsA, "SPORE_REVIEWER_MODE"); e != "code-review" {
		t.Errorf("A SPORE_REVIEWER_MODE = %q", e)
	}
	if e := envValue(argsA, "SPORE_REVIEW_EFFORT"); e != "high" {
		t.Errorf("A SPORE_REVIEW_EFFORT = %q, want high", e)
	}

	argsB, err := ReviewerSpec(root, "feature-x", task.ReviewerB).TmuxArgs()
	if err != nil {
		t.Fatalf("B TmuxArgs: %v", err)
	}
	cmdB := argsB[len(argsB)-1]
	if !strings.Contains(cmdB, "exec codex ") {
		t.Errorf("B command = %q, want the task's codex", cmdB)
	}
	if !strings.Contains(cmdB, filepath.Join("bootstrap", "roles", "reviewer.md")) {
		t.Errorf("B command = %q, want reviewer.md", cmdB)
	}
	if e := envValue(argsB, "SPORE_REVIEWER_MODE"); e != "fleet" {
		t.Errorf("B SPORE_REVIEWER_MODE = %q, want fleet", e)
	}
	if hasEnvArg(argsB, "SPORE_REVIEW_EFFORT") {
		t.Errorf("B carries SPORE_REVIEW_EFFORT in fleet mode")
	}

	argsE, err := EngineerSpec(root, "feature-x").TmuxArgs()
	if err != nil {
		t.Fatalf("engineer TmuxArgs: %v", err)
	}
	if hasEnvArg(argsE, "SPORE_REVIEWER_MODE") {
		t.Errorf("engineer carries SPORE_REVIEWER_MODE")
	}
}

func TestRoleSpawnReviewerSlotAgentOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(task.CodexBinEnv, "")
	t.Setenv(task.CodexModelEnv, "")
	root := newGitRepoFor(t, "demo-project")
	writeRoleTask(t, root, "")
	writeSporeTOML(t, root, "[fleet.reviewers.B]\nagent = \"codex\"\n")

	want := map[string]string{
		"engineer": "exec claude ",
		"A":        "exec claude ",
		"B":        "exec codex ",
	}
	specs := map[string]RoleSpawnSpec{
		"engineer": EngineerSpec(root, "feature-x"),
		"A":        ReviewerSpec(root, "feature-x", task.ReviewerA),
		"B":        ReviewerSpec(root, "feature-x", task.ReviewerB),
	}
	for name, spec := range specs {
		args, err := spec.TmuxArgs()
		if err != nil {
			t.Fatalf("%s TmuxArgs: %v", name, err)
		}
		if cmd := args[len(args)-1]; !strings.Contains(cmd, want[name]) {
			t.Errorf("%s command = %q, want %q", name, cmd, want[name])
		}
	}
}

func TestRoleSpawnImplicitCodeReviewFallsBackToFleet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(task.CodexBinEnv, "")
	t.Setenv(task.CodexModelEnv, "")
	cases := []struct {
		name, taskAgent, toml, specAgent, wantMode, wantCmd, wantBody string
	}{
		{name: "claude default", wantMode: "code-review", wantCmd: "exec claude ", wantBody: "reviewer-code-review.md"},
		{name: "task pins codex", taskAgent: "codex", wantMode: "fleet", wantCmd: "exec codex ", wantBody: "reviewer.md"},
		{name: "slot agent codex", toml: "[fleet.reviewers.A]\nagent = \"codex\"\n", wantMode: "fleet", wantCmd: "exec codex ", wantBody: "reviewer.md"},
		{name: "workers default codex", toml: "[fleet.workers]\ndefault = \"codex\"\n", wantMode: "fleet", wantCmd: "exec codex ", wantBody: "reviewer.md"},
		{name: "spec agent", specAgent: "sleep 30", wantMode: "fleet", wantCmd: "exec sleep 30", wantBody: "reviewer.md"},
		{name: "explicit code-review keeps claude", taskAgent: "codex", toml: "[fleet.reviewers.A]\nmode = \"code-review\"\n", wantMode: "code-review", wantCmd: "exec claude ", wantBody: "reviewer-code-review.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := newGitRepoFor(t, "demo-project")
			writeRoleTask(t, root, c.taskAgent)
			if c.toml != "" {
				writeSporeTOML(t, root, c.toml)
			}
			spec := ReviewerSpec(root, "feature-x", task.ReviewerA)
			spec.Agent = c.specAgent
			args, err := spec.TmuxArgs()
			if err != nil {
				t.Fatalf("TmuxArgs: %v", err)
			}
			if e := envValue(args, "SPORE_REVIEWER_MODE"); e != c.wantMode {
				t.Errorf("SPORE_REVIEWER_MODE = %q, want %q", e, c.wantMode)
			}
			if got := hasEnvArg(args, "SPORE_REVIEW_EFFORT"); got != (c.wantMode == "code-review") {
				t.Errorf("SPORE_REVIEW_EFFORT present = %v in mode %s", got, c.wantMode)
			}
			cmd := args[len(args)-1]
			if !strings.Contains(cmd, c.wantCmd) {
				t.Errorf("command = %q, want %q", cmd, c.wantCmd)
			}
			if !strings.Contains(cmd, string(filepath.Separator)+c.wantBody) {
				t.Errorf("command = %q, want body %s", cmd, c.wantBody)
			}
		})
	}
}

func TestRoleSpawnEffortIgnoredInFleetWarns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(task.CodexBinEnv, "")
	t.Setenv(task.CodexModelEnv, "")
	var warn strings.Builder
	defer func(w io.Writer) { roleWarnings = w }(roleWarnings)
	roleWarnings = &warn

	root := newGitRepoFor(t, "demo-project")
	writeRoleTask(t, root, "codex")
	writeSporeTOML(t, root, "[fleet.reviewers.A]\neffort = \"high\"\n[fleet.reviewers.B]\neffort = \"low\"\n")
	for _, inst := range []task.ReviewerInstance{task.ReviewerA, task.ReviewerB} {
		args, err := ReviewerSpec(root, "feature-x", inst).TmuxArgs()
		if err != nil {
			t.Fatalf("%s TmuxArgs: %v", inst, err)
		}
		if hasEnvArg(args, "SPORE_REVIEW_EFFORT") {
			t.Errorf("%s carries SPORE_REVIEW_EFFORT in fleet mode", inst)
		}
		if want := "[fleet.reviewers." + string(inst) + "] effort"; !strings.Contains(warn.String(), want) {
			t.Errorf("warnings = %q, want %q", warn.String(), want)
		}
	}
}

func TestRoleSpawnMixedRoleSources(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var warn strings.Builder
	defer func(w io.Writer) { roleWarnings = w }(roleWarnings)
	roleWarnings = &warn

	root := newGitRepoFor(t, "demo-project")
	writeRoleBodies(t, root)
	if err := os.Remove(filepath.Join(root, "bootstrap", "roles", "reviewer-code-review.md")); err != nil {
		t.Fatal(err)
	}
	argsA, err := ReviewerSpec(root, "feature-x", task.ReviewerA).TmuxArgs()
	if err != nil {
		t.Fatalf("A TmuxArgs: %v", err)
	}
	bundled, err := ResolveRoleBody(root, "reviewer-code-review.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(bundled, root) || !strings.Contains(argsA[len(argsA)-1], bundled) {
		t.Errorf("A command = %q, want bundled body %s", argsA[len(argsA)-1], bundled)
	}
	if !strings.Contains(warn.String(), "no reviewer-code-review.md") {
		t.Errorf("warnings = %q, want a mixed-source warning", warn.String())
	}
	argsB, err := ReviewerSpec(root, "feature-x", task.ReviewerB).TmuxArgs()
	if err != nil {
		t.Fatalf("B TmuxArgs: %v", err)
	}
	if want := filepath.Join(root, "bootstrap", "roles", "reviewer.md"); !strings.Contains(argsB[len(argsB)-1], want) {
		t.Errorf("B command = %q, want project body %s", argsB[len(argsB)-1], want)
	}
}

func TestRoleSpawnRejectsBadReviewerConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	writeRoleBodies(t, root)
	writeSporeTOML(t, root, "[fleet.reviewers.A]\nmode = \"code-review\"\nagent = \"codex\"\n")
	if _, err := ReviewerSpec(root, "feature-x", task.ReviewerA).TmuxArgs(); err == nil {
		t.Fatal("expected code-review + codex to be rejected at spawn")
	}
}

func TestSporeTOMLReviewerSlots(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReviewersConfig(root)
	if err != nil {
		t.Fatalf("spore.toml: %v", err)
	}
	if cfg.A.Mode != ReviewerModeCodeReview || cfg.B.Mode != ReviewerModeFleet {
		t.Errorf("spore.toml reviewers = %+v, want A code-review, B fleet", cfg)
	}
	if _, err := os.Stat(filepath.Join(root, "bootstrap", "roles", "reviewer-code-review.md")); err != nil {
		t.Errorf("code-review body: %v", err)
	}
}
