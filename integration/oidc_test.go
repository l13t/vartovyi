package integration

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vartovyi/internal/api"
	"vartovyi/internal/config"
	"vartovyi/internal/store"
)

// Exercises real discovery, PKCE, signed ID tokens and sessions against a local
// synthetic identity provider; no external provider or account is contacted.
func TestOIDCAdminConfigurationAndLogin(t *testing.T) {
	if os.Getenv("VARTOVYI_INTEGRATION") != "1" {
		t.Skip("requires disposable PostgreSQL")
	}
	s, e := store.Open(t.Context(), "postgres://vartovyi:test-password@postgres:5432/vartovyi?sslmode=disable", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(t.Context()); e != nil {
		t.Fatal(e)
	}
	c := config.Config{PublicURL: "http://localhost", EncryptionKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	app, e := api.New(t.Context(), c, s)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	app.Config.PublicURL = server.URL
	admin, me := loginForTest(t, server.URL, "admin", "admin")
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	type authorization struct{ nonce, challenge, clientID, subject string }
	var mu sync.Mutex
	codes := map[string]authorization{}
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": provider.URL, "authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if e := r.ParseForm(); e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			auth, ok := codes[r.FormValue("code")]
			delete(codes, r.FormValue("code"))
			mu.Unlock()
			challenge := sha256.Sum256([]byte(r.FormValue("code_verifier")))
			clientID, secret, basic := r.BasicAuth()
			if !basic || clientID != auth.clientID || secret != "synthetic-client-secret" || !ok || base64.RawURLEncoding.EncodeToString(challenge[:]) != auth.challenge || r.FormValue("redirect_uri") != server.URL+"/auth/oidc/callback" {
				t.Error("invalid OIDC exchange or PKCE")
				w.WriteHeader(400)
				return
			}
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
			claims, _ := json.Marshal(map[string]any{"iss": provider.URL, "aud": auth.clientID, "sub": auth.subject, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": auth.nonce, "preferred_username": "synthetic-oidc", "groups": []string{"operators"}})
			unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
			hash := sha256.Sum256([]byte(unsigned))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access", "token_type": "Bearer", "id_token": unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)})
		default:
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	var settings store.OIDCSettings
	request(t, admin, "GET", server.URL+"/api/v1/settings/oidc", "", nil, 200, &settings)
	changes := store.OIDCChanges{Enabled: true, Issuer: provider.URL, ClientID: "dynamic-client", ClientSecret: "synthetic-client-secret", RoleClaim: "groups", RoleMap: map[string]string{"operators": "manager"}, Revision: settings.Revision}
	request(t, admin, "PUT", server.URL+"/api/v1/settings/oidc", me.CSRF, changes, 200, &settings)
	defer func() {
		disabled := store.OIDCChanges{Revision: settings.Revision}
		request(t, admin, "PUT", server.URL+"/api/v1/settings/oidc", me.CSRF, disabled, 200, nil)
	}()
	var encrypted string
	if e = s.Pool.QueryRow(t.Context(), "SELECT client_secret FROM oidc_settings WHERE id=1").Scan(&encrypted); e != nil || strings.Contains(encrypted, "synthetic-client-secret") {
		t.Fatal("OIDC secret must be encrypted")
	}
	for _, path := range []string{"/api/v1/settings/oidc", "/settings?tab=oidc"} {
		r, err := admin.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if strings.Contains(string(body), "synthetic-client-secret") {
			t.Fatal("OIDC secret leaked")
		}
	}
	// A fresh API instance resolves the same stored settings without startup
	// discovery or process-local configuration changes.
	replica, e := api.New(t.Context(), app.Config, s)
	if e != nil {
		t.Fatal(e)
	}
	replicaServer := httptest.NewServer(replica.Handler())
	defer replicaServer.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	begin := func(subject string) (string, string) {
		t.Helper()
		r, err := browser.Get(replicaServer.URL + "/auth/oidc")
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 302 {
			t.Fatalf("OIDC start status %d", r.StatusCode)
		}
		target, err := url.Parse(r.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		q := target.Query()
		if q.Get("client_id") != changes.ClientID || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
			t.Fatal("invalid authorization request")
		}
		code := subject + "-code"
		mu.Lock()
		codes[code] = authorization{q.Get("nonce"), q.Get("code_challenge"), q.Get("client_id"), subject}
		mu.Unlock()
		return code, q.Get("state")
	}
	code, state := begin("first-login")
	checkPage(t, admin, server.URL+"/settings?tab=recording", 200)
	callback := func(code, state string, want int) {
		t.Helper()
		r, err := browser.Get(replicaServer.URL + "/auth/oidc/callback?" + url.Values{"code": {code}, "state": {state}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("OIDC callback: want %d got %d", want, r.StatusCode)
		}
	}
	callback(code, state, 303)
	var identity identity
	request(t, browser, "GET", replicaServer.URL+"/api/v1/me", "", nil, 200, &identity)
	if identity.User.Role != "manager" {
		t.Fatal("dynamic role mapping not applied")
	}
	for _, method := range []string{"GET", "PUT"} {
		request(t, browser, method, replicaServer.URL+"/api/v1/settings/oidc", identity.CSRF, map[string]any{}, 403, nil)
	}
	// A stale settings form and a failed discovery must preserve active config.
	request(t, admin, "PUT", server.URL+"/api/v1/settings/oidc", me.CSRF, changes, 409, nil)
	changes.Revision = settings.Revision
	changes.ClientSecret = ""
	changes.Issuer = server.URL + "/missing-provider"
	request(t, admin, "PUT", server.URL+"/api/v1/settings/oidc", me.CSRF, changes, 502, nil)
	active, e := s.OIDC(t.Context(), store.OIDCSettings{})
	if e != nil || active.Issuer != provider.URL || active.ClientSecret != "synthetic-client-secret" {
		t.Fatal("failed discovery changed active settings")
	}
	code, state = begin("stale-flow")
	changes.Issuer = provider.URL
	changes.ClientID = "changed-dynamic-client"
	request(t, admin, "PUT", server.URL+"/api/v1/settings/oidc", me.CSRF, changes, 200, &settings)
	callback(code, state, 401)
	code, state = begin("new-config-login")
	callback(code, state, 303)
	// Disabling via the HTML form works and overrides any server fallback.
	r, e := admin.PostForm(server.URL+"/ui/settings/oidc", url.Values{"csrf": {me.CSRF}, "revision": {fmt.Sprint(settings.Revision)}, "issuer": {provider.URL}, "client_id": {changes.ClientID}, "role_claim": {"groups"}, "manager_groups": {"operators"}})
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("OIDC form failed")
	}
	request(t, admin, "GET", server.URL+"/api/v1/settings/oidc", "", nil, 200, &settings)
	if settings.Enabled || !settings.SecretSet {
		t.Fatal("form must disable OIDC while preserving secret")
	}
	request(t, browser, "GET", replicaServer.URL+"/auth/oidc", "", nil, 404, nil)
	loginForTest(t, server.URL, "admin", "admin")
}
