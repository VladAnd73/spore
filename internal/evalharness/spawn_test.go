package evalharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSpawnerBuildsAHeadlessBypassPermissionsCommand(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, bin, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{Prompt: "hello world", WorkDir: "/tmp/somewhere"}
	res, err := s.Spawn(context.Background(), req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Stdout != "ok" || res.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--print") && !strings.Contains(joined, "-p") {
		t.Fatalf("expected a print/headless flag in args, got %v", captured)
	}
	if !strings.Contains(joined, "bypassPermissions") {
		t.Fatalf("expected an unattended permission mode in args, got %v", captured)
	}
	if captured[len(captured)-1] != "hello world" {
		t.Fatalf("expected the prompt as the last arg, got %v", captured)
	}
}

// TestClaudeSpawnerAlwaysSkipsUserSettingsEvenWithNoExtraArgs guards
// against a real bug this task's live non-regression run hit: a dream
// scenario (proposer or reviewer) sends no ExtraArgs at all, so before
// this fix ClaudeSpawner never passed --setting-sources project, and
// this host's real ~/.claude/settings.json Stop hook chain (spore fleet
// replenish-hook, then spore hooks watch-inbox with a 7-day timeout)
// kept every one of 7 live scenario runs from exiting for over ten
// minutes after they had already finished - confirmed live by finding
// "spore hooks watch-inbox" running as a child of the stuck "claude -p"
// process. This is the same hang docs/todo/
// eval-harness-for-prompt-surfaces.md's "Stop-hook hang" section
// describes and fixed for phase 2's session scenarios only; dream
// scenarios need the identical flag regardless of ExtraArgs.
func TestClaudeSpawnerAlwaysSkipsUserSettingsEvenWithNoExtraArgs(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, bin, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{Prompt: "hello world", WorkDir: "/tmp/somewhere"}
	if _, err := s.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--setting-sources project") {
		t.Fatalf("expected --setting-sources project in argv even with no ExtraArgs, got %v", captured)
	}
}

// TestClaudeSpawnerIncludesExtraArgsBeforeThePrompt covers the
// full-session scenario's need for a flag ClaudeSpawner's fixed argv
// does not include (--output-format stream-json, to capture a parseable
// transcript). The prompt must still end up last: it is a positional
// arg, not a flag value.
func TestClaudeSpawnerIncludesExtraArgsBeforeThePrompt(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, bin, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{
		Prompt:    "hello world",
		WorkDir:   "/tmp/somewhere",
		ExtraArgs: []string{"--output-format", "stream-json", "--verbose"},
	}
	if _, err := s.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--output-format stream-json") {
		t.Fatalf("expected extra args in argv, got %v", captured)
	}
	if captured[len(captured)-1] != "hello world" {
		t.Fatalf("expected the prompt to remain the last arg, got %v", captured)
	}
}

// TestClaudeSpawnerSeparatesThePromptWithDashDash guards against a real
// bug this task's fixture hit live: a worker-shaped task body starts
// with YAML frontmatter ("---\nstatus: active\n..."), and without a
// bare "--" argument before it, claude's own CLI parser reads the
// leading "---" as an unknown option and exits before doing anything.
// Production's real spawn (internal/task/lifecycle.go, confirmed live
// by inspecting a running coordinator's argv) inserts exactly this "--"
// before the task body; ClaudeSpawner must match it.
func TestClaudeSpawnerSeparatesThePromptWithDashDash(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, bin, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{Prompt: "---\nstatus: active\n---\n\nbody", WorkDir: "/tmp/somewhere"}
	if _, err := s.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(captured) < 2 {
		t.Fatalf("expected at least a separator and the prompt, got %v", captured)
	}
	if captured[len(captured)-2] != "--" {
		t.Fatalf("expected \"--\" immediately before the prompt, got %v", captured)
	}
	if captured[len(captured)-1] != req.Prompt {
		t.Fatalf("expected the prompt as the last arg, got %v", captured)
	}
}

// Binary must reach the real exec, not only the error message: a caller
// pointing at a wrapper or a pinned build otherwise silently runs
// whatever `claude` PATH resolves to.
func TestClaudeSpawnerRunsTheConfiguredBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	bin := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"fake-agent ran: $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := ClaudeSpawner{Binary: bin}.Spawn(context.Background(),
		SpawnRequest{Prompt: "hello", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(res.Stdout, "fake-agent ran: -p ") || !strings.Contains(res.Stdout, "-- hello") {
		t.Fatalf("configured binary did not run with the spawn args: %q", res.Stdout)
	}
}
