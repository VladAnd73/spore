package spore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const attachScript = "bootstrap/handover/spore-attach.sh"

// attachHost is a private tmux server plus a tmux shim on PATH. The shim
// records attach and new-session -A calls instead of running them (there
// is no terminal under go test) and forwards everything else to the
// private server, so the script under test can never reach the
// operator's own tmux.
type attachHost struct {
	t     *testing.T
	label string
	env   []string
	log   string
	tmux  string
}

func newAttachHost(t *testing.T) *attachHost {
	t.Helper()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	root := t.TempDir()
	h := &attachHost{
		t:     t,
		label: "spore-attach-test",
		log:   filepath.Join(root, "attach.log"),
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := "#!" + bash + "\n" +
		"case \"$1\" in\n" +
		"  attach|attach-session) echo \"$*\" >> " + h.log + "; exit 0 ;;\n" +
		"  new-session) case \" $* \" in *\" -A \"*) echo \"$*\" >> " + h.log + "; exit 0 ;; esac ;;\n" +
		"esac\n" +
		"exec " + realTmux + " -L " + h.label + " \"$@\"\n"
	h.tmux = filepath.Join(bin, "tmux")
	if err := os.WriteFile(h.tmux, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	// Unix socket paths cap near 108 bytes; t.TempDir() nests too deep.
	tmpdir, err := os.MkdirTemp("", "sat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpdir) })
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") ||
			strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		h.env = append(h.env, kv)
	}
	h.env = append(h.env, "TMUX_TMPDIR="+tmpdir, "PATH="+bin+":"+os.Getenv("PATH"))
	t.Cleanup(func() {
		cmd := exec.Command(realTmux, "-L", h.label, "kill-server")
		cmd.Env = h.env
		_ = cmd.Run()
	})
	h.session("spore/pilot/keepalive")
	return h
}

func (h *attachHost) session(name string) {
	h.t.Helper()
	cmd := exec.Command(h.tmux, "new-session", "-d", "-s", name, "sleep 600")
	cmd.Env = h.env
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("create session %s: %v: %s", name, err, out)
	}
}

// run invokes the script the way sshd does and returns the recorded
// attach call (empty when none), the combined output, and the exit code.
func (h *attachHost) run(stdin string, args ...string) (attached, output string, code int) {
	h.t.Helper()
	_ = os.Remove(h.log)
	cmd := exec.Command("bash", append([]string{attachScript}, args...)...)
	cmd.Env = h.env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatalf("run spore-attach: %v", err)
	}
	b, _ := os.ReadFile(h.log)
	return strings.TrimSpace(string(b)), string(out), code
}

// land runs the bare no-argument landing. Under go test stdin is no
// TTY, and a no-command login without one falls through to `bash -l`,
// so reach the landing through the forced-command shape instead.
func (h *attachHost) land() (attached, output string, code int) {
	h.t.Helper()
	return h.run("", "-c", attachScript)
}

func TestSporeAttachWithTwoCoordinatorsLandsInPilot(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")
	h.session("spore/beta/coordinator")

	attached, out, code := h.land()
	if code != 0 {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(attached, "new-session -A -s spore/pilot/default") {
		t.Errorf("attached %q, want the default pilot session", attached)
	}
	for _, want := range []string{"spore/alpha/coordinator", "spore/beta/coordinator", "spore@<host> alpha"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestSporeAttachWithOneCoordinatorAttachesIt(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")

	attached, out, code := h.land()
	if code != 0 || attached != "attach-session -t =spore/alpha/coordinator" {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

func TestSporeAttachIgnoresSessionsThatOnlyEndInCoordinator(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")
	h.session("consoletest/scratch/coordinator")

	attached, out, code := h.land()
	if code != 0 || attached != "attach-session -t =spore/alpha/coordinator" {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

func TestSporeAttachProjectArgAttachesThatCoordinator(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")
	h.session("spore/beta/coordinator")

	attached, out, code := h.run("", "beta")
	if code != 0 || attached != "attach-session -t =spore/beta/coordinator" {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

func TestSporeAttachUnknownProjectListsCoordinators(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")

	attached, out, code := h.run("", "gamma")
	if code == 0 || attached != "" {
		t.Fatalf("exit %d, attached %q; want a failure without attaching", code, attached)
	}
	if !strings.Contains(out, "spore/alpha/coordinator") {
		t.Errorf("output does not list running coordinators:\n%s", out)
	}
}

func TestSporeAttachCoordMenuPicksACoordinator(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")
	h.session("spore/beta/coordinator")

	attached, out, code := h.run("2\n", "coord")
	if code != 0 || attached != "attach-session -t =spore/beta/coordinator" {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

// sshd runs the login shell as `<shell> -c "<command>"`. A forced
// authorized_keys command starts with the script path; a plain
// `ssh spore@<host> <args>` does not.
func TestSporeAttachForcedCommandReshapesArgv(t *testing.T) {
	h := newAttachHost(t)

	attached, out, code := h.run("", "-c", "/usr/local/bin/spore-attach pilot bob")
	if code != 0 || !strings.Contains(attached, "new-session -A -s spore/pilot/bob") {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

func TestSporeAttachPlainSSHArgKeepsTheProject(t *testing.T) {
	h := newAttachHost(t)
	h.session("spore/alpha/coordinator")
	h.session("spore/webapp/coordinator")

	attached, out, code := h.run("", "-c", "webapp")
	if code != 0 || attached != "attach-session -t =spore/webapp/coordinator" {
		t.Fatalf("exit %d, attached %q; output:\n%s", code, attached, out)
	}
}

func TestSporeAttachRejectsUnsafeNames(t *testing.T) {
	h := newAttachHost(t)

	for _, cmd := range []string{"pilot a:b", "pilot my.app", "pilot -x", "my.app", "-x"} {
		attached, _, code := h.run("", "-c", cmd)
		if code == 0 || attached != "" {
			t.Errorf("%q: exit %d, attached %q; want a rejected name", cmd, code, attached)
		}
	}
}

// A tmux server inherits its default-shell from $SHELL when it starts,
// and runs every later session command as `$default-shell -c <cmd>`. The
// spore user's $SHELL is spore-attach, so a server started from the
// pilot shell must not keep it, or the reconciler's coordinator spawn
// re-enters this script and dies.
func TestSporeAttachPilotServerRunsLaterSessionCommands(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	root := t.TempDir()
	label := "spore-attach-shell-test"
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// -f /dev/null keeps a host ~/.tmux.conf from masking the bug.
	// No terminal under go test: turn the pilot's `new-session -A` into
	// a detached session so the script still starts the server.
	shim := "#!" + bash + "\n" +
		"args=()\n" +
		"for a in \"$@\"; do if [ \"$a\" = -A ]; then args+=(-d); else args+=(\"$a\"); fi; done\n" +
		"exec " + realTmux + " -f /dev/null -L " + label + " \"${args[@]}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(attachScript)
	if err != nil {
		t.Fatal(err)
	}
	loginShell := filepath.Join(bin, "spore-attach")
	if err := os.WriteFile(loginShell, src, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpdir, err := os.MkdirTemp("", "sas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpdir) })
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "PATH", "SHELL":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TMUX_TMPDIR="+tmpdir, "PATH="+bin+":"+os.Getenv("PATH"), "SHELL="+loginShell)
	t.Cleanup(func() {
		cmd := exec.Command(realTmux, "-L", label, "kill-server")
		cmd.Env = env
		_ = cmd.Run()
	})

	login := exec.Command(loginShell, "-c", "pilot")
	login.Env = env
	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("pilot login: %v: %s", err, out)
	}

	marker := filepath.Join(root, "ran")
	spawn := exec.Command(realTmux, "-L", label, "new-session", "-d", "-s", "spore/alpha/coordinator",
		"touch "+marker+"; sleep 600")
	spawn.Env = env
	if out, err := spawn.CombinedOutput(); err != nil {
		t.Fatalf("spawn: %v: %s", err, out)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	show := exec.Command(realTmux, "-L", label, "show", "-g", "default-shell")
	show.Env = env
	got, _ := show.CombinedOutput()
	t.Fatalf("session command never ran; server %s", strings.TrimSpace(string(got)))
}
