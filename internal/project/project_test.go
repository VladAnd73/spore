package project

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	spore "github.com/versality/spore"
)

func TestNewScaffoldsAFreshDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")

	res, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("no git repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "spore-bootstrap", "SKILL.md")); err != nil {
		t.Errorf("skills not installed: %v", err)
	}
	role, err := os.ReadFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"))
	if err != nil {
		t.Fatalf("role.md: %v", err)
	}
	if string(role) != string(spore.BundledCoordinatorRole) {
		t.Errorf("role.md does not match the bundled role")
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore: %v", err)
	}
	if !hasLine(string(ignore), ".worktrees/") {
		t.Errorf(".gitignore lacks .worktrees/: %q", ignore)
	}
	out, err := exec.Command("git", "-C", dir, "check-ignore", "-q", ".worktrees/x/file").CombinedOutput()
	if err != nil {
		t.Errorf("git does not ignore .worktrees/: %v %s", err, out)
	}

	if res.Name != "demo" {
		t.Errorf("Name = %q, want demo", res.Name)
	}
	want := `"demo".path = "` + dir + `";`
	if !strings.Contains(res.HostStep, want) {
		t.Errorf("HostStep lacks %q:\n%s", want, res.HostStep)
	}
	if !strings.Contains(res.HostStep, "spore bootstrap") {
		t.Errorf("HostStep does not point at spore bootstrap, which writes CLAUDE.md:\n%s", res.HostStep)
	}
	if !strings.Contains(res.HostStep, "nixos-rebuild switch --flake") {
		t.Errorf("HostStep lacks the rebuild command:\n%s", res.HostStep)
	}
	// The graceful pre-switch hook drains, then kills, every project's
	// workers; the installer used `boot` to dodge it, unwarned.
	if !strings.Contains(res.HostStep, "drains") || !strings.Contains(res.HostStep, "spore fleet status") {
		t.Errorf("HostStep does not warn that switch drains running workers:\n%s", res.HostStep)
	}
}

func TestNewKeepsExistingFilesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	custom := []byte("# my own role\n")
	if err := os.MkdirAll(filepath.Join(dir, "bootstrap", "coordinator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/bin/"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if _, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole); err != nil {
			t.Fatalf("New #%d: %v", i, err)
		}
	}

	role, _ := os.ReadFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"))
	if string(role) != string(custom) {
		t.Errorf("custom role.md overwritten: %q", role)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(ignore) != "/bin/\n.worktrees/\n" {
		t.Errorf(".gitignore = %q", ignore)
	}
}

// tmux rewrites "." to "_" in session names, so spore/my.app/coordinator
// can never be found again; a leading "-" reads as an ssh option.
func TestNewRejectsNamesTheAttachShellCannotTarget(t *testing.T) {
	for _, name := range []string{"has space", "my.app", "-x"} {
		dir := filepath.Join(t.TempDir(), name)
		if _, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole); err == nil {
			t.Errorf("New accepted project name %q", name)
		}
	}
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// The host step is copy-pasted into a shell and into spore-projects.nix,
// so a path with a space, a quote or ${ must come out quoted for both.
func TestNewHostStepQuotesAnOddPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `my "odd" ${dir} \x`, "proj")
	res, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole)
	if err != nil {
		t.Fatal(err)
	}
	var cds, nixLines int
	for _, line := range strings.Split(res.HostStep, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "cd "):
			cds++
			if out, err := exec.Command("bash", "-n", "-c", line).CombinedOutput(); err != nil {
				t.Errorf("bash -n %q: %v: %s", line, err, out)
				continue
			}
			script := `cd() { printf '%s' "$1"; }; spore() { :; }; ` + line
			out, err := exec.Command("bash", "-c", script).Output()
			if err != nil || string(out) != res.Root {
				t.Errorf("%q cds to %q, want %q (%v)", line, out, res.Root, err)
			}
		case strings.Contains(line, ".path = "):
			nixLines++
			if _, err := exec.LookPath("nix-instantiate"); err != nil {
				continue
			}
			expr := "{ " + line + ` }."proj".path`
			out, err := exec.Command("nix-instantiate", "--eval", "--json", "-E", expr).CombinedOutput()
			if err != nil {
				t.Errorf("nix-instantiate %q: %v: %s", expr, err, out)
				continue
			}
			var got string
			if err := json.Unmarshal(out, &got); err != nil || got != res.Root {
				t.Errorf("nix line %q evaluates to %s, want %q", line, out, res.Root)
			}
		}
	}
	if cds == 0 || nixLines == 0 {
		t.Fatalf("host step has %d cd lines and %d nix lines:\n%s", cds, nixLines, res.HostStep)
	}
}

// spore-projects.nix is an attrset; a line appended after its closing
// brace is a Nix syntax error, and the switch fails.
func TestNewHostStepSaysTheLineGoesInsideTheBraces(t *testing.T) {
	res, err := New(filepath.Join(t.TempDir(), "demo"), spore.BundledSkills, spore.BundledCoordinatorRole)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HostStep, "inside the { }") {
		t.Errorf("HostStep does not say where in spore-projects.nix the line goes:\n%s", res.HostStep)
	}
}
