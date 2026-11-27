package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedStatementWhenAwaitKeepsReturnScope(t *testing.T) {
	runGeneratedFixture(t, `
fn direct(early: Boolean): String {
 when {
  early -> await {
   if early { return "outer" }
   42
  }
  else -> 13
 }
 return "after"
}
fn subject(early: Boolean): String {
 when (await { if early { return "subject" } 42 }) {
  42 -> 13
  else -> 7
 }
 return "after"
}
fn lexical(early: Boolean): String {
 val value = when {
  early -> {
   when {
    early -> await { if early { return "branch" } 42 }
    else -> 13
   }
   "wrong"
  }
  else -> "after"
 }
 return value
}
`, `package generated
import ("context"; "testing")
func TestGeneratedStatementWhenAwait(t *testing.T) {
 app:=&App{}
 for _, tt := range []struct {
  name string
  call func(context.Context, bool) (string, error)
  early string
 }{
  {"direct",app.direct,"outer"},
  {"subject",app.subject,"subject"},
  {"lexical",app.lexical,"branch"},
 }{
  t.Run(tt.name,func(t *testing.T){
   if got,err:=tt.call(t.Context(),true); got!=tt.early||err!=nil{t.Fatalf("early = %q %v, want %q",got,err,tt.early)}
   if got,err:=tt.call(t.Context(),false); got!="after"||err!=nil{t.Fatalf("normal = %q %v",got,err)}
  })
 }
}
`, StableIDs{})
}

func TestAddressedYieldPreservesReturnTargets(t *testing.T) {
	for _, sample := range []struct {
		name, result string
		inline       *inlineValueScope
	}{
		{"closure", "return &_yield0\n", nil},
		{"inline", "result = &_yield0\nbreak done\n", &inlineValueScope{
			controlScope: controlScope{label: "done"}, result: "result", yield: true,
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c := newCompiler(nil)
			c.indent, c.inForExpr, c.yieldAddr, c.inlineValue = "", true, true, sample.inline
			item := &ast.Ident{Name: "item", TypeTagged: ast.TypeTagged{TypeTag: "Int"}}
			c.compileExpr(&ast.YieldExpr{Value: item})
			if got, want := compilerOut(c), "var _yield0 int64 = item\n"+sample.result; got != want {
				t.Fatalf("yield generated %q, want %q", got, want)
			}
		})
	}
}

func TestConditionalValueReturnWritesWithoutTemporaryAllocation(t *testing.T) {
	const want = "\tif item > 0 { return true }\n"
	c := newCompiler(nil)
	c.indent = "\t"
	condition, value := strings.Clone("item > 0"), strings.Clone("true")
	reset := func() {
		c.b.Reset()
		c.b.Grow(len(want))
	}
	baseline := testing.AllocsPerRun(20, func() {
		reset()
		c.b.WriteString(want)
	})
	got := testing.AllocsPerRun(20, func() {
		reset()
		c.writeConditionalValue(condition, value)
	})
	if out := compilerOut(c); out != want {
		t.Fatalf("conditional return generated %q, want %q", out, want)
	}
	if got != baseline {
		t.Fatalf("conditional return allocated %g beyond its output buffer", got-baseline)
	}
}
