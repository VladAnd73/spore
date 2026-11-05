// Package ghapp authenticates as a GitHub App installation: it reads
// the host-side github.toml, mints and caches installation tokens, and
// builds the env that makes `gh` and `git push` act as the app.
package ghapp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/sporetoml"
	"github.com/versality/spore/internal/task"
)

// PRFlowDraft turns the role-loop GitHub mirror on. Any other pr_flow
// value, including absent, keeps the files-only loop.
const PRFlowDraft = "draft"

// DefaultAPIURL is the GitHub REST root used when api_url is unset.
const DefaultAPIURL = "https://api.github.com"

// Config is the host-side github.toml. Zero value means "off".
type Config struct {
	PRFlow         string
	AppID          int64
	InstallationID int64
	PrivateKey     string
	OperatorLogin  string
	CommitName     string
	CommitEmail    string
	// APIURL overrides DefaultAPIURL (GitHub Enterprise, tests).
	APIURL string
}

// keys maps each github.toml key to whether its value is an integer.
var keys = map[string]bool{
	"pr_flow":         false,
	"app_id":          true,
	"installation_id": true,
	"private_key":     false,
	"operator_login":  false,
	"commit_name":     false,
	"commit_email":    false,
	"api_url":         false,
}

// Keys lists the accepted github.toml keys, sorted.
func Keys() []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Enabled reports whether the draft-PR mirror is on.
func (c Config) Enabled() bool { return c.PRFlow == PRFlowDraft }

// API returns the REST root without a trailing slash.
func (c Config) API() string {
	if c.APIURL == "" {
		return DefaultAPIURL
	}
	return strings.TrimRight(c.APIURL, "/")
}

// Validate checks the keys an enabled flow needs to mint a token and
// attribute commits.
func (c Config) Validate() error {
	var missing []string
	if c.AppID == 0 {
		missing = append(missing, "app_id")
	}
	if c.InstallationID == 0 {
		missing = append(missing, "installation_id")
	}
	if c.PrivateKey == "" {
		missing = append(missing, "private_key")
	}
	if c.Enabled() {
		for k, v := range map[string]string{
			"operator_login": c.OperatorLogin,
			"commit_name":    c.CommitName,
			"commit_email":   c.CommitEmail,
		} {
			if v == "" {
				missing = append(missing, k)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("github.toml: missing %s", strings.Join(missing, ", "))
}

// PrivateKeyPath expands a leading "~/" against $HOME.
func (c Config) PrivateKeyPath() string {
	return expandHome(c.PrivateKey)
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home := os.Getenv("HOME"); home != "" {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// ConfigPath is <StateDirForProject>/github.toml. It lives outside the
// project tree: app ids and key paths are per teammate, never in the
// consumer repo.
func ConfigPath(projectRoot string) (string, error) {
	dir, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "github.toml"), nil
}

// LoadConfig reads github.toml for projectRoot. A missing file is the
// zero Config (flow off), not an error.
func LoadConfig(projectRoot string) (Config, error) {
	path, err := ConfigPath(projectRoot)
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("ghapp: read %s: %w", path, err)
	}
	cfg, err := ParseConfig(string(b))
	if err != nil {
		return Config{}, fmt.Errorf("ghapp: %s: %w", path, err)
	}
	return cfg, nil
}

// ParseConfig reads the top-level keys of a github.toml body. Unknown
// keys and non-integer ids are errors so a typo surfaces loudly.
func ParseConfig(content string) (Config, error) {
	entries, err := sporetoml.ParseSection(content, "")
	if err != nil {
		return Config{}, err
	}
	var c Config
	for _, e := range entries {
		isInt, ok := keys[e.Key]
		if !ok {
			return Config{}, fmt.Errorf("line %d: unknown key %q (want one of %s)", e.Line, e.Key, strings.Join(Keys(), ", "))
		}
		var n int64
		if isInt {
			n, err = strconv.ParseInt(e.Value, 10, 64)
			if err != nil || n <= 0 {
				return Config{}, fmt.Errorf("line %d: %s: want a positive integer, got %q", e.Line, e.Key, e.Value)
			}
		}
		switch e.Key {
		case "pr_flow":
			if e.Value != PRFlowDraft && e.Value != "off" && e.Value != "" {
				return Config{}, fmt.Errorf("line %d: pr_flow: want %q or \"off\", got %q", e.Line, PRFlowDraft, e.Value)
			}
			c.PRFlow = e.Value
		case "app_id":
			c.AppID = n
		case "installation_id":
			c.InstallationID = n
		case "private_key":
			c.PrivateKey = e.Value
		case "operator_login":
			c.OperatorLogin = e.Value
		case "commit_name":
			c.CommitName = e.Value
		case "commit_email":
			c.CommitEmail = e.Value
		case "api_url":
			c.APIURL = e.Value
		}
	}
	return c, nil
}

// SetConfigKey writes key = value into github.toml (0600), creating the
// file and its directory. An empty value deletes the key. The result is
// re-parsed first, so an invalid value never lands on disk.
func SetConfigKey(projectRoot, key, value string) (string, error) {
	isInt, ok := keys[key]
	if !ok {
		return "", fmt.Errorf("ghapp: unknown key %q (want one of %s)", key, strings.Join(Keys(), ", "))
	}
	path, err := ConfigPath(projectRoot)
	if err != nil {
		return "", err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("ghapp: read %s: %w", path, err)
	}
	var content string
	switch {
	case value == "":
		content = sporetoml.DeleteKey(string(old), "", key)
	case isInt:
		content = sporetoml.SetKeyRaw(string(old), "", key, value)
	default:
		content = sporetoml.SetKey(string(old), "", key, value)
	}
	if _, err := ParseConfig(content); err != nil {
		return "", fmt.Errorf("ghapp: %w", err)
	}
	if err := writePrivate(path, []byte(content)); err != nil {
		return "", err
	}
	return path, nil
}

// writePrivate replaces path via a 0600 temp file and rename.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
