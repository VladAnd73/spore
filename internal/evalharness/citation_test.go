package evalharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCitationCheck_RealCitationConfirmed proves the plumbing for a
// genuine citation: BuildCitationCheckRun assembles a prompt that
// carries the fixture's real claim and file:line pointer, copies the
// fixture's repo/ subtree into the sandbox so an agent could actually
// re-open the cited line, and CitationConfirmed correctly reads a
// spawned agent's CONFIRMED verdict back out of its transcript.
func TestCitationCheck_RealCitationConfirmed(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt, runWD, err := BuildCitationCheckRun(sb, "real-citation")
	if err != nil {
		t.Fatalf("BuildCitationCheckRun: %v", err)
	}
	if runWD != sb.RunWD {
		t.Fatalf("expected runWD to be the sandbox's RunWD, got %s", runWD)
	}
	if !strings.Contains(prompt, "widget.go:13") {
		t.Fatalf("expected the prompt to carry the fixture's file:line pointer, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "CONFIRMED") || !strings.Contains(prompt, "REJECTED") {
		t.Fatalf("expected the prompt to demand the exact CONFIRMED/REJECTED vocabulary, got:\n%s", prompt)
	}
	if _, err := os.Stat(filepath.Join(runWD, "widget.go")); err != nil {
		t.Fatalf("expected the fixture repo to be copied into the sandbox: %v", err)
	}

	fs := &fakeSpawner{result: SpawnResult{Stdout: "I opened widget.go:13 and it does return nil when cfg is nil.\nVERDICT: CONFIRMED\n"}}
	res, err := fs.Spawn(context.Background(), SpawnRequest{Prompt: prompt, WorkDir: runWD, Env: sb.Env()})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !CitationConfirmed(res.Stdout) {
		t.Fatalf("expected CitationConfirmed to be true for a CONFIRMED verdict, got transcript:\n%s", res.Stdout)
	}
}

// TestCitationCheck_FabricatedCitationRejected is the mirror case: the
// fabricated-citation fixture's report.md claims something the cited
// line does not actually say, and a correct verdict is REJECTED.
func TestCitationCheck_FabricatedCitationRejected(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt, runWD, err := BuildCitationCheckRun(sb, "fabricated-citation")
	if err != nil {
		t.Fatalf("BuildCitationCheckRun: %v", err)
	}
	if !strings.Contains(prompt, "widget.go:13") {
		t.Fatalf("expected the prompt to carry the fixture's file:line pointer, got:\n%s", prompt)
	}

	fs := &fakeSpawner{result: SpawnResult{Stdout: "I opened widget.go:13 and it does not log anything; the claim does not hold.\nVERDICT: REJECTED\n"}}
	res, err := fs.Spawn(context.Background(), SpawnRequest{Prompt: prompt, WorkDir: runWD, Env: sb.Env()})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if CitationConfirmed(res.Stdout) {
		t.Fatalf("expected CitationConfirmed to be false for a REJECTED verdict, got transcript:\n%s", res.Stdout)
	}
}

func TestCitationConfirmedIgnoresTheWordAppearingOutsideTheVerdictLine(t *testing.T) {
	transcript := "The claim mentions the word CONFIRMED but I have not verified it yet.\nVERDICT: REJECTED\n"
	if CitationConfirmed(transcript) {
		t.Fatalf("expected CitationConfirmed to require an exact VERDICT line, not a loose text match")
	}
}
