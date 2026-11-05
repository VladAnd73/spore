package fleet

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
)

const fakeGH = `#!/bin/sh
log="$FAKE_GH_LOG"
{ printf 'ARGS:'; for a in "$@"; do printf ' %s' "$a"; done; printf '\nTOKEN: %s\n' "$GH_TOKEN"; } >> "$log"
for a in "$@"; do
  if [ "$a" = "--body-file" ]; then printf 'BODY:\n' >> "$log"; cat >> "$log"; printf 'END\n' >> "$log"; fi
done
if [ -e "$FAKE_GH_FAIL" ]; then echo "gh exploded" >&2; exit 1; fi
case "$1 $2" in
  "pr list") echo '[]' ;;
  "pr create") echo "https://github.com/acme/widget/pull/12" ;;
esac
`

type mirrorFixture struct {
	root, tasksDir, slug, wt, bare, ghLog, failFile, inbox string
	mints                                                  *atomic.Int32
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newMirrorFixture builds a project whose origin is an https URL that
// git rewrites (insteadOf) to a local bare repo, a task worktree on
// wt/demo, a fake gh on PATH, and a github.toml minting tokens from an
// httptest server.
func newMirrorFixture(t *testing.T, prFlow string) *mirrorFixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	coordState := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", coordState)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Test")
		t.Setenv(k+"_EMAIL", "test@example.com")
	}

	f := &mirrorFixture{slug: "demo", mints: &atomic.Int32{}}
	f.root = newGitRepoFor(t, "demo-project")
	f.inbox = filepath.Join(coordState, "demo-project", "inbox")
	f.bare = filepath.Join(t.TempDir(), "widget.git")
	runGit(t, filepath.Dir(f.bare), "init", "-q", "--bare", f.bare)
	runGit(t, f.root, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(f.root, ".gitignore"), []byte(".spore/\n.worktrees/\ntasks/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "init")
	runGit(t, f.root, "remote", "add", "origin", "https://github.com/acme/widget.git")
	runGit(t, f.root, "config", "url."+f.bare+".insteadOf", "https://github.com/acme/widget.git")
	runGit(t, f.root, "push", "-q", "origin", "main")
	runGit(t, f.root, "fetch", "-q", "origin")
	runGit(t, f.root, "remote", "set-head", "origin", "main")
	f.wt = filepath.Join(f.root, ".worktrees", f.slug)
	runGit(t, f.root, "worktree", "add", "-q", "-b", "wt/demo", f.wt)
	f.commit(t, "one")

	f.tasksDir = filepath.Join(f.root, "tasks")
	if err := os.MkdirAll(f.tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.tasksDir, "demo.md"), []byte("---\nstatus: active\nslug: demo\ntitle: Demo widget\nloop: role\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := task.WriteSpec(f.root, f.slug, []byte("**Status**: draft\n\n# demo\n\nMake the widget spin.\n")); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.ghLog = filepath.Join(t.TempDir(), "gh.log")
	f.failFile = filepath.Join(t.TempDir(), "fail")
	t.Setenv("FAKE_GH_LOG", f.ghLog)
	t.Setenv("FAKE_GH_FAIL", f.failFile)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mints.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_app", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	}))
	t.Cleanup(srv.Close)
	cfg := "pr_flow = \"" + prFlow + "\"\napp_id = 1\ninstallation_id = 2\nprivate_key = \"" + pemPath +
		"\"\noperator_login = \"octo\"\ncommit_name = \"Octo Cat\"\ncommit_email = \"octo@example.com\"\napi_url = \"" + srv.URL + "\"\n"
	stateDir, err := task.StateDirForProject(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "github.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *mirrorFixture) commit(t *testing.T, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.wt, "add", name+".txt")
	runGit(t, f.wt, "commit", "-qm", name)
	return runGit(t, f.wt, "rev-parse", "HEAD")
}

func (f *mirrorFixture) tick(t *testing.T) Snapshot {
	t.Helper()
	snap, err := DeriveSnapshot(f.root, f.slug)
	if err != nil {
		t.Fatal(err)
	}
	mirrorGitHub(f.root, f.tasksDir, f.slug, snap)
	return snap
}

func (f *mirrorFixture) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.ghLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if a, ok := strings.CutPrefix(l, "ARGS: "); ok {
			out = append(out, a)
		}
	}
	return out
}

func (f *mirrorFixture) log(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.ghLog)
	return string(b)
}

func (f *mirrorFixture) state(t *testing.T) githubState {
	t.Helper()
	var s githubState
	b, err := os.ReadFile(githubStatePath(f.root, f.slug))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *mirrorFixture) bareHead(t *testing.T) string {
	t.Helper()
	return runGit(t, f.bare, "rev-parse", "refs/heads/wt/demo")
}

func TestGitHubMirrorFullFlow(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	f.tick(t)
	if c := f.calls(t); c != nil {
		t.Fatalf("no engineer round yet, gh calls = %v", c)
	}
	if f.mints.Load() != 0 {
		t.Fatalf("token minted with nothing to post")
	}

	writeEng(1)(t, f.root, f.slug)
	f.tick(t)
	f.tick(t)
	want := []string{
		"pr list --head wt/demo --state open --json number,url --limit 1",
		"pr create --draft --base main --head wt/demo --title Demo widget --body-file - --assignee octo",
	}
	if got := f.calls(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("round 1 calls:\n got %q\nwant %q", got, want)
	}
	head := runGit(t, f.wt, "rev-parse", "HEAD")
	if f.bareHead(t) != head {
		t.Fatalf("branch not pushed to origin")
	}
	log := f.log(t)
	for _, s := range []string{"TOKEN: ghs_app", "Make the widget spin.", "Dispatched by @octo", "`Review #1` / `Review #2`", "<details><summary>Spec</summary>"} {
		if !strings.Contains(log, s) {
			t.Errorf("gh log missing %q:\n%s", s, log)
		}
	}
	if s := f.state(t); s.PRNumber != 12 || s.PRURL != "https://github.com/acme/widget/pull/12" || s.PushedSHA != head {
		t.Fatalf("state = %+v", s)
	}

	mustWriteReview(t, f.root, f.slug, task.ReviewerA, 1, task.Review{Verdict: task.VerdictRequestChanges, Summary: "needs tests", Comments: []string{"add a test"}})
	f.tick(t)
	writeEng(2)(t, f.root, f.slug)
	head2 := f.commit(t, "two")
	f.tick(t)
	mustWriteReview(t, f.root, f.slug, task.ReviewerA, 2, task.Review{Verdict: task.VerdictApprove, Summary: "good"})
	mustWriteReview(t, f.root, f.slug, task.ReviewerB, 1, task.Review{Verdict: task.VerdictApprove, Summary: "ship it"})
	if snap := f.tick(t); snap.Phase != PhaseDone {
		t.Fatalf("phase = %s", snap.Phase)
	}
	for range 3 {
		f.tick(t)
	}
	want = append(want,
		"pr review 12 --comment --body-file -",
		"pr comment 12 --body-file -",
		"pr review 12 --comment --body-file -",
		"pr review 12 --comment --body-file -",
		"pr ready 12",
	)
	if got := f.calls(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("full calls:\n got %q\nwant %q", got, want)
	}
	if f.bareHead(t) != head2 {
		t.Fatalf("round 2 commit not pushed")
	}
	log = f.log(t)
	for _, s := range []string{
		"**Review #1 (round 1): request changes**\n\nneeds tests\n\n- add a test\n",
		"**Engineer (round 2)**\n\nAddressed:\n- placeholder\n",
		"**Review #1 (round 2): approve**",
		"**Review #2 (round 1): approve**\n\nship it",
	} {
		if !strings.Contains(log, s) {
			t.Errorf("gh log missing %q:\n%s", s, log)
		}
	}
	if !f.state(t).Ready {
		t.Fatal("ready flag not set")
	}
	envs := readInboxEnvelopes(t, f.inbox)
	if len(envs) != 1 || envs[0].Body != "demo: done, PR #12 ready for operator approval: https://github.com/acme/widget/pull/12" {
		t.Fatalf("envelopes = %+v", envs)
	}
	if f.mints.Load() != 1 {
		t.Fatalf("token mints = %d, want 1 (cached)", f.mints.Load())
	}
}

func TestGitHubMirrorFailureNotifiesOnce(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	writeEng(1)(t, f.root, f.slug)
	if err := os.WriteFile(f.failFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	f.tick(t)
	envs := readInboxEnvelopes(t, f.inbox)
	if len(envs) != 1 || !strings.Contains(envs[0].Body, "demo: github mirror failed") || !strings.Contains(envs[0].Body, "gh exploded") {
		t.Fatalf("envelopes = %+v", envs)
	}
	if s := f.state(t); s.LastError == "" || len(s.Posted) != 0 {
		t.Fatalf("state = %+v", s)
	}
	if err := os.Remove(f.failFile); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if s := f.state(t); s.LastError != "" || s.PRNumber != 12 || len(s.Posted) != 1 {
		t.Fatalf("after recovery state = %+v", s)
	}
}

func TestGitHubMirrorForcePushAfterRewrite(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	writeEng(1)(t, f.root, f.slug)
	f.tick(t)
	mustWriteReview(t, f.root, f.slug, task.ReviewerA, 1, task.Review{Verdict: task.VerdictRequestChanges, Summary: "squash"})
	f.tick(t)
	runGit(t, f.wt, "commit", "-q", "--amend", "-m", "one, amended")
	amended := runGit(t, f.wt, "rev-parse", "HEAD")
	writeEng(2)(t, f.root, f.slug)
	f.tick(t)
	if f.bareHead(t) != amended {
		t.Fatalf("rewritten branch not force-pushed; state = %+v", f.state(t))
	}
}

func TestGitHubMirrorEscalationCommentsOnce(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	writeEng(1)(t, f.root, f.slug)
	for r := 1; r <= MaxReviewerRounds; r++ {
		if r > 1 {
			writeEng(r)(t, f.root, f.slug)
		}
		mustWriteReview(t, f.root, f.slug, task.ReviewerA, r, task.Review{Verdict: task.VerdictRequestChanges, Summary: "no"})
	}
	if snap := f.tick(t); snap.Phase != PhaseEscalated {
		t.Fatalf("phase = %s", snap.Phase)
	}
	f.tick(t)
	n := 0
	for _, c := range f.calls(t) {
		if c == "pr ready 12" {
			t.Fatal("escalated PR marked ready")
		}
		if c == "pr comment 12 --body-file -" {
			n++
		}
	}
	// Two engineer revision comments plus one escalation comment.
	if n != 3 || !strings.Contains(f.log(t), "Review #1 requested changes at round 3, the cap of 3") {
		t.Fatalf("comments = %d, log:\n%s", n, f.log(t))
	}
}

func TestGitHubMirrorRejectsSSHRemote(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	runGit(t, f.root, "remote", "set-url", "origin", "git@github.com:acme/widget.git")
	writeEng(1)(t, f.root, f.slug)
	f.tick(t)
	if s := f.state(t); !strings.Contains(s.LastError, "not https") || s.PRNumber != 0 {
		t.Fatalf("state = %+v", s)
	}
	if c := f.calls(t); c != nil {
		t.Fatalf("gh ran with an ssh remote: %v", c)
	}
}

func TestGitHubMirrorUsesFrontmatterBase(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	if err := os.WriteFile(filepath.Join(f.tasksDir, "demo.md"), []byte("---\nstatus: active\nslug: demo\nbase: wt/parent\nloop: role\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEng(1)(t, f.root, f.slug)
	f.tick(t)
	c := f.calls(t)
	if len(c) != 2 || !strings.HasPrefix(c[1], "pr create --draft --base wt/parent --head wt/demo --title demo ") {
		t.Fatalf("calls = %q", c)
	}
}

// TestDriveRoleLoopPRFlowOffUnchanged pins the files-only behaviour
// with pr_flow off: plain done notice, no github.json, no gh call.
func TestDriveRoleLoopPRFlowOffUnchanged(t *testing.T) {
	for _, flow := range []string{"off", "draft"} {
		t.Run(flow, func(t *testing.T) {
			f := newMirrorFixture(t, flow)
			writeEng(1)(t, f.root, f.slug)
			mustWriteReview(t, f.root, f.slug, task.ReviewerA, 1, task.Review{Verdict: task.VerdictApprove, Summary: "ok"})
			mustWriteReview(t, f.root, f.slug, task.ReviewerB, 1, task.Review{Verdict: task.VerdictApprove, Summary: "ok"})
			for range 2 {
				if _, err := DriveRoleLoop(f.root, f.tasksDir, f.slug); err != nil {
					t.Fatal(err)
				}
			}
			envs := readInboxEnvelopes(t, f.inbox)
			_, statErr := os.Stat(githubStatePath(f.root, f.slug))
			if flow == "off" {
				if len(envs) != 1 || envs[0].Body != "demo: done" || !os.IsNotExist(statErr) || f.calls(t) != nil {
					t.Fatalf("off: envs=%+v stat=%v calls=%v", envs, statErr, f.calls(t))
				}
				return
			}
			if len(envs) != 1 || !strings.HasPrefix(envs[0].Body, "demo: done, PR #12 ready") {
				t.Fatalf("draft: envs=%+v", envs)
			}
		})
	}
}

func TestMirrorEventsOrder(t *testing.T) {
	root := t.TempDir()
	slug := "demo"
	writeEng(1)(t, root, slug)
	mustWriteReview(t, root, slug, task.ReviewerA, 1, task.Review{Verdict: task.VerdictRequestChanges, Summary: "x"})
	writeEng(2)(t, root, slug)
	mustWriteReview(t, root, slug, task.ReviewerA, 2, task.Review{Verdict: task.VerdictApprove, Summary: "x"})
	mustWriteReview(t, root, slug, task.ReviewerB, 1, task.Review{Verdict: task.VerdictRequestChanges, Summary: "x"})
	snap, err := DeriveSnapshot(root, slug)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := mirrorEvents(root, slug, snap)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range evs {
		names = append(names, e.name)
	}
	want := []string{"engineer-round-1.json", "A/round-1.json", "engineer-round-2.json", "A/round-2.json", "B/round-1.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("events = %v, want %v", names, want)
	}
}

func TestEngineerCommitIdentityEnv(t *testing.T) {
	f := newMirrorFixture(t, "draft")
	args, err := EngineerSpec(f.root, f.slug).TmuxArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, kv := range []string{"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com", "GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com"} {
		if !strings.Contains(joined, "\n"+kv+"\n") {
			t.Errorf("engineer args missing %s", kv)
		}
	}
	rargs, err := ReviewerSpec(f.root, f.slug, task.ReviewerA).TmuxArgs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rargs, "\n"), "GIT_AUTHOR_NAME") {
		t.Error("reviewer got commit identity")
	}

	off := newMirrorFixture(t, "off")
	args, err = EngineerSpec(off.root, off.slug).TmuxArgs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, "\n"), "GIT_AUTHOR_NAME") {
		t.Error("pr_flow off engineer got commit identity")
	}
}
