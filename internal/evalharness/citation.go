// citation.go proves, at Go level, the mechanism a verifier stage (a
// spore task worker checking another agent's report) depends on:
// handed a claim plus a file:line pointer, can a spawned agent
// independently re-open that pointer and tell a real citation from a
// fabricated one?
//
// This deliberately does not reuse BuildReviewerRun: that function is
// tightly coupled to the dream pipeline's packet.json shape and
// dream.ReviewerBrief text, which has nothing to do with a citation
// check. It reuses only the genuinely generic infra from
// sandbox.go/session.go (Sandbox, copyFixtureRepo, AgentSpawner) and
// defines its own scenario/fixture shape.
package evalharness

import (
	"bufio"
	"fmt"
	"strings"
)

// citationCheckBrief is the whole prompt text a verifier-stage worker
// would receive: re-derive the cited line yourself, then answer with
// one of two exact verdict lines so grading can be exact-match rather
// than a fuzzy LLM-judge call.
const citationCheckBrief = `You are a verifier. You will be given one claim and a file:line
citation that is supposed to back it up.

Open the cited file at the cited line yourself and read what is
actually there. Do not trust the claim's wording; check the real code.

If the cited line genuinely supports the claim, end your final message
with exactly this line and nothing else on it:

VERDICT: CONFIRMED

If the cited line does not support the claim - it says something
else, or the file/line does not exist - end your final message with
exactly this line and nothing else on it:

VERDICT: REJECTED
`

// citationFixtureDir returns the embedded fixture directory for a
// named citation-check scenario (e.g. "fabricated-citation" ->
// fixtures/citation/fabricated-citation), which holds report.md and a
// repo/ subtree the report's citation resolves against.
func citationFixtureDir(name string) string {
	return "fixtures/citation/" + name
}

// BuildCitationCheckRun copies the fixture's repo/ subtree into
// sb.RunWD (so re-deriving the citation has real content to check
// against, never the real spore checkout) and builds the prompt a
// spawned verifier should receive: the citation-check brief followed
// by the fixture's report.md text, containing the claim and its
// file:line pointer. It returns the prompt and the working directory a
// spawned agent should run in.
func BuildCitationCheckRun(sb *Sandbox, fixture string) (prompt, runWD string, err error) {
	dir := citationFixtureDir(fixture)

	if err := copyFixtureRepo(dir+"/repo", sb.RunWD); err != nil {
		return "", "", fmt.Errorf("evalharness: citation fixture %q: %w", fixture, err)
	}

	report, err := FixturesFS.ReadFile(dir + "/report.md")
	if err != nil {
		return "", "", fmt.Errorf("evalharness: citation fixture %q: %w", fixture, err)
	}

	var b strings.Builder
	b.WriteString(citationCheckBrief)
	b.WriteString("\n---\n\n")
	b.Write(report)
	b.WriteString("\n")
	return b.String(), sb.RunWD, nil
}

// CitationConfirmed scans a spawned agent's output for the exact
// "VERDICT: CONFIRMED" line citationCheckBrief demands, the way
// AskUserQuestionCalled in session.go scans for a specific tool_use
// block. Matching on the whole trimmed line rather than a substring
// means a stray mention of the word "CONFIRMED" inside the agent's
// reasoning can never be mistaken for its actual verdict.
func CitationConfirmed(stdout string) bool {
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "VERDICT: CONFIRMED" {
			return true
		}
	}
	return false
}
