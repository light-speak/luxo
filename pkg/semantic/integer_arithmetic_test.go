package semantic

import (
	"math"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestIntegerConstantArithmeticRejectsInvalidResults(t *testing.T) {
	for _, source := range []string{
		`fn value(): Int { return 9223372036854775807 + 1 }`,
		`fn value(): Int { return -9223372036854775808 - 1 }`,
		`fn value(): Int { return 9223372036854775807 * 2 }`,
		`fn value(): Int { return -(-9223372036854775808) }`,
		`fn value(): Int { return -9223372036854775808 / -1 }`,
		`fn value(): Int { return 1 / 0 }`,
		`fn value(): Int { return 1 % 0 }`,
		`fn value(input: Int): Int { return input / 0 }`,
		`fn value(): Int { return (9223372036854775806 + 1) + 1 }`,
		`fn value(): Int { return 1 / (2 - 2) }`,
		`fn value(): Duration { return 9223372036854ms + 1ms }`,
		`fn value(): Duration { return (9223372036854775807).days }`,
		`type Value { count: Int = 9223372036854775807 + 1 }`,
	} {
		t.Run(source, func(t *testing.T) {
			expectError(t, analyze(t, source), "arithmetic fails")
		})
	}
	// Parameter defaults already reject expressions outside the constant grammar.
	expectError(t, analyze(t, `fn value(input: Int = 9223372036854775807 + 1): Int { return input }`), "compile-time constant")
}

func TestIntegerConstantArithmeticKeepsValidSemantics(t *testing.T) {
	for _, source := range []string{
		`fn value(): Int { return -9223372036854775808 }`,
		`fn value(): Int { return -9223372036854775808 % -1 }`,
		`fn value(): Int { return -5 / 2 }`,
		`fn value(): Int { return -5 % 2 }`,
		`fn value(): Int { return (40 + 2) * 1 }`,
		`fn value(): Duration { return 1ms + 2ms }`,
		`fn value(): Duration { return (2).days + 1h }`,
		`fn value(input: Int): Int { return input + 1 }`,
		`fn value(input: Float): Float { return input / 0 }`,
		`fn value(input: Float): Float { return input + 1 }`,
		`fn value(): String { return "a" + "b" }`,
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, source)) })
	}
}

func TestIntegerConstantHelpers(t *testing.T) {
	for _, sample := range []struct {
		literal  *ast.Literal
		negative bool
		value    int64
		kind     string
	}{
		{nil, false, 0, ""}, {&ast.Literal{Kind: token.Float, Value: "1.0"}, false, 0, ""},
		{&ast.Literal{Kind: token.Int, Value: "+42"}, false, 42, "Int"},
		{&ast.Literal{Kind: token.Int, Value: "-42"}, true, 42, "Int"},
		{&ast.Literal{Kind: token.Int, Value: "9223372036854775808"}, true, math.MinInt64, "Int"},
		{&ast.Literal{Kind: token.Int, Value: "invalid"}, false, 0, ""},
		{&ast.Literal{Kind: token.Duration, Value: "1unknown"}, false, 0, ""},
		{&ast.Literal{Kind: token.Duration, Value: "-1ms"}, false, -1000000, "Duration"},
	} {
		value, known := integralConstantLiteral(sample.literal, sample.negative)
		if known != (sample.kind != "") || value.value != sample.value || value.kind != sample.kind {
			t.Fatal(sample, value, known)
		}
	}
	for _, field := range []string{"days", "hours", "minutes", "seconds", "milliseconds"} {
		expectNoErrors(t, analyze(t, "fn value(): Duration { return (2)."+field+" }"))
	}
	a := New()
	literal := &ast.Literal{Kind: token.Int, Value: "2"}
	duration := &ast.Literal{Kind: token.Duration, Value: "1ms"}
	for _, expression := range []ast.Expr{
		nil, literal, &ast.Ident{Name: "value"},
		&ast.UnaryExpr{Op: "!", Value: literal}, &ast.UnaryExpr{Op: "-", Value: literal},
		&ast.UnaryExpr{Op: "-", Value: &ast.Ident{Name: "value"}},
		&ast.MemberExpr{Object: literal, Field: "unknown"},
		&ast.BinaryExpr{Op: "==", Left: literal, Right: literal},
		&ast.BinaryExpr{Op: "+", Left: literal, Right: duration},
		&ast.BinaryExpr{Op: "*", Left: duration, Right: duration},
	} {
		if _, err, known := a.evaluateIntegerConstant(expression); err != nil || known {
			t.Fatal(expression, err, known)
		}
		a.checkIntegerConstant(expression)
	}
	constant := &ast.BinaryExpr{Op: "+", Left: literal, Right: literal}
	a.checkIntegerConstant(constant)
	a.checkIntegerConstant(constant)
	if got, known := a.integerConstantValue(constant); !known || got.value != 4 {
		t.Fatal(got, known)
	}
	for _, source := range []string{
		`fn value(): Int { return -(-(1 + 1)) }`,
		`fn value(): Duration { return 2ms - 1ms }`,
		`fn value(): Duration { return -(1ms + 1ms) }`,
	} {
		expectNoErrors(t, analyze(t, source))
	}
}
