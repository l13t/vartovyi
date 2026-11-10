package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"vartovyi/internal/model"
)

type OIDCSettings struct {
	Enabled      bool              `json:"enabled"`
	Issuer       string            `json:"issuer"`
	ClientID     string            `json:"client_id"`
	ClientSecret string            `json:"-"`
	SecretSet    bool              `json:"secret_set"`
	RoleClaim    string            `json:"role_claim"`
	RoleMap      map[string]string `json:"role_map"`
	Revision     int64             `json:"revision"`
}
type OIDCChanges struct {
	Enabled      bool              `json:"enabled"`
	Issuer       string            `json:"issuer"`
	ClientID     string            `json:"client_id"`
	ClientSecret string            `json:"client_secret"`
	ClearSecret  bool              `json:"clear_secret"`
	RoleClaim    string            `json:"role_claim"`
	RoleMap      map[string]string `json:"role_map"`
	Revision     int64             `json:"revision"`
}

var ErrInvalidOIDC = errors.New("invalid OIDC settings")

func PrepareOIDC(v OIDCChanges, old OIDCSettings) (OIDCSettings, error) {
	result := OIDCSettings{Enabled: v.Enabled, Issuer: strings.TrimSpace(v.Issuer), ClientID: strings.TrimSpace(v.ClientID), ClientSecret: old.ClientSecret, RoleClaim: strings.TrimSpace(v.RoleClaim), RoleMap: v.RoleMap, Revision: v.Revision}
	if v.ClearSecret {
		result.ClientSecret = ""
	} else if v.ClientSecret != "" {
		result.ClientSecret = v.ClientSecret
	}
	result.SecretSet = result.ClientSecret != ""
	if len(result.Issuer) > 2048 || len(result.ClientID) > 512 || len(result.ClientSecret) > 4096 || len(result.RoleClaim) > 200 || len(result.RoleMap) > 100 || v.Revision < 0 {
		return result, ErrInvalidOIDC
	}
	if result.Issuer != "" {
		u, e := url.Parse(result.Issuer)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return result, ErrInvalidOIDC
		}
	}
	if result.Enabled && (result.Issuer == "" || result.ClientID == "") {
		return result, ErrInvalidOIDC
	}
	for external, role := range result.RoleMap {
		if strings.TrimSpace(external) == "" || len(external) > 200 || !model.Role(role).Valid() {
			return result, ErrInvalidOIDC
		}
	}
	if len(result.RoleMap) > 0 && result.RoleClaim == "" {
		return result, ErrInvalidOIDC
	}
	return result, nil
}
func (s *Store) OIDC(ctx context.Context, fallback OIDCSettings) (OIDCSettings, error) {
	var b []byte
	var secret string
	var revision int64
	e := s.Pool.QueryRow(ctx, "SELECT config,client_secret,revision FROM oidc_settings WHERE id=1").Scan(&b, &secret, &revision)
	if errors.Is(e, pgx.ErrNoRows) {
		fallback.SecretSet = fallback.ClientSecret != ""
		return fallback, nil
	}
	if e != nil {
		return OIDCSettings{}, e
	}
	var v OIDCSettings
	if e = json.Unmarshal(b, &v); e != nil {
		return v, e
	}
	if secret != "" {
		v.ClientSecret, e = s.cipher.Decrypt(secret)
	}
	v.SecretSet = v.ClientSecret != ""
	v.Revision = revision
	return v, e
}
func (s *Store) SaveOIDC(ctx context.Context, v OIDCSettings) (OIDCSettings, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return v, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039443)"); e != nil {
		return v, e
	}
	var revision int64
	e = tx.QueryRow(ctx, "SELECT revision FROM oidc_settings WHERE id=1").Scan(&revision)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return v, e
	}
	if revision != v.Revision {
		return v, ErrConflict
	}
	secret := ""
	if v.ClientSecret != "" {
		secret, e = s.cipher.Encrypt(v.ClientSecret)
		if e != nil {
			return v, e
		}
	}
	v.Revision++
	v.SecretSet = v.ClientSecret != ""
	b, e := json.Marshal(v)
	if e != nil {
		return v, e
	}
	_, e = tx.Exec(ctx, "INSERT INTO oidc_settings(id,config,client_secret,revision) VALUES(1,$1,$2,$3) ON CONFLICT(id) DO UPDATE SET config=$1,client_secret=$2,revision=$3", b, secret, v.Revision)
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}
