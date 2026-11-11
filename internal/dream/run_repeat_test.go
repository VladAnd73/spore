package dream

import (
	"os"
	"path/filepath"
	"testing"
)

// Claude Code stamps transcript lines with milliseconds. A watermark
// truncated to whole seconds sits just before the newest line, so the
// session that set it reads as new again and every night re-mints it.
func TestRunSecondPassSeesNothingNewWithMillisecondTimestamps(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00.250Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00.731Z", "no, use the kernel flow"))

	if first := mustRun(t, f.opts); first.TaskSlug == "" {
		t.Fatal("the first pass minted nothing to hold a watermark for")
	}

	opts := f.opts
	opts.RunID = "20260902-second"
	second := mustRun(t, opts)

	if second.Sessions != 0 || second.TaskSlug != "" {
		t.Fatalf("the newest session was digested again: %+v", second)
	}
}

// A watermark written before sub-second precision is still a valid
// lower bound.
func TestRunReadsAWholeSecondWatermark(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00.250Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00.731Z", "no, use the kernel flow"))
	wmDir := filepath.Join(f.state, "spore", "proj", "dreams")
	if err := os.MkdirAll(wmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wmDir, "watermark.json"),
		[]byte(`{"last":"2026-09-01T01:06:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := mustRun(t, f.opts)

	if rep.Sessions != 0 || rep.Since != "2026-09-01T01:06:00Z" {
		t.Fatalf("whole-second watermark not honoured: %+v", rep)
	}
}

// The proposer and its reviewers run as a dream-<run-id> worker. Their
// own transcript is new every night, so digesting it would mint another
// proposer on a project that was otherwise quiet, forever.
func TestRunSkipsItsOwnDreamWorkerSessions(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "dream-20260901-ab12")
	f.worker(t, "proj", "dream-20260901-ab12",
		userLine(cwd, "2026-09-01T03:00:00Z", "# Dream proposer"),
		userLine(cwd, "2026-09-01T03:05:00Z", "no, gate first"))

	rep := mustRun(t, f.opts)

	if rep.Sessions != 0 || rep.TaskSlug != "" {
		t.Fatalf("a dream worker's own session was digested: %+v", rep)
	}
}

// The first night has no earlier watermark, which is a real position:
// "from the beginning". Rewinding past it must work like any other night.
func TestRewindAfterTheFirstRunPutsEverySessionBackInScope(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, use the kernel flow"))
	if first := mustRun(t, f.opts); first.TaskSlug == "" {
		t.Fatal("the first run minted nothing")
	}

	res, err := Rewind("proj")
	if err != nil {
		t.Fatalf("Rewind after the first run: %v", err)
	}
	if res.Previous != "" {
		t.Fatalf("rewound to %q, want the unset start", res.Previous)
	}

	opts := f.opts
	opts.RunID = "20260902-again"
	if again := mustRun(t, opts); again.Sessions != 1 {
		t.Fatalf("the rewound night was not read again: %+v", again)
	}
}

func TestRunSkipsOnlyMintedDreamSlugs(t *testing.T) {
	for _, tc := range []struct {
		slug     string
		digested bool
	}{
		{"dream-20260901-ab12", false},
		{"dream-20260901-ab12-2", false},
		{"dream-journal-export", true},
		{"dream-20260901-ab12-extra-work", true},
	} {
		t.Run(tc.slug, func(t *testing.T) {
			f := newRunFixture(t, "proj")
			cwd := workerCwd("proj", tc.slug)
			f.worker(t, "proj", tc.slug,
				userLine(cwd, "2026-09-01T03:00:00Z", "# Goal"),
				userLine(cwd, "2026-09-01T03:05:00Z", "no, gate first"))

			rep := mustRun(t, f.opts)

			if got := rep.Sessions == 1; got != tc.digested {
				t.Fatalf("slug %s digested = %v, want %v: %+v", tc.slug, got, tc.digested, rep)
			}
		})
	}
}
