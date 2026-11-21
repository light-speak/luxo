package pg

import (
	"errors"
	"time"
)

// ErrDurationPrecision indicates a nanosecond value PostgreSQL cannot preserve.
var ErrDurationPrecision = errors.New("pg: duration must be an exact number of microseconds")

// ValidateDuration rejects lossy interval writes before reaching the driver.
func ValidateDuration(value time.Duration) error {
	if value%time.Microsecond != 0 {
		return ErrDurationPrecision
	}
	return nil
}

func validateDurationArgument(input any) error {
	switch value := input.(type) {
	case time.Duration:
		return ValidateDuration(value)
	case *time.Duration:
		if value != nil {
			return ValidateDuration(*value)
		}
	case []time.Duration:
		return validateDurationList(value)
	case []*time.Duration:
		for _, item := range value {
			if item != nil {
				if err := ValidateDuration(*item); err != nil {
					return err
				}
			}
		}
	case *[]time.Duration:
		if value != nil {
			return validateDurationArgument(*value)
		}
	case *[]*time.Duration:
		if value != nil {
			return validateDurationArgument(*value)
		}
	}
	return nil
}

// Check four independent remainders per branch; OR cannot cancel opposite signs.
func validateDurationList(values []time.Duration) error {
	for len(values) >= 4 {
		remainder := values[0]%time.Microsecond | values[1]%time.Microsecond | values[2]%time.Microsecond | values[3]%time.Microsecond
		if remainder != 0 {
			return ErrDurationPrecision
		}
		values = values[4:]
	}
	for _, value := range values {
		if err := ValidateDuration(value); err != nil {
			return err
		}
	}
	return nil
}
