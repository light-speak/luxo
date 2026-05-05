package convert

import (
	gomath "math"
	"testing"
)

func TestToString(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{42, "42"},
		{3.14, "3.14"},
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{nil, "<nil>"},
		{int64(100), "100"},
		{[]int{1, 2}, "[1 2]"},
	}
	for _, tt := range tests {
		if got := ToString(tt.input); got != tt.want {
			t.Errorf("ToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToInt(t *testing.T) {
	t.Run("valid conversions", func(t *testing.T) {
		tests := []struct {
			name  string
			input any
			want  int64
		}{
			{"int64", int64(42), 42},
			{"int", int(42), 42},
			{"int8", int8(42), 42},
			{"int16", int16(42), 42},
			{"int32", int32(42), 42},
			{"uint", uint(42), 42},
			{"uint8", uint8(42), 42},
			{"uint16", uint16(42), 42},
			{"uint32", uint32(42), 42},
			{"uint64", uint64(42), 42},
			{"float64", float64(3.9), 4}, // rounds to nearest
			{"float32", float32(3.9), 4},
			{"string", "42", 42},
			{"bool true", true, 1},
			{"bool false", false, 0},
		}
		for _, tt := range tests {
			got, err := ToInt(tt.input)
			if err != nil {
				t.Errorf("ToInt(%v) [%s] error = %v", tt.input, tt.name, err)
				continue
			}
			if got != tt.want {
				t.Errorf("ToInt(%v) [%s] = %v, want %v", tt.input, tt.name, got, tt.want)
			}
		}
	})
	t.Run("invalid string", func(t *testing.T) {
		_, err := ToInt("not a number")
		if err == nil {
			t.Errorf("ToInt(\"not a number\") expected error")
		}
	})
	t.Run("unsupported type", func(t *testing.T) {
		_, err := ToInt([]int{1})
		if err == nil {
			t.Errorf("ToInt([]int) expected error")
		}
	})
}

func TestToFloat(t *testing.T) {
	t.Run("valid conversions", func(t *testing.T) {
		tests := []struct {
			name  string
			input any
			want  float64
		}{
			{"float64", float64(3.14), 3.14},
			{"float32", float32(3.0), 3.0},
			{"int", int(42), 42.0},
			{"int8", int8(42), 42.0},
			{"int16", int16(42), 42.0},
			{"int32", int32(42), 42.0},
			{"int64", int64(42), 42.0},
			{"uint", uint(42), 42.0},
			{"uint8", uint8(42), 42.0},
			{"uint16", uint16(42), 42.0},
			{"uint32", uint32(42), 42.0},
			{"uint64", uint64(42), 42.0},
			{"string", "3.14", 3.14},
			{"bool true", true, 1.0},
			{"bool false", false, 0.0},
		}
		for _, tt := range tests {
			got, err := ToFloat(tt.input)
			if err != nil {
				t.Errorf("ToFloat(%v) [%s] error = %v", tt.input, tt.name, err)
				continue
			}
			if got != tt.want {
				t.Errorf("ToFloat(%v) [%s] = %v, want %v", tt.input, tt.name, got, tt.want)
			}
		}
	})
	t.Run("invalid string", func(t *testing.T) {
		_, err := ToFloat("abc")
		if err == nil {
			t.Errorf("ToFloat(\"abc\") expected error")
		}
	})
	t.Run("unsupported type", func(t *testing.T) {
		_, err := ToFloat([]int{1})
		if err == nil {
			t.Errorf("ToFloat([]int) expected error")
		}
	})
}

func TestToBool(t *testing.T) {
	t.Run("valid conversions", func(t *testing.T) {
		tests := []struct {
			name  string
			input any
			want  bool
		}{
			{"bool true", true, true},
			{"bool false", false, false},
			{"string true", "true", true},
			{"string TRUE", "TRUE", true},
			{"string 1", "1", true},
			{"string t", "t", true},
			{"string false", "false", false},
			{"string FALSE", "FALSE", false},
			{"string 0", "0", false},
			{"string f", "f", false},
			{"int non-zero", int(42), true},
			{"int zero", int(0), false},
			{"int64 non-zero", int64(1), true},
			{"int64 zero", int64(0), false},
			{"float64 non-zero", float64(1.5), true},
			{"float64 zero", float64(0), false},
		}
		for _, tt := range tests {
			got, err := ToBool(tt.input)
			if err != nil {
				t.Errorf("ToBool(%v) [%s] error = %v", tt.input, tt.name, err)
				continue
			}
			if got != tt.want {
				t.Errorf("ToBool(%v) [%s] = %v, want %v", tt.input, tt.name, got, tt.want)
			}
		}
	})
	t.Run("invalid string", func(t *testing.T) {
		_, err := ToBool("maybe")
		if err == nil {
			t.Errorf("ToBool(\"maybe\") expected error")
		}
	})
	t.Run("unsupported type", func(t *testing.T) {
		_, err := ToBool([]int{1})
		if err == nil {
			t.Errorf("ToBool([]int) expected error")
		}
	})
}

func TestIntToString(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{42, "42"},
		{-100, "-100"},
		{9223372036854775807, "9223372036854775807"},
	}
	for _, tt := range tests {
		if got := IntToString(tt.input); got != tt.want {
			t.Errorf("IntToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFloatToString(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0"},
		{3.14, "3.14"},
		{-1.5, "-1.5"},
		{100, "100"},
		{0.1, "0.1"},
	}
	for _, tt := range tests {
		if got := FloatToString(tt.input); got != tt.want {
			t.Errorf("FloatToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToIntUint64Overflow(t *testing.T) {
	_, err := ToInt(uint64(gomath.MaxInt64 + 1))
	if err == nil {
		t.Fatal("should error on uint64 overflow")
	}
}

func TestToIntFloat64Rounds(t *testing.T) {
	v, err := ToInt(float64(1.9))
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Errorf("ToInt(1.9) = %d, want 2 (rounded)", v)
	}
}

func TestToIntNaN(t *testing.T) {
	_, err := ToInt(gomath.NaN())
	if err == nil {
		t.Error("expected error for NaN")
	}
}

func TestToIntInf(t *testing.T) {
	_, err := ToInt(gomath.Inf(1))
	if err == nil {
		t.Error("expected error for +Inf")
	}
	_, err = ToInt(gomath.Inf(-1))
	if err == nil {
		t.Error("expected error for -Inf")
	}
}

func TestToIntFloat32NaN(t *testing.T) {
	_, err := ToInt(float32(gomath.NaN()))
	if err == nil {
		t.Error("expected error for float32 NaN")
	}
}

func TestToIntFloat64Overflow(t *testing.T) {
	_, err := ToInt(float64(1e19))
	if err == nil {
		t.Error("expected error for float64 overflow")
	}
}

func TestBoolToString(t *testing.T) {
	if got := BoolToString(true); got != "true" {
		t.Errorf("BoolToString(true) = %q, want %q", got, "true")
	}
	if got := BoolToString(false); got != "false" {
		t.Errorf("BoolToString(false) = %q, want %q", got, "false")
	}
}

func TestStringToInt(t *testing.T) {
	if got := StringToInt("42"); got != 42 {
		t.Errorf("got %d", got)
	}
	if got := StringToInt("invalid"); got != 0 {
		t.Errorf("invalid should return 0, got %d", got)
	}
	if got := StringToInt(""); got != 0 {
		t.Errorf("empty should return 0, got %d", got)
	}
}

func TestStringToFloat(t *testing.T) {
	if got := StringToFloat("3.14"); got != 3.14 {
		t.Errorf("got %f", got)
	}
	if got := StringToFloat("invalid"); got != 0 {
		t.Errorf("invalid should return 0, got %f", got)
	}
}
