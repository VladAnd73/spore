package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureHook runs fn with stdin piped from input and returns its exit
// code plus captured stdout and stderr.
func captureHook(t *testing.T, input string, fn func() int) (code int, stdout, stderr string) {
	t.Helper()
	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr })

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	inW.Write([]byte(input))
	inW.Close()
	os.Stdin = inR
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW

	var outBuf, errBuf bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&outBuf, outR); close(done) }()
	errDone := make(chan struct{})
	go func() { io.Copy(&errBuf, errR); close(errDone) }()

	code = fn()
	outW.Close()
	errW.Close()
	<-done
	<-errDone
	return code, outBuf.String(), errBuf.String()
}

// writeCodexRollout writes a rollout whose latest token_count reports
// ctx live context tokens and a much larger cumulative total.
func writeCodexRollout(t *testing.T, ctx int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "10", "21")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"timestamp":"2026-10-21T12:32:55.101Z","ordinal":0,"type":"session_meta","payload":{"id":"01a15e73-0000-7000-8000-000000000001","cwd":"/p","cli_version":"0.162.1"}}`,
		fmt.Sprintf(`{"timestamp":"2026-10-21T12:33:00.000Z","ordinal":4,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"output_tokens":0,"total_tokens":%d},"model_context_window":258400},"rate_limits":{}}}`, ctx*5, ctx, ctx),
	}
	path := filepath.Join(dir, "rollout-2026-10-21T12-32-55-01a15e73-0000-7000-8000-000000000001.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// codexStopPayload is the live codex-cli 0.162.1 Stop shape.
func codexStopPayload(transcript string) string {
	b, _ := json.Marshal(map[string]any{
		"session_id":             "01a15e73-0000-7000-8000-000000000001",
		"turn_id":                "turn-1",
		"transcript_path":        transcript,
		"cwd":                    "/p",
		"hook_event_name":        "Stop",
		"model":                  "gpt-5.5",
		"permission_mode":        "bypassPermissions",
		"stop_hook_active":       false,
		"last_assistant_message": "done",
	})
	return string(b)
}

// Codex treats exit 2 as a block only when stderr is non-empty; empty
// stderr counts as a failed hook.
func assertCodexBlock(t *testing.T, code int, stderr, want string) {
	t.Helper()
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", code, stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("exit 2 with empty stderr: codex would treat it as a hook failure, not a block")
	}
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestCoordinatorTokenMonitorCodexStopBlocks(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(state, "proj", "inbox"))
	path := writeCodexRollout(t, 295000)

	code, _, stderr := captureHook(t, codexStopPayload(path), func() int { return runCoordinatorTokenMonitor(nil) })
	assertCodexBlock(t, code, stderr, "context 295000 tokens")
}

func TestCoordinatorTokenMonitorCodexBelowCap(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(state, "proj", "inbox"))
	path := writeCodexRollout(t, 50000)

	code, _, stderr := captureHook(t, codexStopPayload(path), func() int { return runCoordinatorTokenMonitor(nil) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0 below cap (cumulative total must not count); stderr=%q", code, stderr)
	}
}

func TestWorkerTokenMonitorCodexStopBlocks(t *testing.T) {
	clearRoleEnv(t)
	dir := t.TempDir()
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(dir, "workers", "codexw", "inbox"))
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", filepath.Join(dir, "coord"))
	t.Setenv("SPORE_ACCOUNT_TIER", "max")
	path := writeCodexRollout(t, 290000)

	code, _, stderr := captureHook(t, codexStopPayload(path), func() int { return runWorkerTokenMonitor(nil) })
	assertCodexBlock(t, code, stderr, "WORKER TOKEN MONITOR")
}

func TestTokenMonitorCodexCompressedRolloutSkips(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(state, "proj", "inbox"))
	path := writeCodexRollout(t, 295000)
	zst := path + ".zst"
	if err := os.Rename(path, zst); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := captureHook(t, codexStopPayload(zst), func() int { return runCoordinatorTokenMonitor(nil) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a compressed rollout; stderr=%q", code, stderr)
	}
}

func TestHooksStopToleratesCodexPayload(t *testing.T) {
	code, stdout, stderr := captureHook(t, codexStopPayload("/nope.jsonl"), runHooksStop)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.TrimSpace(stdout) != "{}" {
		t.Errorf("stdout = %q, want {}", stdout)
	}
}

func TestHooksPreToolUseToleratesCodexPayload(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"session_id":      "01a15e73-0000-7000-8000-000000000001",
		"turn_id":         "turn-1",
		"transcript_path": nil,
		"cwd":             "/p",
		"hook_event_name": "PreToolUse",
		"model":           "gpt-5.5",
		"permission_mode": "bypassPermissions",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": "git push --force origin main"},
		"tool_use_id":     "call_1",
		"agent_id":        nil,
		"agent_type":      nil,
	})
	code, stdout, stderr := captureHook(t, string(b), runHooksPreToolUse)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Errorf("stdout = %q, want a deny decision", stdout)
	}
}

// noCodexContext points the SessionStart context lookup at empty dirs
// so the host's real state.md and Claude memory stay out of stdout.
func noCodexContext(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("SPORE_PROJECT_ROOT", root)
	t.Setenv("SPORE_TASK_SLUG", "")
	return root
}

func TestHooksCodexSessionStartRecordsThread(t *testing.T) {
	noCodexContext(t)
	inbox := filepath.Join(t.TempDir(), "coordinator", "proj", "inbox")
	t.Setenv("SPORE_TASK_INBOX", inbox)
	t.Setenv("SPORE_COORDINATOR_SESSION", "spore/proj/coordinator")
	payload := `{"session_id":"thread-9","transcript_path":null,"cwd":"/p","hook_event_name":"SessionStart","model":"gpt-5.5","permission_mode":"bypassPermissions","source":"startup"}`

	code, stdout, stderr := captureHook(t, payload, runHooksCodexSessionStart)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want silent 0", code, stdout, stderr)
	}
	got, err := os.ReadFile(filepath.Join(filepath.Dir(inbox), "codex-threads", "spore_proj_coordinator.json"))
	if err != nil || !strings.Contains(string(got), `"thread_id":"thread-9"`) {
		t.Fatalf("thread record = %q, %v", got, err)
	}
}

func TestHooksCodexSessionStartNonSporeSessionNoOp(t *testing.T) {
	noCodexContext(t)
	t.Setenv("SPORE_TASK_INBOX", "")
	code, stdout, stderr := captureHook(t, `{"session_id":"x"}`, runHooksCodexSessionStart)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want silent 0", code, stdout, stderr)
	}
}

func TestHooksCodexSessionStartInjectsStateAndRecordsThread(t *testing.T) {
	root := noCodexContext(t)
	if err := os.WriteFile(filepath.Join(root, "state.md"), []byte("STATE-BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(t.TempDir(), "coordinator", "proj", "inbox")
	t.Setenv("SPORE_TASK_INBOX", inbox)
	t.Setenv("SPORE_COORDINATOR_SESSION", "spore/proj/coordinator")
	t.Setenv("SPORE_TASK_SLUG", "coordinator")
	code, stdout, stderr := captureHook(t, `{"session_id":"thread-7","hook_event_name":"SessionStart","source":"startup"}`, runHooksCodexSessionStart)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(out.HookSpecificOutput.AdditionalContext, "STATE-BODY") {
		t.Errorf("stdout = %s, want SessionStart context carrying state.md", stdout)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(inbox), "codex-threads", "spore_proj_coordinator.json")); err != nil {
		t.Errorf("thread record not written: %v", err)
	}
}

func TestHooksWatchInboxCodexNeedsInbox(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")
	code, _, stderr := captureHook(t, "", func() int { return runHooksWatchInbox([]string{"--codex"}) })
	if code != 2 || !strings.Contains(stderr, "SPORE_TASK_INBOX") {
		t.Fatalf("code=%d stderr=%q, want usage exit 2", code, stderr)
	}
}
