package codegen

import (
	"fmt"
	"strings"
	"testing"
)

var selectedObjectGenerationCases = []struct{ name, directive, body string }{
	{"direct", "@paginate", `return Product.select().orderBy(id.asc).all()`},
	{"wrapper", "@paginate", `return Box { rows: Product.select().orderBy(id.asc).all() }.rows`},
	{"nested", "@paginate", `val outer = Outer { box: Box { rows: Product.select().orderBy(id.asc).all() } } return outer.box.rows`},
	{"assignment", "@paginate", `val box = Box { rows: [] } box.rows = Product.select().orderBy(id.asc).all() return box.rows`},
	{"copy", "@paginate", `val box = Box { rows: Product.select().orderBy(id.asc).all() } val alias = box box.rows = [] return alias.rows`},
	{"reference", "@paginate", `val holder = Holder { id: 1, rows: [] } val alias = holder holder.rows = Product.select().orderBy(id.asc).all() return alias.rows`},
	{"ordinary", "", `val box = Box { rows: Product.select().all() } return box.rows.filter { it.price > 0 }`},
	{"conditional", "", `var box = Box { rows: Product.select().all() } if true { box = Box { rows: [] } } return box.rows`},
	{"opaque", "", `val box = Box { rows: Product.select().all() } val ignored = inspectBox(box)? return box.rows`},
}

func BenchmarkSelectedQueryObjectGeneration(b *testing.B) {
	for _, sample := range selectedObjectGenerationCases {
		for _, count := range []int{1, 32} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString(selectedQueryBenchmarkModel + "\ntype Box { rows: [Product] }\ntype Outer { box: Box }\nmodel Holder @noTime { id: Int @id rows: [Product] @by(id,id) }\n")
				if sample.name == "opaque" {
					source.WriteString("fn inspectBox(box: Box): Result<Int> @native\n")
				}
				for index := range count {
					fmt.Fprintf(&source, "api lookup%d(): [Product] %s { %s }\n", index, sample.directive, sample.body)
				}
				input := analyzePermissionFixture(b, source.String())
				if len(input.Errors) != 0 {
					b.Fatal(input.Errors)
				}
				ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{
					"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}, "Box": {"rows": 1}, "Outer": {"box": 1}, "Holder": {"id": 1, "rows": 2},
				}})
				generator := mustNewGenerator(b, GeneratorConfig{Driver: DriverPG, IDs: ids})
				b.ReportAllocs()
				for b.Loop() {
					if _, err := generator.Generate(input, "generated", nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
