package tokenmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsCoordinator(t *testing.T) {
	cases := []struct {
		inbox    string
		stateDir string
		want     bool
	}{
		{"/state/coord", "/state/coord", true},
		{"/state/coord/some/inbox", "/state/coord", true},
		{"/state/workers/slug/inbox", "/state/coord", false},
		{"", "/state/coord", false},
	}
	for _, tc := range cases {
		cfg := Config{Inbox: tc.inbox, CoordinatorStateDir: tc.stateDir}
		if got := cfg.IsCoordinator(); got != tc.want {
			t.Errorf("IsCoordinator(%q, %q) = %v, want %v", tc.inbox, tc.stateDir, got, tc.want)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		inbox string
		want  string
	}{
		{"/state/workers/my-slug/inbox", "my-slug"},
		{"/anything/abc/inbox", "abc"},
		{"", ""},
		{"/", ""},
	}
	for _, tc := range cases {
		cfg := Config{Inbox: tc.inbox}
		if got := cfg.Slug(); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.inbox, got, tc.want)
		}
	}
}

func TestWrapCap(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"override beats tier", Config{WrapOverride: 99000, Tier: "max"}, 99000},
		{"max tier", Config{Tier: "max"}, DefaultWrapMax},
		{"sub tier", Config{Tier: "pro"}, DefaultWrapSub},
		{"unknown tier", Config{Tier: ""}, DefaultWrapSub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg.Defaults()
			if got := cfg.WrapCap(); got != tc.want {
				t.Errorf("WrapCap = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestForceCap(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"override beats tier", Config{ForceOverride: 99000, Tier: "max"}, 99000},
		{"max tier", Config{Tier: "max"}, DefaultForceMax},
		{"sub tier", Config{Tier: "pro"}, DefaultForceSub},
		{"unknown tier", Config{Tier: ""}, DefaultForceSub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg.Defaults()
			if got := cfg.ForceCap(); got != tc.want {
				t.Errorf("ForceCap = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSoftCap(t *testing.T) {
	if got := (Config{Tier: "max"}).Defaults().SoftCap(); got != 250000 {
		t.Errorf("max SoftCap = %d, want 250000", got)
	}
	if got := (Config{Tier: "pro"}).Defaults().SoftCap(); got != 90000 {
		t.Errorf("sub SoftCap = %d, want 90000", got)
	}
}

func TestCheckSkipsCoordinator(t *testing.T) {
	stateDir := t.TempDir()
	cfg := Config{
		Inbox:               filepath.Join(stateDir, "myproject", "inbox"),
		CoordinatorStateDir: stateDir,
	}
	if got := Check(cfg, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for coordinator inbox, got %s", got.Level)
	}
}

func TestCheckSkipsEmptyInbox(t *testing.T) {
	if got := Check(Config{}, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for empty inbox, got %s", got.Level)
	}
}

func TestCheckSkipsBadInboxLayout(t *testing.T) {
	cfg := Config{
		Inbox:               "/inbox",
		CoordinatorStateDir: "/never",
	}
	if got := Check(cfg, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for bad inbox layout, got %s", got.Level)
	}
}

func TestCheckOk(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "test-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if got.Level != "ok" {
		t.Errorf("Level = %s, want ok", got.Level)
	}
	if got.ShouldFire {
		t.Error("ShouldFire = true, want false")
	}
	if got.Slug != "test-slug" {
		t.Errorf("Slug = %q, want %q", got.Slug, "test-slug")
	}
}

func TestCheckWrapMax(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":290000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "wrap-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true")
	}
	if got.WrapCap != DefaultWrapMax {
		t.Errorf("WrapCap = %d, want %d", got.WrapCap, DefaultWrapMax)
	}
	if !strings.Contains(got.Message, "wrap-slug") {
		t.Errorf("Message missing slug: %q", got.Message)
	}
	if !strings.Contains(got.Message, "tier=max") {
		t.Errorf("Message missing tier: %q", got.Message)
	}
}

func TestCheckWrapSubTier(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":121000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "sub-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "pro",
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != DefaultWrapSub {
		t.Errorf("WrapCap = %d, want %d", got.WrapCap, DefaultWrapSub)
	}
	if !strings.Contains(got.Message, "tier=pro") {
		t.Errorf("Message missing tier: %q", got.Message)
	}
}

func TestCheckWrapUnknownTierUsesSub(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":121000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "u", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != DefaultWrapSub {
		t.Errorf("WrapCap = %d, want %d (unknown tier should default to sub)", got.WrapCap, DefaultWrapSub)
	}
	if !strings.Contains(got.Message, "tier=unknown") {
		t.Errorf("Message tier rendering: %q", got.Message)
	}
}

func TestCheckWrapOverride(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "tiny", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
		WrapOverride:        40000,
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != 40000 {
		t.Errorf("WrapCap = %d, want 40000", got.WrapCap)
	}
}

func TestCheckWrapFinishUnitEveryStop(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":290000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "fu-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Fatalf("Level = %s, want wrap", got.Level)
	}
	if !strings.Contains(got.Message, "Do not start new investigations") ||
		!strings.Contains(got.Message, "Finish the unit currently in flight") {
		t.Errorf("wrap message must carry finish-unit semantics, got:\n%s", got.Message)
	}
	if !strings.Contains(got.Message, `tmux kill-session -t "$(tmux display-message -p '#S')"`) {
		t.Errorf("wrap message must keep the kill-session command, got:\n%s", got.Message)
	}
	again := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if !again.ShouldFire || again.Level != "wrap" {
		t.Errorf("wrap must fire on every Stop, got level=%s fire=%v", again.Level, again.ShouldFire)
	}
}

func TestCheckForceMax(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":301000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "force-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if got.Level != "force" {
		t.Fatalf("Level = %s, want force", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true")
	}
	if got.ForceCap != DefaultForceMax {
		t.Errorf("ForceCap = %d, want %d", got.ForceCap, DefaultForceMax)
	}
	if !strings.Contains(got.Message, "regardless of") {
		t.Errorf("force message must demand an unconditional wrap, got:\n%s", got.Message)
	}
	again := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if !again.ShouldFire || again.Level != "force" {
		t.Errorf("force must fire on every Stop, got level=%s fire=%v", again.Level, again.ShouldFire)
	}
}

func TestCheckForceSubTier(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":141000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "fsub", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "pro",
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "force" {
		t.Fatalf("Level = %s, want force", got.Level)
	}
	if got.ForceCap != DefaultForceSub {
		t.Errorf("ForceCap = %d, want %d", got.ForceCap, DefaultForceSub)
	}
}

func TestCheckSoftWarnOncePerSession(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":251000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inbox := filepath.Join(dir, "workers", "soft-slug", "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Inbox:               inbox,
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{SessionID: "soft-sid", TranscriptPath: transcriptFile})
	if got.Level != "soft" {
		t.Fatalf("Level = %s, want soft", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true on first soft crossing")
	}
	if got.SoftCap != 250000 {
		t.Errorf("SoftCap = %d, want 250000", got.SoftCap)
	}
	if !strings.Contains(got.Message, "next natural break") {
		t.Errorf("soft message must suggest the next natural break, got:\n%s", got.Message)
	}

	marker := filepath.Join(dir, "workers", "soft-slug", "token-monitor", "soft-sid.soft")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected soft marker at %s: %v", marker, err)
	}

	again := Check(cfg, HookPayload{SessionID: "soft-sid", TranscriptPath: transcriptFile})
	if again.Level != "ok" || again.ShouldFire {
		t.Errorf("second soft check: level=%s fire=%v, want ok/false", again.Level, again.ShouldFire)
	}
}

func TestCheckTinyOverrideDisablesSoft(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":10000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "tinyo", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		WrapOverride:        20000,
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if got.Level != "ok" || got.ShouldFire {
		t.Errorf("non-positive soft cap must disable the soft band, got level=%s fire=%v", got.Level, got.ShouldFire)
	}
}

func TestIsRolePane(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{"engineer", true},
		{"reviewer", true},
		{"", false},
		{"qa", false},
	}
	for _, tc := range cases {
		if got := (Config{Role: tc.role}).IsRolePane(); got != tc.want {
			t.Errorf("IsRolePane(%q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

// writeTranscript drops a one-line transcript claiming n context
// tokens and returns its path.
func writeTranscript(t *testing.T, n string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":` + n + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckRolePaneWrapEngineer(t *testing.T) {
	tpath := writeTranscript(t, "290000")
	cfg := Config{
		Role:     "engineer",
		RoleSlug: "eng-slug",
		TaskDir:  filepath.Join(t.TempDir(), ".spore", "eng-slug"),
		Tier:     "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: tpath})
	if got.Level != "wrap" {
		t.Fatalf("Level = %s, want wrap", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true")
	}
	if got.Slug != "eng-slug" {
		t.Errorf("Slug = %q, want eng-slug", got.Slug)
	}
	for _, want := range []string{
		"ROLE TOKEN MONITOR (finish-unit)",
		"Finish the unit currently in flight",
		"wt/eng-slug",
		"responses/engineer-round-N.json",
		"respawns this phase's pane",
		`tmux kill-session -t "$(tmux display-message -p '#S')"`,
		"tier=max",
	} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("wrap message missing %q:\n%s", want, got.Message)
		}
	}
	if strings.Contains(got.Message, "tasks/") {
		t.Errorf("role message must not point at tasks/<slug>.md:\n%s", got.Message)
	}
	again := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: tpath})
	if !again.ShouldFire || again.Level != "wrap" {
		t.Errorf("wrap must fire on every Stop, got level=%s fire=%v", again.Level, again.ShouldFire)
	}
}

func TestCheckRolePaneForceReviewer(t *testing.T) {
	tpath := writeTranscript(t, "301000")
	cfg := Config{
		Role:             "reviewer",
		ReviewerInstance: "B",
		RoleSlug:         "rev-slug",
		Tier:             "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: tpath})
	if got.Level != "force" {
		t.Fatalf("Level = %s, want force", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true")
	}
	for _, want := range []string{
		"ROLE TOKEN MONITOR (force)",
		"regardless of what is in flight",
		"reviews/B/round-N.json",
		"respawns this phase's pane",
	} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("force message missing %q:\n%s", want, got.Message)
		}
	}
	if strings.Contains(got.Message, "Commit in-flight work") {
		t.Errorf("reviewer message must not carry the engineer commit step:\n%s", got.Message)
	}
	if strings.Contains(got.Message, "tasks/") {
		t.Errorf("role message must not point at tasks/<slug>.md:\n%s", got.Message)
	}
}

func TestCheckRolePaneSubTierCaps(t *testing.T) {
	tpath := writeTranscript(t, "121000")
	cfg := Config{Role: "engineer", RoleSlug: "s1", Tier: "pro"}
	got := Check(cfg, HookPayload{TranscriptPath: tpath})
	if got.Level != "wrap" {
		t.Fatalf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != DefaultWrapSub {
		t.Errorf("WrapCap = %d, want %d", got.WrapCap, DefaultWrapSub)
	}
}

func TestCheckRolePaneSoftOncePerSession(t *testing.T) {
	tpath := writeTranscript(t, "251000")
	taskDir := filepath.Join(t.TempDir(), ".spore", "soft-slug")
	cfg := Config{Role: "engineer", RoleSlug: "soft-slug", TaskDir: taskDir, Tier: "max"}

	got := Check(cfg, HookPayload{SessionID: "soft-sid", TranscriptPath: tpath})
	if got.Level != "soft" {
		t.Fatalf("Level = %s, want soft", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true on first soft crossing")
	}
	if !strings.Contains(got.Message, "next natural break") {
		t.Errorf("soft message must suggest the next natural break, got:\n%s", got.Message)
	}

	marker := filepath.Join(taskDir, "state", "token-monitor", "soft-sid.soft")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected soft marker at %s: %v", marker, err)
	}

	again := Check(cfg, HookPayload{SessionID: "soft-sid", TranscriptPath: tpath})
	if again.Level != "ok" || again.ShouldFire {
		t.Errorf("second soft check: level=%s fire=%v, want ok/false", again.Level, again.ShouldFire)
	}
}

func TestCheckRolePaneNoTaskDirDisablesSoft(t *testing.T) {
	tpath := writeTranscript(t, "251000")
	cfg := Config{Role: "engineer", RoleSlug: "nodir", Tier: "max"}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: tpath})
	if got.Level != "ok" || got.ShouldFire {
		t.Errorf("soft without a task dir: level=%s fire=%v, want ok/false", got.Level, got.ShouldFire)
	}
}

func TestCheckRolePaneSlugFromTaskDir(t *testing.T) {
	tpath := writeTranscript(t, "50000")
	cfg := Config{Role: "engineer", TaskDir: "/proj/.spore/derived-slug", Tier: "max"}
	got := Check(cfg, HookPayload{TranscriptPath: tpath})
	if got.Slug != "derived-slug" {
		t.Errorf("Slug = %q, want derived-slug", got.Slug)
	}
}

func TestCheckRolePaneMissingSlugSkips(t *testing.T) {
	cfg := Config{Role: "engineer"}
	if got := Check(cfg, HookPayload{}); got.Level != "skip" {
		t.Errorf("Level = %s, want skip without slug", got.Level)
	}
}

func TestCheckRolePanePrecedesInheritedInbox(t *testing.T) {
	tpath := writeTranscript(t, "290000")
	stateDir := t.TempDir()
	cfg := Config{
		Role:                "engineer",
		RoleSlug:            "inherit-slug",
		Tier:                "max",
		Inbox:               filepath.Join(stateDir, "someproject", "inbox"),
		CoordinatorStateDir: stateDir,
	}
	got := Check(cfg, HookPayload{TranscriptPath: tpath})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap (role path must win over inherited inbox)", got.Level)
	}
	if !strings.Contains(got.Message, "ROLE TOKEN MONITOR") {
		t.Errorf("expected role message, got:\n%s", got.Message)
	}
}

func TestCheckMissingTranscript(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
	}
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_PROJECT_DIR", "/no/such")
	got := Check(cfg, HookPayload{})
	if got.Level != "skip" {
		t.Errorf("Level = %s, want skip", got.Level)
	}
}
