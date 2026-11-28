package codegen

import (
	"strings"
	"testing"
)

func TestInlineAwaitReturnKeepsPublicQueryLineage(t *testing.T) {
	for _, sample := range []struct {
		name, body string
		projected  int
	}{
		{"discarded when arm", `val ignored = when {
 early -> await { if early { return Node.select().orderBy(id.asc).all() } 42 }
 else -> 13
}`, 2},
		{"standalone when arm", `when {
 early -> await { if early { return Node.select().orderBy(id.asc).all() } 42 }
 else -> 13
}`, 2},
		{"standalone when subject", `when (await { if early { return Node.select().orderBy(id.asc).all() } 42 }) {
 42 -> 13
 else -> 7
}`, 2},
		{"discarded elvis operand", `(await {
 if early { return Node.select().orderBy(id.asc).all() }
 null
}) ?: 42`, 2},
		{"discarded for collection", `val ignored = for item in (await {
 if early { return Node.select().orderBy(id.asc).all() }
 [42, 13]
}) { item }`, 2},
		{"discarded for body", `val ignored = for item in [42, 13] {
 val n = await { if early { return Node.select().orderBy(id.asc).all() } 0 }
 item + n
}`, 2},
		{"discarded lexical when block", `val ignored = when {
 early -> {
  val rows = await { if early { return Node.select().orderBy(id.asc).all() } [Node { id: 9, name: "local" }] }
  rows
 }
 else -> [Node { id: 8, name: "normal" }]
}`, 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String }
api page(early: Boolean): [Node] @paginate {
`+sample.body+`
return Node.select().orderBy(id.asc).all()
}`)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2}}})
			generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			handler := string(generated.Files["handler.gen.go"])
			selected := strings.Count(handler, "selectNodeSQLColumns(req.Select)")
			pages := strings.Count(handler, ".AllWithCount(ctx)")
			if selected != sample.projected || pages != sample.projected {
				t.Fatalf("client selection count=%d paged count=%d, want %d each", selected, pages, sample.projected)
			}
			runGeneratedCompilation(t, generated, "package generated\n")
		})
	}
}
