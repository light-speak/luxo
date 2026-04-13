package lsp

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	mu            sync.RWMutex
	docs          map[string]*Document
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
	siblingCache  []*Document // cached sibling files (refreshed on open/close)
	siblingDirty  bool        // true when sibling cache needs refresh
	OnAnalyzed    func()      // called after analyzeAll completes (for pushing diagnostics)
}

const analyzeDebounce = 100 * time.Millisecond

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
	s.siblingDirty = true // new file opened, refresh sibling discovery
	s.mu.Unlock()

	s.analyzeAll()
	return s.Get(uri)
}

// Update updates a document's content and re-analyzes with debounce.
// Rapid keystrokes only trigger one analysis after the last keystroke.
func (s *DocumentStore) Update(uri string, version int, content string) *Document {
	s.mu.Lock()
	doc, ok := s.docs[uri]
	if !ok {
		doc = &Document{URI: uri}
		s.docs[uri] = doc
	}
	doc.Version = version
	doc.Content = content
	// Clear stale results immediately so diagnostics don't flicker
	doc.lexErrors = nil
	doc.parseErrors = nil
	doc.File = nil
	doc.Tokens = nil
	s.mu.Unlock()

	s.scheduleAnalysis()
	return s.Get(uri)
}

// scheduleAnalysis debounces analysis — resets the timer on each call.
func (s *DocumentStore) scheduleAnalysis() {
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()
	if s.debounceTimer != nil {
		s.debounceTimer.Stop()
	}
	s.debounceTimer = time.AfterFunc(analyzeDebounce, func() {
		s.analyzeAll()
	})
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
	s.siblingDirty = true
	s.mu.Unlock()
}

// analyzeAll runs analysis on all open documents together for cross-file resolution.
// Also discovers sibling .luxo files in the same origin/ directory.
func (s *DocumentStore) analyzeAll() {
	s.mu.RLock()
	docs := make([]*Document, 0, len(s.docs))
	for _, doc := range s.docs {
		docs = append(docs, doc)
	}
	s.mu.RUnlock()

	// Discover sibling .luxo files (cached, only refreshed on open/close)
	s.mu.Lock()
	dirty := s.siblingDirty
	if dirty || s.siblingCache == nil {
		s.siblingCache = s.discoverSiblingFiles(docs)
		s.siblingDirty = false
	}
	siblings := s.siblingCache // copy slice header under lock
	s.mu.Unlock()
	allDocs := append(docs, siblings...)

	// Phase 1: lex and parse each file independently — collect results locally
	type docResult struct {
		tokens    []token.Token
		lexErrs   []lexer.Error
		file      *ast.File
		parseErrs []parser.Error
	}
	results := make([]docResult, len(allDocs))
	var files []*ast.File
	for i, doc := range allDocs {
		filename := URIToPath(doc.URI)

		l := lexer.New(doc.Content, filename)
		tokens, lexErrs := l.Tokenize()
		results[i].tokens = tokens
		results[i].lexErrs = lexErrs

		p := parser.New(tokens)
		file, parseErrs := p.Parse(filename)
		results[i].file = file
		results[i].parseErrs = parseErrs

		if file != nil {
			files = append(files, file)
		}
	}

	// Phase 2: semantic analysis across all files together with module isolation
	a := semantic.New()
	semResult := a.AnalyzeWithModules(files)

	// Write all results under lock to avoid races with server goroutine
	s.mu.Lock()
	for i, doc := range allDocs {
		doc.Tokens = results[i].tokens
		doc.lexErrors = results[i].lexErrs
		doc.File = results[i].file
		doc.parseErrors = results[i].parseErrs
		doc.Result = semResult
	}
	s.mu.Unlock()

	// notify server to push diagnostics
	if s.OnAnalyzed != nil {
		s.OnAnalyzed()
	}
}

// discoverSiblingFiles finds .luxo files in the same origin/ directory
// that aren't already open. Reads them from disk.
func (s *DocumentStore) discoverSiblingFiles(openDocs []*Document) []*Document {
	// Find origin/ directories from open files
	originDirs := make(map[string]bool)
	openPaths := make(map[string]bool)
	for _, doc := range openDocs {
		p := URIToPath(doc.URI)
		openPaths[p] = true
		dir := filepath.Dir(p)
		// Walk up to find origin/ parent
		for dir != "/" && dir != "." {
			base := filepath.Base(dir)
			if base == "origin" {
				originDirs[dir] = true
				break
			}
			// Also check if current dir IS inside origin/
			parent := filepath.Dir(dir)
			if filepath.Base(parent) == "origin" {
				originDirs[parent] = true
				break
			}
			dir = parent
		}
		// If file is directly in origin/
		if filepath.Base(filepath.Dir(p)) == "origin" {
			originDirs[filepath.Dir(p)] = true
		}
	}

	var discovered []*Document
	for originDir := range originDirs {
		// Walk origin/ recursively for .luxo files
		filepath.WalkDir(originDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".luxo") {
				return nil
			}
			if openPaths[path] {
				return nil // already open
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			discovered = append(discovered, &Document{
				URI:     PathToURI(path),
				Content: string(content),
			})
			return nil
		})
	}
	return discovered
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
			warnLen := warn.NameLen
			if warnLen < 1 {
				warnLen = 1
			}
			diags = append(diags, Diagnostic{
				Range:    tokenPosToRange(warn.Pos, warnLen),
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

// extractCallInfo extracts function name before '(' and model name.
// Supports both function-style find(User, ...) and chain-style User.find(...).
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

	// chain-style: check for Model.method( pattern — dot before funcName
	if start > 0 && line[start-1] == '.' {
		objEnd := start - 1
		objStart := objEnd - 1
		for objStart >= 0 && isIdentChar(line[objStart]) {
			objStart--
		}
		objStart++
		if objStart < objEnd {
			modelName := line[objStart:objEnd]
			return funcName, modelName
		}
	}

	// function-style: first arg after '(' — may be on same line or next
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

// FindEnclosingObjectType returns the type name of an enclosing object construction.
// For example, in `AuthResult { user: expr }`, if the cursor is on `user`,
// this returns "AuthResult". It scans backward to find the matching `{`,
// then extracts the uppercase identifier before it.
func (d *Document) FindEnclosingObjectType(pos Position) string {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return ""
	}

	braceDepth := 0
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
			if ch == '}' {
				braceDepth++
			} else if ch == '{' {
				if braceDepth == 0 {
					return d.extractTypeBeforeBrace(lines, lineIdx, i)
				}
				braceDepth--
			}
		}
		lineIdx--
	}
	return ""
}

// extractTypeBeforeBrace extracts the uppercase identifier immediately before '{' at the given position.
// Returns empty string if this is a declaration context (model/type/interface/etc.).
func (d *Document) extractTypeBeforeBrace(lines []string, lineIdx, braceCol int) string {
	line := lines[lineIdx]
	name := extractIdentBeforePos(line, braceCol)
	if len(name) == 0 || name[0] < 'A' || name[0] > 'Z' {
		return ""
	}
	if precedingWordIsDeclaration(line, braceCol, name) {
		return ""
	}
	return name
}

// extractIdentBeforePos finds the identifier ending just before pos (skipping spaces).
func extractIdentBeforePos(line string, pos int) string {
	end := pos - 1
	for end >= 0 && line[end] == ' ' {
		end--
	}
	if end < 0 {
		return ""
	}
	idEnd := end + 1
	idStart := end
	for idStart >= 0 && isIdentChar(line[idStart]) {
		idStart--
	}
	idStart++
	if idStart >= idEnd {
		return ""
	}
	return line[idStart:idEnd]
}

// precedingWordIsDeclaration checks if the word before the type name is a declaration keyword.
func precedingWordIsDeclaration(line string, braceCol int, typeName string) bool {
	// find where the type name starts
	nameStart := braceCol - 1
	for nameStart >= 0 && line[nameStart] == ' ' {
		nameStart--
	}
	nameStart = nameStart - len(typeName) + 1
	kwEnd := nameStart - 1
	for kwEnd >= 0 && line[kwEnd] == ' ' {
		kwEnd--
	}
	if kwEnd < 0 {
		return false
	}
	kwStart := kwEnd
	for kwStart >= 0 && isIdentChar(line[kwStart]) {
		kwStart--
	}
	kwStart++
	return isDeclarationKeyword(line[kwStart : kwEnd+1])
}

// isDeclarationKeyword returns true if the word is a Luxo declaration keyword
// that introduces a type definition (not an object construction).
func isDeclarationKeyword(word string) bool {
	switch word {
	case "model", "type", "interface", "enum", "sealed", "error", "event", "extend", "middleware":
		return true
	}
	return false
}

// IsInsideMyLoadCall checks if the cursor position is inside a my.load(...) call.
// Returns true if the enclosing call is "load" and the identifier before "load(" is "my.".
func (d *Document) IsInsideMyLoadCall(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}

	// find the enclosing '(' by scanning backward
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
					// found the opening paren — check if preceded by "my.load"
					return isMyLoadBefore(line, i)
				}
				parenDepth--
			}
		}
		lineIdx--
	}
	return false
}

// isMyLoadBefore checks if the text before parenCol is "my.load".
func isMyLoadBefore(line string, parenCol int) bool {
	// extract identifier before '('
	end := parenCol
	start := end - 1
	for start >= 0 && isIdentChar(line[start]) {
		start--
	}
	start++
	if start >= end {
		return false
	}
	funcName := line[start:end]
	if funcName != "load" {
		return false
	}
	// check for "my." before "load"
	dotPos := start - 1
	if dotPos < 0 || line[dotPos] != '.' {
		return false
	}
	objEnd := dotPos
	objStart := objEnd - 1
	for objStart >= 0 && isIdentChar(line[objStart]) {
		objStart--
	}
	objStart++
	if objStart >= objEnd {
		return false
	}
	return line[objStart:objEnd] == "my"
}

// IsInsideScopeDirective checks if the cursor is inside @scope(...) arguments.
// Returns true when pos is between the '(' and ')' of @scope(...).
func (d *Document) IsInsideScopeDirective(pos Position) bool {
	lines := strings.Split(d.Content, "\n")
	if pos.Line >= len(lines) {
		return false
	}

	// scan backward from cursor to find enclosing '('
	// LSP cursor position is *before* the character at that index,
	// so we scan from col-1 to avoid including the character at cursor.
	parenDepth := 0
	lineIdx := pos.Line
	col := pos.Character - 1
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
					return isScopeDirectiveBefore(line, i)
				}
				parenDepth--
			}
		}
		lineIdx--
	}
	return false
}

// isScopeDirectiveBefore checks if "@scope" appears immediately before the '(' at parenCol.
func isScopeDirectiveBefore(line string, parenCol int) bool {
	// extract identifier before '('
	end := parenCol
	start := end - 1
	for start >= 0 && isIdentChar(line[start]) {
		start--
	}
	start++
	if start >= end {
		return false
	}
	name := line[start:end]
	if name != "scope" {
		return false
	}
	// check for '@' before "scope"
	atPos := start - 1
	return atPos >= 0 && line[atPos] == '@'
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
