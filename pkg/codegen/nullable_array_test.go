package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// Execute generated readers and both model/type writers, not only source assertions.
func TestGeneratedNullableArrayProtocol(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			model := &ast.ModelDecl{Name: "Payload", Fields: []*ast.FieldDecl{
				{Name: "values", Type: &ast.TypeRef{Name: "Int", IsList: true, Nullable: true}},
			}}
			generator := generatorWithModelFieldIDs(map[string]map[string]int{"Payload": {"values": 1}, "PlainPayload": {"values": 1}})
			var source strings.Builder
			source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
			writeArrayFixtureDeclaration(&source, generator, model, nil, declaration)
			plain := &ast.ModelDecl{Name: "PlainPayload", Fields: []*ast.FieldDecl{
				{Name: "values", Type: &ast.TypeRef{Name: "Int", IsList: true}},
			}}
			writeArrayFixtureDeclaration(&source, generator, plain, nil, declaration)
			runGeneratedProtocolPackage(t, source.String(), nullableArrayAssertions)
		})
	}
}

func TestGeneratedJSONListPayloadPreflight(t *testing.T) {
	model := &ast.ModelDecl{Name: "Payload", Fields: []*ast.FieldDecl{
		{Name: "values", Type: &ast.TypeRef{Name: "JSON", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Payload": {"values": 1}})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"encoding/json\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateModel(&source, model, nil)
	g.generateReadLuxo(&source, model, nil)
	source.WriteString(jsonListPayloadBaseline)
	runGeneratedProtocolPackage(t, source.String(), jsonListPayloadAssertions)
}

const jsonListPayloadBaseline = `
// Frozen generated JSON reader before payload preflight.
func (p *Payload) readBeforePayload(dec *codec.Decoder) {
    dec.SkipArenaHeader()
    for dec.NextField() {
        switch dec.FieldID() {
        case 1:
            n := dec.ReadArrayLength()
            p.Values = make([]json.RawMessage, n)
            for i := range p.Values { p.Values[i] = dec.ReadBytes() }
        default: dec.SkipField()
        }
    }
}
`

const jsonListPayloadAssertions = `package generated
import ("fmt"; "testing"; "github.com/light-speak/luxo/pkg/lux/codec")
func TestJSONListPayload(t *testing.T) {
    for _, input := range []bool{false, true} {
        raw := codec.AppendArrayHeader([]byte{0, 1}, 32)
        raw = append(raw, 0)
        dec := codec.NewDecoder(raw)
        var value Payload
        if input { value.ReadLuxoInput(dec) } else { value.ReadLuxo(dec) }
        if dec.Err() == nil || value.Values != nil { t.Fatalf("short list allocated: %d, %v", len(value.Values), dec.Err()) }
    }
    raw := codec.AppendBytes([]byte{0, 1, 1}, []byte("9223372036854775807"))
    raw = append(raw, 0)
    dec := codec.NewDecoder(raw)
    var value Payload
    value.ReadLuxo(dec)
    if err := dec.Finish(); err != nil || len(value.Values) != 1 || string(value.Values[0]) != "9223372036854775807" { t.Fatalf("valid JSON changed: %v, %v", value.Values, err) }
}
func BenchmarkPayloadJSONList(b *testing.B) {
    for _, count := range []int{0, 1, 128} {
        data := codec.AppendArrayHeader([]byte{0, 1}, count)
        for range count { data = codec.AppendBytes(data, []byte("9223372036854775807")) }
        data = append(data, 0)
        for _, before := range []bool{true, false} {
            b.Run(fmt.Sprintf("count=%d/before=%v", count, before), func(b *testing.B) {
                var value Payload
                b.ReportAllocs()
                for b.Loop() {
                    dec := codec.NewDecoder(data)
                    if before { value.readBeforePayload(dec) } else { value.ReadLuxo(dec) }
                    if err := dec.Finish(); err != nil || len(value.Values) != count { b.Fatal("invalid benchmark result") }
                }
            })
        }
    }
}
`

func runGeneratedProtocolPackage(t *testing.T, source, assertions string, legacyReference ...bool) {
	t.Helper()
	dir := writeProtocolFixture(t, source, assertions)
	args, benchmark := protocolTestArgs()
	natsAdapter := strings.Contains(assertions, "github.com/light-speak/luxo/pkg/lux/nats/event")
	if natsAdapter {
		writeAdapterFixtureModule(t, dir, "pkg/lux/nats")
		args = append(args[:1], append([]string{"-mod=mod"}, args[1:]...)...)
	}
	if len(legacyReference) > 0 && legacyReference[0] {
		args = append([]string{"test", "-overlay=" + legacyRequestBenchmarkOverlay(t, dir)}, args[1:]...)
	}
	command := exec.Command("go", args...)
	command.Dir = dir
	if natsAdapter {
		command.Env = append(os.Environ(), "GOWORK=off")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated protocol execution: %v\n%s", err, output)
	}
	if benchmark {
		t.Logf("%s", output)
	}
}

func writeProtocolFixture(t *testing.T, source, assertions string) string {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "generated-protocol-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	for name, data := range map[string]string{"model.gen.go": source, "wire_test.go": assertions, "helpers_test.go": protocolJSONAssertions} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func protocolTestArgs() ([]string, bool) {
	args := []string{"test", "-count=1", "-timeout=30s", "."}
	benchmark := false
	for _, option := range []struct{ env, name, duration, count string }{
		{"LUXO_ARRAY_BENCHMARK", "BenchmarkArray", "200ms", "3"},
		{"LUXO_SCANNER_BENCHMARK", "BenchmarkScannerDiscard", "200ms", "1"},
		{"LUXO_NESTED_BENCHMARK", "BenchmarkNested", "200ms", "3"},
		{"LUXO_FLOAT_BENCHMARK", "BenchmarkGeneratedFloat", "500ms", "1"},
		{"LUXO_PAYLOAD_BENCHMARK", "BenchmarkPayload", "300ms", "1"},
		{"LUXO_STRUCTURED_PARAM_BENCHMARK", "BenchmarkStructuredTransport", "200ms", "1"},
		{"LUXO_ENUM_BENCHMARK", "BenchmarkEnum", "200ms", "1"},
		{"LUXO_CACHE_BENCHMARK", "BenchmarkCacheBoundary", "500ms", "1"},
		{"LUXO_LOADER_BINDING_BENCHMARK", "BenchmarkLoaderBinding", "300ms", "1"},
		{"LUXO_EVENT_BENCHMARK", "BenchmarkEventNullable", "300ms", "1"},
		{"LUXO_EVENT_SNAPSHOT_BENCHMARK", "BenchmarkEventSnapshot", "300ms", "1"},
		{"LUXO_JSON_INPUT_BENCHMARK", "BenchmarkJSONInput", "200ms", "1"},
		{"LUXO_JSON_LOCAL_BENCHMARK", "BenchmarkLocalJSONTransport", "200ms", "1"},
		{"LUXO_CRUD_INPUT_BENCHMARK", "BenchmarkMutationInput", "200ms", "1"},
		{"LUXO_STREAM_PREPARE_BENCHMARK", "BenchmarkStreamPreparationLifecycle", "300ms", "1"},
	} {
		if os.Getenv(option.env) == "1" {
			args = []string{"test", "-run=^$", "-bench=" + option.name, "-benchtime=" + option.duration, "-benchmem", "-count=" + option.count, "."}
			benchmark = true
		}
	}
	return args, benchmark
}

func legacyRequestBenchmarkOverlay(t *testing.T, dir string) string {
	t.Helper()
	if strings.Contains(os.Getenv("GOFLAGS"), "-overlay") {
		t.Fatal("historical request benchmark overlay cannot replace another GOFLAGS overlay")
	}
	apiDir, err := filepath.Abs("../lux/api")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(apiDir, "request_legacy_benchmark.go")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("benchmark overlay target must not exist: %s (%v)", target, err)
	}
	snapshot := filepath.Join(apiDir, "request_legacy_reference_test.go")
	data, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{target: snapshot}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy-overlay.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func structuredParamBenchmarkEnabled() bool {
	return os.Getenv("LUXO_STRUCTURED_PARAM_BENCHMARK") == "1"
}

const protocolJSONAssertions = `package generated
import "testing"
func checkedJSON(t testing.TB) func([]byte, error) []byte {
    t.Helper()
    return func(data []byte, err error) []byte {
        t.Helper()
        if err != nil { t.Fatal(err) }
        return data
    }
}
`

func writeArrayFixtureDeclaration(source *strings.Builder, g *GeneratorContext, model *ast.ModelDecl, enums map[string]bool, declaration string) {
	generateModel(source, model, enums)
	if declaration == "model" {
		g.generateWriteLuxo(source, model, enums)
		g.generateModelWriteColumnar(source, model, enums, "WriteColumnar"+model.Name, "[]"+model.Name)
	} else {
		g.generateTypeWriteLuxo(source, model, enums)
		g.generateTypeWriteColumnar(source, model, enums)
	}
	g.generateReadLuxo(source, model, enums)
	g.generateReadColumnar(source, model, enums)
}

func TestGeneratedNullableScalarArrays(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			source, assertions := nullableScalarSources(declaration)
			runGeneratedProtocolPackage(t, source, assertions)
		})
	}
}

func nullableScalarSources(declaration string) (string, string) {
	fixtures := []struct{ name, values, wire, json string }{
		{"Int", "[]int64{-1, 1}", "[]byte{2, 1, 2}", "[-1,1]"},
		{"Float", "[]float64{1.5}", "[]byte{1, 0, 0, 0, 0, 0, 0, 248, 63}", "[1.5]"},
		{"String", `[]string{"hello", "世界"}`, `[]byte("\x02\x05hello\x06世界")`, `["hello","世界"]`},
		{"Boolean", "[]bool{true, false}", "[]byte{2, 1, 0}", "[true,false]"},
		{"DateTime", "[]time.Time{time.Unix(0, 1).UTC(), time.Unix(-1, 999999999).UTC()}", "[]byte{2, 0, 1, 1, 255, 147, 235, 220, 3}", `["1970-01-01T00:00:00.000000001Z","1969-12-31T23:59:59.999999999Z"]`},
		{"Duration", "[]time.Duration{-1, 1}", "[]byte{2, 1, 2}", "[-1,1]"},
		{"UUID", "[]uuid.UUID{{}}", "make([]byte, 17)", `["00000000-0000-0000-0000-000000000000"]`},
		{"Decimal", `[]decimal.Decimal{decimal.RequireFromString("1.25")}`, `[]byte("\x01\x041.25")`, `["1.25"]`},
		{"Bytes", "[][]byte{{0, 255}, {}}", "[]byte{2, 2, 0, 255, 0}", `["AP8=",""]`},
		{"JSON", `[]json.RawMessage{json.RawMessage("{\"id\":9223372036854775807}"), json.RawMessage("null")}`, `[]byte("\x02\x1a{\"id\":9223372036854775807}\x04null")`, `[{"id":9223372036854775807},null]`},
		{"ArrayState", `[]ArrayState{"open", "closed"}`, `[]byte("\x02\x04open\x06closed")`, `["open","closed"]`},
	}
	enums := map[string]bool{"ArrayState": true}
	ids := make(map[string]map[string]int)
	var models []*ast.ModelDecl
	for _, fixture := range fixtures {
		name := "Array" + fixture.name
		ids[name] = map[string]int{"values": 1, "tail": 2}
		models = append(models, &ast.ModelDecl{Name: name, Fields: []*ast.FieldDecl{
			{Name: "values", Type: &ast.TypeRef{Name: fixture.name, IsList: true, Nullable: true}},
			{Name: "tail", Type: &ast.TypeRef{Name: "Int"}},
		}})
	}
	g := generatorWithModelFieldIDs(ids)
	var source, assertions strings.Builder
	source.WriteString("package generated\n")
	writeWriteJSONImports(&source, models, 0)
	generateEnum(&source, &ast.EnumDecl{Name: "ArrayState", Values: []string{"open", "closed"}})
	assertions.WriteString(nullableScalarTestImports)
	for i, model := range models {
		writeArrayFixtureDeclaration(&source, g, model, enums, declaration)
		f := fixtures[i]
		fieldType := strings.NewReplacer("Boolean", "Bool", "ArrayState", "Enum").Replace(f.name)
		assertions.WriteString(strings.NewReplacer("$MODEL", model.Name, "$VALUES", f.values, "$WIRE", f.wire,
			"$JSON", fmt.Sprintf("%q", f.json), "$FIELD", fieldType).Replace(nullableScalarTestBody))
	}
	return source.String(), assertions.String()
}

const nullableScalarTestImports = `package generated
import (
    "bytes"
    "encoding/json"
    "testing"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/schema"
)
`

const nullableScalarTestBody = `
func Test$MODEL(t *testing.T) {
    values := $VALUES
    populated := $WIRE
    populated[0] = byte(len(values))
    metadata := schema.New()
    model := &schema.Model{Name: "$MODEL", Fields: []schema.Field{
        {ID: 1, Name: "values", Type: schema.Field$FIELD, IsList: true, Nullable: true},
        {ID: 2, Name: "tail", Type: schema.FieldInt},
    }}
    metadata.RegisterModel(model)
    for _, state := range []string{"null", "empty", "populated", "unselected"} {
        t.Run(state, func(t *testing.T) {
            payload := $MODEL{Values: values, Tail: 42}
            cell, valueJSON := populated, $JSON
            switch state {
            case "null": payload.Values, cell, valueJSON = nil, nil, "null"
            case "empty": payload.Values, cell, valueJSON = values[:0], []byte{0}, "[]"
            }
            row, column := []byte{0, 1}, []byte{1, 0, 1}
            flag := byte(1)
            if payload.Values == nil { flag = 0 }
            row = append(append(row, flag), cell...)
            column = append(column, flag)
            if flag != 0 { column = append(append(column, byte(len(cell))), cell...) }
            mask := []byte{1, 3}
            wantJSON := "{\"values\":" + valueJSON + ",\"tail\":42}"
            if state == "unselected" { row, column, mask, wantJSON = []byte{0}, []byte{1,0}, []byte{1,2}, "{\"tail\":42}" }
            row, column = append(row, 2, 84, 0), append(column, 2, 84, 0)
            var encoded api.ResponseBuf
            payload.WriteLuxo(&encoded, mask)
            if !bytes.Equal(encoded.B, row) { t.Fatalf("row=%x want=%x", encoded.B, row) }
            encoded.B = encoded.B[:0]
            WriteColumnar$MODEL(&encoded, []$MODEL{payload}, mask)
            if !bytes.Equal(encoded.B, column) { t.Fatalf("column=%x want=%x", encoded.B, column) }
            columnRows, columnErr := ReadColumnar$MODEL(column)
            if columnErr != nil || len(columnRows) != 1 { t.Fatalf("column read: %v rows=%d", columnErr, len(columnRows)) }
            encoded.B = encoded.B[:0]
            columnRows[0].WriteLuxo(&encoded, mask)
            if !bytes.Equal(encoded.B, row) { t.Fatalf("column read row=%x want=%x", encoded.B, row) }
            decoded := $MODEL{Values: values}
            reader := codec.NewDecoder(row)
            decoded.ReadLuxo(reader)
            if reader.Err() != nil || decoded.Tail != 42 { t.Fatalf("reader error=%v tail=%d", reader.Err(), decoded.Tail) }
            if state == "null" && decoded.Values != nil { t.Fatal("null did not reset reused field") }
            if state == "empty" && decoded.Values == nil { t.Fatal("empty became null") }
            if state == "unselected" && len(decoded.Values) != len(values) { t.Fatal("unselected field was modified") }
            encoded.B = encoded.B[:0]
            decoded.WriteLuxo(&encoded, mask)
            if !bytes.Equal(encoded.B, row) { t.Fatalf("decoded row=%x want=%x", encoded.B, row) }
            if got := string(checkedJSON(t)(schema.BinaryToJSON(nil, row, model, metadata))); got != wantJSON { t.Errorf("JSON=%s want=%s", got, wantJSON) }
            if got := string(checkedJSON(t)(schema.BinaryListToJSON(nil, column, model, metadata))); got != "["+wantJSON+"]" { t.Errorf("column JSON=%s want=[%s]", got, wantJSON) }
        })
    }
}
`

const nullableArrayAssertions = `package generated
import (
    "bytes"
    "fmt"
    "math"
    "slices"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/schema"
)

func TestNullableArrayWire(t *testing.T) {
    integers := append([]byte{2}, bytes.Repeat([]byte{0xff}, 9)...)
    integers = append(integers, 1, 0xfe)
    integers = append(integers, bytes.Repeat([]byte{0xff}, 8)...)
    integers = append(integers, 1)
    cases := []struct { name string; values []int64; cell []byte; json string }{
        {"null", nil, nil, "null"},
        {"empty", []int64{}, []byte{0}, "[]"},
        {"full-width", []int64{math.MinInt64, math.MaxInt64}, integers, "[-9223372036854775808,9223372036854775807]"},
    }
    metadata := schema.New()
    model := &schema.Model{Name: "Payload", Fields: []schema.Field{{ID: 1, Name: "values", Type: schema.FieldInt, IsList: true, Nullable: true}}}
    metadata.RegisterModel(model)
    var items []Payload
    column := []byte{3, 0, 1}
    for _, test := range cases {
        payload := Payload{Values: test.values}
        items = append(items, payload)
        flag := byte(1)
        if test.values == nil { flag = 0 }
        row := append([]byte{0, 1, flag}, test.cell...)
        row = append(row, 0)
        column = append(column, flag)
        if flag == 1 { column = append(column, byte(len(test.cell))); column = append(column, test.cell...) }
        t.Run(test.name, func(t *testing.T) {
            var output api.ResponseBuf
            payload.WriteLuxo(&output, []byte{1,1})
            if !bytes.Equal(output.B, row) { t.Errorf("row=%x want=%x", output.B, row) }
            for _, initial := range [][]int64{nil, {42}} {
                decoded := Payload{Values: initial}
                reader := codec.NewDecoder(row)
                decoded.ReadLuxo(reader)
                if reader.Err() != nil || (decoded.Values == nil) != (test.values == nil) || !slices.Equal(decoded.Values, test.values) {
                    t.Errorf("decoded=%v nil=%v error=%v", decoded.Values, decoded.Values == nil, reader.Err())
                }
            }
            wantJSON := "{\"values\":" + test.json + "}"
            if got := string(checkedJSON(t)(schema.BinaryToJSON(nil, row, model, metadata))); got != wantJSON { t.Errorf("JSON=%s want=%s", got, wantJSON) }
            canonical, err := metadata.JSONToBinary("Payload", []byte(wantJSON))
            if err != nil || !bytes.Equal(canonical, row) { t.Errorf("JSON input=%x error=%v want=%x", canonical, err, row) }
        })
    }
    column = append(column, 0)
    var output api.ResponseBuf
    WriteColumnarPayload(&output, items, []byte{1,1})
    if !bytes.Equal(output.B, column) { t.Errorf("column=%x want=%x", output.B, column) }
    output.B = output.B[:0]
    if err := writePointerArrayColumn(&output, items, []byte{1,1}); err != nil { t.Fatal(err) }
    if !bytes.Equal(output.B, column) { t.Errorf("pointer baseline=%x want=%x", output.B, column) }
    wantJSON := "[{\"values\":null},{\"values\":[]},{\"values\":[-9223372036854775808,9223372036854775807]}]"
    if got := string(checkedJSON(t)(schema.BinaryListToJSON(nil, column, model, metadata))); got != wantJSON { t.Errorf("column JSON=%s want=%s", got, wantJSON) }
    if got := string(checkedJSON(t)(schema.BinaryToJSON(nil, []byte{0,0}, model, metadata))); got != "{}" { t.Errorf("unselected field=%s", got) }
}

// This compares two valid schema shapes, not an old broken nullable encoding.
func BenchmarkArray(b *testing.B) {
    for _, count := range []int{0, 16} {
        values := make([]int64, count)
        for i := range values { values[i] = int64(i) }
        plain, nullable := PlainPayload{Values: values}, Payload{Values: values}
        mask := []byte{1,1}
        b.Run(fmt.Sprintf("%d/required/row", count), func(b *testing.B) {
            buf := api.ResponseBuf{B: make([]byte, 0, 256)}
            b.ReportAllocs()
            for b.Loop() { buf.B = buf.B[:0]; plain.WriteLuxo(&buf, mask) }
        })
        b.Run(fmt.Sprintf("%d/nullable/row", count), func(b *testing.B) {
            buf := api.ResponseBuf{B: make([]byte, 0, 256)}
            b.ReportAllocs()
            for b.Loop() { buf.B = buf.B[:0]; nullable.WriteLuxo(&buf, mask) }
        })
        b.Run(fmt.Sprintf("%d/required/column32", count), func(b *testing.B) {
            buf := api.ResponseBuf{B: make([]byte, 0, 4096)}
            items := make([]PlainPayload, 32)
            for i := range items { items[i] = plain }
            b.ReportAllocs()
            for b.Loop() { buf.B = buf.B[:0]; WriteColumnarPlainPayload(&buf, items, mask) }
        })
        b.Run(fmt.Sprintf("%d/nullable/column32", count), func(b *testing.B) {
            buf := api.ResponseBuf{B: make([]byte, 0, 4096)}
            items := make([]Payload, 32)
            for i := range items { items[i] = nullable }
            b.ReportAllocs()
            for b.Loop() { buf.B = buf.B[:0]; WriteColumnarPayload(&buf, items, mask) }
        })
        b.Run(fmt.Sprintf("%d/pointer-baseline/column32", count), func(b *testing.B) {
            buf := api.ResponseBuf{B: make([]byte, 0, 4096)}
            items := make([]Payload, 32)
            for i := range items { items[i] = nullable }
            b.ReportAllocs()
            for b.Loop() {
                buf.B = buf.B[:0]
                if err := writePointerArrayColumn(&buf, items, mask); err != nil { b.Fatal(err) }
            }
        })
    }
}

// Frozen first-correct implementation, not a released-version regression baseline.
// It produces the same nullable wire but materializes a second pointer slice.
func writePointerArrayColumn(buf *api.ResponseBuf, items []Payload, mask []byte) error {
    mask = codec.SelectionMaskFields(mask)
    w := &codec.ColumnarWriter{}
    w.SetCount(len(items))
    if len(mask) == 0 || codec.FieldMaskHas(mask, 1) {
        cells := make([][]byte, len(items))
        vals := make([]*[]byte, len(items))
        for i, item := range items {
            values := item.Values
            if values == nil { continue }
            var cb []byte
            cb = codec.AppendArrayHeader(cb, len(values))
            for _, v := range values { cb = codec.AppendSvarint(cb, v) }
            cells[i] = cb
            vals[i] = &cells[i]
        }
        w.WriteColumnBytesPtr(1, vals)
    }
    data, err := w.Bytes()
    if err != nil { return err }
    buf.B = append(buf.B, data...)
    return nil
}
`
