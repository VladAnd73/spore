package evalharness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// SpawnRequest is everything an AgentSpawner needs to start one
// disposable agent run. Prompt is passed as the first user message,
// the same shape production spawns a worker with: internal/task's
// lifecycle passes a task file's body as the agent's first positional
// arg, and internal/dream's MintTask builds that body from the
// embedded proposer brief. A headless spawn matches that shape with
// -p/--print instead of a tmux pane; see docs/todo/
// eval-harness-for-prompt-surfaces.md for why that substitution is
// faithful (in short: --print without --bare still loads hooks,
// skills, and CLAUDE.md the same way an interactive session would).
type SpawnRequest struct {
	Prompt  string
	WorkDir string
	Env     []string
	// ExtraArgs are inserted after ClaudeSpawner's fixed base args and
	// before Prompt. A dream scenario leaves this empty (text output is
	// enough to grade files on disk); a full-session scenario sets it to
	// request a parseable transcript (--output-format stream-json
	// --verbose), which SessionSpawnArgs names.
	ExtraArgs []string
}

// SpawnResult is what a finished agent run produced. Stdout is kept
// verbatim (not just a pass/fail) so a scenario report can show the
// actual transcript text as proof a real agent ran, not a stub.
type SpawnResult struct {
	Stdout   string
	ExitCode int
}

// AgentSpawner starts one agent run and waits for it to finish.
// Scenario grading depends only on this interface, so a fast unit test
// can inject a fake spawner while the CLI and the live acceptance
// tests use ClaudeSpawner to run a real `claude` process.
type AgentSpawner interface {
	Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error)
}

// ClaudeSpawner runs the real `claude` CLI in headless print mode.
// runCommand defaults to execCommand (a real subprocess); tests
// override it to capture the exact argv without starting a process.
type ClaudeSpawner struct {
	// Binary is the executable to run. Empty means "claude", the same
	// default internal/fleet and internal/task fall back to.
	Binary     string
	runCommand func(ctx context.Context, bin, dir string, env []string, args []string) (stdout string, exitCode int, err error)
}

func (s ClaudeSpawner) Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error) {
	bin := s.Binary
	if bin == "" {
		bin = "claude"
	}
	// bypassPermissions is required: an unattended run has no human to
	// answer a permission prompt, and every scenario already runs
	// inside a Sandbox with no remote and no push-capable token, which
	// is what makes bypassing safe here.
	// "--" ends flag parsing before the prompt: a worker-shaped task
	// body commonly starts with YAML frontmatter ("---\nstatus:
	// active\n..."), which claude's own CLI parser otherwise reads as
	// an unknown option and refuses to run. Production's real spawn
	// (internal/task/lifecycle.go) does the same; verified live against
	// a running coordinator's argv.
	//
	// --setting-sources project is required on every spawn, not just a
	// full-session one: this host's real ~/.claude/settings.json
	// registers a Stop hook chain (spore's own fleet/coordinator
	// inbox-watching machinery) that is unrelated to any eval fixture
	// but was confirmed live to keep a spawned "claude -p" process from
	// exiting for minutes after it had already produced its answer -
	// "spore hooks watch-inbox" running as its child process, still
	// alive, is the direct evidence. A dream (proposer/reviewer)
	// scenario sends no ExtraArgs and hit this every time before this
	// flag moved here; see docs/todo/eval-harness-for-prompt-surfaces.md,
	// "The Stop-hook hang".
	args := []string{"-p", "--permission-mode", "bypassPermissions", "--setting-sources", "project"}
	args = append(args, req.ExtraArgs...)
	args = append(args, "--", req.Prompt)
	run := s.runCommand
	if run == nil {
		run = execCommand
	}
	stdout, code, err := run(ctx, bin, req.WorkDir, req.Env, args)
	if err != nil {
		return SpawnResult{Stdout: stdout, ExitCode: code}, fmt.Errorf("evalharness: spawn %s: %w", bin, err)
	}
	return SpawnResult{Stdout: stdout, ExitCode: code}, nil
}

func execCommand(ctx context.Context, bin, dir string, env []string, args []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	// Separate buffers, not one shared writer: os/exec copies a
	// process's stdout and stderr pipes on two concurrent goroutines,
	// and two goroutines calling Write on the same bytes.Buffer race.
	// A dream scenario never noticed (it grades files on disk, not this
	// string), but a scenario that parses this text as line-delimited
	// JSON (see session.go's AskUserQuestionCalled) needs stdout never
	// interleaved with stderr noise mid-line.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	out := stdout.String()
	if stderr.Len() > 0 {
		out += "\n--- stderr ---\n" + stderr.String()
	}
	return out, code, err
}
