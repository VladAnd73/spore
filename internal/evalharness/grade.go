package evalharness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/dream"
)

// ProposerExpectation is the code-graded, exact-match verdict a
// proposer scenario expects, per the evidence taxonomy proposer.md
// defines: DEMONSTRATED evidence is something that happened,
// DISCUSSED evidence is something that was only described, and a
// packet whose evidence is all DISCUSSED is not supposed to exist.
type ProposerExpectation int

const (
	// ExpectDemonstrated wants at least one written packet whose
	// evidence includes a DEMONSTRATED item.
	ExpectDemonstrated ProposerExpectation = iota
	// ExpectNotDemonstrated wants no written packet to cite
	// DEMONSTRATED evidence. A packet may still exist (e.g. correctly
	// tagged DISCUSSED and left for a human), it just may not claim
	// something happened when nothing in the fixture did.
	ExpectNotDemonstrated
	// ExpectNoPackets wants an empty night: no packets/<n>.json at all.
	ExpectNoPackets
)

// GradeProposerRun inspects runDir's packets/ directory (written, if at
// all, by the spawned agent) and reports whether it matches want.
func GradeProposerRun(runDir string, want ProposerExpectation) (pass bool, reason string, err error) {
	packets, err := dream.LoadPackets(runDir)
	if err != nil {
		return false, "", fmt.Errorf("evalharness: grade proposer run: %w", err)
	}
	hasDemonstrated := false
	for _, pf := range packets {
		for _, ev := range pf.Packet.Evidence {
			if ev.Kind == "DEMONSTRATED" {
				hasDemonstrated = true
			}
		}
	}
	switch want {
	case ExpectNoPackets:
		if len(packets) == 0 {
			return true, "no packets were written", nil
		}
		return false, fmt.Sprintf("expected no packets, got %d", len(packets)), nil
	case ExpectDemonstrated:
		if hasDemonstrated {
			return true, "at least one packet cites DEMONSTRATED evidence", nil
		}
		return false, "no packet cites DEMONSTRATED evidence", nil
	case ExpectNotDemonstrated:
		if !hasDemonstrated {
			return true, "no packet wrongly cites DEMONSTRATED evidence", nil
		}
		return false, "a packet cites DEMONSTRATED evidence for a claim this fixture never demonstrated", nil
	default:
		return false, "", fmt.Errorf("evalharness: grade proposer run: unknown expectation %d", want)
	}
}

// ReviewerExpectation is the code-graded, exact-match verdict a
// reviewer scenario expects. reviewer.md's verdict field is one of
// confirmed, refuted, or unevidenced; ExpectRefuse accepts either of
// the latter two, since both mean the reviewer did not approve.
type ReviewerExpectation int

const (
	ExpectApprove ReviewerExpectation = iota
	ExpectRefuse
)

// GradeReviewerRun reads the single verdict file the spawned reviewer
// was expected to write under runWD/verdicts/ and reports whether it
// matches want.
func GradeReviewerRun(runWD string, want ReviewerExpectation) (pass bool, reason string, err error) {
	v, ok, err := loadFirstVerdict(runWD)
	if err != nil {
		return false, "", fmt.Errorf("evalharness: grade reviewer run: %w", err)
	}
	if !ok {
		return false, "no verdicts/*.json file was written", nil
	}
	approved := v.Verdict == "confirmed"
	switch want {
	case ExpectApprove:
		if approved {
			return true, "verdict=confirmed", nil
		}
		return false, fmt.Sprintf("expected confirmed, got %q: %s", v.Verdict, v.Reason), nil
	case ExpectRefuse:
		if !approved {
			return true, fmt.Sprintf("verdict=%s: %s", v.Verdict, v.Reason), nil
		}
		return false, "expected refuted or unevidenced, got confirmed: " + v.Reason, nil
	default:
		return false, "", fmt.Errorf("evalharness: grade reviewer run: unknown expectation %d", want)
	}
}

// loadFirstVerdict reads the lowest-numbered verdicts/<n>.json under
// runWD. A reviewer scenario's fixture always carries exactly one
// packet, so there is exactly one verdict file to find; a missing
// verdicts/ directory is not an error, it is a reviewer that never
// wrote its answer.
func loadFirstVerdict(runWD string) (dream.Verdict, bool, error) {
	dir := filepath.Join(runWD, "verdicts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return dream.Verdict{}, false, nil
		}
		return dream.Verdict{}, false, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return dream.Verdict{}, false, nil
	}
	sort.Strings(names)
	b, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		return dream.Verdict{}, false, err
	}
	var v dream.Verdict
	if err := json.Unmarshal(b, &v); err != nil {
		return dream.Verdict{}, false, fmt.Errorf("verdict %s: %w", names[0], err)
	}
	return v, true, nil
}
