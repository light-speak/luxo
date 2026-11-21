package pg

import (
	"errors"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// ErrDecimalRange indicates a value outside PostgreSQL's numeric storage range.
var ErrDecimalRange = errors.New("pg: decimal exceeds numeric storage range")

func bindDecimal(value decimal.Decimal) (pgtype.Numeric, error) {
	if value.IsZero() {
		return pgtype.Numeric{Int: new(big.Int), Valid: true}, nil
	}
	exponent := value.Exponent()
	if exponent > 131071 {
		return pgtype.Numeric{}, ErrDecimalRange
	}
	coefficient := value.Coefficient()
	digits := strings.TrimPrefix(coefficient.String(), "-")
	if int64(len(digits))+int64(exponent) > 131072 {
		return pgtype.Numeric{}, ErrDecimalRange
	}
	if exponent < -16383 {
		extra := -16383 - int64(exponent)
		if extra > int64(len(digits)) {
			return pgtype.Numeric{}, ErrDecimalRange
		}
		for _, digit := range digits[len(digits)-int(extra):] {
			if digit != '0' {
				return pgtype.Numeric{}, ErrDecimalRange
			}
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(extra), nil)
		coefficient.Quo(coefficient, divisor)
		exponent = -16383
	}
	return pgtype.Numeric{Int: coefficient, Exp: exponent, Valid: true}, nil
}

func bindDecimalPointer(value *decimal.Decimal) (pgtype.Numeric, error) {
	if value == nil {
		return pgtype.Numeric{}, nil
	}
	return bindDecimal(*value)
}

func bindDecimalList[T any](values []T, bind func(T) (pgtype.Numeric, error)) ([]pgtype.Numeric, error) {
	if values == nil {
		return nil, nil
	}
	result := make([]pgtype.Numeric, len(values))
	for i, value := range values {
		converted, err := bind(value)
		if err != nil {
			return nil, err
		}
		result[i] = converted
	}
	return result, nil
}
