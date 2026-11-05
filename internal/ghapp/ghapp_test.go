package ghapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func genKey(t *testing.T, dir string, pkcs8 bool) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}
	path := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return key, path
}

func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	enc := base64.RawURLEncoding
	hdr, _ := enc.DecodeString(parts[0])
	if string(hdr) != `{"alg":"RS256","typ":"JWT"}` {
		t.Fatalf("header = %s", hdr)
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	raw, _ := enc.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestSignJWT(t *testing.T) {
	for _, pkcs8 := range []bool{false, true} {
		key, path := genKey(t, t.TempDir(), pkcs8)
		b, _ := os.ReadFile(path)
		parsed, err := ParsePrivateKey(b)
		if err != nil {
			t.Fatalf("pkcs8=%v: %v", pkcs8, err)
		}
		now := time.Unix(1_700_000_000, 0)
		jwt, err := SignJWT(42, parsed, now)
		if err != nil {
			t.Fatal(err)
		}
		c := verifyJWT(t, jwt, &key.PublicKey)
		if c["iss"] != "42" || c["iat"].(float64) != float64(now.Unix()-60) || c["exp"].(float64) != float64(now.Unix()+540) {
			t.Fatalf("claims = %v", c)
		}
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := ParsePrivateKey([]byte("nope")); err == nil {
		t.Fatal("want error")
	}
}

type tokenServer struct {
	*httptest.Server
	calls atomic.Int32
	exp   time.Time
}

func newTokenServer(t *testing.T, pub *rsa.PublicKey, exp time.Time) *tokenServer {
	ts := &tokenServer{exp: exp}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/77/access_tokens" {
			http.Error(w, "bad route "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "no bearer", http.StatusUnauthorized)
			return
		}
		verifyJWT(t, jwt, pub)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_test" + string(rune('0'+ts.calls.Load())),
			"expires_at": ts.exp.UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestMintAndCache(t *testing.T) {
	dir := t.TempDir()
	key, path := genKey(t, dir, false)
	now := time.Now()
	ts := newTokenServer(t, &key.PublicKey, now.Add(time.Hour))
	cfg := Config{AppID: 1, InstallationID: 77, PrivateKey: path, APIURL: ts.URL + "/"}
	clock := now
	m := Minter{Config: cfg, Now: func() time.Time { return clock }}
	cache := filepath.Join(dir, "state", "github-token.json")

	tok, err := m.CachedToken(context.Background(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "ghs_test1" {
		t.Fatalf("token = %q", tok.Token)
	}
	if st, err := os.Stat(cache); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("cache stat = %v, %v", st, err)
	}

	tok, err = m.CachedToken(context.Background(), cache)
	if err != nil || tok.Token != "ghs_test1" || ts.calls.Load() != 1 {
		t.Fatalf("cached: tok=%q err=%v calls=%d", tok.Token, err, ts.calls.Load())
	}

	clock = now.Add(56 * time.Minute)
	tok, err = m.CachedToken(context.Background(), cache)
	if err != nil || tok.Token != "ghs_test2" || ts.calls.Load() != 2 {
		t.Fatalf("near expiry: tok=%q err=%v calls=%d", tok.Token, err, ts.calls.Load())
	}

	m.Config.AppID = 2
	if _, err := m.CachedToken(context.Background(), cache); err != nil || ts.calls.Load() != 3 {
		t.Fatalf("app change: err=%v calls=%d", err, ts.calls.Load())
	}
}

func TestMintErrors(t *testing.T) {
	dir := t.TempDir()
	_, path := genKey(t, dir, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := Minter{Config: Config{AppID: 1, InstallationID: 77, PrivateKey: path, APIURL: srv.URL}}.Mint(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("err = %v", err)
	}
	_, err = Minter{Config: Config{AppID: 1}}.Mint(context.Background())
	if err == nil || !strings.Contains(err.Error(), "installation_id, private_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig(`# host-side
pr_flow = "draft"
app_id = 123456
installation_id = 7890123
private_key = "~/.config/spore/p/github-app.pem"
operator_login = "octo"
commit_name = "Octo Cat"
commit_email = "octo@example.com"

[ignored]
x = 1
`)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled() || c.AppID != 123456 || c.InstallationID != 7890123 || c.OperatorLogin != "octo" || c.CommitName != "Octo Cat" {
		t.Fatalf("cfg = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/home/x")
	if got := c.PrivateKeyPath(); got != "/home/x/.config/spore/p/github-app.pem" {
		t.Fatalf("key path = %q", got)
	}
	if c.API() != DefaultAPIURL {
		t.Fatalf("api = %q", c.API())
	}
	for _, bad := range []string{`app_id = "x"`, `bogus = 1`, `pr_flow = "yes"`, `installation_id = -1`} {
		if _, err := ParseConfig(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	off, err := ParseConfig(`pr_flow = "off"`)
	if err != nil || off.Enabled() {
		t.Fatalf("off: %+v %v", off, err)
	}
	if err := (Config{PRFlow: PRFlowDraft, AppID: 1, InstallationID: 2, PrivateKey: "k"}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "commit_email, commit_name, operator_login") {
		t.Fatalf("validate = %v", err)
	}
}

func TestSetConfigKey(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"app_id", "5"}, {"pr_flow", "draft"}, {"commit_name", "A B"}, {"app_id", "6"}} {
		if _, err := SetConfigKey(root, kv[0], kv[1]); err != nil {
			t.Fatalf("%v: %v", kv, err)
		}
	}
	path := filepath.Join(state, "spore", "proj", "github.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "app_id = 6\npr_flow = \"draft\"\ncommit_name = \"A B\"\n"; string(b) != want {
		t.Fatalf("file = %q, want %q", b, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if _, err := SetConfigKey(root, "app_id", "x"); err == nil {
		t.Fatal("non-integer app_id accepted")
	}
	if _, err := SetConfigKey(root, "nope", "x"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := SetConfigKey(root, "commit_name", ""); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(root)
	if err != nil || c.AppID != 6 || c.CommitName != "" || !c.Enabled() {
		t.Fatalf("load = %+v %v", c, err)
	}
}

func TestLoadConfigMissingIsOff(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c, err := LoadConfig(t.TempDir())
	if err != nil || c.Enabled() || c != (Config{}) {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

func TestEnvShape(t *testing.T) {
	env := Env([]string{"PATH=/bin", "GH_TOKEN=old", "GH_ENTERPRISE_TOKEN=old", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=x"}, "tok")
	want := map[string]string{
		"PATH":                "/bin",
		"GH_TOKEN":            "tok",
		"GH_ENTERPRISE_TOKEN": "tok",
		"GIT_CONFIG_COUNT":    "3",
		"GIT_CONFIG_KEY_0":    "user.name",
		"GIT_CONFIG_KEY_1":    "credential.helper",
		"GIT_CONFIG_VALUE_1":  "",
		"GIT_CONFIG_KEY_2":    "credential.helper",
		"GIT_CONFIG_VALUE_2":  credentialHelper,
	}
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := got[k]; dup {
			t.Fatalf("duplicate %s", k)
		}
		got[k] = v
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestEnvGitCredential runs real git: the injected helper must answer
// a credential fill for an https remote with the app token.
func TestEnvGitCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	home := t.TempDir()
	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
	cmd := exec.Command("git", "credential", "fill")
	cmd.Env = Env(base, "ghs_secret")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=o/r.git\n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git credential fill: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "username=x-access-token\n") || !strings.Contains(string(out), "password=ghs_secret\n") {
		t.Fatalf("output = %s", out)
	}
}

func TestCheckHTTPSRemote(t *testing.T) {
	if err := CheckHTTPSRemote("https://github.com/o/r.git"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"git@github.com:o/r.git", "ssh://git@github.com/o/r.git", "/tmp/bare.git"} {
		if err := CheckHTTPSRemote(u); err == nil || !strings.Contains(err.Error(), "not https") {
			t.Errorf("%s: err = %v", u, err)
		}
	}
}
