package dream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/statefile"
)

// repeatThreshold is how many times one command has to be run before the
// digest calls it a retry loop.
const repeatThreshold = 3

// DefaultDeepReadCap is how many sessions a run reads deeply when the
// caller names no cap. internal/watch's [dreams] deep_read_cap default
// has to be this same number, because the CLI passes the config value
// straight into Options.DeepReadCap: two different constants means the
// one a reader finds is not the one that runs. They already drifted
// once, 5 here against 3 there, and
// TestDefaultDeepReadCapMatchesTheWatchConfigDefault fails if they
// drift again.
//
// Five rather than three because the bar for an inferred claim is two
// independent sessions. Three deep reads leave that bar binding on the
// count instead of on the evidence: one weak session and the night can
// corroborate nothing. Five leaves room for two corroborated pairs.
const DefaultDeepReadCap = 5

// defaultDigestBudget is in bytes and only ever binds on a run with no
// watermark: measured on this host, one project's nightly digest is
// 31 KB and its seven-day digest is 61 KB, while the whole corpus at
// once is 354 KB.
const defaultDigestBudget = 120000

// omittedListLimit bounds the roll call of dropped sessions. Naming all
// 327 that a first run on this host drops costs 29 KB, which is a
// quarter of the budget spent on names the reader cannot act on; the
// count and the top score above the list are the parts that matter.
const omittedListLimit = 25

// Options configures one nightly run. A zero DigestBudget takes the
// default above; a negative one means no budget.
//
// DeepReadCap is a pointer because internal/watch documents its config
// knob's zero as legal ("do none of this"), and the CLI passes that
// value straight through: a plain int could not tell "the caller named
// no cap" from "the caller named zero" and would silently turn an
// operator's opt-out back into the default. A nil DeepReadCap takes
// the default above; any pointed-to value, including a pointer to
// zero, is used as given.
type Options struct {
	ProjectsRoot string
	Home         string
	Project      string
	TasksDir     string
	Now          time.Time
	RunID        string
	DeepReadCap  *int
	DigestBudget int
	DryRun       bool
}

// TranscriptError names a transcript the run could not read. Discovery
// drops an unreadable file without a word, so a broken corpus and a quiet
// night are otherwise the same result.
type TranscriptError struct {
	Path string
	Err  string
}

// Report is everything the run learned about itself. The counts are
// there so a caller can tell a night with nothing in it from a discovery
// that stopped working: Discovered is before the project filter, Sessions
// after it, and the two kind counts break Sessions down.
type Report struct {
	Project     string
	RunID       string
	RunDir      string
	Since       string
	Discovered  int
	Sessions    int
	Coordinator int
	Worker      int
	Digested    int
	Unreadable  []TranscriptError
	DeepRead    int
	DigestBytes int
	Omitted     []string
	KnownClaims int
	TaskSlug    string
	Watermark   string
	Previous    string
	DryRun      bool
	Warnings    []string
}

// watermark is where the last run stopped. History and RunID are kept
// so a night whose task was minted and then never judged can be found
// and put back: nothing in this arc notices that a judging worker died,
// so the position it skipped past has to stay reachable. History holds
// the values Last held before each of the last maxWatermarkHistory
// runs replaced it, newest first ("" is the first run's unset start),
// because a single Previous value only survives one missed night: a
// second consecutive night whose task also goes unjudged used to push
// the first one out of reach for good.
type watermark struct {
	Last    string   `json:"last"`
	History []string `json:"history,omitempty"`
	RunID   string   `json:"run_id,omitempty"`
}

// maxWatermarkHistory bounds how many nights back a rewind can reach.
// Ten covers two work weeks of nightly runs going unjudged before an
// operator notices - a wide margin over the single-night loss this
// fixes - while keeping the watermark file small: each step is one
// RFC3339 timestamp, so the file stays well under a kilobyte even at
// the cap.
const maxWatermarkHistory = 10

// RewindResult reports what Rewind moved.
type RewindResult struct {
	Last     string
	Previous string
	RunID    string
}

// Rewind moves a project's watermark back one recorded step, restoring
// the value Last held before the most recent run that replaced it. It
// returns an error if there is no history to rewind to. RunID is the
// id of the run whose sessions are back in scope; it is not carried
// forward, because nothing paired a run id with each older History
// entry before this fix either, so an older step's own run id was
// never recoverable.
func Rewind(project string) (RewindResult, error) {
	path, err := statefile.Path(project, filepath.Join("dreams", "watermark.json"))
	if err != nil {
		return RewindResult{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return RewindResult{}, err
	}
	var wm watermark
	if err := json.Unmarshal(b, &wm); err != nil {
		return RewindResult{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(wm.History) == 0 {
		return RewindResult{}, fmt.Errorf("dream: rewind: %s has no previous value to rewind to", path)
	}
	res := RewindResult{Last: wm.Last, Previous: wm.History[0], RunID: wm.RunID}
	next := watermark{Last: wm.History[0], History: wm.History[1:]}
	if err := statefile.WriteJSONAtomic(path, "dream-watermark", next); err != nil {
		return RewindResult{}, err
	}
	return res, nil
}

// Run executes the deterministic half of a night: discover the sessions
// this project produced since the last run, digest them, score them,
// write the run directory, mint the task a judging agent picks up, and
// advance the watermark.
//
// It writes nothing outside the run directory, the watermark, and the
// tasks directory, so it takes no snapshot and seals nothing: there is
// no pre-run state for it to restore. The stage that writes lessons into
// the harness does not exist yet, and that stage owns Snapshot and Seal.
func Run(opts Options) (Report, error) {
	rep := Report{Project: opts.Project, RunID: opts.RunID, DryRun: opts.DryRun}
	if strings.TrimSpace(opts.Project) == "" {
		return rep, fmt.Errorf("dream: run: project must not be empty")
	}
	if strings.TrimSpace(opts.RunID) == "" {
		return rep, fmt.Errorf("dream: run: run id must not be empty")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}

	wmPath, err := statefile.Path(opts.Project, filepath.Join("dreams", "watermark.json"))
	if err != nil {
		return rep, err
	}
	wm := loadWatermark(wmPath)
	since, _ := time.Parse(time.RFC3339Nano, wm.Last)
	rep.Since = wm.Last

	// A projects root that does not resolve is a configuration fault,
	// not an empty corpus, and Discover reports it as an error rather
	// than a quiet night.
	sessions, classifyErrs, err := Discover(opts.ProjectsRoot, opts.Home, since)
	if err != nil {
		return rep, fmt.Errorf("dream: run: projects root: %w", err)
	}
	rep.Unreadable = classifyErrs
	rep.Discovered = len(sessions)

	var mine []Session
	for _, s := range sessions {
		if s.Project != opts.Project {
			continue
		}
		mine = append(mine, s)
		if s.Kind == KindCoordinator {
			rep.Coordinator++
		} else {
			rep.Worker++
		}
	}
	rep.Sessions = len(mine)
	sort.Slice(mine, func(i, j int) bool { return mine[i].Path < mine[j].Path })

	newest := since
	digests := make([]SessionDigest, 0, len(mine))
	for _, s := range mine {
		if s.Last.After(newest) {
			newest = s.Last
		}
		d, err := BuildDigest(s, repeatThreshold)
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, TranscriptError{s.Path, err.Error()})
			continue
		}
		digests = append(digests, d)
	}
	rep.Digested = len(digests)
	FlagDeepReads(digests, deepReadCap(opts))
	rep.warn(opts, digests)

	runDir, err := statefile.Path(opts.Project, filepath.Join("dreams", opts.RunID))
	if err != nil {
		return rep, err
	}
	rep.RunDir = runDir
	if len(digests) == 0 || opts.DryRun {
		rep.plan(opts, digests)
		return rep, nil
	}

	body, omitted := renderDigest(opts, rep.Since, digests, rep.Unreadable)
	rep.Omitted, rep.DigestBytes = omitted, len(body)
	deep := deepReads(digests)
	rep.DeepRead = len(deep)

	claims, err := LoadLedger(opts.Project)
	if err != nil {
		return rep, err
	}
	claimsBody, live := formatKnownClaims(opts.Project, claims)
	rep.KnownClaims = live

	// Both files land before the mint. A path unit watches the tasks
	// directory, so the task file appearing is what starts a worker, and
	// the run directory has to be finished by then.
	_, statErr := os.Stat(runDir)
	fresh := os.IsNotExist(statErr)
	if _, err := RunDir(opts.Project, opts.RunID); err != nil {
		return rep, err
	}
	if err := writeRunFile(runDir, DigestFile, body); err != nil {
		return rep, err
	}
	if err := writeRunFile(runDir, KnownClaimsFile, claimsBody); err != nil {
		return rep, err
	}
	projectRoot, err := filepath.Abs(filepath.Dir(opts.TasksDir))
	if err != nil {
		return rep, err
	}
	if err := writeRunFile(runDir, MemoryDirFile, MemoryDir(opts.ProjectsRoot, projectRoot)+"\n"); err != nil {
		return rep, err
	}

	slug, err := MintTask(opts.TasksDir, opts.Project, opts.RunID, runDir, deep)
	if err != nil {
		if fresh {
			discardRunDir(runDir)
		}
		return rep, err
	}
	rep.TaskSlug = slug

	if newest.IsZero() {
		newest = opts.Now
	}
	rep.Previous, rep.Watermark = wm.Last, newest.UTC().Format(time.RFC3339Nano)
	// An empty Last is the first night's start, "from the beginning",
	// and is pushed like any other position so rewind can reach it.
	history := append([]string{wm.Last}, wm.History...)
	if len(history) > maxWatermarkHistory {
		history = history[:maxWatermarkHistory]
	}
	next := watermark{Last: rep.Watermark, History: history, RunID: opts.RunID}
	if err := statefile.WriteJSONAtomic(wmPath, "dream-watermark", next); err != nil {
		return rep, err
	}
	return rep, nil
}

// plan fills in what a run that wrote nothing would have produced, so a
// dry run reports the same shape as a real one.
func (r *Report) plan(opts Options, ds []SessionDigest) {
	if !opts.DryRun {
		return
	}
	body, omitted := renderDigest(opts, r.Since, ds, r.Unreadable)
	r.Omitted, r.DigestBytes = omitted, len(body)
	r.DeepRead = len(deepReads(ds))
}

func loadWatermark(path string) watermark {
	var wm watermark
	b, err := os.ReadFile(path)
	if err != nil {
		return wm
	}
	_ = json.Unmarshal(b, &wm)
	return wm
}

func deepReadCap(opts Options) int {
	if opts.DeepReadCap == nil {
		return DefaultDeepReadCap
	}
	return *opts.DeepReadCap
}

func digestBudget(opts Options) int {
	if opts.DigestBudget == 0 {
		return defaultDigestBudget
	}
	return opts.DigestBudget
}

func deepReads(ds []SessionDigest) []DeepRead {
	var out []DeepRead
	for _, d := range ds {
		if !d.DeepRead || d.Entries <= 0 {
			continue
		}
		abs, err := filepath.Abs(d.Session.Path)
		if err != nil {
			continue
		}
		out = append(out, DeepRead{
			Session: filepath.Base(d.Session.Path),
			Path:    abs,
			Entries: d.Entries,
		})
	}
	return out
}

func (r *Report) warn(opts Options, ds []SessionDigest) {
	if r.Discovered == 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"no sessions at all under %s since %q: either nothing ran, or discovery has stopped matching",
			opts.ProjectsRoot, r.Since))
	} else if r.Sessions == 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%d session(s) discovered and none belong to project %q",
			r.Discovered, opts.Project))
	}
	if r.Sessions > 0 && r.Coordinator == 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%d session(s) in scope and no coordinator among them: the coordinator marker %q may no longer match the seed file",
			r.Sessions, coordinatorMarker))
	}
	for _, u := range r.Unreadable {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("could not read %s: %s", u.Path, u.Err))
	}
	for _, d := range ds {
		if d.Truncated {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"%s holds an entry too large to scan, so its digest covers only part of the session",
				filepath.Base(d.Session.Path)))
		}
	}
}

// renderDigest builds digest.md and returns the sessions it left out.
// Dropping is by score, lowest first, and the file names every session it
// dropped: a digest that is quietly short reads to the proposer as a
// complete account of the night.
func renderDigest(opts Options, since string, ds []SessionDigest, unreadable []TranscriptError) (string, []string) {
	keep, dropped := splitToBudget(ds, digestBudget(opts))
	if since == "" {
		since = "the beginning of the corpus"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Dream digest: %s, run %s\n\n", opts.Project, opts.RunID)
	fmt.Fprintf(&b, "sessions in this file: %d of %d\n", len(ds)-len(dropped), len(ds))
	fmt.Fprintf(&b, "window: activity after %s, up to %s\n\n",
		since, opts.Now.UTC().Format(time.RFC3339))

	kept := make([]SessionDigest, 0, len(ds))
	for i, d := range ds {
		if keep[i] {
			kept = append(kept, d)
		}
	}
	b.WriteString(FormatDigest(kept))

	var names []string
	for _, i := range dropped {
		names = append(names, filepath.Base(ds[i].Session.Path))
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "## Omitted to keep this file readable\n\n"+
			"%d session(s) scored lowest and were left out, the best of them\n"+
			"scoring %d. Nothing from them is below and no claim should rest on\n"+
			"them. They are listed highest scoring first.\n\n",
			len(names), ds[dropped[0]].Score)
		for n, i := range dropped {
			if n >= omittedListLimit {
				fmt.Fprintf(&b, "- and %d more, none scoring higher than the last line\n",
					len(dropped)-omittedListLimit)
				break
			}
			fmt.Fprintf(&b, "- %s (%s / %s, score %d)\n", filepath.Base(ds[i].Session.Path),
				ds[i].Session.Slug, ds[i].Session.Kind, ds[i].Score)
		}
		b.WriteString("\n")
	}
	if len(unreadable) > 0 {
		fmt.Fprintf(&b, "## Transcripts this run could not open\n\n"+
			"%d file(s). Nothing from them is in this digest, so the corpus\n"+
			"below is incomplete.\n\n", len(unreadable))
		for _, u := range unreadable {
			fmt.Fprintf(&b, "- %s: %s\n", u.Path, u.Err)
		}
		b.WriteString("\n")
	}
	return b.String(), names
}

// splitToBudget marks which digests fit. The highest scoring session is
// always kept even when it alone is over budget, because a digest with
// nothing in it is worse than a long one.
func splitToBudget(ds []SessionDigest, budget int) ([]bool, []int) {
	keep := make([]bool, len(ds))
	if len(ds) == 0 {
		return keep, nil
	}
	order := make([]int, len(ds))
	for i := range order {
		order[i] = i
	}
	// The same total order FlagDeepReads uses, so the two agree on which
	// sessions matter and the choice is stable across runs.
	sort.Slice(order, func(a, b int) bool {
		ia, ib := ds[order[a]], ds[order[b]]
		if ia.Score != ib.Score {
			return ia.Score > ib.Score
		}
		if ia.Session.Slug != ib.Session.Slug {
			return ia.Session.Slug < ib.Session.Slug
		}
		return ia.Session.Path < ib.Session.Path
	})
	var dropped []int
	total := 0
	for n, idx := range order {
		size := len(FormatDigest([]SessionDigest{ds[idx]}))
		if n > 0 && budget > 0 && total+size > budget {
			dropped = append(dropped, idx)
			continue
		}
		total += size
		keep[idx] = true
	}
	// dropped comes out in the same order, so the caller lists the
	// highest scoring casualty first and can truncate the tail honestly.
	return keep, dropped
}

// formatKnownClaims lists every claim still in play. The proposer can
// only reuse a claim's exact wording if it has that wording in front of
// it, and a paraphrase gets a new fingerprint and never reaches the bar
// that needs two independent sessions.
func formatKnownClaims(project string, l *Ledger) (string, int) {
	var live []*Entry
	for _, e := range l.Entries {
		if e.Status == StatusDead || e.Status == StatusRefuted {
			continue
		}
		live = append(live, e)
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].Claim != live[j].Claim {
			return live[i].Claim < live[j].Claim
		}
		return live[i].Type < live[j].Type
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# Known claims: %s\n\n", project)
	b.WriteString("Every claim already on file for this project, with its type and its\n" +
		"status. If your claim is the same claim as one of these, copy its text\n" +
		"character for character and keep its type unchanged. Do not improve the\n" +
		"wording. A reworded claim is a new claim: it gets its own fingerprint\n" +
		"and never reaches the bar that needs two independent sessions.\n\n")
	if len(live) == 0 {
		b.WriteString("This project's ledger is empty. No claim has been recorded yet.\n\n" +
			"That is the state of the ledger, not a verdict on your material, and\n" +
			"it does not mean wording is free. The phrasing you choose tonight is\n" +
			"what every later run has to copy, so write each claim in the canonical\n" +
			"form the brief asks for and keep it stable.\n")
		return b.String(), 0
	}
	fmt.Fprintf(&b, "%d claim(s) on file.\n\n", len(live))
	for _, e := range live {
		fmt.Fprintf(&b, "- type=%s status=%s sessions=%d claim: %s\n",
			e.Type, e.Status, len(e.Sessions), e.Claim)
	}
	return b.String(), len(live)
}

// discardRunDir takes back a run directory this call created and then
// could not mint for. Only the two files the run wrote are removed and
// the directory only goes if it is then empty, so a run directory that
// held anything else survives untouched.
func discardRunDir(runDir string) {
	for _, name := range []string{DigestFile, KnownClaimsFile, MemoryDirFile} {
		_ = os.Remove(filepath.Join(runDir, name))
	}
	_ = os.Remove(runDir)
}

func writeRunFile(runDir, name, body string) error {
	return os.WriteFile(filepath.Join(runDir, name), []byte(body), backupCopyMode)
}
