package config

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
)

// Load merges JSON configuration and optional secrets overrides. Any file with
// credentials must be private; Nomad renders it into the task secrets directory.
type Config struct {
	Listen             string            `json:"listen"`
	PublicURL          string            `json:"public_url"`
	DatabaseURL        string            `json:"database_url"`
	DatabaseHost       string            `json:"database_host"`
	DatabasePassword   string            `json:"database_password"`
	NATSAddress        string            `json:"nats_address"`
	NATSPassword       string            `json:"nats_password"`
	NATSURL            string            `json:"nats_url"`
	EncryptionKey      string            `json:"encryption_key"`
	ServiceToken       string            `json:"service_token"`
	AdminUser          string            `json:"admin_user"`
	AdminPassword      string            `json:"admin_password"`
	ShardID            string            `json:"shard_id"`
	ShardURLs          map[string]string `json:"shard_urls"`
	RecorderURL        string            `json:"recorder_url"`
	MediaURL           string            `json:"media_url"`
	RTSPURL            string            `json:"rtsp_url"`
	HLSURL             string            `json:"hls_url"`
	PlaybackURL        string            `json:"playback_url"`
	Archive            string            `json:"archive"`
	QuotaBytes         int64             `json:"quota_bytes"`
	CookieSecure       bool              `json:"cookie_secure"`
	TelegramToken      string            `json:"telegram_token"`
	TelegramChat       string            `json:"telegram_chat"`
	OIDCIssuer         string            `json:"oidc_issuer"`
	OIDCClientID       string            `json:"oidc_client_id"`
	OIDCSecret         string            `json:"oidc_secret"`
	OIDCRoleClaim      string            `json:"oidc_role_claim"`
	OIDCRoleMap        map[string]string `json:"oidc_role_map"`
	DiscoveryInterface string            `json:"discovery_interface"`
	CameraCIDRs        []string          `json:"camera_cidrs"`
}

func Load() (Config, error) {
	c := Config{Listen: ":8080", ShardID: "main", Archive: "/archive", CookieSecure: true, MediaURL: "http://127.0.0.1:9997", RTSPURL: "rtsp://127.0.0.1:8554", HLSURL: "http://127.0.0.1:8888", PlaybackURL: "http://127.0.0.1:9996", RecorderURL: "http://127.0.0.1:8090", AdminUser: "admin", AdminPassword: "admin"}
	for _, env := range []string{"VARTOVYI_CONFIG", "VARTOVYI_SECRETS"} {
		if p := os.Getenv(env); p != "" {
			b, e := os.ReadFile(p)
			if e != nil {
				return c, e
			}
			if e = json.Unmarshal(b, &c); e != nil {
				return c, e
			}
		}
	}
	if c.DatabaseURL == "" && c.DatabaseHost != "" {
		u := url.URL{Scheme: "postgres", User: url.UserPassword("vartovyi", c.DatabasePassword), Host: c.DatabaseHost, Path: "/vartovyi", RawQuery: "sslmode=disable"}
		c.DatabaseURL = u.String()
	}
	if c.NATSURL == "" && c.NATSAddress != "" {
		u := url.URL{Scheme: "nats", User: url.UserPassword("vartovyi", c.NATSPassword), Host: c.NATSAddress}
		c.NATSURL = u.String()
	}
	if len(c.ServiceToken) < 16 || len(c.ServiceToken) > 4096 {
		return c, errors.New("service_token must be 16 to 4096 bytes")
	}
	for _, raw := range []string{c.MediaURL, c.HLSURL, c.PlaybackURL, c.RecorderURL} {
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			return c, errors.New("internal endpoints must be HTTP URLs")
		}
	}
	return c, nil
}
