package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedStaticStructuredParams(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "bytes"
    "encoding/json"
    "fmt"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/jsonutil"
)
`)
	child := &ast.ModelDecl{Name: "Child", Fields: []*ast.FieldDecl{testField("id", "Int")}}
	input := &ast.ModelDecl{Name: "Input", Fields: []*ast.FieldDecl{
		testField("name", "String"), testField("bytes", "Bytes"), testField("stamp", "DateTime"), testField("json", "JSON"),
		{Name: "child", Type: &ast.TypeRef{Name: "Child", Nullable: true}},
		{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"Child": {"id": 1}, "Input": {"name": 1, "bytes": 2, "stamp": 3, "json": 4, "child": 5, "children": 6},
	})
	for _, model := range []*ast.ModelDecl{child, input} {
		generateModel(&source, model, nil)
		g.generateReadLuxo(&source, model, nil)
		g.generateReadColumnar(&source, model, nil)
		g.generateReadJSONInput(&source, model, nil)
	}
	for _, list := range []bool{false, true} {
		for _, shape := range []string{"Required", "Nullable", "Optional"} {
			writeStructuredFixtureReader(&source, list, shape)
		}
		writeCanonicalStructuredFixtureReader(&source, list)
	}
	if strings.Contains(source.String(), "req.ParamJSON") || strings.Contains(source.String(), "req.BinaryMode") {
		t.Fatal("structured parameters still use a transport-specific arbitrary-target decoder")
	}
	assertions := structuredParamAssertions + structuredJSONMigrationAssertions + localJSONTransportBenchmarks
	benchmark := structuredParamBenchmarkEnabled()
	if benchmark {
		for _, list := range []bool{false, true} {
			writeStructuredReferenceReader(&source, list)
		}
		assertions = strings.Replace(assertions, `"testing"`, "\"testing\"\n\"os\"\n\"fmt\"", 1) + structuredParamBenchmarks
	}
	runGeneratedProtocolPackage(t, source.String(), assertions, benchmark)
}

// Preserve the previous generated static reader for equivalent route benchmarks.
func writeCanonicalStructuredFixtureReader(b *strings.Builder, list bool) {
	param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "Input", IsList: list}}
	fmt.Fprintf(b, "func readCanonicalList%t(req *api.Request, out *[]Input) error {\n", list)
	writeStructuredParamDeclaration(b, param, nil, "\t")
	if list {
		writeStructuredListParamDecode(b, param, "\t")
		b.WriteString("*out=value\n")
	} else {
		writeStructuredScalarParamDecode(b, param, "\t")
		b.WriteString("*out=[]Input{value}\n")
	}
	b.WriteString("return nil\n}\n")
}

// Retain the pre-migration handler only as a performance reference.
func writeStructuredReferenceReader(source *strings.Builder, list bool) {
	param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "Input", IsList: list}}
	fmt.Fprintf(source, "func readBeforeList%t(req *api.Request, out *[]Input) error {\nvar value %s\nif req.BinaryMode {\n", list, resolveGoType(param.Type))
	if list {
		writeStructuredListParamDecode(source, param, "\t")
	} else {
		writeStructuredScalarParamDecode(source, param, "\t")
	}
	source.WriteString("} else { if err:=req.LegacyParamJSON(\"value\",&value); err!=nil { return err } }\n")
	if list {
		source.WriteString("*out=value\n")
	} else {
		source.WriteString("*out=[]Input{value}\n")
	}
	source.WriteString("return nil\n}\n")
}

func writeStructuredFixtureReader(source *strings.Builder, list bool, shape string) {
	param := &ast.ParamDecl{Name: "value", Type: &ast.TypeRef{Name: "Input", IsList: list, Nullable: shape != "Required"}}
	if shape == "Optional" {
		param.Default = &ast.Literal{Kind: token.Null, Value: "null"}
	}
	fmt.Fprintf(source, "func read%sList%t(req *api.Request, out *[]Input) error {\n", shape, list)
	writeCallableParamExtraction(source, param, nil, "\t")
	switch {
	case list:
		source.WriteString("*out = value\n")
	case param.Type.Nullable:
		source.WriteString("if value != nil { *out = []Input{*value} } else { *out = nil }\n")
	default:
		source.WriteString("*out = []Input{value}\n")
	}
	source.WriteString("return nil\n}\n")
}

const structuredParamAssertions = `package generated
import (
    "bytes"
    "context"
    "encoding/json"
    "net/http/httptest"
    "reflect"
    "strings"
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/schema"
)

const object = "{\"name\":\"東京\",\"bytes\":\"AAH/\",\"stamp\":\"1969-12-31T23:59:59.123456789Z\",\"json\":{\"n\":123456789012345678901234567890},\"child\":{\"id\":9223372036854775807},\"children\":[{\"id\":-9223372036854775808}]}"

func structuredSchema(list, nullable, optional bool) *schema.Schema {
    s:=schema.New()
    s.RegisterType(&schema.TypeDecl{Name:"Child",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt}}})
    s.RegisterType(&schema.TypeDecl{Name:"Input",Fields:[]schema.Field{
        {ID:1,Name:"name",Type:schema.FieldString}, {ID:2,Name:"bytes",Type:schema.FieldBytes},
        {ID:3,Name:"stamp",Type:schema.FieldDateTime}, {ID:4,Name:"json",Type:schema.FieldJSON},
        {ID:5,Name:"child",Type:schema.FieldModel,TypeName:"Child",Nullable:true},
        {ID:6,Name:"children",Type:schema.FieldModel,TypeName:"Child",IsList:true},
    }})
    s.RegisterAPI(&schema.API{ID:42,Name:"read",Params:[]schema.Param{
        {ID:9,Name:"value",Type:schema.FieldModel,TypeName:"Input",IsList:list,Nullable:nullable,HasDefault:optional},
        {ID:2,Name:"count",Type:schema.FieldInt},
    }})
    return s
}

func requestBody(t testing.TB, router *api.Router, raw string, list, binary bool) []byte {
    t.Helper()
    if !binary {
        body:="{\"$api\":\"read\",\"count\":42"
        if raw!="" {body+=",\"value\":"+raw}
        return []byte(body+"}")
    }
    params:=map[string]any{"count":int64(42)}
    switch raw {
    case "":
    case "null": params["value"]=nil
    case "[]": params["value"]=api.BinaryMessages{}
    default:
        message,err:=router.Schema.JSONToBinary("Input",[]byte(object))
        if err!=nil {t.Fatal(err)}
        if raw=="broken" {message=[]byte{0,1}}
        if list {params["value"]=api.BinaryMessages{message}} else {params["value"]=api.BinaryMessage(message)}
    }
    body,err:=api.EncodeBinaryRequest(42,nil,params,router.Registry.ParamOrder("read"))
    if err!=nil {t.Fatal(err)}
    return body
}

func testStructuredCase(t *testing.T, read func(*api.Request,*[]Input)error, list, nullable, optional, binary bool) {
    t.Helper()
    router:=api.NewRouterWithOptions(api.RouterOptions{})
    router.Schema=structuredSchema(list,nullable,optional)
    router.Registry.RegisterSchemaAPIs(router.Schema)
    var got []Input
    router.HandleLocal("read",func(_ context.Context,req *api.Request)error {
        if !req.BinaryMode && len(req.BinaryParams())!=0 {t.Fatal("local JSON unexpectedly prepared Binary")}
        if n,err:=req.ParamInt("count"); err!=nil || n!=42 {t.Fatalf("scalar beside structure: %d %v",n,err)}
        return read(req,&got)
    })
    send:=func(raw string)int {
        req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(requestBody(t,router,raw,list,binary)))
        content:="application/json"; if binary {content="application/x-luxo"}
        req.Header.Set("Content-Type",content)
        if binary {req.Header.Set("X-Luxo-Mode","binary")}
        response:=httptest.NewRecorder(); router.ServeHTTP(response,req)
        if response.Code!=200 && response.Code!=400 {t.Fatalf("unexpected response: %d %s",response.Code,response.Body.String())}
        return response.Code
    }
    raw:=object; if list {raw="["+raw+"]"}
    if code:=send(raw); code!=200 {t.Fatalf("valid structured input: HTTP %d",code)}
    want:=[]Input{{Name:"東京",Bytes:[]byte{0,1,255},Stamp:time.Date(1969,12,31,23,59,59,123456789,time.UTC),
        Json:json.RawMessage("{\"n\":123456789012345678901234567890}"),Child:&Child{Id:9223372036854775807},Children:[]Child{{Id:-9223372036854775808}}}}
    if !reflect.DeepEqual(got,want) {t.Fatalf("decoded structure: %#v; want %#v",got,want)}
    retained:=got
    if !binary || nullable {
        if code:=send("null"); (code==200)!=nullable || (nullable && got!=nil) {t.Fatalf("null: %d %#v",code,got)}
    }
    if code:=send(""); (code==200)!=optional || (optional && got!=nil) {t.Fatalf("missing: %d %#v",code,got)}
    if list {if code:=send("[]"); code!=200 || got==nil || len(got)!=0 {t.Fatalf("empty: %d %#v",code,got)}}
    if binary {if code:=send("broken"); code!=400 {t.Fatalf("damaged structured message: %d",code)}}
    if !binary {
        for _,bad:=range []string{"{\"bytes\":false}","{\"stamp\":0}","{\"name\":true}","{\"unknown\":1}","{\"children\":[null]}"} {
            if list {bad="["+bad+"]"}
            if code:=send(bad); code!=400 {t.Fatalf("invalid structured input accepted: %s",bad)}
        }
    }
    // Binary Bytes/JSON intentionally borrow the request buffer during the handler.
    // Prepared JSON messages own their storage and must survive request reuse.
    if !binary && !reflect.DeepEqual(retained,want) {t.Fatal("request reuse changed retained JSON value")}
}

func TestStructuredTransport(t *testing.T) {
    for _,tc:=range []struct{name string; read func(*api.Request,*[]Input)error; list,nullable,optional bool}{
        {"required",readRequiredListfalse,false,false,false}, {"nullable",readNullableListfalse,false,true,false},
        {"optional",readOptionalListfalse,false,true,true}, {"requiredList",readRequiredListtrue,true,false,false},
        {"nullableList",readNullableListtrue,true,true,false}, {"optionalList",readOptionalListtrue,true,true,true},
    } {
        for _,mode:=range []string{"JSON","Binary"} {
            t.Run(tc.name+mode,func(t *testing.T){testStructuredCase(t,tc.read,tc.list,tc.nullable,tc.optional,strings.EqualFold(mode,"binary"))})
        }
    }
}
`

// These assertions describe observable input semantics, not the current
// intermediate Binary representation that the direct JSON reader will remove.
const structuredJSONMigrationAssertions = `
func TestStructuredJSONLastFieldAndMissingSemantics(t *testing.T) {
 for _,list:=range []bool{false,true} {
  for _,sample:=range []struct{name,raw string; want Input}{
   {"empty object","{}",Input{}},
   {"escaped duplicate name","{\"name\":true,\"na\\u006de\":\"final\"}",Input{Name:"final"}},
   {"overridden nested value","{\"child\":{\"id\":\"wrong\"},\"child\":{\"id\":42}}",Input{Child:&Child{Id:42}}},
   {"overridden bytes","{\"bytes\":[1,2],\"bytes\":\"AAH/\"}",Input{Bytes:[]byte{0,1,255}}},
   {"overridden nullable child","{\"child\":{\"id\":42},\"child\":null}",Input{}},
   {"present empty list","{\"children\":[]}",Input{Children:[]Child{}}},
   {"opaque JSON null","{\"json\":null}",Input{Json:json.RawMessage("null")}},
  } {
   t.Run(sample.name+map[bool]string{false:"/Object",true:"/List"}[list],func(t *testing.T){
    raw:=sample.raw;if list {raw="["+raw+"]"}
    got,calls,code:=readStructuredJSON(t,raw,list)
    if code!=200 || calls!=1 || !reflect.DeepEqual(got,[]Input{sample.want}) {t.Fatalf("code=%d calls=%d got=%#v want=%#v",code,calls,got,sample.want)}
   })
  }
 }
}
func TestStructuredJSONRejectsInvalidFinalOrMalformedOverwrittenValues(t *testing.T) {
 for _,list:=range []bool{false,true} {
  for _,raw:=range []string{
   "{\"name\":\"valid\",\"name\":true}",
   "{\"name\":\"valid\",\"name\":null}",
   "{\"name\":\"\\uD800\",\"name\":\"valid\"}",
   "{\"name\":\""+string([]byte{255})+"\",\"name\":\"valid\"}",
   "{\"child\":{\"id\":42,\"unknown\":1}}",
   "{\"child\":{\"id\":9223372036854775808}}",
   "{\"children\":[null]}",
   "{\"children\":[{\"id\":1},{\"id\":false}]}",
  } {
   if list {raw="["+raw+"]"}
   got,calls,code:=readStructuredJSON(t,raw,list)
   if code!=400 || calls!=0 || got!=nil {t.Fatalf("invalid input published: %q code=%d calls=%d got=%#v",raw,code,calls,got)}
  }
 }
}
func readStructuredJSON(t *testing.T,raw string,list bool)([]Input,int,int) {
 t.Helper()
 router:=api.NewRouter()
 router.Schema=structuredSchema(list,false,false)
 router.Registry.RegisterSchemaAPIs(router.Schema)
 var got []Input
 var borrowed []byte
 calls:=0
 router.HandleLocal("read",func(_ context.Context,req *api.Request)error {
  if len(req.BinaryParams())!=0 || len(req.BinaryRequest())!=0 {t.Fatal("local JSON still encoded to Binary")}
  borrowed=req.Params["value"]
  read:=readRequiredListfalse;if list {read=readRequiredListtrue}
  if err:=read(req,&got);err!=nil {return err}
  calls++
  return nil
 })
 body:=requestBody(t,router,raw,list,false)
 response:=httptest.NewRecorder()
 router.ServeHTTP(response,httptest.NewRequest("POST","/luxo",bytes.NewReader(body)))
 // Typed JSON inputs must not retain mutable request/decoder storage.
 clear(body)
 clear(borrowed)
 return got,calls,response.Code
}
func TestStructuredJSONOwnsPrecisionAndMutableValues(t *testing.T) {
 want:=Input{Name:"東京",Bytes:[]byte{0,1,255},Stamp:time.Date(1969,12,31,23,59,59,123456789,time.UTC),
  Json:json.RawMessage("{\"n\":123456789012345678901234567890}"),Child:&Child{Id:9223372036854775807},Children:[]Child{{Id:-9223372036854775808}}}
 for _,list:=range []bool{false,true} {
  raw:=object;if list {raw="["+raw+"]"}
  got,calls,code:=readStructuredJSON(t,raw,list)
  if code!=200 || calls!=1 || !reflect.DeepEqual(got,[]Input{want}) {t.Fatalf("input aliases request or loses precision: %d %d %#v",code,calls,got)}
 }
}
`

const localJSONTransportBenchmarks = `
func BenchmarkLocalJSONTransport(b *testing.B) {
 for _,sample:=range []struct{name,raw string;list bool}{
  {"object",object,false},
  {"empty-list","[]",true},
  {"nested-128","["+strings.Repeat(object+",",127)+object+"]",true},
 } {
  for _,binary:=range []bool{false,true} {
   mode:="json";if binary {mode="binary"}
   for _,local:=range []bool{false,true} {
    name:="canonical";if local {name="local"}
    b.Run(sample.name+"/"+mode+"/"+name,func(b *testing.B){
     router:=api.NewRouterWithOptions(api.RouterOptions{})
     router.Schema=structuredSchema(sample.list,false,false);router.Registry.RegisterSchemaAPIs(router.Schema)
     read:=readCanonicalListfalse;if sample.list {read=readCanonicalListtrue}
     if local {read=readRequiredListfalse;if sample.list {read=readRequiredListtrue}}
     var got []Input
     handler:=func(_ context.Context,req *api.Request)error{
      if _,err:=req.ParamInt("count");err!=nil {return err}
      return read(req,&got)
     }
     if local {router.HandleLocal("read",handler)} else {router.Handle("read",handler)}
     body:=requestBody(b,router,sample.raw,sample.list,binary)
     // Build a complete binary list here: requestBody's older single-item fixture
     // intentionally serves error/null tests and does not parse arbitrary lists.
     if binary && sample.name=="nested-128" {
      message,err:=router.Schema.JSONToBinary("Input",[]byte(object));if err!=nil {b.Fatal(err)}
      messages:=make(api.BinaryMessages,128);for i:=range messages {messages[i]=message}
      body,err=api.EncodeBinaryRequest(42,nil,map[string]any{"value":messages,"count":int64(42)},router.Registry.ParamOrder("read"));if err!=nil {b.Fatal(err)}
     }
     want:=1;if sample.name=="empty-list" {want=0};if sample.name=="nested-128" {want=128}
     b.ReportAllocs()
     for b.Loop() {
      req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
      if binary {req.Header.Set("X-Luxo-Mode","binary");req.Header.Set("Content-Type","application/x-luxo")}
      response:=httptest.NewRecorder();router.ServeHTTP(response,req)
      if response.Code!=200 || len(got)!=want {b.Fatalf("invalid result: %d %d %s",response.Code,len(got),response.Body.String())}
     }
    })
   }
  }
 }
}
`

const structuredParamBenchmarks = `
func BenchmarkStructuredTransport(b *testing.B) {
    for _,count:=range []int{-1,0,1,128} {
        for _,binary:=range []bool{false,true} {
            benchmarkStructuredTransport(b,count,binary)
        }
    }
}

func benchmarkStructuredTransport(b *testing.B, count int, binary bool) {
    router:=api.NewRouterWithOptions(api.RouterOptions{})
    list:=count>=0
    router.Schema=structuredSchema(list,false,false)
    router.Registry.RegisterSchemaAPIs(router.Schema)
    read:=readRequiredListfalse
    if list {read=readRequiredListtrue}
    if os.Getenv("LUXO_STRUCTURED_PARAM_BEFORE")=="1" {
        read=readBeforeListfalse
        if list {read=readBeforeListtrue}
    }
    var got []Input
    router.Handle("read",func(_ context.Context,req *api.Request)error {return read(req,&got)})
    raw:=object
    if count==0 {raw="[]"} else if count>0 {raw="["+strings.Repeat(object+",",count-1)+object+"]"}
    body:=requestBody(b,router,raw,list,binary)
    // The wire builder above needs the actual list length in this benchmark.
    if binary && count>1 {
        message,err:=router.Schema.JSONToBinary("Input",[]byte(object)); if err!=nil {b.Fatal(err)}
        messages:=make(api.BinaryMessages,count); for i:=range messages {messages[i]=message}
        body,err=api.EncodeBinaryRequest(42,nil,map[string]any{"value":messages,"count":int64(42)},router.Registry.ParamOrder("read"))
        if err!=nil {b.Fatal(err)}
    }
    mode:="JSON"; if binary {mode="Binary"}
    label:=mode+"/scalar"
    if list {label=fmt.Sprintf("%s/list%d",mode,count)}
    b.Run(label,func(b *testing.B) {
        b.ReportAllocs()
        for b.Loop() {
            req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
            if binary {req.Header.Set("X-Luxo-Mode","binary")}
            response:=httptest.NewRecorder(); router.ServeHTTP(response,req)
            expected:=count; if expected<0 {expected=1}
            if response.Code!=200 || len(got)!=expected {b.Fatalf("HTTP %d: %d values",response.Code,len(got))}
        }
    })
}

`
