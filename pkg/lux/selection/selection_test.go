package selection

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/str"
)

func TestParseEmpty(t *testing.T) {
	fields, err := Parse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(fields))
	}
}

func TestParseOnlySpaces(t *testing.T) {
	fields, err := Parse("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 0 {
		t.Error("spaces-only should return empty")
	}
}

func TestParseSingleField(t *testing.T) {
	fields, err := Parse("name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 1 || fields[0].Name != "name" {
		t.Errorf("expected [name], got %v", fields)
	}
	if fields[0].Children != nil {
		t.Error("leaf field should have nil children")
	}
}

func TestParseMultipleFields(t *testing.T) {
	fields, err := Parse("name,email,age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"name", "email", "age"}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d", len(want), len(fields))
	}
	for i, f := range fields {
		if f.Name != want[i] {
			t.Errorf("field %d: expected '%s', got '%s'", i, want[i], f.Name)
		}
	}
}

func TestParseWithSpaces(t *testing.T) {
	fields, err := Parse(" name , email , age ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}
	if fields[0].Name != "name" || fields[1].Name != "email" || fields[2].Name != "age" {
		t.Error("fields with spaces parsed incorrectly")
	}
}

func TestParseUnderscore(t *testing.T) {
	fields, err := Parse("_id,first_name,__internal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}
	if fields[0].Name != "_id" || fields[1].Name != "first_name" || fields[2].Name != "__internal" {
		t.Error("underscore fields not parsed correctly")
	}
}

func TestParseCamelCase(t *testing.T) {
	fields, err := Parse("firstName,lastName,createdAt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}
	if fields[0].Name != "firstName" || fields[2].Name != "createdAt" {
		t.Error("camelCase fields not parsed correctly")
	}
}

func TestParseNestedOneLevel(t *testing.T) {
	fields, err := Parse("name,posts{title,content}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Name != "name" || fields[0].Children != nil {
		t.Error("first field should be leaf 'name'")
	}
	posts := fields[1]
	if posts.Name != "posts" || len(posts.Children) != 2 {
		t.Fatalf("posts should have 2 children, got %d", len(posts.Children))
	}
	if posts.Children[0].Name != "title" || posts.Children[1].Name != "content" {
		t.Error("posts children should be title, content")
	}
}

func TestParseNestedWithSpaces(t *testing.T) {
	fields, err := Parse("name , posts { title , content } ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[1].Name != "posts" || len(fields[1].Children) != 2 {
		t.Error("nested with spaces parsed incorrectly")
	}
}

func TestParseDeepNesting(t *testing.T) {
	input := "name,posts{title,comments{content,user{name,avatar}}}"
	fields, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}

	posts := fields[1]
	if posts.Name != "posts" || len(posts.Children) != 2 {
		t.Fatal("posts should have 2 children")
	}

	comments := posts.Children[1]
	if comments.Name != "comments" || len(comments.Children) != 2 {
		t.Fatal("comments should have 2 children")
	}

	user := comments.Children[1]
	if user.Name != "user" || len(user.Children) != 2 {
		t.Fatal("user should have 2 children")
	}
	if user.Children[0].Name != "name" || user.Children[1].Name != "avatar" {
		t.Error("user children should be name, avatar")
	}
}

func TestParseMultipleNested(t *testing.T) {
	fields, err := Parse("posts{title},comments{content}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Name != "posts" || len(fields[0].Children) != 1 {
		t.Error("posts wrong")
	}
	if fields[1].Name != "comments" || len(fields[1].Children) != 1 {
		t.Error("comments wrong")
	}
}

// Build a{b{c{...}}} with n levels
func buildDeepInput(depth int) string {
	var b strings.Builder
	for i := range depth {
		if i > 0 {
			b.WriteByte('{')
		}
		b.WriteByte(byte('a' + i%26))
	}
	for range depth - 1 {
		b.WriteByte('}')
	}
	return b.String()
}

func TestParseDeep10Levels(t *testing.T) {
	input := buildDeepInput(10)
	fields, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Walk down 10 levels
	f := fields
	for i := range 10 {
		if len(f) != 1 {
			t.Fatalf("level %d: expected 1 field, got %d", i, len(f))
		}
		expected := string(rune('a' + i%26))
		if f[0].Name != expected {
			t.Fatalf("level %d: expected '%s', got '%s'", i, expected, f[0].Name)
		}
		if i < 9 {
			f = f[0].Children
		}
	}
	if f[0].Children != nil {
		t.Error("deepest field should be leaf")
	}
}

// Error cases

func TestErrorTrailingComma(t *testing.T) {
	_, err := Parse("name,email,")
	if err == nil {
		t.Fatal("expected error for trailing comma")
	}
}

func TestErrorEmptyBraces(t *testing.T) {
	_, err := Parse("posts{}")
	if err == nil {
		t.Fatal("expected error for empty braces")
	}
}

func TestErrorUnclosedBrace(t *testing.T) {
	_, err := Parse("posts{title")
	if err == nil {
		t.Fatal("expected error for unclosed brace")
	}
}

func TestErrorNestedUnclosed(t *testing.T) {
	_, err := Parse("posts{comments{content}")
	if err == nil {
		t.Fatal("expected error for nested unclosed brace")
	}
}

func TestErrorExtraCloseBrace(t *testing.T) {
	_, err := Parse("name}")
	if err == nil {
		t.Fatal("expected error for extra close brace")
	}
}

func TestErrorUnexpectedChar(t *testing.T) {
	_, err := Parse("name;email")
	if err == nil {
		t.Fatal("expected error for unexpected character")
	}
}

func TestErrorStartsWithNumber(t *testing.T) {
	_, err := Parse("123field")
	if err == nil {
		t.Fatal("expected error for field starting with number")
	}
}

func TestErrorCommaOnly(t *testing.T) {
	_, err := Parse(",")
	if err == nil {
		t.Fatal("expected error for comma only")
	}
}

func TestErrorDoubleComma(t *testing.T) {
	_, err := Parse("name,,email")
	if err == nil {
		t.Fatal("expected error for double comma")
	}
}

func TestErrorFirstFieldBadNested(t *testing.T) {
	// Error in first parseField call (line 46)
	_, err := Parse("posts{}")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestErrorSecondFieldBadNested(t *testing.T) {
	// Error in loop parseField call (line 61)
	_, err := Parse("name,posts{}")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestErrorNestedCommaUnclosed(t *testing.T) {
	// Error propagation from nested parseList through loop parseField
	_, err := Parse("name,posts{title,comments{}")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSQLColumnsNil(t *testing.T) {
	cols := SQLColumns(nil)
	if cols != nil {
		t.Errorf("nil fields should return nil, got %v", cols)
	}
}

func TestSQLColumnsEmpty(t *testing.T) {
	cols := SQLColumns([]*Field{})
	if cols != nil {
		t.Errorf("empty fields should return nil, got %v", cols)
	}
}

func TestSQLColumnsLeafOnly(t *testing.T) {
	fields, _ := Parse("name,email,avatar")
	cols := SQLColumns(fields)
	// Should include id + selected fields
	if len(cols) != 4 {
		t.Fatalf("expected 4 cols (id+3), got %d: %v", len(cols), cols)
	}
	if cols[0] != "id" {
		t.Error("first col should be id")
	}
}

func TestSQLColumnsSkipsRelation(t *testing.T) {
	fields, _ := Parse("name,posts{title}")
	cols := SQLColumns(fields)
	// posts has Children → skipped
	for _, c := range cols {
		if c == "posts" {
			t.Error("relation field should be skipped")
		}
	}
}

func TestSQLColumnsIdAlreadySelected(t *testing.T) {
	fields, _ := Parse("id,name")
	cols := SQLColumns(fields)
	// Should not duplicate id
	count := 0
	for _, c := range cols {
		if c == "id" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("id should appear once, got %d", count)
	}
}

func TestSQLColumnsCamelToSnake(t *testing.T) {
	fields, _ := Parse("userId,createdAt")
	cols := SQLColumns(fields)
	found := map[string]bool{}
	for _, c := range cols {
		found[c] = true
	}
	if !found["user_id"] {
		t.Error("userId should become user_id")
	}
	if !found["created_at"] {
		t.Error("createdAt should become created_at")
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"userId", "user_id"},
		{"createdAt", "created_at"},
		{"name", "name"},
		{"ID", "i_d"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := str.ToSnakeCase(tt.in); got != tt.want {
			t.Errorf("str.ToSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSQLColumnsOnlyRelations(t *testing.T) {
	// All fields have Children (relations) — only "id" is collected, so should return nil (SELECT *)
	fields := []*Field{
		{Name: "posts", Children: []*Field{{Name: "title"}}},
		{Name: "comments", Children: []*Field{{Name: "content"}}},
	}
	cols := SQLColumns(fields)
	if cols != nil {
		t.Errorf("only-relation fields should return nil (SELECT *), got %v", cols)
	}
}
