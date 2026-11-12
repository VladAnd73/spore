package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
)

// fakePanes stands in for tmux: probes read from panes, spawns create
// a healthy pane, kills delete it.
type fakePanes struct {
	panes  map[string]*paneProbe
	spawns []string
	kills  []string
	now    time.Time
}

func stubPanes(t *testing.T) *fakePanes {
	t.Helper()
	f := &fakePanes{panes: map[string]*paneProbe{}, now: time.Unix(1_800_000_000, 0)}
	prevSpawn, prevProbe, prevKill, prevNow := spawnRole, probeRolePane, killRoleSession, nowFunc
	spawnRole = func(spec RoleSpawnSpec) (string, bool, error) {
		s, err := spec.SessionName()
		if err != nil {
			return "", false, err
		}
		if _, ok := f.panes[s]; ok {
			return s, false, nil
		}
		f.spawns = append(f.spawns, s)
		f.panes[s] = &paneProbe{Exists: true, Command: "claude", Created: f.now, Content: "booting"}
		return s, true, nil
	}
	probeRolePane = func(s string) (paneProbe, error) {
		if p, ok := f.panes[s]; ok {
			return *p, nil
		}
		return paneProbe{}, nil
	}
	killRoleSession = func(s string) {
		f.kills = append(f.kills, s)
		delete(f.panes, s)
	}
	nowFunc = func() time.Time { return f.now }
	t.Cleanup(func() {
		spawnRole, probeRolePane, killRoleSession, nowFunc = prevSpawn, prevProbe, prevKill, prevNow
	})
	return f
}

func livenessFixture(t *testing.T) (root, inbox string) {
	t.Helper()
	root = newGitRepoFor(t, "demo-project")
	stateDir := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", stateDir)
	if _, err := task.EnsureRoleTaskDir(root, "demo"); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(stateDir, "demo-project", "inbox")
}

func envBodies(t *testing.T, inbox string) []string {
	t.Helper()
	var out []string
	for _, e := range readInboxEnvelopes(t, inbox) {
		out = append(out, e.Body)
	}
	return out
}

func TestAgentGone(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		name string
		p    paneProbe
		want string
	}{
		{"agent running", paneProbe{Command: "claude", Created: now.Add(-time.Hour)}, ""},
		{"version-titled agent", paneProbe{Command: "2.1.199", Created: now.Add(-time.Hour)}, ""},
		{"pane dead", paneProbe{Dead: true, Command: "claude", Created: now}, "pane dead"},
		{"shell in grace", paneProbe{Command: "sh", Created: now.Add(-5 * time.Second)}, ""},
		{"shell after grace", paneProbe{Command: "bash", Created: now.Add(-time.Minute)}, "agent exited to bash"},
		{"login shell", paneProbe{Command: "-zsh", Created: now.Add(-time.Minute)}, "agent exited to -zsh"},
	}
	for _, c := range cases {
		if got := agentGone(c.p, now); got != c.want {
			t.Errorf("%s: agentGone = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEnsureRolePaneSpawnsOnceAndLeavesHealthyPane(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	for range 3 {
		if err := ensureRolePane(spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.spawns) != 1 || len(f.kills) != 0 {
		t.Errorf("spawns=%v kills=%v, want one spawn, no kills", f.spawns, f.kills)
	}
	if bodies := envBodies(t, inbox); len(bodies) != 0 {
		t.Errorf("initial spawn notified: %v", bodies)
	}
}

func TestEnsureRolePaneRespawnsDeadAgent(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	session, _ := spec.SessionName()
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(time.Minute)
	f.panes[session].Command = "bash"
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.kills) != 1 || len(f.spawns) != 2 {
		t.Fatalf("kills=%v spawns=%v, want 1 kill and 2 spawns", f.kills, f.spawns)
	}
	bodies := envBodies(t, inbox)
	want := "demo: respawned engineer (agent exited to bash; round 1, respawn 1/3)"
	if len(bodies) != 1 || bodies[0] != want {
		t.Errorf("notices = %q, want [%q]", bodies, want)
	}
}

// While the pre-start hook runs (or waits on the slug lock) the pane
// shows the launch shell past spawnGrace; the marker keeps liveness
// off it. A marker older than the session is a leftover and is ignored.
func TestEnsureRolePaneSkipsWhilePreStartRuns(t *testing.T) {
	root, _ := livenessFixture(t)
	f := stubPanes(t)
	spec := ReviewerSpec(root, "demo", task.ReviewerB)
	session, _ := spec.SessionName()
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	created := f.panes[session].Created
	marker := task.PreStartMarkerPath(root, "demo", "reviewer-B")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(marker, created, created); err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(5 * time.Minute)
	f.panes[session].Command = "bash"
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.kills) != 0 {
		t.Fatalf("killed a pane mid pre-start: %v", f.kills)
	}

	stale := created.Add(-time.Minute)
	if err := os.Chtimes(marker, stale, stale); err != nil {
		t.Fatal(err)
	}
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.kills) != 1 {
		t.Fatalf("stale marker kept a dead agent alive: kills=%v", f.kills)
	}
}

// A hand-started pane has no spawn marker; its death must still reach
// the coordinator.
func TestEnsureRolePaneNotifiesHandStartedDeath(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := ReviewerSpec(root, "demo", task.ReviewerB)
	session, _ := spec.SessionName()
	f.panes[session] = &paneProbe{Exists: true, Dead: true}
	if err := ensureRolePane(spec, 1); err != nil {
		t.Fatal(err)
	}
	bodies := envBodies(t, inbox)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "respawned reviewer-B (pane dead; round 2, respawn 1/3)") {
		t.Errorf("notices = %q", bodies)
	}
}

func TestEnsureRolePaneCapsRespawnsPerRound(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	session, _ := spec.SessionName()

	die := func() {
		f.now = f.now.Add(time.Minute)
		f.panes[session].Dead = true
	}
	if err := ensureRolePane(spec, 2); err != nil {
		t.Fatal(err)
	}
	for range MaxRoleRespawns {
		die()
		if err := ensureRolePane(spec, 2); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.spawns) != 1+MaxRoleRespawns || len(f.kills) != MaxRoleRespawns {
		t.Fatalf("spawns=%d kills=%d, want %d and %d", len(f.spawns), len(f.kills), 1+MaxRoleRespawns, MaxRoleRespawns)
	}

	die()
	for range 2 {
		if err := ensureRolePane(spec, 2); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.spawns) != 1+MaxRoleRespawns || len(f.kills) != MaxRoleRespawns {
		t.Errorf("past cap: spawns=%d kills=%d, want no further spawn or kill", len(f.spawns), len(f.kills))
	}
	bodies := envBodies(t, inbox)
	if len(bodies) != MaxRoleRespawns+1 {
		t.Fatalf("notices = %d, want %d: %q", len(bodies), MaxRoleRespawns+1, bodies)
	}
	var escalations int
	for _, b := range bodies {
		if strings.Contains(b, "escalated: engineer agent died again after 3 respawns in round 3") {
			escalations++
		}
	}
	if escalations != 1 {
		t.Errorf("escalation notices = %d, want 1: %q", escalations, bodies)
	}

	// A new round re-arms the cap.
	if err := ensureRolePane(spec, 3); err != nil {
		t.Fatal(err)
	}
	if len(f.kills) != 1+MaxRoleRespawns || len(f.spawns) != 2+MaxRoleRespawns {
		t.Errorf("next round: kills=%d spawns=%d, want dead pane replaced", len(f.kills), len(f.spawns))
	}
}

// A token-monitor wrap kills its own session. Any number of those in
// one round must respawn silently and leave the cap untouched.
func TestEnsureRolePaneMissingSessionNeverCounts(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	session, _ := spec.SessionName()
	for range 2 * MaxRoleRespawns {
		if err := ensureRolePane(spec, 0); err != nil {
			t.Fatal(err)
		}
		delete(f.panes, session)
	}
	if len(f.spawns) != 2*MaxRoleRespawns {
		t.Fatalf("spawns = %d, want %d", len(f.spawns), 2*MaxRoleRespawns)
	}
	if bodies := envBodies(t, inbox); len(bodies) != 0 {
		t.Errorf("missing-session respawns notified: %q", bodies)
	}

	// The cap is still whole for a real agent death.
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	f.panes[session].Dead = true
	if err := ensureRolePane(spec, 0); err != nil {
		t.Fatal(err)
	}
	bodies := envBodies(t, inbox)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "respawn 1/3") {
		t.Errorf("notices = %q, want one respawn 1/3", bodies)
	}
}

// After A approves, the drive must bring B up even when nothing was
// started by hand, and keep the engineer pane alive.
func TestDriveRoleLoopSpawnsReviewerAfterTransition(t *testing.T) {
	root, _ := livenessFixture(t)
	tasksDir := setupRoleLoopDriveFixture(t, root, "demo")
	f := stubPanes(t)
	if err := task.WriteEngineerResponse(root, "demo", 1, task.EngineerResponse{Notes: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := DriveRoleLoop(root, tasksDir, "demo"); err != nil {
		t.Fatal(err)
	}
	mustWriteReview(t, root, "demo", task.ReviewerA, 1, task.Review{Verdict: task.VerdictApprove, Summary: "ok"})
	snap, err := DriveRoleLoop(root, tasksDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PhaseReviewB {
		t.Fatalf("phase = %s, want review-B", snap.Phase)
	}
	eng, _ := EngineerSpec(root, "demo").SessionName()
	revB, _ := ReviewerSpec(root, "demo", task.ReviewerB).SessionName()
	if f.panes[eng] == nil || f.panes[revB] == nil {
		t.Errorf("panes after transition = %v, want engineer and reviewer-B", f.spawns)
	}

	// B's pane dies right after spawn: the next tick replaces it.
	f.panes[revB].Dead = true
	if _, err := DriveRoleLoop(root, tasksDir, "demo"); err != nil {
		t.Fatal(err)
	}
	if len(f.kills) != 1 || f.kills[0] != revB || f.panes[revB] == nil || f.panes[revB].Dead {
		t.Errorf("kills=%v, want reviewer-B respawned", f.kills)
	}
}

func TestCheckRoleStallNotifiesOncePerQuietSpell(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	session, _ := spec.SessionName()
	f.panes[session] = &paneProbe{Exists: true, Command: "claude", Created: f.now, Content: "idle\n───\n❯ \n───\nstatus 10:00"}
	expected := task.EngineerResponsePath(root, "demo", 1)
	window := 20 * time.Minute
	tick := func(after time.Duration) {
		t.Helper()
		f.now = f.now.Add(after)
		if err := checkRoleStall(spec, 0, expected, window); err != nil {
			t.Fatal(err)
		}
	}

	tick(0)
	f.panes[session].Content = "idle\n───\n❯ \n───\nstatus 10:05"
	tick(19 * time.Minute)
	if n := len(envBodies(t, inbox)); n != 0 {
		t.Fatalf("notices before window = %d", n)
	}
	tick(time.Minute)
	tick(5 * time.Minute)
	bodies := envBodies(t, inbox)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "demo: stalled: engineer has not written engineer-round-1.json") {
		t.Fatalf("notices = %q, want one stall", bodies)
	}

	// Activity re-arms; a second quiet spell notifies again.
	f.panes[session].Content = "working\n───\n❯ \n───"
	tick(time.Minute)
	tick(10 * time.Minute)
	if n := len(envBodies(t, inbox)); n != 1 {
		t.Errorf("notices mid-spell = %d, want 1", n)
	}
	tick(10 * time.Minute)
	if n := len(envBodies(t, inbox)); n != 2 {
		t.Errorf("notices after second spell = %d, want 2", n)
	}

	// A role file write counts as activity.
	writeRaw(t, filepath.Join(task.RoleTaskDir(root, "demo"), "responses", "scratch.txt"), "x")
	tick(25 * time.Minute)
	if n := len(envBodies(t, inbox)); n != 2 {
		t.Errorf("notices after file activity = %d, want 2", n)
	}
}

func TestCheckRoleStallSkipsWhenDisabledOrDone(t *testing.T) {
	root, inbox := livenessFixture(t)
	f := stubPanes(t)
	spec := EngineerSpec(root, "demo")
	session, _ := spec.SessionName()
	f.panes[session] = &paneProbe{Exists: true, Command: "claude", Created: f.now, Content: "idle"}
	expected := task.EngineerResponsePath(root, "demo", 1)
	for range 3 {
		f.now = f.now.Add(time.Hour)
		if err := checkRoleStall(spec, 0, expected, 0); err != nil {
			t.Fatal(err)
		}
	}
	writeRaw(t, expected, "{}")
	for range 3 {
		f.now = f.now.Add(time.Hour)
		if err := checkRoleStall(spec, 0, expected, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(envBodies(t, inbox)); n != 0 {
		t.Errorf("notices = %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(task.RoleTaskDir(root, "demo"), "state", "activity-engineer-r0")); !os.IsNotExist(err) {
		t.Errorf("activity state written while disabled or done: %v", err)
	}
}

func TestTranscriptPartDropsInputAndStatusLine(t *testing.T) {
	pane := "line one\n✻ Working (3s)\n────\n❯ draft\n────\nstatus 10:00"
	if got := transcriptPart(pane); got != "line one\n✻ Working (3s)" {
		t.Errorf("transcriptPart = %q", got)
	}
	if got := transcriptPart("no prompt"); got != "no prompt" {
		t.Errorf("transcriptPart without prompt = %q", got)
	}
}

func TestLoadRoleStallMinutes(t *testing.T) {
	cases := []struct {
		toml    string
		want    int
		wantErr bool
	}{
		{"", DefaultRoleStallMinutes, false},
		{"[fleet]\nmax_workers = 2\n", DefaultRoleStallMinutes, false},
		{"[fleet]\nrole_stall_minutes = 45 # slow suite\n", 45, false},
		{"[fleet]\nrole_stall_minutes = 0\n", 0, false},
		{"[fleet]\nrole_stall_minutes = soon\n", DefaultRoleStallMinutes, true},
		{"[fleet]\nrole_stall_minutes = -1\n", DefaultRoleStallMinutes, true},
	}
	for _, c := range cases {
		root := t.TempDir()
		if c.toml != "" {
			if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(c.toml), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := LoadRoleStallMinutes(root)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%q: got %d, %v; want %d, err=%v", c.toml, got, err, c.want, c.wantErr)
		}
	}
}

// TestProbeRolePaneTmux runs the real probe against the isolated tmux
// server TestMain starts: a running agent, a pane left dead by
// remain-on-exit, and a missing session.
func TestProbeRolePaneTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	for _, cmd := range []*exec.Cmd{
		exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", "probe-live", "exec sleep 300"),
		// Chained in one client call so the option lands before the
		// pane can exit and take the session with it.
		exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", "probe-dead", "sleep 1",
			";", "set-option", "-t", "probe-dead", "remain-on-exit", "on"),
	} {
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v: %s", cmd.Args, err, out)
		}
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", "probe-live").Run()
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", "probe-dead").Run()
	})

	// Until the exec lands, tmux reports the forked server or the shell
	// as pane_current_command.
	var live paneProbe
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		live, err = probeRolePaneTmux("probe-live")
		if err != nil {
			t.Fatal(err)
		}
		if live.Command == "sleep" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !live.Exists || live.Dead || live.Command != "sleep" || live.Created.IsZero() {
		t.Errorf("live probe = %+v", live)
	}
	if reason := agentGone(live, time.Now().Add(time.Hour)); reason != "" {
		t.Errorf("live pane judged gone: %s", reason)
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		dead, err := probeRolePaneTmux("probe-dead")
		if err != nil {
			t.Fatal(err)
		}
		if dead.Dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reported dead: %+v", dead)
		}
		time.Sleep(100 * time.Millisecond)
	}

	missing, err := probeRolePaneTmux("probe-missing")
	if err != nil || missing.Exists {
		t.Errorf("missing probe = %+v, %v", missing, err)
	}
}
