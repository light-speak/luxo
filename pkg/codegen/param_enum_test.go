package codegen

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedStaticEnumParams(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "context"
    "fmt"
    "github.com/light-speak/luxo/pkg/lux"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
var _ = fmt.Errorf
var _ = codec.NewDecoder
type capture[T any] struct { value T }
func (c *capture[T]) Set(value T) { c.value=value }
type queryCapture struct { calls int }
func (q *queryCapture) Where(conditions ...lux.Condition) *queryCapture { q.calls++; return q }
func (q *queryCapture) Count(context.Context) (int64,error) {return 1,nil}
type App struct { Record *queryCapture }
`)
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	generateEnum(&source, &ast.EnumDecl{Name: "Empty"})
	for _, list := range []bool{false, true} {
		for _, shape := range []string{"Required", "Nullable", "Optional", "Default", "DefaultRequired"} {
			if list && strings.HasPrefix(shape, "Default") {
				continue
			}
			param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "State", IsList: list, Nullable: shape != "Required" && shape != "DefaultRequired"}}
			if shape == "Optional" {
				param.Default = &ast.Literal{Kind: token.Null, Value: "null"}
			} else if strings.HasPrefix(shape, "Default") {
				param.Default = &ast.MemberExpr{Object: &ast.Ident{Name: "State"}, Field: "OPEN"}
			}
			fmt.Fprintf(&source, "func read%sList%t(req *api.Request,out *%s) error {\n", shape, list, resolveGoType(param.Type))
			writeCallableParamExtraction(&source, param, map[string]bool{"State": true}, "\t")
			source.WriteString("*out=value; return nil\n}\n")
			textType := *param.Type
			textType.Name = "String"
			fmt.Fprintf(&source, "func readInferred%sList%t(req *api.Request,out *%s) error {\n", shape, list, resolveGoType(&textType))
			writeInferredEnumParamExtraction(&source, param, map[string]bool{"State": true}, "\t")
			source.WriteString("*out=value; return nil\n}\n")
		}
	}
	for _, list := range []bool{false, true} {
		field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "State", IsList: list, Nullable: true}}
		fmt.Fprintf(&source, "func setList%t(req *api.Request,builder *capture[%s]) error {\n", list, resolveGoType(field.Type))
		generateParamSet(&source, field, "Set", "\t", map[string]bool{"State": true})
		source.WriteString("return nil\n}\n")
		writeInferredEnumFixture(&source, list)
	}
	if strings.Contains(source.String(), "req.ParamJSON") {
		t.Fatal("enum input still uses an arbitrary target decoder")
	}
	assertions := enumParamAssertions
	benchmark := os.Getenv("LUXO_ENUM_BENCHMARK") == "1"
	if benchmark {
		// The historical decoder is injected only into the opt-in benchmark build.
		source.WriteString(`
func beforeScalar(req *api.Request,out *State) error { return req.LegacyParamJSON("value",out) }
func beforeList(req *api.Request,out *[]State) error { return req.LegacyParamJSON("value",out) }
`)
		assertions = strings.Replace(assertions, `"testing"`, "\"testing\"\n\"fmt\"", 1) + enumParamBenchmarks
	}
	runGeneratedProtocolPackage(t, source.String(), assertions, benchmark)
}

func writeInferredEnumFixture(source *strings.Builder, list bool) {
	param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "State", IsList: list}}
	model := &ast.ModelDecl{Name: "Record", Fields: []*ast.FieldDecl{testField("id", "Int"), testField("state", "State")}}
	op, name := "eq", "countRecordsByState"
	if list {
		op, name = "in", "countRecordsByStateIn"
	}
	inf := &InferredAPI{Action: "count", ModelName: "Record", Groups: []ClauseGroup{{Clauses: []InferClause{{Field: "state", Op: op}}}}}
	generateInferredHandler(source, &ast.ApiDecl{Name: name, Params: []*ast.ParamDecl{param}}, inf, map[string]bool{"State": true}, model)
}

const enumParamAssertions = `package generated
import (
    "context"
    "encoding/json"
    "reflect"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
)

func enumInput(raw string,binary bool,value any) *api.Request {
    req:=&api.Request{}
    if binary {
        req.SetBinaryParams([]string{"value"},1)
        if raw!="" {req.SetParamSlot(0,value)}
    } else {
        req.Params=map[string]json.RawMessage{}
        if raw!="" {req.Params["value"]=[]byte(raw)}
    }
    return req
}

func TestEnumScalarParams(t *testing.T) {
    if Empty("").IsValid() || Empty("OPEN").IsValid() {t.Fatal("empty enum accepted a member")}
    for _,binary:=range []bool{false,true} {
        for _,value:=range []string{"OPEN","CLOSED","FUTURE","","open"} {
            raw:="\""+value+"\""
            valid:=value=="OPEN" || value=="CLOSED"
            req:=enumInput(raw,binary,value)
            var got State
            if err:=readRequiredListfalse(req,&got); (err==nil)!=valid || (valid && string(got)!=value) {t.Fatalf("scalar %q: %v %v",value,got,err)}
            var text string
            if err:=readInferredRequiredListfalse(req,&text); (err==nil)!=valid || (valid && text!=value) {t.Fatalf("inferred scalar %q: %v %v",value,text,err)}
            for _,read:=range []func(*api.Request,**State)error{readNullableListfalse,readOptionalListfalse,readDefaultListfalse} {
                var ptr *State
                if err:=read(req,&ptr); (err==nil)!=valid || (valid && (ptr==nil || string(*ptr)!=value)) {t.Fatalf("nullable %q: %v %v",value,ptr,err)}
            }
            for _,read:=range []func(*api.Request,**string)error{readInferredNullableListfalse,readInferredOptionalListfalse,readInferredDefaultListfalse} {
                var ptr *string
                if err:=read(req,&ptr); (err==nil)!=valid || (valid && (ptr==nil || *ptr!=value)) {t.Fatalf("inferred nullable %q: %v %v",value,ptr,err)}
            }
            var builder capture[*State]
            if err:=setListfalse(req,&builder); (err==nil)!=valid || (valid && (builder.value==nil || string(*builder.value)!=value)) {t.Fatalf("CRUD scalar %q: %v %v",value,builder.value,err)}
        }
        missing:=enumInput("",binary,nil)
        var got *State
        if err:=readNullableListfalse(missing,&got); err==nil {t.Fatal("required nullable omitted")}
        if err:=readOptionalListfalse(missing,&got); err!=nil || got!=nil {t.Fatalf("optional missing: %v %v",got,err)}
        if err:=readDefaultListfalse(missing,&got); err!=nil || got==nil || *got!=StateOPEN {t.Fatalf("default: %v %v",got,err)}
        var required State
        if err:=readDefaultRequiredListfalse(missing,&required); err!=nil || required!=StateOPEN {t.Fatalf("required default: %v %v",required,err)}
        var requiredText string
        if err:=readInferredDefaultRequiredListfalse(missing,&requiredText); err!=nil || requiredText!="OPEN" {t.Fatalf("inferred required default: %v %v",requiredText,err)}
        if err:=readDefaultListfalse(enumInput("null",binary,nil),&got); err!=nil || got!=nil {t.Fatalf("null retained default: %v %v",got,err)}
        var text *string
        if err:=readInferredDefaultListfalse(missing,&text); err!=nil || text==nil || *text!="OPEN" {t.Fatalf("inferred default: %v %v",text,err)}
        if err:=readInferredDefaultListfalse(enumInput("null",binary,nil),&text); err!=nil || text!=nil {t.Fatalf("inferred default null: %v %v",text,err)}
        if err:=readRequiredListfalse(enumInput("7",binary,int64(7)),new(State)); err==nil {t.Fatal("enum accepted integer")}
    }
}

func TestEnumListParams(t *testing.T) {
    for _,binary:=range []bool{false,true} {
        for _,tc:=range []struct{raw string; values []string; valid bool}{
            {"[]",[]string{},true},{"[\"OPEN\",\"CLOSED\"]",[]string{"OPEN","CLOSED"},true},
            {"[\"FUTURE\"]",[]string{"FUTURE"},false},{"[\"OPEN\",\"FUTURE\"]",[]string{"OPEN","FUTURE"},false},
        } {
            req:=enumInput(tc.raw,binary,tc.values)
            want:=make([]State,len(tc.values)); for i,v:=range tc.values {want[i]=State(v)}
            for _,read:=range []func(*api.Request,*[]State)error{readRequiredListtrue,readNullableListtrue,readOptionalListtrue} {
                var got []State
                if err:=read(req,&got); (err==nil)!=tc.valid || (tc.valid && !reflect.DeepEqual(got,want)) {t.Fatalf("list %s: %v %v",tc.raw,got,err)}
            }
            for _,read:=range []func(*api.Request,*[]string)error{readInferredRequiredListtrue,readInferredNullableListtrue,readInferredOptionalListtrue} {
                var got []string
                if err:=read(req,&got); (err==nil)!=tc.valid || (tc.valid && !reflect.DeepEqual(got,tc.values)) {t.Fatalf("inferred list %s: %v %v",tc.raw,got,err)}
            }
            var builder capture[[]State]
            if err:=setListtrue(req,&builder); (err==nil)!=tc.valid || (tc.valid && !reflect.DeepEqual(builder.value,want)) {t.Fatalf("CRUD list %s: %v %v",tc.raw,builder.value,err)}
        }
        var got []State
        missing:=enumInput("",binary,nil)
        if err:=readNullableListtrue(missing,&got); err==nil {t.Fatal("required list omitted")}
        if err:=readOptionalListtrue(missing,&got); err!=nil || got!=nil {t.Fatalf("optional list: %v %v",got,err)}
        if err:=readNullableListtrue(enumInput("null",binary,nil),&got); err!=nil || got!=nil {t.Fatalf("nullable list: %v %v",got,err)}
        if err:=readRequiredListtrue(enumInput("[7]",binary,[]int64{7}),&got); err==nil {t.Fatal("enum list accepted integers")}
    }
}

func TestInferredEnumQueries(t *testing.T) {
    for _,binary:=range []bool{false,true} {
        for _,list:=range []bool{false,true} {
            for _,value:=range []string{"OPEN","FUTURE"} {
                app:=&App{Record:&queryCapture{}}
                read:=handleCountRecordsByState(app)
                req:=enumInput("\""+value+"\"",binary,value)
                if list {
                    read=handleCountRecordsByStateIn(app)
                    req=enumInput("[\""+value+"\"]",binary,[]string{value})
                }
                req.Buf=&api.ResponseBuf{}
                err:=read(context.Background(),req)
                if value=="OPEN" {
                    if err!=nil || app.Record.calls!=1 {t.Fatalf("valid query: %v %v",app.Record,err)}
                } else if err==nil || app.Record.calls!=0 {t.Fatalf("invalid member reached query: %v %v",app.Record,err)}
            }
        }
    }
}
`

const enumParamBenchmarks = `
func BenchmarkEnumParams(b *testing.B) {
    for _,binary:=range []bool{false,true} {
        req:=enumInput("\"OPEN\"",binary,"OPEN")
        for _,tc:=range []struct{name string;read func(*api.Request,*State)error}{{"before",beforeScalar},{"after",readRequiredListfalse}} {
            b.Run(fmt.Sprintf("scalar/binary=%t/%s",binary,tc.name),func(b *testing.B){
                b.ReportAllocs()
                for b.Loop() { var out State; if err:=tc.read(req,&out); err!=nil || out!=StateOPEN {b.Fatal(out,err)} }
            })
        }
        for _,count:=range []int{0,1,128} {
            values:=make([]string,count)
            for i:=range values {values[i]="OPEN"}
            raw,err:=json.Marshal(values); if err!=nil {b.Fatal(err)}
            req:=enumInput(string(raw),binary,values)
            for _,tc:=range []struct{name string;read func(*api.Request,*[]State)error}{{"before",beforeList},{"after",readRequiredListtrue}} {
                b.Run(fmt.Sprintf("list%d/binary=%t/%s",count,binary,tc.name),func(b *testing.B){
                    b.ReportAllocs()
                    for b.Loop() { var out []State; if err:=tc.read(req,&out); err!=nil || len(out)!=count {b.Fatal(len(out),err)} }
                })
            }
        }
    }
}
`
