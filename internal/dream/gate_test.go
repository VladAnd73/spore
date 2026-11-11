package dream

import (
	"testing"
)

func gateTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// TestGateRunAppliesTheTwoTierBar exercises the two-tier evidence bar
// through the real packet-loading path, not by calling Ledger.Observe
// directly: this is the code path acceptance scenario 5 needs, since
// nothing before this wired Gate up to a real packet.
func TestGateRunAppliesTheTwoTierBar(t *testing.T) {
	gateTestEnv(t)
	runDir := t.TempDir()
	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "/tmp/memory/small-commits.md",
		"text": "---\nname: small commits\ndescription: operator wants small commits\n---\n"
	}`)
	writePacketFile(t, runDir, 2, `{
		"claim": "gh pr create targets upstream",
		"type": "tool-behavior",
		"sessions": ["sesn-1"],
		"tier": "memory",
		"target": "/tmp/memory/x.md",
		"text": "..."
	}`)

	results, err := GateRun("proj", "20260901-ab12", runDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].Cleared {
		t.Fatal("operator-preference must clear on first sighting")
	}
	if results[1].Cleared {
		t.Fatal("an inferred claim must not clear on one session")
	}

	// A second, independent session sees the same inferred claim on a
	// later run.
	runDir2 := t.TempDir()
	writePacketFile(t, runDir2, 1, `{
		"claim": "gh pr create targets upstream",
		"type": "tool-behavior",
		"sessions": ["sesn-2"],
		"tier": "memory",
		"target": "/tmp/memory/x.md",
		"text": "..."
	}`)
	results2, err := GateRun("proj", "20260902-cd34", runDir2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results2) != 1 {
		t.Fatalf("got %d results, want 1", len(results2))
	}
	if !results2[0].Cleared {
		t.Fatal("the same claim from a second independent session must clear")
	}
	if results2[0].Fingerprint != results[1].Fingerprint {
		t.Fatal("the same claim text and type must fingerprint identically across runs")
	}
}

// TestGateRunExcludesAPacketAlreadyRefuted covers acceptance scenario
// 6: a fingerprint the ledger already knows as refuted must never
// clear again, even freshly proposed with new session evidence.
func TestGateRunExcludesAPacketAlreadyRefuted(t *testing.T) {
	gateTestEnv(t)
	fp := Fingerprint(TypeToolBehavior, "flag --foo exists")

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	l.Observe(TypeToolBehavior, "flag --foo exists", "sesn-0", "2026-08-01")
	l.Record(fp, StatusRefuted, "no such flag in --help", "run-0")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	runDir := t.TempDir()
	writePacketFile(t, runDir, 1, `{
		"claim": "flag --foo exists",
		"type": "tool-behavior",
		"sessions": ["sesn-9"],
		"tier": "memory",
		"target": "/tmp/memory/x.md",
		"text": "..."
	}`)
	results, err := GateRun("proj", "20260905-ef56", runDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Cleared {
		t.Fatal("a refuted fingerprint must never clear again")
	}
}

func TestGateRunOnEmptyRunReturnsNoResults(t *testing.T) {
	gateTestEnv(t)
	runDir := t.TempDir()
	results, err := GateRun("proj", "20260901-ab12", runDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}
