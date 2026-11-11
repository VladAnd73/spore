package evalharness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/versality/spore/internal/composer"
)

// TestLiveAskViaToolSession is phase 2's one live, real-agent scenario:
// does wiring rules/core/ask-via-tool.md into a composed CLAUDE.md as
// an always-on fragment change whether a real claude -p agent, facing
// the fixture's genuine 3-option decision, calls AskUserQuestion?
//
// Skipped by default -- like phase 1's live runs, this spends real API
// budget and is not perfectly deterministic, so it stays out of
// routine `go test ./...` / `just check`. Run it deliberately:
//
//	EVALHARNESS_LIVE=1 go test ./internal/evalharness/ -run TestLiveAskViaToolSession -v -timeout 10m
//
// Both this test's two runs, and the grading logic it exercises, are
// the evidence recorded in docs/todo/eval-harness-for-prompt-surfaces.md.
func TestLiveAskViaToolSession(t *testing.T) {
	if os.Getenv("EVALHARNESS_LIVE") == "" {
		t.Skip("set EVALHARNESS_LIVE=1 to run this scenario's real, unattended claude -p processes")
	}

	task, err := LoadAskViaToolFixtureTask()
	if err != nil {
		t.Fatalf("LoadAskViaToolFixtureTask: %v", err)
	}

	// Transcripts are written under the OS temp dir, not t.TempDir():
	// t.TempDir() is removed the moment this test function returns, but
	// the whole point of capturing a transcript is to read it afterward
	// as evidence.
	transcriptDir := filepath.Join(os.TempDir(), "evalharness-live-ask-via-tool")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	t.Logf("transcripts will be written under %s", transcriptDir)

	baseline := runAskViaToolArm(t, transcriptDir, "baseline", task, func(sb *Sandbox) (string, error) {
		return composer.Compose(realRulesDir, filepath.Join(realRulesDir, "consumers", "spore.txt"), composer.Options{})
	})
	candidate := runAskViaToolArm(t, transcriptDir, "candidate", task, func(sb *Sandbox) (string, error) {
		consumerPath, err := WriteCandidateConsumer(sb)
		if err != nil {
			return "", err
		}
		return composer.Compose(realRulesDir, consumerPath, composer.Options{})
	})

	t.Logf("baseline: AskUserQuestion called = %v", baseline.asked)
	t.Logf("candidate: AskUserQuestion called = %v", candidate.asked)
	t.Logf("baseline transcript path: %s", baseline.transcriptPath)
	t.Logf("candidate transcript path: %s", candidate.transcriptPath)

	if baseline.asked == candidate.asked {
		t.Logf("MEASURED RESULT: no change -- both arms called AskUserQuestion=%v", baseline.asked)
	} else {
		t.Logf("MEASURED RESULT: changed -- baseline=%v, candidate=%v", baseline.asked, candidate.asked)
	}
}

type armResult struct {
	asked          bool
	transcriptPath string
}

// runAskViaToolArm builds a fresh sandbox, composes CLAUDE.md the way
// composeClaudeMD says to, spawns a real claude -p process against the
// fixture task, writes the raw transcript next to the sandbox for
// manual inspection, and grades it for an AskUserQuestion call.
func runAskViaToolArm(t *testing.T, transcriptDir, label, task string, composeClaudeMD func(sb *Sandbox) (string, error)) armResult {
	t.Helper()

	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("%s: NewSandbox: %v", label, err)
	}
	claudeMD, err := composeClaudeMD(sb)
	if err != nil {
		t.Fatalf("%s: compose CLAUDE.md: %v", label, err)
	}
	if _, err := WriteComposedClaudeMD(sb, claudeMD); err != nil {
		t.Fatalf("%s: WriteComposedClaudeMD: %v", label, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	spawner := ClaudeSpawner{}
	res, err := spawner.Spawn(ctx, SpawnRequest{
		Prompt:    task,
		WorkDir:   sb.RunWD,
		Env:       sb.Env(),
		ExtraArgs: SessionSpawnArgs,
	})
	// A spawn error (including a context-deadline kill) does not
	// invalidate grading: stream-json writes each event as it happens,
	// so whatever tool_use events occurred before any kill are already
	// in res.Stdout. See AskUserQuestionCalled's truncated-tail handling.
	if err != nil {
		t.Logf("%s: spawn returned an error (grading proceeds on captured output regardless): %v", label, err)
	}

	transcriptPath := filepath.Join(transcriptDir, label+"-transcript.jsonl")
	if writeErr := os.WriteFile(transcriptPath, []byte(res.Stdout), 0o644); writeErr != nil {
		t.Fatalf("%s: write transcript: %v", label, writeErr)
	}

	return armResult{asked: AskUserQuestionCalled(res.Stdout), transcriptPath: transcriptPath}
}
