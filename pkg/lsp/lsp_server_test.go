package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

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
	openDoc(server, output, "file:///test.luxo", "sealed Result {\n  Ok(value: String)\n  Err(message: String)\n}")
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 8})
	if !strings.Contains(resp, "sealed Result") {
		t.Error("expected 'sealed Result' in hover response")
	}
	if !strings.Contains(resp, "Ok") {
		t.Error("expected variant 'Ok' in hover")
	}
}

func TestHoverSymbol(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  find(User, where: id == id)\n}")
	// hover over "getUser" — symbol hover
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 1, Character: 5})
	if !strings.Contains(resp, "getUser") {
		t.Error("expected 'getUser' in symbol hover")
	}
}

func TestHoverSymbolWithDoc(t *testing.T) {
	server, output := newTestServer()
	// doc comment syntax: // comment before api
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\n// Get a user by ID\napi getUser(id: Int): User {\n  find(User, where: id == id)\n}")
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
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi test(): User {\n  find(User, id: 1)\n}")
	// hover over "find" on line 2, character 2
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 2, Character: 3})
	if !strings.Contains(resp, "find") {
		t.Error("expected 'find' in builtin function hover response")
	}
	if !strings.Contains(resp, "Query database") || !strings.Contains(resp, "查询数据库") {
		t.Error("expected bilingual description in builtin function hover")
	}
}

func TestHoverBuiltinFunctionEmit(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi test(): User {\n  emit(test, id: 1)\n  find(User, id: 1)\n}")
	// hover over "emit" on line 2
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 2, Character: 3})
	if !strings.Contains(resp, "emit") {
		t.Error("expected 'emit' in builtin function hover response")
	}
	if !strings.Contains(resp, "async event") || !strings.Contains(resp, "异步事件") {
		t.Error("expected bilingual description for emit in hover")
	}
}

func TestDefinitionSymbol(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  find(User, where: id == id)\n}")
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
	openDoc(server, output, "file:///test.luxo", "model User { name: String }\napi getUser(id: Int): User {\n  find(User, where: id == id)\n}")
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
// when word is not a type, not a symbol, and not a keyword.
func TestHandleHoverFinalNilReturn(t *testing.T) {
	server, output := newTestServer()
	openDoc(server, output, "file:///test.luxo", "model User { name: String }")
	// "name" is a field name, not a type/symbol/keyword in the scope
	resp := requestHover(server, output, "file:///test.luxo", Position{Line: 0, Character: 14})
	// Should return null result (not a type, not a symbol, not a keyword)
	if strings.Contains(resp, "markdown") {
		t.Error("expected null hover result for non-type/non-symbol/non-keyword word")
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
