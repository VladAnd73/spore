package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Slice struct {
	Kind string
	TS   time.Time
	Text string
}

type SessionDigest struct {
	Session          Session
	Brief            string
	OperatorMessages []Slice
	Failures         []Slice
	RepeatedCommands []Slice
	OperatorRefusals []Slice
	HookFeedback     []Slice
	FinalReport      string
	End              string
	Entries          int
	Truncated        bool
	Score            int
	DeepRead         bool
}

// digestEntry carries the provenance fields rawEntry omits. Most
// text-bearing user entries in a real transcript are harness injections
// (skill bodies, task notifications, Stop-hook feedback) rather than the
// operator speaking, and only isMeta and promptSource tell them apart.
type digestEntry struct {
	Type           string          `json:"type"`
	Timestamp      string          `json:"timestamp"`
	IsMeta         bool            `json:"isMeta"`
	PromptSource   string          `json:"promptSource"`
	ToolUseResult  json.RawMessage `json:"toolUseResult"`
	ToolDenialKind string          `json:"toolDenialKind"`
	Message        json.RawMessage `json:"message"`
}

// BuildDigest keeps only the slices that carry a lesson: what the
// operator said, what failed, what was retried, what the operator
// refused, and, separately and unscored, what the harness blocked. The
// bulk of a transcript is successful tool calls and reasoning, which is
// dropped so a night's worth of sessions fits in a model's context.
func BuildDigest(s Session, repeatThreshold int) (SessionDigest, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return SessionDigest{}, err
	}
	defer f.Close()

	d := SessionDigest{Session: s, End: "unknown"}
	cmdCounts := map[string]int{}
	var lastAssistant string
	sawWrapUp := false

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		// Counted before the parse, so the total matches what a shell
		// counts. It is the denominator of the proposer's coverage claim,
		// and the proposer counts lines with wc, not with a JSON parser.
		d.Entries++
		var e digestEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, e.Timestamp)
		switch e.Type {
		case "user":
			if errText, isErr := toolError(e.Message); isErr {
				switch {
				case operatorRefused(e, errText):
					d.OperatorRefusals = append(d.OperatorRefusals,
						Slice{Kind: "refusal", TS: ts, Text: errText})
				case hookStopped(e, errText):
					d.HookFeedback = append(d.HookFeedback,
						Slice{Kind: "hook", TS: ts, Text: errText})
				default:
					d.Failures = append(d.Failures,
						Slice{Kind: "tool-error", TS: ts, Text: errText})
				}
				continue
			}
			text := messageText(e.Message)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if wrapUp(text) {
				sawWrapUp = true
			}
			// The operator filter runs before any classification: a hook
			// speaking through an injected user entry is marked as such,
			// and reading its prose first is what put 630 of 733 harness
			// messages in the operator-correction bucket.
			if !operatorAuthored(e) {
				if hookStopped(e, text) {
					d.HookFeedback = append(d.HookFeedback,
						Slice{Kind: "hook", TS: ts, Text: text})
				}
				continue
			}
			if d.Brief == "" {
				d.Brief = text
				continue
			}
			d.OperatorMessages = append(d.OperatorMessages,
				Slice{Kind: "operator", TS: ts, Text: text})
		case "assistant":
			if cmd, ok := bashCommand(e.Message); ok {
				cmdCounts[cmd]++
			}
			if t := messageText(e.Message); strings.TrimSpace(t) != "" {
				lastAssistant = t
			}
		}
	}
	// A single JSONL line over the scanner's cap stops the scan where it
	// stands. Recording it is what stops a partial session from being
	// described as a whole one downstream.
	d.Truncated = sc.Err() != nil
	d.FinalReport = lastAssistant
	d.End = endState(lastAssistant, sawWrapUp)
	for cmd, n := range cmdCounts {
		if n >= repeatThreshold {
			d.RepeatedCommands = append(d.RepeatedCommands,
				Slice{Kind: "repeated", Text: fmt.Sprintf("%dx %s", n, cmd)})
		}
	}
	sort.Slice(d.RepeatedCommands, func(i, j int) bool {
		return d.RepeatedCommands[i].Text < d.RepeatedCommands[j].Text
	})
	return d, nil
}

// operatorAuthored reports whether a human typed the entry. Harness
// injections are marked isMeta, or carry promptSource "system"; an entry
// with neither marker is the operator's own prompt.
func operatorAuthored(e digestEntry) bool {
	return !e.IsMeta && e.PromptSource != "system"
}

// endState reads how the session finished. It feeds scoring: a session
// that ended blocked or ran out of context is far likelier to hold a
// lesson than one that simply finished.
func endState(finalReport string, sawWrapUp bool) string {
	switch {
	case strings.Contains(finalReport, "BLOCKED"):
		return "blocked"
	case sawWrapUp:
		return "tokens"
	case strings.Contains(finalReport, "DONE"):
		return "done"
	}
	return "unknown"
}

// wrapUp matches what the token monitors actually emit. Both the worker
// and coordinator messages carry "TOKEN MONITOR"; the other two spellings
// cover older reminder wording.
func wrapUp(text string) bool {
	return strings.Contains(text, "TOKEN MONITOR") ||
		strings.Contains(text, "wrap-up") ||
		strings.Contains(text, "token threshold")
}

func toolError(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Content any    `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_result" && c.IsError {
			return fmt.Sprint(c.Content), true
		}
	}
	return "", false
}

func bashCommand(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_use" && c.Name == "Bash" && c.Input.Command != "" {
			return c.Input.Command, true
		}
	}
	return "", false
}

// The CLI names how a stopped tool call was stopped in two top-level
// fields. toolDenialKind is exact but recent: only about a third of the
// stopped calls in the corpus carry it, and none of the older ones, so
// the prose below stays as the fallback.
const (
	denialKindUser = "user-rejected"
	denialKindRule = "permission-rule"
	userRejected   = "User rejected tool use"
)

// operatorRefused reports whether a human stopped this tool call. It is
// applied only to failed tool_results, because that is the only way a
// real refusal arrives: the same prose in a user message is someone
// quoting it, which is all three "permission to use" matches in the
// corpus turned out to be.
func operatorRefused(e digestEntry, text string) bool {
	if e.ToolDenialKind != "" {
		return e.ToolDenialKind == denialKindUser
	}
	return toolUseResultText(e.ToolUseResult) == userRejected ||
		strings.Contains(text, "doesn't want to proceed with this tool use")
}

// hookStopped reports whether the harness stopped the call or was simply
// talking to itself. Three shapes, all measured in the corpus: a
// permission rule denying a tool, the tmux PreToolUse block, and the
// built-in sleep guard, which frames its refusal as a tool_use_error.
func hookStopped(e digestEntry, text string) bool {
	if e.ToolDenialKind == denialKindRule {
		return true
	}
	return strings.Contains(text, "must run in a tmux window") ||
		strings.Contains(text, "<tool_use_error>Blocked: ") ||
		strings.Contains(text, "blocked by hook") ||
		strings.Contains(text, "hook feedback")
}

func toolUseResultText(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// FormatDigest renders the digests as the markdown the proposer reads.
func FormatDigest(ds []SessionDigest) string {
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "## %s / %s (%s)\n\n", d.Session.Project,
			d.Session.Slug, d.Session.Kind)
		fmt.Fprintf(&b, "session: %s\nended: %s\nentries: %d\ndeep-read: %v\n",
			filepath.Base(d.Session.Path), d.End, d.Entries, d.DeepRead)
		if d.Truncated {
			b.WriteString("incomplete: one entry was too large to read, " +
				"so the scan stopped before the end of this session\n")
		}
		b.WriteString("\n")
		writeAssignment(&b, d.Brief)
		writeSlices(&b, "Operator messages", d.OperatorMessages)
		writeSlices(&b, "Failures", d.Failures)
		writeSlices(&b, "Repeated commands", d.RepeatedCommands)
		writeSlices(&b, "Operator refusals", d.OperatorRefusals)
		writeHookFeedback(&b, "Hook feedback (harness, not the operator)",
			d.HookFeedback)
	}
	return b.String()
}

// briefLimit is what one session's opening assignment may spend. The
// text is a whole task file for a worker and the whole shared role file
// for a coordinator, and only its opening says what the session was for.
const briefLimit = 400

// writeAssignment prints what the session was told to do. The proposer
// needs it to tell a session that discussed a lesson from one that
// demonstrated it, and the heading has to say the text is not evidence,
// because a task brief argues its own case better than any finding in
// the file. The agent's closing report is deliberately not printed for
// the same reason, and End already carries the only checkable fact in it.
func writeAssignment(b *strings.Builder, brief string) {
	if strings.TrimSpace(brief) == "" {
		return
	}
	b.WriteString("### Opening assignment: what this session was asked to do, not evidence\n\n")
	fmt.Fprintf(b, "%s\n\n", truncate(strings.ReplaceAll(brief, "\n", " "), briefLimit))
}

// hookShapeLimit caps what the hook bucket may spend of the reader's
// context. It is scored zero, and rendering it in full cost 320 KB of a
// 619 KB digest across 388 sessions: half the budget to say the same
// three hooks fired again.
const hookShapeLimit = 3

// writeHookFeedback reports how often each hook fired rather than what
// it said each time. The bucket is kept because a session blocked forty
// times by one hook says something about the harness, but it says it
// once.
func writeHookFeedback(b *strings.Builder, title string, ss []Slice) {
	if len(ss) == 0 {
		return
	}
	counts := map[string]int{}
	var order []string
	for _, s := range ss {
		k := hookShape(s.Text)
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	sort.Slice(order, func(i, j int) bool {
		if counts[order[i]] != counts[order[j]] {
			return counts[order[i]] > counts[order[j]]
		}
		return order[i] < order[j]
	})
	fmt.Fprintf(b, "### %s\n\n", title)
	fmt.Fprintf(b, "- %d entries, %d distinct\n", len(ss), len(order))
	for i, k := range order {
		if i >= hookShapeLimit {
			break
		}
		fmt.Fprintf(b, "- %dx %s\n", counts[k], k)
	}
	b.WriteString("\n")
}

// hookShape collapses a hook message to the part that repeats. The Stop
// hook embeds a live token count, and in the coordinator's case a whole
// worker report, in every message, so two firings of the same hook share
// no text until the digits are normalised and the tail is cut.
func hookShape(text string) string {
	var b strings.Builder
	prevDigit := false
	for _, r := range strings.Join(strings.Fields(text), " ") {
		if r >= '0' && r <= '9' {
			if !prevDigit {
				b.WriteByte('#')
			}
			prevDigit = true
			continue
		}
		prevDigit = false
		b.WriteRune(r)
	}
	return truncate(b.String(), 120)
}

func writeSlices(b *strings.Builder, title string, ss []Slice) {
	if len(ss) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	for _, s := range ss {
		fmt.Fprintf(b, "- [%s] %s\n", s.TS.Format(time.RFC3339), oneLine(s.Text))
	}
	b.WriteString("\n")
}

func oneLine(s string) string {
	return truncate(strings.ReplaceAll(s, "\n", " "), 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + " ..."
}
