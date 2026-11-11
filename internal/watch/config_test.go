package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWatchToml(t *testing.T, dir, project, body string) {
	t.Helper()
	p := filepath.Join(dir, "spore", project)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "watch.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The CLI told an operator whose project was disabled where to enable
// it by recomputing this path itself (cmd/spore's watchTomlPath). A
// second, independent copy of this resolution drifting from the one
// readWatchToml actually uses would send an operator to edit a file
// nothing reads.
func TestTomlPathMatchesWhereLoadDreamsConfigReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := TomlPath("proj")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[dreams]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatalf("LoadDreamsConfig did not read the file TomlPath named: %s", path)
	}
}

func TestLoadDreamsConfigReadsTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true

[dreams]
enabled = true
deep_read_cap = 5
max_writes_per_run = 20
recurrence_threshold = 3
`)
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true, DeepReadCap: 5, MaxWritesPerRun: 20, RecurrenceThreshold: 3}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestLoadDreamsConfigDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if got.Enabled {
		t.Fatal("a missing [dreams] table must mean disabled")
	}
	want := DreamsConfig{DeepReadCap: 5, MaxWritesPerRun: 10, RecurrenceThreshold: 2}
	if got != want {
		t.Fatalf("defaults not applied: %+v, want %+v", got, want)
	}
}

func TestLoadDreamsConfigEnabledOnlyKeepsNumericDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", "[dreams]\nenabled = true\n")
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true, DeepReadCap: 5, MaxWritesPerRun: 10, RecurrenceThreshold: 2}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

// A cautious operator may quote the boolean, and TOML allows a trailing
// comment. Both are the same value as a bare true; neither may silently
// disable the feature.
func TestLoadDreamsConfigAcceptsQuotedAndCommentedEnabled(t *testing.T) {
	for _, line := range []string{
		`enabled = "true"`,
		`enabled = 'true'`,
		`enabled = true # nightly`,
		`enabled=true`,
		`enabled =    true`,
	} {
		t.Run(line, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeWatchToml(t, dir, "proj", "[dreams]\n"+line+"\n")
			got, err := LoadDreamsConfig("proj")
			if err != nil {
				t.Fatal(err)
			}
			if !got.Enabled {
				t.Fatalf("%s must enable dreaming, got %+v", line, got)
			}
		})
	}
}

// A typo in a knob must surface, not fall back to a default. An unattended
// feature that writes to memory files gives the operator no other signal.
func TestLoadDreamsConfigRejectsUnparseableAndNegativeKnobs(t *testing.T) {
	for _, body := range []string{
		"[dreams]\nenabled = true\ndeep_read_cap = banana\n",
		"[dreams]\nenabled = true\ndeep_read_cap = 3.5\n",
		"[dreams]\nenabled = true\ndeep_read_cap = -1\n",
		"[dreams]\nenabled = true\nmax_writes_per_run = -10\n",
		"[dreams]\nenabled = true\nrecurrence_threshold = lots\n",
	} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeWatchToml(t, dir, "proj", body)
			_, err := LoadDreamsConfig("proj")
			if err == nil {
				t.Fatalf("want an error for %q", body)
			}
			if !strings.Contains(err.Error(), "dreams") {
				t.Fatalf("error %q must name the table", err)
			}
		})
	}
}

// Zero is a legal setting, not a typo: it means "do none of this".
func TestLoadDreamsConfigAcceptsZeroKnobs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
[dreams]
enabled = true
deep_read_cap = 0
max_writes_per_run = 0
recurrence_threshold = 0
`)
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}
