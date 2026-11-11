// Package statefile resolves per-project spore state paths and writes
// JSON to them atomically. The watch and dream subsystems both keep
// per-project state; sharing this keeps their path resolution identical.
package statefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Path returns the path to name inside project's spore state dir,
// honouring XDG_STATE_HOME and falling back to $HOME/.local/state.
func Path(project, name string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("neither XDG_STATE_HOME nor HOME set")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project, name), nil
}

// WriteJSONAtomic marshals v and renames it into place, so a crashed or
// overlapping run never leaves a half-written state file behind.
// tmpPrefix names the temp file for easy identification of a stray write.
func WriteJSONAtomic(path, tmpPrefix string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, "."+tmpPrefix+"-*.json", b)
}

// WriteFileAtomic writes b to a uniquely named temp file (os.CreateTemp
// pattern tmpPattern, mode 0600) beside path and renames it into place.
func WriteFileAtomic(path, tmpPattern string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tmpPattern)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
