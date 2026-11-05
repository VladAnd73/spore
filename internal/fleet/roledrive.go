package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
)

// DriveRoleLoop advances the role-loop state machine for slug by one
// tick. It is idempotent and side-effecting:
//
//   - On first call, copies tasks/<slug>.md (frontmatter stripped) to
//     .spore/<slug>/spec.md.
//   - Reads the derived snapshot.
//   - Keeps the role panes the phase expects alive (engineer always,
//     plus reviewer A or B in review phases) via ensureRolePane: a
//     missing session is spawned, a session whose agent died is
//     killed and respawned, capped per role per round.
//   - Reaps reviewer A when the loop transitions into B (A's pane is
//     no longer needed once it has approved).
//   - When the snapshot is engineer-revise-*, sends a tmux send-keys
//     wake to the engineer pane pointing at the latest verdict file.
//     A delivery marker guards against re-sending the same verdict.
//   - When the snapshot is review-A / review-B and the reviewer has
//     already issued a verdict (round >= 1), sends the same style of
//     wake to the reviewer pane pointing at the latest engineer
//     response. Round 0 is skipped: a freshly spawned reviewer starts
//     from its role body.
//   - When the snapshot is PhaseDone, writes a synthesized summary at
//     .spore/<slug>/summary.md (idempotent: skip if file already
//     exists).
//   - When the snapshot is PhaseEscalated, leaves panes alive.
//   - Bounces any role file that fails validation back to its author
//     (see bounceInvalidRoleFiles). Runs after the spawn pass so the
//     owning pane is up to receive the wake.
//   - Tells the coordinator when the active role stalls (see
//     checkRoleStall).
//
// Returns the snapshot observed after side effects.
func DriveRoleLoop(projectRoot, tasksDir, slug string) (Snapshot, error) {
	// Checked before anything touches .spore/<slug>/, so a refused
	// drive on a generic task leaves no role state behind.
	if err := task.RequireRoleLoop(tasksDir, slug); err != nil {
		return Snapshot{}, fmt.Errorf("drive %s: %w", slug, err)
	}
	if err := task.CacheSpecFromTaskFile(projectRoot, tasksDir, slug); err != nil {
		return Snapshot{}, fmt.Errorf("drive %s: cache spec: %w", slug, err)
	}
	if err := task.EnsureLegacyMarker(projectRoot, slug); err != nil {
		return Snapshot{}, fmt.Errorf("drive %s: legacy marker: %w", slug, err)
	}
	// Own the worktree precondition explicitly. The role panes cwd
	// into <projectRoot>/.worktrees/<slug>/; without this call the
	// loop would rely on the homogeneous fleet's task.Ensure to add
	// the worktree, which is gated off for role-looped slugs.
	if _, err := task.EnsureWorktree(tasksDir, slug); err != nil {
		return Snapshot{}, fmt.Errorf("drive %s: ensure worktree: %w", slug, err)
	}
	snap, err := DeriveSnapshot(projectRoot, slug)
	if err != nil {
		return Snapshot{}, fmt.Errorf("drive %s: snapshot: %w", slug, err)
	}

	engineer := EngineerSpec(projectRoot, slug)
	engineer.TasksDir = tasksDir
	switch snap.Phase {
	case PhaseEngineerInitial, PhaseEngineerReviseA, PhaseEngineerReviseB:
		if err := ensureRolePane(engineer, snap.EngineerRound); err != nil {
			return snap, fmt.Errorf("drive %s: ensure engineer: %w", slug, err)
		}
		if snap.Phase != PhaseEngineerInitial {
			if err := wakeEngineer(projectRoot, slug, snap); err != nil {
				return snap, fmt.Errorf("drive %s: wake engineer: %w", slug, err)
			}
		}
	case PhaseReviewA, PhaseReviewB:
		if snap.Phase == PhaseReviewB {
			// A approved; tear down A's pane and bring B up. Engineer
			// stays alive across the handover.
			if _, err := ReapRole(ReviewerSpec(projectRoot, slug, task.ReviewerA)); err != nil {
				return snap, fmt.Errorf("drive %s: reap reviewer A: %w", slug, err)
			}
		}
		if err := ensureRolePane(engineer, snap.EngineerRound); err != nil {
			return snap, fmt.Errorf("drive %s: ensure engineer: %w", slug, err)
		}
		reviewer := ReviewerSpec(projectRoot, slug, snap.CurrentReviewer)
		reviewer.TasksDir = tasksDir
		if err := ensureRolePane(reviewer, snap.ReviewerRound); err != nil {
			return snap, fmt.Errorf("drive %s: ensure reviewer %s: %w", slug, snap.CurrentReviewer, err)
		}
		if err := wakeReviewer(projectRoot, slug, snap); err != nil {
			return snap, fmt.Errorf("drive %s: wake reviewer %s: %w", slug, snap.CurrentReviewer, err)
		}
	case PhaseDone:
		if err := writeSummary(projectRoot, slug); err != nil {
			return snap, fmt.Errorf("drive %s: write summary: %w", slug, err)
		}
		if err := clearEscalationMarkers(projectRoot, slug); err != nil {
			return snap, fmt.Errorf("drive %s: clear escalation markers: %w", slug, err)
		}
		created, err := writeReadyMarker(projectRoot, slug)
		if err != nil {
			return snap, fmt.Errorf("drive %s: write ready marker: %w", slug, err)
		}
		if created {
			notifyRoleLoop(projectRoot, fmt.Sprintf("%s: done", slug))
		}
		// Operator wants the panes torn down once the branch is
		// ready; leaving them alive only re-spends tokens.
		_, _ = ReapRole(EngineerSpec(projectRoot, slug))
		_, _ = ReapRole(ReviewerSpec(projectRoot, slug, task.ReviewerA))
		_, _ = ReapRole(ReviewerSpec(projectRoot, slug, task.ReviewerB))
	case PhaseEscalated:
		// Leave panes alive per spec: the operator inspects. Drop a
		// marker so the waybar chip can surface the escalation.
		created, err := writeEscalationMarker(projectRoot, slug, snap)
		if err != nil {
			return snap, fmt.Errorf("drive %s: write escalation marker: %w", slug, err)
		}
		if created {
			notifyRoleLoop(projectRoot, fmt.Sprintf("%s: escalated (reviewer %s, round %d)",
				slug, snap.CurrentReviewer, snap.ReviewerRound))
		}
	}

	if err := bounceInvalidRoleFiles(projectRoot, slug); err != nil {
		return snap, fmt.Errorf("drive %s: bounce invalid files: %w", slug, err)
	}
	if spec, round, expected, ok := activeRole(projectRoot, slug, snap); ok {
		if err := checkRoleStall(spec, round, expected, roleStallWindow(projectRoot)); err != nil {
			return snap, fmt.Errorf("drive %s: stall check: %w", slug, err)
		}
	}
	return snap, nil
}

// activeRole names the role that owns the next file write in snap,
// its completed-round count, and the file it is expected to write.
// ok is false in terminal phases.
func activeRole(projectRoot, slug string, snap Snapshot) (RoleSpawnSpec, int, string, bool) {
	switch snap.Phase {
	case PhaseEngineerInitial, PhaseEngineerReviseA, PhaseEngineerReviseB:
		return EngineerSpec(projectRoot, slug), snap.EngineerRound,
			task.EngineerResponsePath(projectRoot, slug, snap.EngineerRound+1), true
	case PhaseReviewA, PhaseReviewB:
		return ReviewerSpec(projectRoot, slug, snap.CurrentReviewer), snap.ReviewerRound,
			task.ReviewPath(projectRoot, slug, snap.CurrentReviewer, snap.ReviewerRound+1), true
	}
	return RoleSpawnSpec{}, 0, "", false
}

// wakeEngineer nudges the engineer pane with the path of the latest
// reviewer verdict, so the agent picks the revision round up without
// waiting on a manual prompt. Delivery and idempotency live in
// wakeSession.
func wakeEngineer(projectRoot, slug string, snap Snapshot) error {
	if snap.CurrentReviewer == "" || snap.ReviewerRound < 1 {
		return nil
	}
	verdictPath := task.ReviewPath(projectRoot, slug, snap.CurrentReviewer, snap.ReviewerRound)
	msg := fmt.Sprintf("Reviewer %s requested changes at round %d. Read %s and start the revision.",
		snap.CurrentReviewer, snap.ReviewerRound, verdictPath)
	_, err := wakeSession(EngineerSpec(projectRoot, slug), engineerWakeMarkerStem(snap), msg)
	return err
}

// wakeReviewer nudges the persistent reviewer pane with the path of
// the latest engineer response. Only fires when the reviewer has
// already issued a verdict (ReviewerRound >= 1): the phase moving back
// to review-* then means a fresh engineer response landed and the idle
// pane must be told. Round 0 is a freshly spawned reviewer that starts
// from its role body; no nudge needed.
func wakeReviewer(projectRoot, slug string, snap Snapshot) error {
	if snap.CurrentReviewer == "" || snap.ReviewerRound < 1 {
		return nil
	}
	respPath := task.EngineerResponsePath(projectRoot, slug, snap.EngineerRound)
	msg := fmt.Sprintf("Engineer responded to your round %d verdict. Read %s and review round %d.",
		snap.ReviewerRound, respPath, snap.ReviewerRound+1)
	_, err := wakeSession(ReviewerSpec(projectRoot, slug, snap.CurrentReviewer), reviewerWakeMarkerStem(snap), msg)
	return err
}

// engineerWakeMarkerStem keys the engineer wake on which verdict is
// being delivered: a new reviewer round re-arms the nudge.
func engineerWakeMarkerStem(snap Snapshot) string {
	return fmt.Sprintf("woken-%s-%d", snap.CurrentReviewer, snap.ReviewerRound)
}

// reviewerWakeMarkerStem keys the reviewer wake on which engineer
// response is being delivered: a new engineer round re-arms the nudge.
func reviewerWakeMarkerStem(snap Snapshot) string {
	return fmt.Sprintf("woken-reviewer-%s-%d", snap.CurrentReviewer, snap.EngineerRound)
}

// wakeSession delivers msg to the role pane for spec, gated by an
// idempotency marker at <roletaskdir>/state/<stem>-<created>. The
// marker name embeds the session's `#{session_created}` stamp so a
// respawn (pane crashed, then re-minted by SpawnRole on the next
// tick) clears the gate and the new pane gets nudged in turn. The
// marker is written only after sendWakeVerified confirms submission;
// on failure the next drive tick retries the whole wake.
func wakeSession(spec RoleSpawnSpec, markerStem, msg string) (wakeOutcome, error) {
	session, err := spec.SessionName()
	if err != nil {
		return wakePending, err
	}
	if !hasSession(session) {
		// Pane is not up; the spawn pass above will have failed if
		// the agent crashed. Skip the wake and let the next tick try.
		return wakeNoSession, nil
	}
	created, err := sessionCreated(session)
	if err != nil {
		// `display-message` failure means we cannot tell if this is a
		// fresh pane or the one we already nudged. Skip rather than
		// risk spamming send-keys; the next tick retries.
		return wakePending, nil
	}

	markerDir := filepath.Join(task.RoleTaskDir(spec.ProjectRoot, spec.Slug), "state")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return wakePending, err
	}
	marker := filepath.Join(markerDir, markerStem+"-"+created)
	if _, err := os.Stat(marker); err == nil {
		return wakeDelivered, nil
	}
	delivered, err := sendWakeVerified(session, msg)
	if err != nil {
		return wakePending, err
	}
	if !delivered {
		// Pane is up but its input prompt is not (agent still booting
		// after a respawn). Nothing was sent; the next tick retries.
		return wakePending, nil
	}
	if err := os.WriteFile(marker, []byte(msg+"\n"), 0o644); err != nil {
		return wakePending, err
	}
	return wakeDelivered, nil
}

// wakeOutcome is what wakeSession achieved. wakeDelivered covers a
// wake confirmed on this call or an earlier one (marker present).
type wakeOutcome int

const (
	wakePending wakeOutcome = iota
	wakeDelivered
	wakeNoSession
)

// wakePasteSettle is how long the agent TUI gets to settle the pasted
// wake text before the Enter keypress. Sent in one send-keys call, the
// trailing Enter is swallowed as part of the paste and the message
// sits unsubmitted in the input box.
const wakePasteSettle = 500 * time.Millisecond

// wakeSubmitSettle is how long to wait after Enter before capturing
// the pane to check the input box emptied.
const wakeSubmitSettle = 500 * time.Millisecond

// sendWakeVerified delivers msg to the tmux session in split calls
// (text, settle, Enter) and confirms via capture-pane that the text
// left the input region. Returns (false, nil) without sending when
// the pane shows no input prompt yet: right after a respawn the agent
// TUI has not rendered, and text sent then lands in a launch shell or
// is lost, so the caller must leave the marker unwritten and retry
// next tick. After sending, a submission that cannot be verified
// (message still pending, or the prompt vanished) retries Enter once
// and then errors, again leaving the marker unwritten.
func sendWakeVerified(session, msg string) (bool, error) {
	pane, err := capturePane(session)
	if err != nil {
		// Cannot inspect the pane (likely died since hasSession).
		// Nothing sent; skip and let the next tick retry.
		return false, nil
	}
	if !hasInputPrompt(pane) {
		return false, nil
	}
	if err := exec.Command("tmux", "send-keys", "-t", session, msg).Run(); err != nil {
		return false, fmt.Errorf("tmux send-keys text: %w", err)
	}
	time.Sleep(wakePasteSettle)
	for range 2 {
		if err := exec.Command("tmux", "send-keys", "-t", session, "Enter").Run(); err != nil {
			return false, fmt.Errorf("tmux send-keys enter: %w", err)
		}
		time.Sleep(wakeSubmitSettle)
		pane, err := capturePane(session)
		if err != nil {
			return false, fmt.Errorf("tmux capture-pane: %w", err)
		}
		if wakeSubmitted(pane, msg) {
			return true, nil
		}
	}
	return false, fmt.Errorf("wake not verifiably submitted in %s after enter retry", session)
}

func capturePane(session string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", session).Output()
	return string(out), err
}

// wakeSubmitted reports whether the capture shows msg cleared from a
// present input region. Only the last prompt region of the capture is
// scanned: a submitted message is echoed into the transcript above
// the input line and must not count as pending. Both sides are
// reduced to alphanumerics so the prompt glyph, wrapping, and
// punctuation cannot break the match. A capture with no prompt line
// is NOT submitted: post-send it means the TUI redrew into an
// unrecognisable state, and counting that as success would silently
// lose the wake.
func wakeSubmitted(pane, msg string) bool {
	region, ok := lastInputRegion(pane)
	if !ok {
		return false
	}
	frag := alnumOnly(msg)
	if len(frag) > wakePendingFragLen {
		frag = frag[:wakePendingFragLen]
	}
	if frag == "" {
		return true
	}
	return !strings.Contains(alnumOnly(region), frag)
}

// wakePendingFragLen bounds the message fragment matched against the
// input region, guarding against the region truncating a long message.
const wakePendingFragLen = 24

// inputPromptRune is the prompt character the claude-code TUI draws at
// the start of its input line. The TUI (>= 2.1.199) renders the input
// area as plain full-width U+2500 rules around a U+276F prompt line;
// there are no box-corner glyphs to key on.
const inputPromptRune = '❯'

// hasInputPrompt reports whether the capture contains a TUI input
// line. Known limitation: selection dialogs (trust prompt,
// AskUserQuestion) also render U+276F lines, so a wake fired at a
// dialog still types into it.
func hasInputPrompt(pane string) bool {
	_, ok := lastInputRegion(pane)
	return ok
}

// lastInputRegion returns the contents of the TUI input region: the
// LAST line whose trimmed content starts with the U+276F prompt
// character, plus any following lines up to the next full-width rule
// line or end of capture. Taking the last prompt line matters because
// submitted messages are echoed into the transcript above with the
// same glyph. Returns ok=false when no prompt line is present.
func lastInputRegion(pane string) (string, bool) {
	lines := strings.Split(pane, "\n")
	prompt := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), string(inputPromptRune)) {
			prompt = i
		}
	}
	if prompt == -1 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(lines[prompt])
	b.WriteString("\n")
	for _, l := range lines[prompt+1:] {
		if isRuleLine(l) {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

// isRuleLine reports whether the line is a full-width horizontal rule:
// non-empty after trimming and made of U+2500 only.
func isRuleLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, r := range line {
		if r != '─' {
			return false
		}
	}
	return true
}

// alnumOnly strips s down to its ASCII letters and digits.
func alnumOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeEscalationMarker drops an idempotent marker at
// `<roletaskdir>/state/escalated-<reviewer>-<round>` so the waybar
// chip can surface the escalation. Reports whether this call created
// the marker: the first write is the once-per-transition signal the
// coordinator notification keys on. Returns (false, nil) when the
// snapshot lacks a reviewer (defensive: PhaseEscalated always carries
// one).
func writeEscalationMarker(projectRoot, slug string, snap Snapshot) (bool, error) {
	if snap.CurrentReviewer == "" || snap.ReviewerRound < 1 {
		return false, nil
	}
	markerDir := filepath.Join(task.RoleTaskDir(projectRoot, slug), "state")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return false, err
	}
	marker := filepath.Join(markerDir, fmt.Sprintf("escalated-%s-%d", snap.CurrentReviewer, snap.ReviewerRound))
	return createMarkerExcl(marker)
}

// writeReadyMarker drops an idempotent marker at
// `<roletaskdir>/state/ready` so the waybar chip can surface
// ready-to-claim tasks once the role-loop hits PhaseDone. The marker
// is a singleton (a slug is either ready or not), cleared by
// task.Done when the operator finalises the task. Reports whether
// this call created the marker (see writeEscalationMarker).
func writeReadyMarker(projectRoot, slug string) (bool, error) {
	markerDir := filepath.Join(task.RoleTaskDir(projectRoot, slug), "state")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return false, err
	}
	marker := filepath.Join(markerDir, "ready")
	return createMarkerExcl(marker)
}

// createMarkerExcl creates the marker file with O_EXCL so first
// creation is atomic: concurrent drives (path-watcher Reconcile plus
// the replenish hook or the role-drive CLI) cannot both observe
// "missing" and both claim the once-per-transition notify.
func createMarkerExcl(marker string) (bool, error) {
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, f.Close()
}

// notifyRoleLoop drops a tell envelope into the coordinator inbox for
// this project so the coordinator pane wakes on terminal role-loop
// transitions instead of polling. Best-effort by design: a failure
// logs to stderr and never fails the drive. Uses the explicit
// project-path variant, not NotifyCoordinatorEnv: the envelope must
// land even when the coordinator's own replenish hook drives the
// transition (the marker guard upstream already prevents repeats).
func notifyRoleLoop(projectRoot, body string) {
	project := filepath.Base(projectRoot)
	if err := hooks.NotifyCoordinatorEvent(project, "role-loop", body); err != nil {
		fmt.Fprintf(os.Stderr, "role-loop notify coordinator: %v\n", err)
	}
}

// clearEscalationMarkers removes any `escalated-*` files under the
// task's state dir. Called on PhaseDone so an operator force-approve
// after an escalation clears the chip. A missing state dir is fine.
func clearEscalationMarkers(projectRoot, slug string) error {
	dir := filepath.Join(task.RoleTaskDir(projectRoot, slug), "state")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "escalated-") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// sessionCreated returns the `#{session_created}` stamp tmux assigns
// the named session at spawn (a unix timestamp). Used by the engineer
// wake marker to detect a respawn: a fresh pane gets a different
// stamp, so the marker name changes and the next drive tick re-fires
// the send-keys nudge.
func sessionCreated(name string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", name, "#{session_created}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// summaryFingerprintPrefix is the leading marker the synthesised
// summary embeds (after the date line). Encodes the counts of inputs
// the summary was built from, so a later drive tick can tell whether
// the on-disk file is stale relative to the current artifact tree.
const summaryFingerprintPrefix = "<!-- summary-fingerprint:"

// writeSummary synthesises .spore/<slug>/summary.md from the on-disk
// review threads. Idempotent across drive ticks with stable inputs,
// but regenerates when the embedded fingerprint disagrees with the
// current artifact counts: a leftover summary from a previous task
// lifecycle (different engineer/reviewer round counts) cannot shadow
// a fresh run, and a hand-edited summary that strips the fingerprint
// is treated as stale (the marker fence is rebuilt).
func writeSummary(projectRoot, slug string) error {
	path := filepath.Join(task.RoleTaskDir(projectRoot, slug), "summary.md")
	fp, err := computeSummaryFingerprint(projectRoot, slug)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if extractSummaryFingerprint(existing) == fp {
			return nil
		}
	}
	body, err := buildSummary(projectRoot, slug, fp)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// computeSummaryFingerprint returns the artifact-count signature the
// next writeSummary will compare against the file's embedded marker.
// Shape: "engineer=<E> A=<NA> B=<NB>".
func computeSummaryFingerprint(projectRoot, slug string) (string, error) {
	eng, err := task.ListEngineerRounds(projectRoot, slug)
	if err != nil {
		return "", err
	}
	a, err := task.ListReviewRounds(projectRoot, slug, task.ReviewerA)
	if err != nil {
		return "", err
	}
	b, err := task.ListReviewRounds(projectRoot, slug, task.ReviewerB)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("engineer=%d A=%d B=%d", len(eng), len(a), len(b)), nil
}

// extractSummaryFingerprint reads the embedded marker out of an
// existing summary.md body. Returns "" when the marker is missing or
// malformed; the caller treats that as "regenerate".
func extractSummaryFingerprint(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, summaryFingerprintPrefix) {
			continue
		}
		line = strings.TrimPrefix(line, summaryFingerprintPrefix)
		line = strings.TrimSuffix(line, "-->")
		return strings.TrimSpace(line)
	}
	return ""
}

func buildSummary(projectRoot, slug, fingerprint string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: review summary\n\n", slug)
	fmt.Fprintf(&b, "Generated %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "%s %s -->\n\n", summaryFingerprintPrefix, fingerprint)

	for _, instance := range []task.ReviewerInstance{task.ReviewerA, task.ReviewerB} {
		rounds, err := task.ListReviewRounds(projectRoot, slug, instance)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "## reviewer %s\n\n", instance)
		if len(rounds) == 0 {
			b.WriteString("No rounds recorded.\n\n")
			continue
		}
		for _, r := range rounds {
			rev, err := task.ReadReview(projectRoot, slug, instance, r)
			if errors.Is(err, task.ErrInvalidRoleFile) {
				continue
			}
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "### round %d -> %s\n\n", r, rev.Verdict)
			if rev.Summary != "" {
				fmt.Fprintf(&b, "%s\n\n", rev.Summary)
			}
			if len(rev.Comments) > 0 {
				for _, c := range rev.Comments {
					fmt.Fprintf(&b, "- %s\n", c)
				}
				b.WriteString("\n")
			}
		}
	}

	engRounds, err := task.ListEngineerRounds(projectRoot, slug)
	if err != nil {
		return "", err
	}
	if len(engRounds) > 0 {
		b.WriteString("## engineer responses\n\n")
		for _, r := range engRounds {
			resp, err := task.ReadEngineerResponse(projectRoot, slug, r)
			if errors.Is(err, task.ErrInvalidRoleFile) {
				continue
			}
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "### round %d\n\n", r)
			if len(resp.Addressed) > 0 {
				b.WriteString("Addressed:\n")
				for _, c := range resp.Addressed {
					fmt.Fprintf(&b, "- %s\n", c)
				}
				b.WriteString("\n")
			}
			if len(resp.Pushback) > 0 {
				b.WriteString("Pushback:\n")
				for _, c := range resp.Pushback {
					fmt.Fprintf(&b, "- %s\n", c)
				}
				b.WriteString("\n")
			}
			if resp.Notes != "" {
				fmt.Fprintf(&b, "Notes: %s\n\n", resp.Notes)
			}
			if len(resp.Followups) > 0 {
				b.WriteString("Proposed follow-ups:\n")
				for _, c := range resp.Followups {
					fmt.Fprintf(&b, "- %s\n", c)
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String(), nil
}
