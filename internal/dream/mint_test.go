package dream

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task/frontmatter"
)

// testTmuxSocket: see internal/task/tmuxsocket_test.go for the
// socket-isolation rationale. The end-to-end mint test drives the real
// fleet reconciler, which spawns tmux sessions.
const testTmuxSocket = "default"

func TestMain(m *testing.M) {
	tmpdir, err := os.MkdirTemp("", "spore-dream-tmux-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: mkdtemp:", err)
		os.Exit(2)
	}
	if err := os.Setenv("TMUX_TMPDIR", tmpdir); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: setenv:", err)
		os.Exit(2)
	}
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	_ = exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", "keepalive", "sleep 86400").Run()
	code := m.Run()
	_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-server").Run()
	_ = os.RemoveAll(tmpdir)
	os.Exit(code)
}

func TestMintTaskWritesAnActiveTaskForTheNamedProject(t *testing.T) {
	tasksDir := projectTasksDir(t, "otherproj")
	runDir := completeRunDir(t)
	reads := []DeepRead{
		{Session: "sesn-1", Path: fakeTranscript(t, "sesn-1"), Entries: 1420},
		{Session: "sesn-2", Path: fakeTranscript(t, "sesn-2"), Entries: 77},
	}

	slug, err := MintTask(tasksDir, "otherproj", "20260901-ab12", runDir, reads)
	if err != nil {
		t.Fatal(err)
	}
	body := readTask(t, tasksDir, slug)
	for _, want := range []string{
		"project: otherproj",
		"status: active",
		"20260901-ab12",
		"sesn-1",
		"sesn-2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("minted task is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "status: draft") {
		t.Error("a minted dream task must not be a draft: fleet.Reconcile only spawns status=active")
	}
}

func TestMintTaskAllocatesAroundACollision(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	runDir := completeRunDir(t)

	first, err := MintTask(tasksDir, "p", "run-1", runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstBody := readTask(t, tasksDir, first)

	second, err := MintTask(tasksDir, "p", "run-1", runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("a second mint must allocate a distinct slug, both were %q", first)
	}
	if got := readTask(t, tasksDir, first); got != firstBody {
		t.Error("the second mint overwrote the first task file")
	}
}

func TestMintedTaskParsesBackAndCarriesTheWholeProposerBrief(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	slug, err := MintTask(tasksDir, "p", "run-1", completeRunDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatalf("frontmatter.Parse: %v", err)
	}
	if m.Slug != slug {
		t.Errorf("slug = %q, want %q", m.Slug, slug)
	}
	for _, tail := range []string{"## An empty night is a result", "outlives it."} {
		if !strings.Contains(string(body), tail) {
			t.Errorf("the tail of the proposer brief is missing %q: the body is truncated", tail)
		}
	}
	if !strings.Contains(string(body), ProposerBrief) {
		t.Error("the body does not carry the proposer brief verbatim")
	}
}

func TestMintedTaskResolvesEveryPathAProposerNeeds(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	runDir := completeRunDir(t)
	transcript := fakeTranscript(t, "sesn-1")

	slug, err := MintTask(tasksDir, "p", "run-1", runDir,
		[]DeepRead{{Session: "sesn-1", Path: transcript, Entries: 1420}})
	if err != nil {
		t.Fatal(err)
	}
	body := readTask(t, tasksDir, slug)
	for _, want := range []string{
		runDir,
		filepath.Join(runDir, DigestFile),
		filepath.Join(runDir, KnownClaimsFile),
		transcript,
		"1420",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("minted task does not resolve %q:\n%s", want, body)
		}
	}
}

func TestMintTaskRefusesAnIncompleteRunDirectory(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")

	missing := filepath.Join(t.TempDir(), "not-there")
	if _, err := MintTask(tasksDir, "p", "run-1", missing, nil); err == nil {
		t.Error("expected a refusal for a run directory that does not exist")
	}

	bare := t.TempDir()
	if _, err := MintTask(tasksDir, "p", "run-1", bare, nil); err == nil {
		t.Errorf("expected a refusal for a run directory with no %s", DigestFile)
	}

	writeFile(t, filepath.Join(bare, DigestFile), "digest\n")
	if _, err := MintTask(tasksDir, "p", "run-1", bare, nil); err == nil {
		t.Errorf("expected a refusal for a run directory with no %s", KnownClaimsFile)
	}

	writeFile(t, filepath.Join(bare, KnownClaimsFile), "claims\n")
	writeFile(t, filepath.Join(bare, MemoryDirFile), "/srv/claude/projects/-srv-p/memory\n")
	if _, err := MintTask(tasksDir, "p", "run-1", bare, nil); err != nil {
		t.Errorf("a complete run directory must mint: %v", err)
	}
}

func TestMintTaskRefusesAProjectThatDoesNotOwnTheTasksDir(t *testing.T) {
	tasksDir := projectTasksDir(t, "realproj")
	if _, err := MintTask(tasksDir, "otherproj", "run-1", completeRunDir(t), nil); err == nil {
		t.Error("expected a refusal: the tasks dir belongs to realproj, not otherproj")
	}
	if entries, _ := os.ReadDir(tasksDir); len(entries) != 0 {
		t.Error("a refused mint must not leave a task file behind")
	}
}

func TestMintTaskRefusesADeepReadAnAgentCannotUse(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	runDir := completeRunDir(t)
	transcript := fakeTranscript(t, "sesn-1")

	cases := map[string]DeepRead{
		"no session id":  {Session: "", Path: transcript, Entries: 3},
		"no path":        {Session: "sesn-1", Entries: 3},
		"relative path":  {Session: "sesn-1", Path: "sesn-1.jsonl", Entries: 3},
		"absent path":    {Session: "sesn-1", Path: filepath.Join(t.TempDir(), "gone.jsonl"), Entries: 3},
		"no entry count": {Session: "sesn-1", Path: transcript},
	}
	for name, dr := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := MintTask(tasksDir, "p", "run-1", runDir, []DeepRead{dr}); err == nil {
				t.Errorf("expected a refusal for a deep read with %s", name)
			}
		})
	}
}

func TestMintedTaskCarriesAnRFC3339CreatedStamp(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	slug, err := MintTask(tasksDir, "p", "run-1", completeRunDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, m.Created); err != nil {
		t.Errorf("created = %q, want RFC3339: %v", m.Created, err)
	}
}

func TestMintedTaskFileIsNoMorePermissiveThan0644(t *testing.T) {
	tasksDir := projectTasksDir(t, "p")
	slug, err := MintTask(tasksDir, "p", "run-1", completeRunDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if extra := info.Mode().Perm() &^ 0o644; extra != 0 {
		t.Errorf("mode = %04o, more permissive than 0644 by %04o", info.Mode().Perm(), extra)
	}
}

// TestMintedTaskIsSpawnedByTheFleetWhileADraftIsNot is the end-to-end
// scenario: it mints into a real project's tasks directory and runs the
// reconciler that production runs, which is the only thing that turns a
// task file into a working agent.
func TestMintedTaskIsSpawnedByTheFleetWhileADraftIsNot(t *testing.T) {
	requireToolchain(t)

	const project = "dreame2e"
	tasksDir := projectTasksDir(t, project)
	projectRoot := filepath.Dir(tasksDir)
	gitInit(t, projectRoot)

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := fleet.Enable(); err != nil {
		t.Fatalf("fleet.Enable: %v", err)
	}
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	t.Cleanup(func() { killSporeSessions(projectRoot) })

	slug, err := MintTask(tasksDir, project, "20260903-e2e", completeRunDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	draft := frontmatter.Meta{Status: "draft", Slug: "dream-draft-control", Title: "control"}
	writeFile(t, filepath.Join(tasksDir, "dream-draft-control.md"),
		string(frontmatter.Write(draft, nil)))

	res, err := fleet.Reconcile(fleet.Config{
		TasksDir:    tasksDir,
		ProjectRoot: projectRoot,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("fleet.Reconcile: %v", err)
	}
	if !contains(res.Spawned, slug) {
		t.Errorf("the minted task was not spawned: Spawned = %v", res.Spawned)
	}
	if contains(res.Spawned, "dream-draft-control") {
		t.Error("a draft was spawned; the draft-never-runs finding no longer holds")
	}
	if !hasTmuxSession("spore/" + project + "/" + slug) {
		t.Error("no live tmux session for the minted task")
	}
}

func projectTasksDir(t *testing.T, project string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), project, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// completeRunDir builds the run directory as task 10 must leave it:
// both files the proposer brief promises are already on disk.
func completeRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, DigestFile), "# digest\n")
	writeFile(t, filepath.Join(dir, KnownClaimsFile), "# known claims\n")
	writeFile(t, filepath.Join(dir, MemoryDirFile), "/srv/claude/projects/-srv-p/memory\n")
	return dir
}

func fakeTranscript(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".jsonl")
	writeFile(t, path, "{\"type\":\"user\"}\n")
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTask(t *testing.T, tasksDir, slug string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func requireToolchain(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"git", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available: %v", bin, err)
		}
	}
}

func gitInit(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func hasTmuxSession(name string) bool {
	return exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", name).Run() == nil
}

func killSporeSessions(projectRoot string) {
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	prefix := "spore/" + filepath.Base(projectRoot) + "/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, prefix) {
			_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", line).Run()
		}
	}
}
