package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MemoryDirFile names, inside a run directory, the file holding the one
// memory directory this run's memory-tier writes may land in. Digest
// records it, the mint shows it to the proposer, and write enforces it,
// because the write stage otherwise writes wherever a packet points.
const MemoryDirFile = "memory-dir"

var projectDirRE = regexp.MustCompile(`[^a-zA-Z0-9]`)

// MemoryDir is Claude Code's per-project memory directory under the
// transcripts root. Claude Code names the project directory by turning
// every non-alphanumeric byte of the root into '-', but truncates a name
// past 200 bytes and appends a hash, so the plain encoding is only the
// fallback: the directory whose transcripts ran in projectRoot wins.
func MemoryDir(projectsRoot, projectRoot string) string {
	if dir := projectDirByCwd(projectsRoot, projectRoot); dir != "" {
		return filepath.Join(dir, "memory")
	}
	return filepath.Join(projectsRoot, projectDirRE.ReplaceAllString(projectRoot, "-"), "memory")
}

// projectDirByCwd returns the directory under projectsRoot whose first
// transcript records cwd == root, or "" when none does. Every transcript
// in one directory shares a cwd, so one file per directory is enough.
func projectDirByCwd(projectsRoot, root string) string {
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsRoot, e.Name())
		files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		if err != nil || len(files) == 0 {
			continue
		}
		if transcriptCwd(files[0]) == root {
			return dir
		}
	}
	return ""
}

// transcriptCwd is the first cwd a transcript records; leading entries
// such as summaries carry none.
func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Cwd != "" {
			return e.Cwd
		}
	}
	return ""
}

func readMemoryDir(runDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(runDir, MemoryDirFile))
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%s: %q is not an absolute path", MemoryDirFile, dir)
	}
	return filepath.Clean(dir), nil
}

// checkMemoryTargets refuses the batch before anything is snapshotted or
// written if any memory-tier target is not a file directly inside the
// recorded memory directory: MEMORY.md is appended beside the target.
func checkMemoryTargets(runDir string, toWrite []PacketFile) error {
	var memDir string
	for _, pf := range toWrite {
		if pf.Packet.Tier != "memory" {
			continue
		}
		if memDir == "" {
			d, err := readMemoryDir(runDir)
			if err != nil {
				return fmt.Errorf("dream: write: packet %d: no memory directory recorded for this run: %w", pf.N, err)
			}
			memDir = d
		}
		if filepath.Dir(filepath.Clean(pf.Packet.Target)) != memDir {
			return fmt.Errorf("dream: write: packet %d: memory target %q is not inside %s",
				pf.N, pf.Packet.Target, memDir)
		}
	}
	return nil
}
