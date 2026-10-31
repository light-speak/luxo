package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedColumnarRequiresReturnedField(t *testing.T) {
	model := &ast.ModelDecl{Name: "Projection", Fields: []*ast.FieldDecl{{
		Name: "value", Type: &ast.TypeRef{Name: "String", Nullable: true},
		Directives: []*ast.Directive{{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.Ident{Name: "allowColumn"}},
		}}}},
	}}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Projection": {"value": 1}})
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			writer := "WriteColumnarProjection"
			if declaration == "model" {
				writer += "Values"
			}
			source := columnarFixtureSource(g, []*ast.ModelDecl{model}, declaration, nil)
			// Identical generated writer with only the new projection check removed.
			var baseline strings.Builder
			g.generateDirectColumnar(&baseline, model, nil, writer+"BeforeProjection", "[]Projection", declaration == "model", nil)
			source += strings.NewReplacer(
				"\t_columnStart := len(buf.B)\n", "",
				"\tif len(buf.B) == _columnStart { return codec.ErrEmptyColumnarProjection }\n", "",
			).Replace(baseline.String())
			runGeneratedProtocolPackage(t, source, strings.ReplaceAll(columnarProjectionAssertions+columnarProjectionBenchmark, "WRITE", writer))
		})
	}
}

const columnarProjectionAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

    "bytes"
    "fmt"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
var allowColumn bool
func TestFinalColumnProjection(t *testing.T) {
    items := []Projection{{}, {}}
    for _, allowed := range []bool{false, true} {
        allowColumn = allowed
        for _, id := range []int{1, 2} {
            mask := testutil.Bytes(t)(codec.AppendSelectionMask(nil, testutil.Bytes(t)(codec.FieldMaskSet(nil,id)), nil))
            var buf api.ResponseBuf
            err := WRITE(&buf, items, mask)
            if !allowed || id != 1 {
                if err == nil { t.Fatalf("empty projection succeeded: visible=%v field=%d body=%x", allowed,id,buf.B) }
            } else {
                if err != nil { t.Fatal(err) }
                if !bytes.Equal(buf.B, []byte{2,0,1,0,0,0}) { t.Fatalf("null cells lost: %x",buf.B) }
            }
            buf.Reset()
            if err := WRITE(&buf, nil, mask); err != nil || !bytes.Equal(buf.B, []byte{0,0,0}) {
                t.Fatalf("empty list changed: %x / %v",buf.B,err)
            }
        }
    }
}
func TestReadProjectionFailsBeforeAllocatingRows(t *testing.T) {
    data := append(codec.AppendVarint(nil, 1_000_000), 0, 0)
    allocations := testing.AllocsPerRun(10, func() {
        items, err := ReadColumnarProjection(data)
        if err == nil || items != nil { t.Fatal("invalid empty projection returned rows") }
    })
    if allocations != 0 { t.Fatalf("invalid rows allocated: %v",allocations) }
}
`

const columnarProjectionBenchmark = `
func TestProjectionWriterParityAndAllocations(t *testing.T) {
    allowColumn = true
    text := "value"
    items := []Projection{{Value:&text},{}}
    mask := testutil.Bytes(t)(codec.AppendSelectionMask(nil, []byte{1}, nil))
    baseline := api.ResponseBuf{B:make([]byte,0,4096)}
    checked := api.ResponseBuf{B:make([]byte,0,4096)}
    if err := WRITEBeforeProjection(&baseline,items,mask); err != nil { t.Fatal(err) }
    allocations := testing.AllocsPerRun(100,func() {
        checked.Reset()
        if err := WRITE(&checked,items,mask); err != nil { t.Fatal(err) }
    })
    if allocations != 0 || !bytes.Equal(baseline.B,checked.B) { t.Fatalf("wire or allocations changed: %x / %x / %g",baseline.B,checked.B,allocations) }
}
func BenchmarkPayloadColumnarProjection(b *testing.B) {
    allowColumn = true
    for _, count := range []int{0,1,128} {
        items := make([]Projection,count)
        text := "value"
        for i := range items { items[i].Value = &text }
        mask := testutil.Bytes(b)(codec.AppendSelectionMask(nil, []byte{1}, nil))
        for _, before := range []bool{true,false} {
            b.Run(fmt.Sprintf("count=%d/before=%t",count,before),func(b *testing.B) {
                buf := api.ResponseBuf{B:make([]byte,0,4096)}
                b.ReportAllocs()
                for b.Loop() {
                    buf.Reset()
                    var err error
                    if before { err = WRITEBeforeProjection(&buf,items,mask) } else { err = WRITE(&buf,items,mask) }
                    if err != nil { b.Fatal(err) }
                }
            })
        }
    }
}
`

func TestGeneratedColumnarConstants(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			fields := []*ast.FieldDecl{
				{Name: "number", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "fraction", Type: &ast.TypeRef{Name: "Float"}},
				{Name: "optionalNumber", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
				{Name: "optionalFraction", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
			}
			for _, field := range fields {
				field.Directives = []*ast.Directive{{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "3"}},
				}}}}
			}
			model := &ast.ModelDecl{Name: "Constant", Fields: fields}
			g := generatorWithModelFieldIDs(map[string]map[string]int{"Constant": {"number": 1, "fraction": 2, "optionalNumber": 3, "optionalFraction": 4}})
			source := columnarFixtureSource(g, []*ast.ModelDecl{model}, declaration, nil)
			runGeneratedProtocolPackage(t, source, columnarConstantAssertions)
		})
	}
}

const columnarConstantAssertions = `package generated
import (
    "bytes"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
func TestConstants(t *testing.T) {
    var buf api.ResponseBuf
    number, fraction := int64(99), 1.5
    items := []Constant{{Number:99, Fraction:1.5}, {OptionalNumber:&number, OptionalFraction:&fraction}}
    writeFixture(&buf, items, nil)
    reference := &codec.ColumnarWriter{}
    reference.SetCount(2)
    reference.WriteColumnInt(1, []int64{3,3})
    reference.WriteColumnFloat(2, []float64{3,3})
    expectedNumber, expectedFraction := int64(3), float64(3)
    reference.WriteColumnIntPtr(3, []*int64{nil,&expectedNumber})
    reference.WriteColumnFloatPtr(4, []*float64{nil,&expectedFraction})
    expected, err := reference.Bytes()
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(buf.B, expected) { t.Fatalf("constants: %x", buf.B) }
    for i := range items {
        buf.Reset()
        items[i].WriteLuxo(&buf, nil)
        var decoded Constant
        dec := codec.NewDecoder(buf.B)
        decoded.ReadLuxo(dec)
        if err := dec.Finish(); err != nil { t.Fatal(err) }
        if decoded.Number != 3 || decoded.Fraction != 3 { t.Fatalf("row constants: %#v", decoded) }
        if i == 0 && (decoded.OptionalNumber != nil || decoded.OptionalFraction != nil) { t.Fatal("null changed") }
        if i == 1 && (*decoded.OptionalNumber != 3 || *decoded.OptionalFraction != 3) { t.Fatal("nullable constants changed") }
    }
}
`

type columnarScalarFixture struct {
	name, value, wireType, wireValue, column string
}

func columnarScalarFixtures() []columnarScalarFixture {
	return []columnarScalarFixture{
		{"Int", "int64(math.MaxInt64)", "int64", "value", "Int"},
		{"Float", "1.25", "float64", "value", "Float"},
		{"String", `strings.Repeat("世", 55)`, "string", "value", "String"},
		{"Boolean", "true", "bool", "value", "Bool"},
		{"DateTime", "time.Unix(-1, 999999999).UTC()", "time.Time", "value", "DateTime"},
		{"Duration", "time.Duration(math.MinInt64)", "int64", "int64(value)", "Int"},
		{"UUID", "uuid.UUID{0: 1, 15: 255}", "[16]byte", "[16]byte(value)", "UUID"},
		{"Decimal", `decimal.RequireFromString("12345678901234567890.123456789")`, "string", "value.String()", "String"},
		{"Bytes", "[]byte{0,255}", "[]byte", "value", "Bytes"},
		{"JSON", `json.RawMessage("{\"int\":9223372036854775807}")`, "[]byte", "[]byte(value)", "Bytes"},
		{"State", `State("ready")`, "string", "string(value)", "String"},
	}
}

func TestGeneratedColumnarScalarParity(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			source, assertions := columnarScalarSources(declaration)
			runGeneratedProtocolPackage(t, source, assertions)
		})
	}
}

func columnarScalarSources(declaration string) (string, string) {
	model := &ast.ModelDecl{Name: "Scalars"}
	ids := map[string]int{}
	var assertions strings.Builder
	assertions.WriteString(columnarScalarAssertions)
	for i, fixture := range columnarScalarFixtures() {
		for j, name := range []string{fmt.Sprintf("value%d", i), fmt.Sprintf("optional%d", i)} {
			model.Fields = append(model.Fields, &ast.FieldDecl{Name: name, Type: &ast.TypeRef{Name: fixture.name, Nullable: j == 1}})
			ids[name] = 2*i + j + 1
		}
		writeColumnarScalarFixture(&assertions, fixture, i)
	}
	assertions.WriteString(columnarScalarChecks)
	for i, fixture := range columnarScalarFixtures() {
		fmt.Fprintf(&assertions, `
func TestReusedNull%d(t *testing.T) {
    value := %s
    item := Scalars{Optional%d: &value, Value0:42}
    dec := codec.NewDecoder([]byte{0,%d,0,0})
    item.ReadLuxo(dec)
    if err := dec.Finish(); err != nil { t.Fatal(err) }
    if item.Optional%d != nil { t.Fatal("null kept the old %s value") }
    if item.Value0 != 42 { t.Fatal("unselected field changed") }
}
`, i, fixture.value, i, 2*i+2, i, fixture.name)
	}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Scalars": ids})
	var source strings.Builder
	source.WriteString(columnarFixtureSource(g, []*ast.ModelDecl{model}, declaration, map[string]bool{"State": true}))
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"ready"}})
	return source.String(), assertions.String()
}

func writeColumnarScalarFixture(b *strings.Builder, fixture columnarScalarFixture, index int) {
	fmt.Fprintf(b, "{ value := %s\nwire := %s\n", fixture.value, fixture.wireValue)
	fmt.Fprintf(b, "for i := range items { items[i].Value%d = value }; items[1].Optional%d = &value\n", index, index)
	fmt.Fprintf(b, "reference.WriteColumn%s(%d, []%s{wire,wire})\n", fixture.column, 2*index+1, fixture.wireType)
	fmt.Fprintf(b, "reference.WriteColumn%sPtr(%d, []*%s{nil,&wire})\n}\n", fixture.column, 2*index+2, fixture.wireType)
}

const columnarScalarAssertions = `package generated
import (
    "bytes"
    "encoding/json"
    "math"
    "strings"
    "testing"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
func TestScalarParity(t *testing.T) {
    items := make([]Scalars, 2)
    reference := &codec.ColumnarWriter{}
    reference.SetCount(len(items))
`

const columnarScalarChecks = `
    for _, capacity := range []int{0,4096} {
        buf := api.ResponseBuf{B: make([]byte,0,capacity)}
        writeFixture(&buf, items, nil)
        expected, err := reference.Bytes()
        if err != nil { t.Fatal(err) }
        if !bytes.Equal(buf.B, expected) { t.Fatalf("capacity=%d: %x != %x", capacity, buf.B, expected) }
        decoded, err := ReadColumnarScalars(buf.B)
        if err != nil { t.Fatal(err) }
        var roundTrip api.ResponseBuf
        writeFixture(&roundTrip, decoded, nil)
        if !bytes.Equal(roundTrip.B, buf.B) { t.Fatal("scalar round trip mismatch") }
    }
    // Partial selection excludes every string-bearing column and its arena bytes.
    mask := []byte{1,3}
    reference.Reset()
    reference.SetCount(2)
    reference.WriteColumnInt(1, []int64{math.MaxInt64,math.MaxInt64})
    value := int64(math.MaxInt64)
    reference.WriteColumnIntPtr(2, []*int64{nil,&value})
    var buf api.ResponseBuf
    writeFixture(&buf, items, mask)
    expected, err := reference.Bytes()
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(buf.B, expected) { t.Fatalf("partial: %x", buf.B) }
    buf.Reset()
    writeFixture(&buf, nil, nil)
    if !bytes.Equal(buf.B, []byte{0,0,0}) { t.Fatalf("empty: %x", buf.B) }
}
`

func TestGeneratedColumnarDirectives(t *testing.T) {
	for _, declaration := range []string{"model", "type"} {
		t.Run(declaration, func(t *testing.T) {
			runGeneratedProtocolPackage(t, columnarDirectiveSource(declaration), columnarDirectiveAssertions)
		})
	}
}

func columnarDirectiveModels() []*ast.ModelDecl {
	transform := &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "transformName"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "it"}}}}},
	}}}
	visible := &ast.Directive{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.BinaryExpr{Left: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"}, Op: "==", Right: &ast.Literal{Kind: token.String, Value: "admin"}}},
	}}}
	return []*ast.ModelDecl{
		{Name: "PrivateChild", Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{transform, {Name: "mask"}}},
			{Name: "secret", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{transform, visible, {Name: "mask"}}},
			{Name: "hidden", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
			{Name: "internal", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
			{Name: "noID", Type: &ast.TypeRef{Name: "String"}},
		}},
		{Name: "PrivateParent", Fields: []*ast.FieldDecl{{Name: "children", Type: &ast.TypeRef{Name: "PrivateChild", IsList: true}}}},
	}
}

func columnarDirectiveSource(declaration string) string {
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"PrivateChild": {"name": 1, "secret": 2, "hidden": 3, "internal": 4}, "PrivateParent": {"children": 1},
	})
	return columnarFixtureSource(g, columnarDirectiveModels(), declaration, nil)
}

func columnarFixtureSource(g *GeneratorContext, models []*ast.ModelDecl, declaration string, enums map[string]bool) string {
	file := &ast.File{}
	var structs strings.Builder
	for _, model := range models {
		generateModel(&structs, model, enums)
		if declaration == "model" {
			file.Models = append(file.Models, model)
		} else {
			file.Types = append(file.Types, &ast.TypeDecl{Name: model.Name, Fields: model.Fields})
		}
	}
	root := models[len(models)-1].Name
	writer := "WriteColumnar" + root
	if declaration == "model" {
		writer += "Values"
	}
	fmt.Fprintf(&structs, "func writeFixture(buf *api.ResponseBuf, items []%s, mask []byte) { %s(buf, items, mask) }\n", root, writer)
	return string(g.generateWriteJSONFile(&semantic.Result{Files: []*ast.File{file}}, "generated", enums)) + structs.String()
}

const columnarDirectiveAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

    "bytes"
    "strings"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/str"
)
var transformCalls int
func transformName(value string) string { transformCalls++; return strings.ToUpper(value) }
type identity struct { role string }
func (i identity) ID() int64 { return 1 }
func (i identity) Int(string) int64 { return 0 }
func (i identity) String(key string) string { if key == "role" { return i.role }; return "" }
func TestNestedColumnDirectives(t *testing.T) {
    secret := "confidentiallongvalue"
    children := []PrivateChild{{Name:"firstlongvalue", Secret:&secret}, {Name:"secondlongvalue"}}
    items := []PrivateParent{{Children:children}}
    for _, role := range []string{"admin","guest"} {
        for _, fields := range []byte{1,3} {
            mask := testutil.Bytes(t)(codec.AppendSelectionMask(nil, []byte{1}, []codec.SelectionMaskChild{{FieldID:1, Mask:[]byte{1,fields}}}))
            buf := api.ResponseBuf{Identity:identity{role:role}}
            transformCalls = 0
            writeFixture(&buf, items, mask)
            expectedCalls := 2
            if role == "admin" && fields == 3 { expectedCalls++ }
            if transformCalls != expectedCalls { t.Fatalf("%s mask=%d: transform calls=%d want=%d", role, fields, transformCalls, expectedCalls) }
            child := &codec.ColumnarWriter{}
            child.SetCount(2)
            child.WriteColumnString(1, []string{str.Mask(strings.ToUpper(children[0].Name),3,4), str.Mask(strings.ToUpper(children[1].Name),3,4)})
            if role == "admin" && fields == 3 {
                masked := str.Mask(strings.ToUpper(secret),3,4)
                child.WriteColumnStringPtr(2, []*string{&masked,nil})
            }
            body, err := child.Bytes()
            if err != nil { t.Fatal(err) }
            expected := codec.AppendBytes([]byte{1,0,1}, body)
            expected = append(expected,0)
            if !bytes.Equal(buf.B, expected) { t.Fatalf("%s mask=%d: %x != %x", role, fields, buf.B, expected) }
        }
    }
}
`
