package fleet

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/task"
)

// ResolveRoleBody returns the path of role body name (e.g.
// "reviewer.md"): <projectRoot>/bootstrap/roles/<name> when present,
// else the bundled body materialized under
// <StateDirForProject>/roles/<name>. The pane reads the body with
// cat, so the bundled copy must exist on disk; it is rewritten only
// when its content differs from the binary's.
func ResolveRoleBody(projectRoot, name string) (string, error) {
	project := filepath.Join(projectRoot, "bootstrap", "roles", name)
	if _, err := os.Stat(project); err == nil {
		return project, nil
	}
	body, err := fs.ReadFile(spore.BundledRoles, "bootstrap/roles/"+name)
	if err != nil {
		return "", fmt.Errorf("rolespawn: role body %s: not in %s and not bundled: %w", name, filepath.Dir(project), err)
	}
	state, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	path := filepath.Join(state, "roles", name)
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, body) {
		return path, nil
	}
	if err := writePrivateFile(path, string(body)); err != nil {
		return "", fmt.Errorf("rolespawn: materialize role body %s: %w", name, err)
	}
	return path, nil
}
