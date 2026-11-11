package dream

import (
	"strings"
	"testing"
)

// requirePhrases matches against the brief with runs of whitespace
// collapsed, so a phrase still counts when a reflow wraps it across two
// lines. The test is meant to catch a rule being deleted, not a line
// being rewrapped.
func requirePhrases(t *testing.T, name, brief string, want []string) {
	t.Helper()
	flat := strings.Join(strings.Fields(brief), " ")
	for _, w := range want {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("%s brief is missing %q", name, w)
		}
	}
}

func TestBriefsCarryTheLoadBearingRules(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"You may not write anything",
		"evidence packet",
		"operator-preference",
		"known-claims.md",
		"An empty night is a result",
	})
	requirePhrases(t, "reviewer", ReviewerBrief, []string{
		"Re-derive, never trust",
		"Documentation over recall",
		"Default to reject",
		"stale",
		"You may not repair the packet",
	})
}

// Both briefs ship inside the binary to every consumer of this harness,
// so a phrase that fits only the machine they were written on is a leak.
// "spore" itself is not banned: it is the CLI's own subcommand
// vocabulary ("spore dream gate", "spore task done"), which every
// consumer project's worker runs regardless of what that project is
// called, not a leak of this kernel repo's identity.
func TestBriefsNameNoProjectAndNoHost(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		for _, banned := range []string{
			"nixos", "/home/", "@",
		} {
			if strings.Contains(strings.ToLower(brief), banned) {
				t.Errorf("%s brief leaks %q; kernel assets stay generic", name, banned)
			}
		}
	}
}

func TestBriefsAreAsciiOnly(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		for i, r := range brief {
			if r > 127 {
				t.Errorf("%s brief has non-ASCII rune %q at byte %d", name, r, i)
			}
		}
	}
}

// A median transcript does not fit in the reading agent's context, so the
// brief has to bound the read and forbid the claim of a full one.
func TestProposerBriefBoundsTheDeepRead(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"Never read a transcript end to end",
		"anchor",
		"truncates every entry",
		"Never write that you read a session in full",
		"coverage",
	})
}

// The ledger fingerprints a claim by its exact wording, so a reworded
// repeat never reaches the two-session bar. Constraining the wording is
// half the fix; reusing a wording already on file is the other half.
func TestProposerBriefRequiresCanonicalFormAndReuse(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"canonical form",
		"copy that claim line word for word",
		"one sentence, present tense",
		"use its type unchanged",
	})
}

// Gate waves an operator preference through on first sighting and nothing
// in Go checks that the operator ever said it.
func TestProposerBriefGuardsTheOperatorPreferenceType(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"verbatim",
		"Operator messages",
		"is never this type",
	})
}

// The corpus contains this feature's own design sessions, so prose about a
// lesson sits next to the lesson itself.
func TestBothBriefsSeparateDiscussedFromDemonstrated(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		requirePhrases(t, name, brief, []string{"DISCUSSED", "DEMONSTRATED"})
	}
}

func TestReviewerBriefConfirmedRequiresProof(t *testing.T) {
	requirePhrases(t, "reviewer", ReviewerBrief, []string{
		"`confirmed` requires a non-empty `proof`",
	})
}

// The proposer brief used to end at "write no packet and say so": stages
// 4 and 5 did not exist yet, so nothing told the same worker session to
// gate, spawn a reviewer, write survivors, or ever finish its own task.
func TestProposerBriefOrchestratesGateReviewAndWrite(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"spore dream gate",
		"spore dream reviewer-brief",
		"spore dream write",
		"spore task tell coordinator",
		"spore task done",
	})
}

// The reviewer must be handed exactly the brief, one packet, and its
// target: nothing that would let it see the proposer's own reasoning.
func TestProposerBriefIsolatesTheReviewerSubagent(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"a context that cannot see anything about this session",
		"exactly three things: the",
	})
}

// sessions is what Gate counts independent sightings against; without
// this field in the schema nothing tells the proposer how a claim
// clears the two-tier bar in one run instead of waiting for a second
// night.
func TestProposerBriefDocumentsTheSessionsField(t *testing.T) {
	requirePhrases(t, "proposer", ProposerBrief, []string{
		"\"sessions\"",
		"independent sightings",
	})
}
