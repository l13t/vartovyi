package auth

import (
	"encoding/base64"
	"testing"
)

func TestPasswordAndCipher(t *testing.T) {
	h, e := HashPassword("long enough password")
	if e != nil || !VerifyPassword(h, "long enough password") || VerifyPassword(h, "wrong") {
		t.Fatal("password verification")
	}
	c, e := NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	v, e := c.Encrypt("rtsp://user:secret@camera")
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.Decrypt(v)
	if e != nil || p != "rtsp://user:secret@camera" {
		t.Fatal("round trip")
	}
	if _, e = c.Decrypt(v[:len(v)-4] + "AAAA"); e == nil {
		t.Fatal("tampering accepted")
	}
}

func TestDefaultAdminPasswordDoesNotWeakenNormalPolicy(t *testing.T) {
	hash, e := HashDefaultAdminPassword()
	if e != nil || !VerifyPassword(hash, "admin") {
		t.Fatal("default admin password must work")
	}
	if _, e = HashPassword("admin"); e == nil {
		t.Fatal("normal user passwords still require 12 bytes")
	}
}
