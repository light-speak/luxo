package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/token"
)

// ========== Scope & Lookup Tests ==========

func TestScopeLookup(t *testing.T) {
	scope := NewScope()
	scope.Define(&Symbol{Name: "x", Kind: SymVariable})

	child := scope.Child()
	child.Define(&Symbol{Name: "y", Kind: SymVariable})

	// child can see parent's symbols
	if child.Lookup("x") == nil {
		t.Error("expected to find 'x' in parent scope")
	}
	// parent can't see child's symbols
	if scope.Lookup("y") != nil {
		t.Error("expected not to find 'y' in parent scope")
	}
}

func TestTypeNarrowing(t *testing.T) {
	scope := NewScope()
	nullableType := &ResolvedType{Kind: TypeModel, Name: "User", Nullable: true}
	scope.Define(&Symbol{Name: "user", Kind: SymVariable, Type: nullableType})

	// before narrowing
	resolved := scope.ResolvedTypeOf("user")
	if !resolved.Nullable {
		t.Error("expected nullable before narrowing")
	}

	// narrow
	scope.Narrow("user", nullableType.AsNonNull())

	// after narrowing
	resolved = scope.ResolvedTypeOf("user")
	if resolved.Nullable {
		t.Error("expected non-null after narrowing")
	}
}

func TestLookupPrefix(t *testing.T) {
	scope := NewScope()
	scope.Define(&Symbol{Name: "user", Kind: SymVariable})
	scope.Define(&Symbol{Name: "username", Kind: SymVariable})
	scope.Define(&Symbol{Name: "post", Kind: SymVariable})

	results := scope.LookupPrefix("us")
	if len(results) != 2 {
		t.Errorf("expected 2 results for prefix 'us', got %d", len(results))
	}
}

// ========== Edit Distance Tests ==========

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"String", "Stirng", 2},
		{"User", "Usar", 1},
		{"hello", "hello", 0},
		{"abc", "xyz", 3},
	}
	for _, tt := range tests {
		d := editDistance(tt.a, tt.b)
		if d != tt.expected {
			t.Errorf("editDistance(%q, %q) = %d, expected %d", tt.a, tt.b, d, tt.expected)
		}
	}
}

func TestEditDistanceEmptyStrings(t *testing.T) {
	if d := editDistance("", "abc"); d != 3 {
		t.Errorf("editDistance('', 'abc') = %d, want 3", d)
	}
	if d := editDistance("abc", ""); d != 3 {
		t.Errorf("editDistance('abc', '') = %d, want 3", d)
	}
	if d := editDistance("", ""); d != 0 {
		t.Errorf("editDistance('', '') = %d, want 0", d)
	}
}

func TestDefineDuplicate(t *testing.T) {
	scope := NewScope()
	ok1 := scope.Define(&Symbol{Name: "x", Kind: SymVariable})
	if !ok1 {
		t.Error("first define should return true")
	}
	ok2 := scope.Define(&Symbol{Name: "x", Kind: SymVariable})
	if ok2 {
		t.Error("duplicate define should return false")
	}
}

func TestLookupLocal(t *testing.T) {
	parent := NewScope()
	parent.Define(&Symbol{Name: "x", Kind: SymVariable})

	child := parent.Child()
	child.Define(&Symbol{Name: "y", Kind: SymVariable})

	// LookupLocal should find y in child
	if child.LookupLocal("y") == nil {
		t.Error("expected to find 'y' in local scope")
	}
	// LookupLocal should NOT find x in child (it's in parent)
	if child.LookupLocal("x") != nil {
		t.Error("expected not to find 'x' in local scope")
	}
}

func TestLookupPrefixInParent(t *testing.T) {
	parent := NewScope()
	parent.Define(&Symbol{Name: "userModel", Kind: SymModel})
	parent.Define(&Symbol{Name: "postModel", Kind: SymModel})

	child := parent.Child()
	child.Define(&Symbol{Name: "userName", Kind: SymVariable})

	results := child.LookupPrefix("user")
	// Should find "userName" in child and "userModel" in parent
	if len(results) != 2 {
		t.Errorf("expected 2 results for prefix 'user', got %d", len(results))
	}
}

func TestAsNullableAlreadyNullable(t *testing.T) {
	typ := &ResolvedType{Kind: TypeString, Name: "String", Nullable: true}
	result := typ.AsNullable()
	if result != typ {
		t.Error("AsNullable on already nullable type should return same pointer")
	}
}

func TestAsNonNullAlreadyNonNull(t *testing.T) {
	typ := &ResolvedType{Kind: TypeString, Name: "String", Nullable: false}
	result := typ.AsNonNull()
	if result != typ {
		t.Error("AsNonNull on already non-null type should return same pointer")
	}
}

func TestIsComparableEnum(t *testing.T) {
	typ := &ResolvedType{Kind: TypeEnum, Name: "Role"}
	if !typ.IsComparable() {
		t.Error("Enum should be comparable")
	}
	// Also verify non-comparable type
	modelTyp := &ResolvedType{Kind: TypeModel, Name: "User"}
	if modelTyp.IsComparable() {
		t.Error("Model should not be comparable")
	}
}

// ========== Coverage-boosting Tests ==========

func TestErrorStringFormat(t *testing.T) {
	// Without suggestion
	e := Error{
		Pos:     token.Position{File: "test.luxo", Line: 1, Col: 5},
		Message: "unknown type 'Foo'",
	}
	s := e.Error()
	if !strings.Contains(s, "unknown type 'Foo'") {
		t.Errorf("expected message in error string, got %q", s)
	}
	if strings.Contains(s, "(") {
		t.Errorf("did not expect suggestion parentheses, got %q", s)
	}

	// With suggestion
	e.Suggestion = "did you mean 'Foo2'?"
	s = e.Error()
	if !strings.Contains(s, "did you mean 'Foo2'?") {
		t.Errorf("expected suggestion in error string, got %q", s)
	}
}

func TestSymbolKindString(t *testing.T) {
	tests := []struct {
		kind SymbolKind
		want string
	}{
		{SymModel, "model"},
		{SymInterface, "interface"},
		{SymEnum, "enum"},
		{SymSealed, "sealed"},
		{SymType, "type"},
		{SymApi, "api"},
		{SymFn, "fn"},
		{SymError, "error"},
		{SymField, "field"},
		{SymParam, "param"},
		{SymVariable, "variable"},
		{SymMiddleware, "middleware"},
		{SymScope, "scope"},
		{SymbolKind(999), "unknown"},
	}
	for _, tt := range tests {
		got := tt.kind.String()
		if got != tt.want {
			t.Errorf("SymbolKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
