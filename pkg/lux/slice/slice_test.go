package slice

import "testing"

func TestMap(t *testing.T) {
	result := Map([]int{1, 2, 3}, func(x int) int { return x * 2 })
	if len(result) != 3 || result[0] != 2 || result[1] != 4 || result[2] != 6 {
		t.Errorf("got %v", result)
	}
}

func TestFilter(t *testing.T) {
	result := Filter([]int{1, 2, 3, 4, 5}, func(x int) bool { return x%2 == 0 })
	if len(result) != 2 || result[0] != 2 || result[1] != 4 {
		t.Errorf("got %v", result)
	}
}

func TestFilterEmpty(t *testing.T) {
	result := Filter([]int{1, 3, 5}, func(x int) bool { return x%2 == 0 })
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestSumOf(t *testing.T) {
	type item struct{ price float64 }
	result := SumOf([]item{{10}, {20}, {30}}, func(i item) float64 { return i.price })
	if result != 60 {
		t.Errorf("got %f", result)
	}
}

func TestSumOfInt(t *testing.T) {
	result := SumOfInt([]int64{1, 2, 3}, func(x int64) int64 { return x })
	if result != 6 {
		t.Errorf("got %d", result)
	}
}

func TestAny(t *testing.T) {
	if !Any([]int{1, 2, 3}, func(x int) bool { return x == 2 }) {
		t.Error("should find 2")
	}
	if Any([]int{1, 2, 3}, func(x int) bool { return x == 5 }) {
		t.Error("should not find 5")
	}
}

func TestAll(t *testing.T) {
	if !All([]int{2, 4, 6}, func(x int) bool { return x%2 == 0 }) {
		t.Error("all even")
	}
	if All([]int{2, 3, 6}, func(x int) bool { return x%2 == 0 }) {
		t.Error("3 is not even")
	}
}

func TestNone(t *testing.T) {
	if !None([]int{1, 3, 5}, func(x int) bool { return x%2 == 0 }) {
		t.Error("no even numbers")
	}
}

func TestFind(t *testing.T) {
	v, ok := Find([]int{1, 2, 3}, func(x int) bool { return x == 2 })
	if !ok || v != 2 {
		t.Error("should find 2")
	}
	_, ok = Find([]int{1, 2, 3}, func(x int) bool { return x == 5 })
	if ok {
		t.Error("should not find 5")
	}
}

func TestJoinToString(t *testing.T) {
	type user struct{ name string }
	result := JoinToString([]user{{"a"}, {"b"}, {"c"}}, func(u user) string { return u.name }, ", ")
	if result != "a, b, c" {
		t.Errorf("got %q", result)
	}
}

func TestGroupBy(t *testing.T) {
	type item struct {
		cat  string
		name string
	}
	items := []item{{"a", "1"}, {"b", "2"}, {"a", "3"}}
	groups := GroupBy(items, func(i item) string { return i.cat })
	if len(groups["a"]) != 2 || len(groups["b"]) != 1 {
		t.Errorf("got %v", groups)
	}
}

func TestUnique(t *testing.T) {
	result := Unique([]int{1, 2, 2, 3, 1})
	if len(result) != 3 || result[0] != 1 || result[1] != 2 || result[2] != 3 {
		t.Errorf("got %v", result)
	}
}

func TestFlatten(t *testing.T) {
	result := Flatten([][]int{{1, 2}, {3}, {4, 5}})
	if len(result) != 5 || result[4] != 5 {
		t.Errorf("got %v", result)
	}
}

func TestCount(t *testing.T) {
	n := Count([]int{1, 2, 3, 4, 5}, func(x int) bool { return x > 3 })
	if n != 2 {
		t.Errorf("got %d", n)
	}
}

func TestFirstLast(t *testing.T) {
	v, ok := First([]int{10, 20})
	if !ok || v != 10 {
		t.Error("first")
	}
	v, ok = Last([]int{10, 20})
	if !ok || v != 20 {
		t.Error("last")
	}
	_, ok = First([]int{})
	if ok {
		t.Error("empty first")
	}
}

func TestSortBy(t *testing.T) {
	type item struct{ name string }
	result := SortBy([]item{{"c"}, {"a"}, {"b"}}, func(i item) string { return i.name })
	if result[0].name != "a" || result[1].name != "b" || result[2].name != "c" {
		t.Errorf("got %v", result)
	}
}

func TestSortByInt(t *testing.T) {
	result := SortByInt([]int64{3, 1, 2}, func(x int64) int64 { return x })
	if result[0] != 1 || result[1] != 2 || result[2] != 3 {
		t.Errorf("got %v", result)
	}
}

func TestMapStringToInt(t *testing.T) {
	result := Map([]string{"a", "bb", "ccc"}, func(s string) int { return len(s) })
	if result[0] != 1 || result[1] != 2 || result[2] != 3 {
		t.Errorf("got %v", result)
	}
}
