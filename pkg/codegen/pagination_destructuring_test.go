package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedScalarDestructuringPreservesPagination(t *testing.T) {
	for _, sample := range []struct {
		name     string
		body     string
		required []string
	}{
		{
			name: "query alias",
			body: `val (_, rows) = Node.orderBy(id.asc).all()
return rows`,
			required: []string{".Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)", "codec.AppendSvarint(req.Buf.B, _luxoPageTotal0)"},
		},
		{
			name: "shadow with manual list",
			body: `val rows = Node.orderBy(id.asc).all()
if choose {
 val (_, rows) = [Node { id: 8 }]
 return rows
}
return rows`,
			required: []string{"if choose {", "int64(len(rows))", "codec.AppendSvarint(req.Buf.B, _luxoTotal1)", "codec.AppendSvarint(req.Buf.B, _luxoPageTotal0)"},
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := `model Node @noTime { id: Int @id }
api page(choose: Boolean): [Node] @paginate {
` + sample.body + "\n}"
			input := analyzePermissionFixture(t, source)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}})
			generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			handler := string(generated.Files["handler.gen.go"])
			for _, text := range sample.required {
				index := strings.Index(handler, text)
				if index == -1 {
					t.Fatalf("generated handler missing %q:\n%s", text, handler)
				}
				handler = handler[index+len(text):]
			}
		})
	}
}
