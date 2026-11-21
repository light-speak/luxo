package lux

import (
	"cmp"
	"errors"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// MaxDecimalAlignment bounds additional decimal places materialized by arithmetic.
const MaxDecimalAlignment = 4096

// ErrDecimalAlignment rejects excessive alignment without truncation or rounding.
var ErrDecimalAlignment = errors.New("decimal exponent alignment exceeds 4096 digits")

// AddDecimal adds exact values within the exponent-alignment resource budget.
func AddDecimal(left, right decimal.Decimal) (decimal.Decimal, error) {
	if right.IsZero() {
		return left, nil
	}
	if left.IsZero() {
		return right, nil
	}
	if !decimalAlignmentAllowed(left, right) {
		return decimal.Decimal{}, ErrDecimalAlignment
	}
	return left.Add(right), nil
}

// SubDecimal subtracts exact values without expanding a zero operand.
func SubDecimal(left, right decimal.Decimal) (decimal.Decimal, error) {
	if right.IsZero() {
		return left, nil
	}
	if left.IsZero() {
		return right.Neg(), nil
	}
	if !decimalAlignmentAllowed(left, right) {
		return decimal.Decimal{}, ErrDecimalAlignment
	}
	return left.Sub(right), nil
}

func decimalAlignmentAllowed(left, right decimal.Decimal) bool {
	gap := int64(left.Exponent()) - int64(right.Exponent())
	return gap >= -MaxDecimalAlignment && gap <= MaxDecimalAlignment
}

// FormatDecimal preserves precision without expanding unbounded exponent padding.
// Ordinary values retain Decimal.String formatting; large exponents stay explicit.
func FormatDecimal(value decimal.Decimal) string {
	exponent := value.Exponent()
	if exponent >= -1024 && exponent <= 1024 {
		return value.String()
	}
	if value.IsZero() {
		return "0"
	}
	return value.Coefficient().String() + "e" + strconv.FormatInt(int64(exponent), 10)
}

// CompareDecimal compares exact values without expanding large exponent gaps.
func CompareDecimal(left, right decimal.Decimal) int {
	gap := int64(left.Exponent()) - int64(right.Exponent())
	// At most 64 decimal places of rescaling cannot amplify a short exponent.
	if gap >= -64 && gap <= 64 {
		return left.Cmp(right)
	}
	leftSign, rightSign := left.Sign(), right.Sign()
	if leftSign != rightSign {
		return cmp.Compare(leftSign, rightSign)
	}
	if leftSign == 0 {
		return 0
	}
	leftDigits := strings.TrimPrefix(left.Coefficient().String(), "-")
	rightDigits := strings.TrimPrefix(right.Coefficient().String(), "-")
	leftMagnitude := int64(len(leftDigits)) + int64(left.Exponent())
	rightMagnitude := int64(len(rightDigits)) + int64(right.Exponent())
	if order := cmp.Compare(leftMagnitude, rightMagnitude); order != 0 {
		return leftSign * order
	}
	return leftSign * compareDecimalDigits(leftDigits, rightDigits)
}

// Missing trailing coefficient digits are virtual zeroes, never allocated padding.
func compareDecimalDigits(left, right string) int {
	for index := range max(len(left), len(right)) {
		leftDigit, rightDigit := byte('0'), byte('0')
		if index < len(left) {
			leftDigit = left[index]
		}
		if index < len(right) {
			rightDigit = right[index]
		}
		if order := cmp.Compare(leftDigit, rightDigit); order != 0 {
			return order
		}
	}
	return 0
}
