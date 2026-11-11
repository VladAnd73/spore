package dream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/versality/spore/internal/statefile"
)

// RunInfo describes one run directory for a caller deciding what to
// list, revert, or prune.
type RunInfo struct {
	RunID      string
	When       time.Time
	Dated      string // "manifest created_at" or "directory mtime"
	Revertible bool
}

// ListRuns lists a project's run directories, newest first. The
// manifest's created_at is authoritative when present, but only a run
// that snapshotted has a manifest, so a run that never did falls back
// to its directory's own mtime and says which source it used.
func ListRuns(project string) ([]RunInfo, error) {
	dir, err := statefile.Path(project, "dreams")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RunInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r := RunInfo{RunID: e.Name(), Dated: "directory mtime"}
		if info, err := e.Info(); err == nil {
			r.When = info.ModTime()
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "manifest.json"))
		if err == nil {
			r.Revertible = true
			var m struct {
				CreatedAt string `json:"created_at"`
			}
			if json.Unmarshal(b, &m) == nil {
				if ts, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
					r.When, r.Dated = ts, "manifest created_at"
				}
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out, nil
}
