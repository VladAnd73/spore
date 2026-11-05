// Package statearchive moves verifiably done blocks out of a
// coordinator state.md into host-side archive files, leaving a one-line
// link per archive under a trailing "## Archive" section. The agent
// decides what is done; this package only does the byte-exact move.
package statearchive

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Budget is the state.md size the SessionStart hook can inject whole.
// Claude Code persists larger hook output to a file and injects only a
// short preview, so anything past this is lost on respawn.
const Budget = 8192

const ArchiveHeading = "## Archive"

// Unit is one archivable span of state.md. Top-level units are "## "
// sections (ID "3"); children are "### " subsections or paragraphs
// with a bold lead inside a section (ID "3.2"). Start/End are byte
// offsets; a unit owns everything up to the next boundary, trailing
// blank lines included.
type Unit struct {
	ID        string
	Start     int
	End       int
	Section   string
	FirstLine string
	Children  []Unit
}

func (u Unit) Size() int { return u.End - u.Start }

// Doc is a parsed state.md. Units excludes the preamble before the
// first "## " heading and the "## Archive" section, neither of which
// is archivable.
type Doc struct {
	Raw   []byte
	Units []Unit
}

var boldLeadRE = regexp.MustCompile(`^\*\*\S`)

type line struct {
	start, end int
	text       string
}

func splitLines(b []byte) []line {
	var out []line
	for off := 0; off < len(b); {
		i := bytes.IndexByte(b[off:], '\n')
		end := len(b)
		if i >= 0 {
			end = off + i + 1
		}
		out = append(out, line{off, end, strings.TrimRight(string(b[off:end]), "\r\n")})
		off = end
	}
	return out
}

func isFence(s string) bool {
	t := strings.TrimLeft(s, " ")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

func isH2(s string) bool { return strings.HasPrefix(s, "## ") }
func isH3(s string) bool { return strings.HasPrefix(s, "### ") }

// Parse splits raw into units. Headings and bold leads inside fenced
// code blocks are ignored.
func Parse(raw []byte) Doc {
	lines := splitLines(raw)
	doc := Doc{Raw: raw}
	inFence := false
	var cur *Unit
	var child *Unit
	prevBlank := true
	flushChild := func(end int) {
		if child != nil {
			child.End = end
			cur.Children = append(cur.Children, *child)
			child = nil
		}
	}
	flush := func(end int) {
		if cur == nil {
			return
		}
		flushChild(end)
		cur.End = end
		if strings.TrimSpace(cur.FirstLine) != ArchiveHeading {
			doc.Units = append(doc.Units, *cur)
		}
		cur = nil
	}
	n := 0
	for _, l := range lines {
		if isFence(l.text) {
			inFence = !inFence
		}
		switch {
		case !inFence && isH2(l.text):
			flush(l.start)
			n++
			heading := strings.TrimSpace(l.text)
			cur = &Unit{ID: fmt.Sprint(n), Start: l.start, Section: heading, FirstLine: heading}
		case cur != nil && !inFence && (isH3(l.text) || (prevBlank && boldLeadRE.MatchString(l.text))):
			flushChild(l.start)
			child = &Unit{
				ID:        fmt.Sprintf("%s.%d", cur.ID, len(cur.Children)+1),
				Start:     l.start,
				Section:   cur.Section,
				FirstLine: strings.TrimSpace(l.text),
			}
		}
		prevBlank = strings.TrimSpace(l.text) == ""
	}
	flush(len(raw))
	return doc
}

// Lookup returns the unit with id, top-level or child.
func (d Doc) Lookup(id string) (Unit, bool) {
	for _, u := range d.Units {
		if u.ID == id {
			return u, true
		}
		for _, c := range u.Children {
			if c.ID == id {
				return c, true
			}
		}
	}
	return Unit{}, false
}

// Request is one archive operation.
type Request struct {
	StateFile  string
	ArchiveDir string
	Project    string
	Slug       string
	IDs        []string
	Now        time.Time
	DryRun     bool
}

// Result describes what Archive wrote (or would write on a dry run).
type Result struct {
	ArchivePath    string
	ArchiveContent []byte
	NewState       []byte
	Link           string
	OldSize        int
	NewSize        int
	Removed        []Unit
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Archive moves the selected units into <ArchiveDir>/<date>-<slug>.md
// and rewrites StateFile without them, plus one link line under
// "## Archive". The archive file is written and fsynced first; state.md
// is then replaced atomically. Nothing is written when any check fails.
func Archive(req Request) (Result, error) {
	var res Result
	if !slugRE.MatchString(req.Slug) {
		return res, fmt.Errorf("invalid slug %q: use lowercase letters, digits, '.', '_', '-'", req.Slug)
	}
	if len(req.IDs) == 0 {
		return res, errors.New("no unit ids given")
	}
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	raw, err := os.ReadFile(req.StateFile)
	if err != nil {
		return res, err
	}
	doc := Parse(raw)
	units, err := selectUnits(doc, req.IDs)
	if err != nil {
		return res, err
	}

	date := req.Now.Format("2006-01-02")
	path := filepath.Join(req.ArchiveDir, date+"-"+req.Slug+".md")
	if _, err := os.Lstat(path); err == nil {
		return res, fmt.Errorf("archive file %s already exists; pick another --slug", path)
	} else if !os.IsNotExist(err) {
		return res, err
	}

	archive := renderArchive(req, path, units, raw)
	link := fmt.Sprintf("- %s %s: %s -> %s", date, req.Slug, truncate(stripLead(units[0].FirstLine), 80), path)

	var kept bytes.Buffer
	prev := 0
	for _, u := range units {
		kept.Write(raw[prev:u.Start])
		prev = u.End
	}
	kept.Write(raw[prev:])
	newState, insAt, ins := appendLink(kept.Bytes(), link)

	if err := verify(raw, units, archive, kept.Bytes(), newState, insAt, ins); err != nil {
		return res, fmt.Errorf("verification failed, nothing written: %w", err)
	}

	res = Result{
		ArchivePath:    path,
		ArchiveContent: archive,
		NewState:       newState,
		Link:           link,
		OldSize:        len(raw),
		NewSize:        len(newState),
		Removed:        units,
	}
	if req.DryRun {
		return res, nil
	}

	if err := os.MkdirAll(req.ArchiveDir, 0o755); err != nil {
		return res, err
	}
	if err := writeExclusive(path, archive); err != nil {
		return res, err
	}
	if err := replaceState(req.StateFile, raw, newState); err != nil {
		_ = os.Remove(path)
		return res, err
	}
	return res, nil
}

// selectUnits resolves ids, drops children already covered by a
// selected section, and returns units in file order.
func selectUnits(doc Doc, ids []string) ([]Unit, error) {
	seen := map[string]bool{}
	var units []Unit
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		u, ok := doc.Lookup(id)
		if !ok {
			return nil, fmt.Errorf("unknown unit id %q (run `spore coordinator state ls`)", id)
		}
		units = append(units, u)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Start < units[j].Start })
	var out []Unit
	for _, u := range units {
		if len(out) > 0 && u.Start < out[len(out)-1].End {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

func renderArchive(req Request, path string, units []Unit, raw []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# state.md archive: %s\n\n", req.Slug)
	fmt.Fprintf(&b, "- Source project: %s\n", req.Project)
	fmt.Fprintf(&b, "- Source file: %s\n", req.StateFile)
	fmt.Fprintf(&b, "- Archived at: %s\n", req.Now.Format(time.RFC3339))
	for _, u := range units {
		fmt.Fprintf(&b, "\n<!-- block %s from section %q -->\n\n", u.ID, u.Section)
		block := raw[u.Start:u.End]
		b.Write(block)
		if !bytes.HasSuffix(block, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// appendLink adds link under "## Archive", creating the section at the
// end of the file when missing. It returns the new content plus the
// offset and bytes inserted, so verify can check nothing else moved.
func appendLink(kept []byte, link string) ([]byte, int, []byte) {
	lines := splitLines(kept)
	inFence := false
	secStart, secEnd := -1, len(kept)
	for _, l := range lines {
		if isFence(l.text) {
			inFence = !inFence
		}
		if inFence || !isH2(l.text) {
			continue
		}
		if secStart >= 0 {
			secEnd = l.start
			break
		}
		if strings.TrimSpace(l.text) == ArchiveHeading {
			secStart = l.start
		}
	}
	var at int
	var ins []byte
	if secStart < 0 {
		at = len(kept)
		switch {
		case len(kept) == 0:
		case bytes.HasSuffix(kept, []byte("\n\n")):
		case bytes.HasSuffix(kept, []byte("\n")):
			ins = append(ins, '\n')
		default:
			ins = append(ins, '\n', '\n')
		}
		ins = append(ins, ArchiveHeading+"\n\n"+link+"\n"...)
	} else {
		at = secEnd
		for at > secStart && kept[at-1] == '\n' && at-2 >= secStart && kept[at-2] == '\n' {
			at--
		}
		if at > 0 && kept[at-1] != '\n' {
			ins = append(ins, '\n')
		}
		ins = append(ins, link+"\n"...)
	}
	out := make([]byte, 0, len(kept)+len(ins))
	out = append(out, kept[:at]...)
	out = append(out, ins...)
	out = append(out, kept[at:]...)
	return out, at, ins
}

func verify(raw []byte, units []Unit, archive, kept, newState []byte, insAt int, ins []byte) error {
	removed := 0
	for _, u := range units {
		block := raw[u.Start:u.End]
		if !bytes.Contains(archive, block) {
			return fmt.Errorf("block %s missing from archive", u.ID)
		}
		removed += len(block)
	}
	if len(kept)+removed != len(raw) {
		return fmt.Errorf("byte count mismatch: kept %d + removed %d != %d", len(kept), removed, len(raw))
	}
	prev, off := 0, 0
	for _, u := range units {
		seg := raw[prev:u.Start]
		if !bytes.Equal(kept[off:off+len(seg)], seg) {
			return errors.New("untouched content changed")
		}
		off += len(seg)
		prev = u.End
	}
	if !bytes.Equal(kept[off:], raw[prev:]) {
		return errors.New("untouched tail changed")
	}
	if !bytes.Equal(newState[insAt:insAt+len(ins)], ins) ||
		!bytes.Equal(append(append([]byte{}, newState[:insAt]...), newState[insAt+len(ins):]...), kept) {
		return errors.New("archive link insertion touched other content")
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// replaceState swaps in newState via temp + rename, refusing when the
// file changed since it was read (the coordinator may be mid-write).
func replaceState(path string, old, newState []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state.md.archive-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		tmp.Close()
		os.Remove(tmpName)
		return e
	}
	if _, err := tmp.Write(newState); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	cur, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(cur, old) {
		os.Remove(tmpName)
		if err != nil {
			return err
		}
		return fmt.Errorf("%s changed while archiving; rerun", path)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func stripLead(s string) string {
	s = strings.TrimLeft(s, "# ")
	return strings.ReplaceAll(s, "**", "")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// FormatList renders the `state ls` table.
func FormatList(path string, d Doc) string {
	var b strings.Builder
	size := len(d.Raw)
	fmt.Fprintf(&b, "%s: %d bytes, budget %d", path, size, Budget)
	if size > Budget {
		fmt.Fprintf(&b, " (over by %d)", size-Budget)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%-7s %7s  %s\n", "ID", "BYTES", "FIRST LINE")
	for _, u := range d.Units {
		fmt.Fprintf(&b, "%-7s %7d  %s\n", u.ID, u.Size(), truncate(u.FirstLine, 90))
		for _, c := range u.Children {
			fmt.Fprintf(&b, "%-7s %7d    %s\n", c.ID, c.Size(), truncate(c.FirstLine, 88))
		}
	}
	return b.String()
}
