package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/versality/spore/internal/dream"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/statefile"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// dreamFixture is one scratch machine: a fake home holding the project
// root and the transcript corpus, plus its own state and config trees.
// Isolation matters more than usual here, because this command mints
// tasks a live fleet would spawn.
type dreamFixture struct {
	project     string
	root        string
	tasksDir    string
	home        string
	transcripts string
	state       string
	config      string
}

func newDreamFixture(t *testing.T) *dreamFixture {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := strings.ReplaceAll(t.Name(), "/", "_")
	f := &dreamFixture{
		project:     project,
		root:        filepath.Join(home, project),
		home:        home,
		transcripts: filepath.Join(home, ".claude", "projects"),
		state:       filepath.Join(base, "state"),
		config:      filepath.Join(base, "config"),
	}
	f.tasksDir = filepath.Join(f.root, "tasks")
	for _, d := range []string{f.tasksDir, f.transcripts, f.state, f.config} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", f.state)
	t.Setenv("XDG_CONFIG_HOME", f.config)
	return f
}

// enable writes the [dreams] table that turns the nightly job on.
func (f *dreamFixture) enable(t *testing.T, extra ...string) {
	t.Helper()
	dir := filepath.Join(f.config, "spore", f.project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[dreams]\nenabled = true\n" + strings.Join(extra, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "watch.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *dreamFixture) digest(t *testing.T, extra ...string) (int, string, string) {
	t.Helper()
	return f.run(t, append([]string{"digest",
		"--project-root", f.root,
		"--transcripts", f.transcripts}, extra...)...)
}

func (f *dreamFixture) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := dreamMain(&out, &errOut, args)
	return code, out.String(), errOut.String()
}

// correction writes one worker transcript carrying an operator
// correction, which is what scores a session above zero and so makes it
// eligible for a deep read.
func (f *dreamFixture) correction(t *testing.T, slug, day string) string {
	t.Helper()
	cwd := filepath.Join(f.home, f.project, ".worktrees", slug)
	dir := filepath.Join(f.transcripts, "-corpus--worktrees-"+slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := dreamUserLine(cwd, day+"T01:00:00Z", "# Goal") + "\n" +
		dreamUserLine(cwd, day+"T01:05:00Z", "no, always fetch origin first") + "\n"
	path := filepath.Join(dir, slug+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dreamUserLine(cwd, ts, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts +
		`","message":{"role":"user","content":"` + text + `"}}`
}

func (f *dreamFixture) statePath(t *testing.T, name string) string {
	t.Helper()
	p, err := statefile.Path(f.project, filepath.Join("dreams", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *dreamFixture) writeWatermark(t *testing.T, wm map[string]any) {
	t.Helper()
	if err := statefile.WriteJSONAtomic(f.statePath(t, "watermark.json"), "test-watermark", wm); err != nil {
		t.Fatal(err)
	}
}

func (f *dreamFixture) readWatermark(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(f.statePath(t, "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wm map[string]any
	if err := json.Unmarshal(b, &wm); err != nil {
		t.Fatal(err)
	}
	return wm
}

// runDirs lists the run directories under the project's dreams state.
func (f *dreamFixture) runDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(f.statePath(t, ""))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func (f *dreamFixture) taskFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.tasksDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// Scenario 1.
func TestDreamUsageOnAnUnusableSubcommand(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown subcommand": {"wat"},
		"no subcommand":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := dreamMain(&out, &errOut, args); code == 0 {
				t.Fatal("must not exit 0")
			}
			if !strings.Contains(errOut.String(), "usage: spore dream") {
				t.Errorf("no usage on stderr:\n%s", errOut.String())
			}
		})
	}
}

// Scenario 2.
func TestDreamRevertRequiresRunID(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := dreamMain(&out, &errOut, []string{"revert"}); code == 0 {
		t.Fatal("revert without a run id must not exit 0")
	}
	if !strings.Contains(errOut.String(), "usage: spore dream revert") {
		t.Errorf("no revert usage on stderr:\n%s", errOut.String())
	}
}

// Scenario 3. A disabled project that prints nothing is
// indistinguishable from a working run that found nothing.
func TestDreamDigestSaysSoWhenDreamsAreDisabled(t *testing.T) {
	f := newDreamFixture(t)

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("a disabled project must be a clean no-op, got exit %d\n%s%s", code, out, errOut)
	}
	said := out + errOut
	if !strings.Contains(said, "disabled") {
		t.Errorf("a disabled project must say so:\nstdout=%q\nstderr=%q", out, errOut)
	}
	if !strings.Contains(said, "watch.toml") {
		t.Errorf("the operator cannot act on this without the config path:\n%s", said)
	}
	if got := f.taskFiles(t); len(got) != 0 {
		t.Errorf("a disabled project minted %v", got)
	}
}

// Scenario 4. A quiet night is not an error, and the timer that alarms
// on it gets muted.
func TestDreamDigestQuietNightExitsZeroAndWarns(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("a quiet night must exit 0, got %d\n%s%s", code, out, errOut)
	}
	if got := f.taskFiles(t); len(got) != 0 {
		t.Errorf("a quiet night minted %v", got)
	}
	if !strings.Contains(errOut, dreamWarnToken) {
		t.Errorf("a quiet night must be warned about, stderr was:\n%q", errOut)
	}
	if !strings.Contains(errOut, "discovery") {
		t.Errorf("the warning must say the other reading is broken discovery:\n%q", errOut)
	}
	for _, want := range []string{"sessions=0", "task=none"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q:\n%s", want, out)
		}
	}
}

// Scenario 5.
func TestDreamDigestFailsAndNamesAMissingTranscriptsRoot(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	missing := filepath.Join(f.home, "no-such-corpus")

	code, out, errOut := f.digest(t, "--transcripts", missing)

	if code == 0 {
		t.Fatalf("a transcripts root that is not there must not exit 0\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, missing) {
		t.Errorf("the error does not name the path %q:\n%s", missing, errOut)
	}
	if !strings.Contains(errOut, dreamErrorToken) {
		t.Errorf("a failure must carry %q so a week of journal is greppable:\n%s", dreamErrorToken, errOut)
	}
}

// Scenario 6.
func TestDreamDigestDryRunWritesNothing(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")
	f.writeWatermark(t, map[string]any{"last": "2026-09-01T00:00:00Z"})
	before, err := os.ReadFile(f.statePath(t, "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := f.digest(t, "--dry-run")

	if code != 0 {
		t.Fatalf("dry run exit = %d, want 0\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "sessions=1") {
		t.Errorf("a dry run must still report its counts:\n%s", out)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("a dry run must be unmistakable in the journal:\n%s", out)
	}
	if dirs := f.runDirs(t); len(dirs) != 0 {
		t.Errorf("a dry run wrote run directories %v", dirs)
	}
	if got := f.taskFiles(t); len(got) != 0 {
		t.Errorf("a dry run minted %v", got)
	}
	after, err := os.ReadFile(f.statePath(t, "watermark.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a dry run moved the watermark:\nbefore=%s\nafter=%s", before, after)
	}
}

// Scenario 7. Nothing in the kernel promotes a draft, so the task is
// minted active and the CLI must not also try to start it.
func TestDreamDigestMintsAnActiveTaskAndDoesNotStartIt(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("digest exit = %d, want 0\n%s%s", code, out, errOut)
	}
	files := f.taskFiles(t)
	if len(files) != 1 {
		t.Fatalf("task files = %v, want exactly one", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "active" {
		t.Errorf("status = %q, want active: fleet.Reconcile spawns nothing else", m.Status)
	}
	slug := strings.TrimSuffix(filepath.Base(files[0]), ".md")
	if !strings.Contains(out, "task="+slug) {
		t.Errorf("summary does not name the minted task %q:\n%s", slug, out)
	}
	// task.Start also creates a worktree and a tmux session. Neither
	// exists, which is the observable half of "Start was not called".
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", slug)); err == nil {
		t.Error("a worktree exists: the CLI started the task instead of leaving it to the fleet")
	}
}

// This pins why scenario 7 is shaped that way: a `task.Start` after a
// successful mint fails on every run, because the mint is already active.
func TestTaskStartRefusesTheTaskDigestMints(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")

	if code, out, errOut := f.digest(t); code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	files := f.taskFiles(t)
	if len(files) != 1 {
		t.Fatalf("task files = %v, want exactly one", files)
	}
	slug := strings.TrimSuffix(filepath.Base(files[0]), ".md")

	_, err := task.Start(f.tasksDir, slug)
	if err == nil {
		t.Fatal("task.Start succeeded on a minted dream task; the plan's start step would work after all")
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Errorf("task.Start error = %v, want an already-active refusal", err)
	}
}

// Call 1: a corpus the job has lost access to must not drown the one
// line that says so.
func TestDreamDigestCapsTheWarningList(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	unreadable := dreamWarningLimit + 5
	for i := 0; i < unreadable; i++ {
		dir := filepath.Join(f.transcripts, fmt.Sprintf("-locked-%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "s.jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o000); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("an unreadable corpus is not a run failure, got exit %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, fmt.Sprintf("unreadable=%d", unreadable)) {
		t.Errorf("the summary must carry the full unreadable count:\n%s", out)
	}
	if n := strings.Count(errOut, dreamWarnToken); n > dreamWarningLimit {
		t.Errorf("%d warning lines, want at most %d", n, dreamWarningLimit)
	}
	if !strings.Contains(errOut, "more warning") {
		t.Errorf("a truncated warning list must say how many it dropped:\n%s", errOut)
	}
}

// Call 5: the config's cap wins over the dream package's own default,
// and a flag wins over both.
func TestDreamDigestDeepReadCapFlagOverridesTheConfig(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t, "deep_read_cap = 3")
	f.correction(t, "fix-a", "2026-09-02")
	f.correction(t, "fix-b", "2026-09-02")

	code, out, errOut := f.digest(t, "--deep-read-cap", "1")

	if code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"deep-read=1", "cap=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q, so the effective cap is not visible:\n%s", want, out)
		}
	}
}

func TestDreamDigestReportsTheConfiguredCapWhenNoFlagIsGiven(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t, "deep_read_cap = 2")
	f.correction(t, "fix-a", "2026-09-02")

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "cap=2") {
		t.Errorf("summary must show the cap the config set:\n%s", out)
	}
}

// Nothing reaped old run directories, so every night's few KB
// accumulated forever. A digest run must reclaim a run that is both
// old enough to be past any plausible judging delay and whose minted
// task is gone, without touching one whose task is still active.
func TestDreamDigestPrunesOldRunsWithGoneOrDoneTasksButKeepsLiveOnes(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)

	gone, err := dream.RunDir(f.project, "20200101-gone0")
	if err != nil {
		t.Fatal(err)
	}
	live, err := dream.RunDir(f.project, "20200101-live0")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	for _, dir := range []string{gone, live} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	slug := task.Slugify("dream 20200101-live0")
	m := frontmatter.Meta{Status: "active", Slug: slug, Title: "dream 20200101-live0", Project: f.project}
	if err := os.WriteFile(filepath.Join(f.tasksDir, slug+".md"), frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	f.correction(t, "fix-a", "2026-09-02")
	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "20200101-gone0") {
		t.Errorf("the prune must name what it removed:\n%s", out)
	}
	if strings.Contains(out, "20200101-live0") && strings.Contains(out, "removed") {
		t.Errorf("a run with a still-active task must not be reported removed:\n%s", out)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("run directory %s survived pruning", gone)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("run directory %s was pruned while its task is still active: %v", live, err)
	}
}

// Call 4: a human typing `spore dream digest` while the timer fires is
// a realistic Tuesday, and two Run calls race on the watermark.
// internal/watch documents deep_read_cap = 0 as a legal "do none of
// this". The config layer already resolves an absent key to the
// default, so by the time this value reaches dream.Options it is
// always an explicit choice; the bug was dream.Options substituting
// its own default back in for zero.
func TestDreamDigestConfiguredZeroCapMeansNoDeepReads(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t, "deep_read_cap = 0")
	f.correction(t, "fix-a", "2026-09-02")

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"deep-read=0", "cap=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q, so an explicit zero cap was not honored:\n%s", want, out)
		}
	}
}

func TestDreamDigestSkipsWhenAnotherRunHoldsTheLock(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")

	held := dreamHoldLock(t, f.project)
	defer held.Close()

	code, out, errOut := f.digest(t)

	if code != 0 {
		t.Fatalf("a contended lock is not a fault, got exit %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "lock") {
		t.Errorf("the second caller must say why it did nothing:\n%s", errOut)
	}
	if got := f.taskFiles(t); len(got) != 0 {
		t.Errorf("the second caller ran anyway and minted %v", got)
	}
}

// dreamHoldLock takes the lock from a second open file description,
// which flock treats as a different holder even inside one process.
func dreamHoldLock(t *testing.T, project string) *os.File {
	t.Helper()
	path, err := dreamLockPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fh.Close()
		t.Fatalf("could not take the lock the test needs held: %v", err)
	}
	return fh
}

// The wedge guard: a lock that outlives its holder would block every
// future night.
func TestDreamDigestReleasesTheLockForTheNextRun(t *testing.T) {
	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")

	if code, out, errOut := f.digest(t); code != 0 {
		t.Fatalf("first digest exit = %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := f.digest(t)
	if code != 0 {
		t.Fatalf("second digest exit = %d, want 0: the lock wedged\n%s%s", code, out, errOut)
	}
	if strings.Contains(errOut, "lock") {
		t.Errorf("the second run was refused by a stale lock:\n%s", errOut)
	}
}

// Scenario 10. The reconciler production runs is the only thing that
// turns a task file into a working agent. This measures how long it takes.
func TestDreamDigestMintsATaskTheFleetSpawns(t *testing.T) {
	requireToolchain(t)

	f := newDreamFixture(t)
	f.enable(t)
	f.correction(t, "fix-a", "2026-09-02")
	dreamGitInit(t, f.root)
	if err := fleet.Enable(); err != nil {
		t.Fatalf("fleet.Enable: %v", err)
	}
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	t.Cleanup(func() { dreamKillSessions(f.project) })

	started := time.Now()
	code, out, errOut := f.digest(t)
	if code != 0 {
		t.Fatalf("digest exit = %d\n%s%s", code, out, errOut)
	}
	// The task file is on disk here, which is the moment a systemd path
	// unit watching tasks/ would fire. Everything after it is the fleet.
	appeared := time.Now()
	files := f.taskFiles(t)
	if len(files) != 1 {
		t.Fatalf("task files = %v, want exactly one", files)
	}
	slug := strings.TrimSuffix(filepath.Base(files[0]), ".md")

	res, err := fleet.Reconcile(fleet.Config{
		TasksDir:    f.tasksDir,
		ProjectRoot: f.root,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("fleet.Reconcile: %v", err)
	}
	if !dreamContainsString(res.Spawned, slug) {
		t.Fatalf("the minted task was not spawned: Spawned = %v", res.Spawned)
	}
	session := "spore/" + f.project + "/" + slug
	if !dreamHasSession(session) {
		t.Fatalf("no live tmux session %q for the minted task", session)
	}
	t.Logf("digest %s, task file to live worker session %s",
		appeared.Sub(started).Round(time.Millisecond),
		time.Since(appeared).Round(time.Millisecond))
}

func dreamWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dreamRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dreamGitInit(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func dreamHasSession(name string) bool {
	return exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", name).Run() == nil
}

func dreamKillSessions(project string) {
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	prefix := "spore/" + project + "/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, prefix) {
			_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", line).Run()
		}
	}
}

func dreamContainsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
