package statearchive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixture = `# coordinator state - last updated 2026-11-05T09:00Z

## Active objective on respawn

Intro paragraph that is not a unit.

**LATEST (5 Nov).** HK-918 loop reopened. Watching PR #1.
Second line of the same paragraph.

- a bullet that belongs to LATEST

**SNAPCHAT SWITCH (4 Nov) - DONE.** Evidence in .spore/x.
Merged as #2.

**Inline **bold** later in a line stays in this block.**

` + "```" + `
**not a lead, inside a fence**
## not a heading either
` + "```" + `

**HK-827 COMPLETE (29 Oct):** delivered.

## Follow-up backlog

- item one
- item two

### Sub topic

Sub body.

## Recent events

- 2026-11-05 x: y
`

var fixedNow = time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)

func TestParseMarketerShape(t *testing.T) {
	d := Parse([]byte(fixture))
	var got []string
	for _, u := range d.Units {
		got = append(got, u.ID+" "+u.FirstLine)
		for _, c := range u.Children {
			got = append(got, c.ID+" "+c.FirstLine)
		}
	}
	want := []string{
		"1 ## Active objective on respawn",
		"1.1 **LATEST (5 Nov).** HK-918 loop reopened. Watching PR #1.",
		"1.2 **SNAPCHAT SWITCH (4 Nov) - DONE.** Evidence in .spore/x.",
		"1.3 **Inline **bold** later in a line stays in this block.**",
		"1.4 **HK-827 COMPLETE (29 Oct):** delivered.",
		"2 ## Follow-up backlog",
		"2.1 ### Sub topic",
		"3 ## Recent events",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("units:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	u, _ := d.Lookup("1.1")
	if blk := fixture[u.Start:u.End]; !strings.Contains(blk, "- a bullet that belongs to LATEST\n\n") {
		t.Fatalf("1.1 should own its trailing list and blank line, got %q", blk)
	}
	u, _ = d.Lookup("1.3")
	if blk := fixture[u.Start:u.End]; !strings.Contains(blk, "## not a heading either") {
		t.Fatalf("fenced content should stay in 1.3, got %q", blk)
	}
	last := d.Units[len(d.Units)-1]
	if last.End != len(fixture) {
		t.Fatalf("last unit should run to EOF")
	}
}

func setup(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state.md")
	if err := os.WriteFile(state, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return state, filepath.Join(dir, "host", "coordinator", "state-archive")
}

func TestArchiveRoundTrip(t *testing.T) {
	state, adir := setup(t, fixture)
	d := Parse([]byte(fixture))
	var removed []string
	for _, id := range []string{"1.2", "1.4", "2"} {
		u, _ := d.Lookup(id)
		removed = append(removed, fixture[u.Start:u.End])
	}

	res, err := Archive(Request{StateFile: state, ArchiveDir: adir, Project: "marketer",
		Slug: "done-oct", IDs: []string{"2", "1.4", "1.2", "2.1"}, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(adir, "2026-11-05-done-oct.md")
	if res.ArchivePath != wantPath {
		t.Fatalf("path %s want %s", res.ArchivePath, wantPath)
	}
	arch, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, blk := range removed {
		if !bytes.Contains(arch, []byte(blk)) {
			t.Fatalf("archive missing block %q", blk)
		}
	}
	for _, s := range []string{"Source project: marketer", "Archived at: 2026-11-05T09:30:00Z",
		`section "## Active objective on respawn"`, `section "## Follow-up backlog"`} {
		if !bytes.Contains(arch, []byte(s)) {
			t.Fatalf("archive header missing %q:\n%s", s, arch)
		}
	}

	got, _ := os.ReadFile(state)
	link := "- 2026-11-05 done-oct: SNAPCHAT SWITCH (4 Nov) - DONE. Evidence in .spore/x. -> " + wantPath + "\n"
	want := fixture
	for _, blk := range removed {
		want = strings.Replace(want, blk, "", 1)
	}
	want += "\n" + ArchiveHeading + "\n\n" + link
	if string(got) != want {
		t.Fatalf("state.md:\n%s\nwant:\n%s", got, want)
	}
	if len(fixture)-sum(removed) != len(got)-len("\n"+ArchiveHeading+"\n\n"+link) {
		t.Fatalf("removed bytes do not account for the size change")
	}

	// A second archive extends the existing section instead of adding one.
	if _, err := Archive(Request{StateFile: state, ArchiveDir: adir, Project: "marketer",
		Slug: "more", IDs: []string{"1.1"}, Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(state)
	if strings.Count(string(got2), ArchiveHeading) != 1 {
		t.Fatalf("Archive section duplicated:\n%s", got2)
	}
	if !strings.HasSuffix(string(got2), link+"- 2026-11-05 more: LATEST (5 Nov). HK-918 loop reopened. Watching PR #1. -> "+
		filepath.Join(adir, "2026-11-05-more.md")+"\n") {
		t.Fatalf("second link not appended:\n%s", got2)
	}
	if _, ok := Parse(got2).Lookup("4"); ok {
		t.Fatalf("Archive section must not be listed as a unit")
	}
}

func sum(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

func TestArchiveRefusesExistingFile(t *testing.T) {
	state, adir := setup(t, fixture)
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(adir, "2026-11-05-dup.md")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Archive(Request{StateFile: state, ArchiveDir: adir, Slug: "dup", IDs: []string{"1.2"}, Now: fixedNow})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want refusal, got %v", err)
	}
	if b, _ := os.ReadFile(existing); string(b) != "keep" {
		t.Fatalf("existing archive overwritten")
	}
	if b, _ := os.ReadFile(state); string(b) != fixture {
		t.Fatalf("state.md changed on refusal")
	}
}

func TestArchiveDryRunWritesNothing(t *testing.T) {
	state, adir := setup(t, fixture)
	res, err := Archive(Request{StateFile: state, ArchiveDir: adir, Slug: "dry", IDs: []string{"1.2"}, Now: fixedNow, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.NewSize != len(res.NewState) || bytes.Contains(res.NewState, []byte("Merged as #2.")) ||
		!bytes.Contains(res.ArchiveContent, []byte("Merged as #2.")) {
		t.Fatalf("dry run result incomplete:\n%s\n---\n%s", res.NewState, res.ArchiveContent)
	}
	if b, _ := os.ReadFile(state); string(b) != fixture {
		t.Fatalf("dry run changed state.md")
	}
	if _, err := os.Stat(adir); !os.IsNotExist(err) {
		t.Fatalf("dry run created archive dir: %v", err)
	}
}

func TestArchiveRejectsBadInput(t *testing.T) {
	state, adir := setup(t, fixture)
	for _, req := range []Request{
		{Slug: "ok", IDs: []string{"9.9"}},
		{Slug: "../x", IDs: []string{"1.1"}},
		{Slug: "ok"},
	} {
		req.StateFile, req.ArchiveDir, req.Now = state, adir, fixedNow
		if _, err := Archive(req); err == nil {
			t.Fatalf("want error for %+v", req)
		}
	}
	if b, _ := os.ReadFile(state); string(b) != fixture {
		t.Fatalf("state.md changed on rejected input")
	}
}
