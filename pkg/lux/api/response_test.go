package api

import (
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func sel(s string) []*selection.Field {
	f, _ := selection.Parse(s)
	return f
}

func jsonEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	sonic.Unmarshal(got, &g)
	sonic.Unmarshal([]byte(want), &w)
	// Sort keys deterministically for comparison
	gb, _ := sonic.ConfigStd.Marshal(g)
	wb, _ := sonic.ConfigStd.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("got %s, want %s", string(got), want)
	}
}

func TestFilterFieldsNilSelect(t *testing.T) {
	data := map[string]any{"name": "lin", "email": "lin@test.com", "age": 25}
	result, err := FilterFields(data, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return all fields
	var obj map[string]any
	sonic.Unmarshal(result, &obj)
	if obj["name"] != "lin" || obj["email"] != "lin@test.com" {
		t.Error("nil select should return all fields")
	}
}

func TestFilterFieldsSimple(t *testing.T) {
	data := map[string]any{"name": "lin", "email": "lin@test.com", "age": 25}
	result, err := FilterFields(data, sel("name,email"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","email":"lin@test.com"}`)
}

func TestFilterFieldsSingleField(t *testing.T) {
	data := map[string]any{"name": "lin", "email": "lin@test.com"}
	result, err := FilterFields(data, sel("name"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin"}`)
}

func TestFilterFieldsMissingField(t *testing.T) {
	data := map[string]any{"name": "lin"}
	result, err := FilterFields(data, sel("name,email"))
	if err != nil {
		t.Fatal(err)
	}
	// email not in data, should be omitted
	jsonEqual(t, result, `{"name":"lin"}`)
}

func TestFilterFieldsNested(t *testing.T) {
	data := map[string]any{
		"name": "lin",
		"posts": []map[string]any{
			{"title": "hello", "content": "world", "id": 1},
			{"title": "foo", "content": "bar", "id": 2},
		},
	}
	result, err := FilterFields(data, sel("name,posts{title}"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","posts":[{"title":"hello"},{"title":"foo"}]}`)
}

func TestFilterFieldsDeepNested(t *testing.T) {
	data := map[string]any{
		"name": "lin",
		"posts": []map[string]any{
			{
				"title": "hello",
				"comments": []map[string]any{
					{"content": "nice", "author": "tom"},
				},
			},
		},
	}
	result, err := FilterFields(data, sel("name,posts{title,comments{content}}"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","posts":[{"title":"hello","comments":[{"content":"nice"}]}]}`)
}

func TestFilterFieldsEmptyArray(t *testing.T) {
	data := map[string]any{"name": "lin", "posts": []any{}}
	result, err := FilterFields(data, sel("name,posts{title}"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","posts":[]}`)
}

func TestFilterFieldsScalar(t *testing.T) {
	// Scalar value with selection — just returns as-is
	result, err := FilterFields("hello", sel("name"))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `"hello"` {
		t.Errorf("got %s", string(result))
	}
}

func TestFilterFieldsNull(t *testing.T) {
	data := map[string]any{"name": "lin", "avatar": nil}
	result, err := FilterFields(data, sel("name,avatar"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","avatar":null}`)
}

func TestFilterFieldsStruct(t *testing.T) {
	type User struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Age   int    `json:"age"`
	}
	data := User{Name: "lin", Email: "lin@test.com", Age: 25}
	result, err := FilterFields(data, sel("name,email"))
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"name":"lin","email":"lin@test.com"}`)
}

func TestFilterFieldsEmptyRaw(t *testing.T) {
	result, err := filterRaw(json.RawMessage{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %s", string(result))
	}
}

func TestFilterFieldsMarshalError(t *testing.T) {
	// chan is not JSON-serializable
	_, err := FilterFields(make(chan int), sel("name"))
	if err == nil {
		t.Fatal("expected error for non-serializable type")
	}
}

func TestFilterFieldsNilSelectMarshalError(t *testing.T) {
	_, err := FilterFields(make(chan int), nil)
	if err == nil {
		t.Fatal("expected error for non-serializable type with nil select")
	}
}

func TestFilterObjectInvalidJSON(t *testing.T) {
	_, err := filterObject(json.RawMessage(`not json`), sel("name"))
	if err == nil {
		t.Fatal("expected error for invalid JSON object")
	}
}

func TestFilterArrayInvalidJSON(t *testing.T) {
	_, err := filterArray(json.RawMessage(`not json`), sel("name"))
	if err == nil {
		t.Fatal("expected error for invalid JSON array")
	}
}

func TestFilterObjectNestedError(t *testing.T) {
	// posts is an object with invalid nested JSON for child filtering
	raw := json.RawMessage(`{"posts":{"bad": invalid}}`)
	_, err := filterObject(raw, sel("posts{title}"))
	if err != nil {
		// Expected: outer unmarshal fails first
		return
	}
	t.Fatal("expected error")
}

func TestFilterObjectChildError(t *testing.T) {
	// posts value is a valid JSON object, but children selection triggers filterRaw on nested invalid
	raw := json.RawMessage(`{"posts":[{"title": bad}]}`)
	_, err := filterObject(raw, sel("posts{title}"))
	// unmarshal fails on outer object because inner is invalid
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFilterArrayChildError(t *testing.T) {
	// Array with element that is invalid for nested filtering
	raw := json.RawMessage(`[{"a": bad}]`)
	_, err := filterArray(raw, sel("a"))
	if err == nil {
		t.Fatal("expected error for invalid array element")
	}
}
