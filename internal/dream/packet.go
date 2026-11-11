package dream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Evidence is one pointer inside a packet, labelled DEMONSTRATED or
// DISCUSSED per the proposer brief.
type Evidence struct {
	Kind  string `json:"kind"`
	Where string `json:"where"`
	What  string `json:"what"`
}

// Packet is one proposer evidence packet, as written to
// packets/<n>.json. Sessions lists the distinct source-session
// identifiers the evidence was drawn from: it is what the two-tier
// gate counts independent sightings against, so a claim evidenced
// across two different sessions in the same packet clears the bar in
// one run, and a claim seen in only one session waits for a later
// night to add a second one.
type Packet struct {
	Claim    string     `json:"claim"`
	Type     string     `json:"type"`
	Evidence []Evidence `json:"evidence,omitempty"`
	Sessions []string   `json:"sessions,omitempty"`
	Coverage string     `json:"coverage,omitempty"`
	Tier     string     `json:"tier"`
	Target   string     `json:"target"`
	Text     string     `json:"text"`
}

// Verdict is one reviewer verdict, as written to verdicts/<n>.json.
type Verdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Proof   string `json:"proof"`
}

// PacketFile pairs a loaded packet with the number that names it, so
// downstream stages can find its matching verdict file and can report
// which file a problem came from.
type PacketFile struct {
	N      int
	Path   string
	Packet Packet
}

func packetsDir(runDir string) string  { return filepath.Join(runDir, "packets") }
func verdictsDir(runDir string) string { return filepath.Join(runDir, "verdicts") }

// LoadPackets reads every packets/<n>.json in runDir and returns them
// sorted by n ascending. A missing packets directory is an empty run,
// not an error: the proposer says so explicitly when a night teaches
// nothing.
func LoadPackets(runDir string) ([]PacketFile, error) {
	dir := packetsDir(runDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PacketFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("dream: packet file %s: name must be <n>.json: %w", e.Name(), err)
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p Packet
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("dream: packet %s: %w", path, err)
		}
		out = append(out, PacketFile{N: n, Path: path, Packet: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out, nil
}

// LoadVerdict reads verdicts/<n>.json. ok is false when the file does
// not exist, which is the normal case for a packet the gate held back
// before it ever reached a reviewer.
func LoadVerdict(runDir string, n int) (Verdict, bool, error) {
	path := filepath.Join(verdictsDir(runDir), strconv.Itoa(n)+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Verdict{}, false, nil
		}
		return Verdict{}, false, err
	}
	var v Verdict
	if err := json.Unmarshal(b, &v); err != nil {
		return Verdict{}, false, fmt.Errorf("dream: verdict %s: %w", path, err)
	}
	return v, true, nil
}
