package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"testing"
)

func TestChannelCloseIgnoresNonChannelReceiver(t *testing.T) {
	a := &Analyzer{}
	scope := NewScope()
	scope.Define(&Symbol{Name: "value", Kind: SymVariable, Type: &ResolvedType{Kind: TypeInt}})
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "value"}, Field: "close"}}
	if result, handled := a.checkChannelCall(call, scope); result != nil || handled {
		t.Fatal("non-channel receiver treated as a channel")
	}
}

func TestTypedChannelContract(t *testing.T) {
	for _, body := range []string{
		`val ch: Channel<Int> = Channel(1) ch <- 42 return <-ch`,
		`val ch: Channel<String> = Channel(1) ch <- "ok" ch.close() for item in ch { return 1 } return 0`,
		`val ch: Channel<Int> = Channel(1) return when { <-ch -> 1 else -> 0 }`,
	} {
		result := analyze(t, `api probe(): Int { `+body+` }`)
		if len(result.Errors) != 0 {
			t.Fatal(result.Errors)
		}
	}
}

func TestTypedChannelRejectsInvalidOperations(t *testing.T) {
	for _, body := range []string{
		`val ch = Channel(1) return 1`,
		`return Channel(1)`,
		`1.close() return 1`,
		`val ch: Channel<Int> = Channel(1) ch <- "bad" return 1`,
		`val ch: Channel<Int> = Channel("bad") return 1`,
		`val ch: Channel<Int> = Channel(-1) return 1`,
		`val ch: Channel<Int> = Channel(1, 2) return 1`,
		`val ch: Channel<Int> = Channel(1) ch.close(1) return 1`,
		`val ch: Channel<Int, String> = Channel(1) return 1`,
		`val ch: Channel = Channel(1) return 1`,
		`return <-1`,
		`1 <- 2 return 1`,
		`val ch: Channel<String> = Channel(1) return when { <-ch -> { value -> value } else -> 1 }`,
		`val ch: Channel<Int> = Channel(1) return when { <-ch -> 1 true -> 2 else -> 0 }`,
		`val ch: Channel<Int> = Channel(1) return when { <-ch -> { first, second -> 1 } else -> 0 }`,
	} {
		t.Run(body, func(t *testing.T) {
			if result := analyze(t, `api probe(): Int { `+body+` }`); len(result.Errors) == 0 {
				t.Fatal("invalid channel accepted")
			}
		})
	}
}
