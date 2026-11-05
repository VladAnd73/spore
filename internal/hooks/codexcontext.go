package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/coordinator/statearchive"
	"github.com/versality/spore/internal/transcript"
)

// CodexStartContext is the Go port of the Claude load-state-md.pl
// SessionStart hook plus the memory index claude-code auto-loads. It
// returns "" when there is nothing to inject.
//
// state.md is gated like the perl hook: only when SPORE_TASK_SLUG is
// unset or "coordinator", so workers and reviewers never see the
// coordinator's narrative. The memory index is not gated: claude-code
// loads it in every session of the project, worktrees included,
// because it keys the dir by the main repo root.
func CodexStartContext() string {
	root := mainRepoRoot(transcript.ProjectDir())
	var parts []string
	if slug := os.Getenv("SPORE_TASK_SLUG"); slug == "" || slug == "coordinator" {
		if body, err := os.ReadFile(filepath.Join(root, "state.md")); err == nil && len(body) > 0 {
			parts = append(parts, StateSizeWarning(len(body))+"# state.md ("+filepath.Join(root, "state.md")+")\n\n"+string(body))
		}
	}
	dir := claudeMemoryDir(root)
	if body, err := os.ReadFile(filepath.Join(dir, "MEMORY.md")); err == nil && len(body) > 0 {
		parts = append(parts, "# Memory index\n\nShared with claude-code. Linked memory files live in "+
			dir+"/; read one when its line is relevant.\n\n"+string(body))
	}
	return strings.Join(parts, "\n\n")
}

// StateSizeWarning mirrors the warning load-state-md.pl prepends when
// state.md exceeds statearchive.Budget; "" when it fits.
func StateSizeWarning(size int) string {
	if size <= statearchive.Budget {
		return ""
	}
	return fmt.Sprintf("WARNING: state.md is %d bytes, over the %d-byte SessionStart budget; "+
		"most of it is not injected. Run `spore coordinator state ls`, then "+
		"`spore coordinator state archive <id>... --slug <name>` for verifiably done blocks.\n\n",
		size, statearchive.Budget)
}

// CodexStartOutput wraps ctx in the SessionStart JSON codex-rs parses
// (hooks/src/schema.rs SessionStartCommandOutputWire, deny_unknown_fields).
// Empty ctx yields nil: Codex treats empty stdout as no output.
func CodexStartOutput(ctx string) []byte {
	if ctx == "" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "SessionStart",
			"additionalContext": ctx,
		},
	})
	return out
}

// mainRepoRoot hops from a worktree to the main checkout via
// --git-common-dir so every worktree reads the same state.md. Outside a
// git repo it returns start unchanged.
func mainRepoRoot(start string) string {
	out, err := exec.Command("git", "-C", start, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return start
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return start
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(start, common)
	}
	if abs, err := filepath.Abs(filepath.Dir(common)); err == nil {
		return abs
	}
	return start
}

func claudeMemoryDir(root string) string {
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".claude")
	}
	return filepath.Join(base, "projects", transcript.ClaudeProjectKey(root), "memory")
}
