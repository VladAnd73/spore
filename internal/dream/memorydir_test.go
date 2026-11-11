package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryDirFollowsClaudeCodeProjectEncoding(t *testing.T) {
	got := MemoryDir("/home/agent/.claude/projects", "/home/agent/my_repo.v2")
	want := "/home/agent/.claude/projects/-home-agent-my-repo-v2/memory"
	if got != want {
		t.Fatalf("MemoryDir = %q, want %q", got, want)
	}
}

func TestMintTaskRefusesARunDirectoryWithNoMemoryDir(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	runDir := t.TempDir()
	writeFile(t, filepath.Join(runDir, DigestFile), "# digest\n")
	writeFile(t, filepath.Join(runDir, KnownClaimsFile), "# known claims\n")

	if _, err := MintTask(tasksDir, "p", "run-1", runDir, nil); err == nil {
		t.Fatalf("expected a refusal for a run directory with no %s", MemoryDirFile)
	}
}

func TestMintTaskNamesTheMemoryDirectory(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	runDir := completeRunDir(t)
	writeFile(t, filepath.Join(runDir, MemoryDirFile), "/srv/claude/projects/-srv-p/memory\n")

	slug, err := MintTask(tasksDir, "p", "run-1", runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "- memory directory: /srv/claude/projects/-srv-p/memory\n") {
		t.Fatalf("minted task does not name the memory directory:\n%s", body)
	}
}

func TestRunRecordsTheProjectMemoryDir(t *testing.T) {
	f := newRunFixture(t, "proj")
	cwd := workerCwd("proj", "fix-a")
	f.worker(t, "proj", "fix-a",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, use the kernel flow"))

	rep := mustRun(t, f.opts)

	want := MemoryDir(f.projects, filepath.Dir(f.tasksDir))
	if got := strings.TrimSpace(readRunFile(t, rep.RunDir, MemoryDirFile)); got != want {
		t.Fatalf("%s = %q, want %q", MemoryDirFile, got, want)
	}
}

func memoryPacket(target string) string {
	return `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "memory",
		"target": "` + jsonPath(target) + `",
		"text": "---\nname: prefer-small-commits\ndescription: Split large diffs.\nmetadata:\n  type: feedback\n---\n\nSplit.\n"
	}`
}

func TestWriteRunRefusesAMemoryTargetOutsideTheMemoryDir(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	memDir := filepath.Join(t.TempDir(), "memory")
	setMemoryDir(t, runDir, memDir)
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	writePacketFile(t, runDir, 1, memoryPacket(outside))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRun("proj", "run-1", runDir, 10); err == nil {
		t.Fatal("expected a refusal for a memory target outside the memory dir")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("the outside target was written: %v", err)
	}
}

func TestWriteRunRefusesAMemoryTargetWhenNoMemoryDirWasRecorded(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "memory", "x.md")
	writePacketFile(t, runDir, 1, memoryPacket(target))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRun("proj", "run-1", runDir, 10); err == nil {
		t.Fatal("expected a refusal: nothing says where memory may go")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target written without a recorded memory dir: %v", err)
	}
}

func TestWriteRunWritesAMemoryTargetInsideTheMemoryDir(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	memDir := filepath.Join(t.TempDir(), "memory")
	setMemoryDir(t, runDir, memDir)
	target := filepath.Join(memDir, "prefer-small-commits.md")
	writePacketFile(t, runDir, 1, memoryPacket(target))
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"said so","proof":"session"}`)
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target inside the memory dir not written: %v", err)
	}
}

func setMemoryDir(t *testing.T, runDir, memDir string) {
	t.Helper()
	writeFile(t, filepath.Join(runDir, MemoryDirFile), memDir+"\n")
}

// Discover skips dream workers by slug shape; a mint that named its
// task differently would bring the nightly self-digest back.
func TestMintedSlugCarriesTheDreamPrefixDiscoverSkips(t *testing.T) {
	slug, err := MintTask(projectTasksDir(t, "p"), "p", "20260901-ab12", completeRunDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !mintedDreamSlugRE.MatchString(slug) {
		t.Fatalf("minted slug %q does not match %s", slug, mintedDreamSlugRE)
	}
}

// Claude Code truncates a long project slug and appends a hash, so the
// plain encoding names a directory that does not exist. The directory
// whose transcripts ran in the project root is the real one.
func TestMemoryDirFindsATruncatedProjectDirByItsTranscriptCwd(t *testing.T) {
	projects := t.TempDir()
	root := "/srv/" + strings.Repeat("deep-checkout-path/", 14) + "repo"
	if len(root) <= 200 {
		t.Fatalf("fixture root is %d chars, want > 200", len(root))
	}
	hashed := filepath.Join(projects, strings.Repeat("-srv-deep", 23)[:200]+"-k3x9q2")
	other := filepath.Join(projects, "-srv-other")
	for _, d := range []string{hashed, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(hashed, "s1.jsonl"),
		`{"type":"summary","summary":"no cwd on this line"}`+"\n"+
			`{"type":"user","cwd":"`+root+`","timestamp":"2026-09-01T01:00:00Z"}`+"\n")
	writeFile(t, filepath.Join(other, "s2.jsonl"), `{"type":"user","cwd":"/srv/other"}`+"\n")

	if got, want := MemoryDir(projects, root), filepath.Join(hashed, "memory"); got != want {
		t.Fatalf("MemoryDir = %q, want %q", got, want)
	}
}
