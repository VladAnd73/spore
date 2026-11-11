// Package project scaffolds a new spore-managed project directory so a
// coordinator can run in it without hand setup.
package project

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/versality/spore/internal/install"
)

// validName mirrors valid_name in bootstrap/handover/spore-attach.sh:
// a name the attach shell refuses can never be reached over ssh. No "."
// (tmux rewrites it to "_" in session names) and no leading "-" (ssh
// would read it as an option).
var validName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

type Result struct {
	Root     string
	Name     string
	Created  []string
	HostStep string
	// NoGitIdentity is true when git has no user.email for Root, so the
	// first commit (by the operator or an agent) would fail.
	NoGitIdentity bool
}

// New makes dir a spore project: git repo, bundled skills, coordinator
// role, and a .gitignore that hides worktrees. Existing files are kept,
// so re-running it on a half-set-up project fills only the gaps.
func New(dir string, skills embed.FS, role []byte) (Result, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return Result{}, err
	}
	res := Result{Root: root, Name: filepath.Base(root)}
	if !validName.MatchString(res.Name) {
		return res, fmt.Errorf("project name %q: use letters, digits, _ - (no dots, no leading -)", res.Name)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return res, err
	}

	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		if out, err := exec.Command("git", "-C", root, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
			return res, fmt.Errorf("git init: %v: %s", err, out)
		}
		res.Created = append(res.Created, ".git")
	}

	ins, err := install.Install(root, skills, "bootstrap/skills")
	if err != nil {
		return res, err
	}
	for _, p := range ins.Written {
		rel, _ := filepath.Rel(root, p)
		res.Created = append(res.Created, rel)
	}

	rolePath := filepath.Join(root, "bootstrap", "coordinator", "role.md")
	if _, err := os.Stat(rolePath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(rolePath), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(rolePath, role, 0o644); err != nil {
			return res, err
		}
		res.Created = append(res.Created, "bootstrap/coordinator/role.md")
	}

	added, err := ensureIgnored(filepath.Join(root, ".gitignore"), ".worktrees/")
	if err != nil {
		return res, err
	}
	if added {
		res.Created = append(res.Created, ".gitignore")
	}

	email, _ := exec.Command("git", "-C", root, "config", "user.email").Output()
	res.NoGitIdentity = strings.TrimSpace(string(email)) == ""

	res.HostStep = hostStep(res.Name, root)
	return res, nil
}

func ensureIgnored(path, line string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return true, os.WriteFile(path, []byte(s+line+"\n"), 0o644)
}

func hostStep(name, root string) string {
	return fmt.Sprintf(`Once the project has a build file (go.mod, package.json, flake.nix, ...),
write the agent instructions (CLAUDE.md, AGENTS.md):
  cd %[3]s && spore bootstrap

Register the project with the host so its coordinator starts on boot.
As root on the host:
  1. In the host flake's spore-projects.nix (/etc/nixos after spore infect),
     add this line inside the { }:
       "%[1]s".path = "%[2]s";
  2. Apply it:
       nixos-rebuild switch --flake /etc/nixos
     The switch drains every project's running workers, and kills the
     ones still running at the timeout. Check spore fleet status first.
Until then, start the coordinator by hand:
  cd %[3]s && spore coordinator start
`, name, nixString(root), shellQuote(root))
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote leaves a plain path bare so the common case reads cleanly.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nixString escapes s for the inside of a Nix "..." string.
func nixString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "${", `\${`).Replace(s)
}
