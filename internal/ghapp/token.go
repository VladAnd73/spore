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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/versality/spore/internal/task"
)

// refreshMargin is how long before expiry a cached token is replaced,
// so a long `git push` never runs on a token that dies mid-flight.
const refreshMargin = 5 * time.Minute

// Token is an installation access token.
type Token struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ParsePrivateKey reads an RSA key from PEM, PKCS#1 (GitHub's format)
// or PKCS#8.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("ghapp: private key: no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ghapp: private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("ghapp: private key: not RSA")
	}
	return rk, nil
}

// SignJWT returns the RS256 app JWT GitHub expects: iat backdated 60s
// for clock drift, exp 9 minutes out (the cap is 10).
func SignJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("ghapp: sign jwt: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// defaultHTTP bounds the token request: the mint runs inside the fleet
// reconcile, where one hung connection would stall every task.
var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// Minter fetches installation tokens. HTTP and Now default to
// defaultHTTP and time.Now.
type Minter struct {
	Config Config
	HTTP   *http.Client
	Now    func() time.Time
}

func (m Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Mint requests a fresh installation token.
func (m Minter) Mint(ctx context.Context) (Token, error) {
	if err := m.Config.Validate(); err != nil {
		return Token{}, err
	}
	pemBytes, err := os.ReadFile(m.Config.PrivateKeyPath())
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: read private key: %w", err)
	}
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		return Token{}, err
	}
	jwt, err := SignJWT(m.Config.AppID, key, m.now())
	if err != nil {
		return Token{}, err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", m.Config.API(), m.Config.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := m.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: token response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return Token{}, fmt.Errorf("ghapp: token request: %s: %s", resp.Status, truncate(string(body), 300))
	}
	var tok Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return Token{}, fmt.Errorf("ghapp: token response: %w", err)
	}
	if tok.Token == "" || tok.ExpiresAt.IsZero() {
		return Token{}, errors.New("ghapp: token response: missing token or expires_at")
	}
	return tok, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// cacheEntry ties a cached token to the app and installation that
// minted it, so editing github.toml never serves a stale identity.
type cacheEntry struct {
	AppID          int64 `json:"app_id"`
	InstallationID int64 `json:"installation_id"`
	Token
}

// TokenCachePath is <StateDirForProject>/github-token.json (0600).
func TokenCachePath(projectRoot string) (string, error) {
	dir, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "github-token.json"), nil
}

// CachedToken returns a token valid for at least refreshMargin, from
// the host-side cache when possible, else minted and cached.
func (m Minter) CachedToken(ctx context.Context, cachePath string) (Token, error) {
	if b, err := os.ReadFile(cachePath); err == nil {
		var e cacheEntry
		if json.Unmarshal(b, &e) == nil &&
			e.AppID == m.Config.AppID && e.InstallationID == m.Config.InstallationID &&
			e.Token.Token != "" && e.ExpiresAt.After(m.now().Add(refreshMargin)) {
			return e.Token, nil
		}
	}
	tok, err := m.Mint(ctx)
	if err != nil {
		return Token{}, err
	}
	b, err := json.Marshal(cacheEntry{AppID: m.Config.AppID, InstallationID: m.Config.InstallationID, Token: tok})
	if err != nil {
		return Token{}, err
	}
	if err := writePrivate(cachePath, b); err != nil {
		return Token{}, fmt.Errorf("ghapp: write token cache: %w", err)
	}
	return tok, nil
}

// ProjectToken loads github.toml for projectRoot and returns a cached
// or fresh installation token.
func ProjectToken(ctx context.Context, projectRoot string) (Token, error) {
	cfg, err := LoadConfig(projectRoot)
	if err != nil {
		return Token{}, err
	}
	path, err := TokenCachePath(projectRoot)
	if err != nil {
		return Token{}, err
	}
	return Minter{Config: cfg}.CachedToken(ctx, path)
}
