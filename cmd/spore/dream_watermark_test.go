package main

import (
	"strings"
	"testing"
)

// internal/dream keeps the watermark type unexported and offers no
// rewind, so dreamWatermark in dream_cmd.go is a second, independent
// declaration of the same three JSON fields. If dream ever renames one,
// rewind keeps writing the old name, dream.Run unmarshals it to the zero
// value, and the next night silently re-reads the whole corpus. Nothing
// fails; the journal just shows a night that did far too much work.
//
// This test is the loud failure that is otherwise missing. It never
// names a JSON key. Everything it asserts comes back out of the CLI's
// own output, so the only way it can pass is if the bytes dream.Run
// writes are the bytes rewind reads, and the bytes rewind writes are the
// bytes dream.Run reads. The real fix is to move Rewind into
// internal/dream and delete the duplicate; until then this test is what
// notices.
func TestRewindRoundTripsTheWatermarkFormatDreamRunWrites(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)

	f.correction(t, "lesson-a", "2026-09-01")
	code, firstOut, errOut := f.digest(t)
	if code != 0 {
		t.Fatalf("first night exit = %d, want 0\n%s%s", code, firstOut, errOut)
	}
	firstMark := dreamSummaryField(t, firstOut, "watermark")

	f.correction(t, "lesson-b", "2026-09-02")
	code, secondOut, errOut := f.digest(t)
	if code != 0 {
		t.Fatalf("second night exit = %d, want 0\n%s%s", code, secondOut, errOut)
	}
	// The second night reading from the first night's mark is dream.Run
	// reading back what dream.Run wrote. If that already fails, the
	// rewind half below cannot be trusted either.
	if got := dreamSummaryField(t, secondOut, "since"); got != firstMark {
		t.Fatalf("second night since = %q, want the first night's watermark %q", got, firstMark)
	}
	secondRun := dreamSummaryRunID(t, secondOut)

	code, rewindOut, errOut := f.run(t, "rewind", "--project", f.project)
	if code != 0 {
		t.Fatalf("rewind exit = %d, want 0. A rewind that cannot find a previous value is what "+
			"a renamed watermark field looks like from here\n%s%s", code, rewindOut, errOut)
	}
	// run_id is the third field and has no other reader, so assert
	// rewind found it rather than printing "none".
	if !strings.Contains(rewindOut, secondRun) {
		t.Errorf("rewind must name the run whose sessions it put back (%s):\n%s", secondRun, rewindOut)
	}

	code, thirdOut, errOut := f.digest(t, "--dry-run")
	if code != 0 {
		t.Fatalf("post-rewind digest exit = %d, want 0\n%s%s", code, thirdOut, errOut)
	}
	if got := dreamSummaryField(t, thirdOut, "since"); got != firstMark {
		t.Fatalf("after rewind the next run reads from %q, want the first night's mark %q. "+
			"dream.Run and the CLI's rewind disagree about the watermark format", got, firstMark)
	}
}

// dreamSummaryField pulls one key=value out of the one-line run summary.
func dreamSummaryField(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "dream ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(tok, key+"="); ok {
				return v
			}
		}
	}
	t.Fatalf("no %s= field in any summary line:\n%s", key, out)
	return ""
}

// dreamSummaryRunID reads the run id out of the summary's "dream
// <project> <run-id>:" head.
func dreamSummaryRunID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "dream" {
			return strings.TrimSuffix(fields[2], ":")
		}
	}
	t.Fatalf("no run id in any summary line:\n%s", out)
	return ""
}
