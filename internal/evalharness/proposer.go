package evalharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/dream"
	"github.com/versality/spore/internal/task/frontmatter"
)

// proposerFixturePath returns the embedded transcript path for a named
// proposer fixture (e.g. "demonstrated" -> fixtures/dream/proposer/
// demonstrated/session.jsonl).
func proposerFixturePath(name string) string {
	return "fixtures/dream/proposer/" + name + "/session.jsonl"
}

// BuildProposerRun runs the real dream.Run pipeline (Discover, digest,
// mint) against one fixture transcript inside sb, so the run directory
// and minted task body it produces are byte-for-byte what production
// would build from the same transcript - not a hand-rolled
// approximation of digest.md's format. It returns the prompt a spawned
// agent should receive (the minted task's body, with the embedded
// proposer brief swapped for briefOverride when non-empty) and the
// absolute run directory the agent should write packets/ into.
func BuildProposerRun(sb *Sandbox, fixture, runID string, now time.Time, briefOverride string) (prompt, runDir string, err error) {
	// dream.Run resolves its ledger, watermark, and run directory
	// through internal/statefile, which reads XDG_STATE_HOME straight
	// out of the process environment - it has no Options field for
	// this. Pointing it at the sandbox's own state dir is what keeps a
	// scenario run from ever touching this host's real dream state for
	// whichever project os.Getenv would otherwise resolve to.
	if err := os.Setenv("XDG_STATE_HOME", sb.StateDir); err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}

	raw, err := FixturesFS.ReadFile(proposerFixturePath(fixture))
	if err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}
	transcript := strings.ReplaceAll(string(raw), "__HOME__", sb.Home)

	sessionDir := filepath.Join(sb.Projects, fixture)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(transcript), 0o644); err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}

	rep, err := dream.Run(dream.Options{
		ProjectsRoot: sb.Projects,
		Home:         sb.Home,
		Project:      sb.Project,
		TasksDir:     sb.TasksDir,
		Now:          now,
		RunID:        runID,
	})
	if err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: dream.Run: %w", fixture, err)
	}
	if rep.TaskSlug == "" {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: dream.Run minted no task (report: %+v)", fixture, rep)
	}

	taskPath := filepath.Join(sb.TasksDir, rep.TaskSlug+".md")
	raw, err = os.ReadFile(taskPath)
	if err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}
	_, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("evalharness: proposer fixture %q: %w", fixture, err)
	}

	prompt = string(body)
	if briefOverride != "" {
		suffix := strings.TrimPrefix(prompt, "\n"+dream.ProposerBrief)
		if suffix == prompt {
			return "", "", fmt.Errorf("evalharness: proposer fixture %q: minted task body did not start with the embedded proposer brief, cannot swap in an override safely", fixture)
		}
		prompt = "\n" + briefOverride + suffix
	}
	return prompt, rep.RunDir, nil
}
