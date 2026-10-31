package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGenerateRejectsSemanticErrors(t *testing.T) {
	input := &semantic.Result{Errors: []semantic.Error{{Message: "invalid where predicate"}}}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "invalid where predicate") {
		t.Fatalf("semantic errors were ignored by formal generation: %v", err)
	}
}

func TestGenerateRejectsUntypedRequestParameters(t *testing.T) {
	for _, optional := range []bool{false, true} {
		param := &ast.ParamDecl{Name: "value"}
		if optional {
			param.Default = &ast.Ident{Name: "fallback"}
		}
		file := &ast.File{APIs: []*ast.ApiDecl{{Name: "read", Params: []*ast.ParamDecl{param}, ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}}}}
		generated, err := Generate(result(file), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "value") {
			t.Fatalf("untyped parameter produced source: optional=%v, err=%v", optional, err)
		}
	}
}

func TestGenerateRejectsUntypedFunctionParameters(t *testing.T) {
	for _, directives := range [][]*ast.Directive{
		{{Name: "native"}}, {{Name: "native"}, {Name: "service"}}, {{Name: "service"}},
	} {
		file := &ast.File{Functions: []*ast.FnDecl{{Name: "read", Directives: directives,
			Params: []*ast.ParamDecl{{Name: "value"}}, ReturnType: &ast.TypeRef{Name: "Int"},
		}}}
		generated, err := Generate(result(file), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "value") {
			t.Fatalf("untyped function parameter produced source: %v", err)
		}
	}
}

func TestGenerateRejectsNonStaticCRUDIdentifiers(t *testing.T) {
	for _, name := range []string{"Custom", "Float", "Boolean"} {
		model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: name}, Directives: []*ast.Directive{{Name: "id"}}},
		}}
		generated, err := Generate(result(&ast.File{Models: []*ast.ModelDecl{model}}), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "Record") {
			t.Fatalf("unsupported CRUD identifier produced source: %s, err=%v", name, err)
		}
	}
}

func TestGenerateRejectsIncompleteCRUDTypes(t *testing.T) {
	for _, typ := range []*ast.TypeRef{nil, {Name: "Int", Nullable: true}, {Name: "Int", IsList: true}} {
		model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
			{Name: "id", Type: typ, Directives: []*ast.Directive{{Name: "id"}}},
		}}
		generated, err := Generate(result(&ast.File{Models: []*ast.ModelDecl{model}}), "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "Record") {
			t.Fatalf("incomplete CRUD type produced source: %v, %v", typ, err)
		}
	}
	if err := validateCRUDParameterTypes(&ast.ModelDecl{Name: "Empty"}); err != nil {
		t.Fatalf("no primary key is synthesized by parameter validation: %v", err)
	}
}

func TestGeneratedStaticCRUDIdentifiers(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "github.com/google/uuid"
    "github.com/light-speak/luxo/pkg/lux/api"
)
`)
	for _, item := range []struct{ name, goType string }{{"Int", "int64"}, {"String", "string"}, {"UUID", "uuid.UUID"}} {
		fmt.Fprintf(&source, "func read%s(req *api.Request, out *%s) error {\n", item.name, item.goType)
		writeParamID(&source, item.goType, "\t")
		source.WriteString("*out=id; return nil\n}\n")
		fmt.Fprintf(&source, "func read%sList(req *api.Request, out *[]%s) error {\n", item.name, item.goType)
		writeParamIDs(&source, item.goType, "\t")
		source.WriteString("*out=ids; return nil\n}\n")
	}
	if strings.Contains(source.String(), "ParamJSON") || strings.Contains(source.String(), "json.Unmarshal") {
		t.Fatal("CRUD identifiers use an arbitrary target decoder")
	}
	runGeneratedProtocolPackage(t, source.String(), staticCRUDIdentifierAssertions)
}

const staticCRUDIdentifierAssertions = `package generated
import (
    "encoding/json"
    "reflect"
    "testing"
    "github.com/google/uuid"
    "github.com/light-speak/luxo/pkg/lux/api"
)

func checkID[T any](t *testing.T, value T, scalar func(*api.Request,*T)error, list func(*api.Request,*[]T)error) {
    t.Helper()
    for _, binary := range []bool{false,true} {
        for _, many := range []bool{false,true} {
            name:="id"; var input any=value
            if many {name="ids";input=[]T{value,value}}
            req:=&api.Request{}
            if binary {req.SetBinaryParams([]string{name},1);req.SetParamSlot(0,input)} else {
                raw,err:=json.Marshal(input);if err!=nil{t.Fatal(err)}
                req.Params=map[string]json.RawMessage{name:raw}
            }
            if many {var out []T; if err:=list(req,&out);err!=nil||!reflect.DeepEqual(out,[]T{value,value}){t.Fatalf("list %v: %v %v",binary,out,err)}} else {
                var out T;if err:=scalar(req,&out);err!=nil||!reflect.DeepEqual(out,value){t.Fatalf("scalar %v: %v %v",binary,out,err)}
            }
        }
        missing:=&api.Request{Params:map[string]json.RawMessage{}}
        if binary {missing.Params=nil;missing.SetBinaryParams([]string{"id","ids"},2)}
        var one T;var many []T
        if scalar(missing,&one)==nil||list(missing,&many)==nil{t.Fatal("missing ID accepted")}
    }
}

func TestCRUDIdentifierReaders(t *testing.T) {
    checkID(t,int64(9223372036854775807),readInt,readIntList)
    checkID(t,"user-東京",readString,readStringList)
    checkID(t,uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),readUUID,readUUIDList)
    for _,raw:=range []string{"null","1.5","9223372036854775808","true"} {
        var id int64;req:=&api.Request{Params:map[string]json.RawMessage{"id":[]byte(raw)}}
        if readInt(req,&id)==nil{t.Fatalf("bad ID %s accepted",raw)}
    }
}
`
