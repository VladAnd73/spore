package evalharness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveCitationCheck is this file's one live, real-agent proof: does
// a real claude -p process, handed BuildCitationCheckRun's prompt,
// actually say CONFIRMED for a genuine citation and REJECTED for a
// fabricated one?
//
// Skipped by default, like the other live scenarios in this package --
// it spends real API budget and is not perfectly deterministic. Run it
// deliberately:
//
//	EVALHARNESS_LIVE=1 go test ./internal/evalharness/ -run TestLiveCitationCheck -v -timeout 5m
func TestLiveCitationCheck(t *testing.T) {
	if os.Getenv("EVALHARNESS_LIVE") == "" {
		t.Skip("set EVALHARNESS_LIVE=1 to run this scenario's real, unattended claude -p processes")
	}

	transcriptDir := filepath.Join(os.TempDir(), "evalharness-live-citation-check")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	t.Logf("transcripts will be written under %s", transcriptDir)

	real := runCitationCheckFixture(t, transcriptDir, "real-citation")
	t.Logf("real-citation: CitationConfirmed = %v, transcript: %s", real.confirmed, real.transcriptPath)
	if !real.confirmed {
		t.Errorf("expected the real citation to be CONFIRMED, got REJECTED (or no verdict) -- see %s", real.transcriptPath)
	}

	fabricated := runCitationCheckFixture(t, transcriptDir, "fabricated-citation")
	t.Logf("fabricated-citation: CitationConfirmed = %v, transcript: %s", fabricated.confirmed, fabricated.transcriptPath)
	if fabricated.confirmed {
		t.Errorf("expected the fabricated citation to be REJECTED, got CONFIRMED -- see %s", fabricated.transcriptPath)
	}
}

type citationCheckResult struct {
	confirmed      bool
	transcriptPath string
}

// runCitationCheckFixture builds a fresh sandbox, spawns a real claude
// -p process against the named fixture's citation-check prompt, writes
// the raw transcript next to the sandbox for manual inspection, and
// grades it with CitationConfirmed.
func runCitationCheckFixture(t *testing.T, transcriptDir, fixture string) citationCheckResult {
	t.Helper()

	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("%s: NewSandbox: %v", fixture, err)
	}
	prompt, runWD, err := BuildCitationCheckRun(sb, fixture)
	if err != nil {
		t.Fatalf("%s: BuildCitationCheckRun: %v", fixture, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	spawner := ClaudeSpawner{}
	res, err := spawner.Spawn(ctx, SpawnRequest{
		Prompt:  prompt,
		WorkDir: runWD,
		Env:     sb.Env(),
	})
	if err != nil {
		t.Logf("%s: spawn returned an error (grading proceeds on captured output regardless): %v", fixture, err)
	}

	transcriptPath := filepath.Join(transcriptDir, fixture+"-transcript.txt")
	if writeErr := os.WriteFile(transcriptPath, []byte(res.Stdout), 0o644); writeErr != nil {
		t.Fatalf("%s: write transcript: %v", fixture, writeErr)
	}

	return citationCheckResult{confirmed: CitationConfirmed(res.Stdout), transcriptPath: transcriptPath}
}
