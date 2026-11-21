package cachepolicy

import (
	"strings"
	"testing"
	"time"
)

func TestParsePolicies(t *testing.T) {
	raw := []byte(`{"version":1,"policies":{"projectDetail":{"mode":"ttl","ttl":"5m","scope":"private"},"catalog":{"mode":"ttl","ttl":60,"scope":"public"},"preview":{"mode":"ttl","ttl":"1d"}}}`)
	catalog, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]Policy{
		"projectDetail": {Mode: TTL, TTL: 5 * time.Minute, Scope: Private},
		"catalog":       {Mode: TTL, TTL: time.Minute, Scope: Public},
		"preview":       {Mode: TTL, TTL: 24 * time.Hour, Scope: Private},
	} {
		got, ok := catalog.Lookup(name)
		if !ok || got != want {
			t.Fatalf("%s: %v, %v; want %v", name, got, ok, want)
		}
		got.TTL = 0
		again, _ := catalog.Lookup(name)
		if again != want {
			t.Fatal("lookup exposed mutable policy storage")
		}
	}
	clear(raw)
	if _, ok := catalog.Lookup("projectDetail"); !ok {
		t.Fatal("catalog borrowed input storage")
	}
	if _, ok := catalog.Lookup("missing"); ok {
		t.Fatal("unknown policy accepted")
	}
	var empty *Catalog
	if _, ok := empty.Lookup("missing"); ok {
		t.Fatal("nil catalog accepted policy")
	}
}

func TestParseRejectsInvalidCatalog(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `[]`, `{}`, `{"version":1}`, `{"policies":{}}`,
		`{"version":2,"policies":{}}`, `{"version":1.0,"policies":{}}`,
		`{"version":"1","policies":{}}`, `{"version":1,"policies":null}`,
		`{"version":1,"policies":{},"backend":"redis"}`,
		`{"version":1,"version":1,"policies":{}}`,
		`{"version":1,"policies":{},"policies":{}}`,
		`{"version":1,"policies":{}} {}`,
		`{"version":1,"policies":{"p":{},"p":{}}}`,
		`{"version":1,"policies":{"p":{"mode":"ttl","ttl":"1m","mode":"ttl"}}}`,
		`{"version":1,"policies":{"p":{"mode":"ttl","ttl":"1m","\u0074tl":"2m"}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			catalog, err := Parse([]byte(raw))
			if err == nil || catalog != nil {
				t.Fatalf("invalid catalog produced partial result: %v, %v", catalog, err)
			}
		})
	}
	if _, err := Parse([]byte(`{"policies":{},"version":1}`)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejectsInvalidPolicy(t *testing.T) {
	for _, policy := range []string{
		`null`, `[]`, `{}`, `{"mode":"ttl"}`, `{"ttl":"1m"}`,
		`{"mode":"automatic","ttl":"1m"}`, `{"mode":1,"ttl":"1m"}`,
		`{"mode":"ttl","ttl":"1m","scope":"tenant"}`,
		`{"mode":"ttl","ttl":"1m","scope":null}`,
		`{"mode":"ttl","ttl":"1m","scope":""}`,
		`{"mode":"ttl","ttl":"1m","dependsOn":[]}`,
		`{"mode":"ttl","ttl":"1m","backend":"memory"}`,
		`{"mode":"ttl","ttl":0}`, `{"mode":"ttl","ttl":-1}`,
		`{"mode":"ttl","ttl":1.5}`, `{"mode":"ttl","ttl":true}`,
		`{"mode":"ttl","ttl":"1.5s"}`, `{"mode":"ttl","ttl":"1w"}`,
		`{"mode":"ttl","ttl":"9223372036855ms"}`,
		`{"mode":"ttl","ttl":"\ud800"}`,
	} {
		t.Run(policy, func(t *testing.T) {
			catalog, err := Parse([]byte(`{"version":1,"policies":{"p":` + policy + `}}`))
			if err == nil || catalog != nil || !strings.Contains(err.Error(), "p") {
				t.Fatalf("invalid policy produced result: %v, %v", catalog, err)
			}
		})
	}
	for _, name := range []string{``, ` p`, `p `, `p\nq`, `p\u0000q`, `\ud800`} {
		if _, err := Parse([]byte(`{"version":1,"policies":{"` + name + `":{"mode":"ttl","ttl":"1s"}}}`)); err == nil {
			t.Fatalf("invalid policy name %q accepted", name)
		}
	}
}

func TestInvalidationIsNotATTLPolicy(t *testing.T) {
	const raw = `{"version":1,"policies":{"p":{"mode":"invalidation","ttl":"1m","scope":"private"}}}`
	if catalog, err := Parse([]byte(raw)); err == nil || catalog != nil {
		t.Fatalf("unsupported invalidation policy accepted: %v, %v", catalog, err)
	}
	policy := Policy{Mode: Mode("invalidation"), TTL: time.Minute, Scope: Private}
	if err := policy.Validate(); err == nil {
		t.Fatal("direct policy bypassed the TTL-only contract")
	}
}

func TestParseTTL(t *testing.T) {
	for _, sample := range []struct {
		text     string
		duration bool
		want     time.Duration
	}{
		{"060", false, time.Minute}, {"1ms", true, time.Millisecond},
		{"1s", true, time.Second}, {"2m", true, 2 * time.Minute},
		{"3h", true, 3 * time.Hour}, {"1d", true, 24 * time.Hour},
		{"9223372036854ms", true, 9223372036854 * time.Millisecond},
		{"0", false, 0}, {"-1", false, 0}, {"+1", false, 0},
		{"9223372037", false, 0}, {"18446744073709551616", false, 0},
		{"", true, 0}, {"1w", true, 0}, {"1.5s", true, 0},
		{"1m1s", true, 0}, {"xs", true, 0}, {"1", true, 0},
	} {
		got, ok := ParseTTL(sample.text, sample.duration)
		if got != sample.want || ok != (sample.want > 0) {
			t.Fatalf("%q: %v, %v; want %v", sample.text, got, ok, sample.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"version":1,"policies":{"p":{"mode":"ttl","ttl":"30s"}}}`))
	f.Add([]byte(`{"version":1,"policies":{}}`))
	f.Add([]byte(`{"version":1,"policies":{"p":{"mode":"invalidation","ttl":"1m"}}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		catalog, err := Parse(raw)
		if err != nil && catalog != nil {
			t.Fatal("failed parse exposed a partial catalog")
		}
	})
}
