package codegen

import (
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func TestAsyncCapturesFollowLexicalScopes(t *testing.T) {
	outer := map[string]valType{"value": {}, "values": {}, "it": {}, "gate": {}, "output": {}}
	for _, test := range []struct {
		name, body string
		want       []string
	}{
		{"empty", "", []string{}},
		{"local shadow", "val value = 1 return value", []string{}},
		{"initializer", "val value = value return value", []string{"value"}},
		{"block scope", "if true { val value = 1 } return value", []string{"value"}},
		{"loop binding", "for value in values { output <- value }", []string{"output", "values"}},
		{"implicit lambda", "values.map { it + value }", []string{"value", "values"}},
		{"explicit lambda", "values.map { value -> value }", []string{"values"}},
		{"when block preserves it", "when { true -> { it } else -> value }", []string{"it", "value"}},
		{"nested async", "async { val _ = <-gate output <- value }", []string{"gate", "output", "value"}},
		{"assignment", "value += 1", []string{"value"}},
		{"nested await", "await { val local = value }", []string{"value"}},
		{"for expression", "val local = for value in values { value }", []string{"values"}},
		{"destructure", "val (value, it) = pair() return value + it", []string{}},
		{"member", "return value.name", []string{"value"}},
		{"object", "return Box { item: value }", []string{"value"}},
		{"template", "return \"value: ${value}\"", []string{"value"}},
		{"throw", "throw value", []string{"value"}},
		{"emit", "emit Notice(item: value)", []string{"value"}},
		{"lists", "return [value, it]", []string{"it", "value"}},
		{"range", "return value..it", []string{"it", "value"}},
		{"elvis", "return value ?: it", []string{"it", "value"}},
		{"bang elvis", "return value !: it", []string{"it", "value"}},
		{"transaction", "transaction { return value }", []string{"value"}},
		{"yield", "yield value", []string{"value"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens, errors := lexer.New("fn probe() {"+test.body+"}", "capture.luxo").Tokenize()
			if len(errors) != 0 {
				t.Fatal(errors)
			}
			file, failures := parser.New(tokens).Parse("capture.luxo")
			if len(failures) != 0 {
				t.Fatal(failures)
			}
			got := capturedAsyncNames(file.Functions[0].Body, outer)
			if !slices.Equal(got, test.want) {
				t.Fatalf("captures %v, want %v", got, test.want)
			}
		})
	}
	if got := capturedAsyncNames(nil, outer); len(got) != 0 {
		t.Fatal(got)
	}
	walker := asyncCaptures{outer: outer, shadow: make(map[string]int), found: make(map[string]bool)}
	walker.block(nil, "value")
	walker.statement(&ast.BreakStmt{})
	walker.scoped(&ast.AwaitExpr{Body: &ast.Block{}})
	walker.scoped(&ast.TransactionExpr{Body: &ast.Block{}})
}
