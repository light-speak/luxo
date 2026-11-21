package builtin

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestStringParameterBindings(t *testing.T) {
	for _, sample := range []struct {
		method string
		names  []string
		want   [2]int
		issue  string
	}{
		{"replace", []string{"", ""}, [2]int{0, 1}, ""},
		{"replace", []string{"new", "old"}, [2]int{1, 0}, ""},
		{"replace", []string{"", "new"}, [2]int{0, 1}, ""},
		{"substring", []string{"start"}, [2]int{0, -1}, ""},
		{"substring", []string{"end"}, [2]int{-1, 0}, "missing"},
		{"replace", []string{"wrong", "new"}, [2]int{-1, -1}, "unknown"},
		{"replace", []string{"old", "old"}, [2]int{0, -1}, "duplicate"},
		{"replace", []string{"new", ""}, [2]int{-1, 0}, "positional after named"},
		{"replace", nil, [2]int{-1, -1}, "count"},
		{"lowercase", []string{""}, [2]int{-1, -1}, "count"},
		{"lowercase", nil, [2]int{-1, -1}, ""},
		{"trimStart", nil, [2]int{-1, -1}, ""},
		{"contains", []string{"substr"}, [2]int{0, -1}, ""},
		{"startsWith", []string{"prefix"}, [2]int{0, -1}, ""},
		{"endsWith", []string{"suffix"}, [2]int{0, -1}, ""},
		{"matches", []string{"pattern"}, [2]int{0, -1}, ""},
		{"split", []string{"sep"}, [2]int{0, -1}, ""},
		{"repeat", []string{"count"}, [2]int{0, -1}, ""},
		{"padStart", []string{"pad", "length"}, [2]int{1, 0}, ""},
	} {
		t.Run(sample.method+"/"+sample.issue, func(t *testing.T) {
			signature, ok := StringMethod(sample.method)
			if !ok {
				t.Fatal(sample.method)
			}
			arguments := make([]*ast.NamedArg, len(sample.names))
			for index, name := range sample.names {
				arguments[index] = &ast.NamedArg{Name: name}
			}
			got, issue := signature.Bind(arguments)
			if got != sample.want || issue.Kind != sample.issue {
				t.Fatalf("bindings=%v issue=%v, want %v %s", got, issue, sample.want, sample.issue)
			}
			for index, argument := range arguments {
				if argument.Name != sample.names[index] {
					t.Fatal("binding mutated source argument order")
				}
			}
		})
	}
	if _, found := StringMethod("unknown"); found {
		t.Fatal("unknown String method accepted")
	}
}

func TestStringBindingUsesNoHeapAllocation(t *testing.T) {
	signature, _ := StringMethod("replace")
	arguments := []*ast.NamedArg{{Name: "new"}, {Name: "old"}}
	if got := testing.AllocsPerRun(100, func() { signature.Bind(arguments) }); got != 0 {
		t.Fatalf("binding allocated %.0f times", got)
	}
}
