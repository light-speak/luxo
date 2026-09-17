package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedStringWriters(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			source := scalarWriterSource(declaration, "String", "string")
			runGeneratedProtocolPackage(t, source, stringWriterAssertions)
		})
	}
}

const stringWriterAssertions = `package generated
import (
    "errors"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)

func stringMask(ids ...int) []byte {
    var fields []byte
    for _, id := range ids { fields = codec.FieldMaskSet(fields, id) }
    return codec.AppendSelectionMask(nil, fields, nil)
}

func TestRejectSelectedInvalidText(t *testing.T) {
    for _, value := range []string{"\xff", "prefix\xed\xa0\x80tail", "\xf4\x90\x80\x80"} {
        payloads := []Payload{
            {Value: value}, {Optional: &value}, {Values: []string{"ok", value}},
            {OptionalValues: []string{"ok", value}}, {Child: &Child{Value: value}},
            {Children: []Child{{Value: "ok"}, {Value: value}}},
        }
        for index, item := range payloads {
            mask := stringMask(index+1)
            buf := &api.ResponseBuf{B: []byte("prefix")}
            if err := item.WriteLuxo(buf, mask); !errors.Is(err, codec.ErrInvalidUTF8) || string(buf.B[:6]) != "prefix" {
                t.Fatalf("row %d published invalid text: %x / %v", index, buf.B, err)
            }
            // Generated writers return failure; the transport discards the failed response.
            buf.B = buf.B[:6]
            if err := WriteColumnarPayload(buf, []Payload{{Value:"ok", Child:&Child{Value:"ok"}}, item}, mask); !errors.Is(err, codec.ErrInvalidUTF8) || string(buf.B[:6]) != "prefix" {
                t.Fatalf("column %d published invalid text: %x / %v", index, buf.B, err)
            }
        }
        for _, encode := range []func(*api.Request, string) error{encodeNative, encodeCompiled} {
            req := &api.Request{Buf: &api.ResponseBuf{}}
            if err := encode(req, value); !errors.Is(err, codec.ErrInvalidUTF8) { t.Fatalf("scalar: %v", err) }
        }
        for _, encode := range []func(*api.Request, []string) error{encodeNativeList, encodeCompiledList} {
            req := &api.Request{Buf: &api.ResponseBuf{}}
            if err := encode(req, []string{"ok", value}); !errors.Is(err, codec.ErrInvalidUTF8) { t.Fatalf("array: %v", err) }
        }
        item := Payload{Value:"valid", Optional:&value, Values:[]string{value}}
        if err := item.WriteLuxo(&api.ResponseBuf{}, stringMask(1)); err != nil { t.Fatalf("unselected field validated: %v", err) }
    }
}
`

func TestGeneratedEventStringErrors(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"Text": {"value": 1, "optional": 2, "values": 3, "role": 4},
	}}})
	event := &ast.EventDecl{Name: "Text", Params: []*ast.ParamDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "String"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "String", Nullable: true}},
		{Name: "values", Type: &ast.TypeRef{Name: "String", IsList: true}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
	}}
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\ntype Role string\n")
	generateEventStruct(&source, event)
	g.generateEventCodec(&source, event, map[string]bool{"Role": true}, nil)
	runGeneratedProtocolPackage(t, source.String(), stringEventAssertions)
}

const stringEventAssertions = `package generated
import ("errors"; "testing"; "github.com/light-speak/luxo/pkg/lux/codec")
func TestStringEventFailure(t *testing.T) {
    for _, value := range []string{"\xff", "good\xed\xa0\x80tail"} {
        for _, event := range []TextEvent{{Value:value}, {Optional:&value}, {Values:[]string{"ok", value}}, {Role:Role(value)}} {
            data, err := event.MarshalLuxo()
            if !errors.Is(err, codec.ErrInvalidUTF8) || data != nil { t.Fatalf("invalid event returned: %x / %v", data, err) }
        }
    }
    value := "東京\ufeff\x00"
    event := TextEvent{Value:value, Optional:&value, Values:[]string{"", value}, Role:Role(value)}
    data, err := event.MarshalLuxo(); if err != nil { t.Fatal(err) }
    var out TextEvent
    if err := out.UnmarshalLuxo(data); err != nil { t.Fatal(err) }
    if out.Value != value || out.Optional == nil || *out.Optional != value || len(out.Values) != 2 || out.Values[1] != value || out.Role != event.Role { t.Fatalf("valid event changed: %+v", out) }
}
`

func TestGeneratedStringOutputDirectives(t *testing.T) {
	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "rewriteText"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "it"}}}}},
	}}}
	masked := &ast.Directive{Name: "mask", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "****"}}}}
	model := &ast.ModelDecl{Name: "Payload", Fields: []*ast.FieldDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{transform}},
		{Name: "masked", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{masked}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		{Name: "optionalRole", Type: &ast.TypeRef{Name: "Role", Nullable: true}},
		{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"Payload": {"value": 1, "masked": 2, "role": 3, "optionalRole": 4, "roles": 5},
	})
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			var source strings.Builder
			source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\"; \"github.com/light-speak/luxo/pkg/lux/str\")\ntype Role string\n")
			writeArrayFixtureDeclaration(&source, g, model, map[string]bool{"Role": true}, declaration)
			runGeneratedProtocolPackage(t, source.String(), stringDirectiveAssertions)
		})
	}
}

const stringDirectiveAssertions = `package generated
import ("errors"; "testing"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/codec")

var rewritten string
var rewriteCalls int
func rewriteText(string) string { rewriteCalls++; return rewritten }

func writeTextFixture(t *testing.T, item Payload, field int, column bool) ([]byte, error) {
    t.Helper()
    mask := codec.AppendSelectionMask(nil, codec.FieldMaskSet(nil, field), nil)
    buf := &api.ResponseBuf{}
    if column { err := WriteColumnarPayload(buf, []Payload{item}, mask); return buf.B, err }
    err := item.WriteLuxo(buf, mask)
    return buf.B, err
}

func TestStringValidationAfterOutputDirectives(t *testing.T) {
    for _, column := range []bool{false,true} {
        rewritten, rewriteCalls = "bad\xff", 0
        if _, err := writeTextFixture(t, Payload{Value:"valid"}, 1, column); !errors.Is(err, codec.ErrInvalidUTF8) || rewriteCalls != 1 {
            t.Fatalf("invalid transformed value: %v / calls=%d", err, rewriteCalls)
        }
        rewritten, rewriteCalls = "東京", 0
        data, err := writeTextFixture(t, Payload{Value:"bad\xff"}, 1, column)
        if err != nil || rewriteCalls != 1 { t.Fatalf("transformed value rejected: %v / calls=%d", err, rewriteCalls) }
        checkTextOutput(t, data, column, 1, "東京")
        rewritten, rewriteCalls = "bad\xff", 0
        data, err = writeTextFixture(t, Payload{Value:"unselected", Masked:"abc\xff"}, 2, column)
        if err != nil || rewriteCalls != 0 { t.Fatalf("mask/selection ignored: %v / calls=%d", err, rewriteCalls) }
        checkTextOutput(t, data, column, 2, "****")
    }
}

func checkTextOutput(t *testing.T, data []byte, column bool, field int, want string) {
    t.Helper()
    if column {
        reader := codec.NewColumnarReader(data)
        if reader.Count() != 1 || !reader.NextColumn() || reader.FieldID() != field { t.Fatal("invalid column header") }
        values := reader.ReadColumnString()
        if reader.NextColumn() || reader.Err() != nil || len(values) != 1 || values[0] != want { t.Fatalf("column text: %v / %v", values, reader.Err()) }
        return
    }
    dec := codec.NewDecoder(data)
    dec.SkipArenaHeader()
    if !dec.NextField() || dec.FieldID() != field || dec.ReadString() != want || dec.NextField() || dec.Finish() != nil { t.Fatalf("row text: %x / %v", data, dec.Err()) }
}

func TestEnumStringValidation(t *testing.T) {
    invalid := Role("bad\xff")
    items := []Payload{{Role:invalid}, {OptionalRole:&invalid}, {Roles:[]Role{"valid", invalid}}}
    for i, item := range items {
        for _, column := range []bool{false,true} {
            if _, err := writeTextFixture(t, item, i+3, column); !errors.Is(err, codec.ErrInvalidUTF8) { t.Fatalf("enum field %d: %v", i+3, err) }
        }
    }
}
`
