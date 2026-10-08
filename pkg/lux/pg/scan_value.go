package pg

import (
	"errors"
	"math"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// These concrete scanner values implement the driver's typed scan interfaces.
// They are local reading intermediates, never fields in generated domain types.
type (
	Int64Value    = pgtype.Int8
	Float64Value  = pgtype.Float8
	TextValue     = pgtype.Text
	BoolValue     = pgtype.Bool
	TimeValue     = pgtype.Timestamptz
	DurationValue = pgtype.Interval
	UUIDValue     = pgtype.UUID
	DecimalValue  = pgtype.Numeric
)

var errInvalidScanValue = errors.New("pg: value is not representable by its Luxo type")

// DecimalFromValue preserves the numeric coefficient and exponent without a
// textual encoding roundtrip, and owns a copy of the mutable coefficient.
func DecimalFromValue(value DecimalValue) (decimal.Decimal, error) {
	if !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil {
		return decimal.Decimal{}, errInvalidScanValue
	}
	return decimal.NewFromBigInt(value.Int, value.Exp), nil
}

// DateTimeFromValue rejects database infinity and values outside the protocol.
func DateTimeFromValue(value TimeValue) (time.Time, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return time.Time{}, errInvalidScanValue
	}
	result := value.Time.UTC()
	if result.Year() < 0 || result.Year() > 9999 {
		return time.Time{}, errInvalidScanValue
	}
	return result, nil
}

// DurationFromValue cannot assign a fixed length to calendar months. PostgreSQL
// stores microseconds; this conversion does not claim nanosecond DB precision.
func DurationFromValue(value DurationValue) (time.Duration, error) {
	if !value.Valid || value.Months != 0 {
		return 0, errInvalidScanValue
	}
	micros, ok := intervalMicroseconds(value)
	if !ok || micros < math.MinInt64/1000 || micros > math.MaxInt64/1000 {
		return 0, errInvalidScanValue
	}
	return time.Duration(micros * 1000), nil
}

func intervalMicroseconds(value DurationValue) (int64, bool) {
	const perDay = int64(24 * time.Hour / time.Microsecond)
	days := int64(value.Days)
	if days >= math.MinInt64/perDay && days <= math.MaxInt64/perDay {
		daily := days * perDay
		if !(daily > 0 && value.Microseconds > math.MaxInt64-daily || daily < 0 && value.Microseconds < math.MinInt64-daily) {
			return daily + value.Microseconds, true
		}
	}
	// Huge, oppositely signed components may cancel into a valid duration.
	// Keep the common small interval path free from big integer arithmetic.
	var total, unit, micros big.Int
	total.SetInt64(days)
	unit.SetInt64(perDay)
	micros.SetInt64(value.Microseconds)
	total.Mul(&total, &unit).Add(&total, &micros)
	return total.Int64(), total.IsInt64()
}
