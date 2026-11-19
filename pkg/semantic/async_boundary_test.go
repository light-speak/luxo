package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestAsyncReturnHasIndependentVoidBoundary(t *testing.T) {
	for _, source := range []string{
		`fn good(done: Channel<Int>): Int { async { return done.close() } return 42 }`,
		`api good(): Int { val done: Channel<Int> = Channel(1) async { return done.close() } return 42 }`,
		`fn good(first: Channel<Int>, second: Channel<Int>): Int { async { async { return first.close() } return second.close() } return 42 }`,
		`fn good(stop: Boolean, done: Channel<Int>): Int { async { if stop { return done.close() } } return 42 }`,
		`fn good(): Int { async { val values = [1].map { return it + 1 } } return 42 }`,
		`fn good(): Int { async { val value = when { true -> { return 1 } else -> 2 } } return 42 }`,
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, source)) })
	}
}

func TestAsyncRejectsReturnedValues(t *testing.T) {
	for _, source := range []string{
		`fn bad(): Int { async { return 42 } return 42 }`,
		`api bad(): Int { async { return 42 } return 42 }`,
		`fn bad(): Float { async { return 0.1 } return 0.1 }`,
		`fn bad(): Int? { async { return null } return null }`,
		`fn bad(): Int { async { if true { return 42 } } return 42 }`,
		`fn bad(): Int { async { async { return 42 } } return 42 }`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, source), "return expects 'Void'") })
	}
}

func TestAsyncRestoresOuterReturnBoundary(t *testing.T) {
	for _, source := range []string{
		`fn bad(): Int { async {} return "bad" }`,
		`api bad(): Int { async {} return "bad" }`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, source), "return expects 'Int'") })
	}
}

func TestAsyncDoesNotProduceAValue(t *testing.T) {
	for _, binding := range []string{"val work", "var work", "val work: Int"} {
		expectError(t, analyze(t, `fn bad(): Int { `+binding+` = async {} return 42 }`), "void operation")
	}
	expectError(t, analyze(t, `fn bad(): Int { return async {} }`), "return expects 'Int'")
}

func TestVoidOperationsCannotBecomeListElements(t *testing.T) {
	for _, body := range []string{
		`for item in [async {}] {}`,
		`[async {}].forEach { item -> }`,
		`for item in [done.close()] {}`,
		`[done.close()].forEach { item -> }`,
	} {
		expectError(t, analyze(t, `fn bad(done: Channel<Int>): Int { `+body+` return 42 }`), "void operation cannot be a list element")
	}
}

func TestAsyncVoidMetadataIsPrivateToAnalysis(t *testing.T) {
	analyzer := newAnalyzer()
	expression := func() *ast.AsyncExpr { return &ast.AsyncExpr{Body: &ast.Block{}} }
	first := analyzer.checkExpr(expression(), analyzer.scope)
	second := analyzer.checkExpr(expression(), analyzer.scope)
	if first == nil || first != second || first.Kind != TypeVoid {
		t.Fatal("async result metadata should be reused within one analysis")
	}
	copy := cloneResolvedType(first)
	copy.Name, copy.IsList, copy.Nullable = "changed", true, true
	other := newAnalyzer()
	independent := other.checkExpr(expression(), other.scope)
	if independent == first || first.Name != "Void" || first.IsList || first.Nullable {
		t.Fatal("async metadata was shared across analyses or mutated by a consumer")
	}
}
