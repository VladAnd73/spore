package dream

import (
	"fmt"
	"testing"
)

func TestFlagDeepReadsHonoursCap(t *testing.T) {
	var ds []SessionDigest
	for i := 0; i < 10; i++ {
		ds = append(ds, SessionDigest{
			Session:          Session{Slug: fmt.Sprintf("s%d", i)},
			OperatorMessages: []Slice{{Text: "x"}, {Text: "y"}},
			Failures:         []Slice{{Text: "boom"}},
			End:              "blocked",
		})
	}
	FlagDeepReads(ds, 3)
	n := 0
	for _, d := range ds {
		if d.DeepRead {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("cap not enforced: %d sessions flagged, want 3", n)
	}
}

func TestFlagDeepReadsPicksHighestScoringFirst(t *testing.T) {
	ds := []SessionDigest{
		{Session: Session{Slug: "quiet"}},
		{Session: Session{Slug: "messy"},
			Failures: []Slice{{}, {}, {}}, End: "blocked"},
	}
	FlagDeepReads(ds, 1)
	if !ds[1].DeepRead || ds[0].DeepRead {
		t.Fatalf("expected only the messy session flagged: %+v", ds)
	}
}

func TestFlagDeepReadsZeroCapFlagsNothing(t *testing.T) {
	ds := []SessionDigest{{Session: Session{Slug: "a"}, Failures: []Slice{{}}}}
	FlagDeepReads(ds, 0)
	if ds[0].DeepRead {
		t.Fatal("zero cap must flag nothing")
	}
}

func TestFlagDeepReadsScoresEveryDigest(t *testing.T) {
	ds := []SessionDigest{{
		Session:          Session{Slug: "a"},
		Failures:         []Slice{{}, {}},
		OperatorMessages: []Slice{{}, {}, {}},
		OperatorRefusals: []Slice{{}},
		RepeatedCommands: []Slice{{}, {}, {}, {}},
		End:              "tokens",
	}}
	FlagDeepReads(ds, 0)
	// 3*2 + 2*3 + 2*1 + 4 + 5 = 23
	if ds[0].Score != 23 {
		t.Fatalf("score = %d, want 23", ds[0].Score)
	}
}

func TestFlagDeepReadsSkipsZeroScoreSessions(t *testing.T) {
	ds := []SessionDigest{
		{Session: Session{Slug: "empty"}, End: "done"},
		{Session: Session{Slug: "one"}, Failures: []Slice{{}}, End: "done"},
	}
	FlagDeepReads(ds, 5)
	if ds[0].DeepRead {
		t.Error("a zero-score session must never be deep-read")
	}
	if !ds[1].DeepRead {
		t.Error("the scoring session should be flagged")
	}
}

// Scores are small integers over hundreds of sessions, so ties are the
// common case. The chosen set must not depend on the order Discover
// happened to walk the filesystem in.
func TestFlagDeepReadsBreaksTiesBySlug(t *testing.T) {
	tied := func(slugs ...string) []SessionDigest {
		var ds []SessionDigest
		for _, s := range slugs {
			ds = append(ds, SessionDigest{
				Session:  Session{Slug: s},
				Failures: []Slice{{}},
			})
		}
		return ds
	}
	forward := tied("charlie", "alpha", "bravo")
	FlagDeepReads(forward, 2)
	reversed := tied("bravo", "alpha", "charlie")
	FlagDeepReads(reversed, 2)

	got := map[string]bool{}
	for _, d := range forward {
		if d.DeepRead {
			got[d.Session.Slug] = true
		}
	}
	if !got["alpha"] || !got["bravo"] || got["charlie"] {
		t.Fatalf("ties not broken by slug: flagged %v", got)
	}
	for _, d := range reversed {
		if d.DeepRead != got[d.Session.Slug] {
			t.Fatalf("selection depends on input order: %q flagged=%v",
				d.Session.Slug, d.DeepRead)
		}
	}
}

// Two sessions can share a slug (the same worktree reused across nights),
// so the slug tiebreak alone is not total. Path is unique per session.
func TestFlagDeepReadsBreaksSlugTiesByPath(t *testing.T) {
	same := func() []SessionDigest {
		return []SessionDigest{
			{Session: Session{Slug: "dup", Path: "/p/z.jsonl"}, Failures: []Slice{{}}},
			{Session: Session{Slug: "dup", Path: "/p/a.jsonl"}, Failures: []Slice{{}}},
		}
	}
	forward := same()
	FlagDeepReads(forward, 1)
	if forward[0].DeepRead || !forward[1].DeepRead {
		t.Fatalf("expected the lower path flagged: %+v", forward)
	}
	reversed := []SessionDigest{same()[1], same()[0]}
	FlagDeepReads(reversed, 1)
	if !reversed[0].DeepRead || reversed[1].DeepRead {
		t.Fatalf("selection depends on input order: %+v", reversed)
	}
}
