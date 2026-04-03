package lsp

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Document Tests ==========

func TestDocumentStoreOpenAndGet(t *testing.T) {
	store := NewDocumentStore()
	doc := store.Open("file:///test.luxo", 1, "model User { name: String }")

	if doc == nil {
		t.Fatal("expected document")
	}

	got := store.Get("file:///test.luxo")
	if got == nil {
		t.Fatal("expected to get document back")
	}
	if got.Version != 1 {
		t.Errorf("expected version 1, got %d", got.Version)
	}
}

func TestDocumentStoreUpdate(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	store.Update("file:///test.luxo", 2, "model User { name: String\nemail: String }")

	doc := store.Get("file:///test.luxo")
	if doc.Version != 2 {
		t.Errorf("expected version 2, got %d", doc.Version)
	}
}

func TestDocumentStoreClose(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	store.Close("file:///test.luxo")

	if store.Get("file:///test.luxo") != nil {
		t.Error("expected document to be removed")
	}
}

func TestDocumentDiagnostics(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: Stirng }")
	doc := store.Get("file:///test.luxo")

	diags := doc.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for typo 'Stirng'")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "Stirng") {
			found = true
			if d.Source != "luxo/semantic" {
				t.Errorf("expected source 'luxo/semantic', got %q", d.Source)
			}
			if d.Severity != 1 {
				t.Errorf("expected severity 1 (Error), got %d", d.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected diagnostic about 'Stirng', got: %v", diags)
	}
}

func TestDocumentDiagnosticsClean(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	diags := doc.Diagnostics()
	for _, d := range diags {
		if d.Severity == 1 {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
}

func TestDocumentWordAt(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	word := doc.WordAt(Position{Line: 0, Character: 7})
	if word != "User" {
		t.Errorf("expected 'User', got %q", word)
	}

	word = doc.WordAt(Position{Line: 0, Character: 0})
	if word != "model" {
		t.Errorf("expected 'model', got %q", word)
	}
}

func TestDocumentWordAtEmpty(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { }")
	doc := store.Get("file:///test.luxo")

	word := doc.WordAt(Position{Line: 0, Character: 13})
	if word != "" {
		t.Errorf("expected empty word at space, got %q", word)
	}
}

func TestDocumentWordAtOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User")
	doc := store.Get("file:///test.luxo")

	word := doc.WordAt(Position{Line: 99, Character: 0})
	if word != "" {
		t.Errorf("expected empty for out-of-bounds line, got %q", word)
	}

	word = doc.WordAt(Position{Line: 0, Character: 999})
	if word != "" {
		t.Errorf("expected empty for out-of-bounds col, got %q", word)
	}
}

func TestDocumentCharAt(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User")
	doc := store.Get("file:///test.luxo")

	ch := doc.CharAt(Position{Line: 0, Character: 5})
	if ch != ' ' {
		t.Errorf("expected space, got %c", ch)
	}

	ch = doc.CharAt(Position{Line: 99, Character: 0})
	if ch != 0 {
		t.Errorf("expected 0 for out-of-bounds, got %c", ch)
	}
}

func TestDocumentPrevChar(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "user.name")
	doc := store.Get("file:///test.luxo")

	ch := doc.PrevChar(Position{Line: 0, Character: 5})
	if ch != '.' {
		t.Errorf("expected '.', got %c", ch)
	}

	ch = doc.PrevChar(Position{Line: 0, Character: 0})
	if ch != 0 {
		t.Errorf("expected 0 for position 0, got %c", ch)
	}
}

func TestDocumentCrossFile(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///base.luxo", 1, "model Base { id: Int }")
	store.Open("file:///user.luxo", 1, "model User : Base { name: String }")
	doc := store.Get("file:///user.luxo")

	// cross-file: User inherits from Base
	if doc.Result == nil {
		t.Fatal("expected analysis result")
	}
	typ := doc.Result.Types["User"]
	if typ == nil {
		t.Fatal("expected User type")
	}
	if len(typ.Parents) != 1 || typ.Parents[0].Name != "Base" {
		t.Errorf("expected User to inherit from Base, got parents: %v", typ.Parents)
	}
}

// ========== URI Conversion Tests ==========

func TestURIToPath(t *testing.T) {
	tests := []struct {
		uri  string
		path string
	}{
		{"file:///Users/test/file.luxo", "/Users/test/file.luxo"},
		{"file:///path/with%20space/file.luxo", "/path/with space/file.luxo"},
		{"/plain/path.luxo", "/plain/path.luxo"},
	}
	for _, tt := range tests {
		got := URIToPath(tt.uri)
		if got != tt.path {
			t.Errorf("URIToPath(%q) = %q, want %q", tt.uri, got, tt.path)
		}
	}
}

func TestPathToURI(t *testing.T) {
	got := PathToURI("/Users/test/file.luxo")
	if got != "file:///Users/test/file.luxo" {
		t.Errorf("expected file:// URI, got %q", got)
	}

	got = PathToURI("relative.luxo")
	if got != "relative.luxo" {
		t.Errorf("expected unchanged relative path, got %q", got)
	}
}

func TestTokenAt(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	tok := doc.TokenAt(Position{Line: 0, Character: 0})
	if tok == nil {
		t.Fatal("expected token at 0:0")
	}
	if tok.Val != "model" {
		t.Errorf("expected 'model', got %q", tok.Val)
	}

	tok = doc.TokenAt(Position{Line: 0, Character: 7})
	if tok == nil {
		t.Fatal("expected token at 0:7")
	}
	if tok.Val != "User" {
		t.Errorf("expected 'User', got %q", tok.Val)
	}

	// out of bounds
	tok = doc.TokenAt(Position{Line: 99, Character: 0})
	if tok != nil {
		t.Error("expected nil for out-of-bounds position")
	}
}

// ========== tokenPosToRange Tests ==========

func TestTokenPosToRange(t *testing.T) {
	r := tokenPosToRange(token.Position{Line: 1, Col: 1}, 5)
	if r.Start.Line != 0 || r.Start.Character != 0 {
		t.Errorf("expected start 0:0, got %d:%d", r.Start.Line, r.Start.Character)
	}
	if r.End.Character != 5 {
		t.Errorf("expected end character 5, got %d", r.End.Character)
	}
}

func TestTokenPosToRangeZero(t *testing.T) {
	r := tokenPosToRange(token.Position{Line: 0, Col: 0}, 0)
	if r.Start.Line != 0 || r.Start.Character != 0 {
		t.Error("expected 0:0 for zero position")
	}
	if r.End.Character != 1 {
		t.Errorf("expected min length 1, got %d", r.End.Character)
	}
}

func TestDocumentDiagnosticsLexerError(t *testing.T) {
	// Directly construct a document with lexer errors to test the Diagnostics() branch
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "bad",
		lexErrors: []lexer.Error{
			{Pos: token.Position{File: "test.luxo", Line: 1, Col: 1}, Message: "unexpected character '~'"},
		},
	}

	diags := doc.Diagnostics()
	found := false
	for _, d := range diags {
		if d.Source == "luxo/lexer" {
			found = true
		}
	}
	if !found {
		t.Error("expected lexer error diagnostic")
	}
}

func TestDocumentDiagnosticsParserError(t *testing.T) {
	// Directly construct a document with parser errors to test the Diagnostics() branch
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "bad",
		parseErrors: []parser.Error{
			{Pos: token.Position{File: "test.luxo", Line: 1, Col: 1}, Message: "expected identifier"},
		},
	}

	diags := doc.Diagnostics()
	found := false
	for _, d := range diags {
		if d.Source == "luxo/parser" {
			found = true
		}
	}
	if !found {
		t.Error("expected parser error diagnostic")
	}
}

func TestDocumentDiagnosticsWarning(t *testing.T) {
	store := NewDocumentStore()
	// A model with a field using an unknown type generates a semantic error,
	// but we also want to test the warning path. We construct a doc with Result.Warnings manually.
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	// Inject a warning to cover the warning branch in Diagnostics()
	if doc.Result != nil {
		doc.Result.Warnings = append(doc.Result.Warnings, semantic.Warning{
			Pos:     token.Position{File: URIToPath("file:///test.luxo"), Line: 1, Col: 1},
			Message: "test warning",
		})
	}

	diags := doc.Diagnostics()
	found := false
	for _, d := range diags {
		if d.Severity == 2 && strings.Contains(d.Message, "test warning") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning diagnostic")
	}
}

func TestCharAtOutOfBoundsCol(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "abc")
	doc := store.Get("file:///test.luxo")

	ch := doc.CharAt(Position{Line: 0, Character: 100})
	if ch != 0 {
		t.Errorf("expected 0 for out-of-bounds col, got %c", ch)
	}
}

func TestURIToPathParseError(t *testing.T) {
	// Use a URI that url.Parse will fail on — control chars
	got := URIToPath("file://\x7f\x7f\x7f")
	// Should fall through to TrimPrefix fallback or return as-is
	if got == "" {
		t.Error("expected non-empty path for parse-error fallback")
	}
}

func TestDiagnosticsSemanticErrorFiltering(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///a.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///a.luxo")

	if doc.Result != nil {
		// Add an error for a different file — should be filtered out
		doc.Result.Errors = append(doc.Result.Errors, semantic.Error{
			Pos:        token.Position{File: "/other.luxo", Line: 1, Col: 1},
			Message:    "error in other file",
			Suggestion: "fix it",
		})
	}

	diags := doc.Diagnostics()
	for _, d := range diags {
		if strings.Contains(d.Message, "error in other file") {
			t.Error("should not include diagnostics from other files")
		}
	}
}

func TestDiagnosticsSemanticErrorWithSuggestion(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	if doc.Result != nil {
		doc.Result.Errors = append(doc.Result.Errors, semantic.Error{
			Pos:        token.Position{File: URIToPath("file:///test.luxo"), Line: 1, Col: 1},
			Message:    "unknown type",
			Suggestion: "did you mean String?",
		})
	}

	diags := doc.Diagnostics()
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "did you mean String?") {
			found = true
		}
	}
	if !found {
		t.Error("expected suggestion in diagnostic message")
	}
}

func TestDiagnosticsFileFiltering(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///main.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///main.luxo")

	if doc.Result == nil {
		t.Fatal("expected analysis result")
	}

	// Add semantic errors: one for this file, one for another file, one with empty file
	doc.Result.Errors = append(doc.Result.Errors,
		semantic.Error{
			Pos:     token.Position{File: URIToPath("file:///main.luxo"), Line: 1, Col: 1},
			Message: "error in main",
		},
		semantic.Error{
			Pos:     token.Position{File: "/other.luxo", Line: 1, Col: 1},
			Message: "error in other",
		},
		semantic.Error{
			Pos:     token.Position{File: "", Line: 1, Col: 1},
			Message: "error with empty file",
		},
	)
	// Add warnings similarly
	doc.Result.Warnings = append(doc.Result.Warnings,
		semantic.Warning{
			Pos:     token.Position{File: "/other.luxo", Line: 1, Col: 1},
			Message: "warning in other",
		},
		semantic.Warning{
			Pos:     token.Position{File: "", Line: 1, Col: 1},
			Message: "warning with empty file",
		},
	)

	diags := doc.Diagnostics()
	for _, d := range diags {
		if strings.Contains(d.Message, "error in other") {
			t.Error("should filter out errors from other files")
		}
		if strings.Contains(d.Message, "warning in other") {
			t.Error("should filter out warnings from other files")
		}
	}
	// "error with empty file" should pass through (empty file matches the continue condition as false)
	foundEmpty := false
	foundMain := false
	for _, d := range diags {
		if strings.Contains(d.Message, "error with empty file") {
			foundEmpty = true
		}
		if strings.Contains(d.Message, "error in main") {
			foundMain = true
		}
	}
	if !foundEmpty {
		t.Error("expected error with empty file to pass through filter")
	}
	if !foundMain {
		t.Error("expected error in main to be included")
	}
}

func TestUpdateNewDoc(t *testing.T) {
	store := NewDocumentStore()
	// Update a document that was never opened — should create it
	doc := store.Update("file:///new.luxo", 1, "model NewModel { }")
	if doc == nil {
		t.Fatal("expected document to be created on update")
	}
	if doc.Content != "model NewModel { }" {
		t.Errorf("unexpected content: %q", doc.Content)
	}
}

func TestFormatTypeHoverDefault(t *testing.T) {
	// Test the default branch (not model, enum, or sealed)
	typ := &semantic.ResolvedType{Kind: semantic.TypeCustom, Name: "MyType"}
	result := formatTypeHover("MyType", typ)
	if !strings.Contains(result, "type MyType") {
		t.Errorf("expected 'type MyType' for custom type hover, got: %s", result)
	}
}

func TestFormatTypeHoverNilFieldType(t *testing.T) {
	typ := &semantic.ResolvedType{
		Kind: semantic.TypeModel,
		Name: "Widget",
		Fields: map[string]*semantic.FieldInfo{
			"data": {Name: "data", Type: nil, Nullable: true},
		},
	}
	result := formatTypeHover("Widget", typ)
	if !strings.Contains(result, "model Widget") {
		t.Errorf("expected 'model Widget' in hover, got: %s", result)
	}
	if !strings.Contains(result, "?") {
		t.Error("expected nullable marker '?' in hover for nullable field")
	}
	// Should not panic when field.Type is nil
}

// TestFormatTypeHoverMultipleParents covers the i > 0 branch in model parent listing.
func TestFormatTypeHoverMultipleParents(t *testing.T) {
	typ := &semantic.ResolvedType{
		Kind: semantic.TypeModel,
		Name: "Child",
		Parents: []*semantic.ResolvedType{
			{Name: "ParentA"},
			{Name: "ParentB"},
		},
		Fields: map[string]*semantic.FieldInfo{},
	}
	result := formatTypeHover("Child", typ)
	if !strings.Contains(result, "ParentA") {
		t.Error("expected ParentA in hover")
	}
	if !strings.Contains(result, ", ParentB") {
		t.Error("expected ', ParentB' separator in hover for multiple parents")
	}
}

// TestFormatTypeHoverSealedVariantNoFields covers sealed variant with no fields.
func TestFormatTypeHoverSealedVariantNoFields(t *testing.T) {
	typ := &semantic.ResolvedType{
		Kind: semantic.TypeSealed,
		Name: "MySealed",
		Variants: []*semantic.SealedVariantInfo{
			{Name: "Empty"},
			{Name: "WithFields", Fields: []*semantic.FieldInfo{{Name: "code"}, {Name: "message"}}},
		},
	}
	result := formatTypeHover("MySealed", typ)
	if !strings.Contains(result, "sealed MySealed") {
		t.Errorf("expected 'sealed MySealed', got: %s", result)
	}
	if !strings.Contains(result, "Empty") {
		t.Error("expected variant 'Empty' in hover")
	}
	// "Empty" should NOT have parentheses since it has no fields
	// "WithField" should have parentheses
	if !strings.Contains(result, "WithFields(code, message)") {
		t.Errorf("expected 'WithFields(code, message)' in hover, got: %s", result)
	}
}

func TestFormatSymbolHover(t *testing.T) {
	sym := &semantic.Symbol{
		Name: "getUser",
		Kind: semantic.SymApi,
		Type: &semantic.ResolvedType{Name: "User"},
		Doc:  "Retrieves a user by ID",
	}
	result := formatSymbolHover(sym)
	if !strings.Contains(result, "api getUser") {
		t.Errorf("expected 'api getUser', got: %s", result)
	}
	if !strings.Contains(result, "User") {
		t.Error("expected return type 'User' in symbol hover")
	}
	if !strings.Contains(result, "Retrieves a user by ID") {
		t.Error("expected doc comment in symbol hover")
	}
}

func TestFormatSymbolHoverNoDoc(t *testing.T) {
	sym := &semantic.Symbol{
		Name: "helper",
		Kind: semantic.SymFn,
	}
	result := formatSymbolHover(sym)
	if !strings.Contains(result, "fn helper") {
		t.Errorf("expected 'fn helper', got: %s", result)
	}
	if strings.Contains(result, "---") {
		t.Error("should not have doc separator when no doc")
	}
}
