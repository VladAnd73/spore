package infect

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	spore "github.com/versality/spore"
)

func TestPushFirstGuardURLTargetsCanonicalRepo(t *testing.T) {
	want := "https://api.github.com/repos/marketertechnologies/spore/commits/abc123"
	if got := SporeOriginCommitsURL + "abc123"; got != want {
		t.Fatalf("commits URL = %q, want %q", got, want)
	}
}

func TestPinBundledSporeOverridesToCanonicalRepo(t *testing.T) {
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := PinBundledSpore(t.TempDir(), "abc123"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\ngithub:marketertechnologies/spore/abc123\n") {
		t.Fatalf("nix flake lock args lack canonical override:\n%s", got)
	}
}

func TestStagedBundledFlakeTargetsCanonicalRepo(t *testing.T) {
	dir, err := Stage(spore.BundledFlake, t.TempDir(), "h", []string{"ssh-ed25519 K op"}, "")
	if err != nil {
		t.Fatal(err)
	}
	flake, err := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(flake), `inputs.spore.url = "github:marketertechnologies/spore";`) {
		t.Fatalf("staged flake.nix spore input is not canonical:\n%s", flake)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "flake.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Nodes map[string]struct {
			Locked   map[string]any `json:"locked"`
			Original map[string]any `json:"original"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	node := lock.Nodes["spore"]
	for name, ref := range map[string]map[string]any{"locked": node.Locked, "original": node.Original} {
		if ref["owner"] != "marketertechnologies" || ref["repo"] != "spore" {
			t.Errorf("flake.lock spore %s = %v/%v, want marketertechnologies/spore", name, ref["owner"], ref["repo"])
		}
	}
}

// TestPinnedStageResolvesOnGitHub runs the real `nix flake lock`
// against github.com, so it needs network and a commit already pushed
// to marketertechnologies/spore.
func TestPinnedStageResolvesOnGitHub(t *testing.T) {
	commit := os.Getenv("SPORE_INFECT_PIN_COMMIT")
	if commit == "" {
		t.Skip("set SPORE_INFECT_PIN_COMMIT=<pushed sha> to run; needs network")
	}
	dir, err := Stage(spore.BundledFlake, t.TempDir(), "h", []string{"ssh-ed25519 K op"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := PinBundledSpore(dir, commit); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"flake", "metadata", "--json", dir).Output()
	if err != nil {
		t.Fatalf("nix flake metadata: %v", err)
	}
	var meta struct {
		Locks struct {
			Nodes map[string]struct {
				Locked map[string]any `json:"locked"`
			} `json:"nodes"`
		} `json:"locks"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		t.Fatal(err)
	}
	got := meta.Locks.Nodes["spore"].Locked
	t.Logf("spore locked: %v", got)
	if got["owner"] != "marketertechnologies" || got["repo"] != "spore" || got["rev"] != commit {
		t.Fatalf("spore locked = %v, want marketertechnologies/spore@%s", got, commit)
	}
}
