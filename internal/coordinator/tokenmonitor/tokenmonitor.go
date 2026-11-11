// Package tokenmonitor wraps the budget short-window token context
// into a stop-hook shape for the coordinator. It reads the hook
// payload (session_id + transcript_path), sums input tokens from the
// latest assistant message's usage block, and fires reminders when
// thresholds are crossed. Three bands: soft (once per session, wrap
// at the next natural break), hard (every Stop, finish the in-flight
// unit then wrap; start nothing new), force (every Stop, wrap
// immediately regardless of what is in flight).
package tokenmonitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/transcript"
)

const (
	DefaultSoftCap = 250000
	DefaultHardCap = 290000

	// DefaultForceCap is the wrap ceiling on a 1M-context session; past it
	// the monitor demands an immediate wrap even mid-unit.
	DefaultForceCap = 300000

	// coordinatorRespawnCommand is the tmux command surfaced to the
	// coordinator agent on soft/hard cap. respawn-pane -k re-execs the
	// pane's command without tearing down the session, so an
	// SSH-attached operator stays connected while the agent process is
	// replaced. The session-name expression resolves to the current
	// session, so an external_session_pattern coordinator (running
	// under a non-spore session name) respawns itself in place too.
	// The exec target is the host shim that spore infect installs;
	// keep this in sync with internal/infect/infect.go install paths.
	coordinatorRespawnCommand = `tmux respawn-pane -k -t "$(tmux display-message -p '#S'):0" 'exec /usr/local/bin/spore-coordinator-launch'`
)

type Config struct {
	SoftCap    int
	HardCap    int
	ForceCap   int
	StateDir   string
	LedgerFile string
	Inbox      string
}

type CheckResult struct {
	Ctx        int    `json:"ctx"`
	SoftCap    int    `json:"soft_cap"`
	HardCap    int    `json:"hard_cap"`
	ForceCap   int    `json:"force_cap"`
	Level      string `json:"level"`
	Message    string `json:"message,omitempty"`
	ShouldFire bool   `json:"should_fire"`
}

type HookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func (c Config) Defaults() Config {
	if c.SoftCap <= 0 {
		c.SoftCap = DefaultSoftCap
	}
	if c.HardCap <= 0 {
		c.HardCap = DefaultHardCap
	}
	if c.ForceCap <= 0 {
		c.ForceCap = DefaultForceCap
	}
	if c.StateDir == "" {
		c.StateDir = defaultStateDir()
	}
	if c.LedgerFile == "" {
		c.LedgerFile = filepath.Join(c.StateDir, "token-monitor.jsonl")
	}
	return c
}

// defaultStateDir resolves the coordinator state dir from the
// SPORE_COORDINATOR_STATE_DIR env var, falling back to
// $HOME/.local/state/spore/coordinator.
func defaultStateDir() string {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "spore", "coordinator")
}

// IsCoordinator returns true if the inbox path is under the state
// dir, matching the self-test logic from the bash version.
func (c Config) IsCoordinator() bool {
	if c.Inbox == "" {
		return false
	}
	stateRoot := strings.TrimRight(c.StateDir, "/")
	return c.Inbox == stateRoot || strings.HasPrefix(c.Inbox, stateRoot+"/")
}

// Check reads the transcript, sums context tokens, and determines
// whether a soft or hard cap has been crossed. The sessionID is used
// for the soft-marker (fire only once per session at soft cap).
func Check(cfg Config, payload HookPayload) CheckResult {
	cfg = cfg.Defaults()

	if !cfg.IsCoordinator() {
		return CheckResult{Level: "skip"}
	}

	tpath := payload.TranscriptPath
	if tpath == "" || !fileExists(tpath) {
		tpath = transcript.FindFallbackTranscript()
	}
	if tpath == "" {
		return CheckResult{Level: "skip"}
	}

	ctx := transcript.SumContextTokens(tpath)
	if ctx <= 0 {
		return CheckResult{Level: "ok", Ctx: ctx, SoftCap: cfg.SoftCap, HardCap: cfg.HardCap}
	}

	markerDir := filepath.Join(cfg.StateDir, "token-monitor")
	os.MkdirAll(markerDir, 0o700)
	sid := payload.SessionID
	if sid == "" {
		sid = "unknown"
	}
	softMarker := filepath.Join(markerDir, sid+".soft")

	result := CheckResult{
		Ctx:      ctx,
		SoftCap:  cfg.SoftCap,
		HardCap:  cfg.HardCap,
		ForceCap: cfg.ForceCap,
	}

	if ctx >= cfg.ForceCap {
		result.Level = "force"
		result.ShouldFire = true
		result.Message = fmt.Sprintf(
			"COORDINATOR TOKEN MONITOR (force): context %d tokens >= force cap %d.\n"+
				"Quality degrades from here. Wrap up IMMEDIATELY, regardless of\n"+
				"what is in flight:\n"+
				"  1. Flush state.md now; note where the in-flight unit stopped.\n"+
				"  2. Post a one-line summary to the operator if anything is still open.\n"+
				"  3. Run: %s\n"+
				"respawn-pane preserves the tmux session and any attached SSH client;\n"+
				"only the agent process is replaced. The reconciler is not involved.",
			ctx, cfg.ForceCap, coordinatorRespawnCommand)
		appendLedger(cfg, sid, ctx, false, false, true)
		return result
	}

	if ctx >= cfg.HardCap {
		result.Level = "hard"
		result.ShouldFire = true
		result.Message = fmt.Sprintf(
			"COORDINATOR TOKEN MONITOR (finish-unit): context %d tokens >= finish-unit cap %d.\n"+
				"Do not start new investigations or new units of work.\n"+
				"Finish the unit currently in flight, then wrap:\n"+
				"  1. Flush state.md so the next coordinator boots from it.\n"+
				"  2. Post a one-line summary to the operator if anything is still open.\n"+
				"  3. Run: %s\n"+
				"respawn-pane preserves the tmux session and any attached SSH client;\n"+
				"only the agent process is replaced. The reconciler is not involved.\n"+
				"This reminder fires on every Stop past the cap. Force cap is %d;\n"+
				"crossing it demands an immediate wrap even mid-unit.",
			ctx, cfg.HardCap, coordinatorRespawnCommand, cfg.ForceCap)
		appendLedger(cfg, sid, ctx, false, true, false)
		return result
	}

	if ctx >= cfg.SoftCap && !fileExists(softMarker) {
		touch(softMarker)
		result.Level = "soft"
		result.ShouldFire = true
		result.Message = fmt.Sprintf(
			"COORDINATOR TOKEN MONITOR (soft): context %d tokens >= soft warn %d.\n"+
				"Wrap up at the next natural break: flush state.md, then run\n"+
				"  %s\n"+
				"Finish-unit cap is %d (past it, finish the in-flight unit and wrap;\n"+
				"a reminder fires on every Stop). Force cap is %d (immediate wrap).",
			ctx, cfg.SoftCap, coordinatorRespawnCommand, cfg.HardCap, cfg.ForceCap)
		appendLedger(cfg, sid, ctx, true, false, false)
		return result
	}

	result.Level = "ok"
	appendLedger(cfg, sid, ctx, false, false, false)
	return result
}

// appendLedger writes one row per check. force_cap / force_fired were
// added after the first ledger shape shipped; readers must tolerate
// rows without them (LedgerVerdict does: missing bools decode false).
func appendLedger(cfg Config, sessionID string, ctx int, softFired, hardFired, forceFired bool) {
	os.MkdirAll(filepath.Dir(cfg.LedgerFile), 0o700)
	f, err := os.OpenFile(cfg.LedgerFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(f, `{"ts":"%s","session_id":"%s","ctx":%d,"soft_cap":%d,"hard_cap":%d,"force_cap":%d,"soft_fired":%s,"hard_fired":%s,"force_fired":%s}`+"\n",
		ts, sessionID, ctx, cfg.SoftCap, cfg.HardCap, cfg.ForceCap,
		strconv.FormatBool(softFired), strconv.FormatBool(hardFired),
		strconv.FormatBool(forceFired))
}

// LedgerVerdict reads the token-monitor ledger and returns whether
// the trailing N signal-bearing sessions are all "broken" (crossed soft
// cap but never fired any level). Rows written before the force band
// existed lack force_cap / force_fired and decode with those false.
// Returns (broken, sessionIDs).
func LedgerVerdict(ledgerFile string, softCap int, threshold int) (bool, string) {
	f, err := os.Open(ledgerFile)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	type sessionInfo struct {
		peak     int
		cap      int
		anyFired bool
	}

	var order []string
	sessions := make(map[string]*sessionInfo)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		var row struct {
			SessionID  string `json:"session_id"`
			Ctx        int    `json:"ctx"`
			SoftCap    int    `json:"soft_cap"`
			SoftFired  bool   `json:"soft_fired"`
			HardFired  bool   `json:"hard_fired"`
			ForceFired bool   `json:"force_fired"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.SessionID == "" {
			continue
		}
		info, exists := sessions[row.SessionID]
		if !exists {
			info = &sessionInfo{}
			sessions[row.SessionID] = info
			order = append(order, row.SessionID)
		}
		if row.Ctx > info.peak {
			info.peak = row.Ctx
		}
		if row.SoftCap > 0 {
			info.cap = row.SoftCap
		}
		if row.SoftFired || row.HardFired || row.ForceFired {
			info.anyFired = true
		}
	}

	var signal []string
	for _, sid := range order {
		info := sessions[sid]
		cap := info.cap
		if cap <= 0 {
			cap = softCap
		}
		if info.peak >= cap && cap > 0 {
			signal = append(signal, sid)
		}
	}

	run := 0
	for i := len(signal) - 1; i >= 0; i-- {
		if !sessions[signal[i]].anyFired {
			run++
		} else {
			break
		}
	}

	if run >= threshold {
		start := len(signal) - run
		broken := ""
		for i := start; i < len(signal); i++ {
			if broken != "" {
				broken += " "
			}
			broken += signal[i]
		}
		return true, broken
	}
	return false, ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func touch(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		f.Close()
	}
}
