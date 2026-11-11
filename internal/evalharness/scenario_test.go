package evalharness

import (
	"context"
	"strings"
	"testing"

	"github.com/versality/spore/internal/dream"
)

type fakeSpawner struct {
	lastReq SpawnRequest
	result  SpawnResult
	err     error
}

func (f *fakeSpawner) Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error) {
	f.lastReq = req
	return f.result, f.err
}

func TestScenariosRegistryHasEveryPhase1DreamScenario(t *testing.T) {
	want := []struct {
		name string
		kind Kind
	}{
		{"proposer-demonstrated", KindProposer},
		{"proposer-discussed-not-demonstrated", KindProposer},
		{"proposer-empty-night", KindProposer},
		{"proposer-retrospective-writeup", KindProposer},
		{"reviewer-fabricated-citation", KindReviewer},
		{"reviewer-real-evidence", KindReviewer},
	}
	for _, w := range want {
		sc, ok := Find(w.name)
		if !ok {
			t.Fatalf("expected a registered scenario named %q", w.name)
		}
		if sc.Kind != w.kind {
			t.Fatalf("scenario %q: kind = %q, want %q", w.name, sc.Kind, w.kind)
		}
	}
}

func TestFindReportsUnknownScenarios(t *testing.T) {
	if _, ok := Find("does-not-exist"); ok {
		t.Fatal("expected Find to report false for an unregistered scenario")
	}
}

func TestRunScenarioSendsTheOverrideProposerBriefInsteadOfTheEmbeddedOne(t *testing.T) {
	sc, ok := Find("proposer-demonstrated")
	if !ok {
		t.Fatal("proposer-demonstrated not registered")
	}
	fs := &fakeSpawner{result: SpawnResult{Stdout: "stub", ExitCode: 0}}
	override := "# Override proposer brief for this test"
	_, err := RunScenario(context.Background(), sc, RunOptions{
		Spawner:               fs,
		SandboxParent:         t.TempDir(),
		ProposerBriefOverride: override,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if !strings.Contains(fs.lastReq.Prompt, override) {
		t.Fatalf("expected the spawned prompt to contain the override brief")
	}
	if strings.Contains(fs.lastReq.Prompt, dream.ProposerBrief) {
		t.Fatalf("expected the override to replace the embedded proposer brief")
	}
}

func TestRunScenarioSendsTheOverrideReviewerBriefInsteadOfTheEmbeddedOne(t *testing.T) {
	sc, ok := Find("reviewer-fabricated-citation")
	if !ok {
		t.Fatal("reviewer-fabricated-citation not registered")
	}
	fs := &fakeSpawner{result: SpawnResult{Stdout: "stub", ExitCode: 0}}
	override := "# Override reviewer brief for this test"
	_, err := RunScenario(context.Background(), sc, RunOptions{
		Spawner:               fs,
		SandboxParent:         t.TempDir(),
		ReviewerBriefOverride: override,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if !strings.Contains(fs.lastReq.Prompt, override) {
		t.Fatalf("expected the spawned prompt to contain the override brief")
	}
	if strings.Contains(fs.lastReq.Prompt, dream.ReviewerBrief) {
		t.Fatalf("expected the override to replace the embedded reviewer brief")
	}
}

func TestRunScenarioWithNoAgentActivityFailsProposerDemonstratedGrading(t *testing.T) {
	sc, ok := Find("proposer-demonstrated")
	if !ok {
		t.Fatal("proposer-demonstrated not registered")
	}
	fs := &fakeSpawner{result: SpawnResult{Stdout: "the fake agent wrote nothing", ExitCode: 0}}
	out, err := RunScenario(context.Background(), sc, RunOptions{
		Spawner:       fs,
		SandboxParent: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if out.Pass {
		t.Fatal("expected failure: no packet was ever written, so a demonstrated claim can't have been proposed")
	}
	if out.SandboxRoot == "" {
		t.Fatal("expected Outcome to report the sandbox root it ran in")
	}
}
