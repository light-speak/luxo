package event

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestSnapshotScalarDomains(t *testing.T) {
	for _, test := range []struct {
		name  string
		check func() error
		want  error
	}{
		{"text", func() error { return CheckString("東京") }, nil},
		{"invalid text", func() error { return CheckString("\xff") }, codec.ErrInvalidUTF8},
		{"finite", func() error { return CheckFloat(math.MaxFloat64) }, nil},
		{"negative zero", func() error { return CheckFloat(math.Copysign(0, -1)) }, nil},
		{"subnormal", func() error { return CheckFloat(math.SmallestNonzeroFloat64) }, nil},
		{"nan", func() error { return CheckFloat(math.NaN()) }, codec.ErrNonFiniteFloat},
		{"infinity", func() error { return CheckFloat(math.Inf(1)) }, codec.ErrNonFiniteFloat},
		{"negative infinity", func() error { return CheckFloat(math.Inf(-1)) }, codec.ErrNonFiniteFloat},
		{"nanoseconds", func() error { return CheckDateTime(time.Unix(-1, 123456789)) }, nil},
		{"year zero", func() error { return CheckDateTime(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)) }, nil},
		{"date overflow", func() error { return CheckDateTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)) }, codec.ErrInvalidDateTime},
		{"json", func() error { return CheckJSON([]byte(`{"n":123456789012345678901234567890}`)) }, nil},
		{"json null", func() error { return CheckJSON([]byte(`null`)) }, nil},
		{"broken json", func() error { return CheckJSON([]byte(`{`)) }, codec.ErrInvalidJSON},
		{"broken unicode", func() error { return CheckJSON([]byte(`"\uD800"`)) }, codec.ErrInvalidJSON},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.check(); !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}
