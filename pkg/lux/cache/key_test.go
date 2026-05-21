package cache

import (
	"testing"
)

func TestBuildKey_Empty(t *testing.T) {
	key := BuildKey("getUser", nil)
	if key != "luxo:api:getUser:" {
		t.Errorf("empty params key = %q", key)
	}
	key2 := BuildKey("getUser", map[string]any{})
	if key2 != "luxo:api:getUser:" {
		t.Errorf("empty map key = %q", key2)
	}
}

func TestBuildKey_Deterministic(t *testing.T) {
	params := map[string]any{"id": int64(42), "name": "test"}
	k1 := BuildKey("getUser", params)
	k2 := BuildKey("getUser", params)
	if k1 != k2 {
		t.Errorf("same params should produce same key: %q vs %q", k1, k2)
	}
}

func TestBuildKey_DifferentParams(t *testing.T) {
	k1 := BuildKey("getUser", map[string]any{"id": int64(1)})
	k2 := BuildKey("getUser", map[string]any{"id": int64(2)})
	if k1 == k2 {
		t.Error("different params should produce different keys")
	}
}

func TestBuildKey_DifferentAPIs(t *testing.T) {
	params := map[string]any{"id": int64(1)}
	k1 := BuildKey("getUser", params)
	k2 := BuildKey("getPost", params)
	if k1 == k2 {
		t.Error("different APIs should produce different keys")
	}
}

func TestBuildKey_OrderIndependent(t *testing.T) {
	// Maps have random iteration order, but BuildKey sorts keys.
	k1 := BuildKey("api", map[string]any{"a": "1", "b": "2", "c": "3"})
	k2 := BuildKey("api", map[string]any{"c": "3", "a": "1", "b": "2"})
	if k1 != k2 {
		t.Errorf("key should be order-independent: %q vs %q", k1, k2)
	}
}

func TestBuildKey_AllTypes(t *testing.T) {
	params := map[string]any{
		"str":   "hello",
		"int":   int64(42),
		"float": float64(3.14),
		"bool":  true,
		"bytes": []byte("raw"),
		"nil":   nil,
	}
	key := BuildKey("test", params)
	if key == "" {
		t.Error("key should not be empty")
	}
	// Key should be deterministic
	key2 := BuildKey("test", params)
	if key != key2 {
		t.Errorf("all-type params should be deterministic: %q vs %q", key, key2)
	}
}

func TestBuildKey_Prefix(t *testing.T) {
	key := BuildKey("getUser", map[string]any{"id": int64(1)})
	if len(key) < len("luxo:api:getUser:") {
		t.Errorf("key too short: %q", key)
	}
	if key[:len("luxo:api:getUser:")] != "luxo:api:getUser:" {
		t.Errorf("key should start with prefix: %q", key)
	}
}

func TestBuildKeyRaw_Empty(t *testing.T) {
	key := BuildKeyRaw("getUser", nil)
	if key != "luxo:api:getUser:" {
		t.Errorf("empty raw key = %q", key)
	}
}

func TestBuildKeyRaw_Deterministic(t *testing.T) {
	raw := []byte{1, 2, 3, 4}
	k1 := BuildKeyRaw("getUser", raw)
	k2 := BuildKeyRaw("getUser", raw)
	if k1 != k2 {
		t.Errorf("same raw should produce same key: %q vs %q", k1, k2)
	}
}

func TestBuildKeyRaw_Different(t *testing.T) {
	k1 := BuildKeyRaw("getUser", []byte{1, 2})
	k2 := BuildKeyRaw("getUser", []byte{3, 4})
	if k1 == k2 {
		t.Error("different raw should produce different keys")
	}
}

func TestBuildKey_IntTypes(t *testing.T) {
	// Go untyped integer (map[string]any{"id": 1}) produces int, not int64.
	// All integer types with the same value should produce the same hash.
	base := BuildKey("api", map[string]any{"id": int(42)})
	cases := []struct {
		name string
		val  any
	}{
		{"int", int(42)},
		{"int8", int8(42)},
		{"int16", int16(42)},
		{"int32", int32(42)},
		{"int64", int64(42)},
		{"uint", uint(42)},
		{"uint8", uint8(42)},
		{"uint16", uint16(42)},
		{"uint32", uint32(42)},
		{"uint64", uint64(42)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := BuildKey("api", map[string]any{"id": tc.val})
			if key != base {
				t.Errorf("%s: got %q, want %q", tc.name, key, base)
			}
		})
	}
}

func TestBuildKey_IntVsDefault(t *testing.T) {
	// Before the fix, int would fall through to default "?" branch.
	// Verify int(1) and int(2) produce different keys (not both "?").
	k1 := BuildKey("api", map[string]any{"id": int(1)})
	k2 := BuildKey("api", map[string]any{"id": int(2)})
	if k1 == k2 {
		t.Error("int(1) and int(2) should produce different keys")
	}
}

func TestBuildKey_Float32(t *testing.T) {
	k1 := BuildKey("api", map[string]any{"val": float32(1.5)})
	k2 := BuildKey("api", map[string]any{"val": float32(2.5)})
	if k1 == k2 {
		t.Error("different float32 values should produce different keys")
	}
}

func TestInvalidatePrefix(t *testing.T) {
	prefix := InvalidatePrefix("User")
	if prefix != "luxo:api:user" {
		t.Errorf("InvalidatePrefix = %q, want %q", prefix, "luxo:api:user")
	}
}
