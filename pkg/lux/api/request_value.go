package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/shopspring/decimal"
)

// Preparation can be retried on the same Request. Never expose a previous
// canonical body or partially prepared messages after a failed attempt.
func (r *Request) clearPreparedJSON() {
	if r.paramNames != nil {
		clear(r.paramSlots[:])
		r.paramNames = nil
		r.paramCount = 0
		r.paramSet = 0
		r.paramNull = 0
	}
	r.binaryRequest = nil
	r.binaryParams = nil
}

// ParamDuration reads signed nanoseconds without a float64 intermediate.
func (r *Request) ParamDuration(name string) (time.Duration, error) {
	return readParamValue(r, name, parseDurationParam)
}

// ParamUUID reads a UUID from JSON text or its native binary slot.
func (r *Request) ParamUUID(name string) (uuid.UUID, error) {
	var reader jsonUUIDArrayReader
	return readParamValue(r, name, func(raw []byte) (uuid.UUID, error) {
		if !jsonutil.ValidUTF8JSON(raw) {
			return uuid.Nil, fmt.Errorf("invalid JSON UUID")
		}
		return reader.parse(bytes.Trim(raw, " \t\r\n"))
	})
}

// ParamDecimal reads decimal text without rounding through float64.
func (r *Request) ParamDecimal(name string) (decimal.Decimal, error) {
	return readParamValue(r, name, parseDecimalParam)
}

// ParamBytes reads Base64 JSON text or bytes borrowed from a binary request.
func (r *Request) ParamBytes(name string) ([]byte, error) {
	return readParamValue(r, name, jsonutil.ParseBase64)
}

// ParamRawJSON returns a validated document borrowed from the request. Numeric
// tokens retain their exact spelling; no arbitrary Go destination is accepted.
func (r *Request) ParamRawJSON(name string) (json.RawMessage, error) {
	return readParamValue(r, name, func(raw []byte) (json.RawMessage, error) {
		if !jsonutil.ValidUTF8JSON(raw) || isJSONNull(raw) {
			return nil, fmt.Errorf("expected a non-null JSON value")
		}
		return raw, nil
	})
}

// ParamFloatArray reads finite Float values without accepting null elements.
func (r *Request) ParamFloatArray(name string) ([]float64, error) {
	return readParamValue(r, name, func(raw []byte) ([]float64, error) {
		return jsonutil.ParseFloat64ArrayLimit(raw, codec.MaxArrayElements)
	})
}

// ParamBoolArray reads Boolean values without coercion.
func (r *Request) ParamBoolArray(name string) ([]bool, error) {
	return readParamArray(r, name, jsonutil.ParseBool)
}

// ParamDateTimeArray retains seconds and nanoseconds at the protocol boundary.
func (r *Request) ParamDateTimeArray(name string) ([]time.Time, error) {
	return readParamArray(r, name, jsonutil.ParseDateTime)
}

// ParamDurationArray reads signed nanosecond values.
func (r *Request) ParamDurationArray(name string) ([]time.Duration, error) {
	return readParamArray(r, name, parseDurationParam)
}

// ParamDecimalArray reads exact decimal strings.
func (r *Request) ParamDecimalArray(name string) ([]decimal.Decimal, error) {
	return readParamArray(r, name, parseDecimalArrayValue)
}

// ParamBytesArray reads Base64 strings or borrows native binary byte slices.
func (r *Request) ParamBytesArray(name string) ([][]byte, error) {
	return readParamValue(r, name, func(raw []byte) ([][]byte, error) {
		return jsonutil.ParseBase64Array(raw, codec.MaxArrayElements)
	})
}

// ParamRawJSONArray borrows exact JSON documents from the request. JSON null
// elements are valid documents; a null outer list is not an array.
func (r *Request) ParamRawJSONArray(name string) ([]json.RawMessage, error) {
	return readParamValue(r, name, func(raw []byte) ([]json.RawMessage, error) {
		return jsonutil.ParseRawArray(raw, codec.MaxArrayElements)
	})
}

// ParamEnumArray reads a named string slice without reflection or an intermediate
// JSON slice. Generated handlers check declared enum members before using it.
func ParamEnumArray[T ~string](r *Request, name string) ([]T, error) {
	if r.Params != nil {
		raw, ok := r.Params[name]
		if !ok {
			return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
		}
		values, err := jsonutil.ParseStringValuesLimit[T](raw, codec.MaxArrayElements)
		if err != nil {
			return nil, InvalidParam(name, err)
		}
		return values, nil
	}
	text, err := r.ParamStringArray(name)
	if err != nil {
		return nil, err
	}
	values := make([]T, len(text))
	for index, value := range text {
		values[index] = T(value)
	}
	return values, nil
}

func readParamValue[T any](r *Request, name string, parse func([]byte) (T, error)) (T, error) {
	var zero T
	if r.Params == nil {
		value, present := r.findParam(name)
		if !present {
			return zero, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
		}
		if typed, ok := value.(T); ok {
			return typed, nil
		}
		return zero, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid binary parameter type"})
	}
	raw, present := r.Params[name]
	if !present {
		return zero, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	value, err := parse(raw)
	if err != nil {
		return zero, InvalidParam(name, err)
	}
	return value, nil
}

func readParamArray[T any](r *Request, name string, parse func([]byte) (T, error)) ([]T, error) {
	return readParamValue(r, name, func(raw []byte) ([]T, error) {
		return parseParamArray(raw, codec.MaxArrayElements, parse)
	})
}

func parseParamArray[T any](raw []byte, limit int, parse func([]byte) (T, error)) ([]T, error) {
	count := 0
	return jsonutil.ParseArray(raw, func(raw []byte) (T, error) {
		if count >= limit {
			var zero T
			return zero, fmt.Errorf("array size exceeds limit %d", limit)
		}
		count++
		return parse(raw)
	})
}

func parseDurationParam(raw []byte) (time.Duration, error) {
	value, err := jsonutil.ParseInt64(raw)
	return time.Duration(value), err
}

func parseDecimalParam(raw []byte) (decimal.Decimal, error) {
	value, err := jsonutil.ParseString(raw)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return decimal.NewFromString(value)
}

// The strict array reader has already checked the complete value and Unicode.
// Plain quoted decimal text does not need another JSON syntax scan.
func parseDecimalArrayValue(raw []byte) (decimal.Decimal, error) {
	if len(raw) >= 2 && raw[0] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return decimal.NewFromString(string(raw[1 : len(raw)-1]))
	}
	return parseDecimalParam(raw)
}
