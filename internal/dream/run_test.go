package dream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/fleet"
)

const testHome = "/home/agent"

// runFixture is one scratch machine: a projects root to discover in, a
// tasks directory owned by the project, and a state home the watermark,
// the ledger and the run directory all land under.
type runFixture struct {
	projects string
	tasksDir string
	state    string
	opts     Options
}

func newRunFixture(t *testing.T, project string) *runFixture {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	tasksDir := filepath.Join(t.TempDir(), project, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &runFixture{projects: t.TempDir(), tasksDir: tasksDir, state: state}
	f.opts = Options{
		ProjectsRoot: f.projects,
		Home:         testHome,
		Project:      project,
		TasksDir:     tasksDir,
		Now:          time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC),
		RunID:        "20260902-test",
		DeepReadCap:  intPtr(3),
	}
	return f
}

// worker writes one worker transcript for project under the fixture's
// projects root and returns its path.
func (f *runFixture) worker(t *testing.T, project, slug string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(f.projects, "-home-agent-"+project+"--worktrees-"+slug)
	return writeTranscript(t, dir, slug+".jsonl", lines...)
}

func (f *runFixture) coordinator(t *testing.T, project string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(f.projects, "-home-agent-"+project)
	return writeTranscript(t, dir, "coord.jsonl", lines...)
}

func workerCwd(project, slug string) string {
	return testHome + "/" + project + "/.worktrees/" + slug
}

func readRunFile(t *testing.T, runDir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func mustRun(t *testing.T, opts Options) Report {
	t.Helper()
	rep, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// Scenario 1.
func TestRunDigestsAnInScopeCorrectionAndMintsATask(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, always fetch origin first"))

	rep := mustRun(t, f.opts)

	if rep.Sessions != 1 {
		t.Fatalf("Sessions = %d, want 1: %+v", rep.Sessions, rep)
	}
	if rep.TaskSlug == "" {
		t.Fatal("no task was minted")
	}
	digest := readRunFile(t, rep.RunDir, DigestFile)
	if !strings.Contains(digest, "always fetch origin first") {
		t.Fatalf("operator correction missing from the digest:\n%s", digest)
	}
	if _, err := os.Stat(filepath.Join(f.tasksDir, rep.TaskSlug+".md")); err != nil {
		t.Fatalf("task file not written: %v", err)
	}
}

// Scenario 2.
func TestRunSecondPassSeesNothingNew(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, use the kernel flow"))

	first := mustRun(t, f.opts)
	if first.TaskSlug == "" {
		t.Fatal("the first pass minted nothing to hold a watermark for")
	}

	opts := f.opts
	opts.RunID = "20260902-second"
	second := mustRun(t, opts)

	if second.Sessions != 0 || second.TaskSlug != "" {
		t.Fatalf("the watermark did not hold: %+v", second)
	}
	entries, err := os.ReadDir(f.tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a second task was minted over the same sessions: %d task files", len(entries))
	}
}

// Scenario 3.
func TestRunDeepReadCarriesAnAbsolutePathAndTheRealEntryCount(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 6; i++ {
		lines = append(lines, toolErrorLine(cwd, fmt.Sprintf("2026-09-01T01:0%d:00Z", i+1),
			"bash: fleebnort: command not found"))
	}
	path := f.worker(t, "proj", "fix-a", lines...)
	wantEntries := len(lines)

	rep := mustRun(t, f.opts)

	if rep.DeepRead != 1 {
		t.Fatalf("DeepRead = %d, want 1: %+v", rep.DeepRead, rep)
	}
	body := readTask(t, f.tasksDir, rep.TaskSlug)
	if !strings.Contains(body, path) {
		t.Errorf("the task does not carry the absolute transcript path %s:\n%s", path, body)
	}
	if want := "(" + strconv.Itoa(wantEntries) + " entries)"; !strings.Contains(body, want) {
		t.Errorf("the task does not carry %q, the transcript's real line count:\n%s", want, body)
	}
}

// internal/watch documents deep_read_cap = 0 as a legal "do none of
// this", and the CLI passes that value straight into Options.DeepReadCap.
// Options has to carry the same meaning: an explicit zero is not "no cap
// named".
func TestRunOptionsDeepReadCapZeroMeansNoDeepReads(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 6; i++ {
		lines = append(lines, toolErrorLine(cwd, fmt.Sprintf("2026-09-01T01:0%d:00Z", i+1),
			"bash: fleebnort: command not found"))
	}
	f.worker(t, "proj", "fix-a", lines...)

	opts := f.opts
	opts.DeepReadCap = intPtr(0)
	rep := mustRun(t, opts)

	if rep.DeepRead != 0 {
		t.Fatalf("DeepRead = %d, want 0: an explicit zero cap was replaced by the default", rep.DeepRead)
	}
}

func intPtr(n int) *int { return &n }

// Scenario 4.
func TestRunWritesLiveClaimsVerbatimAndOmitsRefutedOnes(t *testing.T) {
	f := newRunFixture(t, "proj")
	const live = "Workers must fetch origin before branching."
	const refuted = "The gate runs green without the nix shell."

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	l.Observe(TypeOperatorPreference, live, "sesn-1", "2026-09-01")
	l.Observe(TypeProcessPattern, refuted, "sesn-2", "2026-09-01")
	l.Record(Fingerprint(TypeProcessPattern, refuted), StatusRefuted, "no evidence", "run-0")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	claims := readRunFile(t, rep.RunDir, KnownClaimsFile)
	if !strings.Contains(claims, live) {
		t.Errorf("the live claim is not carried byte for byte:\n%s", claims)
	}
	if !strings.Contains(claims, TypeOperatorPreference) {
		t.Errorf("the live claim's type is missing, so its fingerprint cannot be reproduced:\n%s", claims)
	}
	if strings.Contains(claims, refuted) {
		t.Errorf("a refuted claim was offered back to the proposer:\n%s", claims)
	}
}

// Scenario 5.
func TestRunWritesKnownClaimsForAnEmptyLedgerAndStillMints(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	if rep.TaskSlug == "" {
		t.Fatal("an empty ledger blocked the mint")
	}
	claims := readRunFile(t, rep.RunDir, KnownClaimsFile)
	if strings.TrimSpace(claims) == "" {
		t.Fatal("known-claims.md is empty; MintTask only checks that the file exists")
	}
	if !strings.Contains(claims, "empty") {
		t.Errorf("an empty ledger must say so, or the file reads as a complete list:\n%s", claims)
	}
	if !strings.Contains(claims, "character for character") {
		t.Errorf("the copy rule is missing, so an empty ledger invites fresh wording:\n%s", claims)
	}
}

// Scenario 6.
func TestRunReportsATranscriptItCouldNotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not deny access")
	}
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))
	blocked := f.worker(t, "proj", "fix-b", userLine(workerCwd("proj", "fix-b"),
		"2026-09-01T01:00:00Z", "# Goal"))
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o644) })

	rep := mustRun(t, f.opts)

	if len(rep.Unreadable) != 1 || rep.Unreadable[0].Path != blocked {
		t.Fatalf("an unreadable transcript was dropped silently: Unreadable = %+v", rep.Unreadable)
	}
	digest := readRunFile(t, rep.RunDir, DigestFile)
	if !strings.Contains(digest, filepath.Base(blocked)) {
		t.Errorf("the digest does not tell the proposer its corpus is incomplete:\n%s", digest)
	}
}

// Scenario 7.
func TestRunDryRunWritesNothing(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, always fetch origin first"))

	opts := f.opts
	opts.DryRun = true
	rep := mustRun(t, opts)

	if rep.Sessions != 1 {
		t.Fatalf("a dry run must still report what it found: %+v", rep)
	}
	if rep.TaskSlug != "" {
		t.Errorf("a dry run minted task %q", rep.TaskSlug)
	}
	if entries, _ := os.ReadDir(rep.RunDir); len(entries) != 0 {
		t.Errorf("a dry run left %d file(s) in the run directory", len(entries))
	}
	if entries, _ := os.ReadDir(f.tasksDir); len(entries) != 0 {
		t.Errorf("a dry run left %d file(s) in the tasks directory", len(entries))
	}
	wm := filepath.Join(f.state, "spore", "proj", "dreams", "watermark.json")
	if _, err := os.Stat(wm); !os.IsNotExist(err) {
		t.Errorf("a dry run advanced the watermark at %s", wm)
	}
}

// Scenario 8.
func TestRunMintsATaskTheFleetSpawns(t *testing.T) {
	requireToolchain(t)

	const project = "dreamrun"
	f := newRunFixture(t, project)
	projectRoot := filepath.Dir(f.tasksDir)
	gitInit(t, projectRoot)
	if err := fleet.Enable(); err != nil {
		t.Fatalf("fleet.Enable: %v", err)
	}
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	t.Cleanup(func() { killSporeSessions(projectRoot) })

	cwd := workerCwd(project, "fix-a")
	f.worker(t, project, "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, always fetch origin first"))

	rep := mustRun(t, f.opts)
	if rep.TaskSlug == "" {
		t.Fatal("no task was minted")
	}

	res, err := fleet.Reconcile(fleet.Config{
		TasksDir:    f.tasksDir,
		ProjectRoot: projectRoot,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("fleet.Reconcile: %v", err)
	}
	if !contains(res.Spawned, rep.TaskSlug) {
		t.Fatalf("the minted task was not spawned: Spawned = %v", res.Spawned)
	}
	if !hasTmuxSession("spore/" + project + "/" + rep.TaskSlug) {
		t.Error("no live tmux session for the minted task")
	}
}

// A projects root that is not there is a configuration fault, and
// Discover reports it as an empty corpus. Run must not pass that on as a
// quiet night.
func TestRunRefusesAProjectsRootThatIsNotThere(t *testing.T) {
	f := newRunFixture(t, "proj")
	opts := f.opts
	opts.ProjectsRoot = filepath.Join(t.TempDir(), "not-there")
	if _, err := Run(opts); err == nil {
		t.Fatal("expected an error for a projects root that does not exist")
	}
}

// The coordinator marker is a literal string in discover.go. Reword the
// seed file and every coordinator session vanishes, which otherwise
// reads as a corpus of workers.
func TestRunWarnsWhenNoCoordinatorSessionIsInACorpusThatHasSessions(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	if rep.Coordinator != 0 || rep.Worker != 1 {
		t.Fatalf("kind counts wrong: %+v", rep)
	}
	if !hasWarning(rep, "coordinator") {
		t.Fatalf("no warning about a corpus with no coordinator session: %v", rep.Warnings)
	}
}

// The counterpart guard: a corpus that does hold a coordinator session
// counts it and stays quiet, so the warning above cannot be a constant.
func TestRunCountsACoordinatorSessionAndDoesNotWarnAboutIt(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))
	f.coordinator(t, "proj", userLine(testHome+"/proj", "2026-09-01T01:00:00Z",
		"# Coordinator role (shared)"))

	rep := mustRun(t, f.opts)

	if rep.Coordinator != 1 || rep.Worker != 1 {
		t.Fatalf("kind counts wrong: %+v", rep)
	}
	if hasWarning(rep, "coordinator") {
		t.Errorf("warned about a corpus that has a coordinator session: %v", rep.Warnings)
	}
}

func TestRunReportsAQuietNightWithoutAnError(t *testing.T) {
	f := newRunFixture(t, "proj")

	rep := mustRun(t, f.opts)

	if rep.Sessions != 0 || rep.Discovered != 0 || rep.TaskSlug != "" {
		t.Fatalf("expected an empty run: %+v", rep)
	}
	if !hasWarning(rep, "no sessions") {
		t.Fatalf("an empty corpus must be flagged, not reported as a normal quiet night: %v", rep.Warnings)
	}
}

// Only this project's sessions are in scope, but the report has to say
// how much it looked at, or a project filter that stops matching reads
// as a quiet night.
func TestRunReportsWhatItDiscoveredBeforeTheProjectFilter(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))
	f.worker(t, "other", "fix-b", userLine(workerCwd("other", "fix-b"),
		"2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	if rep.Discovered != 2 {
		t.Errorf("Discovered = %d, want 2 across both projects", rep.Discovered)
	}
	if rep.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1 after the project filter", rep.Sessions)
	}
}

// Call 2: Run writes no manifest because it changes nothing outside its
// own run directory. Revert must therefore refuse, not report a
// successful revert of nothing.
func TestRunLeavesNoManifestSoRevertRefuses(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	if _, err := os.Stat(filepath.Join(rep.RunDir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("Run wrote a manifest it cannot seal: %v", err)
	}
	if _, err := Revert("proj", f.opts.RunID); err == nil {
		t.Fatal("Revert of an unsnapshotted run must refuse, not report success")
	}
}

// Call 3: the watermark advances only when a task was minted, and it
// records what it replaced so a lost night can be put back.
func TestRunWatermarkAdvancesOnMintAndKeepsThePreviousValue(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))

	first := mustRun(t, f.opts)
	if first.Watermark != "2026-09-01T01:00:00Z" {
		t.Fatalf("Watermark = %q, want the newest session's last activity", first.Watermark)
	}

	f.worker(t, "proj", "fix-b", userLine(workerCwd("proj", "fix-b"),
		"2026-09-01T02:00:00Z", "# Goal"))
	opts := f.opts
	opts.RunID = "20260902-second"
	second := mustRun(t, opts)

	if second.Watermark != "2026-09-01T02:00:00Z" {
		t.Fatalf("Watermark = %q, want the second night's newest session", second.Watermark)
	}
	if second.Previous != first.Watermark {
		t.Fatalf("Previous = %q, want %q: without it a lost night cannot be put back",
			second.Previous, first.Watermark)
	}
	if second.RunID != readWatermarkRunID(t, f.state, "proj") {
		t.Error("the watermark does not name the run that advanced it")
	}
}

func TestRunHoldsTheWatermarkWhenTheMintFails(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))
	opts := f.opts
	opts.TasksDir = filepath.Join(t.TempDir(), "wrongproject", "tasks")

	if _, err := Run(opts); err == nil {
		t.Fatal("expected the mint to refuse a tasks dir owned by another project")
	}
	wm := filepath.Join(f.state, "spore", "proj", "dreams", "watermark.json")
	if _, err := os.Stat(wm); !os.IsNotExist(err) {
		t.Fatal("a failed run advanced the watermark, so the sessions it never judged are lost")
	}
	runDir := filepath.Join(f.state, "spore", "proj", "dreams", f.opts.RunID)
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Errorf("a failed run left %s behind; a job that fails nightly grows one of these a night", runDir)
	}
}

// A run directory that was already there is somebody else's, so a failed
// mint must not take it down with it.
func TestRunKeepsARunDirectoryItDidNotCreateWhenTheMintFails(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))
	runDir, err := RunDir("proj", f.opts.RunID)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "packets.json"), "{}\n")

	opts := f.opts
	opts.TasksDir = filepath.Join(t.TempDir(), "wrongproject", "tasks")
	if _, err := Run(opts); err == nil {
		t.Fatal("expected the mint to refuse")
	}
	if _, err := os.Stat(filepath.Join(runDir, "packets.json")); err != nil {
		t.Fatalf("a failed run deleted a run directory it did not create: %v", err)
	}
}

// The digest is the only place the proposer learns what a session was
// asked to do, which is how it tells a session that discussed a lesson
// from one that demonstrated it.
func TestDigestRendersTheOpeningAssignmentAndNotTheFinalReport(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"
	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "Cover the broker publication chain"),
		`{"type":"assistant","cwd":"`+cwd+`","timestamp":"2026-09-01T01:09:00Z",`+
			`"message":{"role":"assistant","content":[{"type":"text",`+
			`"text":"DONE. I proved that workers must fetch origin first."}]}}`)
	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}

	out := FormatDigest([]SessionDigest{d})

	if !strings.Contains(out, "Cover the broker publication chain") {
		t.Errorf("the session's opening assignment is missing:\n%s", out)
	}
	if !strings.Contains(out, "not evidence") {
		t.Errorf("the assignment is not labelled as the session's subject rather than evidence:\n%s", out)
	}
	if strings.Contains(out, "I proved that workers must fetch origin first") {
		t.Errorf("the agent's own closing prose was rendered as digest material:\n%s", out)
	}
}

func TestBuildDigestCountsEveryEntryInTheTranscript(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"
	lines := []string{
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:01:00Z", "no, use the kernel flow"),
		`{"type":"assistant","cwd":"` + cwd + `","timestamp":"2026-09-01T01:02:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`,
		`not json at all`,
	}
	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entries != len(lines) {
		t.Fatalf("Entries = %d, want %d: the count is the denominator of the proposer's coverage claim",
			d.Entries, len(lines))
	}
	if d.Truncated {
		t.Error("a complete transcript was marked truncated")
	}
}

// A single JSONL line over the scanner's 8 MB cap stops the scan early.
// The digest then silently describes part of a session as the whole.
func TestBuildDigestFlagsATranscriptTheScannerCouldNotFinish(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"
	huge := `{"type":"assistant","cwd":"` + cwd + `","timestamp":"2026-09-01T01:01:00Z",` +
		`"message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", 9*1024*1024) + `"}]}}`
	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"), huge)

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Truncated {
		t.Fatal("an oversized line stopped the scan and nothing recorded it")
	}
}

// Call 1: the nightly slice is small, but the first ever run has no
// watermark and reads the whole corpus. The budget only binds there, and
// when it binds the file has to say what it left out.
func TestRunBoundsTheDigestAndNamesWhatItDropped(t *testing.T) {
	f := newRunFixture(t, "proj")
	loud := workerCwd("proj", "loud")
	f.worker(t, "proj", "loud",
		userLine(loud, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(loud, "2026-09-01T01:01:00Z", "no, always fetch origin first"),
		toolErrorLine(loud, "2026-09-01T01:02:00Z", "bash: fleebnort: command not found"))
	f.worker(t, "proj", "quiet",
		userLine(workerCwd("proj", "quiet"), "2026-09-01T01:00:00Z", "# Goal"))

	opts := f.opts
	opts.DigestBudget = 400
	rep := mustRun(t, opts)

	digest := readRunFile(t, rep.RunDir, DigestFile)
	if !strings.Contains(digest, "always fetch origin first") {
		t.Errorf("the highest scoring session was dropped:\n%s", digest)
	}
	if len(rep.Omitted) != 1 || !strings.Contains(rep.Omitted[0], "quiet") {
		t.Fatalf("Omitted = %v, want the one low scoring session", rep.Omitted)
	}
	if !strings.Contains(digest, rep.Omitted[0]) {
		t.Errorf("the digest does not name what it dropped, so the drop is silent:\n%s", digest)
	}
}

// Measured on this host: a first run with no watermark drops 327 of 367
// sessions, and naming every one of them costs 29 KB of the file the
// budget exists to bound.
func TestRunBoundsTheListOfWhatItDropped(t *testing.T) {
	f := newRunFixture(t, "proj")
	for i := 0; i < omittedListLimit+12; i++ {
		slug := fmt.Sprintf("fix-%03d", i)
		cwd := workerCwd("proj", slug)
		f.worker(t, "proj", slug,
			userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
			userLine(cwd, fmt.Sprintf("2026-09-01T01:0%d:00Z", i%10), "no, use the kernel flow"))
	}

	opts := f.opts
	opts.DigestBudget = 500
	rep := mustRun(t, opts)

	if len(rep.Omitted) < omittedListLimit+1 {
		t.Fatalf("expected the budget to bind hard, Omitted = %d", len(rep.Omitted))
	}
	digest := readRunFile(t, rep.RunDir, DigestFile)
	named := 0
	for _, name := range rep.Omitted {
		if strings.Contains(digest, "- "+name) {
			named++
		}
	}
	if named > omittedListLimit {
		t.Errorf("the digest names %d dropped sessions, more than the %d cap", named, omittedListLimit)
	}
	if !strings.Contains(digest, fmt.Sprintf("%d session(s)", len(rep.Omitted))) {
		t.Errorf("the digest does not state how many sessions it dropped:\n%s", digest)
	}
}

func TestRunLeavesTheDigestWholeWhenItFitsTheBudget(t *testing.T) {
	f := newRunFixture(t, "proj")
	f.worker(t, "proj", "fix-a", userLine(workerCwd("proj", "fix-a"),
		"2026-09-01T01:00:00Z", "# Goal"))

	rep := mustRun(t, f.opts)

	if len(rep.Omitted) != 0 {
		t.Fatalf("a digest well under the budget dropped %v", rep.Omitted)
	}
	if strings.Contains(readRunFile(t, rep.RunDir, DigestFile), "Omitted") {
		t.Error("a digest that dropped nothing must not carry an omission notice")
	}
}

func hasWarning(rep Report, substr string) bool {
	for _, w := range rep.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func readWatermarkRunID(t *testing.T, state, project string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "spore", project, "dreams", "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wm struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(b, &wm); err != nil {
		t.Fatal(err)
	}
	return wm.RunID
}
