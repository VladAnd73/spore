package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/dream"
)

func TestTopLevelUsageNamesEveryDreamSubcommand(t *testing.T) {
	i := strings.Index(usage, "  dream ")
	if i < 0 {
		t.Fatal("usage has no dream entry")
	}
	entry := usage[i:]
	if j := strings.Index(entry, "\n  eval "); j >= 0 {
		entry = entry[:j]
	}
	for _, sub := range []string{"digest", "gate", "reviewer-brief", "write", "runs", "revert", "rewind"} {
		if !strings.Contains(entry, sub) {
			t.Errorf("spore --help dream entry does not name %q:\n%s", sub, entry)
		}
	}
}

// A skill is proposed, never installed: write's output must point at the
// proposal it wrote, not at the install path the packet names.
func TestDreamWritePrintsTheSkillProposalPathNotTheInstallPath(t *testing.T) {
	f := newDreamFixture(t)
	const runID = "20260902-skil"
	runDir, err := dream.RunDir(f.project, runID)
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(f.home, ".claude", "skills", "fetching-origin", "SKILL.md")
	dreamWrite(t, filepath.Join(runDir, "packets", "1.json"), `{
		"claim": "operator wants origin fetched before every push",
		"type": "operator-preference",
		"sessions": ["sesn-1"],
		"tier": "skill",
		"target": "`+strings.ReplaceAll(install, `\`, `\\`)+`",
		"text": "---\nname: fetching-origin\ndescription: fetch first\n---\n\nSteps.\n"
	}`)
	if code, out, errOut := f.run(t, "gate", runID, "--project", f.project, "--threshold", "1"); code != 0 {
		t.Fatalf("gate exit = %d\n%s%s", code, out, errOut)
	}
	dreamWrite(t, filepath.Join(runDir, "verdicts", "1.json"),
		`{"verdict":"confirmed","reason":"operator said so","proof":"session sesn-1"}`)

	code, out, errOut := f.run(t, "write", runID, "--project", f.project)
	if code != 0 {
		t.Fatalf("write exit = %d\n%s%s", code, out, errOut)
	}
	proposal := filepath.Join(runDir, "skill-proposals", "fetching-origin.md")
	if _, err := os.Stat(proposal); err != nil {
		t.Fatalf("proposal not written: %v", err)
	}
	for _, text := range []string{out, dreamRead(t, filepath.Join(runDir, "report.md"))} {
		if strings.Contains(text, install) {
			t.Errorf("output names the install path as if installed:\n%s", text)
		}
		if !strings.Contains(text, proposal) {
			t.Errorf("output does not name the proposal path %s:\n%s", proposal, text)
		}
	}
}
