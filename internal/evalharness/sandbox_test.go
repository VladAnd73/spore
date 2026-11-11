package evalharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSandboxCreatesAGitRepoWithNoRemote(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sb.Root, ".git")); err != nil {
		t.Fatalf("expected %s to be a git repo: %v", sb.Root, err)
	}
	out, err := exec.Command("git", "-C", sb.Root, "remote").Output()
	if err != nil {
		t.Fatalf("git remote: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected no git remotes in the sandbox, got %q", out)
	}
}

func TestNewSandboxLaysOutDreamDirectories(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	for _, dir := range []string{sb.Projects, sb.Home, sb.StateDir, sb.TasksDir, sb.RunWD} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected %s to exist: %v", dir, err)
		}
	}
	// TasksDir must satisfy dream.MintTask's ownership check: its parent
	// directory's basename must equal the project name.
	if got, want := filepath.Base(filepath.Dir(sb.TasksDir)), sb.Project; got != want {
		t.Fatalf("tasks dir %s: parent basename = %q, want project %q", sb.TasksDir, got, want)
	}
}

func TestSandboxEnvExcludesGitHubTokensAndSetsIsolatedState(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	env := sb.Env()
	for _, kv := range env {
		if strings.HasPrefix(kv, "GH_TOKEN=") || strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			t.Fatalf("sandbox env leaked a GitHub token: %q", kv)
		}
	}
	found := false
	for _, kv := range env {
		if kv == "XDG_STATE_HOME="+sb.StateDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected XDG_STATE_HOME=%s in sandbox env, got %v", sb.StateDir, env)
	}
}
