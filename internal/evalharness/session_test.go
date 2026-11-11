package evalharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/composer"
)

// realRulesDir points at this repo's actual rules/ directory from
// internal/evalharness's package directory. Tests read the real pool
// (not a copy) so a drift in the real spore.txt or ask-via-tool.md is
// caught here rather than silently going stale in a fixture.
const realRulesDir = "../../rules"

func nonCommentLines(t *testing.T, raw string) []string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// TestCandidateConsumerIsRealSporeConsumerPlusAskViaTool guards the
// fixture consumer-candidate.txt against drifting from the real
// rules/consumers/spore.txt: it must list exactly the same fragments,
// in the same order, plus one extra unconditional core/ask-via-tool
// line. Without this, a later edit to spore.txt (e.g. adding a
// fragment) would make the candidate arm's CLAUDE.md diverge from the
// baseline in more ways than the one thing this scenario measures.
func TestCandidateConsumerIsRealSporeConsumerPlusAskViaTool(t *testing.T) {
	real, err := os.ReadFile(filepath.Join(realRulesDir, "consumers", "spore.txt"))
	if err != nil {
		t.Fatalf("read real spore.txt: %v", err)
	}
	candidate, err := FixturesFS.ReadFile("fixtures/session/ask-decision/consumer-candidate.txt")
	if err != nil {
		t.Fatalf("read candidate consumer fixture: %v", err)
	}

	realLines := nonCommentLines(t, string(real))
	candidateLines := nonCommentLines(t, string(candidate))

	var withoutAskViaTool []string
	found := false
	for _, l := range candidateLines {
		if l == "core/ask-via-tool" {
			found = true
			continue
		}
		withoutAskViaTool = append(withoutAskViaTool, l)
	}
	if !found {
		t.Fatalf("candidate consumer fixture does not list core/ask-via-tool as an always-on line: %v", candidateLines)
	}
	if strings.Join(withoutAskViaTool, "\n") != strings.Join(realLines, "\n") {
		t.Fatalf("candidate consumer fixture has drifted from rules/consumers/spore.txt\nreal:      %v\ncandidate (minus ask-via-tool): %v", realLines, withoutAskViaTool)
	}
}

// TestComposedClaudeMDDiffersOnlyByAskViaToolFragment proves the
// baseline/candidate CLAUDE.md text this scenario spawns an agent
// against actually differs the way the scenario claims: candidate
// carries ask-via-tool.md's AskUserQuestion guidance, baseline does
// not. This is the deterministic layer -- no live agent involved.
func TestComposedClaudeMDDiffersOnlyByAskViaToolFragment(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}

	baseline, err := composer.Compose(realRulesDir, filepath.Join(realRulesDir, "consumers", "spore.txt"), composer.Options{})
	if err != nil {
		t.Fatalf("compose baseline: %v", err)
	}
	candidatePath, err := WriteCandidateConsumer(sb)
	if err != nil {
		t.Fatalf("WriteCandidateConsumer: %v", err)
	}
	candidate, err := composer.Compose(realRulesDir, candidatePath, composer.Options{})
	if err != nil {
		t.Fatalf("compose candidate: %v", err)
	}

	if strings.Contains(baseline, "AskUserQuestion") {
		t.Fatalf("baseline CLAUDE.md unexpectedly already mentions AskUserQuestion:\n%s", baseline)
	}
	if !strings.Contains(candidate, "AskUserQuestion") {
		t.Fatalf("candidate CLAUDE.md should carry ask-via-tool.md's AskUserQuestion guidance:\n%s", candidate)
	}
	if !strings.Contains(candidate, "select:AskUserQuestion") {
		t.Fatalf("candidate CLAUDE.md should carry the deferred-tool-loading step (ToolSearch select:AskUserQuestion):\n%s", candidate)
	}
}

// TestWriteComposedClaudeMDPlacesItWhereAgentCwdAutoDiscoversIt writes
// composed text into a sandbox and confirms it lands at RunWD/CLAUDE.md
// -- the same directory a spawned agent's WorkDir will be, so Claude
// Code's normal CLAUDE.md auto-discovery picks it up with no extra
// flag.
func TestWriteComposedClaudeMDPlacesItWhereAgentCwdAutoDiscoversIt(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	path, err := WriteComposedClaudeMD(sb, "# hello")
	if err != nil {
		t.Fatalf("WriteComposedClaudeMD: %v", err)
	}
	want := filepath.Join(sb.RunWD, "CLAUDE.md")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written CLAUDE.md: %v", err)
	}
	if string(b) != "# hello" {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func TestAskUserQuestionCalledDetectsAToolUseBlock(t *testing.T) {
	transcript := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"thinking out loud"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"AskUserQuestion","input":{}}]}}
{"type":"result","subtype":"success"}
`
	if !AskUserQuestionCalled(transcript) {
		t.Fatalf("expected AskUserQuestionCalled to find the tool_use block")
	}
}

func TestAskUserQuestionCalledFalseWhenAbsent(t *testing.T) {
	transcript := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"ToolSearch","input":{"query":"select:AskUserQuestion"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"no matching deferred tools found, asking in plain text instead"}]}}
`
	if AskUserQuestionCalled(transcript) {
		t.Fatalf("expected AskUserQuestionCalled to be false when no AskUserQuestion tool_use appears (a text mention of the name must not count)")
	}
}

// TestAskUserQuestionCalledToleratesATruncatedTrailingLine matters
// because a live run's process can be killed mid-stream (this
// scenario's own Stop-hook finding, see docs/todo/
// eval-harness-for-prompt-surfaces.md): the last line of captured
// stdout may be a partial JSON object. A truncated trailing line must
// not hide a real match that arrived on an earlier, complete line.
func TestAskUserQuestionCalledToleratesATruncatedTrailingLine(t *testing.T) {
	transcript := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"AskUserQuestion","input":{}}]}}
{"type":"assistant","message":{"content":[{"type":"tex`
	if !AskUserQuestionCalled(transcript) {
		t.Fatalf("a truncated trailing line should not hide an earlier real match")
	}
}
