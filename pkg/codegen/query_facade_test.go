package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

type queryFacadeTestShape struct {
	name     string
	decimal  bool
	writable bool
}

func (shape queryFacadeTestShape) fields() []*ast.FieldDecl {
	valueType := "Int"
	if shape.decimal {
		valueType = "Decimal"
	}
	return []*ast.FieldDecl{
		testField("id", "Int", directive("id")),
		{Name: "legacy"},
		testField("value", valueType),
	}
}

func queryFacadeTestShapes() []queryFacadeTestShape {
	return []queryFacadeTestShape{
		{"R", false, false}, {"User", false, true},
		{"Report", true, false}, {"Invoice", true, true},
		{"A" + strings.Repeat("a", 256), false, false},
		{"B" + strings.Repeat("b", 256), false, true},
		{"C" + strings.Repeat("c", 319), true, false},
		{"D" + strings.Repeat("d", 319), true, true},
	}
}

func TestQueryFacadeSourceShapes(t *testing.T) {
	var source, assertions strings.Builder
	source.WriteString("package generated\nimport (\"context\"; \"github.com/light-speak/luxo/pkg/lux\"; \"github.com/light-speak/luxo/pkg/lux/pg\"; \"github.com/shopspring/decimal\")\n")
	assertions.WriteString("package generated\nimport \"github.com/light-speak/luxo/pkg/lux\"\n")
	for _, shape := range queryFacadeTestShapes() {
		var facade strings.Builder
		defaultGenerator().generateQueryFacade(&facade, shape.name, shape.fields(), shape.writable)
		emitted := facade.String()
		if strings.Contains(emitted, ") SumDecimal(") != shape.decimal ||
			strings.Contains(emitted, ") Delete(") != shape.writable ||
			strings.Contains(emitted, ") SoftDelete(") != shape.writable ||
			strings.Contains(emitted, ") updatePrepared(") != shape.writable {
			t.Fatalf("unexpected method set for %c (%d bytes)", shape.name[0], len(shape.name))
		}
		fmt.Fprintf(&source, "type %s struct{}\n", shape.name)
		source.WriteString(emitted)
		fmt.Fprintf(&assertions, "var _ interface{ Where(...lux.Condition) *%sQuery } = (*%sQuery)(nil)\n", shape.name, shape.name)
	}
	runGeneratedProtocolPackage(t, source.String(), assertions.String())
}

func TestQueryFacadeLongPartsReuseDestination(t *testing.T) {
	long := strings.Repeat("U", 320)
	parts := []string{"type ", long, "Query pg.Query[", long, "]\n"}
	const initial = "// generated facade\n"
	want := initial + strings.Join(parts, "")
	allocated := testing.AllocsPerRun(32, func() {
		var output strings.Builder
		output.Grow(len(want))
		output.WriteString(initial)
		writeQueryFacadeParts(&output, parts...)
		if output.String() != want {
			t.Fatal("long declaration changed existing output or bytes")
		}
	})
	if allocated > 1 {
		t.Fatalf("large declaration used %.0f allocations with an already reserved destination", allocated)
	}
}
