package jsonutil

import (
	"testing"
)

type testStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestParse(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		var s testStruct
		err := Parse(`{"name":"Alice","age":30}`, &s)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if s.Name != "Alice" || s.Age != 30 {
			t.Errorf("Parse() = %+v, want {Name:Alice Age:30}", s)
		}
	})
	t.Run("map", func(t *testing.T) {
		var m map[string]any
		err := Parse(`{"key":"value"}`, &m)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if m["key"] != "value" {
			t.Errorf("Parse() map[key] = %v, want value", m["key"])
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		var s testStruct
		err := Parse("not json", &s)
		if err == nil {
			t.Errorf("Parse() expected error for invalid JSON")
		}
	})
}

func TestParseTo(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s, err := ParseTo[testStruct](`{"name":"Bob","age":25}`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if s.Name != "Bob" || s.Age != 25 {
			t.Errorf("ParseTo() = %+v, want {Name:Bob Age:25}", s)
		}
	})
	t.Run("slice", func(t *testing.T) {
		s, err := ParseTo[[]int](`[1,2,3]`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if len(s) != 3 || s[0] != 1 || s[1] != 2 || s[2] != 3 {
			t.Errorf("ParseTo() = %v, want [1 2 3]", s)
		}
	})
	t.Run("primitive", func(t *testing.T) {
		s, err := ParseTo[string](`"hello"`)
		if err != nil {
			t.Fatalf("ParseTo() error = %v", err)
		}
		if s != "hello" {
			t.Errorf("ParseTo() = %q, want %q", s, "hello")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseTo[testStruct]("bad")
		if err == nil {
			t.Errorf("ParseTo() expected error for invalid JSON")
		}
	})
}

func TestStringify(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s := testStruct{Name: "Alice", Age: 30}
		got, err := Stringify(s)
		if err != nil {
			t.Fatalf("Stringify() error = %v", err)
		}
		want := `{"name":"Alice","age":30}`
		if got != want {
			t.Errorf("Stringify() = %q, want %q", got, want)
		}
	})
	t.Run("slice", func(t *testing.T) {
		got, err := Stringify([]int{1, 2, 3})
		if err != nil {
			t.Fatalf("Stringify() error = %v", err)
		}
		if got != "[1,2,3]" {
			t.Errorf("Stringify() = %q, want %q", got, "[1,2,3]")
		}
	})
	t.Run("unmarshalable", func(t *testing.T) {
		// Channels cannot be marshaled
		ch := make(chan int)
		_, err := Stringify(ch)
		if err == nil {
			t.Errorf("Stringify() expected error for channel")
		}
	})
}

func TestPrettyPrint(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		s := testStruct{Name: "Alice", Age: 30}
		got, err := PrettyPrint(s)
		if err != nil {
			t.Fatalf("PrettyPrint() error = %v", err)
		}
		want := "{\n  \"name\": \"Alice\",\n  \"age\": 30\n}"
		if got != want {
			t.Errorf("PrettyPrint() = %q, want %q", got, want)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got, err := PrettyPrint(nil)
		if err != nil {
			t.Fatalf("PrettyPrint() error = %v", err)
		}
		if got != "null" {
			t.Errorf("PrettyPrint(nil) = %q, want %q", got, "null")
		}
	})
	t.Run("unmarshalable", func(t *testing.T) {
		ch := make(chan int)
		_, err := PrettyPrint(ch)
		if err == nil {
			t.Errorf("PrettyPrint() expected error for channel")
		}
	})
}
