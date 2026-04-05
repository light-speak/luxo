package lsp

import (
	"net/url"
	"strings"
	"sync"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// Document represents an open .luxo file with its analysis results.
type Document struct {
	URI     string
	Version int
	Content string
	File    *ast.File
	Result  *semantic.Result
	Tokens  []token.Token

	lexErrors   []lexer.Error
	parseErrors []parser.Error
}

// DocumentStore manages all open documents.
type DocumentStore struct {
	mu   sync.RWMutex
	docs map[string]*Document
}

// NewDocumentStore creates a new document store.
func NewDocumentStore() *DocumentStore {
	return &DocumentStore{
		docs: make(map[string]*Document),
	}
}

// Open adds or updates a document and triggers analysis.
func (s *DocumentStore) Open(uri string, version int, content string) *Document {
	doc := &Document{
		URI:     uri,
		Version: version,
		Content: content,
	}

	s.mu.Lock()
	s.docs[uri] = doc
	s.mu.Unlock()

	s.analyzeAll()
	return s.Get(uri)
}

// Update updates a document's content and re-analyzes all documents.
func (s *DocumentStore) Update(uri string, version int, content string) *Document {
	s.mu.Lock()
	doc, ok := s.docs[uri]
	if !ok {
		doc = &Document{URI: uri}
		s.docs[uri] = doc
	}
	doc.Version = version
	doc.Content = content
	s.mu.Unlock()

	s.analyzeAll()
	return s.Get(uri)
}

// Get returns a document by URI.
func (s *DocumentStore) Get(uri string) *Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docs[uri]
}

// Close removes a document and re-analyzes remaining documents.
func (s *DocumentStore) Close(uri string) {
	s.mu.Lock()
	delete(s.docs, uri)
	s.mu.Unlock()
}

// analyzeAll runs analysis on all open documents together for cross-file resolution.
func (s *DocumentStore) analyzeAll() {
	s.mu.RLock()
	docs := make([]*Document, 0, len(s.docs))
	for _, doc := range s.docs {
		docs = append(docs, doc)
	}
	s.mu.RUnlock()

	// Phase 1: lex and parse each file independently
	var files []*ast.File
	for _, doc := range docs {
		filename := URIToPath(doc.URI)

		l := lexer.New(doc.Content, filename)
		tokens, lexErrs := l.Tokenize()
		doc.Tokens = tokens
		doc.lexErrors = lexErrs

		p := parser.New(tokens)
		file, parseErrs := p.Parse(filename)
		doc.File = file
		doc.parseErrors = parseErrs

		if file != nil {
			files = append(files, file)
		}
	}

	// Phase 2: semantic analysis across all files together
	a := semantic.New()
	result := a.Analyze(files)

	// distribute results to each document
	for _, doc := range docs {
		doc.Result = result
	}
}

// Diagnostics returns all LSP diagnostics (lexer + parser + semantic errors).
func (d *Document) Diagnostics() []Diagnostic {
	var diags []Diagnostic

	// lexer errors
	for _, err := range d.lexErrors {
		diags = append(diags, Diagnostic{
			Range:    tokenPosToRange(err.Pos, 1),
			Severity: 1,
			Source:   "luxo/lexer",
			Message:  err.Message,
		})
	}

	// parser errors
	for _, err := range d.parseErrors {
		diags = append(diags, Diagnostic{
			Range:    tokenPosToRange(err.Pos, 1),
			Severity: 1,
			Source:   "luxo/parser",
			Message:  err.Message,
		})
	}

	// semantic errors — only for this file
	if d.Result != nil {
		filename := URIToPath(d.URI)
		for _, err := range d.Result.Errors {
			if err.Pos.File != filename && err.Pos.File != "" {
				continue
			}
			msg := err.Message
			if err.Suggestion != "" {
				msg += " (" + err.Suggestion + ")"
			}
			diags = append(diags, Diagnostic{
				Range:    tokenPosToRange(err.Pos, 1),
				Severity: 1,
				Source:   "luxo/semantic",
				Message:  msg,
			})
		}
		for _, warn := range d.Result.Warnings {
			if warn.Pos.File != filename && warn.Pos.File != "" {
				continue
			}
			diags = append(diags, Diagnostic{
				Range:    tokenPosToRange(warn.Pos, 1),
				Severity: 2,
				Source:   "luxo/semantic",
				Message:  warn.Message,
			})
		}
	}

	return diags
}

// TokenAt returns the token at the given LSP position.
func (d *Document) TokenAt(pos Position) *token.Token {
	for i := range d.Tokens {
		t := &d.Tokens[i]
		tLine := t.Pos.Line - 1 // LSP 0-based
		tCol := t.Pos.Col - 1
		if tLine == pos.Line && tCol <= pos.Character && pos.Character < tCol+len(t.Val) {
			return t
		}
	}
	return nil
}

// WordAt returns the word at the given LSP position.
func (d *Document) WordAt(pos Position) string {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	if pos.Character >= len(line) {
		return ""
	}

	start := pos.Character
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	end := pos.Character
	for end < len(line) && isIdentChar(line[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return line[start:end]
}

// CharAt returns the character at the given LSP position.
func (d *Document) CharAt(pos Position) byte {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return 0
	}
	line := lines[pos.Line]
	if pos.Character >= len(line) {
		return 0
	}
	return line[pos.Character]
}

// PrevChar returns the character before the given position.
func (d *Document) PrevChar(pos Position) byte {
	if pos.Character > 0 {
		lines := strings.Split(d.Content, "\n")
		if pos.Line < len(lines) && pos.Character-1 < len(lines[pos.Line]) {
			return lines[pos.Line][pos.Character-1]
		}
	}
	return 0
}

// IsNamedArgLabel checks if the word at pos is a named argument label (followed by ':').
func (d *Document) IsNamedArgLabel(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}
	line := lines[pos.Line]
	// find end of word
	end := pos.Character
	for end < len(line) && isIdentChar(line[end]) {
		end++
	}
	// skip whitespace after word
	for end < len(line) && line[end] == ' ' {
		end++
	}
	return end < len(line) && line[end] == ':'
}

// FindEnclosingCall returns the function name and model name of a CRUD call enclosing pos.
// Supports multi-line calls by scanning backward across lines.
func (d *Document) FindEnclosingCall(pos Position) (funcName string, modelName string) {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return "", ""
	}

	// build a flat string from start of file to cursor, tracking line positions
	parenDepth := 0
	lineIdx := pos.Line
	col := pos.Character
	if col >= len(lines[lineIdx]) {
		col = len(lines[lineIdx]) - 1
	}

	for lineIdx >= 0 {
		line := lines[lineIdx]
		start := 0
		end := len(line) - 1
		if lineIdx == pos.Line {
			end = col
		}
		for i := end; i >= start; i-- {
			ch := line[i]
			if ch == ')' {
				parenDepth++
			} else if ch == '(' {
				if parenDepth == 0 {
					return d.extractCallInfo(lines, lineIdx, i)
				}
				parenDepth--
			}
		}
		lineIdx--
	}
	return "", ""
}

// extractCallInfo extracts function name before '(' and first arg after '('.
func (d *Document) extractCallInfo(lines []string, lineIdx, parenCol int) (string, string) {
	line := lines[lineIdx]
	// function name before '('
	end := parenCol
	start := end - 1
	for start >= 0 && isIdentChar(line[start]) {
		start--
	}
	start++
	var funcName string
	if start < end {
		funcName = line[start:end]
	}
	// first arg after '(' — may be on same line or next
	argStr := line[parenCol+1:]
	argStr = strings.TrimLeft(argStr, " \t")
	if argStr == "" && lineIdx+1 < len(lines) {
		argStr = strings.TrimLeft(lines[lineIdx+1], " \t")
	}
	var modelName string
	for i := 0; i < len(argStr) && isIdentChar(argStr[i]); i++ {
		modelName = argStr[:i+1]
	}
	return funcName, modelName
}

// IsAfterAt checks if the word at pos is preceded by '@' (directive context).
func (d *Document) IsAfterAt(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}
	line := lines[pos.Line]
	start := pos.Character
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	return start > 0 && line[start-1] == '@'
}

// IsAfterDot checks if the word at pos is preceded by a dot (member access context).
func (d *Document) IsAfterDot(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}
	line := lines[pos.Line]
	start := pos.Character
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	return start > 0 && line[start-1] == '.'
}

// IsAfterItDot checks if the word at pos is preceded by "it." (model field access).
func (d *Document) IsAfterItDot(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}
	line := lines[pos.Line]
	// find start of current word
	start := pos.Character
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	// check if preceded by "it."
	if start >= 3 && line[start-3:start] == "it." {
		return true
	}
	return false
}

// ObjectBeforeDot returns the identifier name before the dot at the given position.
func (d *Document) ObjectBeforeDot(pos Position) string {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	start := pos.Character
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	if start < 2 || line[start-1] != '.' {
		return ""
	}
	objEnd := start - 1
	objStart := objEnd - 1
	for objStart >= 0 && isIdentChar(line[objStart]) {
		objStart--
	}
	objStart++
	if objStart >= objEnd {
		return ""
	}
	return line[objStart:objEnd]
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func tokenPosToRange(p token.Position, length int) Range {
	line := p.Line - 1
	col := p.Col - 1
	if line < 0 {
		line = 0
	}
	if col < 0 {
		col = 0
	}
	if length < 1 {
		length = 1
	}
	return Range{
		Start: Position{Line: line, Character: col},
		End:   Position{Line: line, Character: col + length},
	}
}

// URIToPath converts a file:// URI to a filesystem path.
func URIToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		parsed, err := url.Parse(uri)
		if err == nil {
			return parsed.Path
		}
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

// PathToURI converts a filesystem path to a file:// URI.
func PathToURI(path string) string {
	if strings.HasPrefix(path, "/") {
		return "file://" + path
	}
	return path
}
