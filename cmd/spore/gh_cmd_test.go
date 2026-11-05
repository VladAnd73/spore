package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/ghapp"
)

// ghTestProject points the CLI at a fresh project whose github.toml
// mints tokens from an httptest server, and chdirs into the project.
func ghTestProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_PROJECT_ROOT", root)
	t.Chdir(root)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(pemPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_cli", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	}))
	t.Cleanup(srv.Close)
	if code := runGH([]string{"config", "--set", "app_id=1", "--set", "installation_id=2",
		"--set=private_key=" + pemPath, "--set", "api_url=" + srv.URL}); code != 0 {
		t.Fatalf("config --set exit %d", code)
	}
	return root
}

func TestGHConfigSetAndPrint(t *testing.T) {
	ghTestProject(t)
	path, err := ghapp.ConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "app_id = 1\ninstallation_id = 2\nprivate_key = ") {
		t.Fatalf("github.toml = %q", b)
	}
	if code := runGH([]string{"config"}); code != 0 {
		t.Fatalf("config exit %d", code)
	}
	if code := runGH([]string{"config", "--set", "app_id=x"}); code != 1 {
		t.Fatalf("bad --set exit %d", code)
	}
	if code := runGH([]string{"bogus"}); code != 2 {
		t.Fatalf("bogus exit %d", code)
	}
}

func TestGHExecInjectsToken(t *testing.T) {
	ghTestProject(t)
	ok := runGH([]string{"exec", "--", "sh", "-c", `test "$GH_TOKEN" = ghs_cli && test "$GIT_CONFIG_KEY_1" = credential.helper`})
	if ok != 0 {
		t.Fatalf("exec exit %d, want 0", ok)
	}
	if code := runGH([]string{"exec", "--", "sh", "-c", "exit 7"}); code != 7 {
		t.Fatalf("exit passthrough = %d, want 7", code)
	}
}

func TestGHExecRefusesSSHRemote(t *testing.T) {
	root := ghTestProject(t)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:o/r.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if code := runGH([]string{"exec", "--", "true"}); code != 1 {
		t.Fatalf("exec with ssh origin exit %d, want 1", code)
	}
}
