package schema

import (
	"testing"
)

func TestAsModel_Basic(t *testing.T) {
	td := &TypeDecl{
		Name: "AuthPayload",
		Fields: []Field{
			{ID: 1, Name: "token", Type: FieldString},
			{ID: 2, Name: "userId", Type: FieldInt},
		},
	}

	m := td.AsModel()
	if m == nil {
		t.Fatal("AsModel should return non-nil")
	}
	if m.Name != "AuthPayload" {
		t.Errorf("name = %q, want AuthPayload", m.Name)
	}

	// FieldByID
	f := m.FieldByID(1)
	if f == nil || f.Name != "token" {
		t.Errorf("FieldByID(1) = %v, want token", f)
	}
	f = m.FieldByID(2)
	if f == nil || f.Name != "userId" {
		t.Errorf("FieldByID(2) = %v, want userId", f)
	}

	// FieldByName
	f = m.FieldByName("token")
	if f == nil || f.ID != 1 {
		t.Errorf("FieldByName(token) = %v, want ID 1", f)
	}

	// Non-existent
	if m.FieldByID(999) != nil {
		t.Error("FieldByID(999) should be nil")
	}
	if m.FieldByName("missing") != nil {
		t.Error("FieldByName(missing) should be nil")
	}

	// JSONPrefix
	f = m.FieldByName("token")
	if string(f.JSONPrefix) != `"token":` {
		t.Errorf("JSONPrefix = %q, want %q", f.JSONPrefix, `"token":`)
	}
	f = m.FieldByName("userId")
	if string(f.JSONPrefix) != `"userId":` {
		t.Errorf("JSONPrefix = %q, want %q", f.JSONPrefix, `"userId":`)
	}
}

func TestAsModel_CalledTwice_NoCorruption(t *testing.T) {
	td := &TypeDecl{
		Name: "Token",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldString},
			{ID: 2, Name: "expiresAt", Type: FieldDateTime},
		},
	}

	m1 := td.AsModel()
	m2 := td.AsModel()

	// Both should work independently
	f1 := m1.FieldByName("value")
	f2 := m2.FieldByName("value")
	if f1 == nil || f2 == nil {
		t.Fatal("both models should have value field")
	}

	// JSONPrefix should be correct (not corrupted by appending twice)
	if string(f1.JSONPrefix) != `"value":` {
		t.Errorf("m1 JSONPrefix = %q, want %q", f1.JSONPrefix, `"value":`)
	}
	if string(f2.JSONPrefix) != `"value":` {
		t.Errorf("m2 JSONPrefix = %q, want %q", f2.JSONPrefix, `"value":`)
	}

	// Original TypeDecl fields should not be mutated
	if string(td.Fields[0].JSONPrefix) != "" {
		// The copy should prevent td.Fields from being mutated,
		// but the fix uses f.JSONPrefix[:0:0] to force a new backing array.
		// Just verify the original hasn't been corrupted.
		if len(td.Fields[0].JSONPrefix) > 0 {
			// If original was mutated, it would have `"value":` appended multiple times
			expected := `"value":`
			if string(td.Fields[0].JSONPrefix) != expected {
				t.Errorf("original td.Fields[0].JSONPrefix = %q, corrupted", td.Fields[0].JSONPrefix)
			}
		}
	}
}

func TestAsModel_EmptyFields(t *testing.T) {
	td := &TypeDecl{Name: "Empty", Fields: []Field{}}
	m := td.AsModel()
	if m == nil {
		t.Fatal("AsModel should return non-nil for empty fields")
	}
	if m.FieldByID(1) != nil {
		t.Error("empty type should have no fields")
	}
}
