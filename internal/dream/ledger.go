package dream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/statefile"
)

type Status string

const (
	StatusCandidate   Status = "candidate"
	StatusWritten     Status = "written"
	StatusRefuted     Status = "refuted"
	StatusUnevidenced Status = "unevidenced"
	StatusDead        Status = "dead"
)

// The claim type is chosen by the judging worker and nothing here
// verifies it. Only TypeOperatorPreference changes what Gate does, so a
// worker that mislabels an inferred claim as a preference skips the
// two-session bar; the check for that lives in the judging brief.
const (
	TypeOperatorPreference = "operator-preference"
	TypeToolBehavior       = "tool-behavior"
	TypeHostState          = "host-state"
	TypeCodeBehavior       = "code-behavior"
	TypeProcessPattern     = "process-pattern"
)

type Entry struct {
	Fingerprint      string   `json:"fingerprint"`
	Claim            string   `json:"claim"`
	Type             string   `json:"type"`
	Sessions         []string `json:"sessions"`
	FirstSeen        string   `json:"first_seen"`
	LastSeen         string   `json:"last_seen"`
	Status           Status   `json:"status"`
	Reason           string   `json:"reason,omitempty"`
	UnevidencedCount int      `json:"unevidenced_count,omitempty"`
	RunID            string   `json:"run_id,omitempty"`
}

type Ledger struct {
	Entries map[string]*Entry `json:"entries"`
	path    string
}

func LoadLedger(project string) (*Ledger, error) {
	p, err := statefile.Path(project, filepath.Join("dreams", "ledger.json"))
	if err != nil {
		return nil, err
	}
	l := &Ledger{Entries: map[string]*Entry{}, path: p}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", p, err)
	}
	if l.Entries == nil {
		l.Entries = map[string]*Entry{}
	}
	l.path = p
	return l, nil
}

func (l *Ledger) Save() error {
	return statefile.WriteJSONAtomic(l.path, "dream-ledger", l)
}

// Fingerprint normalises a claim so cosmetic rewording maps to the same
// entry: case, punctuation and spacing are dropped, word order and
// wording are not. It collapses less than it looks like it does, so a
// claim reworded between two sessions gets two entries and never reaches
// the recurrence bar. Keep the judging worker emitting one canonical
// phrasing per claim; this is the knob most likely to need tuning.
func Fingerprint(claimType, claim string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(claim) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	norm := strings.Join(strings.Fields(b.String()), " ")
	if norm == "" {
		// A claim with no ASCII alphanumerics at all normalises to
		// nothing, which would merge every such claim into one entry.
		// Fall back to the raw text so it keeps its own identity.
		norm = strings.Join(strings.Fields(strings.ToLower(claim)), " ")
	}
	sum := sha256.Sum256([]byte(claimType + "\x00" + norm))
	return hex.EncodeToString(sum[:6])
}

// Observe records one sighting of a claim and returns its entry. A
// repeat sighting from a session already counted does not raise the
// occurrence count: the bar is independent sessions, not repetitions.
// Two sessions from the same night do count as two, because a night's
// whole corpus carries one date and a cross-day bar would delay every
// inferred claim to its second night at the earliest.
func (l *Ledger) Observe(claimType, claim, sessionID, day string) *Entry {
	fp := Fingerprint(claimType, claim)
	e, ok := l.Entries[fp]
	if !ok {
		e = &Entry{
			Fingerprint: fp,
			Claim:       claim,
			Type:        claimType,
			FirstSeen:   day,
			Status:      StatusCandidate,
		}
		l.Entries[fp] = e
	}
	e.LastSeen = day
	for _, s := range e.Sessions {
		if s == sessionID {
			return e
		}
	}
	e.Sessions = append(e.Sessions, sessionID)
	return e
}

// Gate applies the two-tier evidence bar: an operator preference passes
// on first sighting, anything inferred needs threshold independent
// sessions. Dead and refuted fingerprints never pass, and neither does
// one already written, so a written claim is never re-judged. Reversing
// a written claim takes a Record call from outside the nightly loop.
func (l *Ledger) Gate(e *Entry, threshold int) bool {
	switch e.Status {
	case StatusRefuted, StatusDead, StatusWritten:
		return false
	}
	if e.Type == TypeOperatorPreference {
		return true
	}
	return len(e.Sessions) >= threshold
}

// RevertRunLedger puts every entry a run wrote back to candidate,
// clearing the run id that marked it written. Reverting a run's files
// without this leaves the ledger believing those claims are already
// written, so they never clear the gate again and a restored state.md
// can never regain its lesson.
func RevertRunLedger(project, runID string) error {
	l, err := LoadLedger(project)
	if err != nil {
		return err
	}
	changed := false
	for _, e := range l.Entries {
		if e.RunID != runID || e.Status != StatusWritten {
			continue
		}
		e.Status = StatusCandidate
		e.RunID = ""
		changed = true
	}
	if !changed {
		return nil
	}
	return l.Save()
}

func (l *Ledger) Record(fp string, st Status, reason, runID string) {
	e, ok := l.Entries[fp]
	if !ok {
		return
	}
	e.Reason = reason
	e.RunID = runID
	if st == StatusUnevidenced {
		e.UnevidencedCount++
		if e.UnevidencedCount >= 2 {
			e.Status = StatusDead
			return
		}
		e.Status = StatusCandidate
		return
	}
	e.Status = st
}
