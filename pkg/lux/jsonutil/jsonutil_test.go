package jsonutil

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

func TestValidUTF8JSON(t *testing.T) {
	for _, raw := range []string{"null", "-0", "1e99999", "[null,9223372036854775808]", `{"a":1,"a":2,"text":"精度"}`} {
		if !ValidUTF8JSON([]byte(raw)) {
			t.Errorf("rejected valid raw JSON %s", raw)
		}
	}
	for _, raw := range [][]byte{nil, []byte(""), []byte("{"), []byte("NaN"), []byte("true false"), {'"', 0xff, '"'}} {
		if ValidUTF8JSON(raw) {
			t.Errorf("accepted invalid raw JSON %q", raw)
		}
	}
}

func TestCanonicalJSONUnicode(t *testing.T) {
	data, err := os.ReadFile("../codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		JSONUnicodeCases []struct {
			Name, Raw string
			Valid     bool
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.JSONUnicodeCases) == 0 {
		t.Fatal("missing canonical Unicode cases")
	}
	for _, item := range fixture.JSONUnicodeCases {
		t.Run(item.Name, func(t *testing.T) {
			raw := []byte(item.Raw)
			if got := ValidUTF8JSON(raw); got != item.Valid {
				t.Errorf("valid=%t, want %t for %s", got, item.Valid, item.Raw)
			}
			if string(raw) != item.Raw {
				t.Fatal("validation mutated the original JSON")
			}
		})
	}
}

func TestCanonicalJSONNumberConversions(t *testing.T) {
	data, err := os.ReadFile("../codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		JSONNumberConversions []struct {
			Token              string
			Int64, Float64Bits *string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.JSONNumberConversions) == 0 {
		t.Fatal("missing canonical JSON number conversions")
	}
	for _, item := range fixture.JSONNumberConversions {
		t.Run(item.Token, func(t *testing.T) {
			raw := []byte(item.Token)
			if !json.Valid(raw) {
				t.Fatal("conversion fixture must contain valid JSON")
			}
			integer, intErr := ParseInt64(raw)
			if item.Int64 == nil {
				if intErr == nil {
					t.Fatal("accepted non-Int64 token")
				}
			} else if intErr != nil || strconv.FormatInt(integer, 10) != *item.Int64 {
				t.Fatalf("integer = %d, error = %v", integer, intErr)
			}
			value, floatErr := ParseFloat64(raw)
			if item.Float64Bits == nil {
				if floatErr == nil {
					t.Fatal("accepted out-of-range Float token")
				}
			} else if floatErr != nil || fmt.Sprintf("%016x", math.Float64bits(value)) != *item.Float64Bits {
				t.Fatalf("Float bits = %016x, error = %v", math.Float64bits(value), floatErr)
			}
		})
	}
}

func TestParseFloat64(t *testing.T) {
	for _, token := range []string{"0", "-0", "0.00", "0e99999", "-0E-99999", "1.25", "1E+03", "5e-324", "-5e-324",
		"2.4703282292062328e-324", "1.7976931348623157e308", "9223372036854775808", " \n1.25\r\t"} {
		var want float64
		if err := json.Unmarshal([]byte(token), &want); err != nil {
			t.Fatal(err)
		}
		got, err := ParseFloat64([]byte(token))
		if err != nil || math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("%q: got %g, err %v", token, got, err)
		}
	}
	for _, token := range []string{"", "-", "+1", "01", "-01", ".1", "1.", "1e", "1e+", "1f", "0x1p0", "1_0", "١",
		"NaN", "Infinity", "null", "true", `"1"`, "[]", "{}", "1 2", "1e400", "-1e400", "1e-400", "-1e-400", "2.4703282292062327e-324"} {
		if _, err := ParseFloat64([]byte(token)); err == nil {
			t.Errorf("accepted %q", token)
		}
	}
}

func FuzzParseFloat64(f *testing.F) {
	for _, token := range []string{"0", "-0", "1e-400", "5e-324", "1.25", "null", "1e400", `"1"`} {
		f.Add([]byte(token))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := ParseFloat64(raw)
		if err != nil {
			return
		}
		var want float64
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("accepted malformed numeric token %q", raw)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("different rounding for %q", raw)
		}
	})
}

type testStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestParse(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		var s testStruct
		err := Parse(`{"name":"Alice","age":30}`, &s)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if s.Name != "Alice" || s.Age != 30 {
			t.Errorf("Parse() = %+v, want {Name:Alice Age:30}", s)
		}
	})
	t.Run("map", func(t *testing.T) {
		var m map[string]any
		err := Parse(`{"key":"value"}`, &m)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if m["key"] != "value" {
			t.Errorf("Parse() map[key] = %v, want value", m["key"])
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		var s testStruct
		err := Parse("not json", &s)
		if err == nil {
			t.Errorf("Parse() expected error for invalid JSON")
		}
	})
}

func TestParseTo(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s, err := ParseTo[testStruct](`{"name":"Bob","age":25}`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if s.Name != "Bob" || s.Age != 25 {
			t.Errorf("ParseTo() = %+v, want {Name:Bob Age:25}", s)
		}
	})
	t.Run("slice", func(t *testing.T) {
		s, err := ParseTo[[]int](`[1,2,3]`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if len(s) != 3 || s[0] != 1 || s[1] != 2 || s[2] != 3 {
			t.Errorf("ParseTo() = %v, want [1 2 3]", s)
		}
	})
	t.Run("primitive", func(t *testing.T) {
		s, err := ParseTo[string](`"hello"`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if s != "hello" {
			t.Errorf("ParseTo() = %q, want %q", s, "hello")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseTo[testStruct]("bad")
		if err == nil {
			t.Errorf("ParseTo() expected error for invalid JSON")
		}
	})
}

func TestStringify(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s := testStruct{Name: "Alice", Age: 30}
		got, err := Stringify(s)
		if err != nil {
			t.Fatalf("Stringify() error = %v", err)
		}
		want := `{"name":"Alice","age":30}`
		if got != want {
			t.Errorf("Stringify() = %q, want %q", got, want)
		}
	})
	t.Run("slice", func(t *testing.T) {
		got, err := Stringify([]int{1, 2, 3})
		if err != nil {
			t.Fatalf("Stringify() error = %v", err)
		}
		if got != "[1,2,3]" {
			t.Errorf("Stringify() = %q, want %q", got, "[1,2,3]")
		}
	})
	t.Run("unmarshalable", func(t *testing.T) {
		// Channels cannot be marshaled
		ch := make(chan int)
		_, err := Stringify(ch)
		if err == nil {
			t.Errorf("Stringify() expected error for channel")
		}
	})
}

func TestPrettyPrint(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s := testStruct{Name: "Alice", Age: 30}
		got, err := PrettyPrint(s)
		if err != nil {
			t.Fatalf("PrettyPrint() error = %v", err)
		}
		want := "{\n  \"name\": \"Alice\",\n  \"age\": 30\n}"
		if got != want {
			t.Errorf("PrettyPrint() = %q, want %q", got, want)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got, err := PrettyPrint(nil)
		if err != nil {
			t.Fatalf("PrettyPrint() error = %v", err)
		}
		if got != "null" {
			t.Errorf("PrettyPrint(nil) = %q, want %q", got, "null")
		}
	})
	t.Run("unmarshalable", func(t *testing.T) {
		ch := make(chan int)
		_, err := PrettyPrint(ch)
		if err == nil {
			t.Errorf("PrettyPrint() expected error for channel")
		}
	})
}
