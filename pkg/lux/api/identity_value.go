package api

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/shopspring/decimal"
)

// Typed identity readers share the request boundary parsers. They consume the
// retained JSON token directly, never marshal an intermediate object.
func ReadIdentityDuration(value auth.ClaimValue) (time.Duration, error) {
	duration, valid := value.Int64()
	if !valid {
		return 0, errors.Unauthorized
	}
	return time.Duration(duration), nil
}

func ReadIdentityDateTime(value auth.ClaimValue) (time.Time, error) {
	return jsonutil.ParseDateTime([]byte(value.Raw()))
}

func ReadIdentityUUID(value auth.ClaimValue) (uuid.UUID, error) {
	var reader jsonUUIDArrayReader
	return reader.parse([]byte(value.Raw()))
}

func ReadIdentityDecimal(value auth.ClaimValue) (decimal.Decimal, error) {
	return parseDecimalParam([]byte(value.Raw()))
}

func ReadIdentityBytes(value auth.ClaimValue) ([]byte, error) {
	return jsonutil.ParseBase64([]byte(value.Raw()))
}

func ReadIdentityJSON(value auth.ClaimValue) (json.RawMessage, error) {
	raw := value.Raw()
	if raw == "" || raw == "null" {
		return nil, errors.Unauthorized
	}
	// ClaimValue is already strict JSON; return an owned mutable copy.
	return json.RawMessage(raw), nil
}

func ReadIdentityIntArray(value auth.ClaimValue) ([]int64, error) {
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, jsonutil.ParseInt64)
}

func ReadIdentityStringArray(value auth.ClaimValue) ([]string, error) {
	return jsonutil.ParseStringArrayLimit([]byte(value.Raw()), codec.MaxArrayElements)
}

// Membership is checked by generated code without interface dispatch or a
// temporary string slice. The retained claim is parsed only once.
func ReadIdentityEnumArray[T ~string](value auth.ClaimValue) ([]T, error) {
	return jsonutil.ParseStringValuesLimit[T]([]byte(value.Raw()), codec.MaxArrayElements)
}

func ReadIdentityBooleanArray(value auth.ClaimValue) ([]bool, error) {
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, jsonutil.ParseBool)
}

func ReadIdentityFloatArray(value auth.ClaimValue) ([]float64, error) {
	return jsonutil.ParseFloat64ArrayLimit([]byte(value.Raw()), codec.MaxArrayElements)
}

func ReadIdentityDurationArray(value auth.ClaimValue) ([]time.Duration, error) {
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, parseDurationParam)
}

func ReadIdentityDateTimeArray(value auth.ClaimValue) ([]time.Time, error) {
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, jsonutil.ParseDateTime)
}

func ReadIdentityUUIDArray(value auth.ClaimValue) ([]uuid.UUID, error) {
	var reader jsonUUIDArrayReader
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, reader.parse)
}

func ReadIdentityDecimalArray(value auth.ClaimValue) ([]decimal.Decimal, error) {
	return parseParamArray([]byte(value.Raw()), codec.MaxArrayElements, parseDecimalArrayValue)
}

func ReadIdentityBytesArray(value auth.ClaimValue) ([][]byte, error) {
	return jsonutil.ParseBase64Array([]byte(value.Raw()), codec.MaxArrayElements)
}

func ReadIdentityJSONArray(value auth.ClaimValue) ([]json.RawMessage, error) {
	// The raw array reader borrows its input; this conversion owns the backing bytes.
	return jsonutil.ParseRawArray([]byte(value.Raw()), codec.MaxArrayElements)
}
