// session.go builds and grades phase 2's one proven scenario: does
// wiring rules/core/ask-via-tool.md into a composed CLAUDE.md as an
// always-on fragment change whether a real agent, facing a genuine
// 2-4-option decision, calls AskUserQuestion? See
// docs/todo/eval-harness-for-prompt-surfaces.md for the fixture, the
// measured result, and why this is deliberately not a general
// session-simulator framework: it answers one question, not many.
package evalharness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AskViaToolFixtureTaskPath is the fixture's worker-shaped task body: a
// small, genuine 2-4-option decision (three .NET logging libraries)
// with no operator present to answer, so a real agent's only choices
// are to call AskUserQuestion, ask in plain text, or decide
// unilaterally. It is the same shape MintTask hands a real worker: a
// task file's body becomes the agent's first message.
const AskViaToolFixtureTaskPath = "fixtures/session/ask-decision/task.md"

// AskViaToolCandidateConsumerPath is a throwaway consumer list: the
// real rules/consumers/spore.txt fragments (kept in sync by
// TestCandidateConsumerIsRealSporeConsumerPlusAskViaTool) plus one
// added always-on line, core/ask-via-tool. It never touches the real
// spore.txt; WriteCandidateConsumer materializes it under a sandbox so
// composer.Compose can read it like any other consumer file.
const AskViaToolCandidateConsumerPath = "fixtures/session/ask-decision/consumer-candidate.txt"

// SessionSpawnArgs are the extra claude CLI flags a full-session
// scenario needs beyond ClaudeSpawner's fixed base args: --output-format
// stream-json --verbose, so the transcript can be graded for a specific
// tool call, not just read as one text blob. --setting-sources project
// (this host's fix for "The Stop-hook hang" in
// docs/todo/eval-harness-for-prompt-surfaces.md) is no longer listed
// here because ClaudeSpawner.Spawn now includes it in every spawn's
// base args, not just a full-session one - a dream (proposer/reviewer)
// scenario needs it exactly as much and was found live to hang without
// it too.
var SessionSpawnArgs = []string{"--output-format", "stream-json", "--verbose"}

// LoadAskViaToolFixtureTask returns the fixture task's body text.
func LoadAskViaToolFixtureTask() (string, error) {
	b, err := FixturesFS.ReadFile(AskViaToolFixtureTaskPath)
	if err != nil {
		return "", fmt.Errorf("evalharness: load ask-via-tool fixture task: %w", err)
	}
	return string(b), nil
}

// WriteCandidateConsumer materializes AskViaToolCandidateConsumerPath's
// embedded content to a real file under sb.Root, since
// composer.Compose reads a consumer from disk, not from a string.
func WriteCandidateConsumer(sb *Sandbox) (string, error) {
	raw, err := FixturesFS.ReadFile(AskViaToolCandidateConsumerPath)
	if err != nil {
		return "", fmt.Errorf("evalharness: load candidate consumer fixture: %w", err)
	}
	path := filepath.Join(sb.Root, "candidate-consumer.txt")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", fmt.Errorf("evalharness: write candidate consumer: %w", err)
	}
	return path, nil
}

// WriteComposedClaudeMD writes content as CLAUDE.md into sb.RunWD, the
// directory a spawned agent's WorkDir will be. Claude Code's normal
// CLAUDE.md auto-discovery then picks it up with no extra flag, the
// same way it would for a real worker's composed root instruction
// file.
func WriteComposedClaudeMD(sb *Sandbox, content string) (string, error) {
	path := filepath.Join(sb.RunWD, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("evalharness: write CLAUDE.md: %w", err)
	}
	return path, nil
}

// transcriptLine is the sliver of a claude --output-format stream-json
// line this package grades on: an assistant message's content blocks,
// narrowed to the one field a tool_use block carries that matters here.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// AskUserQuestionCalled scans a claude --output-format stream-json
// transcript (one JSON object per line, as ClaudeSpawner's
// SpawnResult.Stdout carries it) for an assistant tool_use block
// invoking AskUserQuestion by name. A line that fails to parse as JSON
// is skipped rather than treated as an error: a killed or
// still-finishing process's last captured line is often a partial
// write, and a truncated tail must not hide a real match an earlier,
// complete line already carried.
func AskUserQuestionCalled(stdout string) bool {
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.Type != "assistant" {
			continue
		}
		for _, block := range line.Message.Content {
			if block.Type == "tool_use" && block.Name == "AskUserQuestion" {
				return true
			}
		}
	}
	return false
}
