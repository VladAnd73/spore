package ghapp

import (
	"fmt"
	"strconv"
	"strings"
)

// credentialHelper answers git's `get` with the app token from the
// environment, so the token never appears in argv or a config file.
const credentialHelper = `!f() { test "$1" = get || return 0; echo username=x-access-token; echo "password=$GH_TOKEN"; }; f`

// Env returns base with GH_TOKEN and GH_ENTERPRISE_TOKEN (which gh
// reads instead for a GitHub Enterprise Server host) set to token, and
// a git credential helper injected through GIT_CONFIG_COUNT /
// GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n, so both `gh` and `git push` over https act as
// the app. Existing GIT_CONFIG_* entries in base are kept; ours are
// appended after them. The empty helper first clears any inherited
// helper (the operator's own gh or store credentials).
func Env(base []string, token string) []string {
	out := make([]string, 0, len(base)+7)
	count := 0
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "GH_TOKEN", "GH_ENTERPRISE_TOKEN":
			continue
		case "GIT_CONFIG_COUNT":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				count = n
			}
			continue
		}
		out = append(out, kv)
	}
	add := func(key, value string) {
		out = append(out,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", count, key),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count, value))
		count++
	}
	add("credential.helper", "")
	add("credential.helper", credentialHelper)
	out = append(out, "GH_TOKEN="+token, "GH_ENTERPRISE_TOKEN="+token, "GIT_CONFIG_COUNT="+strconv.Itoa(count))
	return out
}

// CheckHTTPSRemote rejects a remote URL the app token cannot push to.
// An ssh remote would push as the operator's own key, not the app.
func CheckHTTPSRemote(url string) error {
	if strings.HasPrefix(url, "https://") {
		return nil
	}
	return fmt.Errorf("ghapp: remote %q is not https; the GitHub App pushes over https only (an ssh remote would push as your own key). Switch it: git remote set-url origin https://github.com/<owner>/<repo>.git", url)
}
