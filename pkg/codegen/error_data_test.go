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
			generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
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
			source := string(result.Files["error.gen.go"]) + "\ntype RemoteState string\n" + model.String()
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
	file := &ast.File{Types: []*ast.TypeDecl{details}, Enums: []*ast.EnumDecl{{Name: "Status"}}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Message: "error.rejected", Fields: fields}}}
	var model strings.Builder
	generateTypeStruct(&model, details, map[string]bool{"Status": true})
	source := string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated")) + "\ntype Status string\n" + model.String()
	runGeneratedProtocolPackage(t, source, staticErrorDataAssertions)
}

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
func TestNilErrorData(t *testing.T) {
 var data *RejectedErrorData
 raw, err := data.AppendJSON([]byte("prefix:"))
 if err != nil || string(raw) != "prefix:null" { t.Fatalf("%s: %v",raw,err) }
 if raw, found := data.AppendText([]byte("prefix:"),"valueInt"); found || string(raw) != "prefix:" { t.Fatal("nil data text") }
}
`
