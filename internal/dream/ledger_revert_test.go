package dream

import "testing"

// TestRevertRunLedgerPutsWrittenEntriesBackToCandidate covers the
// ledger half of acceptance scenario 8: a revert must undo the ledger
// bookkeeping a write made, not just the files, or a claim that was
// reverted from state.md stays permanently unwritable because the
// ledger still thinks it was written.
func TestRevertRunLedgerPutsWrittenEntriesBackToCandidate(t *testing.T) {
	l := newTestLedger(t)
	e1 := l.Observe(TypeOperatorPreference, "prefer small commits", "sesn-1", "2026-09-01")
	l.Record(e1.Fingerprint, StatusWritten, "", "run-1")
	e2 := l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-1", "2026-09-01")
	l.Record(e2.Fingerprint, StatusWritten, "", "run-1")
	// An entry written by a different run must be left alone.
	e3 := l.Observe(TypeHostState, "unrelated claim from another night", "sesn-2", "2026-08-01")
	l.Record(e3.Fingerprint, StatusWritten, "", "run-0")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	if err := RevertRunLedger("proj", "run-1"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, fp := range []string{e1.Fingerprint, e2.Fingerprint} {
		got := reloaded.Entries[fp]
		if got.Status != StatusCandidate {
			t.Fatalf("fingerprint %s: got status %q, want %q", fp, got.Status, StatusCandidate)
		}
		if got.RunID != "" {
			t.Fatalf("fingerprint %s: run id %q was not cleared", fp, got.RunID)
		}
	}
	if got := reloaded.Entries[e3.Fingerprint]; got.Status != StatusWritten {
		t.Fatalf("an entry from a different run must stay written, got %q", got.Status)
	}
}

func TestRevertRunLedgerOnUnknownRunIsANoOp(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeHostState, "some claim", "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusWritten, "", "run-1")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if err := RevertRunLedger("proj", "run-does-not-exist"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Entries[e.Fingerprint]; got.Status != StatusWritten {
		t.Fatalf("an unrelated run id must not touch other entries, got %q", got.Status)
	}
}
