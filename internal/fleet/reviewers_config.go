package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/versality/spore/internal/sporetoml"
	"github.com/versality/spore/internal/task"
)

// Reviewer slot modes: how a reviewer pane produces its verdict file.
// ReviewerModeFleet runs the reviewer.md role body;
// ReviewerModeCodeReview runs reviewer-code-review.md, which drives
// Claude Code's built-in /code-review skill. See ResolveRoleBody.
const (
	ReviewerModeFleet      = "fleet"
	ReviewerModeCodeReview = "code-review"
)

// DefaultReviewEffort is the /code-review level a code-review slot
// runs at when `effort` is unset.
const DefaultReviewEffort = "medium"

var reviewEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// ReviewerSlot is one `[fleet.reviewers.<A|B>]` table, overlaid by
// the host-side workers.toml key by key. Agent "" means the slot
// follows roleAgent, except in code-review mode, which is claude-only.
// DefaultMode marks a Mode no config set. Mode and Effort of a loaded
// slot are provisional: the role spawn settles them (see
// RoleSpawnSpec.resolve), dropping a defaulted code-review slot to
// fleet when its agent resolves to non-claude and filling Effort.
type ReviewerSlot struct {
	Mode        string
	Agent       string
	Effort      string
	DefaultMode bool
}

// ReviewersConfig holds the resolved slot for each reviewer instance.
type ReviewersConfig struct {
	A, B ReviewerSlot
}

var reviewerInstances = []task.ReviewerInstance{task.ReviewerA, task.ReviewerB}

// Slot returns the config for inst.
func (c ReviewersConfig) Slot(inst task.ReviewerInstance) ReviewerSlot {
	return *c.slot(inst)
}

func (c *ReviewersConfig) slot(inst task.ReviewerInstance) *ReviewerSlot {
	if inst == task.ReviewerB {
		return &c.B
	}
	return &c.A
}

// LoadReviewersConfig reads `[fleet.reviewers.A]` and
// `[fleet.reviewers.B]` from spore.toml, then the host overlay (see
// WorkersOverlayPath). Without a `mode`, A defaults to code-review
// and B to fleet: bug hunt first, spec and verification second. The
// spawn settles the effective mode and effort (see ReviewerSlot).
func LoadReviewersConfig(projectRoot string) (ReviewersConfig, error) {
	var cfg ReviewersConfig
	paths := []string{filepath.Join(projectRoot, "spore.toml")}
	if ov, err := WorkersOverlayPath(projectRoot); err == nil {
		paths = append(paths, ov)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return ReviewersConfig{}, fmt.Errorf("reviewers: read %s: %w", path, err)
		}
		for _, inst := range reviewerInstances {
			if err := applyReviewerSection(cfg.slot(inst), string(b), inst); err != nil {
				return ReviewersConfig{}, fmt.Errorf("reviewers: parse %s: %w", path, err)
			}
		}
	}
	for _, inst := range reviewerInstances {
		if err := finishReviewerSlot(cfg.slot(inst), inst); err != nil {
			return ReviewersConfig{}, err
		}
	}
	return cfg, nil
}

func applyReviewerSection(slot *ReviewerSlot, content string, inst task.ReviewerInstance) error {
	section := "fleet.reviewers." + string(inst)
	entries, err := sporetoml.ParseSection(content, section)
	if err != nil {
		return err
	}
	for _, e := range entries {
		switch e.Key {
		case "mode":
			slot.Mode = e.Value
		case "agent":
			slot.Agent = e.Value
		case "effort":
			slot.Effort = e.Value
		default:
			return fmt.Errorf("line %d: unknown key %q in [%s]", e.Line, e.Key, section)
		}
	}
	return nil
}

// finishReviewerSlot fills defaults and rejects combinations that
// would spawn a pane unable to do its job.
func finishReviewerSlot(slot *ReviewerSlot, inst task.ReviewerInstance) error {
	section := "[fleet.reviewers." + string(inst) + "]"
	switch slot.Mode {
	case "":
		slot.DefaultMode = true
		slot.Mode = ReviewerModeFleet
		if inst == task.ReviewerA {
			slot.Mode = ReviewerModeCodeReview
		}
	case ReviewerModeFleet, ReviewerModeCodeReview:
	default:
		return fmt.Errorf("reviewers: %s mode %q: want %q or %q", section, slot.Mode, ReviewerModeFleet, ReviewerModeCodeReview)
	}
	if slot.Agent == "claude-code" {
		slot.Agent = "claude"
	}
	if slot.Effort != "" && !slices.Contains(reviewEfforts, slot.Effort) {
		return fmt.Errorf("reviewers: %s effort %q: want one of %v", section, slot.Effort, reviewEfforts)
	}
	if slot.Mode == ReviewerModeCodeReview && !slot.DefaultMode && slot.Agent != "" && !isClaudeAgent(slot.Agent) {
		return fmt.Errorf("reviewers: %s mode %q runs a Claude Code skill and needs agent claude, got %q", section, ReviewerModeCodeReview, slot.Agent)
	}
	return nil
}
