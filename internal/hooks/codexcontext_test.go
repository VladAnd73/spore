package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/transcript"
)

// contextFixture builds a main checkout with state.md, a linked
// worktree, and a Claude memory dir keyed by the main root. It returns
// (main root, worktree path).
func contextFixture(t *testing.T) (string, string) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(tmp, "my_proj")
	wt := filepath.Join(main, ".worktrees", "slug-a")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run(main, "init", "-q")
	run(main, "commit", "-q", "--allow-empty", "-m", "init")
	run(main, "worktree", "add", "-q", wt)
	if err := os.WriteFile(filepath.Join(main, "state.md"), []byte("STATE-BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	mem := filepath.Join(home, ".claude", "projects", transcript.ClaudeProjectKey(main), "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Rule](rule.md) - MEMORY-LINE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, wt
}

func TestCodexStartContextCoordinatorFromWorktree(t *testing.T) {
	main, wt := contextFixture(t)
	t.Setenv("SPORE_PROJECT_ROOT", wt)
	t.Setenv("SPORE_TASK_SLUG", "coordinator")
	got := CodexStartContext()
	if !strings.Contains(got, "STATE-BODY") || !strings.Contains(got, filepath.Join(main, "state.md")) {
		t.Errorf("context missing main-root state.md:\n%s", got)
	}
	if !strings.Contains(got, "MEMORY-LINE") {
		t.Errorf("context missing memory index:\n%s", got)
	}
	t.Setenv("SPORE_TASK_SLUG", "")
	if !strings.Contains(CodexStartContext(), "STATE-BODY") {
		t.Error("unset slug (operator session) must load state.md")
	}
}

func TestCodexStartContextWorkerGate(t *testing.T) {
	_, wt := contextFixture(t)
	t.Setenv("SPORE_PROJECT_ROOT", wt)
	t.Setenv("SPORE_TASK_SLUG", "slug-a")
	got := CodexStartContext()
	if strings.Contains(got, "STATE-BODY") {
		t.Errorf("worker saw state.md:\n%s", got)
	}
	if !strings.Contains(got, "MEMORY-LINE") {
		t.Errorf("worker must still get the memory index, as claude-code does:\n%s", got)
	}
}

func TestCodexStartContextEmptyOutsideSpore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("SPORE_PROJECT_ROOT", t.TempDir())
	t.Setenv("SPORE_TASK_SLUG", "")
	if got := CodexStartContext(); got != "" {
		t.Errorf("context = %q, want empty", got)
	}
	if out := CodexStartOutput(""); out != nil {
		t.Errorf("empty context must print nothing, got %q", out)
	}
}

// codexStartWire mirrors codex-rs hooks/src/schema.rs
// SessionStartCommandOutputWire: deny_unknown_fields at both levels.
type codexStartWire struct {
	Continue           *bool   `json:"continue"`
	StopReason         *string `json:"stopReason"`
	SuppressOutput     *bool   `json:"suppressOutput"`
	SystemMessage      *string `json:"systemMessage"`
	HookSpecificOutput *struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func TestCodexStartOutputShape(t *testing.T) {
	big := strings.Repeat("x", 40000)
	dec := json.NewDecoder(bytes.NewReader(CodexStartOutput(big)))
	dec.DisallowUnknownFields()
	var w codexStartWire
	if err := dec.Decode(&w); err != nil {
		t.Fatal(err)
	}
	if w.HookSpecificOutput == nil || w.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookSpecificOutput = %+v", w.HookSpecificOutput)
	}
	if w.HookSpecificOutput.AdditionalContext != big {
		t.Error("context was truncated")
	}
}

func TestStateSizeWarningCodexAndPerlHook(t *testing.T) {
	main, wt := contextFixture(t)
	t.Setenv("SPORE_PROJECT_ROOT", wt)
	t.Setenv("SPORE_TASK_SLUG", "coordinator")
	if got := CodexStartContext(); strings.Contains(got, "WARNING: state.md") {
		t.Fatalf("small state.md must not warn:\n%s", got)
	}
	big := "STATE-BODY\n" + strings.Repeat("x", 9000) + "\n"
	if err := os.WriteFile(filepath.Join(main, "state.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "WARNING: state.md is 9012 bytes, over the 8192-byte SessionStart budget"
	if got := CodexStartContext(); !strings.HasPrefix(got, want) || !strings.Contains(got, "STATE-BODY") {
		t.Fatalf("codex context should lead with the size warning:\n%.300s", got)
	}

	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not on PATH")
	}
	hook, err := filepath.Abs("../../bootstrap/handover/hooks/load-state-md.pl")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		warn bool
	}{{"STATE-BODY\n", false}, {big, true}} {
		if err := os.WriteFile(filepath.Join(main, "state.md"), []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("perl", hook)
		cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+wt, "SPORE_TASK_SLUG=coordinator")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hook: %v", err)
		}
		var res struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("hook output %q: %v", out, err)
		}
		ctx := res.HookSpecificOutput.AdditionalContext
		if tc.warn {
			if !strings.HasPrefix(ctx, want) || !strings.HasSuffix(ctx, tc.body) {
				t.Fatalf("perl hook should prepend the warning:\n%.300s", ctx)
			}
		} else if ctx != tc.body {
			t.Fatalf("perl hook changed a small state.md: %q", ctx)
		}
	}
}
