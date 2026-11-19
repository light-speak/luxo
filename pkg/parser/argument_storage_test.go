package parser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/token"
)

func TestParseManyArgumentsOwnTheirStorage(t *testing.T) {
	const count = 24
	arguments := make([]string, count)
	for index := range arguments {
		number := strconv.Itoa(index)
		arguments[index] = "field" + number + ": " + number
	}
	tokens, lexErrors := lexer.New("("+strings.Join(arguments, ", ")+")", "arguments.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	callee := &ast.Ident{Name: "receive"}
	var call *ast.CallExpr
	allocations := testing.AllocsPerRun(20, func() {
		parser := New(tokens)
		call = parser.parseCallArgs(token.Position{}, callee)
		if len(parser.errors) != 0 {
			t.Fatal(parser.errors)
		}
	})
	if len(call.Args) != count {
		t.Fatalf("parsed %d arguments, want %d", len(call.Args), count)
	}
	// Coverage instrumentation changes escape decisions for this parser.
	if testing.CoverMode() == "" && allocations > count+12 {
		t.Fatalf("parsed %d arguments with %.0f allocations", len(call.Args), allocations)
	}
	for index, argument := range call.Args {
		expected := strconv.Itoa(index)
		if argument.Name != "field"+expected || argument.Value.(*ast.Literal).Value != expected {
			t.Fatalf("argument %d changed: %+v", index, argument)
		}
	}
	other := New(tokens).parseCallArgs(token.Position{}, callee)
	call.Args[0].Name = "changed"
	if other.Args[0].Name != "field0" || call.Args[8].Name != "field8" {
		t.Fatal("argument storage was shared across calls or groups")
	}
}
