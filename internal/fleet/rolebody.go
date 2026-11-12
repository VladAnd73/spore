package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/task"
)

var roleWarnings io.Writer = os.Stderr

var warnedRole sync.Map

// warnRoleOnce reports msg once per process: the drive loop spawns
// every round, and a config warning is not news the second time.
func warnRoleOnce(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if _, dup := warnedRole.LoadOrStore(msg, true); !dup {
		fmt.Fprint(roleWarnings, msg)
	}
}

// ResolveRoleBody returns the path of role body name (e.g.
// "reviewer.md"): <projectRoot>/bootstrap/roles/<name> when present,
// else the bundled body materialized under
// <StateDirForProject>/roles/<sha>/<name>. The pane reads the body
// with cat at exec time, so the bundled copy must exist on disk; the
// content-hash directory keeps binaries of different releases
// spawning for one project from rewriting each other's body.
func ResolveRoleBody(projectRoot, name string) (string, error) {
	dir := filepath.Join(projectRoot, "bootstrap", "roles")
	project, err := projectRoleBody(projectRoot, name)
	if err != nil || project != "" {
		return project, err
	}
	body, err := fs.ReadFile(spore.BundledRoles, "bootstrap/roles/"+name)
	if err != nil {
		return "", fmt.Errorf("rolespawn: role body %s: not in %s and not bundled: %w", name, dir, err)
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		warnRoleOnce("rolespawn: %s has no %s; using the bundled body alongside project overrides\n", dir, name)
	}
	state, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	path := filepath.Join(state, "roles", hex.EncodeToString(sum[:6]), name)
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, body) {
		return path, nil
	}
	if err := writePrivateFile(path, string(body)); err != nil {
		return "", fmt.Errorf("rolespawn: materialize role body %s: %w", name, err)
	}
	return path, nil
}

// projectRoleBody returns the project override for name, or "" when
// a path component is absent or a parent is not a directory. A
// component that exists but is broken (dangling symlink, unreadable,
// a body that is not a regular file) is an error, not an absence:
// falling back would silently drop a customized body.
func projectRoleBody(projectRoot, name string) (string, error) {
	p := projectRoot
	parts := []string{"bootstrap", "roles", name}
	for i, part := range parts {
		p = filepath.Join(p, part)
		_, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("rolespawn: project role body %s: %w", p, err)
		}
		fi, err := os.Stat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("rolespawn: project role body %s is a dangling symlink", p)
		case err != nil:
			return "", fmt.Errorf("rolespawn: project role body %s: %w", p, err)
		case i < len(parts)-1 && !fi.IsDir():
			// e.g. an autotools-style ./bootstrap script: not an override.
			return "", nil
		case i == len(parts)-1 && !fi.Mode().IsRegular():
			return "", fmt.Errorf("rolespawn: project role body %s: not a regular file", p)
		}
	}
	return p, nil
}
