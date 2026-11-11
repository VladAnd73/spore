package spore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End to end: two projects made with `spore project new`, both
// coordinators started with `spore coordinator start` on a private tmux
// server, then every spore-attach landing mode against them.
func TestNewProjectsCoordinatorsAndAttachEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the spore binary")
	}
	h := newAttachHost(t)
	root := t.TempDir()

	bin := filepath.Join(root, "spore")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/spore").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	seen := filepath.Join(root, "seen")
	if err := os.MkdirAll(seen, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "claude")
	// The role prompt is the last argument; launch flags such as
	// --dangerously-skip-permissions come before it.
	script := "#!/bin/sh\nfor a; do last=$a; done\nprintf '%s' \"$last\" > " + seen + "/$(basename \"$PWD\")\nexec sleep 600\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var env []string
	for _, kv := range h.env {
		k := kv[:strings.IndexByte(kv, '=')]
		if strings.HasPrefix(k, "SPORE_") || strings.HasPrefix(k, "WT_") || k == "HOME" || k == "XDG_STATE_HOME" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"SPORE_COORDINATOR_AGENT="+agent,
	)
	spore := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("spore %v in %s: %v\n%s", args, dir, err, out)
		}
		return string(out)
	}

	for _, name := range []string{"alpha", "beta"} {
		out := spore(root, "project", "new", name)
		if !strings.Contains(out, `"`+name+`".path = "`+filepath.Join(root, name)+`";`) {
			t.Errorf("project new %s did not print the host registration line:\n%s", name, out)
		}
		spore(filepath.Join(root, name), "coordinator", "start")
	}

	for _, name := range []string{"alpha", "beta"} {
		var got []byte
		for i := 0; i < 50 && len(got) == 0; i++ {
			got, _ = os.ReadFile(filepath.Join(seen, name))
			if len(got) == 0 {
				exec.Command("sleep", "0.1").Run()
			}
		}
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(BundledCoordinatorRole)) {
			t.Errorf("%s coordinator's first message is not the bundled role (got %d bytes)", name, len(got))
		}
	}

	attached, out, code := h.land()
	if code != 0 || !strings.Contains(attached, "spore/pilot/default") {
		t.Errorf("bare login: code=%d attached=%q\n%s", code, attached, out)
	}
	if !strings.Contains(out, "spore/alpha/coordinator") || !strings.Contains(out, "spore/beta/coordinator") {
		t.Errorf("bare login did not list both coordinators:\n%s", out)
	}
	if attached, out, code = h.run("", "beta"); code != 0 || !strings.Contains(attached, "=spore/beta/coordinator") {
		t.Errorf("project arg: code=%d attached=%q\n%s", code, attached, out)
	}
	if attached, out, code = h.run("", "-c", "alpha"); code != 0 || !strings.Contains(attached, "=spore/alpha/coordinator") {
		t.Errorf("ssh -c alpha: code=%d attached=%q\n%s", code, attached, out)
	}
	if attached, out, code = h.run("2\n", "coord"); code != 0 || !strings.Contains(attached, "=spore/beta/coordinator") {
		t.Errorf("coord menu: code=%d attached=%q\n%s", code, attached, out)
	}
	if attached, out, code = h.run("", "-c", attachScript+" pilot bob"); code != 0 || !strings.Contains(attached, "spore/pilot/bob") {
		t.Errorf("forced pilot: code=%d attached=%q\n%s", code, attached, out)
	}
}
