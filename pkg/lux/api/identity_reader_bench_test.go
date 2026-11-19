package api

import (
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

var identityConditionSink bool

type identityBenchmarkRole string

func (r identityBenchmarkRole) IsValid() bool {
	switch r {
	case "ADMIN", "MEMBER":
		return true
	}
	return false
}

func benchmarkIdentityClaims(b *testing.B, raw string) auth.Claims {
	b.Helper()
	cfg := &auth.Config{Secret: "identity-condition-benchmark", Expires: time.Hour}
	token, err := auth.Sign(cfg, func(dst []byte) ([]byte, error) { return append(dst, raw...), nil })
	if err != nil {
		b.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		b.Fatal(err)
	}
	return claims
}

// Reference retains the former unchecked access. It is not equivalent
// correctness: missing or invalid identity values could authorize the predicate.
func BenchmarkIdentityConditionValidity(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"member", `{"role":"MEMBER","id":42}`},
		{"empty", `{"role":"","id":0}`},
		{"missing", `{}`},
		{"wrong-kind", `{"role":42,"id":"0"}`},
	} {
		claims := benchmarkIdentityClaims(b, sample.raw)
		var identity any = claims
		b.Run(sample.name+"/TextReference", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				identityConditionSink = IdentityString(identity, "role") != "ADMIN"
			}
		})
		b.Run(sample.name+"/TextChecked", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				reader := NewIdentityReader(identity)
				identityConditionSink = reader.Allowed(reader.String("role") != "ADMIN")
			}
		})
		b.Run(sample.name+"/EnumReference", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				identityConditionSink = IdentityString(identity, "role") != "ADMIN"
			}
		})
		b.Run(sample.name+"/EnumChecked", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				reader := NewIdentityReader(identity)
				identityConditionSink = reader.Allowed(func() string {
					value := reader.String("role")
					return reader.ValidatedString(value, identityBenchmarkRole(value).IsValid())
				}() != "ADMIN")
			}
		})
		b.Run(sample.name+"/OwnReference", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				identityConditionSink = claims.ID() == 0
			}
		})
		b.Run(sample.name+"/OwnChecked", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, valid := claims.Get("id").Int64()
				identityConditionSink = valid && value == 0
			}
		})
	}
}

var identityEnumArraySink []identityBenchmarkRole

// The previous scalar reader rejected valid lists, so these are absolute
// measurements of the corrected path, not an equivalent old/new comparison.
func BenchmarkIdentityEnumArray(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"empty", `{"role":[]}`},
		{"one", `{"role":["ADMIN"]}`},
		{"128", `{"role":[` + strings.Repeat(`"ADMIN",`, 127) + `"MEMBER"]}`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			value := benchmarkIdentityClaims(b, sample.raw).Get("role")
			b.ReportAllocs()
			for b.Loop() {
				values, err := ReadIdentityEnumArray[identityBenchmarkRole](value)
				if err != nil {
					b.Fatal(err)
				}
				for _, item := range values {
					if !item.IsValid() {
						b.Fatal(item)
					}
				}
				identityEnumArraySink = values
			}
		})
	}
}

// Compare the same verified identity with and without the subject boundary.
// The reference lacks cross-subject protection; signing and verification are excluded.
func BenchmarkIdentitySubjectVisibility(b *testing.B) {
	cfg := &auth.Config{Secret: "subject-benchmark", Expires: time.Hour}
	token, err := auth.SignForSubject(cfg, "User", func(dst []byte) ([]byte, error) { return append(dst, `{"role":"member"}`...), nil })
	if err != nil {
		b.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		b.Fatal(err)
	}
	for _, sample := range []struct {
		name  string
		value any
	}{{"value", claims}, {"pointer", &claims}} {
		b.Run(sample.name+"/reference", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				reader := NewIdentityReader(sample.value)
				identityConditionSink = reader.Allowed(reader.String("role") == "member")
			}
		})
		b.Run(sample.name+"/subject", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				reader := NewIdentityReaderForSubject(sample.value, "User")
				identityConditionSink = reader.Allowed(reader.String("role") == "member")
			}
		})
	}
}
