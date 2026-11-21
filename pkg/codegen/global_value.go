package codegen

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/light-speak/luxo/internal/arith"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// Scalar values can be emitted without a shared mutable runtime initializer.
type globalScalar struct {
	kind     string
	code     string
	integer  int64
	floating float64
}

type generatedGlobalValue struct {
	value     globalScalar
	reference string
}

func (g *GeneratorContext) generateGlobalValues(result *semantic.Result, packageName string, enums map[string]bool) ([]byte, error) {
	g.globalValues = nil
	hasGlobals := false
	for _, file := range result.Files {
		if len(file.Globals) != 0 {
			hasGlobals = true
			break
		}
	}
	if !hasGlobals {
		return nil, nil
	}
	var body strings.Builder
	index, packages := 0, generatedImports(0)
	for _, file := range result.Files {
		for _, declaration := range file.Globals {
			value, ok := g.evaluateGlobalValue(declaration.Value, enums)
			if !ok || declaration.Mutable || len(declaration.Names) != 0 || !coerceGlobalValue(&value, declaration.Type) {
				return nil, fmt.Errorf("%s: cannot generate initializer for top-level val %q: unsupported initialization or invalid constant arithmetic", declaration.Pos, declaration.Name)
			}
			for result.Types["_luxoGlobal"+strconv.Itoa(index)] != nil {
				index++
			}
			name := "_luxoGlobal" + strconv.Itoa(index)
			index++
			reference := writeGlobalValue(&body, name, value, &packages)
			if g.globalValues == nil {
				g.globalValues = make(map[string]generatedGlobalValue)
			}
			g.globalValues[declaration.Name] = generatedGlobalValue{value, reference}
		}
	}
	var source strings.Builder
	writeHeader(&source, packageName, "global.gen.go")
	if packages != 0 {
		source.WriteString("import (\n")
		writeOptionalStandardImports(&source, packages, "math", "time")
		source.WriteString(")\n\n")
	}
	source.WriteString(body.String())
	return []byte(source.String()), nil
}

func coerceGlobalValue(value *globalScalar, target *ast.TypeRef) bool {
	if target == nil {
		return true
	}
	if target.Nullable || target.IsList {
		return false
	}
	return target.Name == value.kind
}

func writeGlobalValue(body *strings.Builder, name string, value globalScalar, packages *generatedImports) string {
	if value.kind == "Float" {
		*packages |= generatedPackageBit("math")
		fmt.Fprintf(body, "func %s() float64 { return math.Float64frombits(%d) }\n\n", name, math.Float64bits(value.floating))
		return name + "()"
	}
	typ, code := value.kind, value.code
	switch value.kind {
	case "Int":
		typ, code = "int64", strconv.FormatInt(value.integer, 10)
	case "Duration":
		*packages |= generatedPackageBit("time")
		typ, code = "time.Duration", strconv.FormatInt(value.integer, 10)
	case "String":
		typ = "string"
	case "Boolean":
		typ = "bool"
	}
	fmt.Fprintf(body, "const %s %s = %s\n\n", name, typ, code)
	return name
}

func (g *GeneratorContext) evaluateGlobalValue(expression ast.Expr, enums map[string]bool) (globalScalar, bool) {
	switch value := expression.(type) {
	case *ast.Literal:
		return globalLiteral(value, "")
	case *ast.Ident:
		global, ok := g.globalValues[value.Name]
		if !ok {
			return globalScalar{}, false
		}
		constant := global.value
		if constant.kind != "Int" && constant.kind != "Duration" && constant.kind != "Float" {
			constant.code = global.reference
		}
		return constant, true
	case *ast.MemberExpr:
		if owner, ok := value.Object.(*ast.Ident); ok && value.EnumConstant && enums[owner.Name] {
			return globalScalar{kind: owner.Name, code: owner.Name + value.Field}, true
		}
	case *ast.UnaryExpr:
		return g.evaluateGlobalUnary(value, enums)
	case *ast.BinaryExpr:
		left, leftOK := g.evaluateGlobalValue(value.Left, enums)
		right, rightOK := g.evaluateGlobalValue(value.Right, enums)
		if leftOK && rightOK {
			return globalBinary(value.Op, value.GetTypeTag(), left, right)
		}
	}
	return globalScalar{}, false
}

func globalLiteral(literal *ast.Literal, sign string) (globalScalar, bool) {
	switch literal.Kind {
	case token.Int:
		value, err := strconv.ParseInt(sign+literal.Value, 10, 64)
		return globalScalar{kind: "Int", integer: value}, err == nil
	case token.Duration:
		value, err := globalDuration(sign + literal.Value)
		return globalScalar{kind: "Duration", integer: int64(value)}, err == nil
	case token.Float:
		value, err := strconv.ParseFloat(sign+literal.Value, 64)
		return globalScalar{kind: "Float", floating: value}, err == nil && !math.IsInf(value, 0) && !math.IsNaN(value)
	case token.String:
		return globalScalar{kind: "String", code: strconv.Quote(literal.Value)}, sign == ""
	case token.True, token.False:
		return globalScalar{kind: "Boolean", code: literal.Kind.String()}, sign == ""
	}
	return globalScalar{}, false
}

func globalDuration(raw string) (time.Duration, error) {
	if !strings.HasSuffix(raw, "d") {
		return time.ParseDuration(raw)
	}
	amount, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64)
	if err != nil {
		return 0, err
	}
	value, err := arith.Mul(amount, int64(24*time.Hour))
	return time.Duration(value), err
}

func (g *GeneratorContext) evaluateGlobalUnary(expression *ast.UnaryExpr, enums map[string]bool) (globalScalar, bool) {
	if literal, ok := expression.Value.(*ast.Literal); ok && (expression.Op == "+" || expression.Op == "-") {
		return globalLiteral(literal, expression.Op)
	}
	value, ok := g.evaluateGlobalValue(expression.Value, enums)
	if !ok {
		return globalScalar{}, false
	}
	if expression.Op == "!" && value.kind == "Boolean" {
		value.code = "!(" + value.code + ")"
		return value, true
	}
	if expression.Op != "+" && expression.Op != "-" {
		return globalScalar{}, false
	}
	if value.kind == "Float" {
		if expression.Op == "-" {
			value.floating = -value.floating
		}
		return value, true
	}
	if value.kind != "Int" && value.kind != "Duration" {
		return globalScalar{}, false
	}
	if expression.Op == "-" {
		var err error
		value.integer, err = arith.Neg(value.integer)
		return value, err == nil
	}
	return value, true
}

func globalBinary(operator, kind string, left, right globalScalar) (globalScalar, bool) {
	if globalNumeric(left) && globalNumeric(right) {
		return globalNumericBinary(operator, kind, left, right)
	}
	if left.kind != right.kind {
		return globalScalar{}, false
	}
	if globalConstantOperator(operator, kind, left.kind) {
		return globalScalar{kind: kind, code: "(" + left.code + " " + operator + " " + right.code + ")"}, true
	}
	return globalScalar{}, false
}

func globalConstantOperator(operator, result, operand string) bool {
	switch operator {
	case "+":
		return result == "String" && operand == "String"
	case "==", "!=":
		return result == "Boolean"
	case "<", "<=", ">", ">=":
		return result == "Boolean" && operand == "String"
	case "&&", "||":
		return result == "Boolean" && operand == "Boolean"
	}
	return false
}

func globalNumeric(value globalScalar) bool {
	return value.kind == "Int" || value.kind == "Duration" || value.kind == "Float"
}

func globalNumericBinary(operator, kind string, left, right globalScalar) (globalScalar, bool) {
	if kind == "Float" || left.kind == "Float" || right.kind == "Float" {
		return globalFloatBinary(operator, kind, left, right)
	}
	if kind == "Boolean" {
		return globalComparison(operator, cmp.Compare(left.integer, right.integer))
	}
	var value int64
	var err error
	switch operator {
	case "+":
		value, err = arith.Add(left.integer, right.integer)
	case "-":
		value, err = arith.Sub(left.integer, right.integer)
	case "*":
		value, err = arith.Mul(left.integer, right.integer)
	case "/":
		value, err = arith.Quo(left.integer, right.integer)
	case "%":
		value, err = arith.Rem(left.integer, right.integer)
	default:
		return globalScalar{}, false
	}
	return globalScalar{kind: kind, integer: value}, err == nil
}

func globalFloatBinary(operator, kind string, left, right globalScalar) (globalScalar, bool) {
	l, r := left.floating, right.floating
	if left.kind != "Float" {
		l = float64(left.integer)
	}
	if right.kind != "Float" {
		r = float64(right.integer)
	}
	if kind == "Boolean" {
		return globalComparison(operator, cmp.Compare(l, r))
	}
	var value float64
	switch operator {
	case "+":
		value = l + r
	case "-":
		value = l - r
	case "*":
		value = l * r
	case "/":
		value = l / r
	case "%":
		value = math.Mod(l, r)
	default:
		return globalScalar{}, false
	}
	return globalScalar{kind: "Float", floating: value}, !math.IsNaN(value) && !math.IsInf(value, 0)
}

func globalComparison(operator string, comparison int) (globalScalar, bool) {
	var value bool
	switch operator {
	case "==":
		value = comparison == 0
	case "!=":
		value = comparison != 0
	case "<":
		value = comparison < 0
	case "<=":
		value = comparison <= 0
	case ">":
		value = comparison > 0
	case ">=":
		value = comparison >= 0
	default:
		return globalScalar{}, false
	}
	return globalScalar{kind: "Boolean", code: strconv.FormatBool(value)}, true
}
