package dream

import (
	"path/filepath"
	"strings"
	"testing"
)

// metaUserLine is a system-injected user entry: a skill body, a hook
// message, a session-start context dump. Real transcripts mark these
// with isMeta or promptSource "system"; the operator never typed them.
func metaUserLine(cwd, ts, text, promptSource string, isMeta bool) string {
	meta := ""
	if isMeta {
		meta = `"isMeta":true,`
	}
	src := ""
	if promptSource != "" {
		src = `"promptSource":"` + promptSource + `",`
	}
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts + `",` + meta + src +
		`"message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]}}`
}

func toolErrorLine(cwd, ts, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts + `",` +
		`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,` +
		`"content":"` + text + `"}]}}`
}

// markedToolErrorLine carries the top-level provenance fields the CLI
// writes alongside a failed tool call: toolUseResult is the exact string
// "User rejected tool use" when a human refused, and toolDenialKind
// names the mechanism. Both are absent on ordinary tool errors.
func markedToolErrorLine(cwd, ts, text, toolUseResult, denialKind string) string {
	extra := ""
	if toolUseResult != "" {
		extra += `"toolUseResult":"` + toolUseResult + `",`
	}
	if denialKind != "" {
		extra += `"toolDenialKind":"` + denialKind + `",`
	}
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts + `",` + extra +
		`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,` +
		`"content":"` + text + `"}]}}`
}

const realRefusal = "The user doesn't want to proceed with this tool use. " +
	"The tool use was rejected (eg. if it was a file edit, the new_string " +
	"was NOT written to the file). STOP what you are doing and wait for the " +
	"user to tell you how to proceed."

const tmuxHookBlock = "Long-running jobs must run in a tmux window, not Bash " +
	"run_in_background. Spawn one with `tmux new-window`."

const tokenMonitorFeedback = "Stop hook feedback: [spore worker token-monitor]: " +
	"WORKER TOKEN MONITOR (wrap): context 305944 tokens >= wrap cap 120000"

func digestOf(t *testing.T, lines ...string) SessionDigest {
	t.Helper()
	cwd := "/home/agent/proj/.worktrees/fix-a"
	p := writeTranscript(t, filepath.Join(t.TempDir(), "d"), "s.jsonl",
		append([]string{userLine(cwd, "2026-09-01T00:00:00Z", "# Goal")}, lines...)...)
	d, err := BuildDigest(
		Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Acceptance scenario 1. A human refusing a tool call is the rarest and
// most direct correction in a transcript; it must survive the split and
// keep moving the score.
func TestBuildDigestCountsOperatorRefusal(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	d := digestOf(t, toolErrorLine(cwd, "2026-09-01T01:00:00Z", realRefusal))

	if len(d.OperatorRefusals) != 1 {
		t.Fatalf("operator refusal not captured: %+v", d.OperatorRefusals)
	}
	if len(d.HookFeedback) != 0 {
		t.Fatalf("operator refusal misfiled as a hook block: %+v", d.HookFeedback)
	}
	ds := []SessionDigest{d, digestOf(t)}
	FlagDeepReads(ds, 0)
	if ds[0].Score <= ds[1].Score {
		t.Fatalf("refusal did not raise the score: %d vs %d",
			ds[0].Score, ds[1].Score)
	}
}

// Acceptance scenario 2. The token monitor talking to itself was 30% of
// the old denial bucket and carries no operator judgement at all.
func TestBuildDigestDoesNotCountTokenMonitorFeedbackAsRefusal(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	d := digestOf(t, metaUserLine(cwd, "2026-09-01T01:00:00Z",
		tokenMonitorFeedback, "", true))

	if len(d.OperatorRefusals) != 0 {
		t.Fatalf("hook feedback counted as an operator refusal: %+v",
			d.OperatorRefusals)
	}
	if scoreBuckets(t, d) != scoreBuckets(t, digestOf(t)) {
		t.Fatalf("hook feedback moved the score: %d vs %d",
			scoreBuckets(t, d), scoreBuckets(t, digestOf(t)))
	}
}

// scoreBuckets scores a digest with its terminal state neutralised. The
// token monitor's wrap message is the only way to know a session ran out
// of context, so it legitimately earns the end-state bonus; what must
// not move is the part of the score the slice buckets drive.
func scoreBuckets(t *testing.T, d SessionDigest) int {
	t.Helper()
	d.End = "done"
	ds := []SessionDigest{d}
	FlagDeepReads(ds, 0)
	return ds[0].Score
}

// Acceptance scenario 3. The tmux PreToolUse block arrives as a failed
// tool_result rather than as injected text, so it needs its own
// treatment even once the injected text is filtered.
func TestBuildDigestDoesNotCountTmuxHookBlockAsRefusal(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	d := digestOf(t, toolErrorLine(cwd, "2026-09-01T01:00:00Z", tmuxHookBlock))

	if len(d.OperatorRefusals) != 0 {
		t.Fatalf("tmux hook block counted as an operator refusal: %+v",
			d.OperatorRefusals)
	}
	if len(d.Failures) != 0 {
		t.Fatalf("tmux hook block leaked into failures: %+v", d.Failures)
	}
	ds := []SessionDigest{d, digestOf(t)}
	FlagDeepReads(ds, 0)
	if ds[0].Score != ds[1].Score {
		t.Fatalf("tmux hook block moved the score: %d vs %d",
			ds[0].Score, ds[1].Score)
	}
}

// Acceptance scenario 4. The whole point of the split: hook volume must
// not buy a session a deep read.
func TestScoreIgnoresHookBlockVolume(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	var noisy []string
	for i := 0; i < 50; i++ {
		noisy = append(noisy,
			toolErrorLine(cwd, "2026-09-01T01:00:00Z", tmuxHookBlock),
			metaUserLine(cwd, "2026-09-01T01:00:00Z", tokenMonitorFeedback, "", true))
	}
	loud := digestOf(t, append(noisy,
		toolErrorLine(cwd, "2026-09-01T02:00:00Z", "bash: fleebnort: not found"))...)
	quiet := digestOf(t,
		toolErrorLine(cwd, "2026-09-01T02:00:00Z", "bash: fleebnort: not found"))

	if scoreBuckets(t, loud) != scoreBuckets(t, quiet) {
		t.Fatalf("hook volume changed the score: loud=%d quiet=%d",
			scoreBuckets(t, loud), scoreBuckets(t, quiet))
	}
	if len(loud.HookFeedback) != 100 {
		t.Fatalf("hook feedback not kept for the reader: %d", len(loud.HookFeedback))
	}
}

// The CLI marks a human refusal with toolUseResult "User rejected tool
// use" and a rule-based block with toolDenialKind "permission-rule".
// Those markers are exact where the prose is not, so they decide the
// split whenever they are present.
func TestBuildDigestSplitsOnStructuredDenialMarkers(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	d := digestOf(t,
		markedToolErrorLine(cwd, "2026-09-01T01:00:00Z",
			"Tool call refused.", "User rejected tool use", "user-rejected"),
		markedToolErrorLine(cwd, "2026-09-01T01:01:00Z",
			"<tool_use_error>Blocked: sleep 45 followed by: tail -5 /tmp/x.log"+
				". To wait for a condition, use Monitor.</tool_use_error>",
			"", "permission-rule"))

	if len(d.OperatorRefusals) != 1 ||
		!strings.Contains(d.OperatorRefusals[0].Text, "refused") {
		t.Fatalf("structured user rejection not read: %+v", d.OperatorRefusals)
	}
	if len(d.HookFeedback) != 1 ||
		!strings.Contains(d.HookFeedback[0].Text, "Blocked: sleep") {
		t.Fatalf("permission-rule block not read as a hook block: %+v", d.HookFeedback)
	}
	if len(d.Failures) != 0 {
		t.Fatalf("a blocked tool call is not a failure: %+v", d.Failures)
	}
}

// FormatDigest is what the proposer reads. Hook chatter under a heading
// that reads like a human refusal would invent operator preferences that
// no operator ever stated.
func TestFormatDigestSeparatesHookBlocksFromRefusals(t *testing.T) {
	cwd := "/home/agent/proj/.worktrees/fix-a"
	d := digestOf(t,
		toolErrorLine(cwd, "2026-09-01T01:00:00Z", realRefusal),
		toolErrorLine(cwd, "2026-09-01T01:01:00Z", tmuxHookBlock),
		toolErrorLine(cwd, "2026-09-01T01:02:00Z", tmuxHookBlock))

	out := FormatDigest([]SessionDigest{d})
	if !strings.Contains(out, "### Operator refusals") {
		t.Fatalf("no operator-refusal heading:\n%s", out)
	}
	if !strings.Contains(out, "### Hook feedback (harness, not the operator)") {
		t.Fatalf("no hook-block heading:\n%s", out)
	}
	if !strings.Contains(out, "2x ") {
		t.Fatalf("repeated hook block not aggregated:\n%s", out)
	}
	const head = "### Operator refusals"
	refusals := out[strings.Index(out, head)+len(head):]
	if i := strings.Index(refusals, "###"); i >= 0 {
		refusals = refusals[:i]
	}
	if strings.Contains(refusals, "tmux window") {
		t.Fatalf("hook chatter presented as an operator refusal:\n%s", out)
	}
}

func TestBuildDigestKeepsSignalDropsNoise(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	cwd := home + "/proj/.worktrees/fix-a"

	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
			`","timestamp":"2026-09-01T01:01:00Z","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`)
	}
	lines = append(lines,
		userLine(cwd, "2026-09-01T01:02:00Z", "no, use the kernel flow instead"),
		`{"type":"user","cwd":"`+cwd+`","timestamp":"2026-09-01T01:03:00Z",`+
			`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,`+
			`"content":"bash: fleebnort: command not found"}]}}`)

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)
	s := Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}

	d, err := BuildDigest(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.OperatorMessages) != 1 ||
		!strings.Contains(d.OperatorMessages[0].Text, "kernel flow") {
		t.Fatalf("operator correction not captured: %+v", d.OperatorMessages)
	}
	if len(d.Failures) != 1 ||
		!strings.Contains(d.Failures[0].Text, "fleebnort") {
		t.Fatalf("failure not captured: %+v", d.Failures)
	}
	out := FormatDigest([]SessionDigest{d})
	if strings.Contains(out, "/x") {
		t.Fatal("successful reads leaked into the digest")
	}
}

func TestBuildDigestReadsTerminalState(t *testing.T) {
	if got := endState("BLOCKED: cannot reach the backend", false); got != "blocked" {
		t.Fatalf("blocked not detected: %q", got)
	}
	if got := endState("all green", true); got != "tokens" {
		t.Fatalf("wrap-up not detected: %q", got)
	}
	if got := endState("DONE, tests pass", false); got != "done" {
		t.Fatalf("done not detected: %q", got)
	}
	if got := endState("still thinking", false); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}

// TestBuildDigestDropsSystemInjectedText pins the shape real transcripts
// actually have: most text-bearing user entries are harness injections
// (skill bodies, task notifications, Stop-hook feedback), not the
// operator speaking. Counting them as operator messages both buries the
// real corrections and blows the digest's size budget.
func TestBuildDigestDropsSystemInjectedText(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		metaUserLine(cwd, "2026-09-01T01:00:00Z",
			"--- status: active slug: fix-a --- # Goal", "typed", false),
		metaUserLine(cwd, "2026-09-01T01:01:00Z",
			"Base directory for this skill: /home/agent/.claude/skills/foo", "", true),
		metaUserLine(cwd, "2026-09-01T01:02:00Z",
			"<task-notification><summary>Monitor event fired</summary></task-notification>",
			"system", false),
		metaUserLine(cwd, "2026-09-01T01:03:00Z",
			"Stop hook feedback: WORKER TOKEN MONITOR (wrap): context 305944 "+
				"tokens >= wrap cap 120000 on tier=unknown.", "", true),
		userLine(cwd, "2026-09-01T01:04:00Z", "actually use the kernel flow"))

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Brief, "slug: fix-a") {
		t.Fatalf("brief not captured: %q", d.Brief)
	}
	if len(d.OperatorMessages) != 1 ||
		!strings.Contains(d.OperatorMessages[0].Text, "kernel flow") {
		t.Fatalf("want only the operator's own message, got: %+v", d.OperatorMessages)
	}
	out := FormatDigest([]SessionDigest{d})
	if strings.Contains(out, "Base directory for this skill") ||
		strings.Contains(out, "Monitor event fired") {
		t.Fatalf("harness injection leaked into the digest:\n%s", out)
	}
	if d.End != "tokens" {
		t.Fatalf("real token-monitor wrap message not detected, End=%q", d.End)
	}
}

// TestBuildDigestClassifiesRejectedToolUseByAuthor pins the
// real-transcript surprise that both a refusal and a hook block arrive
// as an is_error tool_result, never as user text, so matching on user
// text alone finds neither. It used to also assert that the two land in
// the same bucket, which is the defect this file now separates.
func TestBuildDigestClassifiesRejectedToolUseByAuthor(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		toolErrorLine(cwd, "2026-09-01T01:01:00Z",
			"The user doesn't want to proceed with this tool use. "+
				"The tool use was rejected."),
		toolErrorLine(cwd, "2026-09-01T01:02:00Z",
			"Long-running jobs must run in a tmux window, not Bash run_in_background."),
		toolErrorLine(cwd, "2026-09-01T01:03:00Z", "bash: fleebnort: command not found"))

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.OperatorRefusals) != 1 ||
		!strings.Contains(d.OperatorRefusals[0].Text, "doesn't want to proceed") {
		t.Fatalf("want only the operator reject, got: %+v", d.OperatorRefusals)
	}
	if len(d.HookFeedback) != 1 ||
		!strings.Contains(d.HookFeedback[0].Text, "tmux window") {
		t.Fatalf("want the hook block on its own, got: %+v", d.HookFeedback)
	}
	if len(d.Failures) != 1 ||
		!strings.Contains(d.Failures[0].Text, "fleebnort") {
		t.Fatalf("want only the genuine failure, got: %+v", d.Failures)
	}
}

func TestBuildDigestCountsRepeatedCommands(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 4; i++ {
		lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
			`","timestamp":"2026-09-01T01:01:00Z","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","name":"Bash","input":{"command":"just check"}}]}}`)
	}
	lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
		`","timestamp":"2026-09-01T01:02:00Z","message":{"role":"assistant",`+
		`"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}`)

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)
	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RepeatedCommands) != 1 ||
		d.RepeatedCommands[0].Text != "4x just check" {
		t.Fatalf("repeated command not summarised: %+v", d.RepeatedCommands)
	}
}
