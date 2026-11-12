package fleet

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	spore "github.com/versality/spore"
)

func TestResolveRoleBodyProjectOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	writeRoleBodies(t, root)
	got, err := ResolveRoleBody(root, "reviewer.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "bootstrap", "roles", "reviewer.md"); got != want {
		t.Errorf("path = %q, want project file %q", got, want)
	}
}

func TestResolveRoleBodyBundled(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := newGitRepoFor(t, "demo-project")
	for _, name := range []string{"engineer.md", "reviewer.md", "reviewer-code-review.md"} {
		want, err := spore.BundledRoles.ReadFile("bootstrap/roles/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ResolveRoleBody(root, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dir := filepath.Join(state, "spore", "demo-project", "roles"); filepath.Dir(filepath.Dir(got)) != dir || filepath.Base(got) != name {
			t.Errorf("%s path = %q, want %s/<sha>/%s", name, got, dir, name)
		}
		b, err := os.ReadFile(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(want) {
			t.Errorf("%s content differs from the bundled body", name)
		}

		if err := os.WriteFile(got, []byte("stale\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRoleBody(root, name); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(got); string(b) != string(want) {
			t.Errorf("%s: stale copy not refreshed", name)
		}
	}
}

func TestResolveRoleBodyUnknown(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	if _, err := ResolveRoleBody(root, "qa.md"); err == nil {
		t.Fatal("expected error for a body in neither source")
	}
}

func TestResolveRoleBodyPathTracksContent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	eng, err := ResolveRoleBody(root, "engineer.md")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := ResolveRoleBody(root, "reviewer.md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(eng) == filepath.Dir(rev) {
		t.Errorf("bodies with different content share dir %s", filepath.Dir(eng))
	}
}

func TestResolveRoleBodyBrokenOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	dir := filepath.Join(root, "bootstrap", "roles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone.md"), filepath.Join(dir, "reviewer.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoleBody(root, "reviewer.md"); err == nil || !strings.Contains(err.Error(), "dangling") {
		t.Errorf("dangling override: err = %v, want dangling symlink error", err)
	}
	if err := os.Symlink("engineer.md", filepath.Join(dir, "engineer.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoleBody(root, "engineer.md"); err == nil {
		t.Error("looping override: want error, got bundled fallback")
	}
}

func TestResolveRoleBodyDanglingDirSymlink(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	if err := os.MkdirAll(filepath.Join(root, "bootstrap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "moved-checkout", "roles"), filepath.Join(root, "bootstrap", "roles")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoleBody(root, "reviewer.md"); err == nil || !strings.Contains(err.Error(), "dangling") {
		t.Errorf("err = %v, want dangling symlink error for the roles dir", err)
	}
}

func TestResolveRoleBodyNotRegular(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	if err := os.MkdirAll(filepath.Join(root, "bootstrap", "roles", "reviewer.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoleBody(root, "reviewer.md"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("err = %v, want not a regular file", err)
	}
}

func TestWarnRoleOnce(t *testing.T) {
	var warn strings.Builder
	defer func(w io.Writer) { roleWarnings = w }(roleWarnings)
	roleWarnings = &warn
	msg := "rolespawn: " + t.Name() + "\n"
	warnRoleOnce("%s", msg)
	warnRoleOnce("%s", msg)
	if warn.String() != msg {
		t.Errorf("warnings = %q, want one %q", warn.String(), msg)
	}
}

func TestResolveRoleBodyBootstrapScript(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := newGitRepoFor(t, "demo-project")
	if err := os.WriteFile(filepath.Join(root, "bootstrap"), []byte("#!/bin/sh\nautoreconf -i\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRoleBody(root, "reviewer.md")
	if err != nil {
		t.Fatalf("bootstrap script blocked the bundled fallback: %v", err)
	}
	if strings.HasPrefix(got, root) {
		t.Errorf("path = %q, want the bundled body", got)
	}
	if _, err := EngineerSpec(root, "feature-x").TmuxArgs(); err != nil {
		t.Errorf("engineer TmuxArgs: %v", err)
	}
}
