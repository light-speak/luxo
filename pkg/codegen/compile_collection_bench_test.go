package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkCollectionLengthCompiler(b *testing.B) {
	for _, sample := range []struct {
		name, receiver, member, expected string
		list, method                     bool
	}{
		{"field", "Snapshot", "name", "value.Name", false, false},
		{"string-length", "String", "length", "int64(len(value))", false, false},
		{"string-size", "String", "size", "int64(len(value))", false, false},
		{"list-size-method", "Int", "size", "int64(len(value))", true, true},
		{"list-empty-method", "Int", "isEmpty", "(len(value) == 0)", true, true},
		{"string-empty-method", "String", "isEmpty", "(len(value) == 0)", false, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			object := &ast.Ident{Name: "value"}
			object.SetTypeTag(sample.receiver)
			object.SetListType(sample.list)
			member := &ast.MemberExpr{Object: object, Field: sample.member}
			call := &ast.CallExpr{Func: member}
			c := newCompiler(nil)
			c.vars["value"] = valType{name: sample.receiver, isList: sample.list}
			compile := func() string {
				if sample.method {
					return c.compileStringMethod(call)
				}
				return c.compileMember(member)
			}
			if got := compile(); got != sample.expected || c.b.Len() != 0 {
				b.Fatalf("unexpected output %q, statements %q", got, c.b.String())
			}
			b.ReportAllocs()
			for b.Loop() {
				if compile() == "" {
					b.Fatal("missing output")
				}
			}
		})
	}
}

// Both inputs were valid before the property fix; parsing is outside the timer.
func BenchmarkCollectionLengthGeneration(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"scalar", "api run(value: Int): Int { return value }"},
		{"list-method", "api run(values: [Int]): Int { return values.size() }"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{APIs: map[string]int{"run": 1}})}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
