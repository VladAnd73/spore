package dream

import (
	"testing"

	"github.com/versality/spore/internal/watch"
)

// The CLI reads [dreams] deep_read_cap and passes it straight into
// Options.DeepReadCap, so the number a caller gets when it names no cap
// and the number a config gets when it sets no key have to be the same.
// They were not: this package said 5 and internal/watch said 3, which
// made the constant here unreachable from the only caller that matters
// and the doc comment on it wrong. This test is the thing that stops
// that recurring; the constant on its own does not.
//
// internal/watch is imported here in a test file only. The dream package
// itself must not depend on it: watch is the config reader, dream is the
// engine, and the CLI is what joins them.
func TestDefaultDeepReadCapMatchesTheWatchConfigDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := watch.LoadDreamsConfig("a-project-with-no-watch-toml")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.DeepReadCap != DefaultDeepReadCap {
		t.Fatalf("[dreams] deep_read_cap defaults to %d but dream.DefaultDeepReadCap is %d; "+
			"the CLI passes the config value through, so these have to be one number",
			cfg.DeepReadCap, DefaultDeepReadCap)
	}
}
