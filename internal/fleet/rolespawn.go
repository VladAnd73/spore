package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/ghapp"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// RoleSpawnSpec describes a single role pane the coordinator wants
// alive for a task. The coordinator builds one per role / instance
// (engineer + reviewer A + reviewer B) and feeds them to SpawnRole.
type RoleSpawnSpec struct {
	// ProjectRoot is the main repo root. Required.
	ProjectRoot string
	// Slug is the task slug. Required.
	Slug string
	// Role is the role name ("engineer" or "reviewer"). Required.
	Role string
	// ReviewerInstance is the per-instance label. Required when Role
	// is "reviewer". Ignored otherwise.
	ReviewerInstance task.ReviewerInstance
	// RoleBodyPath is the absolute path to the role markdown that
	// the agent should consume as its first user message.
	// Defaults to ResolveRoleBody of <Role>.md, or of
	// reviewer-code-review.md for a reviewer slot in code-review mode.
	RoleBodyPath string
	// Cwd is the working directory for the pane. Defaults to the
	// task worktree at <ProjectRoot>/.worktrees/<Slug>/ for both
	// roles: the engineer commits there and the reviewer needs to
	// `git diff` the branch from there. A consumer with a non-
	// standard worktree layout must set Cwd explicitly.
	Cwd string
	// TasksDir holds <Slug>.md, read for the `agent:` frontmatter.
	// Defaults to <ProjectRoot>/tasks.
	TasksDir string
	// Agent is the command the pane execs. Empty resolves through the
	// reviewer slot's `agent`, then roleAgent.
	Agent string
}

// SessionName returns the tmux session name the spec spawns. For
// engineers: "spore-role/<project>/<slug>/engineer". For reviewers:
// "spore-role/<project>/<slug>/reviewer-<instance>". The "spore-role/"
// prefix keeps role panes off the existing fleet reconciler's spawn-
// and-reap path, which targets the "spore/" prefix.
func (s RoleSpawnSpec) SessionName() (string, error) {
	project, err := task.ProjectName(s.ProjectRoot)
	if err != nil {
		return "", err
	}
	switch s.Role {
	case "engineer":
		return fmt.Sprintf("spore-role/%s/%s/engineer", project, s.Slug), nil
	case "reviewer":
		if err := validateInstance(s.ReviewerInstance); err != nil {
			return "", err
		}
		return fmt.Sprintf("spore-role/%s/%s/reviewer-%s", project, s.Slug, s.ReviewerInstance), nil
	default:
		return "", fmt.Errorf("rolespawn: unknown role %q (want engineer or reviewer)", s.Role)
	}
}

// TmuxArgs returns the argv that `tmux <args>` would receive to spawn
// the session. Exposed so tests can assert on the env wiring without
// invoking tmux.
func (s RoleSpawnSpec) TmuxArgs() ([]string, error) {
	if s.ProjectRoot == "" {
		return nil, fmt.Errorf("rolespawn: empty project root")
	}
	if s.Slug == "" {
		return nil, fmt.Errorf("rolespawn: empty slug")
	}
	session, err := s.SessionName()
	if err != nil {
		return nil, err
	}

	slot, meta, agent, err := s.resolve()
	if err != nil {
		return nil, err
	}
	rolePath := s.RoleBodyPath
	if rolePath == "" {
		body := s.Role + ".md"
		if slot.Mode == ReviewerModeCodeReview {
			body = "reviewer-code-review.md"
		}
		if rolePath, err = ResolveRoleBody(s.ProjectRoot, body); err != nil {
			return nil, err
		}
	}
	cwd := s.Cwd
	if cwd == "" {
		cwd = filepath.Join(s.ProjectRoot, ".worktrees", s.Slug)
	}
	withBody, bare, err := s.invocations(agent, meta)
	if err != nil {
		return nil, err
	}

	taskDir := task.RoleTaskDir(s.ProjectRoot, s.Slug)
	project, err := task.ProjectName(s.ProjectRoot)
	if err != nil {
		return nil, err
	}

	args := []string{
		"new-session", "-d",
		"-s", session,
		"-c", cwd,
		"-e", "SPORE_TASK_SLUG=" + s.Slug,
		"-e", "SPORE_PROJECT_ROOT=" + s.ProjectRoot,
		"-e", "WT_PROJECT=" + project,
		"-e", "SPORE_TASK_DIR=" + taskDir,
		"-e", "SPORE_ROLE=" + s.Role,
	}
	if tier := task.LoadAccountTier(s.ProjectRoot); tier != "" {
		args = append(args, "-e", "SPORE_ACCOUNT_TIER="+tier)
	}
	if s.Role == "reviewer" {
		args = append(args, "-e", "SPORE_REVIEWER_INSTANCE="+string(s.ReviewerInstance))
		args = append(args, "-e", "SPORE_REVIEWER_MODE="+slot.Mode)
		if slot.Mode == ReviewerModeCodeReview {
			args = append(args, "-e", "SPORE_REVIEW_EFFORT="+slot.Effort)
		}
	}
	if s.Role == "engineer" {
		args = append(args, commitIdentityEnv(s.ProjectRoot)...)
	}
	prelude := task.LoadPreStart(s.ProjectRoot).Prelude(s.ProjectRoot, s.Slug, roleKey(s))
	args = append(args, roleShellCommand(prelude, withBody, bare, rolePath))
	return args, nil
}

// commitIdentityEnv authors engineer commits as the dispatching
// operator when the draft-PR flow is on: the app pushes, the operator
// is the commit author, so the operator can approve the app's PR. A
// github.toml that fails to load adds nothing; the mirror reports it.
func commitIdentityEnv(projectRoot string) []string {
	cfg, err := ghapp.LoadConfig(projectRoot)
	if err != nil || !cfg.Enabled() || cfg.CommitName == "" || cfg.CommitEmail == "" {
		return nil
	}
	var out []string
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		out = append(out, "-e", k+"_NAME="+cfg.CommitName, "-e", k+"_EMAIL="+cfg.CommitEmail)
	}
	return out
}

// resolve settles the pane's reviewer slot and agent in one place.
// Agent: the spec's Agent, else the slot's, else claude for an
// explicit code-review slot, else roleAgent. A defaulted code-review
// slot whose agent is non-claude drops to fleet; Effort is filled in
// code-review mode and cleared, with a warning, in fleet mode. Like
// the agent itself, the defaulted mode follows the config at each
// spawn, so editing the agent config mid-task can switch A's protocol
// between rounds (see docs/role-loop.md).
func (s RoleSpawnSpec) resolve() (slot ReviewerSlot, meta frontmatter.Meta, agent string, err error) {
	if s.Role == "reviewer" {
		cfg, err := LoadReviewersConfig(s.ProjectRoot)
		if err != nil {
			return slot, meta, "", err
		}
		slot = cfg.Slot(s.ReviewerInstance)
	}
	if s.Agent == "" {
		tasksDir := s.TasksDir
		if tasksDir == "" {
			tasksDir = filepath.Join(s.ProjectRoot, "tasks")
		}
		if meta, err = readRoleTaskMeta(tasksDir, s.Slug); err != nil {
			return slot, meta, "", err
		}
	}
	switch {
	case s.Agent != "":
		agent = s.Agent
	case slot.Agent != "":
		agent = slot.Agent
	case slot.Mode == ReviewerModeCodeReview && !slot.DefaultMode:
		agent = "claude"
	default:
		if agent, err = roleAgent(s.ProjectRoot, meta); err != nil {
			return slot, meta, "", err
		}
	}
	if s.Role != "reviewer" {
		return slot, meta, agent, nil
	}
	if slot.Mode == ReviewerModeCodeReview && slot.DefaultMode && !isClaudeAgent(agent) {
		slot.Mode = ReviewerModeFleet
	}
	switch {
	case slot.Mode == ReviewerModeCodeReview && slot.Effort == "":
		slot.Effort = DefaultReviewEffort
	case slot.Mode == ReviewerModeFleet && slot.Effort != "":
		warnRoleOnce("rolespawn: %s [fleet.reviewers.%s] effort %q ignored: slot runs in %s mode with agent %q\n", s.ProjectRoot, s.ReviewerInstance, slot.Effort, ReviewerModeFleet, agent)
		slot.Effort = ""
	}
	return slot, meta, agent, nil
}

// invocations returns the pane's agent command with and without a
// role body (see agentInvocations) for the agent resolve picked.
func (s RoleSpawnSpec) invocations(agent string, meta frontmatter.Meta) (withBody, bare string, err error) {
	if s.Agent != "" || agent != "codex" {
		withBody, bare = agentInvocations(agent)
		return withBody, bare, nil
	}
	cmd, err := task.CodexAgentCommand(meta)
	if err != nil {
		return "", "", err
	}
	return cmd + " --", cmd, nil
}

// roleAgent picks the agent for a task's role panes: the task's
// `agent:`, else the effective [fleet.workers] default (host overlay,
// then spore.toml), else claude. Ratio and rules are skipped on
// purpose: they depend on the running mix, and the engineer and
// reviewers of one task must not flip agents between ticks.
func roleAgent(projectRoot string, meta frontmatter.Meta) (string, error) {
	agent := meta.Agent
	if agent == "" {
		cfg, err := LoadWorkersConfig(projectRoot)
		if err != nil {
			return "", err
		}
		agent = cfg.Default
	}
	switch agent {
	case "", "claude-code":
		return "claude", nil
	}
	return agent, nil
}

func readRoleTaskMeta(tasksDir, slug string) (frontmatter.Meta, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return frontmatter.Meta{}, nil
	}
	if err != nil {
		return frontmatter.Meta{}, err
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		return frontmatter.Meta{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// roleShellCommand is coordinatorShellCommand behind the pre-start
// prelude. On hook failure the note heads the role body; with no
// role body the note alone is the first prompt.
func roleShellCommand(prelude, cmdWithBody, cmdBare, rolePath string) string {
	if prelude == "" {
		return bodyShellCommand(cmdWithBody, cmdBare, rolePath)
	}
	return prelude + fmt.Sprintf(
		`if [ -r %[1]s ] && [ -s %[1]s ]; then exec %[2]s "${%[4]s:+$%[4]s

}$(cat %[1]s)"; else exec %[3]s ${%[4]s:+"$%[4]s"}; fi`,
		shellSingleQuote(rolePath), cmdWithBody, cmdBare, task.PreStartNoteVar,
	)
}

// SpawnRole creates the tmux session for spec, idempotently. A live
// session at the computed name is left alone. Returns the session
// name plus whether a spawn actually happened.
func SpawnRole(spec RoleSpawnSpec) (string, bool, error) {
	name, err := spec.SessionName()
	if err != nil {
		return "", false, err
	}
	if hasSession(name) {
		return name, false, nil
	}
	args, err := spec.TmuxArgs()
	if err != nil {
		return "", false, err
	}
	if _, err := task.EnsureRoleTaskDir(spec.ProjectRoot, spec.Slug); err != nil {
		return "", false, err
	}
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return name, true, nil
}

// ReapRole kills the tmux session for spec. Idempotent.
func ReapRole(spec RoleSpawnSpec) (bool, error) {
	name, err := spec.SessionName()
	if err != nil {
		return false, err
	}
	if !hasSession(name) {
		return false, nil
	}
	_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	return true, nil
}

// EngineerSpec builds the engineer pane spec for a task.
func EngineerSpec(projectRoot, slug string) RoleSpawnSpec {
	return RoleSpawnSpec{ProjectRoot: projectRoot, Slug: slug, Role: "engineer"}
}

// ReviewerSpec builds a reviewer pane spec for the given instance.
func ReviewerSpec(projectRoot, slug string, instance task.ReviewerInstance) RoleSpawnSpec {
	return RoleSpawnSpec{
		ProjectRoot:      projectRoot,
		Slug:             slug,
		Role:             "reviewer",
		ReviewerInstance: instance,
	}
}

func validateInstance(i task.ReviewerInstance) error {
	if i != task.ReviewerA && i != task.ReviewerB {
		return fmt.Errorf("rolespawn: reviewer instance %q must be %q or %q", i, task.ReviewerA, task.ReviewerB)
	}
	return nil
}
