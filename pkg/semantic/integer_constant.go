package semantic

import (
	"math"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/arith"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type integerConstant struct {
	value int64
	kind  string
}

func integralConstantLiteral(literal *ast.Literal, negate bool) (integerConstant, bool) {
	if literal == nil || literal.Kind != token.Int && literal.Kind != token.Duration {
		return integerConstant{}, false
	}
	raw := literal.Value
	if strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+") {
		negate = negate != (raw[0] == '-')
		raw = raw[1:]
	}
	kind, unit := "Int", uint64(1)
	if literal.Kind == token.Duration {
		kind = "Duration"
		raw, unit = durationLiteralMagnitude(raw)
	}
	magnitude, err := strconv.ParseUint(raw, 10, 64)
	limit := uint64(math.MaxInt64)
	if negate {
		limit++
	}
	if err != nil || unit == 0 || magnitude > limit/unit {
		return integerConstant{}, false
	}
	value := magnitude * unit
	if negate {
		value = 0 - value
	}
	return integerConstant{int64(value), kind}, true
}

func (a *Analyzer) integerConstantValue(expression ast.Expr) (integerConstant, bool) {
	if cached, exists := a.integerConstants[expression]; exists {
		return cached, cached.kind != ""
	}
	if literal, ok := expression.(*ast.Literal); ok {
		return integralConstantLiteral(literal, false)
	}
	if unary, ok := expression.(*ast.UnaryExpr); ok {
		if literal, ok := signedIntegralLiteral(unary); ok {
			return integralConstantLiteral(literal, true)
		}
	}
	return integerConstant{}, false
}

func (a *Analyzer) checkIntegerConstant(expression ast.Expr) {
	switch value := expression.(type) {
	case *ast.BinaryExpr:
	case *ast.UnaryExpr:
		if _, leaf := signedIntegralLiteral(value); leaf || value.Op != "-" {
			return
		}
	case *ast.MemberExpr:
		if durationPropertyUnit(value.Field) == 0 {
			return
		}
	default:
		return
	}
	if _, visited := a.integerConstants[expression]; visited {
		return
	}
	value, err, known := a.evaluateIntegerConstant(expression)
	if !known {
		return
	}
	if a.integerConstants == nil {
		a.integerConstants = make(map[ast.Expr]integerConstant)
	}
	if err != nil {
		a.integerConstants[expression] = integerConstant{}
		a.addError(expression.GetPos(), "constant %s arithmetic fails: %s / 常量 %s 运算失败：%s", value.kind, err.Error(), value.kind, err.Error())
		return
	}
	a.integerConstants[expression] = value
}

func (a *Analyzer) evaluateIntegerConstant(expression ast.Expr) (integerConstant, error, bool) {
	switch value := expression.(type) {
	case *ast.BinaryExpr:
		return a.integerBinaryConstant(value)
	case *ast.UnaryExpr:
		// Leaf signed literals are validated separately and need no cache entry.
		if _, leaf := signedIntegralLiteral(value); leaf || value.Op != "-" {
			return integerConstant{}, nil, false
		}
		if constant, known := a.integerConstantValue(value.Value); known {
			result, err := arith.Neg(constant.value)
			return integerConstant{result, constant.kind}, err, true
		}
	case *ast.MemberExpr:
		unit := durationPropertyUnit(value.Field)
		if constant, known := a.integerConstantValue(value.Object); unit != 0 && known && constant.kind == "Int" {
			result, err := arith.Mul(constant.value, unit)
			return integerConstant{result, "Duration"}, err, true
		}
	}
	return integerConstant{}, nil, false
}

func (a *Analyzer) integerBinaryConstant(expression *ast.BinaryExpr) (integerConstant, error, bool) {
	left, leftKnown := a.integerConstantValue(expression.Left)
	right, rightKnown := a.integerConstantValue(expression.Right)
	if rightKnown && right.kind == "Int" && right.value == 0 && (expression.Op == "/" || expression.Op == "%") && (expression.GetTypeTag() == "Int" || leftKnown && left.kind == "Int") {
		return integerConstant{kind: "Int"}, arith.ErrDivisionByZero, true
	}
	if !leftKnown || !rightKnown || left.kind != right.kind {
		return integerConstant{}, nil, false
	}
	if left.kind == "Duration" && expression.Op != "+" && expression.Op != "-" {
		return integerConstant{}, nil, false
	}
	var result int64
	var err error
	switch expression.Op {
	case "+":
		result, err = arith.Add(left.value, right.value)
	case "-":
		result, err = arith.Sub(left.value, right.value)
	case "*":
		result, err = arith.Mul(left.value, right.value)
	case "/":
		result, err = arith.Quo(left.value, right.value)
	case "%":
		result, err = arith.Rem(left.value, right.value)
	default:
		return integerConstant{}, nil, false
	}
	return integerConstant{result, left.kind}, err, true
}

func durationPropertyUnit(field string) int64 {
	switch field {
	case "days":
		return 86_400_000_000_000
	case "hours":
		return 3_600_000_000_000
	case "minutes":
		return 60_000_000_000
	case "seconds":
		return 1_000_000_000
	case "milliseconds":
		return 1_000_000
	}
	return 0
}

// Defaults skip ordinary expression checking; process operator children first.
func (a *Analyzer) checkIntegerConstantTree(expression ast.Expr) {
	var operators []ast.Expr
	ast.WalkExpr(expression, func(value ast.Expr) {
		switch value.(type) {
		case *ast.BinaryExpr, *ast.UnaryExpr, *ast.MemberExpr:
			operators = append(operators, value)
		}
	})
	for index := len(operators) - 1; index >= 0; index-- {
		a.checkIntegerConstant(operators[index])
	}
}
