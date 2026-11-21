package codegen

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGeneratedSealedStaticSignature(t *testing.T) {
	runGeneratedFixture(t, `sealed Outcome {
 Success(value: Int)
 Failure(reason: String)
 Pending
}

fn identity(value: Outcome): Outcome { return value }
api echo(value: Outcome): Outcome { return identity(value) }
`, `package generated
import("context";"testing")
func TestSealedStaticSignature(t *testing.T){
 app:=&App{}
 var identity func(context.Context,Outcome)(Outcome,error)=app.identity
 if identity==nil||handleEcho(app)==nil{t.Fatal("missing static sealed entrypoints")}
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"Outcome":         {"Success": 1, "Failure": 2, "Pending": 3},
		"Outcome.Success": {"value": 1}, "Outcome.Failure": {"reason": 1},
	}})
}

func TestGeneratedSealedConstructorsAndWhen(t *testing.T) {
	runGeneratedFixture(t, `sealed Outcome { Success(value: Int, note: String = "ok") Pending }
fn make(value: Int): Outcome { return Outcome.Success(value: value) }
fn score(value: Outcome): Int { return when(value) { is Success -> value.value is Pending -> 0 } }
fn nullable(value: Outcome?): Outcome { return when(value) { null -> Outcome.Pending() is Success -> value is Pending -> value } }
api inspect(value: Outcome): Int { return score(value) }
`, `package generated
import("context";"testing")
func TestStaticSealedWhen(t *testing.T){
 app:=&App{};value,err:=app.make(context.Background(),42);if err!=nil{t.Fatal(err)}
 result,err:=app.score(context.Background(),value);if err!=nil||result!=42{t.Fatal(result,err)}
 pending,err:=app.nullable(context.Background(),nil);if err!=nil||pending.LuxoVariantTag()!=2{t.Fatal(pending,err)}
 restored,err:=app.nullable(context.Background(),&value);if err!=nil||restored.LuxoVariantTag()!=1{t.Fatal(restored,err)}
 if result,err:=app.score(context.Background(),Outcome{});err==nil{t.Fatal("invalid zero holder matched",result)}
}


`, StableIDs{ModelFields: map[string]map[string]int{
		"Outcome": {"Success": 1, "Pending": 2}, "Outcome.Success": {"value": 1, "note": 2},
	}})
}

func TestGeneratedSealedNestedOrdinaryAndGenericValues(t *testing.T) {
	runGeneratedFixture(t, `type Box<T> { value: T }
sealed Outcome { Value(value: Box<Int>) Pending }
type Envelope { result: Outcome history: [Outcome]? }
fn make(): Outcome { return Outcome.Value(Box<Int>(value: 7)) }
api echo(value: Envelope): Envelope { return value }
api boxed(value: Box<Outcome>): Box<Outcome> { return value }
api paged(value: Page<Outcome>): Page<Outcome> { return value }
`, sealedNestedValueAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Box": {"value": 1}, "Envelope": {"result": 1, "history": 2},
		"Outcome": {"Value": 1, "Pending": 2}, "Outcome.Value": {"value": 1},
	}})
}

const sealedNestedValueAssertions = `package generated
import("bytes";"encoding/json";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestSealedNestedValues(t *testing.T){
 const source="{\"result\":{\"Value\":{\"value\":{\"value\":7}}},\"history\":[{\"Pending\":{}}]}"
 runtimeSchema:=schema.New();if err:=RegisterSchema(runtimeSchema);err!=nil{t.Fatal(err)}
 var value Envelope;if err:=value.ReadJSONInput([]byte(source));err!=nil{t.Fatal(err)}
 var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)}
 raw,err:=schema.BinaryToJSON(nil,buf.B,runtimeSchema.LookupModel("Envelope"),runtimeSchema);if err!=nil||string(raw)!=source{t.Fatal("row",string(raw),buf.B,err)}
 var restored Envelope;dec:=codec.NewDecoder(buf.B);restored.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal("row read",err)}
 var again api.ResponseBuf;if err:=restored.WriteLuxo(&again,nil);err!=nil||!bytes.Equal(buf.B,again.B){t.Fatal("row roundtrip",again.B,err)}
 input,err:=runtimeSchema.JSONToBinary("Envelope",json.RawMessage(source));if err!=nil{t.Fatal(err)}
 var accepted Envelope;dec=codec.NewDecoder(input);accepted.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal("input row",input,err)}
 var acceptedBuf api.ResponseBuf;if err:=accepted.WriteLuxo(&acceptedBuf,nil);err!=nil||!bytes.Equal(buf.B,acceptedBuf.B){t.Fatal("input value",acceptedBuf.B,err)}
 var columns api.ResponseBuf;if err:=WriteColumnarEnvelope(&columns,[]Envelope{value},nil);err!=nil{t.Fatal(err)}
 rows,err:=ReadColumnarEnvelope(columns.B);if err!=nil||len(rows)!=1{t.Fatal("columnar",columns.B,err)}
 var last api.ResponseBuf;if err:=rows[0].WriteLuxo(&last,nil);err!=nil||!bytes.Equal(buf.B,last.B){t.Fatal("columnar value",last.B,err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{body,want string}{
  {"{\"$api\":\"echo\",\"$select\":\"result{Value{value{value}}},history{Pending}\",\"value\":"+source+"}",source},
  {"{\"$api\":\"boxed\",\"$select\":\"value{Pending}\",\"value\":{\"value\":{\"Pending\":{}}}}","{\"value\":{\"Pending\":{}}}"},
  {"{\"$api\":\"paged\",\"$select\":\"items{Pending},total,page,pageSize\",\"value\":{\"items\":[{\"Pending\":{}}],\"total\":1,\"page\":1,\"pageSize\":1}}","{\"items\":[{\"Pending\":{}}],\"total\":1,\"page\":1,\"pageSize\":1}"},
 }{
  request:=httptest.NewRequest("POST","/",bytes.NewBufferString(sample.body));request.Header.Set("Content-Type","application/json");response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{t.Fatal(sample.body,response.Code,response.Body.String())};var payload struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&payload);err!=nil||string(payload.Data)!=sample.want{t.Fatal(sample.body,response.Body.String(),err)}
 }
}
`

func TestGeneratedSealedCanonicalCodecs(t *testing.T) {
	raw, err := os.ReadFile("../lux/codec/testdata/sealed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		DSL string `json:"dsl"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	runGeneratedFixture(t, fixture.DSL, sealedCanonicalAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Outcome":         {"Success": 1, "Failure": 2, "Pending": 3, "Nested": 4},
		"Outcome.Success": {"value": 1}, "Outcome.Failure": {"reason": 1},
		"Outcome.Nested": {"child": 1, "history": 2}, "EmptyOutcome": {},
	}})
}

const sealedCanonicalAssertions = `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestSealedCanonicalCodecs(t *testing.T){
 for _,test:=range []struct{json string;wire []byte}{
  {"{\"Success\":{\"value\":7}}",[]byte{1,4,0,1,14,0}},
  {"{\"Pending\":{}}",[]byte{3,2,0,0}},
 }{
  var value Outcome;if err:=value.ReadJSONInput([]byte(test.json));err!=nil{t.Fatal(err)}
  var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil||!bytes.Equal(buf.B,test.wire){t.Fatal(buf.B,err)}
  var decoded Outcome;dec:=codec.NewDecoder(test.wire);decoded.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
  buf.B=nil;if err:=decoded.WriteLuxo(&buf,nil);err!=nil||!bytes.Equal(buf.B,test.wire){t.Fatal(buf.B,err)}
 }
 var value Outcome;if err:=value.ReadJSONInput([]byte("{\"Success\":{\"value\":7}}"));err!=nil{t.Fatal(err)}
 tagMask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
 var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,tagMask);err!=nil||!bytes.Equal(buf.B,[]byte{1,2,0,0}){t.Fatal("tag-only",buf.B,err)}
 var missing Outcome;dec:=codec.NewDecoder(buf.B);missing.ReadLuxoInput(dec);if dec.Finish()==nil{t.Fatal("tag-only output weakened input requiredness")}
 var empty EmptyOutcome;buf.B=nil;if err:=empty.WriteLuxo(&buf,nil);err==nil||len(buf.B)!=0{t.Fatal("uninhabited zero",buf.B,err)}
 if err:=empty.ReadJSONInput([]byte("{}"));err==nil{t.Fatal("uninhabited JSON")}
 var list api.ResponseBuf;if err:=WriteColumnarEmptyOutcome(&list,[]EmptyOutcome{},nil);err!=nil||!bytes.Equal(list.B,[]byte{0}){t.Fatal(list.B,err)}
 runtimeSchema:=schema.New();if err:=RegisterSchema(runtimeSchema);err!=nil{t.Fatal(err)}
 for _,test:=range []struct{input string;output string}{
  {"{\"Failure\":{}}","{\"Failure\":{\"reason\":\"failed\"}}"},
  {"{\"Nested\":{}}","{\"Nested\":{\"child\":null,\"history\":null}}"},
  {"{\"Nested\":{\"child\":{\"Success\":{\"value\":7}},\"history\":[{\"Pending\":{}}]}}","{\"Nested\":{\"child\":{\"Success\":{\"value\":7}},\"history\":[{\"Pending\":{}}]}}"},
 }{
  var nested Outcome;if err:=nested.ReadJSONInput([]byte(test.input));err!=nil{t.Fatal(err)}
  var output api.ResponseBuf;if err:=nested.WriteLuxo(&output,nil);err!=nil{t.Fatal(err)}
  raw,err:=runtimeSchema.SealedToJSON(nil,output.B,"Outcome");if err!=nil||string(raw)!=test.output{t.Fatal("nested schema",string(raw),output.B,err)}
  var restored Outcome;dec:=codec.NewDecoder(output.B);restored.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
  var roundtrip api.ResponseBuf;if err:=restored.WriteLuxo(&roundtrip,nil);err!=nil||!bytes.Equal(roundtrip.B,output.B){t.Fatal("nested binary",roundtrip.B,err)}
 }
 for _,bad:=range [][]byte{{0,2,0,0},{99,2,0,0},{1,4,0,1,14},{1,4,0,1,14,0,3,2,0,0}}{
  before:=(Outcome{}).NewVariant3();value=before;dec:=codec.NewDecoder(bad);value.ReadLuxoInput(dec);if dec.Finish()==nil{t.Fatal("bad frame",bad)}
  if value.LuxoVariantTag()!=before.LuxoVariantTag(){t.Fatal("partial value published")}
 }
}
`

func TestGeneratedSealedDepthResources(t *testing.T) {
	input, ids, _ := canonicalSealedInput(t)
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, generated, sealedDepthAssertions)
}

const sealedDepthAssertions = `package generated
import("errors";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func nestedSealedJSON(count int)string{value:="{\"Pending\":{}}";for level:=1;level<count;level++{value="{\"Nested\":{\"child\":"+value+"}}"};return value}
func TestSealedDepthResources(t *testing.T){
 for _,count:=range []int{31,32,33}{
  var value Outcome;err:=value.ReadJSONInput([]byte(nestedSealedJSON(count)))
  if count>32{if !errors.Is(err,codec.ErrModelDepth){t.Fatal("present object budget",count,err)};continue};if err!=nil{t.Fatal(count,err)}
  var output api.ResponseBuf;if err:=value.WriteLuxo(&output,nil);err!=nil{t.Fatal(count,err)}
  var restored Outcome;dec:=codec.NewDecoder(output.B);restored.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal(count,err)}
 }
 for _,field:=range []string{"child","history"}{for _,count:=range []int{31,32}{
  raw:=nestedSealedJSON(count);if field=="history"{raw="["+raw+"]"};raw="{\"Nested\":{\""+field+"\":"+raw+",\""+field+"\":null}}"
  var value Outcome;err:=value.ReadJSONInput([]byte(raw));if count==31{if err!=nil{t.Fatal("legal discarded value",field,err)}}else if !errors.Is(err,codec.ErrModelDepth){t.Fatal("discarded object budget",field,err)}
 }}
}
`

func TestGeneratedSealedConstructorPreservesArgumentOrder(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime { id: Int @id }
sealed Pair { Value(first: Int, second: Int) Values(values: [Int]) }
fn mark(user: User): Int { user.id = 99 return 1 }
fn positional(user: User): Pair { return Pair.Value(user.id, mark(user)) }
fn named(user: User): Pair { return Pair.Value(second: user.id, first: mark(user)) }
fn numbers(): Pair { return Pair.Values([1, 2]) }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSealedConstructorArgumentOrder(t *testing.T){
 app:=&App{};user:=&User{Id:42}
 value,err:=app.positional(t.Context(),user);if err!=nil||user.Id!=99{t.Fatal(value,user,err)}
 if fields:=value.AsVariant1();fields.F1!=42||fields.F2!=1{t.Fatal("positional source order",fields)}
 user.Id=42;value,err=app.named(t.Context(),user);if err!=nil||user.Id!=99{t.Fatal(value,user,err)}
 if fields:=value.AsVariant1();fields.F1!=1||fields.F2!=42{t.Fatal("named source order",fields)}
 values,err:=app.numbers(t.Context());if err!=nil{t.Fatal(err)};if fields:=values.AsVariant2();len(fields.F1)!=2||fields.F1[0]!=1||fields.F1[1]!=2{t.Fatal("static list payload",fields)}
 var output api.ResponseBuf;if err:=values.WriteLuxo(&output,nil);err!=nil{t.Fatal(err)};var restored Pair;dec:=codec.NewDecoder(output.B);restored.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil||restored.AsVariant2().F1[1]!=2{t.Fatal("list wire",output.B,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1}, "Pair": {"Value": 1, "Values": 2}, "Pair.Value": {"first": 1, "second": 2}, "Pair.Values": {"values": 1},
	}})
}
