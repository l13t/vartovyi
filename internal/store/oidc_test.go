package store

import "testing"

func TestPrepareOIDC(t *testing.T) {
	old := OIDCSettings{ClientSecret: "saved-secret"}
	valid := OIDCChanges{Enabled: true, Issuer: "https://idp.example/realm", ClientID: "vartovyi", RoleClaim: "groups", RoleMap: map[string]string{"operators": "manager"}}
	v, e := PrepareOIDC(valid, old)
	if e != nil || v.ClientSecret != "saved-secret" || !v.SecretSet {
		t.Fatal("secret must be retained")
	}
	valid.ClearSecret = true
	v, e = PrepareOIDC(valid, old)
	if e != nil || v.SecretSet {
		t.Fatal("public PKCE clients may clear the secret")
	}
	for _, issuer := range []string{"http://idp.example", "https://user:pass@idp.example", "https://idp.example?query=1", "https://idp.example#fragment", "file:///tmp/issuer"} {
		invalid := valid
		invalid.Issuer = issuer
		if _, e = PrepareOIDC(invalid, old); e == nil {
			t.Fatalf("accepted invalid issuer: %s", issuer)
		}
	}
	invalid := valid
	invalid.RoleMap = map[string]string{"admins": "superuser"}
	if _, e = PrepareOIDC(invalid, old); e == nil {
		t.Fatal("invalid mapped role accepted")
	}
}
