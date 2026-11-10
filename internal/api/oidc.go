package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"vartovyi/internal/auth"
	"vartovyi/internal/config"
	"vartovyi/internal/model"
	"vartovyi/internal/store"
)

type oidcLogin struct {
	provider    *oidc.Provider
	oauth       oauth2.Config
	cipher      *auth.Cipher
	config      config.Config
	fingerprint string
}
type oidcFlow struct {
	State, Nonce, Verifier string
	At                     time.Time
	Configuration          string
}

func newOIDC(ctx context.Context, c config.Config, s *store.Store) (*oidcLogin, error) {
	ctx = oidc.ClientContext(ctx, &http.Client{Timeout: 10 * time.Second})
	p, e := oidc.NewProvider(ctx, c.OIDCIssuer)
	if e != nil {
		return nil, e
	}
	for _, endpoint := range []string{p.Endpoint().AuthURL, p.Endpoint().TokenURL} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return nil, errors.New("invalid OIDC endpoint")
		}
	}
	cipher, e := auth.NewCipher(c.EncryptionKey)
	if e != nil {
		return nil, e
	}
	return &oidcLogin{provider: p, oauth: oauth2.Config{ClientID: c.OIDCClientID, ClientSecret: c.OIDCSecret, Endpoint: p.Endpoint(), RedirectURL: strings.TrimSuffix(c.PublicURL, "/") + "/auth/oidc/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, cipher: cipher, config: c, fingerprint: oidcFingerprint(c)}, nil
}
func (a *API) oidcStart(w http.ResponseWriter, r *http.Request) {
	login, e := a.currentOIDC(r.Context())
	if e != nil {
		fail(w, 503, "oidc_unavailable")
		return
	}
	if login == nil {
		fail(w, 404, "oidc_disabled")
		return
	}
	flow := oidcFlow{State: auth.Token(), Nonce: auth.Token(), Verifier: oauth2.GenerateVerifier(), At: time.Now(), Configuration: login.fingerprint}
	b, _ := json.Marshal(flow)
	encoded, e := login.cipher.Encrypt(string(b))
	if e != nil {
		fail(w, 500, "login_failed")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_oidc", Value: encoded, Path: "/auth/oidc", HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, login.oauth.AuthCodeURL(flow.State, oidc.Nonce(flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier)), 302)
}
func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	login, e := a.currentOIDC(r.Context())
	if e != nil {
		fail(w, 503, "oidc_unavailable")
		return
	}
	if login == nil {
		fail(w, 404, "oidc_disabled")
		return
	}
	cookie, e := r.Cookie("vartovyi_oidc")
	if e != nil {
		fail(w, 401, "invalid_oidc_state")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vartovyi_oidc", Path: "/auth/oidc", MaxAge: -1, Secure: a.Config.CookieSecure, HttpOnly: true})
	plain, e := login.cipher.Decrypt(cookie.Value)
	var flow oidcFlow
	if e != nil || json.Unmarshal([]byte(plain), &flow) != nil || time.Since(flow.At) > 10*time.Minute || !auth.Equal(flow.State, r.URL.Query().Get("state")) || flow.At.After(time.Now().Add(time.Minute)) || !auth.Equal(flow.Configuration, login.fingerprint) {
		fail(w, 401, "invalid_oidc_state")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = oidc.ClientContext(ctx, &http.Client{Timeout: 10 * time.Second})
	token, e := login.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if e != nil {
		fail(w, 401, "oidc_exchange_failed")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		fail(w, 401, "invalid_id_token")
		return
	}
	id, e := login.provider.Verifier(&oidc.Config{ClientID: login.config.OIDCClientID}).Verify(ctx, raw)
	if e != nil || !auth.Equal(id.Nonce, flow.Nonce) {
		fail(w, 401, "invalid_id_token")
		return
	}
	var claims map[string]any
	if id.Claims(&claims) != nil {
		fail(w, 401, "invalid_id_token")
		return
	}
	name, _ := claims["preferred_username"].(string)
	if name == "" {
		name = "oidc"
	}
	role := model.Viewer
	claim := claims[login.config.OIDCRoleClaim]
	values := []string{}
	if v, ok := claim.(string); ok {
		values = append(values, v)
	}
	if vs, ok := claim.([]any); ok {
		for _, v := range vs {
			if s, ok := v.(string); ok {
				values = append(values, s)
			}
		}
	}
	for _, v := range values {
		mapped := model.Role(login.config.OIDCRoleMap[v])
		if mapped == model.Admin {
			role = mapped
			break
		}
		if mapped == model.Manager {
			role = mapped
		}
	}
	u, e := a.Store.OIDCUser(ctx, login.config.OIDCIssuer+"|"+id.Subject, name, role)
	if e != nil {
		fail(w, 503, "database_unavailable")
		return
	}
	a.startSession(w, r, u)
}

func oidcFingerprint(c config.Config) string {
	b, _ := json.Marshal([]any{c.OIDCIssuer, c.OIDCClientID, c.OIDCSecret, c.OIDCRoleClaim, c.OIDCRoleMap, c.PublicURL})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (a *API) oidcSettings(ctx context.Context) (store.OIDCSettings, error) {
	fallback := store.OIDCSettings{Enabled: a.Config.OIDCIssuer != "", Issuer: a.Config.OIDCIssuer, ClientID: a.Config.OIDCClientID, ClientSecret: a.Config.OIDCSecret, RoleClaim: a.Config.OIDCRoleClaim, RoleMap: a.Config.OIDCRoleMap}
	if a.Store == nil {
		return fallback, nil
	}
	return a.Store.OIDC(ctx, fallback)
}
func (a *API) oidcConfig(v store.OIDCSettings) config.Config {
	c := a.Config
	c.OIDCIssuer = v.Issuer
	c.OIDCClientID = v.ClientID
	c.OIDCSecret = v.ClientSecret
	c.OIDCRoleClaim = v.RoleClaim
	c.OIDCRoleMap = v.RoleMap
	return c
}
func (a *API) currentOIDC(ctx context.Context) (*oidcLogin, error) {
	settings, e := a.oidcSettings(ctx)
	if e != nil || !settings.Enabled {
		return nil, e
	}
	c := a.oidcConfig(settings)
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	if a.oidc != nil && a.oidc.fingerprint == oidcFingerprint(c) {
		return a.oidc, nil
	}
	login, e := newOIDC(ctx, c, a.Store)
	if e != nil {
		return nil, e
	}
	a.oidc = login
	return login, nil
}
