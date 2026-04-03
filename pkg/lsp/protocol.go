package lsp

import "encoding/json"

// JSON-RPC 2.0 message types for LSP protocol.
// Hand-written to avoid external dependencies.

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *RespError       `json:"error,omitempty"`
}

// RespError represents a JSON-RPC error.
type RespError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Notification represents a JSON-RPC notification (no ID).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ========== LSP Initialize ==========

type InitializeParams struct {
	RootURI string `json:"rootUri"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   ServerInfo         `json:"serverInfo"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	TextDocumentSync   int            `json:"textDocumentSync"` // 1=Full, 2=Incremental
	CompletionProvider *CompletionOpt `json:"completionProvider,omitempty"`
	HoverProvider      bool           `json:"hoverProvider"`
	DefinitionProvider bool           `json:"definitionProvider"`
}

type CompletionOpt struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// ========== TextDocument ==========

type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidChangeParams struct {
	TextDocument   VersionedTextDocID   `json:"textDocument"`
	ContentChanges []TextDocumentChange `json:"contentChanges"`
}

type VersionedTextDocID struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentChange struct {
	Text string `json:"text"` // full sync mode: entire content
}

type DidCloseParams struct {
	TextDocument TextDocumentID `json:"textDocument"`
}

type DidSaveParams struct {
	TextDocument TextDocumentID `json:"textDocument"`
}

// ========== Diagnostics ==========

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1=Error, 2=Warning, 3=Info, 4=Hint
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// ========== Position & Range ==========

type Position struct {
	Line      int `json:"line"`      // 0-based
	Character int `json:"character"` // 0-based
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// ========== Completion ==========

type CompletionParams struct {
	TextDocument TextDocumentID `json:"textDocument"`
	Position     Position       `json:"position"`
}

type TextDocumentID struct {
	URI string `json:"uri"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind"` // 1=Text,2=Method,3=Function,6=Variable,7=Class,8=Interface,13=Enum,14=Keyword,15=Snippet
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
}

// ========== Hover ==========

type HoverParams struct {
	TextDocument TextDocumentID `json:"textDocument"`
	Position     Position       `json:"position"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type MarkupContent struct {
	Kind  string `json:"kind"` // "plaintext" or "markdown"
	Value string `json:"value"`
}

// ========== Definition ==========

type DefinitionParams struct {
	TextDocument TextDocumentID `json:"textDocument"`
	Position     Position       `json:"position"`
}
