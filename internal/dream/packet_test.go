package dream

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writePacketFile(t *testing.T, runDir string, n int, body string) {
	t.Helper()
	dir := packetsDir(runDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPacketsReadsEveryPacketInNumericOrder(t *testing.T) {
	runDir := t.TempDir()
	writePacketFile(t, runDir, 2, `{"claim":"second claim","type":"host-state","sessions":["sesn-2"],"tier":"memory","target":"/tmp/memory/two.md","text":"---\nname: two\ndescription: second claim\n---\n"}`)
	writePacketFile(t, runDir, 10, `{"claim":"tenth claim","type":"host-state","sessions":["sesn-3"],"tier":"memory","target":"/tmp/memory/ten.md","text":"---\nname: ten\ndescription: tenth claim\n---\n"}`)
	writePacketFile(t, runDir, 1, `{"claim":"first claim","type":"operator-preference","sessions":["sesn-1"],"tier":"memory","target":"/tmp/memory/one.md","text":"---\nname: one\ndescription: first claim\n---\n"}`)

	got, err := LoadPackets(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d packets, want 3", len(got))
	}
	wantOrder := []int{1, 2, 10}
	for i, w := range wantOrder {
		if got[i].N != w {
			t.Fatalf("position %d: got N=%d, want %d", i, got[i].N, w)
		}
	}
	if got[0].Packet.Claim != "first claim" {
		t.Fatalf("got claim %q, want %q", got[0].Packet.Claim, "first claim")
	}
	if got[0].Packet.Type != TypeOperatorPreference {
		t.Fatalf("got type %q, want %q", got[0].Packet.Type, TypeOperatorPreference)
	}
}

func TestLoadPacketsOnMissingDirReturnsEmpty(t *testing.T) {
	runDir := t.TempDir()
	got, err := LoadPackets(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d packets, want 0", len(got))
	}
}

func TestLoadVerdictReadsConfirmedReasonAndProof(t *testing.T) {
	runDir := t.TempDir()
	dir := verdictsDir(runDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1.json")
	if err := os.WriteFile(path, []byte(`{"verdict":"confirmed","reason":"checked --help","proof":"ran spore dream --help and saw the flag"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok, err := LoadVerdict(runDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected verdict to be found")
	}
	if v.Verdict != "confirmed" || v.Reason != "checked --help" || v.Proof == "" {
		t.Fatalf("unexpected verdict: %+v", v)
	}
}

func TestLoadVerdictMissingFileIsNotFoundNotError(t *testing.T) {
	runDir := t.TempDir()
	_, ok, err := LoadVerdict(runDir, 7)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for a packet that was never reviewed")
	}
}
