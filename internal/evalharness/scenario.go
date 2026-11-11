package evalharness

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Kind names which dream brief a scenario exercises.
type Kind string

const (
	KindProposer Kind = "proposer"
	KindReviewer Kind = "reviewer"
)

// Scenario is one named, registered eval case: a fixture to run
// through one dream brief, and the code-graded, exact-match verdict a
// correct run must produce.
type Scenario struct {
	Name        string
	Kind        Kind
	Fixture     string
	Description string

	proposerWant ProposerExpectation
	reviewerWant ReviewerExpectation
}

// Scenarios is every scenario phase 1 of the eval harness covers: the
// dream pipeline's proposer and reviewer briefs. See
// docs/todo/eval-harness-for-prompt-surfaces.md for why each fixture
// exists and what real incident or brief rule it traces to.
var Scenarios = []Scenario{
	{
		Name:         "proposer-demonstrated",
		Kind:         KindProposer,
		Fixture:      "demonstrated",
		Description:  "A session where a tool actually errored. The proposer should propose the claim with DEMONSTRATED evidence.",
		proposerWant: ExpectDemonstrated,
	},
	{
		Name:         "proposer-discussed-not-demonstrated",
		Kind:         KindProposer,
		Fixture:      "discussed-not-demonstrated",
		Description:  "A session where a lesson is only described (a plan, a design note), never executed. The proposer must not cite it as DEMONSTRATED.",
		proposerWant: ExpectNotDemonstrated,
	},
	{
		Name:         "proposer-empty-night",
		Kind:         KindProposer,
		Fixture:      "empty-night",
		Description:  "A session with nothing noteworthy in it. The proposer should write no packet at all.",
		proposerWant: ExpectNoPackets,
	},
	{
		Name:         "proposer-retrospective-writeup",
		Kind:         KindProposer,
		Fixture:      "retrospective-writeup",
		Description:  "A session asked to write up a known past outage for a postmortem. The assistant's own retrospective account reads exactly like a DEMONSTRATED failure (same file, same line, past-tense panic language) but no Failures entry backs it anywhere in the digest or transcript, and the session's own opening assignment already names it as a writeup. The proposer must not cite it as DEMONSTRATED.",
		proposerWant: ExpectNotDemonstrated,
	},
	{
		Name:         "reviewer-fabricated-citation",
		Kind:         KindReviewer,
		Fixture:      "fabricated-citation",
		Description:  "A packet whose evidence quotes something the cited file does not actually say. The reviewer must refuse.",
		reviewerWant: ExpectRefuse,
	},
	{
		Name:         "reviewer-real-evidence",
		Kind:         KindReviewer,
		Fixture:      "real-evidence",
		Description:  "A packet whose evidence pointer genuinely holds up when re-derived. The reviewer should approve.",
		reviewerWant: ExpectApprove,
	},
}

// Find looks up a registered scenario by name.
func Find(name string) (Scenario, bool) {
	for _, sc := range Scenarios {
		if sc.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}

// RunOptions configures one scenario run.
type RunOptions struct {
	// Spawner starts the agent. Nil means ClaudeSpawner{}, a real
	// `claude -p` process; tests inject a fake to avoid the cost and
	// nondeterminism of a live call.
	Spawner AgentSpawner
	// SandboxParent is the directory a fresh sandbox is created under.
	// Empty means os.TempDir().
	SandboxParent string
	// Now fixes the run's clock, mainly so proposer fixtures produce a
	// stable digest window in tests. Empty means time.Now().UTC().
	Now time.Time
	// RunID names the minted dream run for proposer scenarios. Empty
	// derives one from Now.
	RunID string
	// ProposerBriefOverride and ReviewerBriefOverride, when non-empty,
	// replace the embedded brief a scenario of the matching Kind sends
	// to the agent - the baseline-vs-candidate comparison mechanism a
	// later task uses to prove a brief change is a real improvement.
	ProposerBriefOverride string
	ReviewerBriefOverride string
}

// Outcome is what one scenario run produced.
type Outcome struct {
	Scenario    string
	Pass        bool
	Reason      string
	SandboxRoot string
	GradeDir    string
	RawOutput   string
	ExitCode    int
}

// RunScenario builds a fresh Sandbox, spawns a real (or, in a test,
// fake) agent against the scenario's fixture, and grades the result.
// It never touches anything outside the sandbox it creates: dream's
// ledger and watermark are redirected into the sandbox's own state
// directory (see BuildProposerRun), the sandbox is its own git repo
// with no remote, and the spawned process's environment has GitHub
// push tokens stripped (see Sandbox.Env).
func RunScenario(ctx context.Context, sc Scenario, opts RunOptions) (Outcome, error) {
	parent := opts.SandboxParent
	if parent == "" {
		parent = os.TempDir()
	}
	sb, err := NewSandbox(parent)
	if err != nil {
		return Outcome{Scenario: sc.Name}, err
	}
	out := Outcome{Scenario: sc.Name, SandboxRoot: sb.Root}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	runID := opts.RunID
	if runID == "" {
		runID = now.Format("20060102-150405")
	}

	var prompt, gradeDir string
	switch sc.Kind {
	case KindProposer:
		prompt, gradeDir, err = BuildProposerRun(sb, sc.Fixture, runID, now, opts.ProposerBriefOverride)
	case KindReviewer:
		prompt, gradeDir, err = BuildReviewerRun(sb, sc.Fixture, opts.ReviewerBriefOverride)
	default:
		err = fmt.Errorf("evalharness: scenario %q: unknown kind %q", sc.Name, sc.Kind)
	}
	if err != nil {
		return out, err
	}
	out.GradeDir = gradeDir

	spawner := opts.Spawner
	if spawner == nil {
		spawner = ClaudeSpawner{}
	}
	res, err := spawner.Spawn(ctx, SpawnRequest{Prompt: prompt, WorkDir: sb.RunWD, Env: sb.Env()})
	out.RawOutput, out.ExitCode = res.Stdout, res.ExitCode
	if err != nil {
		out.Reason = fmt.Sprintf("agent spawn failed: %v", err)
		return out, nil
	}

	switch sc.Kind {
	case KindProposer:
		out.Pass, out.Reason, err = GradeProposerRun(gradeDir, sc.proposerWant)
	case KindReviewer:
		out.Pass, out.Reason, err = GradeReviewerRun(gradeDir, sc.reviewerWant)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}
