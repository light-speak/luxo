package pg

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/light-speak/luxo/pkg/lux/datetime"
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

// DurationScanValue validates interval components before writing domain storage.
type DurationScanValue time.Duration

func (value *DurationScanValue) ScanInterval(source DurationValue) error {
	if value == nil {
		return errInvalidScanValue
	}
	converted, err := DurationFromValue(source)
	if err != nil {
		return err
	}
	*value = DurationScanValue(converted)
	return nil
}

type durationPointer struct{ target **time.Duration }

// DurationPointer scans nullable intervals directly into a model pointer field.
// Its single-pointer value avoids allocating a separate scanner wrapper.
func DurationPointer(target **time.Duration) durationPointer { return durationPointer{target: target} }

func (value durationPointer) ScanInterval(source DurationValue) error {
	if value.target == nil {
		return errInvalidScanValue
	}
	if !source.Valid {
		*value.target = nil
		return nil
	}
	converted, err := DurationFromValue(source)
	if err != nil {
		return err
	}
	*value.target = new(converted)
	return nil
}

// DurationListValue writes checked elements directly into domain slice storage.
type DurationListValue []time.Duration

func (value *DurationListValue) SetDimensions(dimensions []pgtype.ArrayDimension) error {
	return (*pgtype.FlatArray[time.Duration])(value).SetDimensions(dimensions)
}

func (value *DurationListValue) ScanIndex(index int) any {
	return (*DurationScanValue)(&(*value)[index])
}

func (value *DurationListValue) ScanIndexType() any { return (*DurationScanValue)(nil) }

var errInvalidScanValue = errors.New("pg: value is not representable by its Luxo type")

// NullableJSONValue owns database bytes and distinguishes SQL NULL from JSON null.
// It implements pgx's BytesScanner without decoding JSON into a dynamic value.
type NullableJSONValue struct{ raw json.RawMessage }

func (value *NullableJSONValue) ScanBytes(src []byte) error {
	value.raw = bytes.Clone(src)
	return nil
}

func (value *NullableJSONValue) Pointer() *json.RawMessage {
	if value.raw == nil {
		return nil
	}
	return &value.raw
}

// JSONListValue scans directly into domain storage while rejecting SQL NULL
// elements. JSON literal null remains a valid, owned raw JSON value.
type JSONListValue []json.RawMessage

func (value *JSONListValue) SetDimensions(dimensions []pgtype.ArrayDimension) error {
	return (*pgtype.FlatArray[json.RawMessage])(value).SetDimensions(dimensions)
}

func (value *JSONListValue) ScanIndex(index int) any {
	return (*requiredJSONValue)(&(*value)[index])
}

func (value *JSONListValue) ScanIndexType() any {
	return (*requiredJSONValue)(nil)
}

type requiredJSONValue json.RawMessage

// JSONValue scans required JSON without collapsing SQL NULL into a JSON value.
// Its storage can be converted directly from a domain json.RawMessage pointer.
type JSONValue = requiredJSONValue

func (value *requiredJSONValue) ScanBytes(source []byte) error {
	if source == nil {
		return errInvalidScanValue
	}
	*value = bytes.Clone(source)
	return nil
}

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
	if ValidateDateTime(result) != nil {
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

// ValidateFloat rejects values outside the finite Luxo Float domain.
func ValidateFloat(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return errInvalidScanValue
	}
	return nil
}

// ValidateDateTime applies the UTC protocol range without changing the instant.
func ValidateDateTime(value time.Time) error {
	if !datetime.ValidSeconds(value.Unix()) {
		return errInvalidScanValue
	}
	return nil
}

// FloatFromValue validates typed grouped results before exposing them to business code.
func FloatFromValue(value Float64Value) (float64, error) {
	if !value.Valid {
		return 0, errInvalidScanValue
	}
	if err := ValidateFloat(value.Float64); err != nil {
		return 0, err
	}
	return value.Float64, nil
}
