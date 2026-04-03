package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Transport Tests ==========

func TestTransportRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("SendResponse error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Content-Length:") {
		t.Error("missing Content-Length header")
	}
	if !strings.Contains(output, `"key":"value"`) {
		t.Error("missing response body")
	}
}

func TestTransportReadMessage(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)

	transport := NewTransport(strings.NewReader(input), io.Discard)
	req, err := transport.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if req.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", req.Method)
	}
}

func TestTransportReadMessageInvalidHeader(t *testing.T) {
	input := "Invalid-Header: 123\r\n\r\n{}"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for missing Content-Length")
	}
}

func TestTransportSendNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	err := transport.SendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         "file:///test.luxo",
		"diagnostics": []any{},
	})
	if err != nil {
		t.Fatalf("SendNotification error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "publishDiagnostics") {
		t.Error("notification method not found in output")
	}
}

func TestTransportSendError(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	id := json.RawMessage(`1`)
	err := transport.SendError(&id, -32601, "method not found")
	if err != nil {
		t.Fatalf("SendError error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "method not found") {
		t.Error("error message not found in output")
	}
}

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

// ========== Server Integration Tests ==========

func newTestServer() (*Server, *bytes.Buffer) {
	var output bytes.Buffer
	logger := log.New(io.Discard, "", 0)
	server := NewServer(strings.NewReader(""), &output, logger)
	return server, &output
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

func TestTransportReadMessageEOFMidBody(t *testing.T) {
	// Content-Length says 100 but only 5 bytes available
	input := "Content-Length: 100\r\n\r\nhello"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for EOF mid-body")
	}
}

func TestTransportSendWriteError(t *testing.T) {
	w := &errWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from writer")
	}
}

// errWriter always returns an error on Write
type errWriter struct{}

func (e *errWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write error")
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

func TestFormatTypeHoverDefault(t *testing.T) {
	// Test the default branch (not model, enum, or sealed)
	typ := &semantic.ResolvedType{Kind: semantic.TypeCustom, Name: "MyType"}
	result := formatTypeHover("MyType", typ)
	if !strings.Contains(result, "type MyType") {
		t.Errorf("expected 'type MyType' for custom type hover, got: %s", result)
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

func TestHandleExit(t *testing.T) {
	server, _ := newTestServer()
	err := server.handleMessage(&Request{Method: "exit"})
	if err != nil {
		t.Fatalf("exit handler error: %v", err)
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

func TestTransportReadMessageInvalidContentLength(t *testing.T) {
	input := "Content-Length: notanumber\r\n\r\n{}"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for non-numeric Content-Length")
	}
	if !strings.Contains(err.Error(), "invalid Content-Length") {
		t.Errorf("expected 'invalid Content-Length' in error, got: %v", err)
	}
}

func TestTransportSendMarshalError(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	// Channels cannot be marshaled to JSON
	err := transport.send(make(chan int))
	if err == nil {
		t.Error("expected error when marshaling a channel")
	}
}

// ========== Coverage Push Tests (Phase 3) ==========

// errHeaderWriter fails on the first Write (header write), succeeds on nothing.
type errHeaderWriter struct {
	calls int
}

func (e *errHeaderWriter) Write(p []byte) (int, error) {
	e.calls++
	if e.calls == 1 {
		return 0, fmt.Errorf("header write error")
	}
	return len(p), nil
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

// TestReadMessageZeroContentLength covers the Content-Length: 0 path.
func TestReadMessageZeroContentLength(t *testing.T) {
	input := "Content-Length: 0\r\n\r\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for Content-Length 0")
	}
	if !strings.Contains(err.Error(), "missing Content-Length") {
		t.Errorf("expected 'missing Content-Length' error, got: %v", err)
	}
}

// TestReadMessageHeaderReadError covers the header read error path.
func TestReadMessageHeaderReadError(t *testing.T) {
	// A reader that returns an error immediately
	r := &errReader{err: fmt.Errorf("connection reset")}
	transport := NewTransport(r, io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error from ReadMessage")
	}
	if !strings.Contains(err.Error(), "reading header") {
		t.Errorf("expected 'reading header' in error, got: %v", err)
	}
}

type errReader struct {
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, e.err
}

// TestReadMessageInvalidJSON covers JSON unmarshal error after valid Content-Length.
func TestReadMessageInvalidJSON(t *testing.T) {
	body := "not valid json!"
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for invalid JSON body")
	}
	if !strings.Contains(err.Error(), "parsing JSON-RPC") {
		t.Errorf("expected 'parsing JSON-RPC' in error, got: %v", err)
	}
}

// TestTransportSendHeaderWriteError covers the header write error path in send().
func TestTransportSendHeaderWriteError(t *testing.T) {
	w := &errHeaderWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from header write")
	}
	if !strings.Contains(err.Error(), "header write error") {
		t.Errorf("expected 'header write error', got: %v", err)
	}
}

// TestTransportSendBodyWriteError covers the body write error path in send().
func TestTransportSendBodyWriteError(t *testing.T) {
	// Writer that succeeds on header (first call via io.WriteString) but fails on body write
	w := &bodyErrWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from body write")
	}
	if !strings.Contains(err.Error(), "body write error") {
		t.Errorf("expected 'body write error', got: %v", err)
	}
}

type bodyErrWriter struct {
	calls int
}

func (e *bodyErrWriter) Write(p []byte) (int, error) {
	e.calls++
	if e.calls == 1 {
		// Header write succeeds
		return len(p), nil
	}
	// Body write fails
	return 0, fmt.Errorf("body write error")
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
