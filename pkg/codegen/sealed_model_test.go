package codegen

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGeneratedSealedModelPayloadFraming(t *testing.T) {
	raw, err := os.ReadFile("../lux/codec/testdata/sealed-model-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ DSL string }
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	runGeneratedFixture(t, fixture.DSL, sealedModelAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Person": {"id": 1, "name": 2}, "ModelResult": {"Owner": 1}, "ModelResult.Owner": {"person": 1, "people": 2},
	}})
}

const sealedModelAssertions = `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection")
func TestSealedModelPayloadFrames(t *testing.T){
 const input="{\"Owner\":{\"person\":{\"id\":7,\"name\":\"first\"},\"people\":[{\"id\":8,\"name\":\"second\"}]}}"
 runtimeSchema:=schema.New();if err:=RegisterSchema(runtimeSchema);err!=nil{t.Fatal(err)}
 wire,err:=runtimeSchema.JSONToBinary("ModelResult",[]byte(input));if err!=nil{t.Fatal(err)}
 var jsonValue,inputValue ModelResult;if err:=jsonValue.ReadJSONInput([]byte(input));err!=nil{t.Fatal(err)};dec:=codec.NewDecoder(wire);inputValue.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal("input inline model framing",wire,err)}
 var a,b api.ResponseBuf;if err:=jsonValue.WriteLuxo(&a,nil);err!=nil{t.Fatal(err)};if err:=inputValue.WriteLuxo(&b,nil);err!=nil||!bytes.Equal(a.B,b.B){t.Fatal("input readers disagree",a.B,b.B,err)}
 if bytes.Equal(wire,a.B){t.Fatal("test failed to distinguish input inline models from response framing")}
 raw,err:=runtimeSchema.SealedToJSON(nil,a.B,"ModelResult");if err!=nil||string(raw)!=input{t.Fatal("model output",string(raw),a.B,err)}
 var restored ModelResult;dec=codec.NewDecoder(a.B);restored.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal("response frame",a.B,err)}
 var again api.ResponseBuf;if err:=restored.WriteLuxo(&again,nil);err!=nil||!bytes.Equal(again.B,a.B){t.Fatal("response roundtrip",again.B,err)}
 var list api.ResponseBuf;if err:=WriteColumnarModelResult(&list,[]ModelResult{jsonValue},nil);err!=nil{t.Fatal(err)};values,err:=ReadColumnarModelResult(list.B);if err!=nil||len(values)!=1{t.Fatal("root list",list.B,err)}
 selected,err:=selection.Parse("Owner{person{id},people{name}}");if err!=nil{t.Fatal(err)};mask,err:=schema.SelectToFieldMask(selected,runtimeSchema.SelectionModel("ModelResult"),runtimeSchema);if err!=nil{t.Fatal(err)}
 var projected api.ResponseBuf;if err:=jsonValue.WriteLuxo(&projected,mask);err!=nil{t.Fatal(err)};raw,err=runtimeSchema.SealedToJSON(nil,projected.B,"ModelResult");if err!=nil||string(raw)!="{\"Owner\":{\"person\":{\"id\":7},\"people\":[{\"name\":\"second\"}]}}"{t.Fatal("selected payload",string(raw),err)}
}
`
