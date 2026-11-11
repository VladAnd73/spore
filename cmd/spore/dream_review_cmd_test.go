package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/dream"
)

// This file covers the CLI wiring for stages 3-5 (gate, the reviewer
// brief, write, and the ledger half of revert). It is split out of
// dream_cmd_test.go, which covers stages 1-2 and pushed past the
// file-size lint's 800-line limit once these were added there.

func TestDreamReviewerBriefPrintsTheEmbeddedBrief(t *testing.T) {
	f := newDreamFixture(t)
	code, out, errOut := f.run(t, "reviewer-brief")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if out != dream.ReviewerBrief {
		t.Fatalf("output does not match the embedded reviewer brief")
	}
}

// TestDreamGateClearsAnOperatorPreferenceAndHoldsAnInferredClaim wires
// acceptance scenario 5 through the actual CLI a worker would run.
func TestDreamGateClearsAnOperatorPreferenceAndHoldsAnInferredClaim(t *testing.T) {
	f := newDreamFixture(t)
	const runID = "20260902-gate"
	runDir, err := dream.RunDir(f.project, runID)
	if err != nil {
		t.Fatal(err)
	}
	dreamWrite(t, filepath.Join(runDir, "packets", "1.json"), `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "/tmp/memory/small-commits.md",
		"text": "x"
	}`)
	dreamWrite(t, filepath.Join(runDir, "packets", "2.json"), `{
		"claim": "gh pr create targets upstream",
		"type": "tool-behavior",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "/tmp/m.md",
		"text": "x"
	}`)

	code, out, errOut := f.run(t, "gate", runID, "--project", f.project, "--threshold", "2")
	if code != 0 {
		t.Fatalf("gate exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "1: cleared") {
		t.Errorf("packet 1 (operator-preference) must clear:\n%s", out)
	}
	if !strings.Contains(out, "2: held") {
		t.Errorf("packet 2 (inferred, one session) must be held:\n%s", out)
	}
	if !strings.Contains(out, "cleared=1") || !strings.Contains(out, "held=1") {
		t.Errorf("summary line must count both:\n%s", out)
	}
}

// TestDreamWriteRecordsRefusalsAndWritesConfirmedSurvivors wires
// acceptance scenarios 6, 7 and 9 through the CLI.
func TestDreamWriteRecordsRefusalsAndWritesConfirmedSurvivors(t *testing.T) {
	f := newDreamFixture(t)
	const runID = "20260902-writ"
	runDir, err := dream.RunDir(f.project, runID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.root, "memory", "prefer-small-commits.md")
	dreamWrite(t, filepath.Join(runDir, dream.MemoryDirFile), filepath.Join(f.root, "memory")+"\n")
	dreamWrite(t, filepath.Join(runDir, "packets", "1.json"), `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "`+strings.ReplaceAll(target, `\`, `\\`)+`",
		"text": "---\nname: Prefer Small Commits\ndescription: Operator wants large diffs split into small commits.\nmetadata:\n  type: feedback\n---\n\nSplit large diffs.\n"
	}`)
	dreamWrite(t, filepath.Join(runDir, "packets", "2.json"), `{
		"claim": "flag --foo exists",
		"type": "tool-behavior",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "/does/not/matter.md",
		"text": "x"
	}`)
	if code, out, errOut := f.run(t, "gate", runID, "--project", f.project, "--threshold", "1"); code != 0 {
		t.Fatalf("gate exit = %d\n%s%s", code, out, errOut)
	}
	dreamWrite(t, filepath.Join(runDir, "verdicts", "1.json"),
		`{"verdict":"confirmed","reason":"operator said so","proof":"session sesn-1"}`)
	dreamWrite(t, filepath.Join(runDir, "verdicts", "2.json"),
		`{"verdict":"refuted","reason":"no such flag in --help","proof":"ran spore dream --help"}`)

	code, out, errOut := f.run(t, "write", runID, "--project", f.project)
	if code != 0 {
		t.Fatalf("write exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "written=1") || !strings.Contains(out, "refused=1") {
		t.Fatalf("summary must count the write and the refusal:\n%s", out)
	}
	body := dreamRead(t, target)
	if !strings.Contains(body, "Split large diffs.") {
		t.Fatalf("memory entry was not written:\n%s", body)
	}
	reportPath := filepath.Join(runDir, "report.md")
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report.md must exist: %v", err)
	}
	report := dreamRead(t, reportPath)
	if !strings.Contains(report, "no such flag in --help") {
		t.Errorf("report.md must name the refusal reason:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(runDir, "manifest.json")); err != nil {
		t.Errorf("a confirmed write must snapshot its target first: %v", err)
	}
}

// TestDreamRevertPutsLedgerEntriesBackToCandidate is the ledger half of
// acceptance scenario 8: a revert that only restores files but leaves
// the ledger believing this run's claims were written would refuse
// them the gate forever, so the restored content could never be
// re-earned.
func TestDreamRevertPutsLedgerEntriesBackToCandidate(t *testing.T) {
	f := newDreamFixture(t)
	const runID = "20260902-ldgr"
	runDir, err := dream.RunDir(f.project, runID)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.root, "state.md")
	dreamWrite(t, target, "before")
	if err := dream.Snapshot(runDir, []string{target}); err != nil {
		t.Fatal(err)
	}
	dreamWrite(t, target, "after")
	if err := dream.Seal(runDir); err != nil {
		t.Fatal(err)
	}

	l, err := dream.LoadLedger(f.project)
	if err != nil {
		t.Fatal(err)
	}
	e := l.Observe(dream.TypeOperatorPreference, "prefer small commits", "sesn-1", "2026-09-02")
	l.Record(e.Fingerprint, dream.StatusWritten, "", runID)
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := f.run(t, "revert", runID, "--project", f.project)
	if code != 0 {
		t.Fatalf("revert exit = %d, want 0\n%s%s", code, out, errOut)
	}

	reloaded, err := dream.LoadLedger(f.project)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Entries[e.Fingerprint]
	if got.Status != dream.StatusCandidate {
		t.Errorf("status = %q, want %q", got.Status, dream.StatusCandidate)
	}
	if got.RunID != "" {
		t.Errorf("run id = %q, want cleared", got.RunID)
	}
}

// TestDreamFullPipelineDigestGateWriteReports is acceptance scenario
// 10: a fixture transcript directory holding a repeated operator
// correction and a genuinely stale claim, run end to end through
// digest, gate, and write. Stages 3 (the proposer) and 4 (the
// reviewer) are the two genuinely agentic steps in this arc, so this
// test stands in for them with fixture packets and verdicts: it proves
// the Go plumbing between every stage is correct and safe, which is
// what an automated test can prove without a live model call.
func TestDreamFullPipelineDigestGateWriteReports(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t, "recurrence_threshold = 1")
	f.correction(t, "fix-a", "2026-09-02")

	if code, out, errOut := f.digest(t); code != 0 {
		t.Fatalf("digest exit = %d, want 0\n%s%s", code, out, errOut)
	}
	dirs := f.runDirs(t)
	if len(dirs) != 1 {
		t.Fatalf("run directories = %v, want exactly one", dirs)
	}
	runID := dirs[0]
	runDir := f.statePath(t, runID)

	stateFile := filepath.Join(f.root, "state.md")
	// digest recorded the project's memory directory; write only lands
	// memory entries there.
	memDir := dream.MemoryDir(f.transcripts, f.root)
	correctionTarget := filepath.Join(memDir, "fetch-origin-before-push.md")
	staleTarget := filepath.Join(memDir, "stale.md")

	// Packet 1: the repeated operator correction, proposed as an
	// operator-preference memory entry.
	dreamWrite(t, filepath.Join(runDir, "packets", "1.json"), `{
		"claim": "operator wants origin fetched before every push",
		"type": "operator-preference",
		"sessions": ["fix-a"],
		"tier": "memory",
		"target": "`+strings.ReplaceAll(correctionTarget, `\`, `\\`)+`",
		"text": "---\nname: Fetch Origin Before Push\ndescription: Operator wants git fetch origin run before every push.\nmetadata:\n  type: feedback\n---\n\nAlways run git fetch origin first.\n"
	}`)
	// Packet 2: a claim that is genuinely false by the time it is
	// reviewed, standing in for the seven-week-stale-lesson failure
	// mode this whole arc exists to catch.
	dreamWrite(t, filepath.Join(runDir, "packets", "2.json"), `{
		"claim": "spore dream has no gate subcommand",
		"type": "tool-behavior",
		"sessions": ["fix-a"],
		"tier": "memory",
		"target": "`+strings.ReplaceAll(staleTarget, `\`, `\\`)+`",
		"text": "---\nname: stale-claim\ndescription: should never be written\n---\n\nBody.\n"
	}`)

	if code, out, errOut := f.run(t, "gate", runID, "--project", f.project); code != 0 {
		t.Fatalf("gate exit = %d, want 0\n%s%s", code, out, errOut)
	} else if !strings.Contains(out, "1: cleared") || !strings.Contains(out, "2: cleared") {
		t.Fatalf("both packets must clear at threshold 1:\n%s", out)
	}

	// Stand-in for the reviewer subagent's output: it confirms the
	// operator's own words and refutes the stale claim by actually
	// running `spore dream --help`, which lists `gate`.
	dreamWrite(t, filepath.Join(runDir, "verdicts", "1.json"),
		`{"verdict":"confirmed","reason":"operator said so verbatim in fix-a","proof":"session fix-a at 2026-09-02T01:05:00Z: \"no, always fetch origin first\""}`)
	dreamWrite(t, filepath.Join(runDir, "verdicts", "2.json"),
		`{"verdict":"refuted","reason":"spore dream --help lists a gate subcommand; the claim is false","proof":"ran spore dream --help and read the usage text"}`)

	code, out, errOut := f.run(t, "write", runID, "--project", f.project)
	if code != 0 {
		t.Fatalf("write exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "written=1") || !strings.Contains(out, "refused=1") {
		t.Fatalf("summary must count one write and one refusal:\n%s", out)
	}

	// The correction lands as a memory file, indexed in MEMORY.md, not
	// as a lesson block in state.md: state.md is never touched by this
	// pipeline (its hand-written CRITICAL LESSON/RULE convention is
	// untouched by this change).
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("state.md must never be created by the dream pipeline: %v", err)
	}
	body := dreamRead(t, correctionTarget)
	if !strings.Contains(body, "Always run git fetch origin first.") {
		t.Fatalf("memory entry missing its body:\n%s", body)
	}
	index := dreamRead(t, filepath.Join(memDir, "MEMORY.md"))
	if !strings.Contains(index, "Fetch Origin Before Push") {
		t.Fatalf("MEMORY.md must index the new entry:\n%s", index)
	}

	// The stale claim was refuted, with its reason recorded against the
	// ledger fingerprint.
	l, err := dream.LoadLedger(f.project)
	if err != nil {
		t.Fatal(err)
	}
	staleFP := dream.Fingerprint(dream.TypeToolBehavior, "spore dream has no gate subcommand")
	staleEntry := l.Entries[staleFP]
	if staleEntry == nil || staleEntry.Status != dream.StatusRefuted || staleEntry.Reason == "" {
		t.Fatalf("stale claim was not refuted with a reason: %+v", staleEntry)
	}
	if _, err := os.Stat(staleTarget); !os.IsNotExist(err) {
		t.Fatalf("a refuted claim must never be written: %v", err)
	}

	// A backup exists for the one file this run actually wrote.
	if _, err := os.Stat(filepath.Join(runDir, "manifest.json")); err != nil {
		t.Fatalf("expected a manifest recording the pre-write snapshot: %v", err)
	}

	// report.md exists and names both outcomes.
	report := dreamRead(t, filepath.Join(runDir, "report.md"))
	if !strings.Contains(report, "origin fetched before every push") || !strings.Contains(report, "gate subcommand") {
		t.Fatalf("report.md must summarise both outcomes:\n%s", report)
	}

}
