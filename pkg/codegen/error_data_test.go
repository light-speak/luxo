package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedErrorDataUsesRemotePrimaryKey(t *testing.T) {
	for _, keyType := range []string{"String", "UUID"} {
		t.Run(keyType, func(t *testing.T) {
			generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Remote": {"state": 1, "key": 2}}}, Events: &EventContext{
				ModelIDType: map[string]string{"Remote": keyType}, ModelIDField: map[string]string{"Remote": "key"},
				EnumModule: map[string]string{"RemoteState": "remote"},
			}})
			file := &ast.File{Extends: []*ast.ExtendDecl{{Name: "Remote", Fields: []*ast.FieldDecl{
				{Name: "state", Type: &ast.TypeRef{Name: "RemoteState"}},
			}}}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{
				{Name: "remote", Type: &ast.TypeRef{Name: "Remote"}},
			}}}}
			result, err := generator.Generate(&semantic.Result{Files: []*ast.File{file}}, "generated", nil)
			if err != nil {
				t.Fatal(err)
			}
			var model strings.Builder
			generator.generateExtendStub(&model, file.Extends[0], map[string]bool{"RemoteState": true})
			source := appendErrorTestEnum(string(result.Files["error.gen.go"])+model.String(), &ast.EnumDecl{Name: "RemoteState", Values: []string{"OPEN", "CLOSED"}})
			key, declaration := `"abc"`, ""
			if keyType == "UUID" {
				key = "uuid.Nil"
				declaration = `import "github.com/google/uuid"`
				// The generated model lives in a separate file in a real module.
				source = strings.Replace(source, "import (", "import (\n\"github.com/google/uuid\"", 1)
			}
			assertions := fmt.Sprintf(`package generated
import ("strings"; "testing")
%s
func TestRemoteKey(t *testing.T) {
 data := NewRejected(Remote{Key: %s, State: RemoteState("OPEN")}).Data
 raw, err := data.AppendJSON(nil)
 if err != nil || !strings.Contains(string(raw), "\"key\":") || strings.Contains(string(raw), "\"id\":") || !strings.Contains(string(raw), "\"state\":\"OPEN\"") { t.Fatalf("%%s: %%v", raw, err) }
 data.(*RejectedErrorData).field0.State = "FUTURE"
 if raw, err := data.AppendJSON([]byte("prefix:")); err == nil || string(raw) != "prefix:" { t.Fatalf("unknown remote enum: %%s %%v",raw,err) }
 if raw, ok := data.AppendText([]byte("prefix:"),"remote"); ok || string(raw) != "prefix:" { t.Fatalf("unknown remote enum text: %%s %%v",raw,ok) }
}`, declaration, key)
			runGeneratedProtocolPackage(t, source, assertions)
		})
	}
}

func TestGeneratedStaticErrorData(t *testing.T) {
	fields := []*ast.ParamDecl{}
	for _, name := range []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "Details", "Status"} {
		fields = append(fields, &ast.ParamDecl{Name: "value" + name, Type: &ast.TypeRef{Name: name}})
	}
	fields = append(fields,
		&ast.ParamDecl{Name: "optional", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
		&ast.ParamDecl{Name: "values", Type: &ast.TypeRef{Name: "Details", IsList: true, Nullable: true}},
	)
	details := &ast.TypeDecl{Name: "Details", Fields: []*ast.FieldDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "next", Type: &ast.TypeRef{Name: "Details", Nullable: true}},
		{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
	}}
	status := &ast.EnumDecl{Name: "Status", Values: []string{"OPEN", "CLOSED"}}
	file := &ast.File{Types: []*ast.TypeDecl{details}, Enums: []*ast.EnumDecl{status}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Message: "error.rejected", Fields: fields}}}
	var model strings.Builder
	generateTypeStruct(&model, details, map[string]bool{"Status": true})
	source := appendErrorTestEnum(string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))+model.String(), status)
	runGeneratedProtocolPackage(t, source, staticErrorDataAssertions)
}

func appendErrorTestEnum(source string, declaration *ast.EnumDecl) string {
	var enum strings.Builder
	generateEnum(&enum, declaration)
	return strings.Replace(source, "import (", "import (\n\"github.com/light-speak/luxo/pkg/lux/codec\"", 1) + enum.String()
}

func TestErrorDataUnsupportedTypeRejected(t *testing.T) {
	var output strings.Builder
	generator := newErrorDataGenerator(&semantic.Result{}, defaultGenerator())
	generator.writeScalar(&output, &ast.TypeRef{Name: "Unsupported"}, "value", false, "\t")
	if got := output.String(); got != "\tw.Reject(\"unsupported error data type: Unsupported\")\n" {
		t.Fatalf("unsupported type was not rejected: %s", got)
	}
}

func TestGeneratedErrorDataEnumBoundary(t *testing.T) {
	status := &ast.EnumDecl{Name: "Status", Values: []string{"OPEN", "CLOSED"}}
	details := &ast.TypeDecl{Name: "Details", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "Status"}},
	}}
	fields := []*ast.ParamDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "Status"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Status", Nullable: true}},
		{Name: "states", Type: &ast.TypeRef{Name: "Status", IsList: true}},
		{Name: "optionalStates", Type: &ast.TypeRef{Name: "Status", IsList: true, Nullable: true}},
		{Name: "details", Type: &ast.TypeRef{Name: "Details"}},
		{Name: "items", Type: &ast.TypeRef{Name: "Details", IsList: true}},
	}
	file := &ast.File{Enums: []*ast.EnumDecl{status}, Types: []*ast.TypeDecl{details}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Fields: fields}}}
	var model strings.Builder
	generateTypeStruct(&model, details, map[string]bool{"Status": true})
	source := appendErrorTestEnum(string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))+model.String(), status)
	runGeneratedProtocolPackage(t, source, errorDataEnumAssertions)
}

const errorDataEnumAssertions = `package generated
import ("bytes"; "context"; "encoding/json"; "net/http"; "net/http/httptest"; "strings"; "testing"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/errors")

func enumErrorFixture() *RejectedErrorData {
 open := StatusOPEN
 return NewRejected(open, &open, []Status{open, StatusCLOSED}, []Status{}, Details{State:open}, []Details{{State:open}}).Data.(*RejectedErrorData)
}
func TestUnknownErrorEnumRejected(t *testing.T) {
 fields := []string{"state", "optional", "states", "optionalStates", "details", "items"}
 for _, value := range []Status{"FUTURE", "", "open", Status(string([]byte{255}))} {
  for index, field := range fields {
   data := enumErrorFixture()
   switch index {
   case 0: data.field0 = value
   case 1: data.field1 = &value
   case 2: data.field2 = append(data.field2, value)
   case 3: data.field3 = []Status{StatusOPEN, value}
   case 4: data.field4.State = value
   case 5: data.field5 = append(data.field5, Details{State:value})
   }
   prefix := []byte("prefix:")
   if raw, err := data.AppendJSON(prefix); err == nil || !strings.Contains(err.Error(), "Status") || string(raw) != "prefix:" {
    t.Errorf("field=%s value=%q JSON=%q err=%v", field, value, raw, err)
   }
   if raw, found := data.AppendText(prefix, field); found || string(raw) != "prefix:" {
    t.Errorf("field=%s value=%q text=%q found=%v", field, value, raw, found)
   }
  }
 }
}
func TestDeclaredErrorEnumsAndNull(t *testing.T) {
 data := enumErrorFixture()
 want := "{\"state\":\"OPEN\",\"optional\":\"OPEN\",\"states\":[\"OPEN\",\"CLOSED\"],\"optionalStates\":[],\"details\":{\"state\":\"OPEN\"},\"items\":[{\"state\":\"OPEN\"}]}"
 if raw, err := data.AppendJSON(nil); err != nil || string(raw) != want { t.Fatalf("%s: %v",raw,err) }
 if raw, found := data.AppendText(nil, "state"); !found || string(raw) != "OPEN" { t.Fatalf("%q %v",raw,found) }
 data.field1, data.field3 = nil, nil
 if raw, err := data.AppendJSON(nil); err != nil || !strings.Contains(string(raw), "\"optional\":null") || !strings.Contains(string(raw), "\"optionalStates\":null") { t.Fatalf("%s: %v",raw,err) }
}
func TestInvalidErrorEnumOverHTTP(t *testing.T) {
 for _, binary := range []bool{false,true} {
  for _, valid := range []bool{false,true} {
   router := api.NewRouter()
   router.Registry.Register("fail",1)
   router.Handle("fail",func(context.Context,*api.Request)error {
    data := enumErrorFixture()
    if !valid { data.field0 = "FUTURE" }
    return errors.New("Rejected",422,"error.rejected").WithData(data)
   })
   body := []byte("{\"$api\":\"fail\"}")
   if binary { body = []byte{1,0,0} }
   req := httptest.NewRequest(http.MethodPost,"/luxo",bytes.NewReader(body))
   if binary { req.Header.Set("X-Luxo-Mode","binary") }
   res := httptest.NewRecorder()
   router.ServeHTTP(res,req)
   wantCode,wantName := 500,"Internal"
   if valid { wantCode,wantName = 422,"Rejected" }
   if res.Code != wantCode { t.Fatalf("binary=%v valid=%v status=%d body=%s",binary,valid,res.Code,res.Body.Bytes()) }
   var name string
   var data json.RawMessage
   if binary {
    envelope,err := api.DecodeBinaryError(res.Body.Bytes(),res.Code)
    if err != nil { t.Fatal(err) }
    name,data = envelope.Name,envelope.Data
   } else {
    var envelope struct { Error string; Data json.RawMessage }
    if err:=json.Unmarshal(res.Body.Bytes(),&envelope); err!=nil { t.Fatal(err) }
    name,data = envelope.Error,envelope.Data
   }
   if name != wantName || (len(data)>0) != valid { t.Fatalf("unsafe error: name=%s data=%s",name,data) }
  }
 }
}
`

func TestGeneratedErrorDataSoftModelFields(t *testing.T) {
	model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "soft"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
	}}
	for _, explicit := range []bool{false, true} {
		if explicit {
			model.Fields = append(model.Fields, softDeleteField())
		}
		file := &ast.File{Models: []*ast.ModelDecl{model}, Extends: []*ast.ExtendDecl{{Name: "Record"}}, Errors: []*ast.ErrorDecl{{
			Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{{Name: "record", Type: &ast.TypeRef{Name: "Record"}}},
		}}}
		var declaration strings.Builder
		generateModel(&declaration, model, nil)
		source := string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated")) + declaration.String()
		// The generated model's DateTime import normally belongs to model.gen.go.
		source = strings.Replace(source, "import (", "import (\n\"time\"", 1)
		runGeneratedProtocolPackage(t, source, `package generated
import ("testing"; "time")
func TestModelData(t *testing.T) {
 value := time.Date(2026,9,15,0,0,0,0,time.UTC)
 for _, deleted := range []*time.Time{nil, &value} {
  data := NewRejected(Record{Id:42,Secret:"private",DeletedAt:deleted}).Data
  got, err := data.AppendJSON(nil)
  want := "{\"record\":{\"id\":42,\"deletedAt\":null}}"
  if deleted != nil { want = "{\"record\":{\"id\":42,\"deletedAt\":\"2026-09-15T00:00:00Z\"}}" }
  if err != nil || string(got) != want { t.Fatalf("%s: %v", got, err) }
 }
}`)
	}
}

const staticErrorDataAssertions = `package generated
import (
 "encoding/json"
 "math"
 "testing"
 "time"
 "github.com/google/uuid"
 "github.com/shopspring/decimal"
)

func errorFixture() *RejectedErrorData {
 return NewRejected(math.MaxInt64, math.Copysign(0,-1), "café\n", true,
  time.Date(2026,9,15,1,2,3,0,time.UTC), time.Duration(math.MinInt64), uuid.Nil,
  decimal.RequireFromString("12345678901234567890.123456789"), []byte{0,255},
  json.RawMessage("{\"n\":123456789012345678901234567890,\"f\":1e-400}"),
  Details{Value:json.RawMessage("-0.000"),Hidden:"secret"}, Status("OPEN"), nil, []Details{}).Data.(*RejectedErrorData)
}
func TestTypedErrorValues(t *testing.T) {
 data := errorFixture()
 raw, err := data.AppendJSON(nil)
 if err != nil { t.Fatal(err) }
 want := "{\"valueInt\":9223372036854775807,\"valueFloat\":-0,\"valueString\":\"café\\n\",\"valueBoolean\":true,\"valueDateTime\":\"2026-09-15T01:02:03Z\",\"valueDuration\":-9223372036854775808,\"valueUUID\":\"00000000-0000-0000-0000-000000000000\",\"valueDecimal\":\"12345678901234567890.123456789\",\"valueBytes\":\"AP8=\",\"valueJSON\":{\"n\":123456789012345678901234567890,\"f\":1e-400},\"valueDetails\":{\"value\":-0.000,\"next\":null},\"valueStatus\":\"OPEN\",\"optional\":null,\"values\":[]}"
 if string(raw) != want { t.Fatalf("%s\nwant %s",raw,want) }
 for _, test := range []struct{key,want string}{
  {"valueInt","9223372036854775807"},{"valueString","café\n"},{"valueStatus","OPEN"},
  {"valueDuration","-9223372036854775808"},{"optional","null"},{"values","[]"},
 } {
  got,found := data.AppendText([]byte("prefix:"),test.key)
  if !found || string(got) != "prefix:"+test.want { t.Fatalf("text %s = %s",test.key,got) }
 }
 if got,found := data.AppendText([]byte("prefix:"),"missing"); found || string(got) != "prefix:" { t.Fatal("unknown field") }
}
func TestErrorEncodingFailsClosed(t *testing.T) {
 data := errorFixture()
 data.field1 = math.NaN()
 if raw,err := data.AppendJSON([]byte("prefix:")); err == nil || string(raw) != "prefix:" { t.Fatal("non-finite output") }
 if raw,ok := data.AppendText([]byte("prefix:"),"valueFloat"); ok || string(raw) != "prefix:" { t.Fatal("non-finite text") }
 data = errorFixture()
 data.field10.Next = &data.field10
 if _,err := data.AppendJSON(nil); err == nil { t.Fatal("cyclic data accepted") }
}
func TestGenericJSONIsNotAnEnum(t *testing.T) {
 data := errorFixture()
 data.field9 = json.RawMessage("{\"state\":\"FUTURE\"}")
 raw, err := data.AppendJSON(nil)
 if err != nil { t.Fatal(err) }
 var decoded struct { ValueJSON json.RawMessage }
 if err := json.Unmarshal(raw,&decoded); err != nil || string(decoded.ValueJSON) != string(data.field9) { t.Fatalf("generic JSON changed: %s %v",raw,err) }
}
func TestNilErrorData(t *testing.T) {
 var data *RejectedErrorData
 raw, err := data.AppendJSON([]byte("prefix:"))
 if err != nil || string(raw) != "prefix:null" { t.Fatalf("%s: %v",raw,err) }
 if raw, found := data.AppendText([]byte("prefix:"),"valueInt"); found || string(raw) != "prefix:" { t.Fatal("nil data text") }
}
`
