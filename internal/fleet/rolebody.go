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

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/task"
)

// roleWarnings receives non-fatal role spawn warnings.
var roleWarnings io.Writer = os.Stderr

// ResolveRoleBody returns the path of role body name (e.g.
// "reviewer.md"): <projectRoot>/bootstrap/roles/<name> when present,
// else the bundled body materialized under
// <StateDirForProject>/roles/<sha>/<name>. The pane reads the body
// with cat at exec time, so the bundled copy must exist on disk; the
// content-hash directory keeps binaries of different releases
// spawning for one project from rewriting each other's body.
func ResolveRoleBody(projectRoot, name string) (string, error) {
	dir := filepath.Join(projectRoot, "bootstrap", "roles")
	project := filepath.Join(dir, name)
	_, err := os.Stat(project)
	switch {
	case err == nil:
		return project, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("rolespawn: project role body %s: %w", project, err)
	}
	// A dangling override is a broken customization, not an absent one.
	if _, err := os.Lstat(project); err == nil {
		return "", fmt.Errorf("rolespawn: project role body %s is a dangling symlink", project)
	}
	body, err := fs.ReadFile(spore.BundledRoles, "bootstrap/roles/"+name)
	if err != nil {
		return "", fmt.Errorf("rolespawn: role body %s: not in %s and not bundled: %w", name, dir, err)
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		fmt.Fprintf(roleWarnings, "rolespawn: %s has no %s; using the bundled body alongside project overrides\n", dir, name)
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
