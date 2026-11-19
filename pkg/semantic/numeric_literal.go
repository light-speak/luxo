package semantic

import (
	"math"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func signedIntegralLiteral(expression *ast.UnaryExpr) (*ast.Literal, bool) {
	if expression.Op != "-" {
		return nil, false
	}
	literal, ok := expression.Value.(*ast.Literal)
	return literal, ok && (literal.Kind == token.Int || literal.Kind == token.Duration)
}

func (a *Analyzer) integralLiteralType(literal *ast.Literal, negate bool) *ResolvedType {
	value := literal.Value
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		negate = negate != (value[0] == '-')
		value = value[1:]
	}
	limit := uint64(math.MaxInt64)
	if negate {
		limit++
	}
	kind := "Int"
	if literal.Kind == token.Duration {
		kind = "Duration"
		var unit uint64
		value, unit = durationLiteralMagnitude(value)
		if unit == 0 {
			value = ""
		} else {
			limit /= unit
		}
	}
	magnitude, err := strconv.ParseUint(value, 10, 64)
	if err != nil || magnitude > limit {
		a.addError(literal.Pos, "%s literal must fit its Int64 range / %s 字面量必须在对应的 Int64 范围内", kind, kind)
	}
	return a.types[kind]
}

func durationLiteralMagnitude(value string) (string, uint64) {
	if strings.HasSuffix(value, "ms") {
		return value[:len(value)-2], 1_000_000
	}
	if len(value) < 2 {
		return "", 0
	}
	var unit uint64
	switch value[len(value)-1] {
	case 's':
		unit = 1_000_000_000
	case 'm':
		unit = 60_000_000_000
	case 'h':
		unit = 3_600_000_000_000
	case 'd':
		unit = 86_400_000_000_000
	}
	return value[:len(value)-1], unit
}

// Defaults and directive arguments do not use ordinary expression checking.
// A literal directly beneath '-' may use the extra magnitude of Int64.min.
func (a *Analyzer) checkNumericLiteralTree(expression ast.Expr) {
	var signed *ast.Literal
	ast.WalkExpr(expression, func(value ast.Expr) {
		if unary, ok := value.(*ast.UnaryExpr); ok {
			if literal, ok := signedIntegralLiteral(unary); ok {
				a.integralLiteralType(literal, true)
				signed = literal
			}
		}
		if literal, ok := value.(*ast.Literal); ok {
			if literal == signed {
				signed = nil
				return
			}
			switch literal.Kind {
			case token.Int, token.Duration:
				a.integralLiteralType(literal, false)
			case token.Float:
				a.validateFloatLiteral(literal)
			}
		}
	})
	a.checkIntegerConstantTree(expression)
}
