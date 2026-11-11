package evalharness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/dream"
)

// reviewerFixtureDir returns the embedded fixture directory for a named
// reviewer scenario (e.g. "fabricated-citation" ->
// fixtures/dream/reviewer/fabricated-citation), which holds packet.json
// and a repo/ subtree the packet's evidence pointers resolve against.
func reviewerFixtureDir(name string) string {
	return "fixtures/dream/reviewer/" + name
}

// BuildReviewerRun reproduces the exactly-three-things prompt
// proposer.md's own reviewer-spawn step builds: the reviewer brief
// text, one packet's JSON, and the packet's target path, and nothing
// else. It copies the fixture's repo/ subtree into sb.RunWD so a
// reviewer's re-derivation (open the file the evidence points at, read
// the real line) has real content to check against inside the sandbox,
// never the real spore checkout. It returns the prompt and the working
// directory a spawned reviewer should run in, which is also where
// verdicts/<n>.json is expected to land.
func BuildReviewerRun(sb *Sandbox, fixture, briefOverride string) (prompt, runWD string, err error) {
	dir := reviewerFixtureDir(fixture)

	if err := copyFixtureRepo(dir+"/repo", sb.RunWD); err != nil {
		return "", "", fmt.Errorf("evalharness: reviewer fixture %q: %w", fixture, err)
	}

	rawPacket, err := FixturesFS.ReadFile(dir + "/packet.json")
	if err != nil {
		return "", "", fmt.Errorf("evalharness: reviewer fixture %q: %w", fixture, err)
	}
	packetJSON := strings.ReplaceAll(string(rawPacket), "__SANDBOX__", sb.RunWD)

	var packet dream.Packet
	if err := json.Unmarshal([]byte(packetJSON), &packet); err != nil {
		return "", "", fmt.Errorf("evalharness: reviewer fixture %q: packet.json: %w", fixture, err)
	}
	if packet.Target == "" {
		return "", "", fmt.Errorf("evalharness: reviewer fixture %q: packet.json has no target", fixture)
	}

	brief := dream.ReviewerBrief
	if briefOverride != "" {
		brief = briefOverride
	}

	var b strings.Builder
	b.WriteString(brief)
	b.WriteString("\n\n---\n\n## Packet JSON\n\n```json\n")
	b.WriteString(packetJSON)
	b.WriteString("\n```\n\n## Target path\n\n")
	b.WriteString(packet.Target)
	b.WriteString("\n")
	return b.String(), sb.RunWD, nil
}

// copyFixtureRepo copies every file under src (an embed.FS directory)
// into dst on the real filesystem, preserving relative paths.
func copyFixtureRepo(src, dst string) error {
	return fs.WalkDir(FixturesFS, src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := FixturesFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
