package codegen

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func canonicalSealedInput(t *testing.T) (*semantic.Result, StableIDs, *schema.Schema) {
	t.Helper()
	raw, err := os.ReadFile("../lux/codec/testdata/sealed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		DSL    string         `json:"dsl"`
		Schema *schema.Schema `json:"schema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	tokens, lexical := lexer.New(fixture.DSL, "sealed.luxo").Tokenize()
	file, parse := parser.New(tokens).Parse("sealed.luxo")
	if len(lexical) != 0 || len(parse) != 0 {
		t.Fatal(lexical, parse)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	lock := lockfile.New()
	if err := lock.Update(result.Files); err != nil {
		t.Fatal(err)
	}
	ids := StableIDs{ModelFields: make(map[string]map[string]int), APIs: make(map[string]int), APIParams: make(map[string]map[string]int)}
	for name, fields := range lock.Types {
		ids.ModelFields[name] = fields.Fields
	}
	for name, api := range lock.APIs {
		ids.APIs[name], ids.APIParams[name] = api.ID, api.Params
	}
	return result, ids, fixture.Schema
}

func TestBuildSealedSchemaFromRealLockMatchesCanonical(t *testing.T) {
	input, ids, expected := canonicalSealedInput(t)
	data, err := BuildSchemaJSON(input, nil, GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	var actual schema.Schema
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(expected.Sealeds)
	got, _ := json.Marshal(actual.Sealeds)
	if !bytes.Equal(want, got) {
		t.Fatalf("sealed metadata differs: actual=%s expected=%s", got, want)
	}
	if actual.APIs["echo"].Params[0].Type != schema.FieldSealed || actual.Types["Outcome"] != nil {
		t.Fatal("sum type fell back to a model")
	}
	if err := actual.Prepare(); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedSealedHTTPTransport(t *testing.T) {
	input, ids, _ := canonicalSealedInput(t)
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, generated, sealedHTTPAssertions)
}

const sealedHTTPAssertions = `package generated
import("bytes";"encoding/json";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSealedPublicHTTP(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{body,want string;status int}{
  {"{\"$api\":\"echo\",\"value\":{\"Success\":{\"value\":7}}}","{\"Success\":{\"value\":7}}",200},
  {"{\"$api\":\"echo\",\"$select\":\"Success\",\"value\":{\"Success\":{\"value\":7}}}","{\"Success\":{}}",200},
  {"{\"$api\":\"echo\",\"$select\":\"Success{value}\",\"value\":{\"Success\":{\"value\":7}}}","{\"Success\":{\"value\":7}}",200},
  {"{\"$api\":\"echo\",\"$select\":\"Pending\",\"value\":{\"Success\":{\"value\":7}}}","{\"Success\":{}}",200},
  {"{\"$api\":\"echoEmpty\",\"value\":null}","null",200},
  {"{\"$api\":\"echoEmptyList\",\"values\":[]}","[]",200},
  {"{\"$api\":\"echo\",\"value\":{\"Missing\":{}}}","",400},
  {"{\"$api\":\"echo\",\"$select\":\"Missing\",\"value\":{\"Pending\":{}}}","",400},
 }{
  request:=httptest.NewRequest("POST","/",bytes.NewBufferString(sample.body));request.Header.Set("Content-Type","application/json");response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=sample.status{t.Fatalf("%s status %d: %s",sample.body,response.Code,response.Body.String())};if sample.status!=200{continue}
  var payload struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&payload);err!=nil||string(payload.Data)!=sample.want{t.Fatal(response.Body.String(),err)}
 }
 childMask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{1},[]codec.SelectionMaskChild{{FieldID:1,Mask:childMask}});if err!=nil{t.Fatal(err)}
 body,err:=api.EncodeBinaryRequest(router.Schema.APIs["echo"].ID,mask,map[string]any{"value":api.BinaryMessage{1,4,0,1,14,0}},router.Registry.ParamOrder("echo"));if err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("X-Luxo-Mode","binary");response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 if response.Code!=200||!bytes.Equal(response.Body.Bytes(),[]byte{1,4,0,1,14,0}){t.Fatal(response.Code,response.Body.Bytes())}
}
`

func generateSealedVisibleFixture(t *testing.T, source string) (*GenerateResult, *semantic.Result) {
	t.Helper()
	input := analyzePermissionFixture(t, source)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	view, err := semantic.CompilationView(input)
	if err != nil {
		t.Fatal(err)
	}
	lock := lockfile.New()
	if err := lock.Update(view.Files); err != nil {
		t.Fatal(err)
	}
	ids := StableIDs{ModelFields: make(map[string]map[string]int), APIs: make(map[string]int), APIParams: make(map[string]map[string]int)}
	for name, fields := range lock.Models {
		ids.ModelFields[name] = fields.Fields
	}
	for name, fields := range lock.Types {
		ids.ModelFields[name] = fields.Fields
	}
	for name, api := range lock.APIs {
		ids.APIs[name], ids.APIParams[name] = api.ID, api.Params
	}
	generated, err := Generate(view, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return generated, view
}

func TestGeneratedSealedVisibleConditions(t *testing.T) {
	generated, _ := generateSealedVisibleFixture(t, sealedVisibleSource)
	runGeneratedCompilation(t, generated, sealedVisibleAssertions)
}

func TestGeneratedGenericSealedVisibleCondition(t *testing.T) {
	generated, view := generateSealedVisibleFixture(t, `sealed Choice { Value(value: Int) Empty }
type Box<T> { value: T? @visible(Choice.Value(1) == Choice.Value(1)) }
fn keep(value: Box<Int>): Int? { return value.value }`)
	name := view.Files[0].Functions[0].Params[0].Type.Name
	assertions := bytes.ReplaceAll([]byte(genericSealedVisibleAssertions), []byte("VISIBLE_GENERIC_TYPE"), []byte(name))
	runGeneratedCompilation(t, generated, string(assertions))
}

const sealedVisibleSource = `sealed Choice { Value(value: Int) Empty }
model User @noTime @withAuth(stores: [id, quota]) { id: Int @id quota: Int }
model VisibleRecord @noTime {
 id: Int @id
 shown: String? @visible(Choice.Value(1) == Choice.Value(1))
 hidden: String? @visible(Choice.Value(1) == Choice.Value(2))
 different: String? @visible(Choice.Value(1) != Choice.Empty())
 negated: String? @visible(!(my.quota == 1) && Choice.Value(1) == Choice.Value(1))
 skipped: String? @visible(true || Choice.Value(my.quota + 1) == Choice.Value(2))
 checked: String? @visible(Choice.Value(my.quota + 1) == Choice.Value(2))
}
model VisibleChild: VisibleRecord @noTime { note: String? }
type VisibleBox { value: Int? @visible(Choice.Value(1) == Choice.Value(1)) }
fn keep(value: VisibleBox): Int? { return value.value }`

const sealedVisibleAssertions = `package generated
import("bytes";"encoding/json";"errors";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/schema")
func TestSealedVisibilityWriters(t *testing.T){
 registry:=schema.New();if err:=RegisterSchema(registry);err!=nil{t.Fatal(err)}
 model:=registry.LookupModel("VisibleRecord");secret:="sealed-visible-marker"
 record:=&VisibleRecord{Id:42,Shown:&secret,Hidden:&secret,Different:&secret,Negated:&secret,Skipped:&secret,Checked:&secret}
 writers:=map[string]func(*api.ResponseBuf,[]byte)error{
  "row":func(buf *api.ResponseBuf,mask []byte)error{return record.WriteLuxo(buf,mask)},
  "column pointers":func(buf *api.ResponseBuf,mask []byte)error{return WriteColumnarVisibleRecord(buf,[]*VisibleRecord{record},mask)},
  "column values":func(buf *api.ResponseBuf,mask []byte)error{return WriteColumnarVisibleRecordValues(buf,[]VisibleRecord{*record},mask)},
 }
 selection:=func(field string)[]byte{t.Helper();var fields []byte;for _,item:=range model.Fields{if item.Name=="id"||item.Name==field{var err error;fields,err=codec.FieldMaskSet(fields,item.ID);if err!=nil{t.Fatal(err)}}};mask,err:=codec.AppendSelectionMask(nil,fields,nil);if err!=nil{t.Fatal(err)};return mask}
 identity:=func(subject,raw string)any{t.Helper();cfg:=&auth.Config{Secret:"sealed-visible",Expires:time.Hour};token,err:=auth.SignForSubject(cfg,subject,func(dst []byte)([]byte,error){return append(dst,raw...),nil});if err!=nil{t.Fatal(err)};claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)};return claims}
 valid:=identity("User","{\"id\":1,\"quota\":2}")
 for _,sample:=range []struct{name,field string;identity any;visible bool}{
  {"equal","shown",nil,true},{"unequal","hidden",nil,false},{"variant","different",nil,true},
  {"valid negation","negated",valid,true},{"false negation","negated",identity("User","{\"id\":1,\"quota\":1}"),false},
  {"nil negation","negated",nil,false},{"missing negation","negated",identity("User","{\"id\":1}"),false},
  {"wrong type negation","negated",identity("User","{\"id\":1,\"quota\":\"2\"}"),false},
  {"wrong subject negation","negated",identity("Other","{\"id\":1,\"quota\":2}"),false},
  {"short circuit","skipped",nil,true},{"checked payload","checked",identity("User","{\"id\":1,\"quota\":1}"),true},
 }{t.Run(sample.name,func(t *testing.T){for name,write:=range writers{t.Run(name,func(t *testing.T){
  buf:=&api.ResponseBuf{Identity:sample.identity};if err:=write(buf,selection(sample.field));err!=nil{t.Fatal(err)}
  if bytes.Contains(buf.B,[]byte(secret))!=sample.visible{t.Fatalf("binary visibility changed: %x",buf.B)}
  var object map[string]json.RawMessage
  if name=="row"{raw,err:=schema.BinaryToJSON(nil,buf.B,model,registry);if err!=nil{t.Fatal(err)};if err:=json.Unmarshal(raw,&object);err!=nil{t.Fatal(err)}}else{raw,err:=schema.BinaryListToJSON(nil,buf.B,model,registry);if err!=nil{t.Fatal(err)};var list []map[string]json.RawMessage;if err:=json.Unmarshal(raw,&list);err!=nil||len(list)!=1{t.Fatal(string(raw),err)};object=list[0]}
  _,present:=object[sample.field];if present!=sample.visible{t.Fatalf("JSON visibility changed: %v",object)}
 })}})}
 overflow:=identity("User","{\"id\":1,\"quota\":9223372036854775807}")
 for name,write:=range writers{buf:=&api.ResponseBuf{Identity:overflow};err:=write(buf,selection("checked"));var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Fatal(name,"checked payload lost error",err)}}
 childModel:=registry.LookupModel("VisibleChild");child:=&VisibleChild{Id:42,Shown:&secret,Hidden:&secret}
 var childFields []byte;for _,field:=range childModel.Fields{if field.Name=="id"||field.Name=="shown"||field.Name=="hidden"{var err error;childFields,err=codec.FieldMaskSet(childFields,field.ID);if err!=nil{t.Fatal(err)}}}
 childMask,err:=codec.AppendSelectionMask(nil,childFields,nil);if err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{
  "row":func(buf *api.ResponseBuf)error{return child.WriteLuxo(buf,childMask)},
  "column pointers":func(buf *api.ResponseBuf)error{return WriteColumnarVisibleChild(buf,[]*VisibleChild{child},childMask)},
  "column values":func(buf *api.ResponseBuf)error{return WriteColumnarVisibleChildValues(buf,[]VisibleChild{*child},childMask)},
 }{t.Run("inherited/"+name,func(t *testing.T){
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)}
  var raw []byte;var err error;var object map[string]json.RawMessage
  if name=="row"{raw,err=schema.BinaryToJSON(nil,buf.B,childModel,registry);if err==nil{err=json.Unmarshal(raw,&object)}}else{raw,err=schema.BinaryListToJSON(nil,buf.B,childModel,registry);if err==nil{var list []map[string]json.RawMessage;err=json.Unmarshal(raw,&list);if err==nil&&len(list)!=1{t.Fatal(string(raw))};if err==nil{object=list[0]}}}
  if err!=nil{t.Fatal(string(raw),err)};if string(object["shown"])!="\"sealed-visible-marker\""{t.Fatal("inherited shown field missing",string(raw))};if _,present:=object["hidden"];present{t.Fatal("inherited hidden field leaked",string(raw))}
 })}
 number:=int64(7);box:=VisibleBox{Value:&number}
 for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return box.WriteLuxo(buf,nil)},"column":func(buf *api.ResponseBuf)error{return WriteColumnarVisibleBox(buf,[]VisibleBox{box},nil)}}{
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(name,err)}
  var raw []byte;var err error;if name=="row"{raw,err=schema.BinaryToJSON(nil,buf.B,registry.LookupModel("VisibleBox"),registry)}else{raw,err=schema.BinaryListToJSON(nil,buf.B,registry.LookupModel("VisibleBox"),registry)}
  if err!=nil||!bytes.Contains(raw,[]byte("\"value\":7")){t.Fatal(name,string(raw),err)}
 }
}
`

const genericSealedVisibleAssertions = `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
func TestGenericSealedVisibilityWriters(t *testing.T){
 number:=int64(7);box:=VISIBLE_GENERIC_TYPE{Value:&number};app:=&App{}
 value,err:=app.keep(t.Context(),box);if err!=nil||value==nil||*value!=number{t.Fatal(value,err)}
 registry:=schema.New();if err:=RegisterSchema(registry);err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return box.WriteLuxo(buf,nil)},"column":func(buf *api.ResponseBuf)error{return WriteColumnarVISIBLE_GENERIC_TYPE(buf,[]VISIBLE_GENERIC_TYPE{box},nil)}}{
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(name,err)}
  var raw []byte;var err error;if name=="row"{raw,err=schema.BinaryToJSON(nil,buf.B,registry.LookupModel("VISIBLE_GENERIC_TYPE"),registry)}else{raw,err=schema.BinaryListToJSON(nil,buf.B,registry.LookupModel("VISIBLE_GENERIC_TYPE"),registry)}
  if err!=nil||!bytes.Contains(raw,[]byte("\"value\":7")){t.Fatal(name,string(raw),err)}
 }
}
`
