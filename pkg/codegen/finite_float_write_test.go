package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedFiniteFloatWriters(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			runGeneratedProtocolPackage(t, finiteFloatWriterSource(declaration), finiteFloatWriterAssertions)
		})
	}
}

func finiteFloatWriterSource(declaration string) string {
	return scalarWriterSource(declaration, "Float", "float64")
}

func scalarWriterSource(declaration, scalar, goType string) string {
	child := &ast.ModelDecl{Name: "Child", Fields: []*ast.FieldDecl{
		{Name: "value", Type: &ast.TypeRef{Name: scalar}},
	}}
	parent := &ast.ModelDecl{Name: "Payload", Fields: []*ast.FieldDecl{
		{Name: "value", Type: &ast.TypeRef{Name: scalar}},
		{Name: "optional", Type: &ast.TypeRef{Name: scalar, Nullable: true}},
		{Name: "values", Type: &ast.TypeRef{Name: scalar, IsList: true}},
		{Name: "optionalValues", Type: &ast.TypeRef{Name: scalar, IsList: true, Nullable: true}},
		{Name: "child", Type: &ast.TypeRef{Name: "Child"}},
		{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"Child":   {"value": 1},
		"Payload": {"value": 1, "optional": 2, "values": 3, "optionalValues": 4, "child": 5, "children": 6},
	})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	writeArrayFixtureDeclaration(&source, g, child, nil, declaration)
	if declaration == "model" {
		g.generateModelWriteColumnar(&source, child, nil, "WriteColumnarChildValues", "[]Child")
	}
	writeArrayFixtureDeclaration(&source, g, parent, nil, declaration)
	writeScalarHandlers(&source, parent, declaration, scalar, goType)
	return source.String()
}

func writeScalarHandlers(source *strings.Builder, model *ast.ModelDecl, declaration, scalar, scalarGoType string) {
	for _, kind := range []string{"Native", "Compiled"} {
		for _, list := range []bool{false, true} {
			name, goType := kind, scalarGoType
			if list {
				name, goType = name+"List", "[]"+scalarGoType
			}
			source.WriteString("func encode" + name + "(req *api.Request, result " + goType + ") error {\n")
			ref := &ast.TypeRef{Name: scalar, IsList: list}
			if kind == "Native" {
				writeNativeReturnEncoding(source, ref, nil, nil)
			} else {
				c := newCompiler(nil)
				c.b, c.api.ReturnType = source, ref
				c.writeScalarReturn("result")
			}
			source.WriteString("return nil\n}\n")
		}
	}
	source.WriteString("func encodeLiteral(req *api.Request, value " + scalarGoType + ") error {\n")
	c := newCompiler(nil)
	c.b, c.api.ReturnType = source, &ast.TypeRef{Name: "Payload"}
	if declaration == "model" {
		c.models[model.Name] = model
	}
	c.writeScalarReturn("Payload{Value: value}")
	source.WriteString("return nil\n}\n")
}

const finiteFloatWriterAssertions = `package generated
import (
    "errors"
    "math"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)

func floatMask(ids ...int) []byte {
    var fields []byte
    for _, id := range ids { fields = codec.FieldMaskSet(fields, id) }
    return codec.AppendSelectionMask(nil, fields, nil)
}

func TestRejectSelectedNonFinite(t *testing.T) {
    for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
        payloads := []Payload{
            {Value: value}, {Optional: &value}, {Values: []float64{1, value}},
            {OptionalValues: []float64{1, value}}, {Child: &Child{Value: value}},
            {Children: []Child{{Value: 1}, {Value: value}}},
        }
        for i, payload := range payloads {
            mask := floatMask(i+1)
            for _, write := range []func(*api.ResponseBuf) error{
                func(buf *api.ResponseBuf) error { return payload.WriteLuxo(buf, mask) },
                func(buf *api.ResponseBuf) error { return WriteColumnarPayload(buf, []Payload{{Child: &Child{}}, payload}, mask) },
            } {
                buf := &api.ResponseBuf{}
                if err := write(buf); !errors.Is(err, codec.ErrNonFiniteFloat) {
                    t.Fatalf("selected field %d: %v", i+1, err)
                }
            }
            // Selecting a different field must not inspect the invalid value.
            skip := floatMask(1)
            if i == 0 { skip = floatMask(2) }
            if err := payload.WriteLuxo(&api.ResponseBuf{}, skip); err != nil { t.Fatal(err) }
            if err := WriteColumnarPayload(&api.ResponseBuf{}, []Payload{payload}, skip); err != nil { t.Fatal(err) }
        }
    }
}

func TestFiniteBitsAndEmptyColumns(t *testing.T) {
    for _, bits := range []uint64{0, 1<<63, 1, 1<<63|1, 0x7fefffffffffffff, 0xffefffffffffffff} {
        value := math.Float64frombits(bits)
        payload := Payload{Value: value, Optional: &value, Values: []float64{value}, OptionalValues: []float64{value}}
        buf := &api.ResponseBuf{}
        if err := payload.WriteLuxo(buf, floatMask(1,2,3,4)); err != nil { t.Fatal(err) }
        var out Payload
        dec := codec.NewDecoder(buf.B)
        out.ReadLuxo(dec)
        if err := dec.Finish(); err != nil { t.Fatal(err) }
        if out.Optional == nil || len(out.Values) != 1 || len(out.OptionalValues) != 1 { t.Fatal("missing Float fields") }
        for _, got := range []float64{out.Value, *out.Optional, out.Values[0], out.OptionalValues[0]} {
            if math.Float64bits(got) != bits { t.Fatalf("bits changed: %x", bits) }
        }
    }
    if err := WriteColumnarPayload(&api.ResponseBuf{}, nil, nil); err != nil { t.Fatal(err) }
    payload := Payload{Child: &Child{}}
    if err := payload.WriteLuxo(&api.ResponseBuf{}, nil); err != nil { t.Fatal(err) }
}

func TestGeneratedHandlerFloatErrors(t *testing.T) {
    for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
        for _, encode := range []func(*api.Request, float64) error{encodeNative, encodeCompiled, encodeLiteral} {
            req := &api.Request{Buf: &api.ResponseBuf{}, FieldMask: floatMask(1)}
            if err := encode(req, value); !errors.Is(err, codec.ErrNonFiniteFloat) { t.Fatal(err) }
        }
        for _, encode := range []func(*api.Request, []float64) error{encodeNativeList, encodeCompiledList} {
            if err := encode(&api.Request{Buf: &api.ResponseBuf{}}, []float64{1,value}); !errors.Is(err, codec.ErrNonFiniteFloat) { t.Fatal(err) }
        }
    }
}
`
