package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// RoleTaskRoot is the directory name at projectRoot that holds the
// per-task artifact tree for the role-based fleet. Layout under
// `<projectRoot>/.spore/<slug>/`:
//
//	spec.md                          one-shot cache of the canonical spec
//	responses/engineer-round-N.json  engineer's per-round handoff
//	reviews/A/round-N.json           reviewer A's per-round verdict
//	reviews/B/round-N.json           reviewer B's per-round verdict
//
// All three role panes (engineer + two reviewers) read and write the
// same tree, regardless of which worktree their cwd points at. The
// coordinator hands the absolute path to each pane via
// SPORE_TASK_DIR at spawn time.
const RoleTaskRoot = ".spore"

// LoopRole is the frontmatter `loop:` value that opts a task into the
// role-based loop. Carried in tasks/<slug>.md so the opt-in travels
// in the same write that flips `status: active`: the fleet reconciler
// sees both atomically and never homogeneous-spawns the slug, even
// before `.spore/<slug>/` exists on disk. The key is the only opt-in:
// `.spore/<slug>/` on its own never classifies a task as role-looped,
// since coordinators stage briefs there for generic tasks too.
const LoopRole = "role"

// RequireRoleLoop errors unless tasks/<slug>.md carries `loop: role`.
func RequireRoleLoop(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("roletask: parse %s: %w", path, err)
	}
	if m.Loop != LoopRole {
		return fmt.Errorf("task %s: not role-looped (frontmatter lacks loop: %s); add it to opt in", slug, LoopRole)
	}
	return nil
}

// IsEscalated reports whether any `state/escalated-*` marker exists
// under `<roletaskdir>/state/`. The role-loop driver writes one such
// marker on a PhaseEscalated transition and removes them on the
// PhaseDone transition; callers (waybar chip, status) use the presence
// of any marker as the chip's "escalated" signal. A missing role-task
// dir returns (false, nil) so the chip stays quiet for slugs without
// an artifact tree yet.
func IsEscalated(projectRoot, slug string) (bool, error) {
	dir := filepath.Join(RoleTaskDir(projectRoot, slug), "state")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "escalated-") {
			return true, nil
		}
	}
	return false, nil
}

// IsReady reports whether the `state/ready` marker exists under
// `<roletaskdir>/state/`. The role-loop driver writes it on the
// PhaseDone transition; task.Done removes it when the operator
// finalises the task. A missing role-task or state dir returns
// (false, nil). Mirrors IsEscalated.
func IsReady(projectRoot, slug string) (bool, error) {
	marker := filepath.Join(RoleTaskDir(projectRoot, slug), "state", "ready")
	if _, err := os.Stat(marker); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ReviewerInstance is the per-spawn label for a reviewer pane. The
// coordinator sets SPORE_REVIEWER_INSTANCE at spawn so a single
// reviewer body serves both passes.
type ReviewerInstance string

const (
	ReviewerA ReviewerInstance = "A"
	ReviewerB ReviewerInstance = "B"
)

// Verdict values written into reviews/<instance>/round-N.json.
const (
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request_changes"
)

// EngineerResponse is the engineer's structured handoff for one
// round. Persisted as `responses/engineer-round-N.json`. Author must
// be EngineerAuthor; see ParseEngineerResponse for the full schema.
type EngineerResponse struct {
	Author    string   `json:"author"`
	Addressed []string `json:"addressed"`
	Pushback  []string `json:"pushback"`
	Notes     string   `json:"notes,omitempty"`
	// Followups proposes out-of-scope work (new services, extra
	// features) for a separate PR instead of growing this one.
	Followups []string `json:"followups,omitempty"`
}

// Review is a reviewer's verdict for one round. Persisted as
// `reviews/<instance>/round-N.json`. Author must equal the instance
// directory the file sits in; see ParseReview.
type Review struct {
	Author   string   `json:"author"`
	Verdict  string   `json:"verdict"`
	Summary  string   `json:"summary"`
	Comments []string `json:"comments"`
}

// RoleTaskDir returns the absolute path to `<projectRoot>/.spore/<slug>/`.
func RoleTaskDir(projectRoot, slug string) string {
	return filepath.Join(projectRoot, RoleTaskRoot, slug)
}

// EnsureRoleTaskDir creates `<projectRoot>/.spore/<slug>/` plus the
// `responses/` and `reviews/{A,B}/` subdirectories. Idempotent.
func EnsureRoleTaskDir(projectRoot, slug string) (string, error) {
	if slug == "" {
		return "", errors.New("roletask: empty slug")
	}
	root := RoleTaskDir(projectRoot, slug)
	for _, sub := range []string{
		"",
		"responses",
		filepath.Join("reviews", string(ReviewerA)),
		filepath.Join("reviews", string(ReviewerB)),
	} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return "", err
		}
	}
	return root, nil
}

// WriteSpec caches body at `<roletaskdir>/spec.md`. The cache is
// immutable for the run; callers that want to revise the spec kill
// and restart the task. EnsureRoleTaskDir is called first so a
// fresh task tree springs into existence on the first write.
func WriteSpec(projectRoot, slug string, body []byte) error {
	root, err := EnsureRoleTaskDir(projectRoot, slug)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "spec.md"), body, 0o644)
}

// SpecExists reports whether `<roletaskdir>/spec.md` is already on
// disk. Callers driving the loop use this as the "cache is hot"
// signal: the cache is immutable for the run, so a present spec
// file short-circuits re-caching from the task file.
func SpecExists(projectRoot, slug string) bool {
	_, err := os.Stat(filepath.Join(RoleTaskDir(projectRoot, slug), "spec.md"))
	return err == nil
}

// CacheSpecFromTaskFile copies the body of `<tasksDir>/<slug>.md`
// (frontmatter stripped) into `<roletaskdir>/spec.md`. Idempotent:
// returns nil without touching the file when spec.md already exists,
// so callers can invoke it on every drive pass. Returns
// fs.ErrNotExist (wrapped) when the task file is missing.
func CacheSpecFromTaskFile(projectRoot, tasksDir, slug string) error {
	if SpecExists(projectRoot, slug) {
		return nil
	}
	taskPath := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		return err
	}
	_, body, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("roletask: parse %s: %w", taskPath, err)
	}
	return WriteSpec(projectRoot, slug, body)
}

// ReadSpec returns the cached spec body.
func ReadSpec(projectRoot, slug string) ([]byte, error) {
	return os.ReadFile(filepath.Join(RoleTaskDir(projectRoot, slug), "spec.md"))
}

// EngineerResponsePath returns the path of round N's engineer
// response file.
func EngineerResponsePath(projectRoot, slug string, round int) string {
	return filepath.Join(
		RoleTaskDir(projectRoot, slug),
		"responses",
		fmt.Sprintf("engineer-round-%d.json", round),
	)
}

// ReviewPath returns the path of the given reviewer's round-N
// verdict file.
func ReviewPath(projectRoot, slug string, instance ReviewerInstance, round int) string {
	return filepath.Join(
		RoleTaskDir(projectRoot, slug),
		"reviews",
		string(instance),
		fmt.Sprintf("round-%d.json", round),
	)
}

// WriteEngineerResponse persists r at round N. The parent dir is
// created on first write so engineers do not need a separate setup
// step. An empty Author is filled with EngineerAuthor.
func WriteEngineerResponse(projectRoot, slug string, round int, r EngineerResponse) error {
	if round < 1 {
		return fmt.Errorf("roletask: round must be >= 1, got %d", round)
	}
	if r.Author == "" {
		r.Author = EngineerAuthor
	}
	if r.Author != EngineerAuthor {
		return fmt.Errorf("roletask: engineer response author %q, want %q", r.Author, EngineerAuthor)
	}
	path := EngineerResponsePath(projectRoot, slug, round)
	return writeJSON(path, r)
}

// ReadEngineerResponse loads and validates round N. A missing file
// returns fs.ErrNotExist (wrapped); a malformed one an error wrapping
// ErrInvalidRoleFile.
func ReadEngineerResponse(projectRoot, slug string, round int) (EngineerResponse, error) {
	path := EngineerResponsePath(projectRoot, slug, round)
	b, err := os.ReadFile(path)
	if err != nil {
		return EngineerResponse{}, err
	}
	legacy, err := isLegacyFile(projectRoot, slug, relTo(RoleTaskDir(projectRoot, slug), path))
	if err != nil {
		return EngineerResponse{}, err
	}
	return ParseEngineerResponse(path, b, legacy)
}

// ListEngineerRounds returns the sorted round numbers present under
// `responses/`. Missing dir returns an empty slice.
func ListEngineerRounds(projectRoot, slug string) ([]int, error) {
	return listRounds(filepath.Join(RoleTaskDir(projectRoot, slug), "responses"), "engineer-round-")
}

// WriteReview persists v at round N under the given instance. An
// empty Author is filled with the instance; the result must pass
// ParseReview in strict mode.
func WriteReview(projectRoot, slug string, instance ReviewerInstance, round int, v Review) error {
	if round < 1 {
		return fmt.Errorf("roletask: round must be >= 1, got %d", round)
	}
	if err := validateInstance(instance); err != nil {
		return err
	}
	if v.Author == "" {
		v.Author = string(instance)
	}
	path := ReviewPath(projectRoot, slug, instance, round)
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := ParseReview(path, b, instance, false); err != nil {
		return fmt.Errorf("roletask: %w", err)
	}
	return writeJSON(path, v)
}

// ReadReview loads and validates round N from the given reviewer. A
// malformed file returns an error wrapping ErrInvalidRoleFile.
func ReadReview(projectRoot, slug string, instance ReviewerInstance, round int) (Review, error) {
	if err := validateInstance(instance); err != nil {
		return Review{}, err
	}
	path := ReviewPath(projectRoot, slug, instance, round)
	b, err := os.ReadFile(path)
	if err != nil {
		return Review{}, err
	}
	legacy, err := isLegacyFile(projectRoot, slug, relTo(RoleTaskDir(projectRoot, slug), path))
	if err != nil {
		return Review{}, err
	}
	return ParseReview(path, b, instance, legacy)
}

// ListReviewRounds returns the sorted round numbers under the given
// reviewer's directory.
func ListReviewRounds(projectRoot, slug string, instance ReviewerInstance) ([]int, error) {
	if err := validateInstance(instance); err != nil {
		return nil, err
	}
	dir := filepath.Join(RoleTaskDir(projectRoot, slug), "reviews", string(instance))
	return listRounds(dir, "round-")
}

func validateInstance(i ReviewerInstance) error {
	if i != ReviewerA && i != ReviewerB {
		return fmt.Errorf("roletask: reviewer instance %q must be %q or %q", i, ReviewerA, ReviewerB)
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// listRounds returns sorted round numbers parsed from filenames of
// the shape `<prefix><N>.json` in dir.
func listRounds(dir, prefix string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var rounds []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		num := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		rounds = append(rounds, n)
	}
	sort.Ints(rounds)
	return rounds, nil
}
