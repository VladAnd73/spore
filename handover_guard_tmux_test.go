package spore

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runGuardTmux(t *testing.T, command string, env ...string) (denied bool, reason string) {
	t.Helper()
	in, _ := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	})
	cmd := exec.Command("perl", "bootstrap/handover/hooks/guard-tmux.pl")
	cmd.Stdin = strings.NewReader(string(in))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("guard-tmux.pl %q: %v", command, err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return false, ""
	}
	var resp struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	return resp.HookSpecificOutput.PermissionDecision == "deny", resp.HookSpecificOutput.PermissionDecisionReason
}

// A webapp worker spiked with `tmux new-session -d -s
// consoletest/scratch/coordinator` on the operator's real server. The
// session outlived the worker and its /coordinator suffix confused
// spore-attach. The guard makes agents name a private socket.
func TestGuardTmuxDeniesWorkerSessionsOnTheRealServer(t *testing.T) {
	worker := "SPORE_TASK_SLUG=console"
	cases := []struct {
		cmd  string
		env  []string
		deny bool
	}{
		{"cd x; tmux new-session -d -s consoletest/scratch/coordinator 'claude'", []string{worker}, true},
		{"tmux new -d -s probe", []string{worker}, true},
		{"tmux -L probe new-session -d -s x 'sleep 1'", []string{worker}, false},
		{"tmux -S /tmp/sock new-session -d", []string{worker}, false},
		{"tmux new-session -d -s scratch", nil, false},
		{"tmux new-window -d -n build 'make'", []string{worker}, false},
		{"tmux capture-pane -p -t spore/x/coordinator", []string{worker}, false},
		{"tmux kill-server", nil, true},
		{"echo hi && tmux kill-server", []string{worker}, true},
		{"tmux -L probe kill-server", nil, false},
		{"tmux -u kill-server", nil, true},
		{"echo tmux new-session is documented", []string{worker}, false},
		{"/usr/bin/tmux kill-server", nil, true},
		{"sudo tmux kill-server", nil, true},
		{"env tmux kill-server", nil, true},
		{`bash -c "tmux kill-server"`, nil, true},
		{"tmux kill-ser", nil, true},
		{"tmux new-s -d -s s", []string{worker}, true},
		{"tmux -Lx kill-server", nil, false},
		{"tmux kill-session -t x", nil, false},
		{"tmux new-window -d -n x", []string{worker}, false},
		{"if true; then tmux kill-server; fi", nil, true},
		{"(tmux kill-server)", nil, true},
		{"{ tmux kill-server; }", nil, true},
		{"timeout 5 tmux kill-server", nil, true},
		{"xargs tmux kill-server", nil, true},
		{"echo $(tmux kill-server)", nil, true},
		{"x=\"`tmux kill-server`\"", nil, true},
		{"sh -c 'tmux kill-server'", nil, true},
		{"sudo -u spore tmux kill-server", nil, true},
		{`echo "tmux kill-server"`, nil, false},
		{`git commit -m "never run tmux kill-server"`, nil, false},
		{"echo 'tmux new-session -d -s x'", []string{worker}, false},
		{"tmux -L x kill-server", nil, false},
		{"tmux -S /tmp/x kill-server", nil, false},
		{"tmux -uL x kill-server", nil, false},
		{"tmux -f /dev/null kill-server", nil, true},
	}
	for _, tc := range cases {
		denied, reason := runGuardTmux(t, tc.cmd, tc.env...)
		if denied != tc.deny {
			t.Errorf("%q env=%v: denied=%v want %v (%s)", tc.cmd, tc.env, denied, tc.deny, reason)
		}
		if denied && !strings.Contains(reason, "-L") {
			t.Errorf("%q: reason does not say how to fix it: %s", tc.cmd, reason)
		}
	}
}

func TestHandoverSettingsWireGuardTmux(t *testing.T) {
	raw, err := os.ReadFile("bootstrap/handover/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]handoverHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(settings.Hooks["PreToolUse"], "/home/spore/.claude/hooks/guard-tmux.pl") {
		t.Fatal("handover settings do not run guard-tmux.pl on PreToolUse")
	}
}
