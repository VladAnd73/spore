package dream

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/statefile"
)

// cmd/spore declared its own copy of the three-field watermark struct
// (dreamWatermark) because this package offered no rewind. A rename of
// a field in run.go's watermark would leave that copy writing the old
// name, and dream.Run would silently re-read the whole corpus on the
// next night. Moving Rewind here means there is exactly one watermark
// format to keep consistent.
func TestRewindRestoresThePreviousLastValue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, err := statefile.Path("proj", filepath.Join("dreams", "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	wm := watermark{Last: "2026-09-03T02:58:11Z", History: []string{"2026-09-02T03:00:00Z"}, RunID: "20260903-ab12"}
	if err := statefile.WriteJSONAtomic(path, "dream-watermark", wm); err != nil {
		t.Fatal(err)
	}

	res, err := Rewind("proj")
	if err != nil {
		t.Fatal(err)
	}
	if res.Last != "2026-09-03T02:58:11Z" || res.Previous != "2026-09-02T03:00:00Z" || res.RunID != "20260903-ab12" {
		t.Fatalf("unexpected result: %+v", res)
	}

	got := loadWatermark(path)
	if got.Last != "2026-09-02T03:00:00Z" {
		t.Fatalf("Last = %q, want the previous value restored", got.Last)
	}
}

// Two consecutive nights whose minted tasks are never judged used to
// lose the older night irrecoverably: watermark only ever kept one
// step back. Rewinding twice must walk back through both.
func TestRewindWalksBackThroughMoreThanOneStep(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, err := statefile.Path("proj", filepath.Join("dreams", "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	wm := watermark{
		Last:    "2026-09-04T00:00:00Z",
		History: []string{"2026-09-03T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-01T00:00:00Z"},
	}
	if err := statefile.WriteJSONAtomic(path, "dream-watermark", wm); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"2026-09-03T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-01T00:00:00Z"} {
		res, err := Rewind("proj")
		if err != nil {
			t.Fatalf("rewind: %v", err)
		}
		if res.Previous != want {
			t.Fatalf("rewind restored %q, want %q", res.Previous, want)
		}
	}
	if _, err := Rewind("proj"); err == nil {
		t.Fatal("history exhausted after 3 rewinds, a 4th must refuse")
	}
}

// Run advances the watermark maxWatermarkHistory times in a row and
// must keep exactly that many steps, oldest dropped first, so the
// state file does not grow without bound while a project runs forever.
func TestRunBoundsTheWatermarkHistory(t *testing.T) {
	f := newRunFixture(t, "proj")
	for i := 0; i < maxWatermarkHistory+3; i++ {
		slug := fmt.Sprintf("fix-%02d", i)
		cwd := workerCwd("proj", slug)
		f.worker(t, "proj", slug, userLine(cwd,
			fmt.Sprintf("2026-09-%02dT01:00:00Z", i+1), "# Goal"))
		opts := f.opts
		opts.RunID = fmt.Sprintf("run-%02d", i)
		mustRun(t, opts)
	}

	path, err := statefile.Path("proj", filepath.Join("dreams", "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	wm := loadWatermark(path)
	if len(wm.History) != maxWatermarkHistory {
		t.Fatalf("History has %d entries, want %d: %v", len(wm.History), maxWatermarkHistory, wm.History)
	}
}

func TestRewindRefusesWhenThereIsNoHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, err := statefile.Path("proj", filepath.Join("dreams", "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := statefile.WriteJSONAtomic(path, "dream-watermark", watermark{Last: "2026-09-03T02:58:11Z"}); err != nil {
		t.Fatal(err)
	}

	if _, err := Rewind("proj"); err == nil {
		t.Fatal("a watermark with no history must refuse to rewind")
	}

	got := loadWatermark(path)
	if got.Last != "2026-09-03T02:58:11Z" {
		t.Fatalf("Last = %q, want it untouched", got.Last)
	}
}
