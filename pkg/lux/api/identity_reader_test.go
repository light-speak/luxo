package api

import (
	"github.com/light-speak/luxo/pkg/lux/auth"
	"testing"
	"time"
)

func TestIdentityReaderPreservesValidityAndShortCircuit(t *testing.T) {
	cfg := &auth.Config{Secret: "identity-reader-test", Expires: time.Hour}
	token, err := auth.Sign(cfg, func(dst []byte) ([]byte, error) {
		return append(dst, `{"id":0,"role":"","enabled":false,"score":0.5}`...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func(*IdentityReader) bool{
		"float":    func(r *IdentityReader) bool { return r.Float("score") == 0.5 },
		"zero":     func(r *IdentityReader) bool { return r.Int("id") == 0 },
		"empty":    func(r *IdentityReader) bool { return r.String("role") == "" },
		"false":    func(r *IdentityReader) bool { return !r.Bool("enabled") },
		"short-or": func(r *IdentityReader) bool { return r.Int("id") == 0 || r.String("missing") == "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := NewIdentityReader(claims)
			if !r.Allowed(check(&r)) {
				t.Fatal("valid value rejected")
			}
		})
	}
	for name, check := range map[string]func(*IdentityReader) bool{
		"missing":         func(r *IdentityReader) bool { return r.String("missing") != "blocked" },
		"negated":         func(r *IdentityReader) bool { return !(r.String("missing") == "blocked") },
		"wrong-kind":      func(r *IdentityReader) bool { return r.Int("role") == 0 },
		"invalid-or-true": func(r *IdentityReader) bool { return r.String("missing") == "" || true },
	} {
		t.Run(name, func(t *testing.T) {
			r := NewIdentityReader(claims)
			if r.Allowed(check(&r)) {
				t.Fatal("invalid read accepted")
			}
		})
	}
	var nilClaims *auth.Claims
	for _, identity := range []any{nil, nilClaims, "wrong"} {
		r := NewIdentityReader(identity)
		if !r.Allowed(true) {
			t.Fatal("constant predicate must not read identity")
		}
		if r.Allowed(r.Int("id") == 0) {
			t.Fatal("missing identity became zero")
		}
	}
	r := NewIdentityReader(&claims)
	if !r.Allowed(r.String("role") == "") {
		t.Fatal("pointer identity rejected")
	}
}

func TestIdentityReaderValidatedString(t *testing.T) {
	r := NewIdentityReader(nil)
	if got := r.ValidatedString("MEMBER", true); got != "MEMBER" || !r.Allowed(true) {
		t.Fatal("valid member rejected")
	}
	if got := r.ValidatedString("UNKNOWN", false); got != "UNKNOWN" || r.Allowed(true) {
		t.Fatal("invalid member allowed")
	}
	r.ValidatedString("MEMBER", true)
	if r.Allowed(true) {
		t.Fatal("later valid member erased failure")
	}
}

func TestIdentityReaderSubjectBoundary(t *testing.T) {
	cfg := &auth.Config{Secret: "reader-subject", Expires: time.Hour}
	token, err := auth.SignForSubject(cfg, "User", func(dst []byte) ([]byte, error) { return append(dst, `{"id":42,"role":"member"}`...), nil })
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []any{claims, &claims} {
		reader := NewIdentityReaderForSubject(identity, "User")
		if !reader.Allowed(reader.Int("id") == 42) {
			t.Fatal("own subject rejected")
		}
		reader = NewIdentityReaderForSubject(identity, "Admin")
		if reader.Allowed(reader.String("role") != "blocked") {
			t.Fatal("other subject granted by negation")
		}
		reader = NewIdentityReaderForSubject(identity, "Admin")
		if !reader.Allowed(true || reader.Int("id") == 42) {
			t.Fatal("unread identity changed short circuit")
		}
	}
	for _, identity := range []any{nil, (*auth.Claims)(nil), "wrong"} {
		reader := NewIdentityReaderForSubject(identity, "User")
		if reader.Allowed(reader.Int("id") == 0) {
			t.Fatal("missing subject granted access")
		}
	}
}
