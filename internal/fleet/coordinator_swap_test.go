package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeSwapTmux struct {
	env, global string
	calls       [][]string
	// fail, when set, fails any call it returns true for.
	fail func(args []string) bool
}

func (f *fakeSwapTmux) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.fail != nil && f.fail(args) {
		return nil, errors.New("injected tmux failure")
	}
	switch args[0] {
	case "show-environment":
		if args[1] == "-g" {
			return []byte(f.global), nil
		}
		return []byte(f.env), nil
	case "list-panes":
		return []byte("%7\n%9\n"), nil
	case "display-message":
		return []byte("spore/proj/coordinator\n"), nil
	}
	return nil, nil
}

// mutations drops the read-only probes so tests compare the commands
// that change tmux state.
func (f *fakeSwapTmux) mutations() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] != "show-environment" && c[0] != "list-panes" && c[0] != "display-message" {
			out = append(out, c)
		}
	}
	return out
}

type swapFixture struct {
	root, inbox, session string
	tmux                 *fakeSwapTmux
	now                  time.Time
	onSleep              func()
}

func newSwapFixture(t *testing.T, toml, sessionEnvExtra string) *swapFixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := &swapFixture{
		root:    t.TempDir(),
		inbox:   filepath.Join(t.TempDir(), "inbox"),
		session: "spore/proj/coordinator",
		now:     time.Unix(1_800_000_000, 0),
	}
	if toml != "" {
		if err := os.WriteFile(filepath.Join(f.root, "spore.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.tmux = &fakeSwapTmux{env: "SPORE_PROJECT_ROOT=" + f.root + "\n" +
		"SPORE_TASK_INBOX=" + f.inbox + "\n" +
		"SPORE_COORDINATOR_ROLE=/r/role.md\n" +
		"-SOMETHING_REMOVED\n" + sessionEnvExtra,
		global: "SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch\nHOME=/home/x\n"}
	t.Setenv("TMUX", "")
	// The caller's env must not pick the agent: an operator shell
	// has neither of these, a worker shell has the worker launcher.
	t.Setenv(CoordinatorAgentEnv, "/caller/agent")
	t.Setenv("SPORE_AGENT_BINARY", "/caller/spore-worker-brief")
	prevTmux, prevSleep, prevNow, prevLauncher := swapTmux, swapSleep, swapNow, swapHostLauncher
	swapHostLauncher = filepath.Join(t.TempDir(), "absent-launcher")
	swapTmux = f.tmux.run
	swapNow = func() time.Time { return f.now }
	swapSleep = func(d time.Duration) {
		f.now = f.now.Add(d)
		if f.onSleep != nil {
			f.onSleep()
		}
	}
	t.Cleanup(func() {
		swapTmux, swapSleep, swapNow, swapHostLauncher = prevTmux, prevSleep, prevNow, prevLauncher
	})
	return f
}

func (f *swapFixture) readTOML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "spore.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *swapFixture) overridePath(t *testing.T) string {
	t.Helper()
	p, err := DriverOverridePath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *swapFixture) readOverride(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.overridePath(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *swapFixture) noOverride(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.overridePath(t)); !os.IsNotExist(err) {
		t.Errorf("driver override exists: %v", err)
	}
}

func TestSwapCoordinatorToCodexClearsModel(t *testing.T) {
	const toml = "# project config\n[coordinator]\ndriver = \"claude\" # pilot\nmodel = \"opus\"\nbrief = \"role.md\"\n\n[fleet]\nmax_workers = 2\n"
	f := newSwapFixture(t, toml, "SPORE_COORDINATOR_PROVIDER=claude\nSPORE_COORDINATOR_MODEL=opus\n")

	res, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.readTOML(t); got != toml {
		t.Errorf("spore.toml changed:\n%q", got)
	}
	if got, want := f.readOverride(t), "[coordinator]\ndriver = \"codex\"\n"; got != want {
		t.Errorf("driver override:\n%q\nwant:\n%q", got, want)
	}
	st, err := os.Stat(f.overridePath(t))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("override mode = %v, err %v", st, err)
	}
	if st, err := os.Stat(filepath.Dir(f.overridePath(t))); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("override dir mode = %v, err %v", st, err)
	}
	cfg, err := LoadCoordinatorConfig(f.root)
	if err != nil || cfg != (CoordinatorConfig{Driver: "codex", Brief: "role.md"}) {
		t.Errorf("reloaded config = %+v, err %v", cfg, err)
	}
	if res.Config.Driver != "codex" || res.Config.Model != "" || res.Config.Brief != "role.md" || res.Pane != "%7" {
		t.Errorf("result = %+v", res)
	}
	exact := "=" + f.session
	want := [][]string{
		{"set-environment", "-t", exact, "SPORE_COORDINATOR_PROVIDER", "codex"},
		{"set-environment", "-t", exact, "-r", "SPORE_COORDINATOR_MODEL"},
		{"respawn-pane", "-k", "-t", "%7", "-c", f.root,
			coordinatorShellCommand("/usr/local/bin/spore-coordinator-launch", "/r/role.md")},
	}
	if got := f.tmux.mutations(); !reflect.DeepEqual(got, want) {
		t.Errorf("tmux sequence:\n%q\nwant:\n%q", got, want)
	}
	if f.tmux.calls[0][0] != "show-environment" || f.tmux.calls[0][2] != exact {
		t.Errorf("first call should read the target session env: %q", f.tmux.calls[0])
	}
}

func TestSwapCoordinatorModelRules(t *testing.T) {
	cases := []struct {
		name, toml, env, to, model string
		modelSet                   bool
		wantModel                  string
	}{
		{"explicit model", "", "", "codex", "gpt-5.5", true, "gpt-5.5"},
		{"same driver keeps model", "[coordinator]\ndriver = \"codex\"\nmodel = \"gpt-5.5\"\n", "", "codex", "", false, "gpt-5.5"},
		{"session env beats toml", "[coordinator]\ndriver = \"claude\"\nmodel = \"opus\"\n", "SPORE_COORDINATOR_PROVIDER=codex\nSPORE_COORDINATOR_MODEL=gpt-5.5\n", "codex", "", false, "gpt-5.5"},
		{"default driver is claude", "", "", "claude", "", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSwapFixture(t, c.toml, c.env)
			res, err := SwapCoordinator(SwapSpec{Session: f.session, To: c.to, Model: c.model, ModelSet: c.modelSet})
			if err != nil {
				t.Fatal(err)
			}
			if res.Config.Model != c.wantModel {
				t.Errorf("model = %q, want %q", res.Config.Model, c.wantModel)
			}
			muts := f.tmux.mutations()
			wantEnv := []string{"set-environment", "-t", "=" + f.session, "SPORE_COORDINATOR_MODEL", c.wantModel}
			if c.wantModel == "" {
				wantEnv = []string{"set-environment", "-t", "=" + f.session, "-r", "SPORE_COORDINATOR_MODEL"}
			}
			if !reflect.DeepEqual(muts[1], wantEnv) {
				t.Errorf("model env = %q, want %q", muts[1], wantEnv)
			}
		})
	}
}

func TestSwapCoordinatorWrapWaitsForStateMD(t *testing.T) {
	f := newSwapFixture(t, "[coordinator]\ndriver = \"claude\"\n", "")
	state := filepath.Join(f.root, "state.md")
	old := f.now.Add(-time.Hour)
	if err := os.WriteFile(state, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(state, old, old); err != nil {
		t.Fatal(err)
	}
	sleeps := 0
	f.onSleep = func() {
		sleeps++
		if sleeps == 3 {
			if err := os.WriteFile(state, []byte("flushed"), 0o644); err != nil {
				t.Fatal(err)
			}
			_ = os.Chtimes(state, f.now, f.now)
		}
	}

	res, err := SwapCoordinator(SwapSpec{
		Session: f.session, To: "codex", Wrap: true,
		WrapTimeout: time.Minute, WrapSettle: 10 * time.Second, Poll: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Wrapped {
		t.Error("Wrapped = false")
	}
	if sleeps < 3+5 {
		t.Errorf("swapped after %d polls; want the 10s settle after the write", sleeps)
	}
	entries, err := os.ReadDir(f.inbox)
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox entries = %v, err %v", entries, err)
	}
	body, _ := os.ReadFile(filepath.Join(f.inbox, entries[0].Name()))
	if !strings.Contains(string(body), "codex coordinator") || !strings.Contains(string(body), "state.md") {
		t.Errorf("wrap envelope = %s", body)
	}
	if len(f.tmux.mutations()) != 3 {
		t.Errorf("tmux mutations = %q", f.tmux.mutations())
	}
}

func TestSwapCoordinatorWrapTimeout(t *testing.T) {
	const toml = "[coordinator]\ndriver = \"claude\"\n"
	spec := func(f *swapFixture, force bool) SwapSpec {
		return SwapSpec{Session: f.session, To: "codex", Wrap: true, Force: force,
			WrapTimeout: 30 * time.Second, WrapSettle: 10 * time.Second, Poll: 2 * time.Second}
	}

	f := newSwapFixture(t, toml, "")
	if _, err := SwapCoordinator(spec(f, false)); !errors.Is(err, ErrWrapTimeout) {
		t.Fatalf("err = %v, want ErrWrapTimeout", err)
	}
	if got := f.readTOML(t); got != toml {
		t.Errorf("spore.toml changed on abort: %q", got)
	}
	f.noOverride(t)
	if m := f.tmux.mutations(); len(m) != 0 {
		t.Errorf("tmux mutated on abort: %q", m)
	}

	f = newSwapFixture(t, toml, "")
	res, err := SwapCoordinator(spec(f, true))
	if err != nil {
		t.Fatal(err)
	}
	if res.Wrapped || res.Config.Driver != "codex" || len(f.tmux.mutations()) != 3 {
		t.Errorf("forced swap: res %+v, mutations %q", res, f.tmux.mutations())
	}
}

func TestSwapCoordinatorRejects(t *testing.T) {
	f := newSwapFixture(t, "", "")
	if _, err := SwapCoordinator(SwapSpec{Session: f.session, To: "gemini"}); err == nil {
		t.Error("unknown driver accepted")
	}
	f.tmux.env = "SPORE_TASK_SLUG=coordinator\n"
	if _, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex"}); err == nil {
		t.Error("session without SPORE_PROJECT_ROOT accepted")
	}
	if len(f.tmux.mutations()) != 0 {
		t.Errorf("tmux mutated: %q", f.tmux.mutations())
	}
}

func TestSwapCoordinatorInsidePaneNeedsNoWrap(t *testing.T) {
	f := newSwapFixture(t, "", "")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%7")
	_, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex", Wrap: true, WrapTimeout: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "--no-wrap") {
		t.Fatalf("err = %v, want a --no-wrap hint", err)
	}
	if _, err := os.Stat(f.inbox); !os.IsNotExist(err) {
		t.Error("wrap request written from inside the pane")
	}
	if _, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex"}); err != nil {
		t.Fatalf("--no-wrap from inside the pane: %v", err)
	}
}

func TestSwapCoordinatorWrapUnsettledAtDeadlineTimesOut(t *testing.T) {
	f := newSwapFixture(t, "[coordinator]\ndriver = \"claude\"\n", "")
	state := filepath.Join(f.root, "state.md")
	sleeps := 0
	f.onSleep = func() {
		sleeps++
		// First write lands 4s before a 30s deadline; the 10s settle
		// cannot finish in time.
		if sleeps == 13 {
			if err := os.WriteFile(state, []byte("half"), 0o644); err != nil {
				t.Fatal(err)
			}
			_ = os.Chtimes(state, f.now, f.now)
		}
	}
	_, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex", Wrap: true,
		WrapTimeout: 30 * time.Second, WrapSettle: 10 * time.Second, Poll: 2 * time.Second})
	if !errors.Is(err, ErrWrapTimeout) {
		t.Fatalf("err = %v, want ErrWrapTimeout", err)
	}
	if len(f.tmux.mutations()) != 0 {
		t.Errorf("tmux mutated: %q", f.tmux.mutations())
	}
}

func TestSwapAgentPrecedence(t *testing.T) {
	const launch = "/usr/local/bin/spore-coordinator-launch"
	cases := []struct {
		name, sessionEnv, global, to, want string
		wantErr, hostLauncher              bool
	}{
		{"host launcher before driver binary", "", "", "codex", "", false, true},
		{"global env beats host launcher", "", "SPORE_COORDINATOR_AGENT=/opt/global-launch\n", "codex", "/opt/global-launch", false, true},
		{"session env wins", "SPORE_COORDINATOR_AGENT=/opt/session-launch\n", "SPORE_COORDINATOR_AGENT=" + launch + "\n", "codex", "/opt/session-launch", false, false},
		{"global env next", "", "SPORE_COORDINATOR_AGENT=" + launch + "\n", "codex", launch, false, false},
		{"driver binary last", "", "", "codex", "codex", false, false},
		{"driver binary claude", "", "", "claude", "claude", false, false},
		{"bare claude agent cannot run codex", "SPORE_COORDINATOR_AGENT=claude\n", "", "codex", "", true, false},
		{"bare codex agent cannot run claude", "", "SPORE_COORDINATOR_AGENT=/run/current-system/sw/bin/codex -m x\n", "claude", "", true, false},
		{"whitespace session env falls through to global", "SPORE_COORDINATOR_AGENT=  \t \n", "SPORE_COORDINATOR_AGENT=" + launch + "\n", "codex", launch, false, false},
		{"whitespace session and global fall through to host launcher", "SPORE_COORDINATOR_AGENT=   \n", "SPORE_COORDINATOR_AGENT= \t\n", "codex", "", false, true},
		{"whitespace session and global fall through to driver binary", "SPORE_COORDINATOR_AGENT=   \n", "SPORE_COORDINATOR_AGENT= \t\n", "claude", "claude", false, false},
		{"matching bare agent is fine", "SPORE_COORDINATOR_AGENT=claude\n", "", "claude", "claude", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSwapFixture(t, "", c.sessionEnv)
			f.tmux.global = c.global
			if c.hostLauncher {
				swapHostLauncher = filepath.Join(t.TempDir(), "spore-coordinator-launch")
				if err := os.WriteFile(swapHostLauncher, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				if c.want == "" {
					c.want = swapHostLauncher
				}
			}
			_, err := SwapCoordinator(SwapSpec{Session: f.session, To: c.to})
			if c.wantErr {
				if err == nil {
					t.Fatal("want an agent/driver mismatch error")
				}
				if len(f.tmux.mutations()) != 0 {
					t.Errorf("tmux mutated: %q", f.tmux.mutations())
				}
				f.noOverride(t)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			muts := f.tmux.mutations()
			if got := muts[len(muts)-1][len(muts[len(muts)-1])-1]; got != coordinatorShellCommand(c.want, "/r/role.md") {
				t.Errorf("respawn command = %q, want agent %q", got, c.want)
			}
		})
	}
}

func TestSwapCoordinatorRollsBackOnTmuxFailure(t *testing.T) {
	const toml = "[coordinator]\ndriver = \"claude\" # keep\nmodel = \"opus\"\n"
	exact := "=spore/proj/coordinator"
	restore := [][]string{
		{"set-environment", "-t", exact, "SPORE_COORDINATOR_PROVIDER", "claude"},
		{"set-environment", "-t", exact, "-r", "SPORE_COORDINATOR_MODEL"},
	}
	const prior = "# swapped earlier\n[coordinator]\ndriver = \"claude\"\n"
	cases := []struct {
		name      string
		failOn    string
		prior     string
		wantInErr string
		wantMuts  int
	}{
		{"respawn fails", "respawn-pane", "", "driver override restored, pane untouched, session env restored", 5},
		{"respawn fails over a prior override", "respawn-pane", prior, "driver override restored, pane untouched, session env restored", 5},
		{"set-environment fails", "set-environment", "", "driver override and the pane untouched", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSwapFixture(t, toml, "SPORE_COORDINATOR_PROVIDER=claude\n-SPORE_COORDINATOR_MODEL\n")
			if c.prior != "" {
				if err := writePrivateFile(f.overridePath(t), c.prior); err != nil {
					t.Fatal(err)
				}
			}
			first := true
			f.tmux.fail = func(args []string) bool {
				if args[0] != c.failOn || !first {
					return false
				}
				first = false
				return true
			}
			_, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex", Model: "gpt-5.5", ModelSet: true})
			if err == nil || !strings.Contains(err.Error(), c.wantInErr) {
				t.Fatalf("err = %v, want it to say %q", err, c.wantInErr)
			}
			if got := f.readTOML(t); got != toml {
				t.Errorf("spore.toml changed: %q", got)
			}
			if c.prior == "" {
				f.noOverride(t)
			} else if got := f.readOverride(t); got != c.prior {
				t.Errorf("driver override not restored: %q", got)
			}
			muts := f.tmux.mutations()
			if len(muts) != c.wantMuts {
				t.Fatalf("mutations = %q", muts)
			}
			if !reflect.DeepEqual(muts[len(muts)-2:], restore) {
				t.Errorf("restore commands = %q, want %q", muts[len(muts)-2:], restore)
			}
		})
	}
}

func TestSwapCoordinatorLeavesProjectTreeAlone(t *testing.T) {
	f := newSwapFixture(t, "", "")
	if _, err := SwapCoordinator(SwapSpec{Session: f.session, To: "codex", Model: "gpt-5.5", ModelSet: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Errorf("project root entries = %v, err %v; want none", entries, err)
	}
	if got, want := f.readOverride(t), "[coordinator]\ndriver = \"codex\"\nmodel = \"gpt-5.5\"\n"; got != want {
		t.Errorf("driver override = %q, want %q", got, want)
	}
}
