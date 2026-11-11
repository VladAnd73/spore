package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/statefile"
)

func TestGateOperatorPreferencePassesOnFirstSighting(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeOperatorPreference, "prefer small commits", "sesn-1", "2026-09-01")
	if !l.Gate(e, 2) {
		t.Fatal("an operator preference must pass on first sighting")
	}
}

func TestGateInferredNeedsTwoIndependentSessions(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-1", "2026-09-01")
	if l.Gate(e, 2) {
		t.Fatal("an inferred claim must not pass on first sighting")
	}
	e = l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-1", "2026-09-02")
	if l.Gate(e, 2) {
		t.Fatal("the same session twice is not two independent sessions")
	}
	e = l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-2", "2026-09-02")
	if !l.Gate(e, 2) {
		t.Fatal("two independent sessions must pass the gate")
	}
}

// The unit of independence is the session, not the day. A night's whole
// corpus carries one date, so a cross-day bar would push every inferred
// claim to its second night at the earliest.
func TestTwoSessionsOnTheSameDayCountAsTwo(t *testing.T) {
	l := newTestLedger(t)
	l.Observe(TypeHostState, "the watch timer fires every 15 minutes", "sesn-1", "2026-09-01")
	e := l.Observe(TypeHostState, "the watch timer fires every 15 minutes", "sesn-2", "2026-09-01")
	if !l.Gate(e, 2) {
		t.Fatal("two sessions on one day must count as two independent sessions")
	}
}

func TestRefutedNeverReturns(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeToolBehavior, "flag --foo exists", "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusRefuted, "no such flag in --help", "run-1")
	e = l.Observe(TypeToolBehavior, "flag --foo exists", "sesn-9", "2026-09-05")
	if l.Gate(e, 1) {
		t.Fatal("a refuted fingerprint must never pass again")
	}
}

func TestTwoUnevidencedVerdictsKillTheFingerprint(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusUnevidenced, "could not reach docs", "run-1")
	e = l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-2", "2026-09-02")
	if !l.Gate(e, 1) {
		t.Fatal("one unevidenced verdict must allow a retry")
	}
	l.Record(e.Fingerprint, StatusUnevidenced, "still unreachable", "run-2")
	e = l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-3", "2026-09-03")
	if l.Gate(e, 1) {
		t.Fatal("a second unevidenced verdict must kill the fingerprint")
	}
}

func TestFingerprintIgnoresCosmeticRewording(t *testing.T) {
	a := Fingerprint(TypeToolBehavior, "gh pr create targets upstream, not the fork")
	b := Fingerprint(TypeToolBehavior, "  GH PR CREATE targets upstream not the fork.  ")
	if a != b {
		t.Fatalf("cosmetic rewording changed the fingerprint: %s vs %s", a, b)
	}
}

// Dropping every non-alphanumeric character also drops comparison and
// arithmetic operators, so two claims that differ only in an operator
// land on one entry. Measured against the real corpus this class is the
// only way distinct claims collide; it is recorded, not fixed, because
// keeping operators would split cosmetic rewordings instead.
func TestFingerprintErasesComparisonOperators(t *testing.T) {
	a := Fingerprint(TypeHostState, "claude >= 2.1.224 has the guard")
	b := Fingerprint(TypeHostState, "claude <= 2.1.224 has the guard")
	if a != b {
		t.Fatalf("expected the operator to be erased, got %s vs %s", a, b)
	}
}

// A claim written entirely in non-ASCII normalises to the empty string.
// Without a fallback every such claim shares one fingerprint per type,
// which would merge unrelated claims into one entry.
func TestFingerprintKeepsNonASCIIClaimsApart(t *testing.T) {
	a := Fingerprint(TypeHostState, "\u0442\u0430\u0439\u043c\u0435\u0440 \u0441\u0440\u0430\u0431\u0430\u0442\u044b\u0432\u0430\u0435\u0442 \u0432 \u0442\u0440\u0438 \u0447\u0430\u0441\u0430")
	b := Fingerprint(TypeHostState, "\u0432\u043e\u0440\u043a\u0435\u0440 \u043d\u0435 \u0432\u0438\u0434\u0438\u0442 \u043e\u0447\u0435\u0440\u0435\u0434\u044c")
	empty := Fingerprint(TypeHostState, "")
	if a == b {
		t.Fatalf("two different non-ASCII claims collided on %s", a)
	}
	if a == empty || b == empty {
		t.Fatal("a non-ASCII claim collided with the empty claim")
	}
}

// Real phrasings of one claim, taken from three authors on this host:
// alignment.md, the state.md rule block, and the memory file. The
// normaliser keeps all three apart, so an inferred claim reworded
// between sessions never reaches the two-session bar. This pins today's
// behaviour; invert it if the normaliser ever gains fuzzy matching.
func TestRewordedInferredClaimNeverReachesTheBar(t *testing.T) {
	l := newTestLedger(t)
	phrasings := []string{
		"operator forbids pushing to GitHub or any remote; commit locally",
		"main on the fork is protected: nobody pushes or commits direct to main",
		"main is PROTECTED (PR-only, never push main)",
	}
	for i, claim := range phrasings {
		e := l.Observe(TypeProcessPattern, claim, fmt.Sprintf("sesn-%d", i), "2026-09-01")
		if l.Gate(e, 2) {
			t.Fatalf("phrasing %d passed the bar on its own: %q", i, claim)
		}
	}
	if len(l.Entries) != 3 {
		t.Fatalf("expected three entries for three phrasings, got %d", len(l.Entries))
	}
}

// The type is self-reported by the judging worker and nothing in Go
// checks it, so labelling an inferred claim an operator preference skips
// the two-session bar. This test exists to prove the hole is real.
func TestSelfReportedTypeBypassesTheEvidenceBar(t *testing.T) {
	l := newTestLedger(t)
	inferred := "gh pr create targets upstream, not the fork"
	e := l.Observe(TypeOperatorPreference, inferred, "sesn-1", "2026-09-01")
	if !l.Gate(e, 2) {
		t.Fatal("expected the self-reported type to bypass the bar")
	}
}

// Once a claim is written it is gated out of every later night, so no
// worker re-judges it. Record on the fingerprint is the only retraction
// lever, and it takes a caller outside the nightly loop.
func TestWrittenClaimIsGatedOutButCanStillBeRetracted(t *testing.T) {
	l := newTestLedger(t)
	claim := "crossSessionInbound refuse suppresses socket registration"
	e := l.Observe(TypeHostState, claim, "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusWritten, "", "run-1")
	e = l.Observe(TypeHostState, claim, "sesn-2", "2026-09-02")
	if l.Gate(e, 1) {
		t.Fatal("a written claim must not re-enter judging")
	}
	l.Record(e.Fingerprint, StatusRefuted, "falsified by a controlled A/B", "run-2")
	if got := l.Entries[e.Fingerprint].Status; got != StatusRefuted {
		t.Fatalf("retraction did not take: status is %q", got)
	}
}

func TestLedgerRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	e := l.Observe(TypeProcessPattern, "workers forget to fetch", "s1", "2026-09-01")
	l.Record(e.Fingerprint, StatusWritten, "", "run-1")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l2, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := l2.Entries[e.Fingerprint]
	if !ok || got.Status != StatusWritten || got.RunID != "run-1" {
		t.Fatalf("entry did not survive the round trip: %+v", got)
	}
}

func TestLoadLedgerRejectsCorruptState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := statefile.Path("proj", filepath.Join("dreams", "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLedger("proj"); err == nil {
		t.Fatal("a corrupt ledger must not load as an empty one")
	}
}

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	return l
}
