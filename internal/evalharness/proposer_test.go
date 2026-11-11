package evalharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/dream"
)

var fixedNow = time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC)

func TestBuildProposerRunMintsARealRunDirAndEmbedsTheRealBriefByDefault(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt, runDir, err := BuildProposerRun(sb, "demonstrated", "20260908-test", fixedNow, "")
	if err != nil {
		t.Fatalf("BuildProposerRun: %v", err)
	}
	if !strings.Contains(prompt, dream.ProposerBrief) {
		t.Fatalf("expected the prompt to embed the real proposer brief verbatim")
	}
	if !strings.Contains(prompt, runDir) {
		t.Fatalf("expected the prompt to name the run directory %s", runDir)
	}
	digest := readRunFile(t, runDir, "digest.md")
	if !strings.Contains(digest, "panic: runtime error") {
		t.Fatalf("expected the digest to carry the fixture's failure text, got:\n%s", digest)
	}
}

func TestBuildProposerRunHonoursAnOverrideBriefInsteadOfTheEmbeddedOne(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	override := "# Totally different proposer brief\n\nNever write any packet, ever."
	prompt, _, err := BuildProposerRun(sb, "demonstrated", "20260908-test", fixedNow, override)
	if err != nil {
		t.Fatalf("BuildProposerRun: %v", err)
	}
	if !strings.Contains(prompt, override) {
		t.Fatalf("expected the prompt to contain the override brief text")
	}
	if strings.Contains(prompt, dream.ProposerBrief) {
		t.Fatalf("expected the override to replace the embedded brief, not sit alongside it")
	}
}

// TestRetrospectiveWriteupFixtureHasNoFailuresEntry pins down the shape
// this harder fixture depends on: the assistant's account of the outage
// reads like a real DEMONSTRATED panic (same file, same line, past-tense
// tool-failure language) but no tool actually errored in this session, so
// the digest must carry no Failures section and must not surface the
// panic text at all - it is reachable only by a deep read of the raw
// transcript, which the session's own score must make it eligible for.
// If any of this stops holding (e.g. because BuildDigest's
// classification changes), the fixture would silently stop testing what
// it claims to.
func TestRetrospectiveWriteupFixtureHasNoFailuresEntry(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, runDir, err := BuildProposerRun(sb, "retrospective-writeup", "20260908-test", fixedNow, "")
	if err != nil {
		t.Fatalf("BuildProposerRun: %v", err)
	}
	digest := readRunFile(t, runDir, "digest.md")
	if strings.Contains(digest, "### Failures") {
		t.Fatalf("expected no Failures section in the digest (nothing actually errored this session), got:\n%s", digest)
	}
	if strings.Contains(digest, "queue.go:88") {
		t.Fatalf("expected the panic language to be invisible in digest.md (only reachable via deep read of the raw transcript), got:\n%s", digest)
	}
	if !strings.Contains(digest, "deep-read: true") {
		t.Fatalf("expected this session to score high enough to be flagged deep-read, got:\n%s", digest)
	}
	transcript := readRunFile(t, filepath.Join(sb.Projects, "retrospective-writeup"), "session.jsonl")
	if !strings.Contains(transcript, "queue.go:88") {
		t.Fatalf("expected the raw transcript a deep read would open to carry the retrospective panic language, got:\n%s", transcript)
	}
}

func readRunFile(t *testing.T, runDir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil {
		t.Fatalf("read %s/%s: %v", runDir, name, err)
	}
	return string(b)
}
