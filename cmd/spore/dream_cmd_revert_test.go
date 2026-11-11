package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/dream"
)

// Scenario 8. Run writes no manifest, so reverting a run it produced
// has to fail out loud.
func TestDreamRevertFailsHonestlyWithoutAManifest(t *testing.T) {
	f := newDreamFixture(t)
	runDir, err := dream.RunDir(f.project, "20260902-nomf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dream.DigestFile), []byte("# digest\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := f.run(t, "revert", "20260902-nomf", "--project", f.project)

	if code == 0 {
		t.Fatalf("a run with no manifest must not revert cleanly\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "no manifest") {
		t.Errorf("the honest failure was swallowed:\nstdout=%q\nstderr=%q", out, errOut)
	}
	if strings.Contains(out, "nothing to revert") {
		t.Error("a missing manifest was translated into a success")
	}
}

func TestDreamRevertRefusesAnUnknownRunWithoutCreatingIt(t *testing.T) {
	f := newDreamFixture(t)

	code, out, errOut := f.run(t, "revert", "20260902-ghost", "--project", f.project)

	if code == 0 {
		t.Fatalf("an unknown run must not exit 0\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "20260902-ghost") {
		t.Errorf("the error does not name the run:\n%s", errOut)
	}
	if dirs := f.runDirs(t); len(dirs) != 0 {
		t.Errorf("a refused revert created %v", dirs)
	}
}

// Call 2: the Skipped list is how a revert says what it deliberately
// did not destroy, which is what an operator needs after a bad night.
func TestDreamRevertReportsSkippedAndRestoredPaths(t *testing.T) {
	f := newDreamFixture(t)
	const runID = "20260902-seal"
	runDir, err := dream.RunDir(f.project, runID)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(f.root, "kept.md")
	touched := filepath.Join(f.root, "touched.md")
	dreamWrite(t, kept, "kept-before")
	dreamWrite(t, touched, "touched-before")

	if err := dream.Snapshot(runDir, []string{kept, touched}); err != nil {
		t.Fatal(err)
	}
	dreamWrite(t, kept, "kept-after")
	dreamWrite(t, touched, "touched-after")
	if err := dream.Seal(runDir); err != nil {
		t.Fatal(err)
	}
	dreamWrite(t, touched, "touched-by-someone-else")

	code, out, errOut := f.run(t, "revert", runID, "--project", f.project)

	if code == 0 {
		t.Fatalf("an incomplete revert must not exit 0\n%s%s", out, errOut)
	}
	if got := dreamRead(t, kept); got != "kept-before" {
		t.Errorf("kept.md = %q, want the pre-run content back", got)
	}
	if got := dreamRead(t, touched); got != "touched-by-someone-else" {
		t.Errorf("touched.md = %q: revert destroyed work done after the run", got)
	}
	if !strings.Contains(out, "restored=1") || !strings.Contains(out, "skipped=1") {
		t.Errorf("the summary must count both outcomes:\n%s", out)
	}
	if !strings.Contains(out+errOut, kept) {
		t.Errorf("the restored path is not named:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, touched) || !strings.Contains(errOut, "sealed") {
		t.Errorf("the skipped path and its reason are not named:\n%s", errOut)
	}
}

// Scenario 9. A minted task nobody judges takes its sessions out of
// every future night; the previous watermark is the only way back.
func TestDreamRewindRestoresThePreviousWatermark(t *testing.T) {
	f := newDreamFixture(t)
	f.writeWatermark(t, map[string]any{
		"last":    "2026-09-03T02:58:11Z",
		"history": []string{"2026-09-02T03:00:00Z"},
		"run_id":  "20260903-ab12",
	})

	code, out, errOut := f.run(t, "rewind", "--project", f.project)

	if code != 0 {
		t.Fatalf("rewind exit = %d, want 0\n%s%s", code, out, errOut)
	}
	wm := f.readWatermark(t)
	if wm["last"] != "2026-09-02T03:00:00Z" {
		t.Errorf("last = %q, want the previous value restored", wm["last"])
	}
	for _, want := range []string{"2026-09-03T02:58:11Z", "2026-09-02T03:00:00Z", "20260903-ab12"} {
		if !strings.Contains(out, want) {
			t.Errorf("rewind must say what it moved and for which run, missing %q:\n%s", want, out)
		}
	}
}

func TestDreamRewindRefusesWhenThereIsNoPreviousValue(t *testing.T) {
	f := newDreamFixture(t)
	f.writeWatermark(t, map[string]any{"last": "2026-09-03T02:58:11Z"})

	code, out, errOut := f.run(t, "rewind", "--project", f.project)

	if code == 0 {
		t.Fatalf("a watermark with nothing behind it must not report a rewind\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, dreamErrorToken) || !strings.Contains(errOut, "no previous value") {
		t.Errorf("the refusal must say what is missing:\n%s", errOut)
	}
	if !strings.Contains(errOut, f.statePath(t, "watermark.json")) {
		t.Errorf("the refusal must name the file the operator would inspect:\n%s", errOut)
	}
	if got := f.readWatermark(t)["last"]; got != "2026-09-03T02:58:11Z" {
		t.Errorf("last = %q, want it untouched", got)
	}
}

// Call 2, second half: "undo last night" needs a way to see last
// night's run id.
func TestDreamRunsListsRunsNewestFirst(t *testing.T) {
	f := newDreamFixture(t)
	mk := func(id string) string {
		dir, err := dream.RunDir(f.project, id)
		if err != nil {
			t.Fatal(err)
		}
		return dir
	}
	old, mid := mk("20200101-old0"), mk("20200102-mid0")
	newest := mk("20260903-new0")
	target := filepath.Join(f.root, "target.md")
	dreamWrite(t, target, "x")
	if err := dream.Snapshot(newest, []string{target}); err != nil {
		t.Fatal(err)
	}
	stamp := func(dir string, day int) {
		ts := time.Date(2020, 1, day, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(dir, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	stamp(old, 1)
	stamp(mid, 2)

	code, out, errOut := f.run(t, "runs", "--project", f.project)

	if code != 0 {
		t.Fatalf("runs exit = %d, want 0\n%s%s", code, out, errOut)
	}
	iNew := strings.Index(out, "20260903-new0")
	iMid := strings.Index(out, "20200102-mid0")
	iOld := strings.Index(out, "20200101-old0")
	if iNew < 0 || iMid < 0 || iOld < 0 {
		t.Fatalf("not every run is listed:\n%s", out)
	}
	if iNew >= iMid || iMid >= iOld {
		t.Errorf("runs are not newest first:\n%s", out)
	}
	if !strings.Contains(out, "no manifest") {
		t.Errorf("a run that cannot be reverted must say so:\n%s", out)
	}
}

func TestDreamRunsSaysSoWhenThereAreNone(t *testing.T) {
	f := newDreamFixture(t)

	code, out, errOut := f.run(t, "runs", "--project", f.project)

	if code != 0 {
		t.Fatalf("runs exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "no runs") {
		t.Errorf("an empty history must say so rather than print nothing:\nstdout=%q\nstderr=%q", out, errOut)
	}
}

func TestDreamRewindAfterTheFirstDigestRewindsToTheBeginning(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")
	if code, out, errOut := f.digest(t); code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}

	code, out, errOut := f.run(t, "rewind", "--project", f.project)

	if code != 0 {
		t.Fatalf("rewind after the first digest exit = %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "-> the beginning") {
		t.Errorf("rewind must say it went back to the start:\n%s", out)
	}
}
