package fleet

import (
	"os"
	"path/filepath"
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
		if wantPath := filepath.Join(state, "spore", "demo-project", "roles", name); got != wantPath {
			t.Errorf("%s path = %q, want %q", name, got, wantPath)
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
