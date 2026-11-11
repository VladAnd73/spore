// Package evalharness runs the dream pipeline's proposer and reviewer
// briefs against fixture scenarios through a real, disposable Claude
// Code process, and grades the result against a known-correct verdict.
//
// Every scenario runs inside a Sandbox: a throwaway directory tree that
// stands in for a project checkout, a session corpus, and spore's own
// state directory, so a run can never write to this repository's real
// git history, tasks queue, or dream ledger.
package evalharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Project is the fixed project name every sandbox uses. dream.Run
// filters sessions by this name and dream.MintTask refuses a tasks
// directory whose parent basename does not match it, so every sandbox
// path that carries the project name has to agree on this one string.
const Project = "evalproj"

// Sandbox is one throwaway scratch tree for a single scenario run.
// Nothing under Root existed before NewSandbox and nothing outside it
// is touched: Root is git-initialized with no remote, so even a
// scenario that tries to push has nowhere to push to, and StateDir
// gives dream's ledger and watermark files a home that is never the
// real project's own state directory.
type Sandbox struct {
	Root     string
	Project  string
	Projects string // dream.Options.ProjectsRoot: the fixture session corpus
	Home     string // dream.Options.Home: session cwd is resolved against this
	TasksDir string // dream.Options.TasksDir: <Root>/<Project>/tasks
	StateDir string // XDG_STATE_HOME: ledger, watermark, backups land here
	RunWD    string // working directory handed to a spawned agent
}

// NewSandbox creates a fresh sandbox under parent, which must already
// exist (a caller-owned scratch directory, e.g. t.TempDir() in a test
// or a mkdtemp'd directory from the CLI).
func NewSandbox(parent string) (*Sandbox, error) {
	root, err := os.MkdirTemp(parent, "evalharness-")
	if err != nil {
		return nil, fmt.Errorf("evalharness: sandbox: %w", err)
	}
	sb := &Sandbox{
		Root:     root,
		Project:  Project,
		Projects: filepath.Join(root, "projects"),
		Home:     filepath.Join(root, "home"),
		TasksDir: filepath.Join(root, Project, "tasks"),
		StateDir: filepath.Join(root, "state"),
		RunWD:    filepath.Join(root, "run"),
	}
	for _, dir := range []string{sb.Projects, sb.Home, sb.TasksDir, sb.StateDir, sb.RunWD} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("evalharness: sandbox: %w", err)
		}
	}
	if err := gitInitNoRemote(root); err != nil {
		return nil, err
	}
	return sb, nil
}

// gitInitNoRemote makes root a git repository with no origin. Several
// dream briefs and lint checks expect to be inside a git repo (they run
// `git rev-parse --git-common-dir` to resolve a target path); a sandbox
// that is not a repo at all would make that command fail and derail the
// scenario for a reason that has nothing to do with what is being
// graded. The repo is deliberately never given a remote, so `git push`
// has nowhere to go and `git remote` always reports nothing.
func gitInitNoRemote(root string) error {
	cmd := exec.Command("git", "init", "--quiet", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("evalharness: git init: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cmd = exec.Command("git", "-C", root, "config", "user.email", "evalharness@localhost")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("evalharness: git config: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cmd = exec.Command("git", "-C", root, "config", "user.name", "eval harness")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("evalharness: git config: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deniedEnvPrefixes names environment variables that would let a
// sandboxed run reach a real, non-fixture system: a GitHub token could
// open a PR, and either GitHub token variable would do it (see
// bootstrap/recipes/github.md on the two not being interchangeable, so
// both are denied rather than trusting one name).
var deniedEnvPrefixes = []string{"GH_TOKEN=", "GITHUB_TOKEN="}

// Env returns the environment a spawned agent process should run with:
// the host's own environment (so Claude Code's own auth, on this
// machine backed by ~/.claude, keeps working), minus anything that
// could push or open a PR against a real remote, plus XDG_STATE_HOME
// pointed at this sandbox so dream's ledger and watermark files never
// touch the real project's state directory.
func (sb *Sandbox) Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		denied := false
		for _, prefix := range deniedEnvPrefixes {
			if strings.HasPrefix(kv, prefix) {
				denied = true
				break
			}
		}
		if !denied {
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_STATE_HOME="+sb.StateDir)
	return env
}
