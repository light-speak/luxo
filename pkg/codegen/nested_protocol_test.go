package codegen

import (
	"go/format"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedNestedResponseProtocol(t *testing.T) {
	for _, declaration := range []string{"model", "type", "mixed"} {
		t.Run(declaration, func(t *testing.T) {
			runGeneratedProtocolPackage(t, nestedProtocolSource(t, declaration), nestedProtocolAssertions+nestedInputAssertions+nestedMalformedAssertions+nestedRecursiveAssertions+nestedBenchmarks)
		})
	}
}

const nestedInputAssertions = `
func BenchmarkPayloadNestedInput(b *testing.B) {
    for _, count := range []int{0, 1, 128} {
        data := codec.AppendArrayHeader([]byte{0, 3}, count)
        for range count { data = append(data, 0, 1, 84, 0) }
        data = append(data, 0)
        for _, before := range []bool{true, false} {
            b.Run(fmt.Sprintf("count=%d/before=%v", count, before), func(b *testing.B) {
                var value Parent
                b.ReportAllocs()
                for b.Loop() {
                    dec := codec.NewDecoder(data)
                    if before { value.readInputBeforePayload(dec) } else { value.ReadLuxoInput(dec) }
                    if err := dec.Finish(); err != nil || len(value.Children) != count { b.Fatal("invalid benchmark result") }
                }
            })
        }
    }
}

func TestNestedInputRejectsAllocationWithoutPayload(t *testing.T) {
    raw := codec.AppendArrayHeader([]byte{0, 3}, 32)
    raw = append(raw, 0)
    dec := codec.NewDecoder(raw)
    var value Parent
    value.ReadLuxoInput(dec)
    if dec.Err() == nil || value.Children != nil { t.Fatalf("short input allocated children: %d, %v", len(value.Children), dec.Err()) }
}

func TestNestedInputUnicode(t *testing.T) {
    metadata, _ := nestedMetadata()
    for _, input := range []string{
        "{\"child\":{\"label\":\"\\uD800\"}}",
        "{\"children\":[{\"label\":\"\\uDC00\"}]}",
        "{\"child\":{\"\\uD800\":1}}",
        "{\"child\":{\"label\":\"\\uD800\",\"label\":\"valid\"}}",
    } {
        if raw, err := metadata.JSONToBinary("Parent", []byte(input)); err == nil || raw != nil {
            t.Fatalf("invalid Unicode reached generated input: %x / %v", raw, err)
        }
    }
    raw, err := metadata.JSONToBinary("Parent", []byte("{\"child\":{\"label\":\"\\uD800\\uDC00\"},\"children\":[{\"label\":\"東京\"}],\"tail\":42}"))
    if err != nil { t.Fatal(err) }
    dec := codec.NewDecoder(raw)
    var value Parent
    value.ReadLuxoInput(dec)
    if err := dec.Finish(); err != nil { t.Fatal(err) }
    if value.Child.Label != "\U00010000" || len(value.Children) != 1 || value.Children[0].Label != "東京" || value.Tail != 42 {
        t.Fatalf("input Unicode changed: %#v", value)
    }
}

func TestNestedInputGrammar(t *testing.T) {
    metadata, _ := nestedMetadata()
    rawJSON := []byte("{\"child\":{\"id\":9223372036854775807,\"label\":\"東京\"},\"optional\":null,\"children\":[{\"id\":-9223372036854775808}],\"optionalChildren\":[],\"tail\":42}")
    raw, err := metadata.JSONToBinary("Parent", rawJSON)
    if err != nil { t.Fatal(err) }
    dec := codec.NewDecoder(raw)
    value := Parent{Optional: &Child{}, OptionalChildren: []Child{{}}}
    value.ReadLuxoInput(dec)
    if err := dec.Finish(); err != nil { t.Fatal(err) }
    if value.Child.Id != math.MaxInt64 || value.Child.Label != "東京" || value.Optional != nil || len(value.Children) != 1 || value.Children[0].Id != math.MinInt64 || value.OptionalChildren == nil || len(value.OptionalChildren) != 0 || value.Tail != 42 {
        t.Fatalf("input mismatch: %#v", value)
    }
}
`

const nestedMalformedAssertions = `
func TestGeneratedRejectsImpossiblyLargeArena(t *testing.T) {
    row := append(codec.AppendVarint(nil, 1<<20), 0)
    for _, input := range []bool{false, true} {
        dec := codec.NewDecoder(row)
        var value Child
        if input { value.ReadLuxoInput(dec) } else { value.ReadLuxo(dec) }
        if dec.Err() == nil { t.Fatal("accepted arena with no string payload") }
    }
    column := append(codec.AppendVarint([]byte{1}, 1<<20), 0)
    if _, err := ReadColumnarChild(column); err == nil { t.Fatal("accepted column arena with no string payload") }
    allocations := testing.AllocsPerRun(100, func() {
        var value Child
        dec := codec.NewDecoder(row)
        value.ReadLuxo(dec)
        if dec.Err() == nil { t.Fatal("invalid row accepted") }
        if _, err := ReadColumnarChild(column); err == nil { t.Fatal("invalid column accepted") }
    })
    if allocations != 0 { t.Fatalf("invalid arena allocated before rejection: %v", allocations) }
}

func TestGeneratedReadersRejectInvalidText(t *testing.T) {
    for _, raw := range [][]byte{{0xff}, {0xc0, 0x80}, {0xed, 0xa0, 0x80}} {
        row := append(codec.AppendBytes([]byte{0, 2}, raw), 0)
        for _, input := range []bool{false, true} {
            dec := codec.NewDecoder(row)
            var value Child
            if input { value.ReadLuxoInput(dec) } else { value.ReadLuxo(dec) }
            if dec.Err() == nil || value.Label != "" { t.Fatal("invalid text reached generated object") }
        }
        column := append(codec.AppendBytes([]byte{1, 0, 2}, raw), 0)
        if _, err := ReadColumnarChild(column); err == nil { t.Fatal("invalid text reached generated column") }
    }
}

func TestNestedResponseBoundaries(t *testing.T) {
    rows := [][]byte{{}, {0}, {0,1}, {0,1,84}, {0,0,42}, {0,99,0}}
    columns := [][]byte{{}, {1}, {1,0}, {1,0,1}, {1,0,1,84}, {1,0,1,84,0,42}, {1,0,99,0}}
    for _, test := range []struct{ field byte; payloads [][]byte }{{1, rows}, {3, columns}} {
        for _, payload := range test.payloads {
            raw := codec.AppendBytes([]byte{0, test.field}, payload)
            raw = append(raw, 5,84,0)
            var value Parent
            dec := codec.NewDecoder(raw)
            value.ReadLuxo(dec)
            if dec.Err() == nil { t.Fatalf("accepted bad child field=%d payload=%x", test.field, payload) }
        }
    }
    for _, field := range []byte{2,4} {
        for _, payload := range [][]byte{{2}, {1}, {1,4,0}} {
            raw := append(append([]byte{0,field}, payload...), 0)
            var value Parent
            dec := codec.NewDecoder(raw)
            value.ReadLuxo(dec)
            if dec.Err() == nil { t.Fatalf("accepted bad nullable field=%d payload=%x", field, payload) }
        }
    }
}
`

func nestedProtocolSource(t *testing.T, declaration string) string {
	t.Helper()
	child := &ast.ModelDecl{Name: "Child", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "label", Type: &ast.TypeRef{Name: "String"}},
	}}
	parent := &ast.ModelDecl{Name: "Parent", Fields: []*ast.FieldDecl{
		{Name: "child", Type: &ast.TypeRef{Name: "Child"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "Child", Nullable: true}},
		{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}},
		{Name: "optionalChildren", Type: &ast.TypeRef{Name: "Child", IsList: true, Nullable: true}},
		{Name: "tail", Type: &ast.TypeRef{Name: "Int"}},
	}}
	node := &ast.ModelDecl{Name: "Node", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "next", Type: &ast.TypeRef{Name: "Node", Nullable: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"Child":  {"id": 1, "label": 2},
		"Parent": {"child": 1, "optional": 2, "children": 3, "optionalChildren": 4, "tail": 5},
		"Node":   {"id": 1, "next": 2},
	})
	file := &ast.File{}
	var declarations strings.Builder
	declarations.WriteString("package generated\n")
	for _, model := range []*ast.ModelDecl{child, parent, node} {
		generateModel(&declarations, model, nil)
		if declaration == "model" || declaration == "mixed" && model == parent {
			file.Models = append(file.Models, model)
		} else {
			file.Types = append(file.Types, &ast.TypeDecl{Name: model.Name, Fields: model.Fields})
		}
	}
	generated := g.generateWriteJSONFile(&semantic.Result{Files: []*ast.File{file}}, "generated", nil)
	declarations.WriteString(nestedInlineBenchmarkBaseline(string(generated)))
	declarations.WriteString(nestedBufferedBenchmarkBaseline(string(generated)))
	declarations.WriteString(nestedInputPayloadBaseline(t, string(generated)))
	declarations.WriteString("func writeParents(buf *api.ResponseBuf, items []Parent, mask []byte) {\n")
	if declaration != "type" {
		declarations.WriteString("WriteColumnarParentValues(buf, items, mask)\n}\n")
	} else {
		declarations.WriteString("WriteColumnarParent(buf, items, mask)\n}\n")
	}
	// Keep imports before declarations in the generated package.
	return string(generated) + strings.TrimPrefix(declarations.String(), "package generated\n")
}

func nestedInputPayloadBaseline(t *testing.T, source string) string {
	t.Helper()
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	source = string(formatted)
	start := strings.Index(source, "func (p *Parent) ReadLuxoInput(")
	if start < 0 {
		t.Fatal("generated input reader missing from benchmark reference")
	}
	endOffset := strings.Index(source[start:], "\n}\n")
	if endOffset < 0 {
		t.Fatal("generated input reader has no complete function boundary")
	}
	end := start + endOffset + 3
	body := strings.Replace(source[start:end], "ReadLuxoInput(", "readInputBeforePayload(", 1)
	return strings.ReplaceAll(body, "dec.CheckElementBytes(_n, 2)", "dec.Err() == nil")
}

func TestNestedInputPayloadBenchmarkReference(t *testing.T) {
	source := nestedProtocolSource(t, "model")
	baseline := nestedInputPayloadBaseline(t, source)
	if strings.Count(baseline, "dec.Err() == nil") != 2 || strings.Contains(baseline, "CheckElementBytes") || !strings.HasSuffix(baseline, "}\n") {
		t.Fatalf("invalid frozen input reader: %s", baseline)
	}
}

// Keep the generated mask/scalar work identical; replace only child framing.
// This reference is generated solely inside the temporary test package.
func nestedInlineBenchmarkBaseline(generated string) string {
	start := strings.Index(generated, "func (p *Parent) WriteLuxo(")
	if start < 0 {
		start = strings.Index(generated, "func (p Parent) WriteLuxo(")
	}
	end := start + strings.Index(generated[start:], "\n}\n") + 3
	body := strings.Replace(generated[start:end], "WriteLuxo(", "writeInlineBenchmarkBaseline(", 1)
	var result strings.Builder
	for line := range strings.Lines(body) {
		if strings.Contains(line, "buf.BeginMessage()") || strings.Contains(line, "buf.EndMessage(") {
			continue
		}
		for _, field := range []struct{ name, mask string }{{"Children", "_childMask3"}, {"OptionalChildren", "_childMask4"}} {
			for _, writer := range []string{"WriteColumnarChild", "WriteColumnarChildValues"} {
				line = strings.ReplaceAll(line, writer+"(buf, p."+field.name+", "+field.mask+")",
					"func() error { buf.B = codec.AppendArrayHeader(buf.B, len(p."+field.name+")); for i := range p."+field.name+" { if err := p."+field.name+"[i].WriteLuxo(buf, "+field.mask+"); err != nil { return err } }; return nil }()")
			}
		}
		result.WriteString(line)
	}
	return result.String()
}

func nestedBufferedBenchmarkBaseline(generated string) string {
	start := strings.Index(generated, "func (p *Parent) WriteLuxo(")
	if start < 0 {
		start = strings.Index(generated, "func (p Parent) WriteLuxo(")
	}
	end := start + strings.Index(generated[start:], "\n}\n") + 3
	body := strings.Replace(generated[start:end], "WriteLuxo(", "writeBufferedBenchmarkBaseline(", 1)
	body = strings.ReplaceAll(body, "WriteColumnarChildValues(", "writeBufferedChildren(")
	return strings.ReplaceAll(body, "WriteColumnarChild(", "writeBufferedChildren(")
}

const nestedProtocolAssertions = `package generated
import (
    "bytes"
    "fmt"
    "math"
    "strings"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/schema"
    "github.com/light-speak/luxo/pkg/lux/selection"
)

func nestedMetadata() (*schema.Schema, *schema.Model) {
    metadata := schema.New()
    metadata.RegisterModel(&schema.Model{Name: "Child", Fields: []schema.Field{
        {ID: 1, Name: "id", Type: schema.FieldInt},
        {ID: 2, Name: "label", Type: schema.FieldString},
    }})
    model := &schema.Model{Name: "Parent", Fields: []schema.Field{
        {ID: 1, Name: "child", Type: schema.FieldModel, TypeName: "Child", Relation: true},
        {ID: 2, Name: "optional", Type: schema.FieldModel, TypeName: "Child", Relation: true, Nullable: true},
        {ID: 3, Name: "children", Type: schema.FieldModel, TypeName: "Child", Relation: true, IsList: true},
        {ID: 4, Name: "optionalChildren", Type: schema.FieldModel, TypeName: "Child", Relation: true, IsList: true, Nullable: true},
        {ID: 5, Name: "tail", Type: schema.FieldInt},
    }}
    metadata.RegisterModel(model)
    return metadata, model
}

func TestNestedResponses(t *testing.T) {
    metadata, model := nestedMetadata()
    fields := []*selection.Field{{Name: "tail"}}
    for _, name := range []string{"child", "optional", "children", "optionalChildren"} {
        fields = append(fields, &selection.Field{Name: name, Children: []*selection.Field{{Name: "id"}}})
    }
    mask, err := schema.SelectToFieldMask(fields, model, metadata)
    if err != nil { t.Fatal(err) }
    child := []byte{0, 1}
    child = codec.AppendSvarint(child, math.MaxInt64)
    child = append(child, 0)
    columns := &codec.ColumnarWriter{}
    columns.SetCount(1)
    columns.WriteColumnInt(1, []int64{math.MaxInt64})
    list, err := columns.Bytes()
    if err != nil { t.Fatal(err) }
    for _, state := range []string{"null", "empty", "populated"} {
        t.Run(state, func(t *testing.T) {
            value := Parent{Child: &Child{Id: math.MaxInt64, Label: "unselected"}, Children: []Child{}, Tail: 42}
            optional, optionalList := []byte{0}, []byte{0}
            if state != "null" {
                value.Optional = value.Child
                value.OptionalChildren = []Child{}
                optional = codec.AppendBytes([]byte{1}, child)
                optionalList = codec.AppendBytes([]byte{1}, []byte{0,0,0})
            }
            if state == "populated" {
                value.Children = []Child{*value.Child}
                value.OptionalChildren = value.Children
                optionalList = codec.AppendBytes([]byte{1}, list)
            }
            expected := codec.AppendBytes([]byte{0,1}, child)
            expected = append(append(expected, 2), optional...)
            expected = append(expected, 3)
            if state == "populated" { expected = codec.AppendBytes(expected, list) } else { expected = codec.AppendBytes(expected, []byte{0,0,0}) }
            expected = append(append(expected, 4), optionalList...)
            expected = append(expected, 5,84,0)
            var output api.ResponseBuf
            value.WriteLuxo(&output, mask)
            if !bytes.Equal(output.B, expected) { t.Fatalf("row=%x want=%x", output.B, expected) }
            reader := codec.NewDecoder(expected)
            decoded := Parent{Optional: &Child{}, OptionalChildren: []Child{{}}}
            decoded.ReadLuxo(reader)
            if reader.Err() != nil || reader.Offset() != len(expected) { t.Fatalf("read: %v offset=%d", reader.Err(), reader.Offset()) }
            output.B = output.B[:0]
            decoded.WriteLuxo(&output, mask)
            if !bytes.Equal(output.B, expected) { t.Fatalf("round trip=%x want=%x", output.B, expected) }
            wantJSON := "{\"child\":{\"id\":9223372036854775807},\"optional\":null,\"children\":[],\"optionalChildren\":null,\"tail\":42}"
            if state == "empty" { wantJSON = "{\"child\":{\"id\":9223372036854775807},\"optional\":{\"id\":9223372036854775807},\"children\":[],\"optionalChildren\":[],\"tail\":42}" }
            if state == "populated" { wantJSON = "{\"child\":{\"id\":9223372036854775807},\"optional\":{\"id\":9223372036854775807},\"children\":[{\"id\":9223372036854775807}],\"optionalChildren\":[{\"id\":9223372036854775807}],\"tail\":42}" }
            if got := string(checkedJSON(t)(schema.BinaryToJSON(nil, expected, model, metadata))); got != wantJSON { t.Fatalf("JSON=%s want=%s", got, wantJSON) }
            var columnOutput api.ResponseBuf
            writeParents(&columnOutput, []Parent{value}, mask)
            columnRows, err := ReadColumnarParent(columnOutput.B)
            if err != nil || len(columnRows) != 1 { t.Fatalf("outer columns: %v count=%d", err, len(columnRows)) }
            output.B = output.B[:0]
            columnRows[0].WriteLuxo(&output, mask)
            if !bytes.Equal(output.B, expected) { t.Fatalf("column round trip=%x want=%x", output.B, expected) }
            if got := string(checkedJSON(t)(schema.BinaryListToJSON(nil, columnOutput.B, model, metadata))); got != "["+wantJSON+"]" { t.Fatalf("column JSON=%s", got) }
        })
    }
}
`

const nestedRecursiveAssertions = `
func TestRecursiveResponseFraming(t *testing.T) {
    metadata := schema.New()
    model := &schema.Model{Name: "Node", Fields: []schema.Field{
        {ID: 1, Name: "id", Type: schema.FieldInt},
        {ID: 2, Name: "next", Type: schema.FieldModel, TypeName: "Node", Relation: true, Nullable: true},
    }}
    metadata.RegisterModel(model)
    fields := []*selection.Field{{Name: "id"}, {Name: "next", Children: []*selection.Field{{Name: "id"}}}}
    for range 2 { fields = []*selection.Field{{Name: "id"}, {Name: "next", Children: fields}} }
    mask, err := schema.SelectToFieldMask(fields, model, metadata)
    if err != nil { t.Fatal(err) }
    value := Node{Id: 1, Next: &Node{Id: math.MinInt64, Next: &Node{Id: math.MaxInt64}}}
    var output api.ResponseBuf
    value.WriteLuxo(&output, mask)
    dec := codec.NewDecoder(output.B)
    var decoded Node
    decoded.ReadLuxo(dec)
    if err := dec.Finish(); err != nil { t.Fatal(err) }
    if decoded.Id != 1 || decoded.Next.Id != math.MinInt64 || decoded.Next.Next.Id != math.MaxInt64 || decoded.Next.Next.Next != nil { t.Fatalf("recursive result=%#v", decoded) }
    want := "{\"id\":1,\"next\":{\"id\":-9223372036854775808,\"next\":{\"id\":9223372036854775807,\"next\":null}}}"
    if got := string(checkedJSON(t)(schema.BinaryToJSON(nil, output.B, model, metadata))); got != want { t.Fatalf("JSON=%s", got) }
}
`

const nestedBenchmarks = `
// Frozen generated Child column writer before direct append. No production fallback.
func writeBufferedChildren(buf *api.ResponseBuf, items []Child, mask []byte) error {
    mask = codec.SelectionMaskFields(mask)
    w := &codec.ColumnarWriter{}
    w.SetCount(len(items))
    if len(mask) == 0 || codec.FieldMaskHas(mask, 1) {
        vals := make([]int64, len(items))
        for i, item := range items { vals[i] = item.Id }
        w.WriteColumnInt(1, vals)
    }
    if len(mask) == 0 || codec.FieldMaskHas(mask, 2) {
        vals := make([]string, len(items))
        for i, item := range items { vals[i] = item.Label }
        w.WriteColumnString(2, vals)
    }
    data, err := w.Bytes()
    if err != nil { return err }
    buf.B = append(buf.B, data...)
    return nil
}

func TestNestedWriterAllocations(t *testing.T) {
    metadata, model := nestedMetadata()
    fields := []*selection.Field{{Name: "children", Children: []*selection.Field{{Name: "id"}, {Name: "label"}}}, {Name: "tail"}}
    mask, err := schema.SelectToFieldMask(fields, model, metadata)
    if err != nil { t.Fatal(err) }
    for _, label := range []string{"東京", strings.Repeat("世",64), strings.Repeat("界",6000)} {
      for _, count := range []int{1,16} {
        value := Parent{Children: make([]Child, count), Tail: 42}
        for i := range value.Children { value.Children[i] = Child{Id: math.MaxInt64, Label: label} }
        buf := api.ResponseBuf{B: make([]byte, 0, count*(len(label)+16)+1024)}
        value.writeBufferedBenchmarkBaseline(&buf, mask)
        expected := bytes.Clone(buf.B)
        allocations := testing.AllocsPerRun(100, func() {
            buf.B = buf.B[:0]
            value.WriteLuxo(&buf, mask)
        })
        if !bytes.Equal(buf.B, expected) { t.Fatalf("direct output differs from buffered output for %d rows", count) }
        if allocations != 0 { t.Fatalf("%d rows: %g intermediate allocations", count, allocations) }
      }
    }
}

func BenchmarkNestedResponse(b *testing.B) {
    metadata, model := nestedMetadata()
    for _, list := range []bool{false, true} {
        field := "child"
        if list { field = "children" }
        fields := []*selection.Field{{Name: field, Children: []*selection.Field{{Name: "id"}, {Name: "label"}}}, {Name: "tail"}}
        mask, err := schema.SelectToFieldMask(fields, model, metadata)
        if err != nil { b.Fatal(err) }
        for _, count := range []int{1,16} {
            if !list && count != 1 { continue }
            value := Parent{Child: &Child{Id: math.MaxInt64, Label: "東京"}, Children: make([]Child, count), Tail: 42}
            for i := range value.Children { value.Children[i] = *value.Child }
            for _, implementation := range []string{"inline", "buffered", "direct"} {
                b.Run(fmt.Sprintf("%s/%d/%s", field, count, implementation), func(b *testing.B) {
                    buf := api.ResponseBuf{B: make([]byte, 0, 16384)}
                    b.ReportAllocs()
                    for b.Loop() {
                        buf.B = buf.B[:0]
                        switch implementation {
                        case "inline": value.writeInlineBenchmarkBaseline(&buf, mask)
                        case "buffered": value.writeBufferedBenchmarkBaseline(&buf, mask)
                        default: value.WriteLuxo(&buf, mask)
                        }
                    }
                })
            }
        }
    }
}
`
