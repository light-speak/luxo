package api

// Frozen arbitrary-target readers are retained only for historical benchmarks.
// Normal builds never compile this file. Generated benchmark fixtures may add
// it through a temporary overlay; correctness fixtures use only production API.
import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/shopspring/decimal"
)

// LegacyParamJSON extracts a required parameter into the target.
func (r *Request) LegacyParamJSON(name string, target any) error {
	return r.legacyParamJSON(name, target, false, false)
}

// LegacyParamJSONOptional extracts an optional parameter into the target.
func (r *Request) LegacyParamJSONOptional(name string, target any) error {
	return r.legacyParamJSON(name, target, true, false)
}

// LegacyParamJSONNullable extracts a required parameter that may explicitly be null.
func (r *Request) LegacyParamJSONNullable(name string, target any) error {
	return r.legacyParamJSON(name, target, false, true)
}

// LegacyParamJSONOptionalNullable extracts an optional parameter that may explicitly be null.
func (r *Request) LegacyParamJSONOptionalNullable(name string, target any) error {
	return r.legacyParamJSON(name, target, true, true)
}

func (r *Request) legacyParamJSON(name string, target any, optional, nullable bool) error {
	if r.Params == nil {
		v, ok := r.findParam(name)
		if !ok {
			if optional {
				return nil
			}
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
		}
		if v == nil {
			if nullable {
				return nil
			}
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must not be null"})
		}
		if ptr, ok := target.(*any); ok {
			*ptr = v
			return nil
		}
		if legacyAssignBinaryParamFast(v, target) {
			return nil
		}
		data, err := json.Marshal(v)
		if err != nil || json.Unmarshal(data, target) != nil {
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid format"})
		}
		return nil
	}
	raw, ok := r.Params[name]
	if !ok {
		if optional {
			return nil
		}
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !nullable {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must not be null"})
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid format"})
	}
	return nil
}

// legacyAssignBinaryParam assigns a binary-decoded value to a typed pointer target.
func legacyAssignBinaryParam(v any, target any) error {
	legacyAssignBinaryParamFast(v, target)
	return nil
}

func legacyAssignBinaryParamFast(v any, target any) bool {
	switch t := target.(type) {
	case **string:
		if sv, ok := v.(string); ok {
			*t = &sv
			return true
		}
	case **int64:
		if iv, ok := v.(int64); ok {
			*t = &iv
			return true
		}
	case **float64:
		if fv, ok := v.(float64); ok {
			*t = &fv
			return true
		}
	case **bool:
		if bv, ok := v.(bool); ok {
			*t = &bv
			return true
		}
	case *string:
		if sv, ok := v.(string); ok {
			*t = sv
			return true
		}
	case *int64:
		if iv, ok := v.(int64); ok {
			*t = iv
			return true
		}
	case *float64:
		if fv, ok := v.(float64); ok {
			*t = fv
			return true
		}
	case *bool:
		if bv, ok := v.(bool); ok {
			*t = bv
			return true
		}
	default:
		return legacyAssignBinaryNativeParam(v, target)
	}
	return false
}

func legacyAssignBinaryNativeParam(v any, target any) bool {
	switch t := target.(type) {
	case *time.Time:
		if tv, ok := v.(time.Time); ok {
			*t = tv
			return true
		}
	case **time.Time:
		if tv, ok := v.(time.Time); ok {
			*t = &tv
			return true
		}
	case *uuid.UUID:
		if uv, ok := v.(uuid.UUID); ok {
			*t = uv
			return true
		}
	case **uuid.UUID:
		if uv, ok := v.(uuid.UUID); ok {
			*t = &uv
			return true
		}
	case *json.RawMessage:
		if raw, ok := v.(json.RawMessage); ok {
			*t = append((*t)[:0], raw...)
			return true
		}
	case *time.Duration:
		if dv, ok := v.(time.Duration); ok {
			*t = dv
			return true
		}
	case **time.Duration:
		if dv, ok := v.(time.Duration); ok {
			*t = &dv
			return true
		}
	case *decimal.Decimal:
		if dv, ok := v.(decimal.Decimal); ok {
			*t = dv
			return true
		}
	case **decimal.Decimal:
		if dv, ok := v.(decimal.Decimal); ok {
			*t = &dv
			return true
		}
	default:
		return legacyAssignBinarySliceParam(v, target)
	}
	return false
}

func legacyAssignBinarySliceParam(v any, target any) bool {
	switch t := target.(type) {
	case *[]byte:
		if bv, ok := v.([]byte); ok {
			*t = bv
			return true
		}
	case *[]int64:
		if values, ok := v.([]int64); ok {
			*t = values
			return true
		}
	case *[]float64:
		if values, ok := v.([]float64); ok {
			*t = values
			return true
		}
	case *[]string:
		if values, ok := v.([]string); ok {
			*t = values
			return true
		}
	case *[]bool:
		if values, ok := v.([]bool); ok {
			*t = values
			return true
		}
	case *[]time.Time:
		if values, ok := v.([]time.Time); ok {
			*t = values
			return true
		}
	case *[]time.Duration:
		if values, ok := v.([]time.Duration); ok {
			*t = values
			return true
		}
	case *[]uuid.UUID:
		if values, ok := v.([]uuid.UUID); ok {
			*t = values
			return true
		}
	case *[]decimal.Decimal:
		if values, ok := v.([]decimal.Decimal); ok {
			*t = values
			return true
		}
	case *[][]byte:
		if values, ok := v.([][]byte); ok {
			*t = values
			return true
		}
	case *[]json.RawMessage:
		if values, ok := v.([]json.RawMessage); ok {
			*t = values
			return true
		}
	}
	return false
}
