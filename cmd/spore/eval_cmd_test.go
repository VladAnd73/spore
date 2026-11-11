package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/evalharness"
)

// fakeSpawner lets these CLI tests exercise the whole eval_cmd wiring
// without starting a real `claude` process: fast, deterministic, and
// free of API cost, per this package's own dream_cmd_test.go precedent
// of injecting fakes for anything that would otherwise shell out.
type fakeSpawner struct {
	stdout string
}

func (f fakeSpawner) Spawn(ctx context.Context, req evalharness.SpawnRequest) (evalharness.SpawnResult, error) {
	return evalharness.SpawnResult{Stdout: f.stdout, ExitCode: 0}, nil
}

func TestEvalListPrintsEveryRegisteredScenario(t *testing.T) {
	var out, errOut bytes.Buffer
	code := evalMain(&out, &errOut, []string{"list"}, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, errOut.String())
	}
	for _, sc := range evalharness.Scenarios {
		if !strings.Contains(out.String(), sc.Name) {
			t.Fatalf("expected list output to mention scenario %q, got:\n%s", sc.Name, out.String())
		}
	}
}

func TestEvalRunUnknownScenarioFails(t *testing.T) {
	var out, errOut bytes.Buffer
	code := evalMain(&out, &errOut, []string{"run", "does-not-exist"}, fakeSpawner{})
	if code == 0 {
		t.Fatalf("expected a non-zero exit for an unknown scenario")
	}
	if !strings.Contains(errOut.String(), "does-not-exist") {
		t.Fatalf("expected the error to name the unknown scenario, got: %s", errOut.String())
	}
}

func TestEvalRunReportsPassForAScenarioTheFakeAgentSatisfies(t *testing.T) {
	sandboxParent := t.TempDir()
	var out, errOut bytes.Buffer
	code := evalMain(&out, &errOut, []string{
		"run", "proposer-empty-night",
		"--sandbox-parent", sandboxParent,
	}, fakeSpawner{stdout: "the fake agent wrote nothing, which is correct here"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stdout: %s, stderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Fatalf("expected PASS in output, got:\n%s", out.String())
	}
}

func TestEvalRunBriefOverrideReplacesTheEmbeddedBrief(t *testing.T) {
	sandboxParent := t.TempDir()
	overridePath := sandboxParent + "/override.md"
	if err := os.WriteFile(overridePath, []byte("# override brief\nnever propose anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	captured := &capturingSpawner{}
	var out, errOut bytes.Buffer
	code := evalMain(&out, &errOut, []string{
		"run", "proposer-empty-night",
		"--sandbox-parent", sandboxParent,
		"--brief-override", overridePath,
	}, captured)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(captured.lastPrompt, "never propose anything") {
		t.Fatalf("expected the spawned prompt to carry the override file's content, got:\n%s", captured.lastPrompt)
	}
}

type capturingSpawner struct {
	lastPrompt string
}

func (c *capturingSpawner) Spawn(ctx context.Context, req evalharness.SpawnRequest) (evalharness.SpawnResult, error) {
	c.lastPrompt = req.Prompt
	return evalharness.SpawnResult{Stdout: "ok", ExitCode: 0}, nil
}

func TestEvalRunUsesTheAgentBinaryFlag(t *testing.T) {
	// A decoy claude first on PATH: if the flag were ignored, the decoy
	// runs and no real agent spends tokens.
	decoyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(decoyDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	marker := filepath.Join(t.TempDir(), "ran")
	bin := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n: > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	evalMain(&out, &errOut, []string{"run", evalharness.Scenarios[0].Name,
		"--agent-binary", bin, "--sandbox-parent", t.TempDir()}, nil)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("--agent-binary was not run (%v)\nstdout: %s\nstderr: %s", err, out.String(), errOut.String())
	}
}
