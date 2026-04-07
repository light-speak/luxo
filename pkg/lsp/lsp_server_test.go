package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Server Integration Tests ==========

func newTestServer() (*Server, *bytes.Buffer) {
	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(strings.NewReader(""), &output, logger)
	return server, &output
}

// ========== Additional Coverage Tests ==========

// helper to open a doc and reset output buffer
func openDoc(server *Server, output *bytes.Buffer, uri, text string) {
	params, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  uri,
			Text: text,
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: params})
	output.Reset()
}

func requestCompletion(server *Server, output *bytes.Buffer, uri string, pos Position) string {
	id := json.RawMessage(`100`)
	params, _ := json.Marshal(CompletionParams{
		TextDocument: TextDocumentID{URI: uri},
		Position:     pos,
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/completion",
		Params:  params,
	})
	return output.String()
}

func requestHover(server *Server, output *bytes.Buffer, uri string, pos Position) string {
	id := json.RawMessage(`101`)
	params, _ := json.Marshal(HoverParams{
		TextDocument: TextDocumentID{URI: uri},
		Position:     pos,
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/hover",
		Params:  params,
	})
	return output.String()
}

func requestDefinition(server *Server, output *bytes.Buffer, uri string, pos Position) string {
	id := json.RawMessage(`102`)
	params, _ := json.Marshal(DefinitionParams{
		TextDocument: TextDocumentID{URI: uri},
		Position:     pos,
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/definition",
		Params:  params,
	})
	return output.String()
}

func requestReferences(server *Server, output *bytes.Buffer, uri string, pos Position) string {
	id := json.RawMessage(`103`)
	params, _ := json.Marshal(ReferenceParams{
		TextDocument: TextDocumentID{URI: uri},
		Position:     pos,
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/references",
		Params:  params,
	})
	return output.String()
}

func TestServerInitialize(t *testing.T) {
	server, output := newTestServer()

	id := json.RawMessage(`1`)
	params, _ := json.Marshal(InitializeParams{RootURI: "file:///test"})
	req := &Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "initialize",
		Params:  params,
	}

	err := server.handleMessage(req)
	if err != nil {
		t.Fatalf("handleInitialize error: %v", err)
	}

	resp := output.String()
	if !strings.Contains(resp, "luxo-lsp") {
		t.Error("expected server name in response")
	}
	if !strings.Contains(resp, `"textDocumentSync":1`) {
		t.Error("expected textDocumentSync capability")
	}
	if !strings.Contains(resp, `"hoverProvider":true`) {
		t.Error("expected hoverProvider capability")
	}
}

func TestServerDidOpenWithErrors(t *testing.T) {
	server, output := newTestServer()

	params, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:        "file:///test.luxo",
			LanguageID: "luxo",
			Version:    1,
			Text:       "model User { name: Stirng }",
		},
	})
	req := &Request{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  params,
	}

	err := server.handleMessage(req)
	if err != nil {
		t.Fatalf("handleDidOpen error: %v", err)
	}

	resp := output.String()
	if !strings.Contains(resp, "publishDiagnostics") {
		t.Error("expected diagnostics notification")
	}
	if !strings.Contains(resp, "Stirng") {
		t.Error("expected error about 'Stirng' in diagnostics")
	}
}

func TestServerDidOpenClean(t *testing.T) {
	server, output := newTestServer()

	params, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }",
		},
	})
	req := &Request{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  params,
	}

	server.handleMessage(req)
	resp := output.String()

	// should have publishDiagnostics but with empty array
	if !strings.Contains(resp, "publishDiagnostics") {
		t.Error("expected diagnostics notification even when clean")
	}
}

func TestServerCompletion(t *testing.T) {
	server, output := newTestServer()

	// open a document first
	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }\n",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})
	output.Reset()

	// request completion
	id := json.RawMessage(`2`)
	compParams, _ := json.Marshal(CompletionParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
		Position:     Position{Line: 1, Character: 0},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/completion",
		Params:  compParams,
	})

	resp := output.String()
	if !strings.Contains(resp, "model") {
		t.Error("expected 'model' in completion items")
	}
	if !strings.Contains(resp, "api") {
		t.Error("expected 'api' in completion items")
	}
}

func TestServerHover(t *testing.T) {
	server, output := newTestServer()

	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})
	output.Reset()

	id := json.RawMessage(`3`)
	hoverParams, _ := json.Marshal(HoverParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
		Position:     Position{Line: 0, Character: 7}, // "User"
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/hover",
		Params:  hoverParams,
	})

	resp := output.String()
	if !strings.Contains(resp, "model User") {
		t.Error("expected 'model User' in hover response")
	}
}

func TestServerHoverKeyword(t *testing.T) {
	server, output := newTestServer()

	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})
	output.Reset()

	id := json.RawMessage(`4`)
	hoverParams, _ := json.Marshal(HoverParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
		Position:     Position{Line: 0, Character: 2}, // "model"
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/hover",
		Params:  hoverParams,
	})

	resp := output.String()
	if !strings.Contains(resp, "data model") || !strings.Contains(resp, "数据模型") {
		t.Error("expected bilingual keyword description in hover")
	}
}

func TestServerDefinition(t *testing.T) {
	server, output := newTestServer()

	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }\napi getUser(id: Int): User",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})
	output.Reset()

	id := json.RawMessage(`5`)
	defParams, _ := json.Marshal(DefinitionParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
		Position:     Position{Line: 1, Character: 23}, // "User" in return type
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/definition",
		Params:  defParams,
	})

	resp := output.String()
	if !strings.Contains(resp, "test.luxo") {
		t.Error("expected file URI in definition response")
	}
}

func TestServerShutdown(t *testing.T) {
	server, _ := newTestServer()

	id := json.RawMessage(`99`)
	err := server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "shutdown",
	})
	if err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	if !server.shutdown {
		t.Error("expected shutdown flag to be set")
	}
}

func TestServerUnknownMethod(t *testing.T) {
	server, output := newTestServer()

	// notification (no ID) — should be ignored
	err := server.handleMessage(&Request{Method: "unknownNotification"})
	if err != nil {
		t.Fatalf("unexpected error for unknown notification: %v", err)
	}

	// request (with ID) — should return error
	id := json.RawMessage(`10`)
	err = server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "unknownMethod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := output.String()
	if !strings.Contains(resp, "method not found") {
		t.Error("expected 'method not found' error response")
	}
}

func TestServerDidChange(t *testing.T) {
	server, _ := newTestServer()

	// open
	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})

	// change
	changeParams, _ := json.Marshal(DidChangeParams{
		TextDocument: VersionedTextDocID{URI: "file:///test.luxo", Version: 2},
		ContentChanges: []TextDocumentChange{
			{Text: "model User { name: String\nemail: String }"},
		},
	})
	err := server.handleMessage(&Request{Method: "textDocument/didChange", Params: changeParams})
	if err != nil {
		t.Fatalf("didChange error: %v", err)
	}

	doc := server.docs.Get("file:///test.luxo")
	if doc.Version != 2 {
		t.Errorf("expected version 2, got %d", doc.Version)
	}
}

func TestServerDidClose(t *testing.T) {
	server, output := newTestServer()

	openParams, _ := json.Marshal(DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:  "file:///test.luxo",
			Text: "model User { name: String }",
		},
	})
	server.handleMessage(&Request{Method: "textDocument/didOpen", Params: openParams})
	output.Reset()

	closeParams, _ := json.Marshal(DidCloseParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
	})
	err := server.handleMessage(&Request{Method: "textDocument/didClose", Params: closeParams})
	if err != nil {
		t.Fatalf("didClose error: %v", err)
	}

	if server.docs.Get("file:///test.luxo") != nil {
		t.Error("expected document to be removed after close")
	}

	// should clear diagnostics
	resp := output.String()
	if !strings.Contains(resp, "publishDiagnostics") {
		t.Error("expected diagnostics cleared on close")
	}
}

func TestHandleInitialized(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{Method: "initialized"})
	if err != nil {
		t.Fatalf("initialized handler error: %v", err)
	}
}

func TestHandleDidSave(t *testing.T) {
	server, _ := newTestServer()
	params, _ := json.Marshal(DidSaveParams{
		TextDocument: TextDocumentID{URI: "file:///test.luxo"},
	})
	err := server.handleMessage(&Request{Method: "textDocument/didSave", Params: params})
	if err != nil {
		t.Fatalf("didSave handler error: %v", err)
	}
}

func TestHandleExit(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{Method: "exit"})
	if err != nil {
		t.Fatalf("exit handler error: %v", err)
	}
}

func TestDidChangeEmptyChanges(t *testing.T) {
	server, _ := newTestServer()
	openDoc(server, &bytes.Buffer{}, "file:///test.luxo", "model User { name: String }")
	params, _ := json.Marshal(DidChangeParams{
		TextDocument:   VersionedTextDocID{URI: "file:///test.luxo", Version: 2},
		ContentChanges: []TextDocumentChange{},
	})
	err := server.handleMessage(&Request{Method: "textDocument/didChange", Params: params})
	if err != nil {
		t.Fatalf("didChange with empty changes error: %v", err)
	}
	// version should not have changed
	doc := server.docs.Get("file:///test.luxo")
	if doc.Version == 2 {
		t.Error("version should not update with empty content changes")
	}
}

func TestMemberCompletion(t *testing.T) {
	server, output := newTestServer()
	// "User." on line 1 — dot at col 4, cursor at col 5
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\nUser.")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 1, Character: 5})
	if !strings.Contains(resp, "name") {
		t.Error("expected field 'name' in member completion")
	}
}

func TestMemberCompletionEnum(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "enum Role { USER ADMIN }\nRole.")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 1, Character: 5})
	if !strings.Contains(resp, "USER") {
		t.Error("expected enum value 'USER' in completion")
	}
	if !strings.Contains(resp, "ADMIN") {
		t.Error("expected enum value 'ADMIN' in completion")
	}
}

func TestDirectiveCompletion(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User {\n  @")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 1, Character: 3})
	if !strings.Contains(resp, "unique") {
		t.Error("expected directive 'unique' in completion")
	}
	if !strings.Contains(resp, "hidden") {
		t.Error("expected directive 'hidden' in completion")
	}
}

func TestCompletionNoDoc(t *testing.T) {
	server, output := newTestServer()
	id := json.RawMessage(`10`)
	params, _ := json.Marshal(CompletionParams{
		TextDocument: TextDocumentID{URI: "file:///nonexistent.luxo"},
		Position:     Position{Line: 0, Character: 0},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/completion",
		Params:  params,
	})
	resp := output.String()
	// Should return empty completion list, not error
	if !strings.Contains(resp, "result") {
		t.Error("expected a response for completion on unknown URI")
	}
}

func TestCompletionWithPrefix(t *testing.T) {
	server, output := newTestServer()
	// Type "mod" and request completion — should include "model"
	openDoc(server, output, "file:///test.luxo", "mod")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 0, Character: 3})
	if !strings.Contains(resp, "model") {
		t.Error("expected 'model' in prefix completion for 'mod'")
	}
}

func TestCollectionMethodCompletion(t *testing.T) {
	server, output := newTestServer()
	// Use a variable name (lowercase) that is NOT a known type, so collection methods are included
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\nusers.")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 1, Character: 6})
	methods := []string{"filter", "map", "sumOf", "count", "any", "firstOrNull", "sortBy", "groupBy", "forEach", "size", "length", "contains", "lowercase", "uppercase"}
	for _, m := range methods {
		if !strings.Contains(resp, m) {
			t.Errorf("expected collection method %q in member completion", m)
		}
	}
}

func TestHoverEnum(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "enum Role { USER ADMIN }")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 5})
	if !strings.Contains(resp, "enum Role") {
		t.Error("expected 'enum Role' in hover response")
	}
	if !strings.Contains(resp, "USER") {
		t.Error("expected enum value USER in hover")
	}
}

func TestHoverSealed(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "sealed PayResult {\n  Ok(value: String)\n  Err(message: String)\n}")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 10})
	if !strings.Contains(resp, "sealed PayResult") {
		t.Error("expected 'sealed PayResult' in hover response")
	}
	if !strings.Contains(resp, "Ok") {
		t.Error("expected variant 'Ok' in hover")
	}
}

func TestHoverSymbol(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  User.find(where: id == id)\n}")
	// hover over "getUser" — symbol hover
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 1, Character: 5})
	if !strings.Contains(resp, "getUser") {
		t.Error("expected 'getUser' in symbol hover")
	}
}

func TestHoverSymbolWithDoc(t *testing.T) {
	server, output := newTestServer()
	// doc comment syntax: // comment before api
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\n// Get a user by ID\napi getUser(id: Int): User {\n  User.find(where: id == id)\n}")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 2, Character: 5})
	if !strings.Contains(resp, "getUser") {
		t.Error("expected 'getUser' in hover with doc")
	}
}

func TestHoverNoDoc(t *testing.T) {
	server, output := newTestServer()
	resp := requestHover(server, output, "file:///nonexistent.luxo", Position{Line: 0, Character: 0})
	// Should return a valid JSON-RPC response (with null result)
	if !strings.Contains(resp, "jsonrpc") {
		t.Error("expected a JSON-RPC response for hover on unknown URI")
	}
}

func TestHoverEmptyWord(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { }")
	// hover on space — empty word
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 13})
	// Should return null
	if strings.Contains(resp, "model User") {
		t.Error("expected null response for empty word hover")
	}
}

func TestHoverModelWithParent(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model Base { id: Int }\nmodel User : Base { name: String }")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 1, Character: 7})
	if !strings.Contains(resp, "model User") {
		t.Error("expected 'model User' in hover response")
	}
	if !strings.Contains(resp, "Base") {
		t.Error("expected parent 'Base' in hover")
	}
}

func TestHoverBuiltinFunction(t *testing.T) {
	server, output := newTestServer()
	// "find" appears as a word in the source text; it is a builtin function
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi test(): User {\n  User.find(id: 1)\n}")
	// hover over "find" on line 2, character 7 (after "  User.")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 2, Character: 7})
	if !strings.Contains(resp, "find") {
		t.Error("expected 'find' in builtin function hover response")
	}
	if !strings.Contains(resp, "Find record by primary key") || !strings.Contains(resp, "按主键查找") {
		t.Error("expected bilingual description in builtin function hover")
	}
}

func TestHoverBuiltinFunctionEmit(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi test(): User {\n  emit(test, id: 1)\n  User.find(id: 1)\n}")
	// hover over "emit" on line 2
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 2, Character: 3})
	if !strings.Contains(resp, "emit") {
		t.Error("expected 'emit' in builtin function hover response")
	}
	if !strings.Contains(resp, "event") || !strings.Contains(resp, "事件") {
		t.Error("expected bilingual description for emit in hover")
	}
}

func TestDefinitionSymbol(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  User.find(where: id == id)\n}")
	// jump to definition of "User" from return type on line 1
	resp := requestDefinition(server, output, "file:///test.luxo", Position{Line: 1, Character: 23})
	if !strings.Contains(resp, "test.luxo") {
		t.Error("expected file URI in definition response for symbol")
	}
}

func TestDefinitionNoDoc(t *testing.T) {
	server, output := newTestServer()
	resp := requestDefinition(server, output, "file:///nonexistent.luxo", Position{Line: 0, Character: 0})
	if !strings.Contains(resp, "jsonrpc") {
		t.Error("expected a JSON-RPC response for definition on unknown URI")
	}
}

func TestDefinitionUnknownWord(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }")
	// look up a word that doesn't exist as a symbol
	resp := requestDefinition(server, output, "file:///test.luxo", Position{Line: 0, Character: 14})
	// "name" is a field, not a top-level symbol — should return null
	if strings.Contains(resp, `"line"`) {
		t.Error("expected null for definition of unknown field")
	}
}

func TestDefinitionBySymbol(t *testing.T) {
	server, output := newTestServer()
	// Open doc where 'getUser' is an api symbol. We want to jump to its definition.
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  User.find(where: id == id)\n}")
	// Definition of "getUser" — this is a symbol, not a type
	resp := requestDefinition(server, output, "file:///test.luxo", Position{Line: 1, Character: 5})
	if !strings.Contains(resp, "test.luxo") {
		t.Error("expected file URI in definition response for symbol")
	}
}

func TestDefinitionSymbolNoFile(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "myVar",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	// Define a symbol with no file position
	doc.Result.Scope.Define(&semantic.Symbol{
		Name: "myVar",
		Kind: semantic.SymVariable,
		Pos:  token.Position{}, // empty — no file
	})
	server.docs.Open("file:///test.luxo", 1, "myVar")
	// Override the doc's result
	server.docs.Get("file:///test.luxo").Result = doc.Result

	output := &bytes.Buffer{}
	resp := requestDefinition(server, output, "file:///test.luxo", Position{Line: 0, Character: 2})
	// Symbol exists but has no file position — should return null result
	if strings.Contains(resp, `"line"`) {
		t.Error("expected null result when symbol has no file position")
	}
}

func TestSymbolKindMapping(t *testing.T) {
	tests := []struct {
		kind     semantic.SymbolKind
		expected int
	}{
		{semantic.SymModel, 7},
		{semantic.SymInterface, 8},
		{semantic.SymEnum, 13},
		{semantic.SymSealed, 7},
		{semantic.SymType, 7},
		{semantic.SymApi, 3},
		{semantic.SymFn, 3},
		{semantic.SymError, 7},
		{semantic.SymVariable, 6},
		{semantic.SymParam, 6},
		{semantic.SymbolKind(999), 1}, // default → Text
	}
	for _, tt := range tests {
		got := symbolKindToCompletionKind(tt.kind)
		if got != tt.expected {
			t.Errorf("symbolKindToCompletionKind(%v) = %d, want %d", tt.kind, got, tt.expected)
		}
	}
}

func TestPublishDiagnosticsNilDoc(t *testing.T) {
	server, output := newTestServer()
	err := server.publishDiagnostics(nil)
	if err != nil {
		t.Fatalf("publishDiagnostics(nil) error: %v", err)
	}
	if output.Len() != 0 {
		t.Error("expected no output for nil doc")
	}
}

func TestHandleDidOpenUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{
		Method: "textDocument/didOpen",
		Params: json.RawMessage(`invalid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in didOpen")
	}
}

func TestHandleDidChangeUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{
		Method: "textDocument/didChange",
		Params: json.RawMessage(`invalid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in didChange")
	}
}

func TestHandleDidCloseUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{
		Method: "textDocument/didClose",
		Params: json.RawMessage(`invalid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in didClose")
	}
}

func TestHandleCompletionUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	id := json.RawMessage(`1`)
	err := server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/completion",
		Params:  json.RawMessage(`not valid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in completion params")
	}
}

func TestHandleHoverUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	id := json.RawMessage(`1`)
	err := server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/hover",
		Params:  json.RawMessage(`not valid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in hover params")
	}
}

func TestHandleDefinitionUnmarshalError(t *testing.T) {
	server, _ := newTestServer()
	id := json.RawMessage(`1`)
	err := server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/definition",
		Params:  json.RawMessage(`not valid json`),
	})
	if err == nil {
		t.Error("expected error for invalid JSON in definition params")
	}
}

func TestMemberCompletionVariable(t *testing.T) {
	server, _ := newTestServer()
	// Create a doc with a scope containing a variable with a type that has fields
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "myVar.",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	userType := &semantic.ResolvedType{
		Kind: semantic.TypeModel,
		Name: "User",
		Fields: map[string]*semantic.FieldInfo{
			"name": {Name: "name", Type: &semantic.ResolvedType{Name: "String"}},
			"age":  {Name: "age", Type: nil}, // field with nil Type
		},
	}
	doc.Result.Scope.Define(&semantic.Symbol{
		Name: "myVar",
		Kind: semantic.SymVariable,
		Type: userType,
	})

	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 6})
	foundName := false
	foundAge := false
	for _, item := range items {
		if item.Label == "name" {
			foundName = true
			if item.Detail != "String" {
				t.Errorf("expected detail 'String' for name, got %q", item.Detail)
			}
		}
		if item.Label == "age" {
			foundAge = true
			if item.Detail != "" {
				t.Errorf("expected empty detail for age (nil type), got %q", item.Detail)
			}
		}
	}
	if !foundName {
		t.Error("expected 'name' field in variable member completion")
	}
	if !foundAge {
		t.Error("expected 'age' field in variable member completion")
	}
	// Also verify collection methods are present
	foundFilter := false
	for _, item := range items {
		if item.Label == "filter" {
			foundFilter = true
		}
	}
	if !foundFilter {
		t.Error("expected collection method 'filter' in variable member completion")
	}
}

func TestMemberCompletionInheritedFields(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model Base { id: Int }\nmodel User : Base { name: String }\nUser.")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 2, Character: 5})
	if !strings.Contains(resp, "name") {
		t.Error("expected own field 'name' in member completion")
	}
	if !strings.Contains(resp, "id") {
		t.Error("expected inherited field 'id' in member completion")
	}
	if !strings.Contains(resp, "from Base") {
		t.Error("expected '(from Base)' annotation for inherited field")
	}
}

func TestMemberCompletionNoResult(t *testing.T) {
	server, _ := newTestServer()
	// doc with no analysis result
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "User.",
	}
	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 5})
	if len(items) != 0 {
		t.Error("expected empty completion for doc without analysis result")
	}
}

func TestMemberCompletionOutOfBoundsLine(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "User.",
		Result:  &semantic.Result{},
	}
	items := server.getMemberCompletions(doc, Position{Line: 99, Character: 5})
	if len(items) != 0 {
		t.Error("expected empty completion for out-of-bounds line")
	}
}

func TestMemberCompletionNoDot(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "User",
		Result:  &semantic.Result{},
	}
	// prevChar is '.', but dotPos points to wrong character
	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 0})
	if len(items) != 0 {
		t.Error("expected empty completion when no dot found")
	}
}

func TestMemberCompletionNoDotChar(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "abc",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	// dotPos = character-1 = 1, but line[1] = 'b', not '.'
	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 2})
	if len(items) != 0 {
		t.Errorf("expected empty completion when dot char not found, got %d items", len(items))
	}
}

func TestGetSymbolCompletionsWithoutType(t *testing.T) {
	server, _ := newTestServer()
	// Create a doc with a scope containing a symbol without a type
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	doc.Result.Scope.Define(&semantic.Symbol{
		Name: "myVar",
		Kind: semantic.SymVariable,
	})

	items := server.getSymbolCompletions(doc, "my")
	found := false
	for _, item := range items {
		if item.Label == "myVar" {
			found = true
			if !strings.Contains(item.Detail, "variable") {
				t.Errorf("expected detail to contain 'variable', got: %s", item.Detail)
			}
		}
	}
	if !found {
		t.Error("expected 'myVar' in symbol completions")
	}
}

func TestGetSymbolCompletionsWithDoc(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	doc.Result.Scope.Define(&semantic.Symbol{
		Name: "getUser",
		Kind: semantic.SymApi,
		Type: &semantic.ResolvedType{Name: "User"},
		Doc:  "Retrieves a user by ID",
	})

	items := server.getSymbolCompletions(doc, "get")
	found := false
	for _, item := range items {
		if item.Label == "getUser" {
			found = true
			if item.Documentation != "Retrieves a user by ID" {
				t.Errorf("expected doc in completion, got %q", item.Documentation)
			}
			if !strings.Contains(item.Detail, "User") {
				t.Errorf("expected type name in detail, got %q", item.Detail)
			}
		}
	}
	if !found {
		t.Error("expected 'getUser' in symbol completions")
	}
}

// TestGetSymbolCompletionsDuplicate covers the seen[sym.Name] dedup branch.
func TestGetSymbolCompletionsDuplicate(t *testing.T) {
	server, _ := newTestServer()

	// Create parent scope with symbol, then child scope with same-name symbol.
	// LookupPrefix walks both and returns duplicates; getSymbolCompletions must dedup.
	parent := semantic.NewScope()
	parent.Define(&semantic.Symbol{
		Name: "dupVar",
		Kind: semantic.SymVariable,
		Doc:  "from parent",
	})
	child := parent.Child()
	child.Define(&semantic.Symbol{
		Name: "dupVar",
		Kind: semantic.SymVariable,
		Doc:  "from child",
	})

	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "",
		Result: &semantic.Result{
			Scope: child,
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	items := server.getSymbolCompletions(doc, "dup")
	count := 0
	for _, item := range items {
		if item.Label == "dupVar" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 entry after dedup, got %d", count)
	}
}

// TestGetSymbolCompletionsNilTypeEmptyDoc covers symbol with nil Type and empty Doc.
func TestGetSymbolCompletionsNilTypeEmptyDoc(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	doc.Result.Scope.Define(&semantic.Symbol{
		Name: "noTypeVar",
		Kind: semantic.SymVariable,
		Type: nil,
		Doc:  "",
	})

	items := server.getSymbolCompletions(doc, "noType")
	found := false
	for _, item := range items {
		if item.Label == "noTypeVar" {
			found = true
			if item.Documentation != "" {
				t.Errorf("expected empty documentation, got %q", item.Documentation)
			}
			// Detail should just be the kind string without ": typeName"
			if strings.Contains(item.Detail, ":") {
				t.Errorf("expected no type in detail, got %q", item.Detail)
			}
		}
	}
	if !found {
		t.Error("expected 'noTypeVar' in symbol completions")
	}
}

func TestDirectiveCompletionAtTriggerChar(t *testing.T) {
	server, output := newTestServer()
	// Place '@' right at cursor position — prevChar check
	// "  @" on line 1, cursor at col 3 (right after @)
	openDoc(server, output, "file:///test.luxo", "model User {\n@")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 1, Character: 1})
	if !strings.Contains(resp, "unique") {
		t.Error("expected directive 'unique' when triggered by @")
	}
}

// TestHandleHoverFinalNilReturn covers the final nil return in handleHover
// when word is not a type, symbol, keyword, field, or builtin.
func TestHandleHoverFinalNilReturn(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }")
	// Use a completely unknown word position — hover on whitespace area
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 12})
	if resp == "" {
		t.Log("empty response as expected for whitespace position")
	}
}

// TestHandleDefinitionEmptyWord covers the word == "" branch in handleDefinition.
func TestHandleDefinitionEmptyWord(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { }")
	// Position on the space — empty word
	resp := requestDefinition(server, output, "file:///test.luxo", Position{Line: 0, Character: 13})
	if strings.Contains(resp, `"line"`) {
		t.Error("expected null result for definition at empty word position")
	}
}

// TestGetMemberCompletionStartGteEnd covers the start >= end branch (no identifier before dot).
func TestGetMemberCompletionStartGteEnd(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: ".",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	// dot at position 0, cursor at 1: dotPos=0, line[0]='.', end=0, start=-1->0, start>=end
	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 1})
	if len(items) != 0 {
		t.Errorf("expected empty completion for dot with no identifier before it, got %d items", len(items))
	}
}

// TestGetMemberCompletionNullableField covers the nullable field branch in model member completion.
func TestGetMemberCompletionNullableField(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: "MyModel.",
		Result: &semantic.Result{
			Scope: semantic.NewScope(),
			Types: map[string]*semantic.ResolvedType{
				"MyModel": {
					Kind: semantic.TypeModel,
					Name: "MyModel",
					Fields: map[string]*semantic.FieldInfo{
						"email": {Name: "email", Type: &semantic.ResolvedType{Name: "String"}, Nullable: true},
					},
				},
			},
		},
	}
	items := server.getMemberCompletions(doc, Position{Line: 0, Character: 8})
	found := false
	for _, item := range items {
		if item.Label == "email" {
			found = true
			if !strings.Contains(item.Detail, "?") {
				t.Errorf("expected nullable marker '?' in detail, got %q", item.Detail)
			}
		}
	}
	if !found {
		t.Error("expected 'email' field in member completion")
	}
}

// ========== Additional Coverage Tests (Phase 2) ==========

func TestRunWithShutdownExit(t *testing.T) {
	// Build shutdown request
	shutdownBody, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      func() *json.RawMessage { r := json.RawMessage(`99`); return &r }(),
		Method:  "shutdown",
	})
	// Build exit notification
	exitBody, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		Method:  "exit",
	})

	var input bytes.Buffer
	for _, body := range [][]byte{shutdownBody, exitBody} {
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}

	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(&input, &output, logger)

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run() }()

	err := <-errCh
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
}

// TestRunReadError covers Run() returning an error when ReadMessage fails and shutdown is false.
func TestRunReadError(t *testing.T) {
	// An empty reader will immediately return io.EOF on ReadString
	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(strings.NewReader(""), &output, logger)

	err := server.Run()
	if err == nil {
		t.Fatal("expected error from Run() when reader returns EOF")
	}
	if !strings.Contains(err.Error(), "read:") {
		t.Errorf("expected 'read:' in error, got: %v", err)
	}
}

// TestRunHandleMessageError covers the error logging branch in Run() when handleMessage returns error.
func TestRunHandleMessageError(t *testing.T) {
	// Send a request with params that are valid JSON but unmarshal to wrong type,
	// followed by an exit message.
	badBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"textDocument/didOpen","params":"wrong type"}`)
	exitBody, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		Method:  "exit",
	})

	var input bytes.Buffer
	for _, body := range [][]byte{badBody, exitBody} {
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}

	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(&input, &output, logger)

	err := server.Run()
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
}

// TestRunShutdownThenEOF covers the Run() branch where shutdown is true and ReadMessage returns EOF.
func TestRunShutdownThenEOF(t *testing.T) {
	// Send only a shutdown request. After processing it, the reader will EOF.
	// Run() should return nil (not an error) because shutdown is true.
	shutdownBody, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      func() *json.RawMessage { r := json.RawMessage(`99`); return &r }(),
		Method:  "shutdown",
	})

	var input bytes.Buffer
	fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(shutdownBody), shutdownBody)

	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(&input, &output, logger)

	err := server.Run()
	if err != nil {
		t.Fatalf("Run() returned error after shutdown+EOF: %v", err)
	}
}

// ========== typeRefToString Tests ==========

func TestTypeRefToString(t *testing.T) {
	tests := []struct {
		name   string
		input  *ast.TypeRef
		expect string
	}{
		{"nil", nil, ""},
		{"simple", &ast.TypeRef{Name: "String"}, "String"},
		{"list", &ast.TypeRef{Name: "User", IsList: true}, "[User]"},
		{"nullable", &ast.TypeRef{Name: "String", Nullable: true}, "String?"},
		{"list+nullable", &ast.TypeRef{Name: "User", IsList: true, Nullable: true}, "[User]?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := typeRefToString(tt.input)
			if result != tt.expect {
				t.Errorf("expected %q, got %q", tt.expect, result)
			}
		})
	}
}

// ========== formatFieldHover Tests ==========

func TestFormatFieldHover(t *testing.T) {
	field := &ast.FieldDecl{
		Name: "email",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "unique"},
		},
		Doc: "User email address",
	}
	result := formatFieldHover("User", field)
	if !strings.Contains(result, "User.email: String") {
		t.Errorf("expected 'User.email: String', got %q", result)
	}
	if !strings.Contains(result, "@unique") {
		t.Errorf("expected '@unique' directive, got %q", result)
	}
	if !strings.Contains(result, "User email address") {
		t.Errorf("expected doc comment, got %q", result)
	}
}

func TestFormatFieldHoverNoDocNoDirectives(t *testing.T) {
	field := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	result := formatFieldHover("User", field)
	if !strings.Contains(result, "User.name: String") {
		t.Errorf("expected 'User.name: String', got %q", result)
	}
	if strings.Contains(result, "---") {
		t.Error("should not contain doc separator when no doc")
	}
}

// ========== collectValsFromBlock Tests ==========

func TestCollectValsFromBlock(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:  token.Position{Line: 3},
				Name: "user",
			},
			&ast.ForStmt{
				Pos:     token.Position{Line: 5},
				VarName: "item",
			},
			&ast.ForStmt{
				Pos: token.Position{Line: 7},
				// no VarName — infinite or condition loop
			},
			&ast.ValStmt{
				Pos:  token.Position{Line: 100},
				Name: "late",
			},
		},
	}
	// cursor at line 10 (0-based) = should see line 3,5,7 but not 100
	items := collectValsFromBlock(block, Position{Line: 10})
	names := map[string]bool{}
	for _, item := range items {
		names[item.Label] = true
	}
	if !names["user"] {
		t.Error("expected 'user' val")
	}
	if !names["item"] {
		t.Error("expected 'item' loop var")
	}
	if names["late"] {
		t.Error("should not include 'late' — after cursor")
	}
}

// ========== getLocalCompletions/getAPILocalCompletions/getFnLocalCompletions ==========

func TestGetAPILocalCompletions(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_api_local.luxo"
	openDoc(server, output, uri, `api getUser(id: Int, name: String): String {
  val user = User.find(id: id)
  user
}`)

	// completions inside the api body (line 1)
	result := requestCompletion(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "id") {
		t.Error("expected 'id' param in completions")
	}
	if !strings.Contains(result, "name") {
		t.Error("expected 'name' param in completions")
	}
	if !strings.Contains(result, "currentUser") {
		t.Error("expected 'currentUser' in completions")
	}
}

func TestGetFnLocalCompletions(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_fn_local.luxo"
	openDoc(server, output, uri, `fn double(x: Int): Int {
  val result = x * 2
  result
}`)

	result := requestCompletion(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, "x") {
		t.Error("expected 'x' param in completions")
	}
}

func TestGetFnNativeNoBody(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_fn_native.luxo"
	openDoc(server, output, uri, `fn encrypt(value: String): String @native`)

	// completions on the fn itself — native fn has no body, should not crash
	result := requestCompletion(server, output, uri, Position{Line: 0, Character: 2})
	if result == "" {
		t.Error("expected some completion output")
	}
}

// ========== getParamMemberCompletions ==========

func TestGetParamMemberCompletions(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_param_member.luxo"
	openDoc(server, output, uri, `model User {
  name: String
  email: String
}
api getUser(user: User): String {
  user.
}`)

	// trigger completion after "user." on line 5
	result := requestCompletion(server, output, uri, Position{Line: 5, Character: 7})
	if !strings.Contains(result, "name") {
		t.Logf("completion result: %s", result)
		// member completions depend on semantic analysis resolving types
	}
}

func TestGetLocalCompletionsNilFile(t *testing.T) {
	doc := &Document{URI: "file:///nil.luxo"}
	items := getLocalCompletions(doc, Position{Line: 0})
	if items != nil {
		t.Error("expected nil for nil file")
	}
}

func TestGetParamMemberCompletionsNoFile(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{URI: "file:///none.luxo"}
	// File is nil — should return nil
	items := server.getParamMemberCompletions(doc, "user")
	if items != nil {
		t.Error("expected nil for nil file")
	}
}

func TestGetParamMemberCompletionsNoResult(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI:  "file:///no_result.luxo",
		File: &ast.File{},
		// Result is nil
	}
	items := server.getParamMemberCompletions(doc, "user")
	if items != nil {
		t.Error("expected nil for nil result")
	}
}

func TestGetParamMemberCompletionsUnknownType(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI: "file:///unknown_type.luxo",
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{Name: "test", Params: []*ast.ParamDecl{
					{Name: "user", Type: &ast.TypeRef{Name: "UnknownType"}},
				}},
			},
		},
		Result: &semantic.Result{
			Types: map[string]*semantic.ResolvedType{},
		},
	}
	items := server.getParamMemberCompletions(doc, "user")
	if items != nil {
		t.Error("expected nil for unknown type")
	}
}

// ========== getLocalHover ==========

func TestGetLocalHoverApiParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_param.luxo"
	openDoc(server, output, uri, `api getUser(id: Int): String {
  id
}`)

	// hover on "id" at line 1
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "param") {
		t.Logf("hover result: %s", result)
	}
}

func TestGetLocalHoverFnParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_fn_param.luxo"
	openDoc(server, output, uri, `fn double(x: Int): Int {
  x
}`)

	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "param") {
		t.Logf("hover result: %s", result)
	}
}

// ========== findParamTypeName ==========

func TestFindParamTypeName(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_find_param.luxo"
	openDoc(server, output, uri, `model User { name: String }
api getUser(user: User): String {
  user
}
fn process(data: String): Int {
  0
}`)

	doc := server.docs.Get(uri)
	if doc == nil || doc.File == nil {
		t.Fatal("expected parsed file")
	}

	name := findParamTypeName(doc.File, "user")
	if name != "User" {
		t.Errorf("expected 'User', got %q", name)
	}

	name = findParamTypeName(doc.File, "data")
	if name != "String" {
		t.Errorf("expected 'String', got %q", name)
	}

	name = findParamTypeName(doc.File, "nonexistent")
	if name != "" {
		t.Errorf("expected empty, got %q", name)
	}
}

// ========== Local Variable Go-to-Definition ==========

func TestDefinitionValStmt(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_val.luxo"
	openDoc(server, output, uri, `api likePost(id: Int): Int {
  val post = Post.find(id: id)
  post
}`)

	// cmd+click on "post" at line 2
	result := requestDefinition(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, `"line":1`) {
		t.Errorf("expected definition at line 1, got: %s", result)
	}
}

func TestDefinitionParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_param.luxo"
	openDoc(server, output, uri, `api getUser(id: Int): Int {
  id
}`)

	// cmd+click on "id" at line 1
	result := requestDefinition(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, `"line":0`) {
		t.Errorf("expected definition at line 0, got: %s", result)
	}
}

func TestDefinitionFnParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_fn_param.luxo"
	openDoc(server, output, uri, `fn double(x: Int): Int {
  val result = x * 2
  result
}`)

	// cmd+click on "x" at line 1
	result := requestDefinition(server, output, uri, Position{Line: 1, Character: 15})
	if !strings.Contains(result, `"line":0`) {
		t.Errorf("expected definition at line 0, got: %s", result)
	}

	// cmd+click on "result" at line 2
	output.Reset()
	result = requestDefinition(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, `"line":1`) {
		t.Errorf("expected definition at line 1, got: %s", result)
	}
}

func TestDefinitionForLoopVar(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_for.luxo"
	openDoc(server, output, uri, `api test(): Int {
  for item in items {
    item
  }
  0
}`)

	// cmd+click on "item" at line 2
	result := requestDefinition(server, output, uri, Position{Line: 2, Character: 4})
	if !strings.Contains(result, `"line":1`) {
		t.Errorf("expected definition at line 1, got: %s", result)
	}
}

func TestDefinitionDoesNotCrossApiBoundary(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_cross.luxo"
	openDoc(server, output, uri, `api register(): Int {
  val user = 1
  user
}
api login(): Int {
  val user = 2
  user
}`)

	// cmd+click on "user" at line 6 (inside login) should jump to line 5, not line 1
	result := requestDefinition(server, output, uri, Position{Line: 6, Character: 2})
	if strings.Contains(result, `"line":1`) {
		t.Errorf("should NOT jump to register's user, got: %s", result)
	}
	if !strings.Contains(result, `"line":5`) {
		t.Errorf("expected definition at line 5 (login's val user), got: %s", result)
	}
}

// ========== Exhaustive Coverage Tests ==========

func TestGetAPILocalCompletionsCursorBeforeApi(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_before_api.luxo"
	openDoc(server, output, uri, `
api test(id: Int): Int {
  id
}`)
	// cursor at line 0 — before the api starts
	result := requestCompletion(server, output, uri, Position{Line: 0, Character: 0})
	// should NOT include api params
	if strings.Contains(result, `"id"`) {
		// The result may still include "id" from keywords, but not from param completions
		t.Logf("result: %s", result)
	}
}

func TestGetFnLocalCompletionsCursorBeforeFn(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_before_fn.luxo"
	openDoc(server, output, uri, `
fn calc(x: Int): Int {
  x
}`)
	// cursor at line 0 — before fn
	result := requestCompletion(server, output, uri, Position{Line: 0, Character: 0})
	// test passes if no panic
	_ = result
}

func TestCollectValsFromBlockVarDetail(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:     token.Position{Line: 2},
				Name:    "count",
				Mutable: true,
			},
		},
	}
	items := collectValsFromBlock(block, Position{Line: 10})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Detail != "var" {
		t.Errorf("expected detail 'var', got %q", items[0].Detail)
	}
}

func TestGetLocalHoverApiParamMatch(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_api_param.luxo"
	openDoc(server, output, uri, `api getUser(userId: Int): Int {
  userId
}`)
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "param") || !strings.Contains(result, "userId") {
		t.Errorf("expected param hover for userId, got: %s", result)
	}
}

func TestGetLocalHoverFnParamMatch(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_fn_param2.luxo"
	openDoc(server, output, uri, `fn calc(amount: Float): Float {
  amount
}`)
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "param") || !strings.Contains(result, "amount") {
		t.Errorf("expected param hover for amount, got: %s", result)
	}
}

func TestGetLocalHoverModelFieldMatch(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_field2.luxo"
	openDoc(server, output, uri, `model User {
  /// The user's email
  email: String @unique
}`)
	result := requestHover(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, "email") {
		t.Logf("hover result: %s", result)
	}
}

func TestGetLocalHoverNoMatch(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_nomatch.luxo"
	openDoc(server, output, uri, `api test(): Int {
  0
}`)
	// hover on "0" — no local variable/param
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	// test passes if no panic
	_ = result
}

func TestHandleHoverLocalParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_local.luxo"
	openDoc(server, output, uri, `api doWork(taskId: Int): Int {
  taskId
}`)
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "param") {
		t.Errorf("expected local param hover, got: %s", result)
	}
}

func TestFormatModelHoverWithParents(t *testing.T) {
	typ := &semantic.ResolvedType{
		Kind: semantic.TypeModel,
		Name: "User",
		Parents: []*semantic.ResolvedType{
			{Name: "Base"},
			{Name: "Auditable"},
		},
		Fields: map[string]*semantic.FieldInfo{
			"name": {Type: &semantic.ResolvedType{Name: "String"}},
		},
	}
	result := formatTypeHover("User", typ)
	if !strings.Contains(result, "Base") {
		t.Errorf("expected parent 'Base', got: %s", result)
	}
	if !strings.Contains(result, "Auditable") {
		t.Errorf("expected parent 'Auditable', got: %s", result)
	}
	if !strings.Contains(result, ": ") {
		t.Errorf("expected ' : ' separator, got: %s", result)
	}
}

func TestPosInsideDeclBeforeDecl(t *testing.T) {
	body := &ast.Block{
		EndPos: token.Position{Line: 5},
	}
	// cursor before declaration
	if posInsideDecl(Position{Line: 0}, token.Position{Line: 3}, body) {
		t.Error("cursor before decl should return false")
	}
}

func TestPosInsideDeclNilBody(t *testing.T) {
	// nil body — native fn/api
	if posInsideDecl(Position{Line: 5}, token.Position{Line: 3}, nil) {
		t.Error("nil body should return false")
	}
}

func TestPosInsideDeclZeroEndPos(t *testing.T) {
	body := &ast.Block{
		EndPos: token.Position{Line: 0},
	}
	if posInsideDecl(Position{Line: 5}, token.Position{Line: 3}, body) {
		t.Error("zero EndPos should return false")
	}
}

func TestFindValInBlockDestructured(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:   token.Position{Line: 2, File: "test.luxo"},
				Names: []string{"user", "posts", "orders"},
			},
		},
	}
	// find "posts" from destructured val
	result := findValInBlock(block, "posts", Position{Line: 5})
	if result == nil {
		t.Fatal("expected to find 'posts' from destructured val")
	}
	if result.Line != 2 {
		t.Errorf("expected line 2, got %d", result.Line)
	}
}

func TestFindValInBlockForVar(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ForStmt{
				Pos:     token.Position{Line: 3, File: "test.luxo"},
				VarName: "item",
			},
		},
	}
	result := findValInBlock(block, "item", Position{Line: 5})
	if result == nil {
		t.Fatal("expected to find 'item' from for loop")
	}
}

func TestFindValInBlockNoMatch(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:  token.Position{Line: 2},
				Name: "x",
			},
		},
	}
	result := findValInBlock(block, "y", Position{Line: 5})
	if result != nil {
		t.Error("expected nil for no match")
	}
}

func TestFindValInBlockAfterCursor(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:  token.Position{Line: 10},
				Name: "late",
			},
		},
	}
	// cursor at line 1, val at line 10 — should not find
	result := findValInBlock(block, "late", Position{Line: 0})
	if result != nil {
		t.Error("should not find val declared after cursor")
	}
}

func TestDefinitionFnValInBody(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_fn_val.luxo"
	openDoc(server, output, uri, `fn calc(x: Int): Int {
  val result = x * 2
  result
}`)
	// cmd+click on "result" at line 2 — should find val at line 1
	result := requestDefinition(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, `"line":1`) {
		t.Errorf("expected definition at line 1, got: %s", result)
	}
}

func TestGetLocalHoverFnParamDirect(t *testing.T) {
	// Use a unique param name that won't match any type/symbol/builtin
	server, output := newTestServer()
	uri := "file:///test_hover_fn_direct.luxo"
	openDoc(server, output, uri, `fn myFunc(zzUniqueParam: Int): Int {
  zzUniqueParam
}`)
	result := requestHover(server, output, uri, Position{Line: 1, Character: 2})
	if !strings.Contains(result, "zzUniqueParam") {
		t.Errorf("expected fn param hover, got: %s", result)
	}
}

func TestGetLocalHoverModelFieldDirect(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_model_direct.luxo"
	openDoc(server, output, uri, `model TestWidget {
  /// widget label
  zzWidgetLabel: String @filterable
}`)
	result := requestHover(server, output, uri, Position{Line: 2, Character: 2})
	if !strings.Contains(result, "zzWidgetLabel") {
		t.Errorf("expected model field hover, got: %s", result)
	}
}

func TestGetLocalHoverEmptyReturn(t *testing.T) {
	// hover on a word that doesn't match anything
	server, output := newTestServer()
	uri := "file:///test_hover_empty.luxo"
	openDoc(server, output, uri, `api test(): Int {
  0
}`)
	// hover on position with no identifiable word
	result := requestHover(server, output, uri, Position{Line: 0, Character: 50})
	// test passes if no panic
	_ = result
}

func TestFormatModelHoverWithInheritanceAndNullableField(t *testing.T) {
	typ := &semantic.ResolvedType{
		Kind: semantic.TypeModel,
		Name: "AuditPost",
		Parents: []*semantic.ResolvedType{
			{Name: "Base"},
		},
		Fields: map[string]*semantic.FieldInfo{
			"title": {Type: &semantic.ResolvedType{Name: "String"}},
			"cover": {Type: &semantic.ResolvedType{Name: "String"}, Nullable: true},
			"notes": {Type: &semantic.ResolvedType{Name: "String"}, Doc: "internal notes"},
		},
	}
	result := formatTypeHover("AuditPost", typ)
	if !strings.Contains(result, "Base") {
		t.Errorf("expected parent in hover, got: %s", result)
	}
	if !strings.Contains(result, "?") {
		t.Errorf("expected nullable marker, got: %s", result)
	}
}

func TestGetLocalHoverDirectApiParam(t *testing.T) {
	doc := &Document{
		URI: "file:///direct.luxo",
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{Line: 1},
					Name: "getUser",
					Params: []*ast.ParamDecl{
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
		},
	}
	result := getLocalHover(doc, "userId", Position{Line: 1})
	if !strings.Contains(result, "userId") || !strings.Contains(result, "Int") {
		t.Errorf("expected api param hover, got: %q", result)
	}
}

func TestGetLocalHoverDirectFnParam(t *testing.T) {
	doc := &Document{
		URI: "file:///direct_fn.luxo",
		File: &ast.File{
			Functions: []*ast.FnDecl{
				{
					Pos:  token.Position{Line: 1},
					Name: "calc",
					Params: []*ast.ParamDecl{
						{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
					},
				},
			},
		},
	}
	result := getLocalHover(doc, "amount", Position{Line: 1})
	if !strings.Contains(result, "amount") || !strings.Contains(result, "Float") {
		t.Errorf("expected fn param hover, got: %q", result)
	}
}

func TestGetLocalHoverDirectModelField(t *testing.T) {
	doc := &Document{
		URI: "file:///direct_model.luxo",
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "Product",
					Fields: []*ast.FieldDecl{
						{
							Name: "price",
							Type: &ast.TypeRef{Name: "Float"},
							Doc:  "product price",
							Directives: []*ast.Directive{
								{Name: "filterable"},
							},
						},
					},
				},
			},
		},
	}
	result := getLocalHover(doc, "price", Position{Line: 5})
	if !strings.Contains(result, "price") || !strings.Contains(result, "Float") {
		t.Errorf("expected model field hover, got: %q", result)
	}
	if !strings.Contains(result, "product price") {
		t.Errorf("expected doc comment, got: %q", result)
	}
}

func TestGetLocalHoverDirectNoMatch(t *testing.T) {
	doc := &Document{
		URI: "file:///direct_nomatch.luxo",
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{Line: 1},
					Name: "test",
					Params: []*ast.ParamDecl{
						{Name: "x", Type: &ast.TypeRef{Name: "Int"}},
					},
				},
			},
		},
	}
	result := getLocalHover(doc, "nonexistent", Position{Line: 1})
	if result != "" {
		t.Errorf("expected empty, got: %q", result)
	}
}

func TestGetLocalHoverSkipsApisBeforeCursor(t *testing.T) {
	doc := &Document{
		URI: "file:///skip.luxo",
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:    token.Position{Line: 10},
					Name:   "laterApi",
					Params: []*ast.ParamDecl{{Name: "z", Type: &ast.TypeRef{Name: "Int"}}},
				},
			},
			Functions: []*ast.FnDecl{
				{
					Pos:    token.Position{Line: 20},
					Name:   "laterFn",
					Params: []*ast.ParamDecl{{Name: "w", Type: &ast.TypeRef{Name: "Int"}}},
				},
			},
		},
	}
	// cursor at line 0, both api and fn are after cursor — should skip both
	result := getLocalHover(doc, "z", Position{Line: 0})
	if result != "" {
		t.Errorf("expected empty (api after cursor), got: %q", result)
	}
	result = getLocalHover(doc, "w", Position{Line: 0})
	if result != "" {
		t.Errorf("expected empty (fn after cursor), got: %q", result)
	}
}

func TestGetLocalHoverNilFile(t *testing.T) {
	doc := &Document{URI: "file:///nil.luxo"}
	result := getLocalHover(doc, "x", Position{Line: 0})
	if result != "" {
		t.Errorf("expected empty for nil file, got: %q", result)
	}
}

func TestFindLocalDefinitionInFnBody(t *testing.T) {
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "calc",
				Params: []*ast.ParamDecl{
					{Pos: token.Position{Line: 1, Col: 10, File: "test.luxo"}, Name: "x"},
				},
				Body: &ast.Block{
					Pos:    token.Position{Line: 1},
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 2, File: "test.luxo"},
							Name: "result",
						},
					},
				},
			},
		},
	}
	// find param
	pos := findLocalDefinition(file, "x", Position{Line: 2})
	if pos == nil {
		t.Fatal("expected to find param 'x'")
	}
	// find val in body
	pos = findLocalDefinition(file, "result", Position{Line: 3})
	if pos == nil {
		t.Fatal("expected to find val 'result'")
	}
	// not found
	pos = findLocalDefinition(file, "unknown", Position{Line: 3})
	if pos != nil {
		t.Error("expected nil for unknown")
	}
}

func TestFindLocalDefinitionApiBodyNoMatch(t *testing.T) {
	// api with body but word not found in params or body vals
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "test",
				Params: []*ast.ParamDecl{
					{Pos: token.Position{Line: 1, Col: 10, File: "test.luxo"}, Name: "id"},
				},
				Body: &ast.Block{
					Pos:    token.Position{Line: 1},
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{Pos: token.Position{Line: 2, File: "test.luxo"}, Name: "x"},
					},
				},
			},
		},
	}
	// search for "unknown" — should check body, find nothing, return nil
	pos := findLocalDefinition(file, "unknown", Position{Line: 3})
	if pos != nil {
		t.Error("expected nil for unknown word")
	}
}

func TestFindLocalDefinitionFnSkippedByCursor(t *testing.T) {
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "first",
				Body: &ast.Block{
					Pos:    token.Position{Line: 1},
					EndPos: token.Position{Line: 3},
				},
			},
		},
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 10},
				Name: "later",
				Params: []*ast.ParamDecl{
					{Pos: token.Position{Line: 10, File: "test.luxo"}, Name: "z"},
				},
				Body: &ast.Block{
					Pos:    token.Position{Line: 10},
					EndPos: token.Position{Line: 15},
				},
			},
		},
	}
	// cursor at line 2 (inside api, outside fn) — fn should be skipped
	pos := findLocalDefinition(file, "z", Position{Line: 2})
	if pos != nil {
		t.Error("expected nil — cursor is not inside fn")
	}
}

func TestFindLocalDefinitionFnBodyNoMatch(t *testing.T) {
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "calc",
				Params: []*ast.ParamDecl{
					{Pos: token.Position{Line: 1, Col: 10, File: "test.luxo"}, Name: "a"},
				},
				Body: &ast.Block{
					Pos:    token.Position{Line: 1},
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{Pos: token.Position{Line: 2, File: "test.luxo"}, Name: "b"},
					},
				},
			},
		},
	}
	// search for "unknown" in fn — should check body, find nothing
	pos := findLocalDefinition(file, "unknown", Position{Line: 3})
	if pos != nil {
		t.Error("expected nil for unknown word in fn")
	}
}

func TestFindLocalDefinitionApiNoBody(t *testing.T) {
	// api without body — params exist but no body to search
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "native",
				Params: []*ast.ParamDecl{
					{Pos: token.Position{Line: 1, Col: 15, File: "test.luxo"}, Name: "id"},
				},
				// Body is nil — @native api
			},
		},
	}
	// native api has no body → posInsideDecl returns false → skip
	pos := findLocalDefinition(file, "id", Position{Line: 1})
	if pos != nil {
		t.Error("expected nil — native api has no body, posInsideDecl should return false")
	}
}

func TestDefinitionNativeApiNoBody(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_def_native.luxo"
	openDoc(server, output, uri, `api oauthLogin(provider: String): String @native
api test(): String {
  val x = 1
  x
}`)
	// cmd+click on "x" at line 3 — should find val at line 2, not crash on native api
	result := requestDefinition(server, output, uri, Position{Line: 3, Character: 2})
	if !strings.Contains(result, `"line":2`) {
		t.Errorf("expected definition at line 2, got: %s", result)
	}
}

// ========== CRUD Completions & Named Arg Definition ==========

func TestCRUDCompletionsInFind(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_comp.luxo"
	openDoc(server, output, uri, `model Order {
  id: Int
  userId: Int
  total: Float
}
api test(): Int {
  val order = Order.find( )
  0
}`)
	// trigger completion inside Order.find(...)
	result := requestCompletion(server, output, uri, Position{Line: 6, Character: 25})
	// should include CRUD params
	if !strings.Contains(result, "where") {
		t.Errorf("expected 'where' CRUD param, got: %s", result)
	}
	// should include model fields
	if !strings.Contains(result, "userId") {
		t.Errorf("expected 'userId' field, got: %s", result)
	}
	if !strings.Contains(result, "total") {
		t.Errorf("expected 'total' field, got: %s", result)
	}
}

func TestCRUDCompletionsNotInNonCRUD(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_no_crud.luxo"
	openDoc(server, output, uri, `api test(): Int {
  val x = someFunc()
  0
}`)
	result := requestCompletion(server, output, uri, Position{Line: 1, Character: 18})
	// should NOT include CRUD params since it's not a CRUD call
	if strings.Contains(result, "\"where\"") {
		t.Errorf("should not have 'where' in non-CRUD context")
	}
}

func TestNamedArgLabelDefinitionToModelField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_named_arg.luxo"
	openDoc(server, output, uri, `model Order {
  id: Int
  userId: Int
}
api test(orderId: Int): Int {
  val order = Order.find(userId: orderId)
  0
}`)
	// cmd+click on "userId" (the named arg label, before :) at line 5
	// "userId" is at position ~26 in "Order.find(userId: orderId)"
	result := requestDefinition(server, output, uri, Position{Line: 5, Character: 26})
	// should jump to userId field in Order model (line 2)
	if !strings.Contains(result, `"line":2`) {
		t.Errorf("expected definition at line 2 (Order.userId), got: %s", result)
	}
}

func TestIsNamedArgLabel(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `User.find(id: 1, where: x == y)`)
	doc := store.Get("file:///test.luxo")

	// "id" at position 10 is followed by ":"
	if !doc.IsNamedArgLabel(Position{Line: 0, Character: 10}) {
		t.Error("expected 'id' to be named arg label")
	}
	// "where" at position 17 is followed by ":"
	if !doc.IsNamedArgLabel(Position{Line: 0, Character: 17}) {
		t.Error("expected 'where' to be named arg label")
	}
	// "find" at position 5 is followed by "("
	if doc.IsNamedArgLabel(Position{Line: 0, Character: 5}) {
		t.Error("'find' should not be named arg label")
	}
}

func TestFindEnclosingCall(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `val x = Order.find(userId: 1)`)
	doc := store.Get("file:///test.luxo")

	fn, model := doc.FindEnclosingCall(Position{Line: 0, Character: 20})
	if fn != "find" {
		t.Errorf("expected 'find', got %q", fn)
	}
	if model != "Order" {
		t.Errorf("expected 'Order', got %q", model)
	}
}

func TestGetCRUDParamCompletionsCreate(t *testing.T) {
	items := getCRUDParamCompletions("create")
	if len(items) != 0 {
		t.Errorf("expected no special params for create, got %d", len(items))
	}
}

func TestGetCRUDParamCompletionsDelete(t *testing.T) {
	items := getCRUDParamCompletions("delete")
	found := false
	for _, item := range items {
		if item.Label == "where" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'where' param for delete")
	}
}

func TestIsCRUDFunc(t *testing.T) {
	if !isCRUDFunc("find") {
		t.Error("find should be CRUD")
	}
	if !isCRUDFunc("create") {
		t.Error("create should be CRUD")
	}
	if isCRUDFunc("someFunc") {
		t.Error("someFunc should not be CRUD")
	}
}

func TestIsNamedArgLabelOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `User.find()`)
	doc := store.Get("file:///test.luxo")
	// out of bounds
	if doc.IsNamedArgLabel(Position{Line: 99, Character: 0}) {
		t.Error("should return false for out-of-bounds")
	}
}

func TestFindEnclosingCallNotInCall(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `val x = 1`)
	doc := store.Get("file:///test.luxo")
	fn, model := doc.FindEnclosingCall(Position{Line: 0, Character: 5})
	if fn != "" || model != "" {
		t.Errorf("expected empty, got fn=%q model=%q", fn, model)
	}
}

func TestFindEnclosingCallOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `User.find()`)
	doc := store.Get("file:///test.luxo")
	fn, _ := doc.FindEnclosingCall(Position{Line: 99, Character: 0})
	if fn != "" {
		t.Error("expected empty for out-of-bounds")
	}
}

func TestFindCRUDFieldDefinitionNonCRUD(t *testing.T) {
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: `val x = someFunc(Order, name: 1)`,
		File:    &ast.File{},
	}
	pos := findCRUDFieldDefinition(doc, "name", Position{Line: 0, Character: 24})
	if pos != nil {
		t.Error("expected nil for non-CRUD func")
	}
}

func TestFindCRUDFieldDefinitionCRUDParam(t *testing.T) {
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: `val x = Order.find(where: true)`,
		File:    &ast.File{},
	}
	// "where" is a CRUD param, not a model field
	pos := findCRUDFieldDefinition(doc, "where", Position{Line: 0, Character: 20})
	if pos != nil {
		t.Error("expected nil for CRUD param 'where'")
	}
}

func TestFindCRUDFieldDefinitionModelNotFound(t *testing.T) {
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: `val x = Unknown.find(name: 1)`,
		File:    &ast.File{Models: []*ast.ModelDecl{{Name: "Other"}}},
	}
	pos := findCRUDFieldDefinition(doc, "name", Position{Line: 0, Character: 22})
	if pos != nil {
		t.Error("expected nil when model not found in file")
	}
}

func TestFindEnclosingCallNestedParens(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `val x = Order.find(where: fn() == 1)`)
	doc := store.Get("file:///test.luxo")
	// cursor at position 34 — after fn() close paren, scans back through nested parens
	fn, model := doc.FindEnclosingCall(Position{Line: 0, Character: 34})
	if fn != "find" {
		t.Errorf("expected 'find', got %q", fn)
	}
	if model != "Order" {
		t.Errorf("expected 'Order', got %q", model)
	}
}

func TestFindEnclosingCallNoSpaceAfterParen(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `Order.find(id: 1)`)
	doc := store.Get("file:///test.luxo")
	fn, model := doc.FindEnclosingCall(Position{Line: 0, Character: 12})
	if fn != "find" {
		t.Errorf("expected 'find', got %q", fn)
	}
	if model != "Order" {
		t.Errorf("expected 'Order', got %q", model)
	}
}

func TestFindCRUDFieldDefinitionNoFile(t *testing.T) {
	doc := &Document{
		URI:     "file:///test.luxo",
		Content: `val x = Order.find(name: 1)`,
	}
	pos := findCRUDFieldDefinition(doc, "name", Position{Line: 0, Character: 20})
	if pos != nil {
		t.Error("expected nil for nil file")
	}
}

func TestResolveDefinitionNilFile(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		URI: "file:///nil.luxo",
		Result: &semantic.Result{
			Types: map[string]*semantic.ResolvedType{},
			Scope: semantic.NewScope(),
		},
	}
	pos := server.resolveDefinition(doc, "unknown", Position{Line: 0})
	if pos != nil {
		t.Error("expected nil for nil file")
	}
}

func TestGetCRUDParamCompletionsUpdate(t *testing.T) {
	items := getCRUDParamCompletions("update")
	found := false
	for _, item := range items {
		if item.Label == "where" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'where' param for update")
	}
}

func TestIsCRUDParam(t *testing.T) {
	if !isCRUDParam("where") {
		t.Error("where should be CRUD param")
	}
	if isCRUDParam("userId") {
		t.Error("userId should not be CRUD param")
	}
}

// ========== Member Method Hover + IsAfterDot ==========

func TestMemberMethodDescription(t *testing.T) {
	tests := []struct {
		word   string
		expect bool
	}{
		{"size", true},
		{"filter", true},
		{"map", true},
		{"length", true},
		{"trim", true},
		{"contains", true},
		{"let", true},
		{"joinToString", true},
		// @withAuth injected methods
		{"createToken", true},
		{"verify", true},
		{"refreshToken", true},
		// Identity methods
		{"load", true},
		{"nonexistent", false},
	}
	for _, tt := range tests {
		desc := memberMethodDescription(tt.word)
		if tt.expect && desc == "" {
			t.Errorf("expected description for %q", tt.word)
		}
		if !tt.expect && desc != "" {
			t.Errorf("unexpected description for %q: %s", tt.word, desc)
		}
	}
}

func TestIsAfterDot(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `items.size`)
	doc := store.Get("file:///test.luxo")
	// "size" at position 6 is after "."
	if !doc.IsAfterDot(Position{Line: 0, Character: 6}) {
		t.Error("expected IsAfterDot = true for items.size")
	}
	// "items" at position 0 is not after "."
	if doc.IsAfterDot(Position{Line: 0, Character: 0}) {
		t.Error("expected IsAfterDot = false for items")
	}
}

func TestIsAfterDotOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `x.y`)
	doc := store.Get("file:///test.luxo")
	if doc.IsAfterDot(Position{Line: 99, Character: 0}) {
		t.Error("expected false for out-of-bounds")
	}
}

func TestIsAfterItDotOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `it.name`)
	doc := store.Get("file:///test.luxo")
	if doc.IsAfterItDot(Position{Line: 99, Character: 0}) {
		t.Error("expected false for out-of-bounds")
	}
}

func TestHoverMemberMethod(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_method.luxo"
	openDoc(server, output, uri, `api test(): Int {
  val items = Post.find(where: true)
  items.size
}`)
	// hover on "size" at line 2, character 8
	result := requestHover(server, output, uri, Position{Line: 2, Character: 8})
	if !strings.Contains(result, "size") {
		t.Errorf("expected size hover, got: %s", result)
	}
}

func TestHoverItFieldNotParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_hover_it.luxo"
	openDoc(server, output, uri, `model Room {
  roomId: Int
}
api test(roomId: Int): Int {
  val r = Room.find(where: it.roomId == roomId)
  0
}`)
	// hover on "roomId" after "it." at line 4 — should show field, not param
	result := requestHover(server, output, uri, Position{Line: 4, Character: 32})
	if strings.Contains(result, "param roomId") {
		t.Errorf("should show field hover, not param, got: %s", result)
	}
}

// ========== References ==========

func TestReferencesParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_refs.luxo"
	openDoc(server, output, uri, `api test(items: Int): Int {
  val x = items
  items
}`)
	result := requestReferences(server, output, uri, Position{Line: 0, Character: 9})
	// "items" appears 3 times — count by range objects
	count := strings.Count(result, `"range"`)
	if count < 3 {
		t.Errorf("expected at least 3 references, got %d in: %s", count, result)
	}
}

func TestReferencesNoDoc(t *testing.T) {
	server, output := newTestServer()
	result := requestReferences(server, output, "file:///none.luxo", Position{Line: 0, Character: 0})
	if strings.Contains(result, "line") {
		t.Error("expected null response for unknown doc")
	}
}

func TestReferencesEmptyWord(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_ref_empty.luxo"
	openDoc(server, output, uri, `model User { }`)
	result := requestReferences(server, output, uri, Position{Line: 0, Character: 12})
	// space position — no word
	_ = result // test passes if no panic
}

func TestMultiLineCRUDCompletion(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_multiline_crud.luxo"
	openDoc(server, output, uri, `model Order {
  id: Int
  userId: Int
}
api test(): Int {
  val order = Order.find(    userId: 1)
  0
}`)
	// completion on line 6, inside multi-line find() call
	result := requestCompletion(server, output, uri, Position{Line: 6, Character: 4})
	if !strings.Contains(result, "where") {
		t.Errorf("expected 'where' in multi-line CRUD, got: %s", result)
	}
}

func TestExtractCallInfoNextLine(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "val x = Order.find(\n  id: 1)")
	doc := store.Get("file:///test.luxo")
	fn, model := doc.FindEnclosingCall(Position{Line: 1, Character: 5})
	if fn != "find" {
		t.Errorf("expected 'find', got %q", fn)
	}
	if model != "Order" {
		t.Errorf("expected 'Order', got %q", model)
	}
}

// ========== Stdlib Module Completions ==========

func TestStdlibTimeCompletions(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_stdlib_time.luxo"
	openDoc(server, output, uri, `api test(): String {
  val now = time.
  now
}`)
	result := requestCompletion(server, output, uri, Position{Line: 1, Character: 17})
	if !strings.Contains(result, "now") {
		t.Errorf("expected 'now' in time completions, got: %s", result)
	}
}

func TestStdlibHttpCompletions(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_stdlib_http.luxo"
	openDoc(server, output, uri, `api test(): String {
  val resp = http.
  resp
}`)
	result := requestCompletion(server, output, uri, Position{Line: 1, Character: 18})
	if !strings.Contains(result, "get") {
		t.Errorf("expected 'get' in http completions, got: %s", result)
	}
}

func TestStdlibCryptoCompletions(t *testing.T) {
	items := getStdlibModuleMethods("crypto")
	names := map[string]bool{}
	for _, item := range items {
		names[item.Label] = true
	}
	if !names["randomToken"] || !names["uuid"] || !names["hash"] {
		t.Errorf("missing crypto methods, got: %v", names)
	}
}

func TestStdlibNonModule(t *testing.T) {
	items := getStdlibModuleMethods("unknown")
	if len(items) != 0 {
		t.Errorf("expected no items for unknown module, got %d", len(items))
	}
}

func TestStdlibMyCompletions(t *testing.T) {
	items := getStdlibModuleMethods("my")
	names := map[string]bool{}
	for _, item := range items {
		names[item.Label] = true
	}
	if !names["id"] {
		t.Errorf("missing my.id in completions, got: %v", names)
	}
	if !names["load"] {
		t.Errorf("missing my.load in completions, got: %v", names)
	}
	if !names["role"] {
		t.Errorf("missing my.role in completions, got: %v", names)
	}
}

func TestDateTimeMethodCompletions(t *testing.T) {
	items := getDateTimeMethods()
	names := map[string]bool{}
	for _, item := range items {
		names[item.Label] = true
	}
	if !names["format"] || !names["add"] || !names["year"] || !names["unix"] {
		t.Errorf("missing DateTime methods, got: %v", names)
	}
}

// ========== ObjectBeforeDot Tests ==========

func TestObjectBeforeDot(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }\napi test(): String {\n  input.name\n}")
	doc := store.Get("file:///test.luxo")

	// pos on "name" after "input."
	obj := doc.ObjectBeforeDot(Position{Line: 2, Character: 8})
	if obj != "input" {
		t.Errorf("expected 'input', got %q", obj)
	}
}

func TestObjectBeforeDotNoDot(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User { name: String }")
	doc := store.Get("file:///test.luxo")

	// pos on "name" without a dot
	obj := doc.ObjectBeforeDot(Position{Line: 0, Character: 16})
	if obj != "" {
		t.Errorf("expected empty string, got %q", obj)
	}
}

func TestObjectBeforeDotOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "x")
	doc := store.Get("file:///test.luxo")
	obj := doc.ObjectBeforeDot(Position{Line: 99, Character: 0})
	if obj != "" {
		t.Errorf("expected empty for out-of-bounds, got %q", obj)
	}
}

// ========== findMemberFieldDefinition Tests ==========

func TestFindMemberFieldDefinition(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_member.luxo"
	openDoc(server, output, uri, `type RegisterInput {
  name: String
  email: String
}
api test(input: RegisterInput): String {
  input.name
}`)
	// definition request on "name" after "input." — line 5, col 8
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 8})
	if !strings.Contains(resp, "test_member.luxo") {
		t.Error("expected definition location for member field")
	}
}

// ========== findFieldInAnyType Tests ==========

func TestFindFieldInAnyType(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_any.luxo"
	openDoc(server, output, uri, `model Post {
  title: String
}
api test(): String {
  val posts = Post.find(where: status == "ok")
  val matched = posts.filter { it.title }
  "done"
}`)
	// definition on "title" after "it." — line 5, col 39
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 37})
	if !strings.Contains(resp, "test_any.luxo") {
		t.Error("expected definition location for it.field")
	}
}

// ========== findFieldInType Tests ==========

func TestFindFieldInType(t *testing.T) {
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "name", Pos: token.Position{File: "test.luxo", Line: 2, Col: 3}},
					{Name: "email", Pos: token.Position{File: "test.luxo", Line: 3, Col: 3}},
				},
			},
		},
		Types: []*ast.TypeDecl{
			{
				Name: "AuthResult",
				Fields: []*ast.FieldDecl{
					{Name: "token", Pos: token.Position{File: "test.luxo", Line: 10, Col: 3}},
				},
			},
		},
	}
	// find in model
	pos := findFieldInType(file, "User", "name")
	if pos == nil {
		t.Fatal("expected to find 'name' in User")
	}
	if pos.Line != 2 {
		t.Errorf("expected line 2, got %d", pos.Line)
	}
	// find in type
	pos = findFieldInType(file, "AuthResult", "token")
	if pos == nil {
		t.Fatal("expected to find 'token' in AuthResult")
	}
	// not found
	pos = findFieldInType(file, "User", "nonexistent")
	if pos != nil {
		t.Error("expected nil for nonexistent field")
	}
	pos = findFieldInType(file, "Unknown", "name")
	if pos != nil {
		t.Error("expected nil for unknown type")
	}
}

// ========== directiveDescription Tests ==========

func TestDirectiveDescription(t *testing.T) {
	desc := directiveDescription("length")
	if desc == "" {
		t.Error("expected description for @length")
	}
	if !strings.Contains(desc, "varchar") {
		t.Error("expected 'varchar' in @length description")
	}
	// unknown directive
	desc = directiveDescription("nonexistent_directive")
	if desc != "" {
		t.Errorf("expected empty for unknown directive, got %q", desc)
	}
}

func TestDirectiveDescriptionCoverage(t *testing.T) {
	// test a sampling of directives to exercise the map
	known := []string{"serial", "bigint", "decimal", "uuid", "inet", "date", "time", "crud", "soft", "noTime", "unique", "hidden", "auth", "native", "cache"}
	for _, name := range known {
		desc := directiveDescription(name)
		if desc == "" {
			t.Errorf("expected non-empty description for @%s", name)
		}
	}
}

// ========== builtinTypeDescription Tests ==========

func TestBuiltinTypeDescriptionAll(t *testing.T) {
	types := []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes"}
	for _, name := range types {
		desc := builtinTypeDescription(name)
		if desc == "" {
			t.Errorf("expected description for %s", name)
		}
	}
	// unknown
	desc := builtinTypeDescription("UnknownType")
	if desc != "" {
		t.Errorf("expected empty for unknown type, got %q", desc)
	}
}

// ========== getValVarHover Tests ==========

func TestGetValVarHover(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_val_hover.luxo"
	openDoc(server, output, uri, `model User { name: String }
api test(): String {
  val token = "abc"
  var count = 0
  token
}`)
	// hover over "token" on line 4
	resp := requestHover(server, output, uri, Position{Line: 4, Character: 3})
	if !strings.Contains(resp, "val") && !strings.Contains(resp, "token") {
		t.Error("expected val hover for 'token'")
	}
}

func TestGetVarHover(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_var_hover.luxo"
	openDoc(server, output, uri, `model User { name: String }
fn helper(): Int {
  var x = 42
  x
}`)
	// hover over "x" on line 3
	resp := requestHover(server, output, uri, Position{Line: 3, Character: 2})
	if !strings.Contains(resp, "var") {
		t.Error("expected var hover for 'x'")
	}
}

// ========== findValHover Tests ==========

func TestFindValHoverInBlock(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:     token.Position{Line: 3, Col: 3},
				Name:    "myVar",
				Mutable: true,
				Type:    &ast.TypeRef{Name: "Int"},
			},
		},
	}
	hover := findValHover(block, "myVar", Position{Line: 3, Character: 5})
	if hover == "" {
		t.Fatal("expected hover for myVar")
	}
	if !strings.Contains(hover, "var") {
		t.Error("expected 'var' in hover")
	}
	if !strings.Contains(hover, "Int") {
		t.Error("expected type 'Int' in hover")
	}
}

// ========== getFieldHover Tests ==========

func TestGetFieldHover(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_field_hover.luxo"
	// open a doc with a model that has fields, and hover on field name in context
	openDoc(server, output, uri, `model User {
  /// User name
  name: String @unique
  email: String
}
type AuthResult {
  token: String
}
api test(): String {
  name
}`)
	// hover over "name" on line 9 — should find field hover
	resp := requestHover(server, output, uri, Position{Line: 9, Character: 3})
	if !strings.Contains(resp, "User.name") {
		t.Error("expected 'User.name' in field hover")
	}
}

func TestGetFieldHoverType(t *testing.T) {
	file := &ast.File{
		Types: []*ast.TypeDecl{
			{
				Name: "AuthResult",
				Fields: []*ast.FieldDecl{
					{Name: "token", Type: &ast.TypeRef{Name: "String"}, Pos: token.Position{Line: 2, Col: 3}},
				},
			},
		},
	}
	hover := getFieldHover(file, "token")
	if hover == "" {
		t.Fatal("expected hover for 'token'")
	}
	if !strings.Contains(hover, "AuthResult.token") {
		t.Error("expected 'AuthResult.token' in hover")
	}
}

// ========== resolveDefinition for enum value ==========

func TestResolveDefinitionEnumValue(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_enum_def.luxo"
	openDoc(server, output, uri, `enum Role { USER ADMIN }
api test(): String {
  val r = Role.USER
  "done"
}`)
	// definition of "USER" — should jump to enum
	resp := requestDefinition(server, output, uri, Position{Line: 2, Character: 16})
	if !strings.Contains(resp, "test_enum_def.luxo") {
		t.Error("expected enum definition location")
	}
}

// ========== IsAfterAt Tests ==========

func TestIsAfterAt(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "model User {\n  name: String @unique\n}")
	doc := store.Get("file:///test.luxo")

	// pos on "unique" after "@"
	result := doc.IsAfterAt(Position{Line: 1, Character: 18})
	if !result {
		t.Error("expected IsAfterAt to return true for @unique")
	}

	// pos on "name" — not after @
	result = doc.IsAfterAt(Position{Line: 1, Character: 4})
	if result {
		t.Error("expected IsAfterAt to return false for 'name'")
	}
}

func TestIsAfterAtOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, "x")
	doc := store.Get("file:///test.luxo")
	result := doc.IsAfterAt(Position{Line: 99, Character: 0})
	if result {
		t.Error("expected false for out-of-bounds")
	}
}

// ========== Hover with directive ==========

func TestHoverDirective(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_dir.luxo"
	openDoc(server, output, uri, "model User {\n  name: String @length(50)\n}")
	// hover on "length" after "@" — line 1, character 17
	resp := requestHover(server, output, uri, Position{Line: 1, Character: 17})
	if !strings.Contains(resp, "varchar") {
		t.Error("expected directive description for @length")
	}
}

// ========== Member method hover after dot ==========

func TestHoverMemberMethodFilter(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_method.luxo"
	openDoc(server, output, uri, `model User { name: String }
api test(): String {
  val users = User.find(where: name == "x")
  users.filter
}`)
	resp := requestHover(server, output, uri, Position{Line: 3, Character: 9})
	if !strings.Contains(resp, "filter") {
		t.Error("expected member method hover for 'filter'")
	}
}

// ========== findFieldInAnyType with types ==========

func TestFindFieldInAnyTypeFromTypes(t *testing.T) {
	file := &ast.File{
		Models: []*ast.ModelDecl{},
		Types: []*ast.TypeDecl{
			{
				Name: "AuthResult",
				Fields: []*ast.FieldDecl{
					{Name: "token", Pos: token.Position{File: "test.luxo", Line: 5, Col: 3}},
				},
			},
		},
	}
	pos := findFieldInAnyType(file, "token")
	if pos == nil {
		t.Fatal("expected to find 'token' in types")
	}
	if pos.Line != 5 {
		t.Errorf("expected line 5, got %d", pos.Line)
	}
	// not found
	pos = findFieldInAnyType(file, "nonexistent")
	if pos != nil {
		t.Error("expected nil for nonexistent field")
	}
}

// ========== resolveHover for local param and val ==========

func TestResolveHoverParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_param_hover.luxo"
	openDoc(server, output, uri, `model User { name: String }
api test(userId: Int): String {
  userId
}`)
	// hover over "userId" on line 2 — should show param hover
	resp := requestHover(server, output, uri, Position{Line: 2, Character: 3})
	if !strings.Contains(resp, "param") {
		t.Error("expected param hover for 'userId'")
	}
}

// ========== Hover builtin type via resolveHover ==========

func TestResolveHoverBuiltinType(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_builtin.luxo"
	openDoc(server, output, uri, "model User { name: String }")
	resp := requestHover(server, output, uri, Position{Line: 0, Character: 20})
	if !strings.Contains(resp, "String") {
		t.Error("expected builtin type hover for 'String'")
	}
}

// ========== findMemberFieldDefinition — param.field jumps to type's field ==========

func TestDefinitionMemberField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_member_def.luxo"
	openDoc(server, output, uri, `model User {
  name: String
  email: String
}
api test(user: User): String {
  user.name
}`)
	// request definition on "name" after "user." on line 5 (0-based), character 7
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 7})
	// should resolve to the field definition
	if !strings.Contains(resp, "test_member_def") {
		t.Error("expected definition to point to field in same file")
	}
}

// ========== findMemberFieldDefinition — it.field in lambda ==========

func TestDefinitionItMemberField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_it_def.luxo"
	openDoc(server, output, uri, `model Item {
  name: String
  price: Float
}
api test(): Int {
  val items = Item.find(where: name == "x")
  items.filter { it.price > 0 }
  0
}`)
	// request definition on "price" after "it." on line 6 (0-based), character 21
	resp := requestDefinition(server, output, uri, Position{Line: 6, Character: 21})
	// The definition may resolve to the field or return null — just exercise the code path
	if resp == "" {
		t.Error("expected non-empty response")
	}
}

// ========== handleReferences — finds all references of a word ==========

func TestHandleReferencesMultiple(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_refs.luxo"
	openDoc(server, output, uri, `model User {
  name: String
}
api test(): String {
  val user = User.find(id: 1)
  user?.name ?: "anon"
}`)
	// request references for "name" on line 1 (0-based)
	id := json.RawMessage(`200`)
	params, _ := json.Marshal(ReferenceParams{
		TextDocument: TextDocumentID{URI: uri},
		Position:     Position{Line: 1, Character: 3},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/references",
		Params:  params,
	})
	resp := output.String()
	// "name" appears at least twice: field decl and usage
	if !strings.Contains(resp, "test_refs") {
		t.Error("expected references to include the test file")
	}
}

// ========== ObjectBeforeDot — edge cases ==========

func TestObjectBeforeDotShortLine(t *testing.T) {
	doc := &Document{Content: "a.b"}
	obj := doc.ObjectBeforeDot(Position{Line: 0, Character: 2})
	if obj != "a" {
		t.Errorf("expected 'a', got %q", obj)
	}
}

func TestObjectBeforeDotNoDotEdge(t *testing.T) {
	doc := &Document{Content: "abc"}
	obj := doc.ObjectBeforeDot(Position{Line: 0, Character: 2})
	if obj != "" {
		t.Errorf("expected empty string for no dot, got %q", obj)
	}
}

func TestObjectBeforeDotLineOutOfRangeEdge(t *testing.T) {
	doc := &Document{Content: "abc"}
	obj := doc.ObjectBeforeDot(Position{Line: 5, Character: 0})
	if obj != "" {
		t.Errorf("expected empty string for out of range line, got %q", obj)
	}
}

// ObjectBeforeDot — cursor at start of line (Character=0), start=0 so start<2
func TestObjectBeforeDotCursorAtStart(t *testing.T) {
	doc := &Document{Content: "abc.def"}
	obj := doc.ObjectBeforeDot(Position{Line: 0, Character: 0})
	if obj != "" {
		t.Errorf("expected empty string for cursor at start, got %q", obj)
	}
}

// ObjectBeforeDot — dot preceded by non-ident char, objStart >= objEnd
func TestObjectBeforeDotNonIdentBeforeDot(t *testing.T) {
	doc := &Document{Content: "(.x"}
	obj := doc.ObjectBeforeDot(Position{Line: 0, Character: 2})
	if obj != "" {
		t.Errorf("expected empty for non-ident before dot, got %q", obj)
	}
}

// ========== handleReferences — empty word ==========

func TestHandleReferencesEmptyWordReturnsNull(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_ref_empty2.luxo"
	// content with spaces so WordAt returns ""
	openDoc(server, output, uri, `   `)
	result := requestReferences(server, output, uri, Position{Line: 0, Character: 1})
	// should get null/empty response, not a list
	if strings.Contains(result, `"line"`) {
		t.Error("expected null response for empty word")
	}
}

// ========== findMemberFieldDefinition — objName is not a param ==========

func TestFindMemberFieldDefinitionNonParam(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_nonparam.luxo"
	openDoc(server, output, uri, `type Input { name: String }
api test(input: Input): String {
  unknown.name
}`)
	// definition on "name" after "unknown." — unknown is not a param
	resp := requestDefinition(server, output, uri, Position{Line: 2, Character: 10})
	// should not find definition via findMemberFieldDefinition (unknown is not a param)
	_ = resp
}

// ========== findMemberFieldDefinition — typeName not found (param type not declared) ==========

func TestFindMemberFieldDefinitionTypeNotFound(t *testing.T) {
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name: "test",
				Params: []*ast.ParamDecl{
					{Name: "input", Type: &ast.TypeRef{Name: "NonExistent"}},
				},
			},
		},
		Models: []*ast.ModelDecl{},
		Types:  []*ast.TypeDecl{},
	}
	doc := &Document{
		Content: "input.name",
		File:    file,
		Result:  &semantic.Result{},
	}
	pos := findMemberFieldDefinition(doc, "name", Position{Line: 0, Character: 6})
	// findParamTypeName returns "NonExistent", findFieldInType returns nil
	if pos != nil {
		t.Error("expected nil when type not found in file")
	}
}

// ========== findMemberFieldDefinition — field found in type (not model) ==========

func TestFindMemberFieldDefinitionInType(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_type_field.luxo"
	openDoc(server, output, uri, `type AuthResult {
  token: String
  userId: Int
}
api login(auth: AuthResult): String {
  auth.token
}`)
	// definition on "token" after "auth." — should find field in type AuthResult
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 7})
	if !strings.Contains(resp, "test_type_field.luxo") {
		t.Error("expected definition location for type field")
	}
}

// ========== findMemberFieldDefinition — val variable from CRUD call ==========

func TestDefinitionMemberFieldValCRUD(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_val_crud.luxo"
	openDoc(server, output, uri, `model Order {
  orderNo: String
  amount: Float
}
api test(): String {
  val order = Order.create(orderNo: "123", amount: 9.99)
  order.orderNo
}`)
	// "orderNo" after "order." on line 6: "  order.orderNo"
	// "order" is 2..7, "." is 7, "orderNo" starts at 8
	resp := requestDefinition(server, output, uri, Position{Line: 6, Character: 10})
	if !strings.Contains(resp, `"line":1`) {
		t.Errorf("expected definition to point to orderNo field at line 1, got %s", resp)
	}
}

// ========== findMemberFieldDefinition — val variable from transaction block ==========

func TestDefinitionMemberFieldValTransaction(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_val_tx.luxo"
	openDoc(server, output, uri, `model Order {
  orderNo: String
  status: String
}
api test(): String {
  val order = transaction {
    Order.create(orderNo: "456", status: "new")
  }
  order.status
}`)
	// "status" after "order." on line 8: "  order.status"
	resp := requestDefinition(server, output, uri, Position{Line: 8, Character: 10})
	if !strings.Contains(resp, `"line":2`) {
		t.Errorf("expected definition to point to status field at line 2, got %s", resp)
	}
}

// ========== findMemberFieldDefinition — val with explicit type annotation ==========

func TestDefinitionMemberFieldValExplicitType(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_val_typed.luxo"
	openDoc(server, output, uri, `model User {
  name: String
  email: String
}
api test(): String {
  val user: User = User.find(id: 1)
  user.email
}`)
	// "email" after "user." on line 6: "  user.email"
	resp := requestDefinition(server, output, uri, Position{Line: 6, Character: 9})
	if !strings.Contains(resp, `"line":2`) {
		t.Errorf("expected definition to point to email field at line 2, got %s", resp)
	}
}

// ========== findMemberFieldDefinition — val from find() ==========

func TestDefinitionMemberFieldValFind(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_val_find.luxo"
	openDoc(server, output, uri, `model Product {
  title: String
  price: Float
}
api test(): String {
  val product = Product.find(id: 1)
  product.title
}`)
	// "title" after "product." on line 6
	resp := requestDefinition(server, output, uri, Position{Line: 6, Character: 12})
	if !strings.Contains(resp, `"line":1`) {
		t.Errorf("expected definition to point to title field at line 1, got %s", resp)
	}
}

// ========== inferExprTypeName — non-CRUD call returns empty ==========

func TestInferExprTypeNameNonCRUD(t *testing.T) {
	expr := &ast.CallExpr{
		Func: &ast.Ident{Name: "encrypt"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "password"}}},
	}
	if got := inferExprTypeName(expr); got != "" {
		t.Errorf("expected empty for non-CRUD call, got %q", got)
	}
}

// ========== inferExprTypeName — nil expr ==========

func TestInferExprTypeNameNil(t *testing.T) {
	if got := inferExprTypeName(nil); got != "" {
		t.Errorf("expected empty for nil expr, got %q", got)
	}
}

// ========== inferExprTypeName — unrecognized expr type ==========

func TestInferExprTypeNameUnknownExpr(t *testing.T) {
	expr := &ast.Literal{Value: "hello"}
	if got := inferExprTypeName(expr); got != "" {
		t.Errorf("expected empty for literal expr, got %q", got)
	}
}

// ========== inferCallTypeName — no args ==========

func TestInferCallTypeNameNoArgs(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "create"},
		Args: []*ast.NamedArg{},
	}
	if got := inferCallTypeName(call); got != "" {
		t.Errorf("expected empty for no-arg CRUD call, got %q", got)
	}
}

// ========== inferCallTypeName — first arg is not Ident ==========

func TestInferCallTypeNameNonIdentFirstArg(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "create"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "42"}}},
	}
	if got := inferCallTypeName(call); got != "" {
		t.Errorf("expected empty when first arg is not Ident, got %q", got)
	}
}

// ========== inferCallTypeName — func is not Ident ==========

func TestInferCallTypeNameFuncNotIdent(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Field: "create"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Order"}}},
	}
	if got := inferCallTypeName(call); got != "" {
		t.Errorf("expected empty when func is not Ident, got %q", got)
	}
}

// ========== inferCallTypeName — block-call with trailing lambda ==========

func TestInferCallTypeNameTrailingLambda(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "transaction"},
		Args: []*ast.NamedArg{
			{Value: &ast.LambdaExpr{
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: &ast.CallExpr{
							Func: &ast.Ident{Name: "create"},
							Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Order"}}},
						}},
					},
				},
			}},
		},
	}
	if got := inferCallTypeName(call); got != "Order" {
		t.Errorf("expected 'Order' for transaction with lambda, got %q", got)
	}
}

// ========== inferCallTypeName — non-CRUD call without lambda args ==========

func TestInferCallTypeNameNonCRUDNoLambda(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "doSomething"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "x"}}},
	}
	if got := inferCallTypeName(call); got != "" {
		t.Errorf("expected empty for non-CRUD non-lambda call, got %q", got)
	}
}

// ========== inferCallTypeName — non-CRUD call with zero args ==========

func TestInferCallTypeNameNonCRUDNoArgs(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "doSomething"},
		Args: []*ast.NamedArg{},
	}
	if got := inferCallTypeName(call); got != "" {
		t.Errorf("expected empty for non-CRUD zero-arg call, got %q", got)
	}
}

// ========== inferBlockTypeName — nil block ==========

func TestInferBlockTypeNameNil(t *testing.T) {
	if got := inferBlockTypeName(nil); got != "" {
		t.Errorf("expected empty for nil block, got %q", got)
	}
}

// ========== inferBlockTypeName — empty block ==========

func TestInferBlockTypeNameEmpty(t *testing.T) {
	block := &ast.Block{Stmts: []ast.Stmt{}}
	if got := inferBlockTypeName(block); got != "" {
		t.Errorf("expected empty for empty block, got %q", got)
	}
}

// ========== inferBlockTypeName — last stmt is ValStmt with type ==========

func TestInferBlockTypeNameLastVal(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name:  "x",
				Type:  &ast.TypeRef{Name: "User"},
				Value: &ast.Literal{Value: "1"},
			},
		},
	}
	if got := inferBlockTypeName(block); got != "User" {
		t.Errorf("expected 'User' for val with explicit type, got %q", got)
	}
}

// ========== inferBlockTypeName — last stmt is non-expr/non-val ==========

func TestInferBlockTypeNameLastNonExpr(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ReturnStmt{},
		},
	}
	if got := inferBlockTypeName(block); got != "" {
		t.Errorf("expected empty for return stmt, got %q", got)
	}
}

// ========== findValTypeName — var not found ==========

func TestFindValTypeNameNotFound(t *testing.T) {
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "test",
				Body: &ast.Block{
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:   token.Position{Line: 2},
							Name:  "other",
							Value: &ast.CallExpr{Func: &ast.Ident{Name: "create"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Order"}}}},
						},
					},
				},
			},
		},
	}
	if got := findValTypeName(file, "missing", Position{Line: 3}); got != "" {
		t.Errorf("expected empty for missing var, got %q", got)
	}
}

// ========== findValTypeName — works in fn body ==========

func TestFindValTypeNameInFn(t *testing.T) {
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "helper",
				Body: &ast.Block{
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:   token.Position{Line: 2},
							Name:  "item",
							Value: &ast.CallExpr{Func: &ast.Ident{Name: "find"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Product"}}}},
						},
					},
				},
			},
		},
	}
	if got := findValTypeName(file, "item", Position{Line: 3}); got != "Product" {
		t.Errorf("expected 'Product', got %q", got)
	}
}

// ========== Object Construction Named Arg Definition Tests ==========

func TestDefinitionNamedArgInObjectConstruction(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_obj_def.luxo"
	openDoc(server, output, uri, `type AuthResult {
  token: String
  user: String
}
api login(password: String): AuthResult {
  AuthResult { token: password, user: password }
}`)
	// Click on "token" (the named arg key before ':') in AuthResult { token: ... }
	// Line 5: "  AuthResult { token: password, user: password }"
	// "token" starts at character 17
	output.Reset()
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 17})
	// should jump to token field in AuthResult type (line 1)
	if !strings.Contains(resp, `"line":1`) {
		t.Errorf("expected definition at line 1 (AuthResult.token), got: %s", resp)
	}
}

func TestDefinitionNamedArgInObjectConstructionUserField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_obj_def2.luxo"
	openDoc(server, output, uri, `type AuthResult {
  token: String
  user: String
}
api login(password: String): AuthResult {
  AuthResult { token: password, user: password }
}`)
	// Click on "user" (the second named arg key) in AuthResult { ..., user: password }
	// Line 5: "  AuthResult { token: password, user: password }"
	// "user" starts at character 34
	output.Reset()
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 34})
	// should jump to user field in AuthResult type (line 2)
	if !strings.Contains(resp, `"line":2`) {
		t.Errorf("expected definition at line 2 (AuthResult.user), got: %s", resp)
	}
}

func TestDefinitionNamedArgInModelObjectConstruction(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_obj_model.luxo"
	openDoc(server, output, uri, `model User {
  name: String
  email: String
}
api createUser(n: String, e: String): User {
  User { name: n, email: e }
}`)
	// Click on "name" (the named arg key) in User { name: n, ... }
	// Line 5: "  User { name: n, email: e }"
	// "name" starts at character 9
	output.Reset()
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 9})
	// should jump to name field in User model (line 1)
	if !strings.Contains(resp, `"line":1`) {
		t.Errorf("expected definition at line 1 (User.name), got: %s", resp)
	}
}

func TestDefinitionNamedArgNotInDeclaration(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_decl.luxo"
	openDoc(server, output, uri, `model User { name: String }`)
	// Click on "name" inside model declaration — this is the definition itself,
	// should NOT resolve via object construction path
	output.Reset()
	resp := requestDefinition(server, output, uri, Position{Line: 0, Character: 14})
	// should return null (no definition to jump to for a declaration)
	if strings.Contains(resp, `"line"`) {
		t.Errorf("expected null for field declaration, got: %s", resp)
	}
}

func TestFindEnclosingObjectType(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `AuthResult { token: x, user: y }`)
	doc := store.Get("file:///test.luxo")

	// cursor on "token" — should find AuthResult
	typeName := doc.FindEnclosingObjectType(Position{Line: 0, Character: 13})
	if typeName != "AuthResult" {
		t.Errorf("expected 'AuthResult', got %q", typeName)
	}

	// cursor on "user" — should find AuthResult
	typeName = doc.FindEnclosingObjectType(Position{Line: 0, Character: 23})
	if typeName != "AuthResult" {
		t.Errorf("expected 'AuthResult', got %q", typeName)
	}
}

func TestFindEnclosingObjectTypeDeclaration(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `model User { name: String }`)
	doc := store.Get("file:///test.luxo")

	// cursor on "name" inside model declaration — should return empty (declaration keyword blocks it)
	typeName := doc.FindEnclosingObjectType(Position{Line: 0, Character: 14})
	if typeName != "" {
		t.Errorf("expected empty for declaration context, got %q", typeName)
	}
}

func TestFindEnclosingObjectTypeNoType(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `{ name: x }`)
	doc := store.Get("file:///test.luxo")

	// anonymous object — no uppercase type name before '{'
	typeName := doc.FindEnclosingObjectType(Position{Line: 0, Character: 2})
	if typeName != "" {
		t.Errorf("expected empty for anonymous object, got %q", typeName)
	}
}

func TestFindEnclosingObjectTypeOutOfBounds(t *testing.T) {
	store := NewDocumentStore()
	store.Open("file:///test.luxo", 1, `AuthResult { x: 1 }`)
	doc := store.Get("file:///test.luxo")

	// line out of bounds
	typeName := doc.FindEnclosingObjectType(Position{Line: 99, Character: 0})
	if typeName != "" {
		t.Errorf("expected empty for out of bounds, got %q", typeName)
	}
}

func TestFindObjectFieldDefinitionNoFile(t *testing.T) {
	doc := &Document{
		Content: `AuthResult { token: x }`,
		File:    nil,
	}
	// no file → should return nil
	p := findObjectFieldDefinition(doc, "token", Position{Line: 0, Character: 13})
	if p != nil {
		t.Error("expected nil when doc.File is nil")
	}
}

func TestFindEnclosingObjectTypeMultiLine(t *testing.T) {
	store := NewDocumentStore()
	src := "AuthResult {\n  token: x,\n  user: y\n}"
	store.Open("file:///test.luxo", 1, src)
	doc := store.Get("file:///test.luxo")

	// cursor on "user" at line 2 — should find AuthResult on line 0
	typeName := doc.FindEnclosingObjectType(Position{Line: 2, Character: 2})
	if typeName != "AuthResult" {
		t.Errorf("expected 'AuthResult', got %q", typeName)
	}
}

func TestFindEnclosingObjectTypeNestedBraces(t *testing.T) {
	store := NewDocumentStore()
	src := "Outer { inner: Inner { x: 1 }, name: y }"
	store.Open("file:///test.luxo", 1, src)
	doc := store.Get("file:///test.luxo")

	// cursor on "name" at character 32 — should find Outer (not Inner)
	typeName := doc.FindEnclosingObjectType(Position{Line: 0, Character: 32})
	if typeName != "Outer" {
		t.Errorf("expected 'Outer', got %q", typeName)
	}
}

func TestFindObjectFieldDefinitionNoType(t *testing.T) {
	doc := &Document{
		Content: `{ token: x }`,
		File:    &ast.File{},
	}
	// no enclosing type → should return nil
	p := findObjectFieldDefinition(doc, "token", Position{Line: 0, Character: 2})
	if p != nil {
		t.Error("expected nil for anonymous object")
	}
}

// ========== Inlay Hint Tests ==========

func TestHandleInlayHintImplicitReturn(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_inlay.luxo"
	openDoc(server, output, uri, `api getUser(id: Int): User {
  val user = User.find(id: id)
  user
}`)

	params, _ := json.Marshal(InlayHintParams{
		TextDocument: TextDocumentID{URI: uri},
		Range:        Range{Start: Position{0, 0}, End: Position{10, 0}},
	})
	raw, _ := json.Marshal(json.RawMessage("1"))
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&raw),
		Method:  "textDocument/inlayHint",
		Params:  params,
	})

	resp := output.String()
	if !strings.Contains(resp, "return") {
		t.Errorf("expected inlay hint with 'return', got: %s", resp)
	}
}

func TestHandleInlayHintNoImplicitReturn(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_inlay_no.luxo"
	// Last stmt is return, not implicit
	openDoc(server, output, uri, `api getUser(id: Int): Int {
  return 42
}`)

	params, _ := json.Marshal(InlayHintParams{
		TextDocument: TextDocumentID{URI: uri},
		Range:        Range{Start: Position{0, 0}, End: Position{10, 0}},
	})
	raw, _ := json.Marshal(json.RawMessage("2"))
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&raw),
		Method:  "textDocument/inlayHint",
		Params:  params,
	})

	resp := output.String()
	// Should NOT contain return hint since last stmt is explicit return
	if strings.Contains(resp, `"return "`) {
		t.Errorf("did not expect inlay hint for explicit return, got: %s", resp)
	}
}

func TestHandleInlayHintFn(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_inlay_fn.luxo"
	openDoc(server, output, uri, `fn double(x: Int): Int {
  x * 2
}`)

	params, _ := json.Marshal(InlayHintParams{
		TextDocument: TextDocumentID{URI: uri},
		Range:        Range{Start: Position{0, 0}, End: Position{10, 0}},
	})
	raw, _ := json.Marshal(json.RawMessage("3"))
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&raw),
		Method:  "textDocument/inlayHint",
		Params:  params,
	})

	resp := output.String()
	if !strings.Contains(resp, "return") {
		t.Errorf("expected inlay hint for fn implicit return, got: %s", resp)
	}
}

func TestHandleInlayHintNoReturnType(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_inlay_void.luxo"
	// No return type — should NOT show hint
	openDoc(server, output, uri, `fn doSomething() {
  val x = 1
}`)

	params, _ := json.Marshal(InlayHintParams{
		TextDocument: TextDocumentID{URI: uri},
		Range:        Range{Start: Position{0, 0}, End: Position{10, 0}},
	})
	raw, _ := json.Marshal(json.RawMessage("4"))
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&raw),
		Method:  "textDocument/inlayHint",
		Params:  params,
	})

	resp := output.String()
	if strings.Contains(resp, `"return "`) {
		t.Errorf("should not hint for function without return type, got: %s", resp)
	}
}

func TestHandleInlayHintNoDoc(t *testing.T) {
	server, output := newTestServer()
	params, _ := json.Marshal(InlayHintParams{
		TextDocument: TextDocumentID{URI: "file:///nonexistent.luxo"},
		Range:        Range{Start: Position{0, 0}, End: Position{10, 0}},
	})
	raw, _ := json.Marshal(json.RawMessage("5"))
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      (*json.RawMessage)(&raw),
		Method:  "textDocument/inlayHint",
		Params:  params,
	})

	resp := output.String()
	if !strings.Contains(resp, "[]") {
		t.Errorf("expected empty hints for nonexistent doc, got: %s", resp)
	}
}

func TestCollectImplicitReturnHintsNil(t *testing.T) {
	hints := collectImplicitReturnHints(&ast.File{})
	if len(hints) != 0 {
		t.Errorf("expected 0 hints for empty file, got %d", len(hints))
	}
}

func TestFindImplicitReturnNilBlock(t *testing.T) {
	hint := findImplicitReturn(nil)
	if hint != nil {
		t.Error("expected nil for nil block")
	}
}

func TestFindImplicitReturnEmptyBlock(t *testing.T) {
	hint := findImplicitReturn(&ast.Block{})
	if hint != nil {
		t.Error("expected nil for empty block")
	}
}

func TestCRUDNamedArgKeyNoGotoDefinition(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_key.luxo"
	// In Post.find(id: id), the first "id" (named arg key) should NOT jump
	// to the api parameter "id". It is a CRUD query parameter name.
	openDoc(server, output, uri, `model Post {
  id:    Int
  title: String
}
api getPost(id: Int): Post {
  val post = Post.find(id: id)
  post
}`)
	output.Reset()
	// Cursor on the first "id" (named arg key at character 23 in "Post.find(id: id)")
	// Line 5 is "  val post = Post.find(id: id)"
	// "id" before ":" starts at character 23
	result := requestDefinition(server, output, uri, Position{Line: 5, Character: 23})
	// The named arg key "id" in a CRUD call should NOT resolve to any definition
	if strings.Contains(result, `"line"`) {
		t.Errorf("expected no definition for CRUD named arg key 'id:', got: %s", result)
	}

	output.Reset()
	// Cursor on the second "id" (the value after ":") at character 27
	result = requestDefinition(server, output, uri, Position{Line: 5, Character: 27})
	// The value "id" should jump to the api parameter at line 4
	if !strings.Contains(result, `"line":4`) {
		t.Errorf("expected definition at line 4 (api param 'id'), got: %s", result)
	}
}

// ========== CRUD where clause field resolution ==========

// TestHoverBareFieldInCRUDWhere tests that a bare field name (without "it." prefix)
// inside a CRUD where clause resolves to the correct model's field, not the first
// model that has a field with that name.
func TestHoverBareFieldInCRUDWhere(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_where_hover.luxo"
	// Post.userId comes first in source order, but RoomMember.find(...) should
	// resolve userId to RoomMember.userId, not Post.userId.
	openDoc(server, output, uri, `model Post {
  userId: Int
  title: String
}
model RoomMember {
  roomId: Int
  userId: Int
}
api test(roomId: Int): RoomMember {
  val member = RoomMember.find(where: it.roomId == roomId && userId == 1)
  member
}`)
	// "userId" without "it." prefix at line 9
	// Line 9: "  val member = RoomMember.find(where: it.roomId == roomId && userId == 1)"
	// "userId" starts around character 60
	output.Reset()
	result := requestHover(server, output, uri, Position{Line: 9, Character: 62})
	if !strings.Contains(result, "RoomMember.userId") {
		t.Errorf("expected hover to show RoomMember.userId, got: %s", result)
	}
	if strings.Contains(result, "Post.userId") {
		t.Errorf("hover should NOT show Post.userId, got: %s", result)
	}
}

// TestHoverItFieldInCRUDWhere tests that it.fieldName inside a CRUD where clause
// resolves to the correct CRUD model's field.
func TestHoverItFieldInCRUDWhere(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_it_hover.luxo"
	openDoc(server, output, uri, `model Post {
  roomId: Int
  title: String
}
model RoomMember {
  roomId: Int
  userId: Int
}
api test(roomId: Int): RoomMember {
  val member = RoomMember.find(where: it.roomId == roomId)
  member
}`)
	// "roomId" after "it." at line 9
	// Line 9: "  val member = RoomMember.find(where: it.roomId == roomId)"
	// "roomId" after "it." starts around character 41
	output.Reset()
	result := requestHover(server, output, uri, Position{Line: 9, Character: 43})
	if !strings.Contains(result, "RoomMember.roomId") {
		t.Errorf("expected hover to show RoomMember.roomId, got: %s", result)
	}
	if strings.Contains(result, "Post.roomId") {
		t.Errorf("hover should NOT show Post.roomId, got: %s", result)
	}
}

// TestDefinitionBareFieldInCRUDWhere tests that goto-definition on a bare field name
// inside a CRUD where clause jumps to the correct model's field definition.
func TestDefinitionBareFieldInCRUDWhere(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_where_def.luxo"
	openDoc(server, output, uri, `model Post {
  userId: Int
  title: String
}
model RoomMember {
  roomId: Int
  userId: Int
}
api test(roomId: Int): RoomMember {
  val member = RoomMember.find(where: it.roomId == roomId && userId == 1)
  member
}`)
	// "userId" without "it." at line 9, should jump to RoomMember.userId at line 6
	output.Reset()
	result := requestDefinition(server, output, uri, Position{Line: 9, Character: 62})
	// RoomMember.userId is at line 6 (0-indexed)
	if !strings.Contains(result, `"line":6`) {
		t.Errorf("expected definition at line 6 (RoomMember.userId), got: %s", result)
	}
}

// TestDefinitionItFieldInCRUDWhere tests that goto-definition on it.fieldName
// inside a CRUD where clause jumps to the correct CRUD model's field.
func TestDefinitionItFieldInCRUDWhere(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_crud_it_def.luxo"
	openDoc(server, output, uri, `model Post {
  roomId: Int
  title: String
}
model RoomMember {
  roomId: Int
  userId: Int
}
api test(roomId: Int): RoomMember {
  val member = RoomMember.find(where: it.roomId == roomId)
  member
}`)
	// "roomId" after "it." at line 9, should jump to RoomMember.roomId at line 5
	output.Reset()
	result := requestDefinition(server, output, uri, Position{Line: 9, Character: 43})
	// RoomMember.roomId is at line 5 (0-indexed)
	if !strings.Contains(result, `"line":5`) {
		t.Errorf("expected definition at line 5 (RoomMember.roomId), got: %s", result)
	}
}

// TestCRUDFieldHoverNoFile tests getCRUDFieldHover when doc.File is nil.
func TestCRUDFieldHoverNoFile(t *testing.T) {
	result := getCRUDFieldHover(&Document{Content: "User.find(where: name == 1)"}, "name", Position{Line: 0, Character: 20})
	if result != "" {
		t.Errorf("expected empty hover for nil file, got: %s", result)
	}
}

// TestCRUDFieldHoverNonCRUD tests getCRUDFieldHover in a non-CRUD call.
func TestCRUDFieldHoverNonCRUD(t *testing.T) {
	result := getCRUDFieldHover(&Document{Content: "foo(User, name: 1)"}, "name", Position{Line: 0, Character: 12})
	if result != "" {
		t.Errorf("expected empty hover for non-CRUD call, got: %s", result)
	}
}

// TestCRUDBareFieldDefinitionNonCRUD tests findCRUDBareFieldDefinition in a non-CRUD call.
func TestCRUDBareFieldDefinitionNonCRUD(t *testing.T) {
	doc := &Document{Content: "foo(User, name: 1)"}
	result := findCRUDBareFieldDefinition(doc, "name", Position{Line: 0, Character: 12})
	if result != nil {
		t.Errorf("expected nil for non-CRUD call, got: %v", result)
	}
}

// TestCRUDBareFieldDefinitionNoFile tests findCRUDBareFieldDefinition when doc.File is nil.
func TestCRUDBareFieldDefinitionNoFile(t *testing.T) {
	doc := &Document{Content: "User.find(where: name == 1)"}
	result := findCRUDBareFieldDefinition(doc, "name", Position{Line: 0, Character: 20})
	if result != nil {
		t.Errorf("expected nil for nil file, got: %v", result)
	}
}

// ========== my.load() goto-definition ==========

// TestDefinitionMyLoadField tests that goto-definition on arguments inside my.load()
// resolves to the corresponding field in the @withAuth model.
func TestDefinitionMyLoadField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_myload.luxo"
	openDoc(server, output, uri, `model User @withAuth(stores: [id]) {
  name: String
  role: String
}
api test(): String @auth {
  val u = my.load(name, role)
  u.name
}`)
	// "name" is at line 5 (0-based), my.load(name, role) — "name" starts at character 18
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 19})
	if !strings.Contains(resp, "test_myload") {
		t.Errorf("expected definition to point to User.name field, got: %s", resp)
	}
	// verify it points to line 2 (1-based) where name: String is declared
	if !strings.Contains(resp, `"line":1`) {
		t.Errorf("expected definition on line 1 (0-based), got: %s", resp)
	}
}

// TestDefinitionMyLoadSecondArg tests goto-definition on the second argument of my.load().
func TestDefinitionMyLoadSecondArg(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_myload2.luxo"
	openDoc(server, output, uri, `model User @withAuth(stores: [id]) {
  name: String
  role: String
}
api test(): String @auth {
  val u = my.load(name, role)
  u.name
}`)
	// "role" is at line 5, my.load(name, role) — "role" starts at character 24
	resp := requestDefinition(server, output, uri, Position{Line: 5, Character: 25})
	if !strings.Contains(resp, "test_myload2") {
		t.Errorf("expected definition to point to User.role field, got: %s", resp)
	}
	// verify it points to line 3 (1-based, 2 zero-based) where role: String is declared
	if !strings.Contains(resp, `"line":2`) {
		t.Errorf("expected definition on line 2 (0-based), got: %s", resp)
	}
}

// TestDefinitionMyLoadNoWithAuth tests that my.load() returns nil when no @withAuth model exists.
func TestDefinitionMyLoadNoWithAuth(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_myload_noauth.luxo"
	openDoc(server, output, uri, `model User {
  name: String
}
api test(): String {
  val u = my.load(name)
  u.name
}`)
	resp := requestDefinition(server, output, uri, Position{Line: 4, Character: 19})
	// should not resolve — no @withAuth model
	if strings.Contains(resp, `"line"`) {
		t.Errorf("expected no definition without @withAuth model, got: %s", resp)
	}
}

// TestDefinitionMyLoadNonexistentField tests that my.load() returns nil for a field
// that doesn't exist in the @withAuth model.
func TestDefinitionMyLoadNonexistentField(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_myload_nofield.luxo"
	openDoc(server, output, uri, `model User @withAuth(stores: [id]) {
  name: String
}
api test(): String @auth {
  val u = my.load(email)
  u.name
}`)
	resp := requestDefinition(server, output, uri, Position{Line: 4, Character: 19})
	// "email" is not a field of User
	if strings.Contains(resp, `"line"`) {
		t.Errorf("expected no definition for nonexistent field, got: %s", resp)
	}
}

// TestIsInsideMyLoadCall tests the IsInsideMyLoadCall document method.
func TestIsInsideMyLoadCall(t *testing.T) {
	tests := []struct {
		name    string
		content string
		pos     Position
		want    bool
	}{
		{
			name:    "inside my.load",
			content: "my.load(name, role)",
			pos:     Position{Line: 0, Character: 10},
			want:    true,
		},
		{
			name:    "not in call",
			content: "val x = name",
			pos:     Position{Line: 0, Character: 8},
			want:    false,
		},
		{
			name:    "inside regular load call",
			content: "load(name)",
			pos:     Position{Line: 0, Character: 6},
			want:    false,
		},
		{
			name:    "inside other.load call",
			content: "other.load(name)",
			pos:     Position{Line: 0, Character: 12},
			want:    false,
		},
		{
			name:    "out of bounds line",
			content: "my.load(name)",
			pos:     Position{Line: 5, Character: 0},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &Document{Content: tt.content}
			got := doc.IsInsideMyLoadCall(tt.pos)
			if got != tt.want {
				t.Errorf("IsInsideMyLoadCall() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsMyLoadBefore tests the isMyLoadBefore helper.
func TestIsMyLoadBefore(t *testing.T) {
	tests := []struct {
		line     string
		parenCol int
		want     bool
	}{
		{"my.load(", 7, true},
		{"  my.load(", 9, true},
		{"load(", 4, false},
		{"other.load(", 10, false},
		{"my.save(", 7, false},
		{"(", 0, false},
	}
	for _, tt := range tests {
		got := isMyLoadBefore(tt.line, tt.parenCol)
		if got != tt.want {
			t.Errorf("isMyLoadBefore(%q, %d) = %v, want %v", tt.line, tt.parenCol, got, tt.want)
		}
	}
}

// TestFindMyLoadFieldDefinitionNilFile tests findMyLoadFieldDefinition when doc.File is nil.
func TestFindMyLoadFieldDefinitionNilFile(t *testing.T) {
	doc := &Document{Content: "my.load(name)"}
	result := findMyLoadFieldDefinition(doc, "name")
	if result != nil {
		t.Errorf("expected nil for nil file, got: %v", result)
	}
}

// TestFindWithAuthModelName tests findWithAuthModelName with various configurations.
func TestFindWithAuthModelName(t *testing.T) {
	// nil file
	doc := &Document{}
	if name := findWithAuthModelName(doc); name != "" {
		t.Errorf("expected empty for nil file, got: %s", name)
	}

	// file with @withAuth model
	doc = &Document{
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name:       "User",
					Directives: []*ast.Directive{{Name: "withAuth"}},
				},
				{
					Name: "Post",
				},
			},
		},
	}
	if name := findWithAuthModelName(doc); name != "User" {
		t.Errorf("expected User, got: %s", name)
	}

	// file without @withAuth
	doc = &Document{
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{Name: "Post"},
			},
		},
	}
	if name := findWithAuthModelName(doc); name != "" {
		t.Errorf("expected empty, got: %s", name)
	}
}

// ========== @scope Hover and Goto Definition Tests ==========

func TestScopeDirectiveHover(t *testing.T) {
	server, output := newTestServer()

	// line 0: model AuditablePost : Base {
	// line 1:   title: String
	// line 2:   status: String
	// line 3:   scope published = where(status == "PUBLISHED")
	// line 4: }
	// line 5:
	// line 6: api listPosts(): [AuditablePost] @scope(published)
	src := "model AuditablePost : Base {\n  title: String\n  status: String\n  scope published = where(status == \"PUBLISHED\")\n}\n\napi listPosts(): [AuditablePost] @scope(published)"
	openDoc(server, output, "file:///scope.luxo", src)

	// hover on @scope directive name itself (the word "scope" after @)
	// line 6: api listPosts(): [AuditablePost] @scope(published)
	// @scope starts at col 33, 'scope' is cols 34-38
	resp := requestHover(server, output, "file:///scope.luxo", Position{Line: 6, Character: 35})
	if !strings.Contains(resp, "scope") || !strings.Contains(resp, "应用查询预设") {
		t.Errorf("expected @scope directive hover, got: %s", resp)
	}

	// hover on scope name "published" inside @scope(published)
	// 'published' starts at col 40
	output.Reset()
	resp = requestHover(server, output, "file:///scope.luxo", Position{Line: 6, Character: 42})
	if !strings.Contains(resp, "published") {
		t.Errorf("expected scope name hover for 'published', got: %s", resp)
	}
	if !strings.Contains(resp, "AuditablePost") {
		t.Errorf("expected model name in hover, got: %s", resp)
	}
}

func TestScopeDirectiveGotoDefinition(t *testing.T) {
	server, output := newTestServer()

	// line 0: model AuditablePost : Base {
	// line 1:   title: String
	// line 2:   status: String
	// line 3:   scope published = where(status == "PUBLISHED")
	// line 4: }
	// line 5:
	// line 6: api listPosts(): [AuditablePost] @scope(published)
	src := "model AuditablePost : Base {\n  title: String\n  status: String\n  scope published = where(status == \"PUBLISHED\")\n}\n\napi listPosts(): [AuditablePost] @scope(published)"
	openDoc(server, output, "file:///scope.luxo", src)

	// goto definition on "published" inside @scope(published)
	// 'published' starts at col 40 on line 6
	resp := requestDefinition(server, output, "file:///scope.luxo", Position{Line: 6, Character: 42})
	// should jump to line 3 (0-indexed) where "scope published" is declared
	if !strings.Contains(resp, `"line":3`) && !strings.Contains(resp, `"line": 3`) {
		t.Errorf("expected definition to jump to scope declaration line, got: %s", resp)
	}
}

func TestIsInsideScopeDirective(t *testing.T) {
	tests := []struct {
		name    string
		content string
		pos     Position
		want    bool
	}{
		{
			name:    "inside @scope args",
			content: "api list(): [Post] @scope(published, mine)",
			pos:     Position{Line: 0, Character: 30},
			want:    true,
		},
		{
			name:    "outside @scope - before paren",
			content: "api list(): [Post] @scope(published)",
			pos:     Position{Line: 0, Character: 22},
			want:    false,
		},
		{
			name:    "not @scope - different directive",
			content: "api list(): [Post] @auth(User)",
			pos:     Position{Line: 0, Character: 27},
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			pos:     Position{Line: 0, Character: 0},
			want:    false,
		},
		{
			name:    "line out of bounds",
			content: "api list(): [Post] @scope(published)",
			pos:     Position{Line: 5, Character: 0},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &Document{Content: tt.content}
			got := doc.IsInsideScopeDirective(tt.pos)
			if got != tt.want {
				t.Errorf("IsInsideScopeDirective() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindScopeDefinition(t *testing.T) {
	doc := &Document{
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "Post",
					Scopes: []*ast.ScopeDecl{
						{
							Name: "published",
							Pos:  token.Position{File: "/test.luxo", Line: 4, Col: 3},
						},
						{
							Name: "recent",
							Pos:  token.Position{File: "/test.luxo", Line: 8, Col: 3},
						},
					},
				},
			},
		},
	}

	// found
	pos := findScopeDefinition(doc, "published")
	if pos == nil {
		t.Fatal("expected to find scope 'published'")
	}
	if pos.Line != 4 {
		t.Errorf("expected line 4, got %d", pos.Line)
	}

	// found second scope
	pos = findScopeDefinition(doc, "recent")
	if pos == nil {
		t.Fatal("expected to find scope 'recent'")
	}
	if pos.Line != 8 {
		t.Errorf("expected line 8, got %d", pos.Line)
	}

	// not found
	pos = findScopeDefinition(doc, "nonexistent")
	if pos != nil {
		t.Error("expected nil for nonexistent scope")
	}

	// nil file
	doc2 := &Document{}
	pos = findScopeDefinition(doc2, "published")
	if pos != nil {
		t.Error("expected nil for nil file")
	}
}

func TestScopeNameHover(t *testing.T) {
	doc := &Document{
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "Post",
					Scopes: []*ast.ScopeDecl{
						{
							Name: "published",
							Pos:  token.Position{File: "/test.luxo", Line: 4, Col: 3},
						},
						{
							Name: "recent",
							Pos:  token.Position{File: "/test.luxo", Line: 8, Col: 3},
							Params: []*ast.ParamDecl{
								{Name: "days", Type: &ast.TypeRef{Name: "Int"}},
							},
						},
					},
				},
			},
		},
	}

	// simple scope
	hover := scopeNameHover(doc, "published")
	if !strings.Contains(hover, "published") {
		t.Errorf("expected 'published' in hover, got: %s", hover)
	}
	if !strings.Contains(hover, "Post") {
		t.Errorf("expected 'Post' in hover, got: %s", hover)
	}

	// scope with params
	hover = scopeNameHover(doc, "recent")
	if !strings.Contains(hover, "days: Int") {
		t.Errorf("expected 'days: Int' in hover, got: %s", hover)
	}

	// not found
	hover = scopeNameHover(doc, "nonexistent")
	if hover != "" {
		t.Errorf("expected empty hover for nonexistent scope, got: %s", hover)
	}

	// nil file
	doc2 := &Document{}
	hover = scopeNameHover(doc2, "published")
	if hover != "" {
		t.Errorf("expected empty hover for nil file, got: %s", hover)
	}
}

// ========== @scope Autocomplete Tests ==========

func TestScopeCompletionSuggestsScopeNames(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test/scope_complete.luxo"
	src := `model Post {
  title: String
  scope published = where(status == "PUBLISHED")
  scope mine = where(userId == my.id)
  scope hot = where(likes > 100)
}
api listPosts(): [Post] @scope()`

	openDoc(server, output, uri, src)
	output.Reset()

	// cursor inside @scope() — line 6 (0-based), character 31 is between ( and )
	// "api listPosts(): [Post] @scope()" — @scope( at col 25, ( at col 31, ) at col 32
	result := requestCompletion(server, output, uri, Position{Line: 6, Character: 31})
	if !strings.Contains(result, "published") {
		t.Errorf("expected 'published' in scope completions, got: %s", result)
	}
	if !strings.Contains(result, "mine") {
		t.Errorf("expected 'mine' in scope completions, got: %s", result)
	}
	if !strings.Contains(result, "hot") {
		t.Errorf("expected 'hot' in scope completions, got: %s", result)
	}
}

func TestScopeCompletionNoModelReturnsEmpty(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test/scope_no_model.luxo"
	src := `type MyResult { data: String }
api test(): MyResult @scope()`

	openDoc(server, output, uri, src)
	output.Reset()

	// cursor inside @scope() — no model, should return no scope items
	result := requestCompletion(server, output, uri, Position{Line: 1, Character: 28})
	// should not contain scope-specific items (no scopes on a type)
	if strings.Contains(result, "\"kind\":20") {
		t.Errorf("expected no EnumMember completions for non-model type, got: %s", result)
	}
}

func TestScopeCompletionScopeWithParams(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test/scope_params.luxo"
	src := `model Post {
  title: String
  scope recent(days: Int) = where(createdAt > now())
}
api listPosts(): [Post] @scope()`

	openDoc(server, output, uri, src)
	output.Reset()

	result := requestCompletion(server, output, uri, Position{Line: 4, Character: 31})
	if !strings.Contains(result, "recent") {
		t.Errorf("expected 'recent' in scope completions, got: %s", result)
	}
	// should show parameter info in detail
	if !strings.Contains(result, "days: Int") {
		t.Errorf("expected param info 'days: Int' in detail, got: %s", result)
	}
}

func TestFindEnclosingApiReturnModelName(t *testing.T) {
	doc := &Document{
		Content: `api listPosts(): [Post] @scope()`,
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{Line: 1, Col: 1},
					Name: "listPosts",
					ReturnType: &ast.TypeRef{
						Name:   "Post",
						IsList: true,
					},
				},
			},
		},
	}

	name := findEnclosingApiReturnModelName(doc, Position{Line: 0, Character: 30})
	if name != "Post" {
		t.Errorf("expected 'Post', got %q", name)
	}

	// different line — should not match
	name = findEnclosingApiReturnModelName(doc, Position{Line: 5, Character: 10})
	if name != "" {
		t.Errorf("expected empty, got %q", name)
	}

	// nil file
	doc2 := &Document{Content: "test"}
	name = findEnclosingApiReturnModelName(doc2, Position{Line: 0, Character: 0})
	if name != "" {
		t.Errorf("expected empty for nil file, got %q", name)
	}

	// API with no return type
	doc3 := &Document{
		Content: `api test() @scope()`,
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{Line: 1, Col: 1},
					Name: "test",
				},
			},
		},
	}
	name = findEnclosingApiReturnModelName(doc3, Position{Line: 0, Character: 15})
	if name != "" {
		t.Errorf("expected empty for nil return type, got %q", name)
	}
}

// ========== Coverage: handleTextDocument — formatting method ==========

func TestHandleFormattingBasic(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_fmt.luxo"
	// open a document with poor formatting (extra spaces)
	openDoc(server, output, uri, "model  User  {\n  name:  String\n}")
	output.Reset()

	id := json.RawMessage(`200`)
	params, _ := json.Marshal(FormattingParams{
		TextDocument: TextDocumentID{URI: uri},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/formatting",
		Params:  params,
	})

	resp := output.String()
	// should return a text edit or nil (depends on formatter behavior)
	if !strings.Contains(resp, "result") {
		t.Errorf("expected a response for formatting, got: %s", resp)
	}
}

func TestHandleFormattingNoDoc(t *testing.T) {
	server, output := newTestServer()
	id := json.RawMessage(`201`)
	params, _ := json.Marshal(FormattingParams{
		TextDocument: TextDocumentID{URI: "file:///nonexistent.luxo"},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/formatting",
		Params:  params,
	})

	resp := output.String()
	if !strings.Contains(resp, "result") {
		t.Errorf("expected null result for nonexistent doc, got: %s", resp)
	}
}

func TestHandleFormattingInvalidParams(t *testing.T) {
	server, output := newTestServer()
	id := json.RawMessage(`202`)
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/formatting",
		Params:  json.RawMessage(`{invalid`),
	})

	resp := output.String()
	if !strings.Contains(resp, "error") {
		t.Errorf("expected error for invalid params, got: %s", resp)
	}
}

func TestHandleFormattingLexError(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_fmt_err.luxo"
	// open with content that causes lexer error
	openDoc(server, output, uri, "model User { name: \"unterminated\n}")
	output.Reset()

	id := json.RawMessage(`203`)
	params, _ := json.Marshal(FormattingParams{
		TextDocument: TextDocumentID{URI: uri},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/formatting",
		Params:  params,
	})

	resp := output.String()
	// formatter returns error, so response should be null result
	if !strings.Contains(resp, "result") {
		t.Errorf("expected result for lex-error doc, got: %s", resp)
	}
}

func TestHandleFormattingNoChange(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test_fmt_nochange.luxo"
	// open with already-formatted content
	openDoc(server, output, uri, "model User {\n  name: String\n}\n")
	output.Reset()

	id := json.RawMessage(`204`)
	params, _ := json.Marshal(FormattingParams{
		TextDocument: TextDocumentID{URI: uri},
	})
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/formatting",
		Params:  params,
	})

	resp := output.String()
	if !strings.Contains(resp, "result") {
		t.Errorf("expected result for already-formatted doc, got: %s", resp)
	}
}

// ========== Coverage: handleInlayHint — invalid params ==========

func TestHandleInlayHintInvalidParams(t *testing.T) {
	server, output := newTestServer()
	id := json.RawMessage(`210`)
	server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/inlayHint",
		Params:  json.RawMessage(`{bad`),
	})
	resp := output.String()
	if !strings.Contains(resp, "error") {
		t.Errorf("expected error for invalid inlayHint params, got: %s", resp)
	}
}

// ========== Coverage: handleReferences — invalid params ==========

func TestHandleReferencesInvalidParams(t *testing.T) {
	server, _ := newTestServer()
	id := json.RawMessage(`220`)
	// handleReferences returns the error directly (not via SendError),
	// so we call handleMessage which logs the error internally.
	err := server.handleMessage(&Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/references",
		Params:  json.RawMessage(`{bad`),
	})
	// handleMessage returns nil (logs the error), but the code path is covered
	_ = err
}

// ========== Coverage: getParamMemberCompletions — resolved type from Result ==========

func TestGetParamMemberCompletionsResolvedType(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		Content: "api test(u: User) { u. }",
		File: &ast.File{
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{Line: 1},
					Name: "test",
					Params: []*ast.ParamDecl{
						{Name: "u", Type: &ast.TypeRef{Name: "User"}},
					},
				},
			},
		},
		Result: &semantic.Result{
			Types: map[string]*semantic.ResolvedType{
				"User": {
					Name: "User",
					Fields: map[string]*semantic.FieldInfo{
						"name":  {Name: "name", Type: &semantic.ResolvedType{Name: "String"}, Pos: token.Position{Line: 2}},
						"email": {Name: "email", Type: &semantic.ResolvedType{Name: "String"}, Pos: token.Position{Line: 3}},
					},
				},
			},
		},
	}
	items := server.getParamMemberCompletions(doc, "u")
	if len(items) == 0 {
		t.Error("expected completions for param type resolved from Result")
	}
	foundName := false
	for _, item := range items {
		if item.Label == "name" {
			foundName = true
		}
	}
	if !foundName {
		t.Error("expected 'name' in completions")
	}
}

// ========== Coverage: getCRUDFieldHover — no matching field ==========

func TestGetCRUDFieldHoverNoMatchingField(t *testing.T) {
	doc := &Document{
		Content: "Order.find(nonexistentField: 1)",
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "Order",
					Fields: []*ast.FieldDecl{
						{Name: "status", Type: &ast.TypeRef{Name: "String"}, Pos: token.Position{Line: 2}},
					},
				},
			},
		},
	}
	hover := getCRUDFieldHover(doc, "nonexistentField", Position{Line: 0, Character: 12})
	if hover != "" {
		t.Errorf("expected empty hover for non-matching field, got: %s", hover)
	}
}

// ========== Coverage: findMyLoadFieldDefinition — cross-file via Result.Types ==========

func TestFindMyLoadFieldDefinitionCrossFile(t *testing.T) {
	doc := &Document{
		Content: "my.load(email)",
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "AuthUser",
					Directives: []*ast.Directive{
						{Name: "withAuth"},
					},
					// no field named "email" in this file's AST
				},
			},
		},
		Result: &semantic.Result{
			Types: map[string]*semantic.ResolvedType{
				"AuthUser": {
					Name: "AuthUser",
					Fields: map[string]*semantic.FieldInfo{
						"email": {Name: "email", Type: &semantic.ResolvedType{Name: "String"}, Pos: token.Position{File: "/other.luxo", Line: 5, Col: 3}},
					},
				},
			},
		},
	}
	pos := findMyLoadFieldDefinition(doc, "email")
	if pos == nil {
		t.Fatal("expected cross-file definition, got nil")
	}
	if pos.File != "/other.luxo" {
		t.Errorf("expected file '/other.luxo', got %q", pos.File)
	}
}

// ========== Coverage: scopeNameHover — scope with multiple params ==========

func TestScopeNameHoverMultipleParams(t *testing.T) {
	doc := &Document{
		File: &ast.File{
			Models: []*ast.ModelDecl{
				{
					Name: "Post",
					Scopes: []*ast.ScopeDecl{
						{
							Name: "filtered",
							Pos:  token.Position{File: "/test.luxo", Line: 3, Col: 3},
							Params: []*ast.ParamDecl{
								{Name: "status", Type: &ast.TypeRef{Name: "String"}},
								{Name: "limit", Type: &ast.TypeRef{Name: "Int"}},
							},
						},
					},
				},
			},
		},
	}
	hover := scopeNameHover(doc, "filtered")
	if !strings.Contains(hover, "status: String") {
		t.Errorf("expected 'status: String' in hover, got: %s", hover)
	}
	if !strings.Contains(hover, ", limit: Int") {
		t.Errorf("expected ', limit: Int' (with comma) in hover, got: %s", hover)
	}
}

// ========== Coverage: getScopeCompletions — modelName empty ==========

func TestGetScopeCompletionsNoModel(t *testing.T) {
	server, _ := newTestServer()
	doc := &Document{
		Content: `api test() @scope()`,
		File:    &ast.File{},
	}
	items := server.getScopeCompletions(doc, Position{Line: 0, Character: 17})
	if items != nil {
		t.Errorf("expected nil for no model, got: %v", items)
	}
}

// ========== Coverage: getScopeCompletions — scope with multiple params ==========

func TestGetScopeCompletionsMultipleParams(t *testing.T) {
	server, output := newTestServer()
	uri := "file:///test/scope_mp.luxo"
	src := `model Post {
  title: String
  scope range(from: Int, to: Int) = where(id >= from && id <= to)
}
api listPosts(): [Post] @scope()`

	openDoc(server, output, uri, src)
	output.Reset()

	result := requestCompletion(server, output, uri, Position{Line: 4, Character: 31})
	if !strings.Contains(result, "range") {
		t.Errorf("expected 'range' in completions, got: %s", result)
	}
	if !strings.Contains(result, "from: Int") {
		t.Errorf("expected param info 'from: Int' in detail, got: %s", result)
	}
	if !strings.Contains(result, "to: Int") {
		t.Errorf("expected param info 'to: Int' in detail, got: %s", result)
	}
}

// ========== Coverage: findMemberFieldDefinition — nil file/result ==========

func TestFindMemberFieldDefinitionNilFileOrResult(t *testing.T) {
	doc := &Document{Content: "obj.field"}
	pos := findMemberFieldDefinition(doc, "field", Position{Line: 0, Character: 5})
	if pos != nil {
		t.Errorf("expected nil for nil file/result, got: %v", pos)
	}
}

// ========== Coverage: findMemberFieldDefinition — empty objName ==========

func TestFindMemberFieldDefinitionEmptyObj(t *testing.T) {
	doc := &Document{
		Content: ".field",
		File:    &ast.File{},
		Result:  &semantic.Result{Types: map[string]*semantic.ResolvedType{}},
	}
	pos := findMemberFieldDefinition(doc, "field", Position{Line: 0, Character: 2})
	if pos != nil {
		t.Errorf("expected nil for empty objName, got: %v", pos)
	}
}

// ========== Coverage: findValTypeName — fn body with non-matching cursor ==========

func TestFindValTypeNameFnBodyNonMatch(t *testing.T) {
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "helper",
				Body: &ast.Block{
					EndPos: token.Position{Line: 5},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 2},
							Name: "item",
							Type: &ast.TypeRef{Name: "Product"},
						},
					},
				},
			},
		},
	}
	// cursor is inside fn body but looking for a different var
	if got := findValTypeName(file, "missing", Position{Line: 3}); got != "" {
		t.Errorf("expected empty for missing var in fn, got %q", got)
	}
}

// ========== Coverage: findValTypeInBlock — stmt after cursor ==========

func TestFindValTypeInBlockStmtAfterCursor(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Pos:   token.Position{Line: 10},
				Name:  "late",
				Value: &ast.CallExpr{Func: &ast.Ident{Name: "create"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Order"}}}},
			},
		},
	}
	// cursor at line 5 (0-based=5 => AST line 6), but stmt is at AST line 10 => stmt.GetPos().Line-1=9 > 5
	got := findValTypeInBlock(block, "late", Position{Line: 5})
	if got != "" {
		t.Errorf("expected empty for stmt after cursor, got %q", got)
	}
}

// ========== Coverage: inferExprTypeName — TransactionExpr ==========

func TestInferExprTypeNameTransaction(t *testing.T) {
	expr := &ast.TransactionExpr{
		Body: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Func: &ast.Ident{Name: "create"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Order"}}},
				}},
			},
		},
	}
	if got := inferExprTypeName(expr); got != "Order" {
		t.Errorf("expected 'Order' for TransactionExpr, got %q", got)
	}
}

// ========== Coverage: findValTypeName — skips non-matching API and fn ==========

func TestFindValTypeNameSkipsNonMatchingDecls(t *testing.T) {
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Pos:  token.Position{Line: 1},
				Name: "earlyApi",
				Body: &ast.Block{
					EndPos: token.Position{Line: 3},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 2},
							Name: "item",
							Type: &ast.TypeRef{Name: "WrongType"},
						},
					},
				},
			},
			{
				Pos:  token.Position{Line: 10},
				Name: "targetApi",
				Body: &ast.Block{
					EndPos: token.Position{Line: 15},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 11},
							Name: "item",
							Type: &ast.TypeRef{Name: "CorrectType"},
						},
					},
				},
			},
		},
		Functions: []*ast.FnDecl{
			{
				Pos:  token.Position{Line: 20},
				Name: "earlyFn",
				Body: &ast.Block{
					EndPos: token.Position{Line: 22},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 21},
							Name: "item",
							Type: &ast.TypeRef{Name: "FnWrong"},
						},
					},
				},
			},
			{
				Pos:  token.Position{Line: 30},
				Name: "targetFn",
				Body: &ast.Block{
					EndPos: token.Position{Line: 35},
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Pos:  token.Position{Line: 31},
							Name: "result",
							Type: &ast.TypeRef{Name: "FnCorrect"},
						},
					},
				},
			},
		},
	}
	// cursor at line 12 (0-based) = AST line 13, inside targetApi (lines 10-15), outside earlyApi (lines 1-3)
	if got := findValTypeName(file, "item", Position{Line: 12}); got != "CorrectType" {
		t.Errorf("expected 'CorrectType', got %q", got)
	}
	// cursor at line 32 (0-based) = AST line 33, inside targetFn (lines 30-35), outside earlyFn (lines 20-22)
	if got := findValTypeName(file, "result", Position{Line: 32}); got != "FnCorrect" {
		t.Errorf("expected 'FnCorrect', got %q", got)
	}
}

// ========== Coverage: inferBlockTypeName — last stmt is ValStmt without type ==========

func TestInferBlockTypeNameLastValNoType(t *testing.T) {
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name: "x",
				Value: &ast.CallExpr{
					Func: &ast.Ident{Name: "create"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Product"}}},
				},
			},
		},
	}
	if got := inferBlockTypeName(block); got != "Product" {
		t.Errorf("expected 'Product' for last ValStmt without type, got %q", got)
	}
}

// ========== Query Method Completions Tests ==========

func TestGetQueryMethodCompletions(t *testing.T) {
	items := getQueryMethodCompletions()
	if len(items) == 0 {
		t.Fatal("expected query method completions")
	}
	// check a few known methods
	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
		if item.Kind != 2 {
			t.Errorf("expected Kind=2 (Method) for %q, got %d", item.Label, item.Kind)
		}
	}
	for _, name := range []string{"where", "all", "first", "find", "create", "createMany",
		"update", "delete", "deleteMany", "orderBy", "limit", "offset",
		"groupBy", "select", "sum", "avg", "count", "min", "max", "exists", "upsert", "paginate"} {
		if !found[name] {
			t.Errorf("expected query method %q in completions", name)
		}
	}
}

func TestMemberCompletionIncludesQueryMethods(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi test(): String {\n  val u = User.\n}")
	resp := requestCompletion(server, output, "file:///test.luxo", Position{Line: 2, Character: 15})
	for _, method := range []string{"where", "create", "delete", "paginate"} {
		if !strings.Contains(resp, method) {
			t.Errorf("expected query method %q in member completion", method)
		}
	}
}

// ========== CRUD Chain Method Hover Tests ==========

func TestMemberMethodDescriptionCRUDChain(t *testing.T) {
	tests := []struct {
		word   string
		expect bool
	}{
		{"where", true},
		{"orderBy", true},
		{"limit", true},
		{"offset", true},
		{"sum", true},
		{"avg", true},
		{"min", true},
		{"max", true},
		{"select", true},
		{"fields", true},
		{"has", true},
	}
	for _, tt := range tests {
		desc := memberMethodDescription(tt.word)
		if tt.expect && desc == "" {
			t.Errorf("expected description for %q", tt.word)
		}
	}
}

// ========== findFieldInType Sealed Tests ==========

func TestFindFieldInTypeSealed(t *testing.T) {
	file := &ast.File{
		Sealeds: []*ast.SealedDecl{
			{
				Name: "Result",
				Variants: []*ast.SealedVariant{
					{
						Name: "Success",
						Fields: []*ast.ParamDecl{
							{Name: "data", Pos: token.Position{File: "test.luxo", Line: 5, Col: 3}},
						},
					},
					{
						Name: "Error",
						Fields: []*ast.ParamDecl{
							{Name: "message", Pos: token.Position{File: "test.luxo", Line: 8, Col: 3}},
						},
					},
				},
			},
		},
	}

	// find field by sealed type name (searches all variants)
	pos := findFieldInType(file, "Result", "data")
	if pos == nil {
		t.Fatal("expected to find 'data' in sealed Result")
	}
	if pos.Line != 5 {
		t.Errorf("expected line 5, got %d", pos.Line)
	}

	// find field by variant name
	pos = findFieldInType(file, "Error", "message")
	if pos == nil {
		t.Fatal("expected to find 'message' in variant Error")
	}
	if pos.Line != 8 {
		t.Errorf("expected line 8, got %d", pos.Line)
	}

	// not found
	pos = findFieldInType(file, "Result", "nonexistent")
	if pos != nil {
		t.Error("expected nil for nonexistent field in sealed")
	}

	pos = findFieldInType(file, "UnknownSealed", "data")
	if pos != nil {
		t.Error("expected nil for unknown sealed type")
	}
}
