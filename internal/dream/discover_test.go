package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func userLine(cwd, ts, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts +
		`","message":{"role":"user","content":"` + text + `"}}`
}

func TestDiscoverClassifiesWorkerAndCoordinatorAndSkipsAdHoc(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"

	writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-fix-a"),
		"w.jsonl",
		userLine(home+"/proj/.worktrees/fix-a", "2026-09-01T01:00:00Z", "# Goal"))

	writeTranscript(t, filepath.Join(root, "-home-agent-proj"),
		"c.jsonl",
		userLine(home+"/proj", "2026-09-01T02:00:00Z", "# Coordinator role (shared)"))

	writeTranscript(t, filepath.Join(root, "-home-agent-proj"),
		"adhoc.jsonl",
		userLine(home+"/proj", "2026-09-01T03:00:00Z", "hey can you look at this"))

	got, errs, err := Discover(root, home, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no per-file errors, got %+v", errs)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in-scope sessions, got %d: %+v", len(got), got)
	}

	byKind := map[Kind]Session{}
	for _, s := range got {
		byKind[s.Kind] = s
	}
	w, ok := byKind[KindWorker]
	if !ok {
		t.Fatal("no worker session discovered")
	}
	if w.Project != "proj" || w.Slug != "fix-a" {
		t.Fatalf("worker misclassified: project=%q slug=%q", w.Project, w.Slug)
	}
	c, ok := byKind[KindCoordinator]
	if !ok {
		t.Fatal("no coordinator session discovered")
	}
	if c.Project != "proj" || c.Slug != "coordinator" {
		t.Fatalf("coordinator misclassified: project=%q slug=%q", c.Project, c.Slug)
	}
}

// An ad-hoc session shares the project root as cwd with a coordinator
// session, so only the role marker separates them. Pin the exclusion by
// path, not by count: a count check passes even if the ad-hoc file is
// what got classified as the coordinator.
func TestDiscoverExcludesAdHocSessionAtProjectRoot(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"

	adhoc := writeTranscript(t, filepath.Join(root, "-home-agent-proj"),
		"adhoc.jsonl",
		userLine(home+"/proj", "2026-09-01T03:00:00Z", "hey can you look at this"))

	got, _, err := Discover(root, home, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected ad-hoc session to be skipped, got %d: %+v", len(got), got)
	}
	for _, s := range got {
		if s.Path == adhoc {
			t.Fatalf("ad-hoc transcript %s was classified as %s", adhoc, s.Kind)
		}
	}
}

func TestDiscoverSkipsSessionsAtOrBeforeWatermark(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-old"),
		"o.jsonl",
		userLine(home+"/proj/.worktrees/old", "2026-08-01T00:00:00Z", "# Goal"))

	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	got, _, err := Discover(root, home, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected nothing after watermark, got %d", len(got))
	}
}

// A projects root that does not exist is a configuration fault, not an
// empty corpus. Discover used to return nil, nil for it, which read
// exactly like a quiet night with nothing to report.
func TestDiscoverErrorsWhenTheProjectsRootDoesNotExist(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-there")

	_, _, err := Discover(root, "/home/agent", time.Time{})

	if err == nil {
		t.Fatal("expected an error for a projects root that does not exist")
	}
}

// classify swallowed whatever error os.Open returned and the caller's
// loop turned it into a silent skip. A corpus the job has lost access
// to must not read the same as a corpus with nothing new in it.
func TestDiscoverReportsATranscriptItCouldNotClassify(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not deny access")
	}
	root := t.TempDir()
	home := "/home/agent"
	blocked := writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-fix-a"),
		"w.jsonl",
		userLine(home+"/proj/.worktrees/fix-a", "2026-09-01T01:00:00Z", "# Goal"))
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o644) })

	got, errs, err := Discover(root, home, time.Time{})

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("an unreadable transcript must not be classified: %+v", got)
	}
	if len(errs) != 1 || errs[0].Path != blocked {
		t.Fatalf("the unreadable transcript was dropped silently: errs = %+v", errs)
	}
}

// The scanner's buffer caps out at 8 MiB. A line past that used to make
// Scan stop without an error surfacing anywhere, so the session's First
// and Last only covered whatever came before the oversized line: a
// truncated read that looked like a complete one.
func TestDiscoverReportsAnOversizedLineInsteadOfTruncating(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	cwd := home + "/proj/.worktrees/fix-a"
	huge := `{"type":"user","cwd":"` + cwd + `","timestamp":"2026-09-01T02:00:00Z","message":{"role":"user","content":"` +
		strings.Repeat("x", 8*1024*1024+1) + `"}}`
	path := writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-fix-a"), "w.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		huge)

	got, errs, err := Discover(root, home, time.Time{})

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a transcript with an oversized line must not be classified as a complete read: %+v", got)
	}
	if len(errs) != 1 || errs[0].Path != path {
		t.Fatalf("the oversized line was truncated silently instead of erroring: errs = %+v", errs)
	}
}
