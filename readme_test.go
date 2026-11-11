package spore

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var readmeSpore = regexp.MustCompile(`(?:^|&&|;|\|)\s*spore\s+([a-z][a-z-]*)(?:\s+([a-z][a-z-]*))?`)

func readmeShellBlocks(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case !in && (line == "```sh" || line == "```bash"):
			in, cur = true, nil
		case in && line == "```":
			in = false
			blocks = append(blocks, strings.Join(cur, "\n"))
		case in:
			cur = append(cur, line)
		}
	}
	if len(blocks) == 0 {
		t.Fatal("README has no sh blocks")
	}
	return blocks
}

func TestReadmeShellBlocksParse(t *testing.T) {
	for i, b := range readmeShellBlocks(t) {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(b)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("block %d does not parse: %v\n%s\n---\n%s", i, err, out, b)
		}
	}
}

func TestReadmeSporeCommandsExist(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the spore binary")
	}
	bin := filepath.Join(t.TempDir(), "spore")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/spore").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	help := func(args ...string) string {
		out, _ := exec.Command(bin, append(args, "--help")...).CombinedOutput()
		return string(out)
	}
	top := help()
	listed := func(text, word string) bool {
		return regexp.MustCompile(`(?m)^\s+(?:spore\s+\S+\s+)?` + regexp.QuoteMeta(word) + `\b`).MatchString(text)
	}
	for _, b := range readmeShellBlocks(t) {
		for _, line := range strings.Split(b, "\n") {
			line = strings.SplitN(line, "#", 2)[0]
			for _, m := range readmeSpore.FindAllStringSubmatch(line, -1) {
				if !listed(top, m[1]) {
					t.Errorf("README names `spore %s`, not in spore --help", m[1])
					continue
				}
				if m[2] == "" || m[1] == "infect" || m[1] == "lint" || m[1] == "bootstrap" {
					continue
				}
				if !listed(help(m[1]), m[2]) {
					t.Errorf("README names `spore %s %s`, not in spore %s --help", m[1], m[2], m[1])
				}
			}
		}
	}
}
