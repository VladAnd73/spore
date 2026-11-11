package dream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/versality/spore/internal/statefile"
)

// A dream run writes into files that hold private notes, so the run
// directory and every backup copy stay owner-only regardless of the mode
// of the original.
const (
	runDirMode     fs.FileMode = 0o700
	backupCopyMode fs.FileMode = 0o600
)

type fileEntry struct {
	Original   string `json:"original"`
	Backup     string `json:"backup,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
	Absent     bool   `json:"absent,omitempty"`
	SealHash   string `json:"seal_hash,omitempty"`
	SealAbsent bool   `json:"seal_absent,omitempty"`
}

type dirEntry struct {
	Path   string      `json:"path"`
	Absent bool        `json:"absent,omitempty"`
	Before []string    `json:"before"`
	After  []string    `json:"after,omitempty"`
	Files  []fileEntry `json:"files,omitempty"`
}

type runManifest struct {
	CreatedAt string      `json:"created_at"`
	Sealed    bool        `json:"sealed,omitempty"`
	Files     []fileEntry `json:"files,omitempty"`
	Dirs      []dirEntry  `json:"dirs,omitempty"`
}

// SkippedPath is a path revert deliberately left alone.
type SkippedPath struct {
	Path   string
	Reason string
}

// FailedPath is a path revert tried to restore and could not.
type FailedPath struct {
	Path string
	Err  string
}

// RevertReport says what happened to every path in the manifest. A caller
// that only reads the error cannot tell a fully restored tree from a
// half-restored one, which is the whole question after a bad night.
type RevertReport struct {
	Restored []string
	Removed  []string
	Skipped  []SkippedPath
	Failed   []FailedPath
}

func manifestPath(runDir string) string { return filepath.Join(runDir, "manifest.json") }

func loadManifest(runDir string) (runManifest, error) {
	var m runManifest
	b, err := os.ReadFile(manifestPath(runDir))
	if err != nil {
		if os.IsNotExist(err) {
			return runManifest{CreatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
		}
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func saveManifest(runDir string, m runManifest) error {
	return statefile.WriteJSONAtomic(manifestPath(runDir), "dream-manifest", m)
}

func hashFile(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), true, nil
}

func listNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// RunDir returns the state directory for one dream run, creating it.
func RunDir(project, runID string) (string, error) {
	p, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return "", err
	}
	return p, os.MkdirAll(p, runDirMode)
}

// Snapshot copies every file the run intends to write into runDir's backup
// tree. A file that does not exist yet is recorded as absent, so revert
// removes it rather than leaving the run's creation behind.
func Snapshot(runDir string, files []string) error {
	backupDir := filepath.Join(runDir, "backup")
	if err := os.MkdirAll(backupDir, runDirMode); err != nil {
		return err
	}
	m, err := loadManifest(runDir)
	if err != nil {
		return err
	}
	m.Files = nil
	for _, f := range files {
		e, err := backupOne(backupDir, f)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, e)
	}
	return saveManifest(runDir, m)
}

func backupOne(backupDir, path string) (fileEntry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fileEntry{}, err
	}
	sum := sha256.Sum256([]byte(abs))
	name := hex.EncodeToString(sum[:8]) + ".bak"
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fileEntry{Original: abs, Absent: true}, nil
		}
		return fileEntry{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fileEntry{}, err
	}
	if err := os.WriteFile(filepath.Join(backupDir, name), b, backupCopyMode); err != nil {
		return fileEntry{}, err
	}
	return fileEntry{Original: abs, Backup: name, Mode: uint32(fi.Mode().Perm())}, nil
}

// SnapshotDir copies a whole directory, one level deep. The dream's main
// output is one memory file per lesson, named at write time, so no caller
// can list those paths in advance: recording the names lets revert delete
// what appeared, and copying the contents lets it undo edits and
// deletions the caller could not have named either.
func SnapshotDir(runDir string, dirs []string) error {
	backupDir := filepath.Join(runDir, "backup")
	if err := os.MkdirAll(backupDir, runDirMode); err != nil {
		return err
	}
	m, err := loadManifest(runDir)
	if err != nil {
		return err
	}
	m.Dirs = nil
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return err
		}
		names, err := listNames(abs)
		if err != nil {
			if os.IsNotExist(err) {
				m.Dirs = append(m.Dirs, dirEntry{Path: abs, Absent: true})
				continue
			}
			return err
		}
		entry := dirEntry{Path: abs, Before: names}
		for _, n := range names {
			p := filepath.Join(abs, n)
			fi, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				continue
			}
			f, err := backupOne(backupDir, p)
			if err != nil {
				return err
			}
			entry.Files = append(entry.Files, f)
		}
		m.Dirs = append(m.Dirs, entry)
	}
	return saveManifest(runDir, m)
}

// Seal records the tree as the run left it. Without it revert overwrites
// blindly and destroys whatever a coordinator or a human wrote in the
// meantime; with it revert refuses to touch anything that moved since.
func Seal(runDir string) error {
	m, err := loadManifest(runDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(manifestPath(runDir)); err != nil {
		return fmt.Errorf("no manifest to seal in %s: %w", runDir, err)
	}
	if err := sealFiles(m.Files); err != nil {
		return err
	}
	for i, d := range m.Dirs {
		names, err := listNames(d.Path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		m.Dirs[i].After = names
		if err := sealFiles(m.Dirs[i].Files); err != nil {
			return err
		}
	}
	m.Sealed = true
	return saveManifest(runDir, m)
}

func sealFiles(files []fileEntry) error {
	for i, f := range files {
		sum, present, err := hashFile(f.Original)
		if err != nil {
			return err
		}
		files[i].SealHash = sum
		files[i].SealAbsent = !present
	}
	return nil
}

// Revert puts the run's targets back and returns every path it returned to
// its pre-run state. A non-nil error means the tree is now a mix of
// pre-run and post-run content; call RevertWithReport to see which is which.
func Revert(project, runID string) ([]string, error) {
	report, err := RevertWithReport(project, runID)
	return append(report.Restored, report.Removed...), err
}

// RevertWithReport reverts and returns the full per-path outcome. It never
// stops at the first failure: stopping would leave the remaining targets
// untouched with no record of which ones those were.
func RevertWithReport(project, runID string) (RevertReport, error) {
	var report RevertReport
	// statefile.Path only computes the path; RunDir would MkdirAll it as
	// a side effect, which is wrong for a run id nobody has heard of.
	dir, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return report, err
	}
	b, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		if _, rerr := os.Stat(filepath.Join(dir, revertedManifestFile)); rerr == nil {
			return report, fmt.Errorf("run %s: %w; nothing left to put back", runID, ErrAlreadyReverted)
		}
		return report, fmt.Errorf("no manifest for run %s: %w", runID, err)
	}
	var m runManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return report, err
	}
	handled := make(map[string]bool, len(m.Files))
	revertFiles(dir, m.Sealed, m.Files, handled, &report)
	for _, d := range m.Dirs {
		revertFiles(dir, m.Sealed, d.Files, handled, &report)
	}
	revertDirs(m, handled, &report)
	if len(report.Failed) > 0 || len(report.Skipped) > 0 {
		return report, fmt.Errorf(
			"revert incomplete: %d path(s) failed, %d skipped, %d restored, %d removed",
			len(report.Failed), len(report.Skipped), len(report.Restored), len(report.Removed))
	}
	// A fully reverted run is back at its pre-run state: retire the
	// manifest so `write` may run again, and a second revert cannot
	// replay these backups over whatever that write produces.
	if err := os.Rename(manifestPath(dir), filepath.Join(dir, revertedManifestFile)); err != nil {
		return report, err
	}
	return report, nil
}

// revertedManifestFile keeps a reverted run's manifest for the record.
const revertedManifestFile = "manifest.reverted.json"

// ErrAlreadyReverted is a revert of a run whose manifest a full revert
// already retired.
var ErrAlreadyReverted = errors.New("already reverted")

func revertFiles(runDir string, sealed bool, files []fileEntry, handled map[string]bool, report *RevertReport) {
	for _, f := range files {
		if handled[f.Original] {
			continue
		}
		handled[f.Original] = true
		if sealed {
			if reason, ok := guardFile(f); !ok {
				report.Skipped = append(report.Skipped, SkippedPath{f.Original, reason})
				continue
			}
		}
		if f.Absent {
			if err := os.Remove(f.Original); err != nil && !os.IsNotExist(err) {
				report.Failed = append(report.Failed, FailedPath{f.Original, err.Error()})
				continue
			}
			report.Removed = append(report.Removed, f.Original)
			continue
		}
		content, err := os.ReadFile(filepath.Join(runDir, "backup", f.Backup))
		if err != nil {
			report.Failed = append(report.Failed, FailedPath{f.Original, err.Error()})
			continue
		}
		if err := restoreFile(f.Original, content, fs.FileMode(f.Mode)); err != nil {
			report.Failed = append(report.Failed, FailedPath{f.Original, err.Error()})
			continue
		}
		report.Restored = append(report.Restored, f.Original)
	}
}

func guardFile(f fileEntry) (string, bool) {
	sum, present, err := hashFile(f.Original)
	if err != nil {
		return err.Error(), false
	}
	if f.SealAbsent {
		if present {
			return "reappeared after the run was sealed", false
		}
		return "", true
	}
	if !present {
		return "deleted after the run was sealed", false
	}
	if sum != f.SealHash {
		return "changed after the run was sealed", false
	}
	return "", true
}

func revertDirs(m runManifest, handled map[string]bool, report *RevertReport) {
	for _, d := range m.Dirs {
		names, err := listNames(d.Path)
		if err != nil {
			if !os.IsNotExist(err) {
				report.Failed = append(report.Failed, FailedPath{d.Path, err.Error()})
			}
			continue
		}
		before, after := nameSet(d.Before), nameSet(d.After)
		for _, name := range names {
			p := filepath.Join(d.Path, name)
			if before[name] || handled[p] {
				continue
			}
			fi, err := os.Lstat(p)
			if err != nil {
				report.Failed = append(report.Failed, FailedPath{p, err.Error()})
				continue
			}
			if fi.IsDir() {
				report.Skipped = append(report.Skipped,
					SkippedPath{p, "a directory appeared; revert removes files only"})
				continue
			}
			if m.Sealed && !after[name] {
				report.Skipped = append(report.Skipped,
					SkippedPath{p, "created after the run was sealed"})
				continue
			}
			if err := os.Remove(p); err != nil {
				report.Failed = append(report.Failed, FailedPath{p, err.Error()})
				continue
			}
			report.Removed = append(report.Removed, p)
		}
		if d.Absent {
			_ = os.Remove(d.Path)
		}
	}
}

func restoreFile(path string, content []byte, mode fs.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
