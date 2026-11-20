package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Scope & Lookup Tests ==========

func TestScopeAllSymbolsListsOnlyLocalBindings(t *testing.T) {
	parent := NewScope()
	parent.Define(&Symbol{Name: "parent", Kind: SymVariable})
	child := parent.Child()
	if got := child.AllSymbols(); len(got) != 0 {
		t.Fatalf("empty child lists parent symbols: %v", got)
	}
	local := &Symbol{Name: "local", Kind: SymVariable}
	child.Define(local)
	if got := child.AllSymbols(); len(got) != 1 || got[0] != local {
		t.Fatalf("child symbols = %v, want local binding", got)
	}
}

func TestBareBuiltinValuesAreRejected(t *testing.T) {
	for _, name := range []string{
		"task", "cache", "storage", "mail", "services", "error", "request",
		"http", "json", "time", "math", "crypto", "convert", "regex", "base64", "url", "uuid", "env",
		"print", "now", "today", "verifyHash", "generateToken", "find", "save",
	} {
		t.Run(name, func(t *testing.T) {
			result := analyze(t, "fn read(): Int { return "+name+" }")
			expectError(t, result, "cannot be used as a value")
		})
	}
	t.Run("await_builtin_name", func(t *testing.T) {
		result := analyze(t, `fn read(): Int { await { val task = 1 } return task }`)
		expectError(t, result, "cannot be used as a value")
	})
	t.Run("await_ordinary_name", func(t *testing.T) {
		result := analyze(t, `fn read(): Int { await { val ordinary = 1 } return ordinary }`)
		expectError(t, result, "undefined: 'ordinary'")
	})
}

func TestBuiltinValueChecksPreserveBindingsAndOperations(t *testing.T) {
	for _, source := range []string{
		`fn read(task: Int): Int { return task }`,
		`fn read(): Int { val task = 1 return task }`,
		`fn read(): Int { val task = 1 await { val task = task + 1 val copy = task } return task }`,
		`model Task @noTime { id: Int @id } fn read(task: Task): Int { return task.id }`,
		`fn read(crypto: Int): Int { return crypto }`,
		`fn now(value: Int): Int { return value } fn read(): Int { return now(1) }`,
		`fn read(): DateTime { return now() }`,
		`fn read(): String { return crypto.randomHex(4) }`,
		`fn read(): Int { val values: Channel<Int> = Channel(1) values.close() return 1 }`,
		`api current(): Int @auth { return my.id }`,
		`api current(): Int @auth { val identity = my return identity.id }`,
		`fn read(): Int { throw error.NotFound }`,
		"fn read(): Int { val (task, entries) = await {\n1\n[2]\n} return task }",
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, source)) })
	}
}

func TestDuplicateVariableBindingsAreRejected(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"immutable", `fn read(): Int { val value = 1 val value = 2 return value }`},
		{"mutable", `fn read(): Int { var value = 1 var value = 2 return value }`},
		{"mixed", `fn read(): Int { val value = 1 var value = 2 return value }`},
		{"parameter", `fn read(value: Int): Int { val value = 2 return value }`},
		{"global", `val value = 1 val value = 2`},
		{"loop", `fn read(): Int { for value in [1] { val value = 2 } return 1 }`},
		{"lambda", `fn read(): [Int] { return [1].map { value -> val value = 2 value } }`},
		{"listener", `event Changed(value: Int) on Changed { value -> val value = 2 }`},
		{"destructured", "fn read(): Int { val (value, value) = await {\n1\n2\n} return value }"},
		{"existing-before-tuple", "fn read(): Int { val value = 1 val (other, value) = await {\n1\n2\n} return value }"},
		{"existing-after-tuple", "fn read(): Int { val (value, other) = await {\n1\n2\n} val value = 3 return value }"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, sample.source)
			for _, diagnostic := range result.Errors {
				if strings.Contains(diagnostic.Message, "duplicate variable binding 'value'") {
					if diagnostic.Pos.Line == 0 || diagnostic.Pos.Col == 0 {
						t.Fatal("binding diagnostic has no source position", diagnostic)
					}
					return
				}
			}
			t.Fatal("duplicate binding was accepted", result.Errors)
		})
	}
}

func TestNestedVariableBindingsAndDiscardsRemainValid(t *testing.T) {
	for _, source := range []string{
		`fn read(value: Int): Int { if true { val value = "inner" val copy = value } return value }`,
		`fn read(): Int { val value = 1 when { true -> { val value = "left" } else -> { val value = false } } return value }`,
		`fn read(): Int { val value = 1 [1].forEach { value -> val copy = value } return value }`,
		`fn read(value: Int): Int { val _ = value val _ = value return value }`,
		"fn read(): Int { val (_, _) = await {\n1\n2\n} return 1 }",
	} {
		expectNoErrors(t, analyze(t, source))
	}
	result := analyze(t, `fn read(): Int { val _ = 1 return _ }`)
	expectError(t, result, "discard binding '_' cannot be read")
}

func TestVariableBindingFailurePreservesEarlierSymbol(t *testing.T) {
	a := New()
	scope := NewScope()
	first := &Symbol{Name: "value", Kind: SymVariable, Type: a.types["Int"], Mutable: false}
	scope.Define(first)
	a.checkValStmt(&ast.ValStmt{
		Name: "value", NamePos: token.Position{Line: 3, Col: 7}, Mutable: true,
		Value: &ast.Literal{Kind: token.String, Value: "different"},
	}, scope)
	if scope.LookupLocal("value") != first || first.Type != a.types["Int"] || first.Mutable {
		t.Fatal("duplicate declaration changed the earlier binding")
	}
	if len(a.errors) != 1 || a.errors[0].Pos.Line != 3 || a.errors[0].Pos.Col != 7 {
		t.Fatal("duplicate declaration did not retain its binding location", a.errors)
	}
	a.checkValStmt(&ast.ValStmt{Name: "_", Value: &ast.Literal{Kind: token.Int, Value: "1"}}, scope)
	if scope.LookupLocal("_") != nil || len(a.errors) != 1 {
		t.Fatal("discard introduced a named binding or another diagnostic")
	}
}

func TestEnumConstantMetadataTracksDeclarationScope(t *testing.T) {
	analyzer := New()
	enum := &ResolvedType{Kind: TypeEnum, Name: "Role", EnumValues: []string{"ADMIN"}}
	scope := NewScope()
	scope.Define(&Symbol{Name: "Role", Kind: SymEnum, Type: enum})
	expression := &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "ADMIN"}
	if analyzer.checkExpr(expression, scope) != enum || !expression.EnumConstant {
		t.Fatal("enum declaration member was not identified")
	}
	shadow := scope.Child()
	shadow.Define(&Symbol{Name: "Role", Kind: SymVariable, Type: enum})
	if analyzer.checkExpr(expression, shadow) != enum || expression.EnumConstant {
		t.Fatal("variable inherited enum declaration metadata")
	}
	expression.EnumConstant = true
	expression.Field = "UNKNOWN"
	if analyzer.checkExpr(expression, scope) != nil || expression.EnumConstant {
		t.Fatal("invalid member retained enum declaration metadata")
	}
}

func TestIdentifierNarrowingMetadataIsScopedAndRecomputed(t *testing.T) {
	analyzer := New()
	parent := NewScope()
	nullable := analyzer.types["Boolean"].AsNullable()
	parent.Define(&Symbol{Name: "value", Kind: SymParam, Type: nullable})
	guard := parent.Child()
	guard.Narrow("value", analyzer.types["Boolean"])
	identifier := &ast.Ident{Name: "value"}
	if actual := analyzer.checkExpr(identifier, guard); !actual.IsNonNullBoolean() || !identifier.NarrowedTag {
		t.Fatal("guard did not annotate the narrowed read")
	}
	if actual := analyzer.checkExpr(identifier, parent); !actual.Nullable || identifier.NarrowedTag {
		t.Fatal("narrowing survived reanalysis outside its guard")
	}
	guard.Define(&Symbol{Name: "value", Kind: SymVariable, Type: nullable})
	if actual := analyzer.checkExpr(identifier, guard); !actual.Nullable || identifier.NarrowedTag {
		t.Fatal("shadowed declaration inherited the old guard")
	}
}

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

func TestScopeLookupReadOnlyPreservesUsage(t *testing.T) {
	for _, used := range []bool{false, true} {
		parent := NewScope()
		outer := &Symbol{Name: "outer", Kind: SymVariable, Used: used}
		shadowed := &Symbol{Name: "shadow", Kind: SymVariable, Used: used}
		parent.Define(outer)
		parent.Define(shadowed)
		child := parent.Child()
		local := &Symbol{Name: "shadow", Kind: SymVariable, Used: used}
		child.Define(local)
		for _, sample := range []struct {
			scope *Scope
			name  string
			want  *Symbol
		}{
			{parent, "outer", outer}, {child, "outer", outer},
			{parent, "shadow", shadowed}, {child, "shadow", local},
			{child, "missing", nil}, {NewScope(), "missing", nil},
		} {
			if got := sample.scope.LookupReadOnly(sample.name); got != sample.want {
				t.Fatalf("read-only lookup %q resolved %p, want %p", sample.name, got, sample.want)
			}
		}
		if outer.Used != used || shadowed.Used != used || local.Used != used {
			t.Fatal("read-only lookup changed symbol usage")
		}
	}
}

func TestScopeLookupMarksOnlyResolvedSymbol(t *testing.T) {
	parent := NewScope()
	outer := &Symbol{Name: "outer", Kind: SymVariable}
	shadowed := &Symbol{Name: "shadow", Kind: SymVariable}
	parent.Define(outer)
	parent.Define(shadowed)
	child := parent.Child()
	local := &Symbol{Name: "shadow", Kind: SymVariable}
	child.Define(local)
	if got := child.Lookup("outer"); got != outer || !outer.Used {
		t.Fatal("ordinary lookup did not mark the parent symbol")
	}
	if got := child.Lookup("shadow"); got != local || !local.Used || shadowed.Used {
		t.Fatal("ordinary lookup changed the shadowed parent")
	}
	if got := child.Lookup("missing"); got != nil || shadowed.Used {
		t.Fatal("missing lookup changed an unrelated symbol")
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

func TestNarrowedTypeStopsAtShadowingBinding(t *testing.T) {
	outer := NewScope()
	text := &ResolvedType{Kind: TypeString, Name: "String"}
	integer := &ResolvedType{Kind: TypeInt, Name: "Int"}
	outer.Define(&Symbol{Name: "value", Type: text.AsNullable()})
	outer.Narrow("value", text)
	inherited := outer.Child()
	if inherited.ResolvedTypeOf("value") != text {
		t.Fatal("unshadowed narrowing was lost")
	}
	inner := inherited.Child()
	inner.Define(&Symbol{Name: "value", Type: integer})
	if inner.ResolvedTypeOf("value") != integer || inner.Child().ResolvedTypeOf("value") != integer {
		t.Fatal("outer narrowing replaced the inner binding")
	}
	if inner.ResolvedTypeOf("missing") != nil {
		t.Fatal("missing binding gained a type")
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
		d := spellingDistanceBelow(tt.a, tt.b, 3)
		if d != tt.expected {
			t.Errorf("editDistance(%q, %q) = %d, expected %d", tt.a, tt.b, d, tt.expected)
		}
	}
}

func TestEditDistanceEmptyStrings(t *testing.T) {
	if d := spellingDistanceBelow("", "abc", 3); d != 3 {
		t.Errorf("editDistance('', 'abc') = %d, want 3", d)
	}
	if d := spellingDistanceBelow("abc", "", 3); d != 3 {
		t.Errorf("editDistance('abc', '') = %d, want 3", d)
	}
	if d := spellingDistanceBelow("", "", 3); d != 0 {
		t.Errorf("editDistance('', '') = %d, want 0", d)
	}
}

func TestAnalyzerRegistersBuiltinsWithoutAllocatingEachSymbol(t *testing.T) {
	catalogAllocations := testing.AllocsPerRun(20, func() { BuiltinTypes() })
	analyzerAllocations := testing.AllocsPerRun(20, func() { New() })
	if extra := analyzerAllocations - catalogAllocations; extra >= float64(len(BuiltinTypes())) {
		t.Fatalf("initializing the analyzer around the builtin catalog allocated %.0f objects", extra)
	}
	first, second := New(), New()
	for name, typ := range first.types {
		symbol := first.scope.LookupLocal(name)
		if symbol == nil || symbol.Kind != SymType || symbol.Type != typ || second.scope.LookupLocal(name) == symbol {
			t.Fatalf("builtin %q was missing or shared a scope symbol", name)
		}
	}
}

func TestFindClosestUsesExistingNamesWithoutAllocating(t *testing.T) {
	analyzer := New()
	analyzer.scope.Define(&Symbol{Name: "retentionLimit", Kind: SymVariable})
	allocations := testing.AllocsPerRun(20, func() {
		if got := analyzer.findClosest("retentoinLimit"); got != "retentionLimit" {
			t.Fatalf("closest scope name = %q", got)
		}
		if got := analyzer.findClosest("Bolean"); got != "Boolean" {
			t.Fatalf("closest type name = %q", got)
		}
	})
	if allocations != 0 {
		t.Fatalf("suggesting existing names allocated %.0f objects", allocations)
	}
}

func TestFindClosestStringMatchesDistanceReference(t *testing.T) {
	names := []string{"", "a", "b", "ab", "aa", "abc", "abcd", "abcde", "abcdef",
		"User", "Usar", "Usor", "é", "É", "用户", "用戶", strings.Repeat("x", 128)}
	for _, target := range names {
		for shift := range names {
			candidates := append([]string{}, names[shift:]...)
			candidates = append(candidates, names[:shift]...)
			want, distance := "", 3
			// Prefixes also exercise missing exact matches, ties and the threshold.
			for count := 0; count <= len(candidates); count++ {
				if count > 0 {
					candidate := candidates[count-1]
					if d := referenceEditDistance(target, candidate); d < distance {
						want, distance = candidate, d
					}
				}
				if got := findClosestString(target, candidates[:count]); got != want {
					t.Fatalf("target %q, shift %d, count %d: closest = %q, want %q", target, shift, count, got, want)
				}
			}
		}
	}
}

func TestEditDistanceIdentifierLengths(t *testing.T) {
	for _, size := range []int{1, 63, 64, 65, 128} {
		left := strings.Repeat("a", size)
		right := strings.Repeat("a", size-1) + "b"
		for _, pair := range [][2]string{{left, right}, {right, left}} {
			if got := spellingDistanceBelow(pair[0], pair[1], 3); got != 1 {
				t.Fatalf("length %d: distance = %d, want 1", size, got)
			}
		}
		if got := spellingDistanceBelow(left, left+"xyz", 3); got != 3 {
			t.Fatalf("length %d: insertion distance = %d, want 3", size, got)
		}
	}
}

func TestEditDistanceShortNamesDoNotAllocate(t *testing.T) {
	var distance int
	allocations := testing.AllocsPerRun(100, func() {
		distance = spellingDistanceBelow("UserProfile", "UserProfiel", 3)
	})
	if distance != 2 || allocations != 0 {
		t.Fatalf("distance = %d, allocations = %g; want 2 and 0", distance, allocations)
	}
}

func TestEditDistanceMatchesMatrix(t *testing.T) {
	words := []string{"", "a", "b", "aa", "ab", "ba", "bb", "aba", "aab", "baa", "abcd", "dcba",
		"UserProfile", "UserProfiel", "é", "É", "用户", "用戶", "\x00", "\xff"}
	for _, left := range words {
		for _, right := range words {
			exact := referenceEditDistance(left, right)
			for limit := 1; limit <= 3; limit++ {
				if got, want := spellingDistanceBelow(left, right, limit), min(exact, limit); got != want {
					t.Fatalf("%q/%q with limit %d: got %d, want %d", left, right, limit, got, want)
				}
			}
		}
	}
}

func referenceEditDistance(left, right string) int {
	width := len(right) + 1
	matrix := make([]int, (len(left)+1)*width)
	for i := 0; i <= len(left); i++ {
		matrix[i*width] = i
	}
	for j := 0; j <= len(right); j++ {
		matrix[j] = j
	}
	for i := 1; i <= len(left); i++ {
		for j := 1; j <= len(right); j++ {
			cost := 0
			if left[i-1] != right[j-1] {
				cost = 1
			}
			matrix[i*width+j] = min(matrix[(i-1)*width+j]+1, matrix[i*width+j-1]+1, matrix[(i-1)*width+j-1]+cost)
		}
	}
	return matrix[len(matrix)-1]
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
