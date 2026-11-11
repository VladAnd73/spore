package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Two of the files the proposer brief promises the agent will find in the
// run directory. MintTask refuses until both, and MemoryDirFile, are on
// disk, which makes the appearance of the task file the signal that the
// run directory is finished: the fleet spawns a worker within seconds
// of the write.
const (
	DigestFile      = "digest.md"
	KnownClaimsFile = "known-claims.md"
)

// DeepRead names one session the proposer is expected to read in part.
// Entries is how many entries the transcript holds. The proposer has to
// report coverage as entries-seen out of entries-present, so without
// the denominator in front of it that claim is not checkable.
type DeepRead struct {
	Session string
	Path    string
	Entries int
}

// MintTask writes an active task carrying the proposer brief and
// returns the allocated slug. The project is passed in, never derived
// from the working directory: this job runs from a timer and mints
// tasks for projects other than its own cwd.
//
// Status is active because nothing in the kernel promotes a draft.
// fleet.Reconcile spawns status=active and nothing else, and the only
// draft-to-active path is task.Start behind `spore task start`, which an
// operator has to run by hand. A draft would sit in the queue until
// someone typed a command every morning.
func MintTask(tasksDir, project, runID, runDir string, deepRead []DeepRead) (string, error) {
	if err := checkMintArgs(tasksDir, project, runID, runDir, deepRead); err != nil {
		return "", err
	}
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return "", err
	}
	title := "dream " + runID
	slug, err := task.Allocate(tasksDir, task.Slugify(title))
	if err != nil {
		return "", err
	}
	m := frontmatter.Meta{
		Status:  "active",
		Slug:    slug,
		Title:   title,
		Created: time.Now().UTC().Format(time.RFC3339),
		Project: project,
	}
	memDir, err := readMemoryDir(runDir)
	if err != nil {
		return "", fmt.Errorf("dream: mint: run dir %s: %w", runDir, err)
	}
	body := "\n" + ProposerBrief + runSection(runID, runDir, memDir, deepRead)
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, frontmatter.Write(m, []byte(body)), 0o644); err != nil {
		return "", err
	}
	return slug, nil
}

// checkMintArgs refuses every argument shape that would produce a task
// no agent can act on. The tasks directory is what actually routes a
// task: no reader of the `project:` field decides where a task lands or
// which repo a worker gets, so the frontmatter alone protects nothing.
// Requiring the directory to belong to the named project is what stops
// one project's dream from being minted into another's queue.
func checkMintArgs(tasksDir, project, runID, runDir string, deepRead []DeepRead) error {
	for _, f := range []struct{ name, value string }{
		{"tasksDir", tasksDir},
		{"project", project},
		{"runID", runID},
		{"runDir", runDir},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("dream: mint: %s must not be empty", f.name)
		}
	}
	abs, err := filepath.Abs(tasksDir)
	if err != nil {
		return err
	}
	if owner := filepath.Base(filepath.Dir(abs)); owner != project {
		return fmt.Errorf("dream: mint: tasks dir %s belongs to project %q, not %q; a task minted here would be picked up by the wrong project's fleet",
			tasksDir, owner, project)
	}
	if !filepath.IsAbs(runDir) {
		return fmt.Errorf("dream: mint: run dir %s must be absolute: a worker reads it from its own worktree", runDir)
	}
	info, err := os.Stat(runDir)
	if err != nil {
		return fmt.Errorf("dream: mint: run dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dream: mint: run dir %s is not a directory", runDir)
	}
	for _, name := range []string{DigestFile, KnownClaimsFile, MemoryDirFile} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			return fmt.Errorf("dream: mint: run dir %s: the brief promises %s: %w", runDir, name, err)
		}
	}
	for i, dr := range deepRead {
		if err := checkDeepRead(dr); err != nil {
			return fmt.Errorf("dream: mint: deep read %d: %w", i, err)
		}
	}
	return nil
}

func checkDeepRead(dr DeepRead) error {
	if strings.TrimSpace(dr.Session) == "" {
		return fmt.Errorf("no session identifier")
	}
	if !filepath.IsAbs(dr.Path) {
		return fmt.Errorf("session %s: transcript path %q must be absolute", dr.Session, dr.Path)
	}
	if _, err := os.Stat(dr.Path); err != nil {
		return fmt.Errorf("session %s: transcript: %w", dr.Session, err)
	}
	if dr.Entries <= 0 {
		return fmt.Errorf("session %s: entry count must be positive: it is the denominator of the coverage claim", dr.Session)
	}
	return nil
}

func runSection(runID, runDir, memDir string, deepRead []DeepRead) string {
	var b strings.Builder
	b.WriteString("\n## This run\n\n")
	fmt.Fprintf(&b, "- run id: %s\n", runID)
	fmt.Fprintf(&b, "- run directory: %s\n", runDir)
	fmt.Fprintf(&b, "- digest: %s\n", filepath.Join(runDir, DigestFile))
	fmt.Fprintf(&b, "- known claims: %s\n", filepath.Join(runDir, KnownClaimsFile))
	fmt.Fprintf(&b, "- memory directory: %s\n", memDir)
	b.WriteString("\n### Deep-read sessions\n\n")
	if len(deepRead) == 0 {
		b.WriteString("None were flagged. Work from the digest alone.\n")
		return b.String()
	}
	b.WriteString("Session, transcript path, and how many entries the transcript holds.\n" +
		"The entry count is the denominator of the coverage line on every\n" +
		"packet you build from that session.\n\n")
	for _, dr := range deepRead {
		fmt.Fprintf(&b, "- %s: %s (%d entries)\n", dr.Session, dr.Path, dr.Entries)
	}
	return b.String()
}
