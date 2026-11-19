package builtin

import "testing"

func TestCollectionContractCategories(t *testing.T) {
	for _, group := range []struct {
		kind  CollectionKind
		names []string
	}{
		{CollectionProperty, []string{"size", "isEmpty"}},
		{CollectionLambda, []string{"map", "filter", "forEach", "any", "sortBy", "sumOf"}},
		{CollectionJoin, []string{"joinToString"}},
		{NotCollectionMethod, []string{"", "first", "count", "contains", "chunked", "Map", "where", "days", "d"}},
	} {
		for _, name := range group.names {
			if got := CollectionMethod(name); got != group.kind {
				t.Fatalf("%q: kind=%d want=%d", name, got, group.kind)
			}
		}
	}
}
