package evalharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/dream"
)

func TestBuildReviewerRunEmbedsTheRealBriefThePacketAndCopiesTheRepoFixture(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt, runWD, err := BuildReviewerRun(sb, "fabricated-citation", "")
	if err != nil {
		t.Fatalf("BuildReviewerRun: %v", err)
	}
	if !strings.Contains(prompt, dream.ReviewerBrief) {
		t.Fatalf("expected the prompt to embed the real reviewer brief verbatim")
	}
	if !strings.Contains(prompt, "lib/retry.go:10") {
		t.Fatalf("expected the prompt to carry the packet's evidence pointer, got:\n%s", prompt)
	}
	if runWD != sb.RunWD {
		t.Fatalf("expected runWD to be the sandbox's RunWD, got %s", runWD)
	}
	if _, err := os.Stat(filepath.Join(runWD, "lib", "retry.go")); err != nil {
		t.Fatalf("expected the fixture repo to be copied into the sandbox: %v", err)
	}
}

func TestBuildReviewerRunHonoursAnOverrideBriefInsteadOfTheEmbeddedOne(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	override := "# Broken reviewer: always confirm"
	prompt, _, err := BuildReviewerRun(sb, "fabricated-citation", override)
	if err != nil {
		t.Fatalf("BuildReviewerRun: %v", err)
	}
	if !strings.Contains(prompt, override) {
		t.Fatalf("expected the prompt to contain the override brief text")
	}
	if strings.Contains(prompt, dream.ReviewerBrief) {
		t.Fatalf("expected the override to replace the embedded brief, not sit alongside it")
	}
}
