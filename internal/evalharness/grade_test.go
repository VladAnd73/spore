package evalharness

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writePacket(t *testing.T, runDir string, n int, body string) {
	t.Helper()
	dir := filepath.Join(runDir, "packets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGradeProposerRunExpectNoPacketsPassesOnAnEmptyRunDir(t *testing.T) {
	runDir := t.TempDir()
	pass, _, err := GradeProposerRun(runDir, ExpectNoPackets)
	if err != nil {
		t.Fatal(err)
	}
	if !pass {
		t.Fatal("expected pass on a run dir with no packets")
	}
}

func TestGradeProposerRunExpectNoPacketsFailsWhenAPacketExists(t *testing.T) {
	runDir := t.TempDir()
	writePacket(t, runDir, 1, `{"claim":"x","type":"tool-behavior","tier":"lesson","target":"/tmp/x"}`)
	pass, _, err := GradeProposerRun(runDir, ExpectNoPackets)
	if err != nil {
		t.Fatal(err)
	}
	if pass {
		t.Fatal("expected failure: a packet was written")
	}
}

func TestGradeProposerRunExpectDemonstratedPassesWhenAPacketCitesDemonstratedEvidence(t *testing.T) {
	runDir := t.TempDir()
	writePacket(t, runDir, 1, `{"claim":"x","type":"tool-behavior","tier":"lesson","target":"/tmp/x",
		"evidence":[{"kind":"DEMONSTRATED","where":"a session","what":"it happened"}]}`)
	pass, _, err := GradeProposerRun(runDir, ExpectDemonstrated)
	if err != nil {
		t.Fatal(err)
	}
	if !pass {
		t.Fatal("expected pass: a packet cites DEMONSTRATED evidence")
	}
}

func TestGradeProposerRunExpectNotDemonstratedFailsWhenAPacketCitesDemonstratedEvidence(t *testing.T) {
	runDir := t.TempDir()
	writePacket(t, runDir, 1, `{"claim":"x","type":"tool-behavior","tier":"lesson","target":"/tmp/x",
		"evidence":[{"kind":"DEMONSTRATED","where":"a plan","what":"it was proposed"}]}`)
	pass, _, err := GradeProposerRun(runDir, ExpectNotDemonstrated)
	if err != nil {
		t.Fatal(err)
	}
	if pass {
		t.Fatal("expected failure: the packet wrongly cites DEMONSTRATED evidence")
	}
}

func TestGradeProposerRunExpectNotDemonstratedPassesOnADiscussedOnlyPacket(t *testing.T) {
	runDir := t.TempDir()
	writePacket(t, runDir, 1, `{"claim":"x","type":"tool-behavior","tier":"lesson","target":"/tmp/x",
		"evidence":[{"kind":"DISCUSSED","where":"a plan","what":"it was proposed"}]}`)
	pass, _, err := GradeProposerRun(runDir, ExpectNotDemonstrated)
	if err != nil {
		t.Fatal(err)
	}
	if !pass {
		t.Fatal("expected pass: the only evidence is labelled DISCUSSED")
	}
}

func writeVerdict(t *testing.T, runWD string, n int, body string) {
	t.Helper()
	dir := filepath.Join(runWD, "verdicts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGradeReviewerRunExpectApprovePassesOnConfirmed(t *testing.T) {
	runWD := t.TempDir()
	writeVerdict(t, runWD, 1, `{"verdict":"confirmed","reason":"checked it","proof":"read lib/retry.go:4"}`)
	pass, _, err := GradeReviewerRun(runWD, ExpectApprove)
	if err != nil {
		t.Fatal(err)
	}
	if !pass {
		t.Fatal("expected pass on a confirmed verdict")
	}
}

func TestGradeReviewerRunExpectRefusePassesOnRefuted(t *testing.T) {
	runWD := t.TempDir()
	writeVerdict(t, runWD, 1, `{"verdict":"refuted","reason":"the file says something else","proof":"read lib/retry.go:10"}`)
	pass, _, err := GradeReviewerRun(runWD, ExpectRefuse)
	if err != nil {
		t.Fatal(err)
	}
	if !pass {
		t.Fatal("expected pass on a refuted verdict")
	}
}

func TestGradeReviewerRunExpectRefuseFailsOnConfirmed(t *testing.T) {
	runWD := t.TempDir()
	writeVerdict(t, runWD, 1, `{"verdict":"confirmed","reason":"trusted it","proof":"none"}`)
	pass, _, err := GradeReviewerRun(runWD, ExpectRefuse)
	if err != nil {
		t.Fatal(err)
	}
	if pass {
		t.Fatal("expected failure: reviewer confirmed a claim that should have been refused")
	}
}

func TestGradeReviewerRunFailsWithNoVerdictFile(t *testing.T) {
	runWD := t.TempDir()
	pass, reason, err := GradeReviewerRun(runWD, ExpectApprove)
	if err != nil {
		t.Fatal(err)
	}
	if pass {
		t.Fatal("expected failure: no verdict file was written")
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason explaining the failure")
	}
}
