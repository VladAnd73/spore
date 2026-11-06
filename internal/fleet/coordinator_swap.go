package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
)

// Seams for the swap. Tests swap them to record the tmux sequence and
// drive state.md without a live server or agent.
var (
	swapTmux = func(args ...string) ([]byte, error) {
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			return out, fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	swapSleep = time.Sleep
	swapNow   = time.Now
	// swapHostLauncher is the host coordinator launcher the NixOS module
	// installs; the reconciler units point SPORE_COORDINATOR_AGENT at it.
	swapHostLauncher = "/usr/local/bin/spore-coordinator-launch"
)

// ErrWrapTimeout is returned when state.md did not advance before the
// wrap deadline and the caller did not force the swap.
var ErrWrapTimeout = errors.New("coordinator swap: state.md not flushed before the wrap timeout")

// SwapSpec describes one coordinator swap. Session is the tmux session
// that owns the coordinator pane; everything else is read from its
// env so a swap from an operator shell targets the same paths the
// pane was spawned with.
type SwapSpec struct {
	Session string
	// To is the new driver: "claude" or "codex".
	To string
	// Model is the new model when ModelSet; see SwapModel.
	Model    string
	ModelSet bool
	// Wrap asks the live coordinator to flush state.md first.
	Wrap bool
	// Force proceeds after a wrap timeout instead of aborting.
	Force       bool
	WrapTimeout time.Duration
	// WrapSettle is how long state.md must stay unchanged after its
	// first write before the pane turns over, so a multi-edit wrap
	// finishes.
	WrapSettle time.Duration
	Poll       time.Duration
	// Log receives progress lines; nil discards them.
	Log func(string)
}

// SwapResult reports what the swap did.
type SwapResult struct {
	ProjectRoot string
	Pane        string
	Wrapped     bool
	Config      CoordinatorConfig
}

// SwapModel resolves the model to write: an explicit model wins; on a
// driver change an omitted model clears it (ids do not carry across
// providers); on the same driver it keeps the current one.
func SwapModel(cur CoordinatorConfig, to, model string, modelSet bool) string {
	if modelSet {
		return model
	}
	if cur.Driver != to {
		return ""
	}
	return cur.Model
}

// runningConfig is the driver and model the pane was launched with:
// the session env first (it beats file config at spawn), then the host
// driver override over spore.toml, then the kernel default driver.
func runningConfig(root string, env map[string]string) CoordinatorConfig {
	cfg, _ := LoadCoordinatorConfig(root)
	if v := env["SPORE_COORDINATOR_PROVIDER"]; v != "" {
		cfg.Driver = v
	}
	if v, ok := env["SPORE_COORDINATOR_MODEL"]; ok {
		cfg.Model = v
	}
	if cfg.Driver == "" {
		cfg.Driver = "claude"
	}
	return cfg
}

// SwapCoordinator flushes, writes the host-side driver override
// (DriverOverridePath; spore.toml is never touched) and respawns the
// coordinator pane of spec.Session under the new driver. Called from
// inside that pane it does not return on success: respawn-pane kills
// the caller with the pane.
func SwapCoordinator(spec SwapSpec) (SwapResult, error) {
	logf := func(format string, a ...any) {
		if spec.Log != nil {
			spec.Log(fmt.Sprintf(format, a...))
		}
	}
	if spec.To != "claude" && spec.To != "codex" {
		return SwapResult{}, fmt.Errorf("coordinator swap: --to must be claude or codex, got %q", spec.To)
	}
	env, err := readTmuxEnv("-t", exactSession(spec.Session))
	if err != nil {
		return SwapResult{}, fmt.Errorf("coordinator swap: session %s: %w", spec.Session, err)
	}
	root := env.vals["SPORE_PROJECT_ROOT"]
	if root == "" {
		return SwapResult{}, fmt.Errorf("coordinator swap: session %s has no SPORE_PROJECT_ROOT in its env", spec.Session)
	}
	pane, err := coordinatorPane(spec.Session)
	if err != nil {
		return SwapResult{}, err
	}
	res := SwapResult{ProjectRoot: root, Pane: pane}

	if spec.Wrap {
		if insideSession(spec.Session) {
			return res, fmt.Errorf("coordinator swap: running inside %s, which cannot answer its own wrap request; flush state.md, then rerun with --no-wrap", spec.Session)
		}
		inbox := env.vals["SPORE_TASK_INBOX"]
		if inbox == "" {
			if inbox, err = task.CoordinatorInboxDirForProject(root); err != nil {
				return res, err
			}
		}
		stateFile := filepath.Join(root, "state.md")
		logf("coordinator swap: asking %s to flush %s", spec.Session, stateFile)
		ok, err := requestWrap(inbox, stateFile, spec)
		if err != nil {
			return res, err
		}
		switch {
		case ok:
			res.Wrapped = true
		case spec.Force:
			logf("coordinator swap: state.md did not advance within %s; --force, swapping anyway", spec.WrapTimeout)
		default:
			return res, ErrWrapTimeout
		}
	}

	model := SwapModel(runningConfig(root, env.vals), spec.To, spec.Model, spec.ModelSet)
	plan, err := planCoordinatorDriver(root, spec.To, model)
	if err != nil {
		return res, err
	}
	res.Config = plan.cfg
	global, err := readTmuxEnv("-g")
	if err != nil {
		return res, fmt.Errorf("coordinator swap: global env: %w", err)
	}
	agent, err := swapAgent(env.vals, global.vals, spec.To)
	if err != nil {
		return res, err
	}
	rolePath := env.vals["SPORE_COORDINATOR_ROLE"]
	if rolePath == "" {
		rolePath = CoordinatorRolePath(root)
	}

	// Mutations run cheapest-to-undo first: session env, then the
	// driver override, then the respawn. A failure undoes the earlier
	// steps so the operator is not left with a flipped override and the
	// old agent still running.
	target := exactSession(spec.Session)
	for _, args := range swapEnvCommands(target, plan.cfg) {
		if _, err := swapTmux(args...); err != nil {
			return res, fmt.Errorf("coordinator swap: %w; %s", err, restoreSwapEnv(target, env, "driver override and the pane untouched"))
		}
	}
	if err := plan.write(); err != nil {
		return res, fmt.Errorf("%w; %s", err, restoreSwapEnv(target, env, "driver override and the pane untouched"))
	}
	logf("coordinator swap: %s driver=%s model=%s", plan.path, plan.cfg.Driver, displayModel(plan.cfg.Model))
	logf("coordinator swap: respawning %s (%s) with %s", spec.Session, pane, agent)
	if _, err := swapTmux(respawnCommand(pane, root, agent, rolePath)...); err != nil {
		left := "driver override restored"
		if rerr := plan.rollback(); rerr != nil {
			left = fmt.Sprintf("driver override %s NOT restored (%v), still says driver=%s", plan.path, rerr, plan.cfg.Driver)
		}
		return res, fmt.Errorf("coordinator swap: %w; %s", err, restoreSwapEnv(target, env, left+", pane untouched"))
	}
	return res, nil
}

// swapAgent picks the binary the respawned pane execs. The caller's
// env is not consulted: an operator shell lacks the reconciler's
// SPORE_COORDINATOR_AGENT, and SPORE_AGENT_BINARY is the worker
// launcher. The session env carries the agent EnsureCoordinator was
// given; the global env holds what the tmux server inherited (usually
// the reconciler's unit env). Sessions spawned before 0.22.0 lack the
// former, and a tmux server started from an ssh login lacks the latter,
// so the host launcher comes next when installed: a bare codex skips
// its inbox sidecar and never hears the coordinator inbox. Without any
// of these the driver names the binary. A bare claude or codex that contradicts the target driver is
// an error, not a silent wrong-agent respawn.
func swapAgent(session, global map[string]string, to string) (string, error) {
	agent := strings.TrimSpace(session[CoordinatorAgentEnv])
	if agent == "" {
		agent = strings.TrimSpace(global[CoordinatorAgentEnv])
	}
	if agent == "" {
		if fi, err := os.Stat(swapHostLauncher); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return swapHostLauncher, nil
		}
		return driverToBinary(to), nil
	}
	fields := strings.Fields(agent)
	switch base := filepath.Base(fields[0]); {
	case (base == "claude" || base == "claude-code") && to != "claude",
		base == "codex" && to != "codex":
		return "", fmt.Errorf("coordinator swap: %s=%q runs %s, not %s; point it at a launcher that reads SPORE_COORDINATOR_PROVIDER (spore-coordinator-launch)", CoordinatorAgentEnv, agent, base, to)
	}
	return agent, nil
}

// exactSession turns a session name into a target that matches only
// that name: a bare name falls back to prefix and fnmatch lookup, which
// could hit spore/<p>/coordinator2.
func exactSession(session string) string {
	return "=" + session
}

// swapEnvCommands sets provider and model in the session env, which
// respawn-pane layers over the global env. A cleared model is marked -r
// so a value inherited by the tmux server (e.g. from
// /etc/spore/coordinator.env) cannot leak back in.
func swapEnvCommands(target string, cfg CoordinatorConfig) [][]string {
	model := []string{"set-environment", "-t", target, "-r", "SPORE_COORDINATOR_MODEL"}
	if cfg.Model != "" {
		model = []string{"set-environment", "-t", target, "SPORE_COORDINATOR_MODEL", cfg.Model}
	}
	return [][]string{
		{"set-environment", "-t", target, "SPORE_COORDINATOR_PROVIDER", cfg.Driver},
		model,
	}
}

func respawnCommand(pane, root, agent, rolePath string) []string {
	return []string{"respawn-pane", "-k", "-t", pane, "-c", root, coordinatorShellCommand(agent, rolePath)}
}

// restoreSwapEnv puts provider and model back as they were and returns
// a description of the state left behind, for the error message.
func restoreSwapEnv(target string, prev tmuxEnv, left string) string {
	var failed []string
	for _, k := range []string{"SPORE_COORDINATOR_PROVIDER", "SPORE_COORDINATOR_MODEL"} {
		args := []string{"set-environment", "-t", target, "-u", k}
		if v, ok := prev.vals[k]; ok {
			args = []string{"set-environment", "-t", target, k, v}
		} else if prev.removed[k] {
			args = []string{"set-environment", "-t", target, "-r", k}
		}
		if _, err := swapTmux(args...); err != nil {
			failed = append(failed, k)
		}
	}
	if len(failed) > 0 {
		return fmt.Sprintf("%s; session env NOT restored for %s, fix with tmux set-environment before the next respawn", left, strings.Join(failed, ", "))
	}
	return left + ", session env restored"
}

// requestWrap drops a wrap-up envelope into the coordinator inbox and
// waits for stateFile's mtime to advance and then settle. A Claude
// coordinator picks it up through its inbox hook, a Codex one through
// the watch-inbox sidecar. Returns false when state.md has not
// advanced and settled by the deadline.
func requestWrap(inbox, stateFile string, spec SwapSpec) (bool, error) {
	before := fileMtime(stateFile)
	msg := fmt.Sprintf("spore coordinator swap: this pane is about to be respawned as a %s coordinator. "+
		"Wrap up now: flush everything the next coordinator needs into %s (in-flight work, open decisions, next steps), "+
		"then end your turn. Do not start new work; the pane turns over once state.md is written.",
		spec.To, stateFile)
	if err := task.TellDir(inbox, CoordinatorSlug, msg); err != nil {
		return false, fmt.Errorf("coordinator swap: write wrap request to %s: %w", inbox, err)
	}
	poll := spec.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	deadline := swapNow().Add(spec.WrapTimeout)
	var seen time.Time
	var stableSince time.Time
	for {
		m := fileMtime(stateFile)
		if m.After(before) {
			if !m.Equal(seen) {
				seen, stableSince = m, swapNow()
			}
			if swapNow().Sub(stableSince) >= spec.WrapSettle {
				return true, nil
			}
		}
		if !swapNow().Before(deadline) {
			return false, nil
		}
		swapSleep(poll)
	}
}

func fileMtime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// tmuxEnv is a tmux environment table. removed holds "-VAR" entries:
// variables tmux strips from processes it starts.
type tmuxEnv struct {
	vals    map[string]string
	removed map[string]bool
}

// readTmuxEnv runs show-environment with scope args (-t <session> or
// -g).
func readTmuxEnv(scope ...string) (tmuxEnv, error) {
	out, err := swapTmux(append([]string{"show-environment"}, scope...)...)
	if err != nil {
		return tmuxEnv{}, err
	}
	env := tmuxEnv{vals: map[string]string{}, removed: map[string]bool{}}
	for _, line := range strings.Split(string(out), "\n") {
		if k, ok := strings.CutPrefix(line, "-"); ok {
			env.removed[k] = true
		} else if k, v, ok := strings.Cut(line, "="); ok {
			env.vals[k] = v
		}
	}
	return env, nil
}

// coordinatorPane returns the id of the session's first pane: the one
// new-session started the agent in. Ids survive window renumbering and
// base-index settings that a "<session>:0" target would not.
func coordinatorPane(session string) (string, error) {
	out, err := swapTmux("list-panes", "-s", "-t", exactSession(session), "-F", "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("coordinator swap: %w", err)
	}
	pane, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if pane == "" {
		return "", fmt.Errorf("coordinator swap: session %s has no panes", session)
	}
	return pane, nil
}

func insideSession(session string) bool {
	pane := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || pane == "" {
		return false
	}
	out, err := swapTmux("display-message", "-p", "-t", pane, "#{session_name}")
	return err == nil && strings.TrimSpace(string(out)) == session
}

func displayModel(m string) string {
	if m == "" {
		return "(cli default)"
	}
	return m
}
