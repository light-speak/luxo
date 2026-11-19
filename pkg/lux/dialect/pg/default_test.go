package pg

import (
	"math"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
)

func TestTypedDefaultSQL(t *testing.T) {
	for _, sample := range []struct {
		value lux.ColumnDefault
		want  string
	}{
		{lux.ColumnDefault{Kind: lux.DefaultNull}, "NULL"},
		{lux.ColumnDefault{Kind: lux.DefaultString, Text: "null"}, "'null'"},
		{lux.ColumnDefault{Kind: lux.DefaultString, Text: "O'Reilly"}, "'O''Reilly'"},
		{lux.ColumnDefault{Kind: lux.DefaultString, Text: ""}, "''"},
		{lux.ColumnDefault{Kind: lux.DefaultString, Text: `O'Reilly\folder`}, `E'O''Reilly\\folder'`},
		{lux.ColumnDefault{Kind: lux.DefaultInt, Int: math.MinInt64}, "-9223372036854775808"},
		{lux.ColumnDefault{Kind: lux.DefaultFloat, Float: math.Copysign(0, -1)}, "'-0'::DOUBLE PRECISION"},
		{lux.ColumnDefault{Kind: lux.DefaultFloat, Float: 0.1}, "'0.1'::DOUBLE PRECISION"},
		{lux.ColumnDefault{Kind: lux.DefaultFloat, Float: math.SmallestNonzeroFloat64}, "'5e-324'::DOUBLE PRECISION"},
		{lux.ColumnDefault{Kind: lux.DefaultBoolean, Bool: true}, "TRUE"},
		{lux.ColumnDefault{Kind: lux.DefaultBoolean}, "FALSE"},
		{lux.ColumnDefault{Kind: lux.DefaultDuration, Int: int64(-24 * time.Hour)}, "INTERVAL '-86400000000 microseconds'"},
		{lux.ColumnDefault{Kind: lux.DefaultJSONNull}, "'null'::JSONB"},
	} {
		got, err := (Dialect{}).DefaultSQL(sample.value)
		if err != nil || got != sample.want {
			t.Errorf("DefaultSQL(%+v) = %q, %v; want %q", sample.value, got, err, sample.want)
		}
	}
}

func TestTypedDefaultSQLRejectsInvalidValues(t *testing.T) {
	for _, value := range []lux.ColumnDefault{
		{Kind: lux.DefaultFloat, Float: math.Inf(1)},
		{Kind: lux.DefaultFloat, Float: math.NaN()},
		{Kind: lux.DefaultDuration, Int: 1},
		{Kind: lux.DefaultString, Text: "a\x00b"},
		{Kind: lux.DefaultString, Text: string([]byte{0xff})},
		{Kind: 255},
	} {
		if got, err := (Dialect{}).DefaultSQL(value); err == nil || got != "" {
			t.Errorf("invalid default became SQL: %+v = %q, %v", value, got, err)
		}
	}
}
