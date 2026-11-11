package watch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DreamsConfig is the [dreams] table: whether nightly dreaming runs for this
// project, and the three knobs that bound one run.
type DreamsConfig struct {
	Enabled             bool
	DeepReadCap         int
	MaxWritesPerRun     int
	RecurrenceThreshold int
}

// LoadDreamsConfig reads the [dreams] table. A missing file or absent section
// means disabled; the numeric knobs still carry their defaults so a table that
// only sets enabled behaves sensibly. An unparseable or negative knob is an
// error rather than a silent fallback: nothing else tells an operator that
// their typo left an unattended feature running on defaults.
func LoadDreamsConfig(project string) (DreamsConfig, error) {
	sections, err := readWatchToml(project)
	if err != nil {
		return DreamsConfig{}, err
	}
	d := sections["dreams"]
	cfg := DreamsConfig{Enabled: parseScalarString(d["enabled"]) == "true"}
	for _, knob := range []struct {
		key string
		def int
		out *int
	}{
		// deep_read_cap must default to dream.DefaultDeepReadCap. Not
		// imported, because watch would then pull the whole dream
		// package and its embedded briefs into every build that reads
		// a config; dream's own test pins the two numbers together.
		{"deep_read_cap", 5, &cfg.DeepReadCap},
		{"max_writes_per_run", 10, &cfg.MaxWritesPerRun},
		{"recurrence_threshold", 2, &cfg.RecurrenceThreshold},
	} {
		n, err := parseIntDefault(d[knob.key], knob.def)
		if err != nil {
			return DreamsConfig{}, fmt.Errorf("[dreams] %s: %w", knob.key, err)
		}
		*knob.out = n
	}
	return cfg, nil
}

// parseIntDefault returns def for an absent key. Zero is a legal setting
// ("do none of this"); a negative or unparseable value is a typo.
func parseIntDefault(val string, def int) (int, error) {
	val = parseScalarString(val)
	if val == "" {
		return def, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("want integer, got %q", val)
	}
	if n < 0 {
		return 0, fmt.Errorf("must be >= 0, got %d", n)
	}
	return n, nil
}

// TomlPath is where a project's watch.toml lives. Callers that need to
// tell an operator which file to edit (a disabled feature's message, an
// error) call this rather than recomputing $XDG_CONFIG_HOME resolution
// themselves, so the path they name is guaranteed to be the one
// readWatchToml actually reads.
func TomlPath(project string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "spore", project, "watch.toml")
}

// readWatchToml parses watch.toml into a section -> key -> raw-value map. The
// anonymous top-level section is keyed by "". A missing file yields an empty
// (non-nil) map with no error.
func readWatchToml(project string) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{"": {}}
	b, err := os.ReadFile(TomlPath(project))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sections, nil
		}
		return nil, err
	}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if sections[section] == nil {
				sections[section] = map[string]string{}
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		sections[section][key] = val
	}
	return sections, nil
}

func parseScalarString(val string) string {
	return strings.Trim(strings.TrimSpace(val), `"'`)
}

func stripTOMLComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}
