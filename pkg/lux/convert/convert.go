// Package convert provides type conversion utility functions for the Luxo runtime.
package convert

import (
	"fmt"
	gomath "math"
	"strconv"
)

// ToString converts any value to its string representation.
func ToString(v any) string {
	return fmt.Sprint(v)
}

// ToInt converts a value to int64.
func ToInt(v any) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case float64:
		return floatToInt(val)
	case float32:
		return floatToInt(float64(val))
	case string:
		return strconv.ParseInt(val, 10, 64)
	case bool:
		if val {
			return 1, nil
		}
		return 0, nil
	case uint64:
		if val > gomath.MaxInt64 {
			return 0, fmt.Errorf("convert: uint64 %d overflows int64", val)
		}
		return int64(val), nil
	default:
		return toIntReflect(v)
	}
}

// toIntReflect handles less common integer types.
// floatToInt converts a float64 to int64 with NaN/Inf/overflow guards.
func floatToInt(f float64) (int64, error) {
	if gomath.IsNaN(f) || gomath.IsInf(f, 0) {
		return 0, fmt.Errorf("convert: float %v cannot be converted to int64", f)
	}
	r := gomath.Round(f)
	if r > float64(gomath.MaxInt64) || r < float64(gomath.MinInt64) {
		return 0, fmt.Errorf("convert: float %v overflows int64", f)
	}
	return int64(r), nil
}

func toIntReflect(v any) (int64, error) {
	switch val := v.(type) {
	case int8:
		return int64(val), nil
	case int16:
		return int64(val), nil
	case int32:
		return int64(val), nil
	case uint:
		if uint64(val) > gomath.MaxInt64 {
			return 0, fmt.Errorf("convert: uint %d overflows int64", val)
		}
		return int64(val), nil
	case uint8:
		return int64(val), nil
	case uint16:
		return int64(val), nil
	case uint32:
		return int64(val), nil
	default:
		return 0, fmt.Errorf("convert: cannot convert %T to int64", v)
	}
}

// ToFloat converts a value to float64.
func ToFloat(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	case bool:
		if val {
			return 1, nil
		}
		return 0, nil
	default:
		return toFloatReflect(v)
	}
}

// toFloatReflect handles less common numeric types.
func toFloatReflect(v any) (float64, error) {
	switch val := v.(type) {
	case int8:
		return float64(val), nil
	case int16:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case uint:
		return float64(val), nil
	case uint8:
		return float64(val), nil
	case uint16:
		return float64(val), nil
	case uint32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("convert: cannot convert %T to float64", v)
	}
}

// ToBool converts a value to bool.
func ToBool(v any) (bool, error) {
	switch val := v.(type) {
	case bool:
		return val, nil
	case string:
		return strconv.ParseBool(val)
	case int:
		return val != 0, nil
	case int64:
		return val != 0, nil
	case float64:
		return val != 0, nil
	default:
		return false, fmt.Errorf("convert: cannot convert %T to bool", v)
	}
}

// IntToString converts an int64 to decimal string.
func IntToString(v int64) string { return strconv.FormatInt(v, 10) }

// FloatToString converts a float64 to string (no trailing zeros).
func FloatToString(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// BoolToString converts a bool to "true" or "false".
func BoolToString(v bool) string { return strconv.FormatBool(v) }
