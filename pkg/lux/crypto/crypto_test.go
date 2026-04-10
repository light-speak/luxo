package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestSHA256(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"hello world", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"},
	}
	for _, tt := range tests {
		if got := SHA256(tt.input); got != tt.want {
			t.Errorf("SHA256(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMD5(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "5d41402abc4b2a76b9719d911017c592"},
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"hello world", "5eb63bbbe01eeed093cb22bb8f5acdc3"},
	}
	for _, tt := range tests {
		if got := MD5(tt.input); got != tt.want {
			t.Errorf("MD5(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHMAC(t *testing.T) {
	tests := []struct {
		data, key, want string
	}{
		// Known HMAC-SHA256 values
		{"hello", "secret", "88aab3ede8d3adf94d26ab90d3bafd4a2083070c3bcce9c014ee04a443847c0b"},
		{"", "key", "5d5d139563c95b5967b9bd9a8c9b233a9dedb45072794cd232dc1b74832607d0"},
	}
	for _, tt := range tests {
		if got := HMAC(tt.data, tt.key); got != tt.want {
			t.Errorf("HMAC(%q, %q) = %q, want %q", tt.data, tt.key, got, tt.want)
		}
	}
}

func TestRandomBytes(t *testing.T) {
	t.Run("correct length", func(t *testing.T) {
		for _, n := range []int{1, 16, 32, 64} {
			b, err := RandomBytes(n)
			if err != nil {
				t.Fatalf("RandomBytes(%d) error = %v", n, err)
			}
			if len(b) != n {
				t.Errorf("RandomBytes(%d) length = %d, want %d", n, len(b), n)
			}
		}
	})
	t.Run("not identical", func(t *testing.T) {
		// Two calls should produce different results (with overwhelming probability)
		a, _ := RandomBytes(32)
		b, _ := RandomBytes(32)
		if hex.EncodeToString(a) == hex.EncodeToString(b) {
			t.Errorf("RandomBytes(32) produced identical results twice")
		}
	})
}

func TestRandomHex(t *testing.T) {
	t.Run("correct length", func(t *testing.T) {
		for _, n := range []int{1, 16, 32} {
			s, err := RandomHex(n)
			if err != nil {
				t.Fatalf("RandomHex(%d) error = %v", n, err)
			}
			if len(s) != 2*n {
				t.Errorf("RandomHex(%d) length = %d, want %d", n, len(s), 2*n)
			}
			// Verify it's valid hex
			if _, err := hex.DecodeString(s); err != nil {
				t.Errorf("RandomHex(%d) produced invalid hex: %q", n, s)
			}
		}
	})
	t.Run("not identical", func(t *testing.T) {
		a, _ := RandomHex(32)
		b, _ := RandomHex(32)
		if a == b {
			t.Errorf("RandomHex(32) produced identical results twice")
		}
	})
}

// failReader is an io.Reader that always returns an error.
type failReader struct{}

func (failReader) Read([]byte) (int, error) {
	return 0, errors.New("simulated rand failure")
}

func TestRandomBytesError(t *testing.T) {
	orig := randReader
	randReader = failReader{}
	defer func() { randReader = orig }()

	_, err := RandomBytes(16)
	if err == nil {
		t.Errorf("RandomBytes() expected error with failing reader")
	}
}

func TestRandomHexError(t *testing.T) {
	orig := randReader
	randReader = failReader{}
	defer func() { randReader = orig }()

	_, err := RandomHex(16)
	if err == nil {
		t.Errorf("RandomHex() expected error with failing reader")
	}
}

func TestRandomBytesZero(t *testing.T) {
	_, err := RandomBytes(0)
	if err == nil {
		t.Fatal("should error for n=0")
	}
}

func TestRandomBytesNegative(t *testing.T) {
	_, err := RandomBytes(-1)
	if err == nil {
		t.Fatal("should error for n<0")
	}
}

// Ensure we imported the packages used in the error test.
var _ io.Reader = failReader{}
var _ = rand.Reader
