package config

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordsAreURLEncoded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if e := os.WriteFile(path, []byte(`{"service_token":"internal-test-token","database_host":"database:5432","database_password":"a b@:/?#","nats_address":"broker:4222","nats_password":"a b@:/?#"}`), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("VARTOVYI_CONFIG", path)
	t.Setenv("VARTOVYI_SECRETS", "")
	c, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{c.DatabaseURL, c.NATSURL} {
		u, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		password, ok := u.User.Password()
		if !ok || password != "a b@:/?#" {
			t.Fatal("password corrupted in DSN")
		}
	}
}
