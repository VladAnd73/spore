package dream

import (
	"os"
	"path/filepath"
	"time"

	"github.com/versality/spore/internal/statefile"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// PruneMinAge is how old a run directory has to be before Prune will
// even consider removing it. MintTask's fleet worker spawns within
// about 0.4 seconds of the task file appearing (measured on this
// host), so the spawn race is not what this margin is against: it is
// against an operator being away long enough that the minted task's
// judgement is genuinely done, not just pending. A week covers a
// normal absence with room to spare.
const PruneMinAge = 7 * 24 * time.Hour

// PruneReport says what one prune pass did to a project's run
// directories.
type PruneReport struct {
	Removed []string
	Kept    []string
}

// Prune removes run directories under a project's dreams state that are
// both older than PruneMinAge and whose minted task is done or no
// longer in tasksDir. Age alone is not the rule: MintTask names each
// task deterministically from its run id, so a run younger than
// PruneMinAge is kept regardless of its task's state, and a run whose
// task is still active, paused, blocked, or draft is kept regardless of
// age, because pruning it would delete the digest a live task still
// points a worker at.
func Prune(project, tasksDir string, now time.Time) (PruneReport, error) {
	var rep PruneReport
	dir, err := statefile.Path(project, "dreams")
	if err != nil {
		return rep, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		info, err := e.Info()
		if err != nil {
			return rep, err
		}
		if now.Sub(info.ModTime()) < PruneMinAge || mintedTaskIsLive(tasksDir, runID) {
			rep.Kept = append(rep.Kept, runID)
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, runID)); err != nil {
			return rep, err
		}
		rep.Removed = append(rep.Removed, runID)
	}
	return rep, nil
}

// mintedTaskIsLive reports whether the task MintTask would have named
// for runID is still in tasksDir with a status other than done. A task
// gone from tasksDir entirely (judged and archived, or never minted)
// counts the same as one marked done: neither points a worker at the
// run directory any longer.
func mintedTaskIsLive(tasksDir, runID string) bool {
	slug := task.Slugify("dream " + runID)
	b, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		return false
	}
	m, _, err := frontmatter.Parse(b)
	if err != nil {
		return false
	}
	return m.Status != "done"
}
