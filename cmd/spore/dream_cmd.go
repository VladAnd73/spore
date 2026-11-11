package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/versality/spore/internal/dream"
	"github.com/versality/spore/internal/statefile"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

const dreamUsage = `usage: spore dream <digest|gate|reviewer-brief|write|runs|revert|rewind> [flags]

  digest [--project-root DIR] [--transcripts DIR] [--deep-read-cap N] [--dry-run]
         Digest the sessions this project produced since the last run and
         mint one proposer task. Exits non-zero only when the run itself
         failed: a night with no sessions exits 0 and warns.
  gate <run-id> [--project NAME] [--threshold N]
         Apply the two-tier evidence bar to every packet the proposer
         wrote this run and print which ones cleared. Run this after
         writing packets/<n>.json and before spawning a reviewer for
         any of them.
  reviewer-brief
         Print the reviewer brief to stdout, so a worker on any project
         can build a reviewer subagent's prompt without this repo's
         source tree: the binary is all a consumer project has.
  write <run-id> [--project NAME] [--max-writes N]
         Record every reviewer verdict, then write the confirmed
         survivors: snapshot every target once, write lesson, memory
         and skill-proposal tiers, seal the run, and write report.md.
         A confirmed packet past --max-writes is held as a candidate,
         not discarded.
  runs   [--project NAME]
         List this project's run directories, newest first.
  revert <run-id> [--project NAME]
         Put a run's targets back. Refuses to touch anything that changed
         after the run was sealed, and says what it refused.
  rewind [--project NAME]
         Move the watermark back to its previous value, so the sessions a
         run consumed and no agent ever judged are read again.

--transcripts is the session corpus to read, default $HOME/.claude/projects.
--project-root is the repo whose tasks/ a minted task lands in, default cwd.
--deep-read-cap overrides the [dreams] deep_read_cap setting for one run.
--threshold overrides the [dreams] recurrence_threshold setting for one run.
--max-writes overrides the [dreams] max_writes_per_run setting for one run.
`

const (
	dreamGateUsage   = "usage: spore dream gate <run-id> [--project NAME] [--threshold N]"
	dreamWriteUsage  = "usage: spore dream write <run-id> [--project NAME] [--max-writes N]"
	dreamRevertUsage = "usage: spore dream revert <run-id> [--project NAME]"
	dreamRewindUsage = "usage: spore dream rewind [--project NAME]"
	dreamRunsUsage   = "usage: spore dream runs [--project NAME]"
)

// This command runs unattended from a nightly timer, so everything it
// prints lands in a journal nobody reads until something is wrong. Both
// tokens exist so one week of journal is greppable for exactly that:
// dream-warn: is a run that finished with something a human should know,
// dream-error: is a run that failed. Warnings are capped because a
// corpus the job has lost access to produces one per file, and 300 of
// them bury the line that says what happened.
const (
	dreamWarnToken    = "dream-warn:"
	dreamErrorToken   = "dream-error:"
	dreamWarningLimit = 20
)

// errDreamLocked is contention, not failure: another run is already
// doing the work this one would have done.
var errDreamLocked = errors.New("another dream run holds the lock")

func runDream(args []string) int { return dreamMain(os.Stdout, os.Stderr, args) }

func dreamMain(out, errOut io.Writer, args []string) int {
	if len(args) < 1 {
		fmt.Fprint(errOut, dreamUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(out, dreamUsage)
		return 0
	case "digest":
		return dreamDigest(out, errOut, args[1:])
	case "gate":
		return dreamGate(out, errOut, args[1:])
	case "reviewer-brief":
		fmt.Fprint(out, dream.ReviewerBrief)
		return 0
	case "write":
		return dreamWriteCmd(out, errOut, args[1:])
	case "runs":
		return dreamRuns(out, errOut, args[1:])
	case "revert":
		return dreamRevert(out, errOut, args[1:])
	case "rewind":
		return dreamRewind(out, errOut, args[1:])
	default:
		fmt.Fprintf(errOut, "spore dream: unknown subcommand %q\n\n%s", args[0], dreamUsage)
		return 2
	}
}

func dreamDigest(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream digest", flag.ContinueOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	transcripts := fs.String("transcripts", "", "session corpus (default: $HOME/.claude/projects)")
	capFlag := fs.Int("deep-read-cap", 0, "sessions to deep-read (default: the [dreams] setting)")
	dryRun := fs.Bool("dry-run", false, "report without writing or minting")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamUsage, err)
	}
	cwd, project, err := dreamContext(*root)
	if err != nil {
		return dreamFail(errOut, "digest", err)
	}
	cfg, err := watch.LoadDreamsConfig(project)
	if err != nil {
		return dreamFail(errOut, "digest", err)
	}
	if !cfg.Enabled {
		// A silent no-op here is indistinguishable from a working run
		// that found nothing, and the config path is the only part of
		// this an operator can act on.
		fmt.Fprintf(out, "dream %s: disabled, nothing to do; set enabled = true under [dreams] in %s\n",
			project, watch.TomlPath(project))
		return 0
	}

	lock, err := dreamLock(project)
	if errors.Is(err, errDreamLocked) {
		fmt.Fprintf(errOut, "%s %s: %v, so this run did nothing\n", dreamWarnToken, project, err)
		return 0
	}
	if err != nil {
		return dreamFail(errOut, "digest", err)
	}
	defer lock.Close()

	deepCap := cfg.DeepReadCap
	if *capFlag > 0 {
		deepCap = *capFlag
	}
	now := time.Now().UTC()
	rep, runErr := dream.Run(dream.Options{
		ProjectsRoot: dreamTranscripts(*transcripts),
		Home:         os.Getenv("HOME"),
		Project:      project,
		TasksDir:     filepath.Join(cwd, "tasks"),
		Now:          now,
		RunID:        now.Format("20060102") + "-" + shortSuffix(now),
		DeepReadCap:  &deepCap,
		DryRun:       *dryRun,
	})
	// Verdict first, detail after: the first line of a run in the
	// journal says whether it worked, and the warnings under it say
	// what a reader should look at.
	if runErr != nil {
		code := dreamFail(errOut, "digest", runErr)
		dreamWarnings(errOut, rep.Warnings)
		return code
	}
	fmt.Fprintln(out, dreamSummary(rep, deepCap))
	dreamWarnings(errOut, rep.Warnings)
	if !*dryRun {
		dreamPrune(out, errOut, project, filepath.Join(cwd, "tasks"), now)
	}
	return 0
}

// dreamPrune reaps run directories old enough that their minted task is
// long past any plausible judging delay, and whose task is done or gone
// from tasksDir. A failure here is a warning, not a run failure: it
// costs a few KB of disk, not a night's work.
func dreamPrune(out, errOut io.Writer, project, tasksDir string, now time.Time) {
	prep, err := dream.Prune(project, tasksDir, now)
	if err != nil {
		fmt.Fprintf(errOut, "%s %s: prune: %v\n", dreamWarnToken, project, err)
		return
	}
	if len(prep.Removed) > 0 {
		fmt.Fprintf(out, "dream prune %s: removed %d run(s): %s\n",
			project, len(prep.Removed), strings.Join(prep.Removed, ", "))
	}
}

// dreamSummary is the one line every night leaves behind. It is
// key=value because the questions asked of a week of these lines are
// answered by grepping one field: why nothing happened (sessions),
// whether discovery still works (sessions against discovered), and when
// the watermark stopped moving.
func dreamSummary(rep dream.Report, deepCap int) string {
	head := fmt.Sprintf("dream %s %s", rep.Project, rep.RunID)
	if rep.DryRun {
		head += " (dry run, nothing written)"
	}
	return fmt.Sprintf("%s: sessions=%d/%d digested=%d deep-read=%d cap=%d "+
		"digest=%dB omitted=%d claims=%d unreadable=%d task=%s since=%s watermark=%s",
		head, rep.Sessions, rep.Discovered, rep.Digested, rep.DeepRead, deepCap,
		rep.DigestBytes, len(rep.Omitted), rep.KnownClaims, len(rep.Unreadable),
		orNone(rep.TaskSlug), orNone(rep.Since), orNone(rep.Watermark))
}

func dreamWarnings(errOut io.Writer, warnings []string) {
	for i, w := range warnings {
		if i >= dreamWarningLimit {
			fmt.Fprintf(errOut, "and %d more warning(s), not printed so this run stays readable\n",
				len(warnings)-dreamWarningLimit)
			return
		}
		fmt.Fprintf(errOut, "%s %s\n", dreamWarnToken, w)
	}
}

func dreamGate(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream gate", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: from cwd)")
	thresholdFlag := fs.Int("threshold", 0, "independent sessions required (default: the [dreams] setting)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamGateUsage, err)
	}
	if fs.NArg() != 1 {
		return dreamBadUsage(errOut, dreamGateUsage, nil)
	}
	runID := fs.Arg(0)
	project, err := dreamProject(*projectFlag)
	if err != nil {
		return dreamFail(errOut, "gate", err)
	}
	cfg, err := watch.LoadDreamsConfig(project)
	if err != nil {
		return dreamFail(errOut, "gate", err)
	}
	threshold := cfg.RecurrenceThreshold
	if *thresholdFlag > 0 {
		threshold = *thresholdFlag
	}
	runDir, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return dreamFail(errOut, "gate", err)
	}
	if _, err := os.Stat(runDir); err != nil {
		fmt.Fprintf(errOut, "%s spore dream gate: no run %q for project %s: %s does not exist\n",
			dreamErrorToken, runID, project, runDir)
		return 1
	}
	lock, err := dreamLock(project)
	if err != nil {
		return dreamFail(errOut, "gate", err)
	}
	defer lock.Close()

	results, err := dream.GateRun(project, runID, runDir, threshold)
	if err != nil {
		return dreamFail(errOut, "gate", err)
	}
	cleared := 0
	for _, r := range results {
		state := "held"
		if r.Cleared {
			state, cleared = "cleared", cleared+1
		}
		fmt.Fprintf(out, "%d: %s fingerprint=%s\n", r.N, state, r.Fingerprint)
	}
	fmt.Fprintf(out, "dream gate %s %s: packets=%d cleared=%d held=%d threshold=%d\n",
		project, runID, len(results), cleared, len(results)-cleared, threshold)
	return 0
}

func dreamWriteCmd(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream write", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: from cwd)")
	maxWritesFlag := fs.Int("max-writes", 0, "confirmed packets to write (default: the [dreams] setting)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamWriteUsage, err)
	}
	if fs.NArg() != 1 {
		return dreamBadUsage(errOut, dreamWriteUsage, nil)
	}
	runID := fs.Arg(0)
	project, err := dreamProject(*projectFlag)
	if err != nil {
		return dreamFail(errOut, "write", err)
	}
	cfg, err := watch.LoadDreamsConfig(project)
	if err != nil {
		return dreamFail(errOut, "write", err)
	}
	maxWrites := cfg.MaxWritesPerRun
	if *maxWritesFlag > 0 {
		maxWrites = *maxWritesFlag
	}
	runDir, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return dreamFail(errOut, "write", err)
	}
	if _, err := os.Stat(runDir); err != nil {
		fmt.Fprintf(errOut, "%s spore dream write: no run %q for project %s: %s does not exist\n",
			dreamErrorToken, runID, project, runDir)
		return 1
	}
	lock, err := dreamLock(project)
	if err != nil {
		return dreamFail(errOut, "write", err)
	}
	defer lock.Close()

	rep, err := dream.WriteRun(project, runID, runDir, maxWrites)
	if err != nil {
		return dreamFail(errOut, "write", err)
	}
	for _, w := range rep.Written {
		fmt.Fprintf(out, "written [%s] %s -> %s\n", w.Tier, w.Claim, w.Target)
	}
	for _, r := range rep.Refused {
		fmt.Fprintf(out, "refused [%s] %s: %s\n", r.Verdict, r.Claim, r.Reason)
	}
	for _, h := range rep.Held {
		fmt.Fprintf(out, "held [%s] %s -> %s\n", h.Tier, h.Claim, h.Target)
	}
	fmt.Fprintf(out, "dream write %s %s: written=%d refused=%d held=%d skill-proposals=%d report=%s\n",
		project, runID, len(rep.Written), len(rep.Refused), len(rep.Held), len(rep.SkillProposals),
		filepath.Join(runDir, "report.md"))
	return 0
}

func dreamRuns(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream runs", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: from cwd)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamRunsUsage, err)
	}
	project, err := dreamProject(*projectFlag)
	if err != nil {
		return dreamFail(errOut, "runs", err)
	}
	dir, err := statefile.Path(project, "dreams")
	if err != nil {
		return dreamFail(errOut, "runs", err)
	}
	runs, err := dream.ListRuns(project)
	if err != nil {
		return dreamFail(errOut, "runs", err)
	}
	if len(runs) == 0 {
		fmt.Fprintf(out, "dream runs %s: no runs under %s\n", project, dir)
		return 0
	}
	fmt.Fprintf(out, "dream runs %s: %d run(s) under %s, newest first\n", project, len(runs), dir)
	for _, r := range runs {
		state := "revertible"
		if !r.Revertible {
			state = "no manifest, revert unavailable"
		}
		fmt.Fprintf(out, "%s  %s (%s)  %s\n",
			r.RunID, r.When.UTC().Format(time.RFC3339), r.Dated, state)
	}
	return 0
}

func dreamRevert(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream revert", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: from cwd)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamRevertUsage, err)
	}
	if fs.NArg() != 1 {
		return dreamBadUsage(errOut, dreamRevertUsage, nil)
	}
	runID := fs.Arg(0)
	project, err := dreamProject(*projectFlag)
	if err != nil {
		return dreamFail(errOut, "revert", err)
	}
	dir, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return dreamFail(errOut, "revert", err)
	}
	lock, err := dreamLock(project)
	if err != nil {
		return dreamFail(errOut, "revert", err)
	}
	defer lock.Close()

	rep, revertErr := dream.RevertWithReport(project, runID)
	if revertErr != nil && dreamRevertTouchedNothing(rep) {
		// An all-zero summary would read as a successful no-op, so the
		// only thing printed here is the failure.
		code := dreamFail(errOut, "revert", revertErr)
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); os.IsNotExist(err) && !errors.Is(revertErr, dream.ErrAlreadyReverted) {
			fmt.Fprintln(errOut, "nothing was put back. A digest run snapshots nothing,"+
				" so it records nothing to undo; the stage that writes into the harness is what will.")
		}
		return code
	}
	// The file revert can succeed while the ledger still believes this
	// run's claims were written, which would refuse them the gate
	// forever even after their content is back. Best-effort: a failure
	// here is worth a warning, not a reason to call the revert failed.
	if err := dream.RevertRunLedger(project, runID); err != nil {
		fmt.Fprintf(errOut, "%s ledger entries for run %s were not reverted to candidate: %s\n",
			dreamWarnToken, runID, err)
	}
	fmt.Fprintf(out, "dream revert %s %s: restored=%d removed=%d skipped=%d failed=%d\n",
		project, runID, len(rep.Restored), len(rep.Removed), len(rep.Skipped), len(rep.Failed))
	for _, p := range rep.Restored {
		fmt.Fprintln(out, "restored", p)
	}
	for _, p := range rep.Removed {
		fmt.Fprintln(out, "removed", p)
	}
	// Skipped is the answer to the question an operator actually has
	// after a bad night: what did the undo deliberately not touch.
	for _, s := range rep.Skipped {
		fmt.Fprintf(errOut, "%s left alone %s: %s\n", dreamWarnToken, s.Path, s.Reason)
	}
	for _, p := range rep.Failed {
		fmt.Fprintf(errOut, "%s could not restore %s: %s\n", dreamErrorToken, p.Path, p.Err)
	}
	if revertErr != nil {
		return dreamFail(errOut, "revert", revertErr)
	}
	return 0
}

func dreamRevertTouchedNothing(rep dream.RevertReport) bool {
	return len(rep.Restored) == 0 && len(rep.Removed) == 0 &&
		len(rep.Skipped) == 0 && len(rep.Failed) == 0
}

func dreamRewind(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("dream rewind", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: from cwd)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return dreamBadUsage(errOut, dreamRewindUsage, err)
	}
	project, err := dreamProject(*projectFlag)
	if err != nil {
		return dreamFail(errOut, "rewind", err)
	}
	lock, err := dreamLock(project)
	if err != nil {
		return dreamFail(errOut, "rewind", err)
	}
	defer lock.Close()

	res, err := dream.Rewind(project)
	if err != nil {
		return dreamFail(errOut, "rewind", err)
	}
	prev := res.Previous
	if prev == "" {
		prev = "the beginning"
	}
	fmt.Fprintf(out, "dream rewind %s: last %s -> %s; the sessions run %s consumed are in scope again\n",
		project, res.Last, prev, orNone(res.RunID))
	return 0
}

// dreamContext resolves the repo whose tasks directory a minted task
// lands in, plus the project name all of this project's state is keyed
// by. It does not chdir: the nightly job runs for a project that is not
// its own working directory.
func dreamContext(root string) (cwd, project string, err error) {
	if root == "" {
		if root, err = os.Getwd(); err != nil {
			return "", "", err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", "", err
	}
	project, err = task.ProjectName(abs)
	if err != nil {
		return "", "", err
	}
	return abs, project, nil
}

func dreamProject(name string) (string, error) {
	if strings.TrimSpace(name) != "" {
		return name, nil
	}
	_, project, err := dreamContext("")
	return project, err
}

func dreamTranscripts(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	return filepath.Join(os.Getenv("HOME"), ".claude", "projects")
}

func dreamLockPath(project string) (string, error) {
	return statefile.Path(project, filepath.Join("dreams", "lock"))
}

// dreamLock serialises everything that writes this project's dream
// state. One systemd oneshot per project would serialise the timer on
// its own, but not a human typing `spore dream digest` while the timer
// fires, and two runs race on the watermark: the loser's advance is
// lost with nothing recorded.
//
// The lock is an flock, which the kernel drops when the holder's file
// descriptor closes for any reason including a crash. An
// exclusive-create lock file would survive a crash and wedge every
// future night.
func dreamLock(project string) (*os.File, error) {
	path, err := dreamLockPath(project)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = fh.Close()
		return nil, fmt.Errorf("%w: %s", errDreamLocked, path)
	}
	return fh, nil
}

func dreamFail(errOut io.Writer, sub string, err error) int {
	fmt.Fprintf(errOut, "%s spore dream %s: %v\n", dreamErrorToken, sub, err)
	return 1
}

func dreamBadUsage(errOut io.Writer, usage string, err error) int {
	if err != nil {
		fmt.Fprintf(errOut, "%s spore dream: %v\n", dreamErrorToken, err)
	}
	fmt.Fprintln(errOut, usage)
	return 2
}

func shortSuffix(now time.Time) string {
	sum := sha256.Sum256([]byte(now.Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:2])
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
