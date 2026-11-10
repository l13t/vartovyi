package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const iterations = 600000

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must be 12 to 1024 bytes")
	}
	return hashPassword(password)
}

// HashDefaultAdminPassword is restricted to the explicitly configured initial
// admin/admin account. Normal account creation and password changes use policy.
func HashDefaultAdminPassword() (string, error) { return hashPassword("admin") }
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}
func VerifyPassword(hash, password string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" || len(password) > 1024 {
		return false
	}
	n, e := strconv.Atoi(p[1])
	if e != nil || n < 100000 || n > 2000000 {
		return false
	}
	s, e := hex.DecodeString(p[2])
	if e != nil || len(s) != 16 {
		return false
	}
	want, e := hex.DecodeString(p[3])
	if e != nil || len(want) != 32 {
		return false
	}
	got, e := pbkdf2.Key(sha256.New, password, s, n, 32)
	return e == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func Token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func Equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

type Cipher struct{ aead cipher.AEAD }

func NewCipher(key string) (*Cipher, error) {
	b, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(b) != 32 {
		return nil, errors.New("encryption_key must be base64 encoded 32 bytes")
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	return &Cipher{a}, e
}
func (c *Cipher) Encrypt(plain string) (string, error) {
	n := make([]byte, c.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(c.aead.Seal(n, n, []byte(plain), nil)), nil
}
func (c *Cipher) Decrypt(encoded string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil || len(b) < c.aead.NonceSize() {
		return "", errors.New("invalid encrypted value")
	}
	p, e := c.aead.Open(nil, b[:c.aead.NonceSize()], b[c.aead.NonceSize():], nil)
	return string(p), e
}
