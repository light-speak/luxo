package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedRawJSONBoundaries(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			source := scalarWriterSource(declaration, "JSON", "json.RawMessage")
			source = strings.Replace(source, "import (", "import (\"encoding/json\";", 1)
			runGeneratedProtocolPackage(t, source, rawJSONAssertions)
		})
	}
}

func TestGeneratedRawJSONEvents(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"Document": {"value": 1, "optional": 2, "values": 3},
	}}})
	event := &ast.EventDecl{Name: "Document", Params: []*ast.ParamDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "JSON", Nullable: true}},
		{Name: "values", Type: &ast.TypeRef{Name: "JSON", IsList: true}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"encoding/json\"; \"fmt\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateEventStruct(&source, event)
	g.generateEventCodec(&source, event, nil, nil)
	runGeneratedProtocolPackage(t, source.String(), rawJSONEventAssertions)
}

func TestGeneratedRawJSONStream(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{
		{Name: "watchJSON", ReturnType: &ast.TypeRef{Name: "JSON"}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}},
		{Name: "watchJSONList", ReturnType: &ast.TypeRef{Name: "JSON", IsList: true}, Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}},
	}}}}
	runGeneratedProtocolPackage(t, string(generateStreamFile(result, "generated")), rawJSONStreamAssertions)
}

const rawJSONAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

    "bytes"
    "encoding/json"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)

func jsonMask(t testing.TB, id int) []byte {
    return testutil.Bytes(t)(codec.AppendSelectionMask(nil, testutil.Bytes(t)(codec.FieldMaskSet(nil, id)), nil))
}

func jsonPayload(raw json.RawMessage) Payload {
    return Payload{Value:raw, Optional:&raw, Values:[]json.RawMessage{raw},
        OptionalValues:[]json.RawMessage{raw}, Child:&Child{Value:raw}, Children:[]Child{{Value:raw}}}
}

func badJSONValues() []json.RawMessage {
    return []json.RawMessage{nil, {}, []byte("{"), []byte("null true"), []byte("\"\\uD800\""),
        []byte("{\"\\uDC00\":1}"), []byte("{\"a\":\"\\uD800\",\"a\":0}"), []byte("\"\xff\""), []byte("NaN")}
}

func TestRawJSONWrites(t *testing.T) {
    for _, raw := range badJSONValues() {
        payload := jsonPayload(raw)
        for field := 1; field <= 6; field++ {
            mask := jsonMask(t, field)
            buf := &api.ResponseBuf{}
            if err := payload.WriteLuxo(buf, mask); err == nil { t.Errorf("row field %d accepted %q", field, raw) }
            buf.B = nil
            if err := WriteColumnarPayload(buf, []Payload{payload}, mask); err == nil { t.Errorf("column field %d accepted %q", field, raw) }
        }
        for _, write := range []func(*api.Request, json.RawMessage) error{encodeNative, encodeCompiled} {
            if err := write(&api.Request{Buf:&api.ResponseBuf{}}, raw); err == nil { t.Errorf("scalar accepted %q", raw) }
        }
        for _, write := range []func(*api.Request, []json.RawMessage) error{encodeNativeList, encodeCompiledList} {
            if err := write(&api.Request{Buf:&api.ResponseBuf{}}, []json.RawMessage{[]byte("null"), raw}); err == nil { t.Errorf("scalar list accepted %q", raw) }
        }
        // An unselected invalid field must not make an unrelated projection fail.
        payload.Value = []byte("null")
        if err := payload.WriteLuxo(&api.ResponseBuf{}, jsonMask(t, 1)); err != nil { t.Fatal(err) }
        if err := WriteColumnarPayload(&api.ResponseBuf{}, []Payload{payload}, jsonMask(t, 1)); err != nil { t.Fatal(err) }
    }
}

func rawJSONCell(field int, raw []byte, column, input bool) []byte {
    encoded := codec.AppendBytes(nil, raw)
    switch field {
    case 1: return encoded
    case 2: return append([]byte{1}, encoded...)
    case 3, 4:
        cell := append([]byte{1}, encoded...)
        if column { cell = codec.AppendBytes(nil, cell) }
        if field == 4 { cell = append([]byte{1}, cell...) }
        return cell
    case 5, 6:
        child := append(append([]byte{0, 1}, encoded...), 0)
        if field == 6 {
            if input { return append([]byte{1}, child...) }
            child = append([]byte{1}, child...)
        }
        if input { return child }
        return codec.AppendBytes(nil, child)
    }
    panic("test field")
}

func TestRawJSONReads(t *testing.T) {
    for _, raw := range badJSONValues() {
        for field := 1; field <= 6; field++ {
            for _, input := range []bool{false, true} {
                data := append(append([]byte{0, byte(field)}, rawJSONCell(field, raw, false, input)...), 0)
                dec, value := codec.NewDecoder(data), Payload{}
                if input { value.ReadLuxoInput(dec) } else { value.ReadLuxo(dec) }
                if err := dec.Finish(); err == nil { t.Errorf("row field %d input %v accepted %q", field, input, raw) }
            }
            data := append(append([]byte{1, 0, byte(field)}, rawJSONCell(field, raw, true, false)...), 0)
            if rows, err := ReadColumnarPayload(data); err == nil || rows != nil { t.Errorf("column field %d accepted %q", field, raw) }
        }
    }
}

func TestRawJSONExactRoundtrip(t *testing.T) {
    for _, raw := range []json.RawMessage{[]byte("null"), []byte(" {} "), []byte("[null,{},[]]"),
        []byte("{\"n\":123456789012345678901234567890,\"f\":1.2345678901234567890123456789,\"z\":-0.00,\"e\":1e-9999,\"t\":\"\\uD800\\uDC00東京\"}")} {
        value := jsonPayload(raw)
        for field := 1; field <= 6; field++ {
            buf := &api.ResponseBuf{}
            if err := value.WriteLuxo(buf, jsonMask(t, field)); err != nil { t.Fatal(err) }
            dec, out := codec.NewDecoder(buf.B), Payload{}
            out.ReadLuxo(dec)
            if err := dec.Finish(); err != nil { t.Fatal(err) }
            checkJSONField(t, out, field, raw)
            buf.B = nil
            if err := WriteColumnarPayload(buf, []Payload{value}, jsonMask(t, field)); err != nil { t.Fatal(err) }
            rows, err := ReadColumnarPayload(buf.B)
            if err != nil || len(rows) != 1 { t.Fatalf("column: %v", err) }
            checkJSONField(t, rows[0], field, raw)
        }
    }
    value := Payload{}
    for _, field := range []int{2,4} {
        buf := &api.ResponseBuf{}
        if err := value.WriteLuxo(buf, jsonMask(t, field)); err != nil { t.Fatal(err) }
        out, dec := Payload{}, codec.NewDecoder(buf.B)
        out.ReadLuxo(dec)
        if err := dec.Finish(); err != nil || out.Optional != nil || out.OptionalValues != nil { t.Fatalf("null: %v", err) }
    }
}

func checkJSONField(t *testing.T, value Payload, field int, want []byte) {
    t.Helper()
    var raw []byte
    switch field {
    case 1: raw = value.Value
    case 2: if value.Optional != nil { raw = *value.Optional }
    case 3: if len(value.Values) == 1 { raw = value.Values[0] }
    case 4: if len(value.OptionalValues) == 1 { raw = value.OptionalValues[0] }
    case 5: if value.Child != nil { raw = value.Child.Value }
    case 6: if len(value.Children) == 1 { raw = value.Children[0].Value }
    }
    if !bytes.Equal(raw, want) { t.Fatalf("field %d lost raw text: %q / %q", field, raw, want) }
}
`

const rawJSONEventAssertions = `package generated
import ("bytes"; "encoding/json"; "testing"; "github.com/light-speak/luxo/pkg/lux/codec")

func TestRawJSONEvent(t *testing.T) {
    good := json.RawMessage("{\"n\":123456789012345678901234567890,\"x\":-0.000,\"e\":1e-9999}")
    for _, raw := range []json.RawMessage{nil, {}, []byte("{"), []byte("\"\\uD800\""), []byte("\"\xff\""), []byte("null false")} {
        for field := 1; field <= 3; field++ {
            event := DocumentEvent{Value:good, Optional:&good, Values:[]json.RawMessage{good}}
            switch field { case 1: event.Value=raw; case 2: event.Optional=&raw; case 3: event.Values=append(event.Values,raw) }
            if data,err := event.MarshalLuxo(); err == nil || data != nil { t.Errorf("event field %d accepted %q",field,raw) }
            var enc codec.Encoder
            enc.WriteFieldBytes(1, good)
            if field != 1 { enc.WriteFieldHeader(field) }
            if field == 2 { enc.WritePresent() }
            if field == 3 { enc.WriteArrayHeader(1) }
            if field == 1 { enc.Reset(); enc.WriteFieldHeader(1) }
            enc.WriteBytes(raw)
            if field != 3 { enc.WriteFieldHeader(3); enc.WriteArrayHeader(0) }
            enc.WriteEnd()
            var decoded DocumentEvent
            if err := decoded.UnmarshalLuxo(enc.Bytes()); err == nil { t.Errorf("event read %d accepted %q",field,raw) }
        }
    }
    event := DocumentEvent{Value:good, Optional:&good, Values:[]json.RawMessage{[]byte("null"),good}}
    data,err := event.MarshalLuxo(); if err != nil { t.Fatal(err) }
    var out DocumentEvent
    if err := out.UnmarshalLuxo(data); err != nil { t.Fatal(err) }
    if !bytes.Equal(out.Value,good) || out.Optional==nil || !bytes.Equal(*out.Optional,good) || len(out.Values)!=2 || !bytes.Equal(out.Values[1],good) { t.Fatal("raw event changed") }
}
`

const rawJSONStreamAssertions = `package generated
import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
type jsonResolver struct { raw json.RawMessage }
func (r *jsonResolver) HandleWatchJSON(_ context.Context, _ *WatchJSONStreamParams, _ any, stream *api.TypedStream[json.RawMessage]) error {
    return stream.Send(r.raw)
}
func (r *jsonResolver) HandleWatchJSONList(_ context.Context, _ *WatchJSONListStreamParams, _ any, stream *api.TypedStream[[]json.RawMessage]) error {
    return stream.Send([]json.RawMessage{[]byte("null"),r.raw})
}
func TestRawJSONNativeStream(t *testing.T) {
    for _, sample := range []struct{ raw string; valid bool }{
        {"",false}, {"\"\\uD800\"",false}, {"{} trailing",false},
        {"{\"n\":123456789012345678901234567890,\"f\":1e-9999}",true},
    } {
        router := api.NewRouter()
        if err:=RegisterStreams(router,nil,&jsonResolver{raw:json.RawMessage(sample.raw)});err!=nil{t.Fatal(err)}
        for _, name := range []string{"watchJSON","watchJSONList"} {
            ctx,cancel := context.WithTimeout(t.Context(),time.Second)
            sub,err := router.OpenInternalStream(ctx,&api.Request{API:name})
            if err != nil { cancel(); t.Fatal(err) }
            count := 0
            for payload := range sub.Messages() {
                count++
                dec := codec.NewDecoder(payload)
                if name == "watchJSONList" {
                    if dec.ReadArrayLength()!=2 || string(dec.ReadJSON())!="null" { t.Fatal("invalid list") }
                }
                if !bytes.Equal(dec.ReadJSON(),[]byte(sample.raw)) || dec.Finish()!=nil { t.Fatal("raw JSON stream changed") }
            }
            sub.Close()
            cancel()
            if sample.valid {
                if count!=1 || sub.Err()!=nil { t.Fatalf("valid stream: %d / %v",count,sub.Err()) }
            } else if count!=0 || !errors.Is(sub.Err(),codec.ErrInvalidJSON) {
                t.Fatalf("bad JSON reached callback: %d / %v",count,sub.Err())
            }
        }
    }
}
`
