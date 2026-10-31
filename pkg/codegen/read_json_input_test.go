package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedJSONInputReader(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "child", Type: &ast.TypeRef{Name: "JSONChild"}},
		{Name: "optionalChild", Type: &ast.TypeRef{Name: "JSONChild", Nullable: true}},
		{Name: "children", Type: &ast.TypeRef{Name: "JSONChild", IsList: true}},
		{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
	}
	for _, kind := range []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "State"} {
		for _, shape := range []string{"value", "list", "optional", "optionalList"} {
			fields = append(fields, &ast.FieldDecl{Name: shape + kind, Type: &ast.TypeRef{
				Name: kind, IsList: shape == "list" || shape == "optionalList", Nullable: strings.HasPrefix(shape, "optional"),
			}})
		}
	}
	child := &ast.ModelDecl{Name: "JSONChild", Fields: []*ast.FieldDecl{testField("id", "Int")}}
	input := &ast.ModelDecl{Name: "JSONInput", Fields: fields}
	empty := &ast.ModelDecl{Name: "JSONEmpty"}
	ids := map[string]map[string]int{"JSONChild": {"id": 1}, "JSONInput": {}, "JSONEmpty": {}}
	for i, field := range fields {
		ids["JSONInput"][field.Name] = i + 1
	}
	g := generatorWithModelFieldIDs(ids)
	var source strings.Builder
	source.WriteString(`package generated
import ("bytes";"encoding/json";"fmt";"time";"github.com/google/uuid";"github.com/shopspring/decimal";
"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/jsonutil";"github.com/light-speak/luxo/pkg/lux/schema")
`)
	enum := &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}}
	generateEnum(&source, enum)
	models := []*ast.ModelDecl{child, input, empty}
	for _, model := range models {
		generateModel(&source, model, map[string]bool{"State": true})
		g.generateReadLuxo(&source, model, map[string]bool{"State": true})
		g.generateReadColumnar(&source, model, map[string]bool{"State": true})
		g.generateReadJSONInput(&source, model, map[string]bool{"State": true})
	}
	schemaSource := string(g.generateSchemaFile(result(&ast.File{Models: models, Enums: []*ast.EnumDecl{enum}}), "generated", map[string]bool{"State": true}))
	source.WriteString(schemaSource[strings.Index(schemaSource, "func RegisterSchema"):])
	for _, banned := range []string{"json.Unmarshal", "json.Marshal", "JSONToBinary", "reflect.", "map[string]"} {
		if strings.Contains(source.String(), banned) {
			t.Fatalf("direct reader uses %q", banned)
		}
	}
	runGeneratedProtocolPackage(t, source.String(), jsonInputReaderAssertions+jsonInputReaderBenchmarks)
}

const jsonInputReaderAssertions = `package generated
import ("bytes";"reflect";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")

func compareJSONInput(t *testing.T, raw []byte) {
    t.Helper()
    registry:=schema.New();RegisterSchema(registry)
    wire,wireErr:=registry.JSONToBinary("JSONInput",raw)
    var want,got JSONInput
    if wireErr==nil {
        dec:=codec.NewDecoder(wire);want.ReadLuxoInput(dec);wireErr=dec.Finish()
    }
    err:=got.ReadJSONInput(raw)
    if (err==nil)!=(wireErr==nil) {t.Fatalf("contract differs for %s: direct=%v previous=%v",raw,err,wireErr)}
    if err!=nil {return}
    clear(raw)
    if !reflect.DeepEqual(got,want) {t.Fatalf("value/ownership differs: got=%#v want=%#v",got,want)}
}

func TestJSONInputAllTypes(t *testing.T) {
    samples:=[]struct{kind,valid,invalid string}{
        {"Int","9223372036854775807","9223372036854775808"},
        {"Float","-0.0","1e400"},{"String","\"東京\\ntext\"","42"},
        {"Boolean","true","\"true\""},{"DateTime","\"1969-12-31T23:59:59.123456789Z\"","0"},
        {"Duration","-9223372036854775808","0.5"},{"UUID","\"550e8400-e29b-41d4-a716-446655440000\"","\"wrong\""},
        {"Decimal","\"12345678901234567890.123456789\"","\"wrong\""},{"Bytes","\"AAH/\"","[0,1,255]"},
        {"JSON","{\"n\":123456789012345678901234567890}","{\"n\":}"},{"State","\"OPEN\"","\"FUTURE\""},
    }
    for _,sample:=range samples {
        for _,shape:=range []string{"value","list","optional","optionalList"} {
            field:=shape+sample.kind
            for _,value:=range []string{sample.valid,sample.invalid,"null"} {
                if shape=="list" || shape=="optionalList" {value="["+value+"]"}
                compareJSONInput(t,[]byte("{\""+field+"\":"+value+"}"))
            }
            compareJSONInput(t,[]byte("{\""+field+"\":null}"))
            if shape=="list" || shape=="optionalList" {compareJSONInput(t,[]byte("{\""+field+"\":[]}"))}
        }
    }
}

func TestJSONInputNestedAndDuplicate(t *testing.T) {
    for _,raw:=range []string{
        "{}", "null", "[]", "{}{}", "{\"hidden\":\"not accepted\"}", "{\"unknown\":1}",
        "{\"child\":{\"id\":42},\"children\":[{\"id\":1},{}]}",
        "{\"optionalChild\":null}","{\"child\":null}","{\"children\":[null]}","{\"child\":{\"bad\":1}}",
        "{\"valueInt\":\"bad\",\"value\\u0049nt\":42}",
        "{\"valueInt\":42,\"valueInt\":\"bad\"}",
        "{\"valueString\":\"\\uD800\",\"valueString\":\"good\"}",
        "{\"child\":{\"id\":\"bad\"},\"child\":{\"id\":42}}",
        "{\"optionalChild\":{\"id\":42},\"optionalChild\":null}",
    } {compareJSONInput(t,[]byte(raw))}
    var empty JSONEmpty
    if err:=empty.ReadJSONInput([]byte("{}"));err!=nil {t.Fatal(err)}
    if err:=empty.ReadJSONInput([]byte("{\"unknown\":1}"));err==nil {t.Fatal("empty schema accepts unknown fields")}
}

func TestJSONInputFailureDoesNotPublishPartialValue(t *testing.T) {
    got:=JSONInput{ValueInt:84,ValueString:"unchanged"}
    want:=got
    if err:=got.ReadJSONInput([]byte("{\"valueInt\":42,\"valueString\":1}"));err==nil || !reflect.DeepEqual(got,want) {t.Fatalf("partial result published: %#v %v",got,err)}
    if err:=got.ReadJSONInput([]byte("{}"));err!=nil || !reflect.DeepEqual(got,JSONInput{}) {t.Fatalf("fresh input retained previous fields: %#v %v",got,err)}
    raw:=[]byte("{\"valueBytes\":\"AAH/\",\"valueJSON\":null,\"optionalString\":\"hello\"}")
    if err:=got.ReadJSONInput(raw);err!=nil {t.Fatal(err)}
    clear(raw)
    if !bytes.Equal(got.ValueBytes,[]byte{0,1,255}) || string(got.ValueJSON)!="null" || got.OptionalString==nil || *got.OptionalString!="hello" {t.Fatal("input fields borrow caller memory")}
    for _,raw:=range []string{"{\"listInt\":["+strings.Repeat("0,",codec.MaxArrayElements)+"0]}","{\"children\":["+strings.Repeat("{},",codec.MaxArrayElements)+"{}]}"} {
        if err:=got.ReadJSONInput([]byte(raw));err==nil {t.Fatal("oversized list accepted")}
    }
}
`

// Compare identical typed results, including the previous intermediate encoding.
// These are warm readers, not complete HTTP or gateway requests.
const jsonInputReaderBenchmarks = `
func BenchmarkJSONInput(b *testing.B) {
    registry:=schema.New();RegisterSchema(registry)
    for _,sample:=range []struct{name,raw string}{
        {"small", "{\"valueInt\":42,\"valueString\":\"hello\"}"},
        {"precision", "{\"valueInt\":9223372036854775807,\"valueDateTime\":\"1969-12-31T23:59:59.123456789Z\",\"valueJSON\":{\"n\":123456789012345678901234567890}}"},
        {"long-text", "{\"valueString\":\""+strings.Repeat("東京",12288)+"\"}"},
        {"text-list", "{\"listString\":["+strings.Repeat("\"hello\",",127)+"\"hello\"]}"},
        {"nested-list", "{\"children\":["+strings.Repeat("{\"id\":42},",127)+"{\"id\":42}]}"},
    } {
        raw:=[]byte(sample.raw)
        for _,direct:=range []bool{false,true} {
            name:="previous";if direct {name="direct"}
            b.Run(sample.name+"/"+name,func(b *testing.B){
                b.ReportAllocs()
                for b.Loop() {
                    var value JSONInput
                    if direct {
                        if err:=value.ReadJSONInput(raw);err!=nil {b.Fatal(err)}
                    } else {
                        data,err:=registry.JSONToBinary("JSONInput",raw);if err!=nil {b.Fatal(err)}
                        dec:=codec.NewDecoder(data);value.ReadLuxoInput(dec)
                        if err:=dec.Finish();err!=nil {b.Fatal(err)}
                    }
                }
            })
        }
    }
}
`

func TestJSONInputFieldEligibility(t *testing.T) {
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Empty": {"hidden": 1, "internal": 2}})
	model := &ast.ModelDecl{Name: "Empty", Fields: []*ast.FieldDecl{
		{Name: "untyped"}, testField("unregistered", "String"),
		{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
		{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
	}}
	var source strings.Builder
	g.generateReadJSONInput(&source, model, nil)
	for _, field := range model.Fields {
		if strings.Contains(source.String(), fmt.Sprintf("case %q:", field.Name)) {
			t.Errorf("non-schema field is accepted: %s", field.Name)
		}
	}
}

func TestGeneratedJSONInputFile(t *testing.T) {
	for _, kind := range []string{"Int", "JSON", "String"} {
		t.Run(kind, func(t *testing.T) {
			model := &ast.ModelDecl{Name: "Record", Fields: []*ast.FieldDecl{testField("value", kind)}}
			decl := &ast.TypeDecl{Name: "Input", Fields: []*ast.FieldDecl{testField("value", kind)}}
			ext := &ast.ExtendDecl{Name: "External", Fields: []*ast.FieldDecl{testField("value", kind)}}
			g := generatorWithModelFieldIDs(map[string]map[string]int{
				"Record": {"value": 1}, "Input": {"value": 1}, "External": {"id": 1, "value": 2},
			})
			var source strings.Builder
			source.Write(g.generateWriteJSONFile(result(&ast.File{
				Models: []*ast.ModelDecl{model}, Types: []*ast.TypeDecl{decl}, Extends: []*ast.ExtendDecl{ext},
			}), "generated", nil))
			generateModel(&source, model, nil)
			generateTypeStruct(&source, decl, nil)
			g.generateExtendStub(&source, ext)
			value := map[string]string{"Int": "42", "String": `"hello"`, "JSON": `{"n":123456789012345678901234567890}`}[kind]
			assertions := fmt.Sprintf(`package generated
import "testing"
func TestReadJSONInput(t *testing.T) {
    raw:=[]byte(%q)
    var model Record; if err:=model.ReadJSONInput(raw);err!=nil {t.Fatal(err)}
    var input Input; if err:=input.ReadJSONInput(raw);err!=nil {t.Fatal(err)}
    var external External; if err:=external.ReadJSONInput(raw);err!=nil {t.Fatal(err)}
}

`, `{"value":`+value+`}`)
			runGeneratedProtocolPackage(t, source.String(), assertions)
		})
	}
}

func TestGeneratedLocalJSONRegistration(t *testing.T) {
	file := &ast.File{Models: []*ast.ModelDecl{testModel("User", nil, []*ast.FieldDecl{testField("id", "Int")})}, APIs: []*ast.ApiDecl{
		{Name: "compiled", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}},
		{Name: "nativeCall", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}},
		{Name: "countUsersById", ReturnType: &ast.TypeRef{Name: "Int"}},
		{Name: "deleteUsersById", ReturnType: &ast.TypeRef{Name: "Int"}},
	}}
	code := string(generateHandlerFile(result(file), "generated", nil))
	for _, name := range []string{"compiled", "nativeCall", "countUsersById", "deleteUsersById"} {
		if !strings.Contains(code, `router.HandleLocal("`+name+`",`) {
			t.Fatalf("typed API %s still prepares canonical JSON input", name)
		}
	}
}

func TestGeneratedInferredLocalJSONInput(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import("context";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/errors")
type App struct{User *probeClient}
type probeClient struct{calls int}
func(c *probeClient)Where(...lux.Condition)*probeClient{c.calls++;return c}
func(c *probeClient)Count(context.Context)(int64,error){return 1,nil}
func(c *probeClient)Delete(context.Context)(int64,error){return 1,nil}
`)
	enums := map[string]bool{"State": true}
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	model := testModel("User", nil, []*ast.FieldDecl{testField("id", "Int"), testField("status", "State")})
	for _, action := range []string{"count", "delete"} {
		name := action + "UsersByIdAndStatus"
		declaration := &ast.ApiDecl{Name: name, ReturnType: &ast.TypeRef{Name: "Int"}}
		inferred := InferAPI(name, map[string]*ast.ModelDecl{"User": model})
		if inferred == nil {
			t.Fatalf("cannot infer %s", name)
		}
		generateInferredHandler(&source, declaration, inferred, enums, model)
	}
	runGeneratedProtocolPackage(t, source.String(), inferredLocalJSONAssertions)
}

const inferredLocalJSONAssertions = `package generated
import("bytes";"fmt";"net/http/httptest";"testing";"context";"os";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestInferredJSONAndBinaryRejectBeforeQuery(t *testing.T){
    for _,name:=range []string{"countUsersByIdAndStatus","deleteUsersByIdAndStatus"}{
        client:=&probeClient{};app:=&App{User:client};router:=api.NewRouter()
        router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
        router.Schema.RegisterAPI(&schema.API{Name:name,ID:1,ReturnType:"Int",Params:[]schema.Param{
            {Name:"id",ID:1,Type:schema.FieldInt},{Name:"status",ID:2,Type:schema.FieldString,TypeName:"State"},
        }})
        router.Registry.RegisterSchemaAPIs(router.Schema)
        handler:=handleCountUsersByIdAndStatus(app)
        if name=="deleteUsersByIdAndStatus" {handler=handleDeleteUsersByIdAndStatus(app)}
        router.HandleLocal(name,func(ctx context.Context,req *api.Request)error{
            if req.Params!=nil && len(req.BinaryParams())!=0 {t.Fatal("local JSON encoded intermediate Binary")}
            return handler(ctx,req)
        })
        if err:=router.Prepare();err!=nil {t.Fatal(err)}
        for _,sample:=range []struct{params string;status int}{
            {"\"id\":42,\"status\":\"OPEN\"",200},
            {"\"id\":42,\"status\":\"FUTURE\"",400},
            {"\"id\":42,\"status\":null",400},
            {"\"id\":42,\"status\":1",400},
            {"\"id\":42",400},{"\"status\":\"OPEN\"",400},
            {"\"id\":null,\"status\":\"OPEN\"",400},
            {"\"id\":9223372036854775808,\"status\":\"OPEN\"",400},
            {"\"id\":42,\"status\":\"OPEN\",\"extra\":0",400},
            {"\"id\":42,\"status\":\"bad\",\"status\":\"OPEN\"",200},
            {"\"id\":42,\"status\":\"\\uD800\",\"status\":\"OPEN\"",400},
        }{
            request:=httptest.NewRequest("POST","/",bytes.NewBufferString(fmt.Sprintf("{\"$api\":%q,%s}",name,sample.params)))
            request.Header.Set("Content-Type","application/json")
            before:=client.calls;response:=httptest.NewRecorder();router.ServeHTTP(response,request)
            if response.Code!=sample.status {t.Fatalf("%s: %s got %d: %s",name,sample.params,response.Code,response.Body.String())}
            if (client.calls>before)!=(sample.status==200) {t.Fatal("invalid JSON entered database query")}
        }
        for _,state:=range []string{"OPEN","FUTURE"}{
            body:=codec.AppendSvarint([]byte{1,0,1},42)
            body=append(body,2);var err error;body,err=codec.AppendString(body,state);if err!=nil{t.Fatal(err)};body=append(body,0)
            request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("X-Luxo-Mode","binary")
            before:=client.calls;response:=httptest.NewRecorder();router.ServeHTTP(response,request)
            want:=200;if state=="FUTURE"{want=400}
            if response.Code!=want || (client.calls>before)!=(want==200){t.Fatalf("Binary state=%s status=%d calls=%d: %s",state,response.Code,client.calls-before,response.Body.String())}
        }
    }
}

func BenchmarkLocalJSONTransportInferred(b *testing.B){
    policies:=[]bool{false,true}
    if os.Getenv("LUXO_JSON_LOCAL_BENCHMARK_REVERSE")=="1"{policies[0],policies[1]=true,false}
    for _,sample:=range []struct{name string;id int64;state string}{
        {"small",42,"OPEN"},{"precision",9223372036854775807,"CLOSED"},
    }{
        for _,binary:=range []bool{false,true}{
            for _,local:=range policies{
                mode,policy:="json","canonical";if binary{mode="binary"};if local{policy="local"}
                b.Run(sample.name+"/"+mode+"/"+policy,func(b *testing.B){
                    router:=api.NewRouter();client:=&probeClient{}
                    router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
                    router.Schema.RegisterAPI(&schema.API{Name:"countUsersByIdAndStatus",ID:1,ReturnType:"Int",Params:[]schema.Param{
                        {Name:"id",ID:1,Type:schema.FieldInt},{Name:"status",ID:2,Type:schema.FieldString,TypeName:"State"},
                    }})
                    router.Registry.RegisterSchemaAPIs(router.Schema)
                    handler:=handleCountUsersByIdAndStatus(&App{User:client})
                    if local{router.HandleLocal("countUsersByIdAndStatus",handler)}else{router.Handle("countUsersByIdAndStatus",handler)}
                    if err:=router.Prepare();err!=nil{b.Fatal(err)}
                    body:=fmt.Appendf(nil,"{\"$api\":\"countUsersByIdAndStatus\",\"id\":%d,\"status\":%q}",sample.id,sample.state)
                    if binary{
                        body=codec.AppendSvarint([]byte{1,0,1},sample.id);body=append(body,2)
                        var err error;body,err=codec.AppendString(body,sample.state);if err!=nil{b.Fatal(err)};body=append(body,0)
                    }
                    b.ReportAllocs();b.ResetTimer()
                    for b.Loop(){
                        request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
                        request.Header.Set("Content-Type","application/json")
                        if binary{request.Header.Set("X-Luxo-Mode","binary")}
                        response:=httptest.NewRecorder();router.ServeHTTP(response,request)
                        if response.Code!=200{b.Fatalf("%d: %s",response.Code,response.Body.String())}
                    }
                    if client.calls==0{b.Fatal("query not executed")}
                })
            }
        }
    }
}
`
