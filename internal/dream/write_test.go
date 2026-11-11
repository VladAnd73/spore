package dream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeVerdictFile(t *testing.T, runDir string, n int, body string) {
	t.Helper()
	dir := verdictsDir(runDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// TestWriteRunLeavesTargetsUntouchedWhenEveryPacketIsRefuted covers
// acceptance scenario 7: a run whose every packet is refuted must not
// modify anything, and the report must say why.
func TestWriteRunLeavesTargetsUntouchedWhenEveryPacketIsRefuted(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "memory", "foo-flag.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "flag --foo exists",
		"type": "tool-behavior",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "---\nname: foo-flag\ndescription: flag --foo exists\nmetadata:\n  type: project\n---\n\nBody.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"refuted","reason":"no such flag in --help","proof":"ran spore dream --help"}`)

	// The real pipeline always gates a packet before it reaches a
	// reviewer, which is what seeds its ledger entry; write only ever
	// records against an entry gate already created.
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 0 {
		t.Fatalf("expected nothing written, got %+v", report.Written)
	}
	if len(report.Refused) != 1 || report.Refused[0].Reason != "no such flag in --help" {
		t.Fatalf("expected one refusal with its reason, got %+v", report.Refused)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target file must not have been created: %v", err)
	}
	reportBody := readReportFile(t, runDir)
	if !strings.Contains(reportBody, "no such flag in --help") {
		t.Errorf("report.md must list the refusal reason:\n%s", reportBody)
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(TypeToolBehavior, "flag --foo exists")
	if l.Entries[fp].Status != StatusRefuted {
		t.Fatalf("ledger status = %q, want %q", l.Entries[fp].Status, StatusRefuted)
	}
}

// TestWriteRunWritesAConfirmedMemoryEntryAndSnapshotsFirst covers
// acceptance scenario 1: a rule/preference-type packet (operator
// wants small commits) lands as a memory file, and MEMORY.md's index
// line carries the real title and hook from the frontmatter, not a
// filename/"see file" fallback. A backup exists before the write
// happens.
func TestWriteRunWritesAConfirmedMemoryEntryAndSnapshotsFirst(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	memDir := filepath.Join(t.TempDir(), "memory")
	setMemoryDir(t, runDir, memDir)
	target := filepath.Join(memDir, "prefer-small-commits.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "---\nname: Prefer Small Commits\ndescription: Operator wants large diffs split into small commits.\nmetadata:\n  type: feedback\n---\n\nSplit large diffs.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"operator said so verbatim","proof":"session sesn-1 at 2026-09-01T00:00:00Z"}`)

	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 1 {
		t.Fatalf("expected one write, got %+v", report.Written)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Split large diffs.") {
		t.Fatalf("memory file body missing:\n%s", body)
	}

	index, err := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("MEMORY.md must be created: %v", err)
	}
	if !strings.Contains(string(index), "Prefer Small Commits") ||
		!strings.Contains(string(index), "Operator wants large diffs split into small commits.") {
		t.Fatalf("MEMORY.md index line must use the real title and hook, not a fallback:\n%s", index)
	}

	if _, err := os.Stat(filepath.Join(runDir, "manifest.json")); err != nil {
		t.Fatalf("expected a manifest recording the pre-write snapshot: %v", err)
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(TypeOperatorPreference, "operator wants small commits")
	if l.Entries[fp].Status != StatusWritten {
		t.Fatalf("ledger status = %q, want %q", l.Entries[fp].Status, StatusWritten)
	}
}

// TestWriteRunCreatesMemoryDirAndIndexWhenMissing covers acceptance
// scenario 2: a fresh project directory with no memory tree yet gets
// one created, holding exactly the one entry this run wrote.
func TestWriteRunCreatesMemoryDirAndIndexWhenMissing(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	memDir := filepath.Join(t.TempDir(), "memory")
	setMemoryDir(t, runDir, memDir)
	target := filepath.Join(memDir, "prefer-small-commits.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "---\nname: Prefer Small Commits\ndescription: Operator wants large diffs split into small commits.\nmetadata:\n  type: feedback\n---\n\nSplit large diffs.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("memory file must be created: %v", err)
	}
	if !strings.Contains(string(body), "Split large diffs.") {
		t.Fatalf("memory file body missing:\n%s", body)
	}

	index, err := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("MEMORY.md must be created: %v", err)
	}
	if entries := strings.Count(string(index), "\n- ["); entries != 1 {
		t.Fatalf("expected exactly one entry in a fresh MEMORY.md, got %d:\n%s", entries, index)
	}
}

// TestWriteRunNeverTouchesStateMd proves the negative acceptance
// scenario: the dream write stage no longer appends anything to
// state.md, for any packet tier. state.md's own hand-written
// CRITICAL LESSON/RULE convention is untouched by this run.
func TestWriteRunNeverTouchesStateMd(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	projectDir := t.TempDir()
	stateFile := filepath.Join(projectDir, "state.md")
	original := "# project state\n\n## CRITICAL LESSON: hand-written (2026-01-01)\n\nKeep this.\n"
	if err := os.WriteFile(stateFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	setMemoryDir(t, runDir, filepath.Join(projectDir, "memory"))
	target := filepath.Join(projectDir, "memory", "prefer-small-commits.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "---\nname: Prefer Small Commits\ndescription: Operator wants large diffs split into small commits.\nmetadata:\n  type: feedback\n---\n\nSplit large diffs.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("state.md must be byte-identical before and after; the dream pipeline must never append to it:\nbefore:\n%s\nafter:\n%s", original, after)
	}
}

// TestWriteRunErrorsWhenMemoryFrontmatterMissingNameOrDescription
// covers the fallback risk called out while retargeting the lesson
// tier: appendMemoryIndex reads name:/description: back out of text
// with a regex, and a packet whose text lacks them must fail loudly
// rather than silently index under the filename and "see file".
func TestWriteRunErrorsWhenMemoryFrontmatterMissingNameOrDescription(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "memory", "no-frontmatter.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "a claim with no real frontmatter",
		"type": "host-state",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "Just a body, no name or description fields.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	if _, err := WriteRun("proj", "run-1", runDir, 10); err == nil {
		t.Fatal("expected an error for a memory packet whose text carries no name:/description: frontmatter")
	}
}

// TestWriteRunSkillProposalNeverTouchesRealSkillsDir covers acceptance
// scenario 9.
func TestWriteRunSkillProposalNeverTouchesRealSkillsDir(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	fakeHome := t.TempDir()
	skillsDir := filepath.Join(fakeHome, ".claude", "skills")

	writePacketFile(t, runDir, 1, `{
		"claim": "bringing up the backend needs three steps in order",
		"type": "process-pattern",
		"tier": "skill",
		"target": ".claude/skills/starting-the-backend/SKILL.md",
		"text": "---\nname: starting-the-backend\n---\n\nStep one, step two, step three.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.SkillProposals) != 1 {
		t.Fatalf("expected one skill proposal, got %+v", report.SkillProposals)
	}
	proposalPath := report.SkillProposals[0]
	if !strings.HasPrefix(proposalPath, filepath.Join(runDir, "skill-proposals")) {
		t.Fatalf("skill proposal %q must live under the run directory", proposalPath)
	}
	if _, err := os.ReadFile(proposalPath); err != nil {
		t.Fatalf("proposal file must exist: %v", err)
	}
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Fatalf("a real skills directory must never be created by this run")
	}
}

// TestWriteRunEnforcesMaxWritesPerRun covers the cap: a confirmed
// packet past the cap is held as a candidate, not discarded and not
// written.
func TestWriteRunEnforcesMaxWritesPerRun(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	dir := t.TempDir()
	setMemoryDir(t, runDir, filepath.Join(dir, "mem"))

	for i := 1; i <= 3; i++ {
		target := filepath.Join(dir, "mem", "claim"+strconv.Itoa(i)+".md")
		writePacketFile(t, runDir, i, `{
			"claim": "distinct claim number `+strconv.Itoa(i)+`",
			"type": "host-state",
			"tier": "memory",
			"target": "`+jsonPath(target)+`",
			"text": "---\nname: claim-`+strconv.Itoa(i)+`\ndescription: claim number `+strconv.Itoa(i)+`\nmetadata:\n  type: project\n---\n\nBody.\n"
		}`)
		writeVerdictFile(t, runDir, i, `{"verdict":"confirmed","reason":"x","proof":"y"}`)
	}
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 2 {
		t.Fatalf("expected 2 written, got %d: %+v", len(report.Written), report.Written)
	}
	if len(report.Held) != 1 {
		t.Fatalf("expected 1 held over cap, got %d: %+v", len(report.Held), report.Held)
	}
	if report.Held[0].Claim != "distinct claim number 3" {
		t.Fatalf("expected the third packet held (numeric order), got %+v", report.Held[0])
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	heldFP := Fingerprint(TypeHostState, "distinct claim number 3")
	if got := l.Entries[heldFP]; got != nil && got.Status == StatusWritten {
		t.Fatalf("a packet held over the cap must not be recorded as written")
	}
}

// TestWriteRunUnreviewedPacketIsIgnored covers a packet the gate held
// back before it ever reached a reviewer: no verdict file exists, so
// WriteRun must skip it entirely rather than erroring.
func TestWriteRunUnreviewedPacketIsIgnored(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "memory", "x.md")
	writePacketFile(t, runDir, 1, `{
		"claim": "some inferred claim seen once",
		"type": "host-state",
		"tier": "memory",
		"target": "`+jsonPath(target)+`",
		"text": "---\nname: x\ndescription: some inferred claim seen once\nmetadata:\n  type: project\n---\n\nBody.\n"
	}`)
	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 0 || len(report.Refused) != 0 || len(report.Held) != 0 {
		t.Fatalf("an unreviewed packet must be silently skipped, got %+v", report)
	}
}

func jsonPath(p string) string {
	return strings.ReplaceAll(p, `\`, `\\`)
}

func readReportFile(t *testing.T, runDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(runDir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeMemoryEntry appends an index line to MEMORY.md next to the
// target, so revert has to put MEMORY.md back too, not just the entry.
func TestRevertAfterWriteRestoresMemoryIndex(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before *string
	}{
		{"existing index", strPtr("# project memory\n\n- [Old](old.md) -- kept\n")},
		{"no index yet", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTestEnv(t)
			runDir, err := RunDir("proj", "run-1")
			if err != nil {
				t.Fatal(err)
			}
			memDir := filepath.Join(t.TempDir(), "memory")
			if err := os.MkdirAll(memDir, 0o755); err != nil {
				t.Fatal(err)
			}
			setMemoryDir(t, runDir, memDir)
			index := filepath.Join(memDir, "MEMORY.md")
			if tc.before != nil {
				if err := os.WriteFile(index, []byte(*tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(memDir, "prefer-small-commits.md")
			writePacketFile(t, runDir, 1, `{
				"claim": "operator wants small commits",
				"type": "operator-preference",
				"tier": "memory",
				"target": "`+jsonPath(target)+`",
				"text": "---\nname: prefer-small-commits\ndescription: Split large diffs.\nmetadata:\n  type: feedback\n---\n\nSplit.\n"
			}`)
			writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
			if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
				t.Fatal(err)
			}

			if _, err := RevertWithReport("proj", "run-1"); err != nil {
				t.Fatal(err)
			}

			got, err := os.ReadFile(index)
			if tc.before == nil {
				if !os.IsNotExist(err) {
					t.Fatalf("MEMORY.md must be gone after revert, got %q (err %v)", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != *tc.before {
				t.Fatalf("MEMORY.md not restored:\n got %q\nwant %q", got, *tc.before)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

// A second write over a sealed run would re-snapshot the files the first
// write already changed, so revert could only reach the first write's
// output, never the pre-run state.
func TestWriteRunRefusesARunItAlreadySealed(t *testing.T) {
	writeTestEnv(t)
	runDir, err := RunDir("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	setMemoryDir(t, runDir, memDir)
	index := filepath.Join(memDir, "MEMORY.md")
	before := "# project memory\n\n- [Old](old.md) -- kept\n"
	if err := os.WriteFile(index, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(memDir, "prefer-small-commits.md")
	writePacketFile(t, runDir, 1, memoryPacket(target))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(manifestPath(runDir))
	if err != nil {
		t.Fatal(err)
	}

	_, err = WriteRun("proj", "run-1", runDir, 10)
	if err == nil || !strings.Contains(err.Error(), "revert") {
		t.Fatalf("second write = %v, want a refusal that names revert", err)
	}
	if again, _ := os.ReadFile(manifestPath(runDir)); string(again) != string(manifest) {
		t.Fatal("a refused second write changed manifest.json")
	}

	if _, err := RevertWithReport("proj", "run-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(index); string(got) != before {
		t.Fatalf("MEMORY.md after revert = %q, want the pre-run %q", got, before)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("revert left the written entry behind: %v", err)
	}
}

// A later night may rewrite an entry it wrote before. The index keeps
// one line per file, carrying the newest hook.
func TestWriteMemoryEntryOverwriteKeepsOneIndexLine(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fetch-first.md")
	entry := func(desc string) string {
		return "---\nname: fetch-first\ndescription: " + desc + "\nmetadata:\n  type: feedback\n---\n\nBody.\n"
	}
	if err := writeMemoryEntry(target, entry("old hook")); err != nil {
		t.Fatal(err)
	}
	if err := writeMemoryEntry(target, entry("new hook")); err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(index), "](fetch-first.md)"); n != 1 {
		t.Fatalf("index lines for fetch-first.md = %d, want 1:\n%s", n, index)
	}
	if !strings.Contains(string(index), "new hook") || strings.Contains(string(index), "old hook") {
		t.Fatalf("index line is not the newest one:\n%s", index)
	}
}

// Only the overwritten file's own index line goes; another entry that
// merely links to it stays.
func TestWriteMemoryEntryOverwriteKeepsLinesThatOnlyCrossLink(t *testing.T) {
	dir := t.TempDir()
	cross := "- [Cross ref](project_other.md) - related: see [no sudo](feedback_no_sudo.md)"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"),
		[]byte("# project memory\n\n- [No sudo](feedback_no_sudo.md) -- old\n"+cross+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := writeMemoryEntry(filepath.Join(dir, "feedback_no_sudo.md"),
		"---\nname: no-sudo\ndescription: new hook\nmetadata:\n  type: feedback\n---\n\nBody.\n")
	if err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), cross) {
		t.Fatalf("the cross-linking entry was deleted:\n%s", index)
	}
	if strings.Contains(string(index), "-- old") || !strings.Contains(string(index), "new hook") {
		t.Fatalf("the overwritten entry's own line was not replaced:\n%s", index)
	}
}

// "Revert it first" must be advice that works: after a revert, the run
// can be written again, and a second revert still reaches the pre-run
// state.
func TestWriteRunAfterRevertWritesAgainAndRevertsAgain(t *testing.T) {
	writeTestEnv(t)
	runDir, err := RunDir("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	setMemoryDir(t, runDir, memDir)
	index := filepath.Join(memDir, "MEMORY.md")
	before := "# project memory\n\n- [Old](old.md) -- kept\n"
	if err := os.WriteFile(index, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(memDir, "prefer-small-commits.md")
	writePacketFile(t, runDir, 1, memoryPacket(target))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := RevertWithReport("proj", "run-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatalf("write after revert: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the second write did not land: %v", err)
	}
	if _, err := RevertWithReport("proj", "run-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(index); string(got) != before {
		t.Fatalf("MEMORY.md after the second revert = %q, want %q", got, before)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the second revert left the entry behind: %v", err)
	}
}

func TestRevertTwiceSaysTheRunWasAlreadyReverted(t *testing.T) {
	writeTestEnv(t)
	runDir, err := RunDir("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(t.TempDir(), "memory")
	setMemoryDir(t, runDir, memDir)
	writePacketFile(t, runDir, 1, memoryPacket(filepath.Join(memDir, "x.md")))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := RevertWithReport("proj", "run-1"); err != nil {
		t.Fatal(err)
	}

	_, err = RevertWithReport("proj", "run-1")
	if err == nil || !strings.Contains(err.Error(), "already reverted") {
		t.Fatalf("second revert = %v, want it to say the run was already reverted", err)
	}
}
