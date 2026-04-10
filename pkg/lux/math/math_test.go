package math

import (
	gomath "math"
	"testing"
)

func TestClamp(t *testing.T) {
	if Clamp(5, 0, 10) != 5 {
		t.Error("in range")
	}
	if Clamp(-1, 0, 10) != 0 {
		t.Error("below min")
	}
	if Clamp(15, 0, 10) != 10 {
		t.Error("above max")
	}
}

func TestClampInt(t *testing.T) {
	if ClampInt(5, 0, 10) != 5 {
		t.Error("in range")
	}
	if ClampInt(-1, 0, 10) != 0 {
		t.Error("below min")
	}
	if ClampInt(15, 0, 10) != 10 {
		t.Error("above max")
	}
}

func TestAbsInt(t *testing.T) {
	if AbsInt(5) != 5 {
		t.Error("positive")
	}
	if AbsInt(-5) != 5 {
		t.Error("negative")
	}
	if AbsInt(0) != 0 {
		t.Error("zero")
	}
	if AbsInt(gomath.MinInt64) != gomath.MaxInt64 {
		t.Error("MinInt64 overflow guard")
	}
}

func TestRandom(t *testing.T) {
	v := Random()
	if v < 0 || v >= 1 {
		t.Errorf("Random() = %f, want [0, 1)", v)
	}
}

func TestRandInt(t *testing.T) {
	for range 100 {
		v := RandInt(5, 10)
		if v < 5 || v > 10 {
			t.Errorf("RandInt(5,10) = %d", v)
		}
	}
	if RandInt(7, 7) != 7 {
		t.Error("equal bounds")
	}
	if RandInt(10, 5) != 10 {
		t.Error("inverted bounds")
	}
}
