package auth

import (
	"encoding/json/jsontext"
	"math"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func parseClaims(raw []byte) (Claims, error) {
	reader := jsonutil.BorrowReader(raw)
	defer reader.Release()
	claims, err := readClaimsObject(reader, raw)
	if err != nil {
		return Claims{}, err
	}
	if err := finishClaimsObject(reader, raw); err != nil {
		return Claims{}, err
	}
	claims.data.raw = string(raw)
	return claims, nil
}

func testClaims(id int64, role string) ClaimsWriter {
	return func(dst []byte) ([]byte, error) {
		dst = strconv.AppendInt(append(dst, `{"id":`...), id, 10)
		dst = append(dst, `,"role":`...)
		dst, err := jsontext.AppendQuote(dst, role)
		return append(dst, '}'), err
	}
}

func TestClaimsPreserveExactNumbers(t *testing.T) {
	for _, id := range []int64{math.MinInt64, -1, 0, 1, 9007199254740993, math.MaxInt64} {
		raw := `{"id":` + strconv.FormatInt(id, 10) + `,"huge":123456789012345678901234567890,"decimal":0.123456789012345678901234567890}`
		claims, err := parseClaims([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if claims.ID() != id {
			t.Fatalf("ID = %d, want %d", claims.ID(), id)
		}
		if claims.Raw() != raw {
			t.Fatal("raw numeric text changed")
		}
		if claims.Get("huge").Raw() != "123456789012345678901234567890" {
			t.Fatal("large number changed")
		}
		if _, ok := claims.Get("huge").Int64(); ok {
			t.Fatal("overflow accepted")
		}
		if _, ok := claims.Get("decimal").Int64(); ok {
			t.Fatal("fraction accepted as integer")
		}
	}
}

func TestClaimsOwnImmutableValues(t *testing.T) {
	raw := []byte(`{"id":42,"role":"ad\u006din","enabled":true,"ratio":1.25,"nested":{"id":9007199254740993},"items":[1,null],"nothing":null}`)
	claims, err := parseClaims(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if claims.ID() != 42 || claims.String("role") != "admin" || !claims.Bool("enabled") || claims.Float("ratio") != 1.25 {
		t.Fatal("decoded values changed")
	}
	if claims.Get("nested").Raw() != `{"id":9007199254740993}` || claims.Get("items").Raw() != `[1,null]` {
		t.Fatal("nested values changed")
	}
	if !claims.Has("nothing") || claims.Get("nothing").Raw() != "null" || claims.Has("missing") {
		t.Fatal("presence was lost")
	}
	if claims.Get("missing").Raw() != "" {
		t.Fatal("missing value was invented")
	}
	if claims.Int("role") != 0 || claims.String("id") != "" || claims.Bool("id") || claims.Float("role") != 0 {
		t.Fatal("wrong types were coerced")
	}
	if claims.Float("id") != 42 {
		t.Fatal("explicit Float conversion failed")
	}
}

func TestClaimsRejectInvalidObjects(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `{"id":1,"id":2}`, `{"id":1,"\u0069d":2}`, `{"id":1} false`, `{"role":"\uD800"}`, `{"\uD800":1}`, `{"id":01}`, "{\"role\":\"\xff\"}"} {
		if claims, err := parseClaims([]byte(raw)); err == nil || claims.data != nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestClaimsIntegerTypeBoundary(t *testing.T) {
	for _, value := range []string{"true", "null", `"42"`, "1.5", "1e0", "9223372036854775808", "-9223372036854775809"} {
		claims, err := parseClaims([]byte(`{"id":` + value + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := claims.Get("id").Int64(); ok {
			t.Errorf("accepted %s as Int64", value)
		}
		if claims.ID() != 0 {
			t.Errorf("coerced %s", value)
		}
	}
}
