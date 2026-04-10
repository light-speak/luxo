// Package crypto provides cryptographic utility functions for the Luxo runtime.
// It wraps Go's crypto packages with a simpler string-oriented API.
package crypto

import (
	gohmac "crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// randReader is the source of cryptographic randomness. It defaults to
// crypto/rand.Reader and can be overridden in tests to simulate errors.
var randReader io.Reader = rand.Reader

// SHA256 returns the SHA-256 hash of data as a hex-encoded string.
func SHA256(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// MD5 returns the MD5 hash of data as a hex-encoded string.
func MD5(data string) string {
	h := md5.Sum([]byte(data))
	return hex.EncodeToString(h[:])
}

// TODO: Bcrypt(password string) (string, error) — requires golang.org/x/crypto/bcrypt.
// Will be added when bcrypt support is needed for @hash annotation.

// TODO: BcryptVerify(password, hash string) bool — requires golang.org/x/crypto/bcrypt.
// Will be added when bcrypt support is needed for @withAuth verify.

// HMAC returns the HMAC-SHA256 of data using key, as a hex-encoded string.
func HMAC(data, key string) string {
	mac := gohmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// RandomBytes returns n cryptographically secure random bytes.
func RandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("crypto: n must be positive, got %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return nil, fmt.Errorf("crypto: failed to generate random bytes: %w", err)
	}
	return b, nil
}

// RandomHex returns n cryptographically secure random bytes as a hex-encoded string.
// The resulting string has length 2*n.
func RandomHex(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
