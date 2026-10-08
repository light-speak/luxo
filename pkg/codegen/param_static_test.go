package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestBuiltinParamExtractionLeavesUntypedInputToNamedPath(t *testing.T) {
	var source strings.Builder
	if writeBuiltinParamExtraction(&source, &ast.ParamDecl{Name: "value"}, nil, "") || source.Len() != 0 {
		t.Fatal("untyped input was classified as a built-in parameter")
	}
}

func TestGeneratedStaticBuiltinParams(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "fmt"
    "encoding/json"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/light-speak/luxo/pkg/lux/api"
)
type capture[T any] struct { value T }
func (c *capture[T]) Set(value T) { c.value=value }
`)
	for _, name := range []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON"} {
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: name, IsList: list, Nullable: nullable}}
				method := fmt.Sprintf("read%sList%tNull%t", name, list, nullable)
				fmt.Fprintf(&source, "func %s(req *api.Request, out *%s) error {\n", method, resolveGoType(param.Type))
				writeCallableParamExtraction(&source, param, nil, "\t")
				source.WriteString("\t*out = value; return nil\n}\n")
			}
		}
	}
	param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "String", Nullable: true}, Default: &ast.Literal{Kind: token.String, Value: "default"}}
	source.WriteString("func readDefault(req *api.Request, out **string) error {\n")
	writeCallableParamExtraction(&source, param, nil, "\t")
	source.WriteString("\t*out = value; return nil\n}\n")
	for _, name := range []string{"Int", "UUID", "JSON"} {
		field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: name, IsList: true, Nullable: true}}
		fmt.Fprintf(&source, "func set%sList(req *api.Request, builder *capture[%s]) error {\n", name, resolveGoType(field.Type))
		generateParamSet(&source, field, "Set", "\t", nil)
		source.WriteString("\treturn nil\n}\n")
	}
	param.Type.Name = "Int"
	param.Default = &ast.Literal{Kind: token.Int, Value: "42"}
	source.WriteString("func readDefaultInt(req *api.Request, out **int64) error {\n")
	writeCallableParamExtraction(&source, param, nil, "\t")
	source.WriteString("\t*out = value; return nil\n}\n")
	// Execute generated source, not only pattern matching the emitter output.
	runGeneratedProtocolPackage(t, source.String(), staticParamAssertions)
	if strings.Contains(source.String(), "req.ParamJSON") {
		t.Fatal("built-in parameters still use an arbitrary target decoder")
	}
}

const staticParamAssertions = `package generated
import (
    "encoding/json"
    "reflect"
    "testing"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/light-speak/luxo/pkg/lux/api"
)

func input(raw string, binary bool, value any) *api.Request {
    if binary {
        req := &api.Request{BinaryMode:true}
        req.SetBinaryParams([]string{"value"},1)
        req.SetParamSlot(0,value)
        return req
    }
    return &api.Request{Params:map[string]json.RawMessage{"value":[]byte(raw)}}
}

func check[T any](t *testing.T, raw string, value T, scalar func(*api.Request,*T)error,
    nullable func(*api.Request,**T)error, list func(*api.Request,*[]T)error,
    nullableList func(*api.Request,*[]T)error) {
    t.Helper()
    for _, binary := range []bool{false,true} {
        req := input(raw,binary,value)
        var got T
        if err:=scalar(req,&got); err!=nil || !reflect.DeepEqual(got,value) { t.Fatalf("scalar %T: %v %v",value,got,err) }
        var ptr *T
        if err:=nullable(req,&ptr); err!=nil || ptr==nil || !reflect.DeepEqual(*ptr,value) { t.Fatalf("nullable %T: %v %v",value,ptr,err) }
        var rows []T
        req=input("["+raw+"]",binary,[]T{value})
        for _, read := range []func(*api.Request,*[]T)error{list,nullableList} {
            if err:=read(req,&rows); err!=nil || !reflect.DeepEqual(rows,[]T{value}) { t.Fatalf("list %T: %v %v",value,rows,err) }
            if err:=read(input("[]",binary,[]T{}),&rows); err!=nil || rows==nil || len(rows)!=0 { t.Fatalf("empty list %T: %v %v",value,rows,err) }
        }
        req=input("null",binary,nil)
        if err:=scalar(req,&got); err==nil { t.Fatalf("scalar %T accepted null",value) }
        if err:=list(req,&rows); err==nil { t.Fatalf("list %T accepted null",value) }
        if err:=nullable(req,&ptr); err!=nil || ptr!=nil { t.Fatalf("nullable %T did not clear: %v %v",value,ptr,err) }
        if err:=nullableList(req,&rows); err!=nil || rows!=nil { t.Fatalf("nullable list %T did not clear: %v %v",value,rows,err) }
        missing:=&api.Request{}
        if binary { missing.SetBinaryParams([]string{"value"},1) }
        if err:=scalar(missing,&got); err==nil { t.Fatal("accepted omitted required scalar") }
        if err:=nullable(missing,&ptr); err==nil { t.Fatal("accepted omitted nullable required scalar") }
        if err:=list(missing,&rows); err==nil { t.Fatal("accepted omitted required list") }
        if err:=nullableList(missing,&rows); err==nil { t.Fatal("accepted omitted nullable required list") }
    }
}

func TestBuiltinParams(t *testing.T) {
    check(t,"42",int64(42),readIntListfalseNullfalse,readIntListfalseNulltrue,readIntListtrueNullfalse,readIntListtrueNulltrue)
    check(t,"1.5",1.5,readFloatListfalseNullfalse,readFloatListfalseNulltrue,readFloatListtrueNullfalse,readFloatListtrueNulltrue)
    check(t,"true",true,readBooleanListfalseNullfalse,readBooleanListfalseNulltrue,readBooleanListtrueNullfalse,readBooleanListtrueNulltrue)
    check(t,"\"東京\"","東京",readStringListfalseNullfalse,readStringListfalseNulltrue,readStringListtrueNullfalse,readStringListtrueNulltrue)
    check(t,"-42",time.Duration(-42),readDurationListfalseNullfalse,readDurationListfalseNulltrue,readDurationListtrueNullfalse,readDurationListtrueNulltrue)
    check(t,"\"1969-12-31T23:59:59.123456789Z\"",time.Date(1969,12,31,23,59,59,123456789,time.UTC),readDateTimeListfalseNullfalse,readDateTimeListfalseNulltrue,readDateTimeListtrueNullfalse,readDateTimeListtrueNulltrue)
    check(t,"\"550e8400-e29b-41d4-a716-446655440000\"",uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),readUUIDListfalseNullfalse,readUUIDListfalseNulltrue,readUUIDListtrueNullfalse,readUUIDListtrueNulltrue)
    check(t,"\"12345678901234567890.123456789\"",decimal.RequireFromString("12345678901234567890.123456789"),readDecimalListfalseNullfalse,readDecimalListfalseNulltrue,readDecimalListtrueNullfalse,readDecimalListtrueNulltrue)
    check(t,"\"AAH/\"",[]byte{0,1,255},readBytesListfalseNullfalse,readBytesListfalseNulltrue,readBytesListtrueNullfalse,readBytesListtrueNulltrue)
    raw:=json.RawMessage("{\"n\":123456789012345678901234567890,\"d\":1.234567890123456789}")
    check(t,string(raw),raw,readJSONListfalseNullfalse,readJSONListfalseNulltrue,readJSONListtrueNullfalse,readJSONListtrueNulltrue)
}

func TestDefaultNullDoesNotReuseDefault(t *testing.T) {
    for _, binary:=range []bool{false,true} {
        missing:=&api.Request{}
        if binary { missing.SetBinaryParams([]string{"value"},1) }
        var got *string
        if err:=readDefault(missing,&got); err!=nil || got==nil || *got!="default" { t.Fatalf("missing: %v %v",got,err) }
        if err:=readDefault(input("null",binary,nil),&got); err!=nil || got!=nil { t.Fatalf("null retained default: %v %v",got,err) }
        if err:=readDefault(input("\"provided\"",binary,"provided"),&got); err!=nil || got==nil || *got!="provided" { t.Fatalf("value: %v %v",got,err) }
        var number *int64
        if err:=readDefaultInt(missing,&number); err!=nil || number==nil || *number!=42 { t.Fatalf("missing integer: %v %v",number,err) }
        if err:=readDefaultInt(input("null",binary,nil),&number); err!=nil || number!=nil { t.Fatalf("null integer: %v %v",number,err) }
        if err:=readDefaultInt(input("7",binary,int64(7)),&number); err!=nil || number==nil || *number!=7 { t.Fatalf("integer: %v %v",number,err) }
    }
}

func checkCRUDList[T any](t *testing.T, raw string, value T, write func(*api.Request,*capture[[]T])error) {
    t.Helper()
    for _, binary:=range []bool{false,true} {
        var builder capture[[]T]
        if err:=write(input("["+raw+"]",binary,[]T{value}),&builder); err!=nil || !reflect.DeepEqual(builder.value,[]T{value}) { t.Fatalf("CRUD %T: %v %v",value,builder.value,err) }
        if err:=write(input("null",binary,nil),&builder); err!=nil || builder.value!=nil { t.Fatalf("CRUD null %T: %v %v",value,builder.value,err) }
        if err:=write(input("[]",binary,[]T{}),&builder); err!=nil || builder.value==nil || len(builder.value)!=0 { t.Fatalf("CRUD empty %T: %v %v",value,builder.value,err) }
    }
}

func TestCRUDNullableLists(t *testing.T) {
    checkCRUDList(t,"42",int64(42),setIntList)
    checkCRUDList(t,"\"550e8400-e29b-41d4-a716-446655440000\"",uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),setUUIDList)
    checkCRUDList(t,"null",json.RawMessage("null"),setJSONList)
}
`
