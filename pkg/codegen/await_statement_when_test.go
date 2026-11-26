package codegen

import "testing"

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
