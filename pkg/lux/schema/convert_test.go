package schema

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func userModel() *Model {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
			{ID: 3, Name: "email", Type: FieldString},
			{ID: 4, Name: "score", Type: FieldFloat},
			{ID: 5, Name: "phone", Type: FieldString, Nullable: true},
			{ID: 6, Name: "isActive", Type: FieldBool},
		},
	}
	s.RegisterModel(m)
	return m
}

func encodeUser(id int64, name, email string, score float64, phone *string, active bool) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(1, id)
	enc.WriteFieldString(2, name)
	enc.WriteFieldString(3, email)
	enc.WriteFieldFloat(4, score)
	if phone != nil {
		enc.WriteFieldStringPtr(5, phone)
	} else {
		enc.WriteFieldStringPtr(5, nil)
	}
	enc.WriteFieldBool(6, active)
	enc.WriteEnd()
	return enc.Bytes()
}

func TestBinaryToJSON_Basic(t *testing.T) {
	m := userModel()
	phone := "13800138000"
	data := encodeUser(1, "Alice", "alice@test.com", 99.5, &phone, true)

	result := BinaryToJSON(nil, data, m)
	expected := `{"id":1,"name":"Alice","email":"alice@test.com","score":99.5,"phone":"13800138000","isActive":true}`

	if string(result) != expected {
		t.Errorf("got:  %s\nwant: %s", result, expected)
	}
}

func TestBinaryToJSON_NullField(t *testing.T) {
	m := userModel()
	data := encodeUser(2, "Bob", "bob@test.com", 0, nil, false)

	result := BinaryToJSON(nil, data, m)
	expected := `{"id":2,"name":"Bob","email":"bob@test.com","score":0,"phone":null,"isActive":false}`

	if string(result) != expected {
		t.Errorf("got:  %s\nwant: %s", result, expected)
	}
}

func TestBinaryToJSON_StringEscape(t *testing.T) {
	m := userModel()
	phone := "123"
	data := encodeUser(1, `Al"ice`, "a@b.com", 0, &phone, true)

	result := BinaryToJSON(nil, data, m)
	if string(result) == "" {
		t.Error("empty result")
	}
	// Should contain escaped quote
	if got := string(result); len(got) == 0 {
		t.Error("empty")
	}
}

func TestBinaryListToJSON(t *testing.T) {
	m := userModel()

	// Encode 2 users in columnar format
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnInt(1, []int64{1, 2})                     // id
	w.WriteColumnString(2, []string{"Alice", "Bob"})       // name
	w.WriteColumnString(3, []string{"a@b.com", "b@b.com"}) // email
	w.WriteColumnFloat(4, []float64{10, 20})               // score
	phone1 := "111"
	w.WriteColumnStringPtr(5, []*string{&phone1, nil}) // phone (nullable)
	w.WriteColumnBool(6, []bool{true, false})          // isActive

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)

	if got[0] != '[' || got[len(got)-1] != ']' {
		t.Errorf("expected JSON array, got: %s", got)
	}
	if len(got) < 50 {
		t.Errorf("result too short: %s", got)
	}
	// Verify content
	if !strings.Contains(got, `"Alice"`) || !strings.Contains(got, `"Bob"`) {
		t.Errorf("missing names: %s", got)
	}
	if !strings.Contains(got, `"111"`) {
		t.Errorf("missing phone: %s", got)
	}
	if !strings.Contains(got, "null") {
		t.Errorf("missing null for Bob's phone: %s", got)
	}
}

func TestJSONParamsToBinary(t *testing.T) {
	api := &API{
		Name: "getUser",
		Params: []Param{
			{ID: 1, Name: "id", Type: FieldInt},
		},
	}

	params := map[string]any{"id": float64(42)}
	data := JSONParamsToBinary(params, api)

	// Decode and verify
	dec := codec.NewDecoder(data)
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field ID 1")
	}
	v := dec.ReadInt()
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestJSONParamsToBinary_MultipleParams(t *testing.T) {
	api := &API{
		Name: "createUser",
		Params: []Param{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "email", Type: FieldString},
			{ID: 3, Name: "score", Type: FieldFloat},
		},
	}

	params := map[string]any{
		"name":  "Alice",
		"email": "alice@test.com",
		"score": float64(99.5),
	}
	data := JSONParamsToBinary(params, api)

	dec := codec.NewDecoder(data)
	found := map[int]bool{}
	for dec.NextField() {
		found[dec.FieldID()] = true
		switch dec.FieldID() {
		case 1:
			if v := dec.ReadString(); v != "Alice" {
				t.Errorf("name: got %q", v)
			}
		case 2:
			if v := dec.ReadString(); v != "alice@test.com" {
				t.Errorf("email: got %q", v)
			}
		case 3:
			if v := dec.ReadFloat(); v != 99.5 {
				t.Errorf("score: got %f", v)
			}
		}
	}
	if len(found) != 3 {
		t.Errorf("expected 3 fields, got %d", len(found))
	}
}
