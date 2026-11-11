package dream

import (
	"fmt"
)

// GateResult is what the two-tier bar decided about one packet.
type GateResult struct {
	N           int
	Fingerprint string
	Cleared     bool
	Packet      Packet
}

// GateRun applies the two-tier evidence bar to every packet the
// proposer wrote this run. An operator-preference packet clears on
// first sighting; anything else needs occurrences across threshold
// independent sessions, counted against the persistent ledger so a
// second sighting on a later night still clears it. Every sighting is
// recorded whether or not it clears, so the count is never lost.
//
// A packet naming no session at all is fingerprinted against the run
// itself: it can only ever count as one sighting no matter how many
// times the same run proposes it, which is the conservative side of
// the bar to be wrong on.
func GateRun(project, runID, runDir string, threshold int) ([]GateResult, error) {
	packets, err := LoadPackets(runDir)
	if err != nil {
		return nil, err
	}
	if len(packets) == 0 {
		return nil, nil
	}
	l, err := LoadLedger(project)
	if err != nil {
		return nil, err
	}
	day := runDay(runID)

	var results []GateResult
	for _, pf := range packets {
		sessions := pf.Packet.Sessions
		if len(sessions) == 0 {
			sessions = []string{runID}
		}
		var e *Entry
		for _, s := range sessions {
			e = l.Observe(pf.Packet.Type, pf.Packet.Claim, s, day)
		}
		results = append(results, GateResult{
			N:           pf.N,
			Fingerprint: e.Fingerprint,
			Cleared:     l.Gate(e, threshold),
			Packet:      pf.Packet,
		})
	}
	if err := l.Save(); err != nil {
		return nil, err
	}
	return results, nil
}

// runDay extracts the calendar date a run id was minted on. Run ids
// are always <YYYYMMDD>-<suffix> (internal/dream's Run builds them that
// way), but a malformed one still needs a stable day string rather than
// an error: gating is not the place to discover a run id is broken.
func runDay(runID string) string {
	if len(runID) >= 8 {
		y, m, d := runID[0:4], runID[4:6], runID[6:8]
		if isDigits(y) && isDigits(m) && isDigits(d) {
			return fmt.Sprintf("%s-%s-%s", y, m, d)
		}
	}
	return runID
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
