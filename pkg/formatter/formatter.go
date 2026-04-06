package formatter

import (
	"strings"

	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/token"
)

// Format formats a .luxo source string and returns the formatted result.
func Format(source, filename string) (string, error) {
	l := lexer.New(source, filename)
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return "", &FormatError{Messages: msgs}
	}
	return FormatTokens(tokens), nil
}

// FormatTokens formats using a pre-existing token stream, avoiding redundant lexing.
func FormatTokens(tokens []token.Token) string {
	f := &formatter{tokens: tokens}
	f.formatFile()
	return f.out.String()
}

// FormatError reports lexer errors encountered during formatting.
type FormatError struct {
	Messages []string
}

func (e *FormatError) Error() string {
	return strings.Join(e.Messages, "; ")
}

// formatter holds all state for a single formatting operation.
type formatter struct {
	tokens    []token.Token
	pos       int
	indent    int
	out       strings.Builder
	lastLine  int // last emitted line (for blank line detection)
	needSpace bool
}

// field describes a single field line for alignment.
type field struct {
	tokens     []token.Token // all tokens in this field line
	nameLen    int           // length of field name
	typeTokens []token.Token // type expression tokens (after colon, before @ or EOL)
	typeLen    int           // rendered length of type expression
	annTokens  []token.Token // annotation tokens
	defTokens  []token.Token // default value tokens (= xxx)
	defLen     int           // rendered length of default value
	comments   []token.Token // standalone comments before this field
	eolComment *token.Token  // end-of-line comment
}

// --- Token stream helpers ---

func (f *formatter) peek() token.Token {
	if f.pos < len(f.tokens) {
		return f.tokens[f.pos]
	}
	// Defensive fallback: pos should never exceed len(tokens) because
	// advance() guards against it, but return EOF for safety.
	return token.Token{Type: token.EOF}
}

func (f *formatter) peekAt(offset int) token.Token {
	idx := f.pos + offset
	if idx < len(f.tokens) {
		return f.tokens[idx]
	}
	// Defensive fallback: callers typically check atEnd() first,
	// so this branch is only reachable with large offsets past the token stream.
	return token.Token{Type: token.EOF}
}

func (f *formatter) advance() token.Token {
	tok := f.peek()
	if f.pos < len(f.tokens) {
		f.pos++
	}
	return tok
}

func (f *formatter) atEnd() bool {
	return f.pos >= len(f.tokens) || f.tokens[f.pos].Type == token.EOF
}

func (f *formatter) writeStr(s string) {
	f.out.WriteString(s)
	f.needSpace = false
}

func (f *formatter) writeIndent() {
	for i := 0; i < f.indent; i++ {
		f.out.WriteString("  ")
	}
}

func (f *formatter) newline() {
	f.out.WriteByte('\n')
	f.needSpace = false
}

func (f *formatter) space() {
	f.out.WriteByte(' ')
}

// emitBlankLineIfNeeded emits a blank line if there was a line gap in the original source.
func (f *formatter) emitBlankLineIfNeeded(tok token.Token) {
	if f.lastLine > 0 && tok.Pos.Line > f.lastLine+1 {
		f.newline()
	}
}

// tokenText returns the source representation of a token.
// Line comments (// ...) have their content stored without the prefix.
// Block comments (/* ... */) store the content between delimiters; we detect
// them by checking if Col > 1 offset from "//" length or by checking for
// leading whitespace/newline patterns. The most reliable signal: the lexer's
// readLineComment skips leading whitespace, so a line comment Val never starts
// with a space. Block comments preserve everything between /* and */, so they
// often start with a space or newline.
// However, this heuristic isn't perfect. We use a more reliable approach:
// block comments were originally at a column that accounts for "/*" (2 chars),
// while line comments account for "// " (3 chars). But since the lexer strips
// leading space for line comments, we detect block comments by checking if the
// Val, when prefixed with "// ", would produce different content on re-lex.
// Simplest reliable approach: store the comment kind in the Val itself.
// Since we can't modify the lexer, we use the heuristic that block comment
// Val starts with content immediately (possibly space/newline) and the Pos.Col
// indicates the start of "/*". A line comment's Val is after "// " whitespace skip.
//
// Practical solution: check if the comment value, when reconstructed as "// X",
// would re-lex to the same value. If not, it's a block comment.
// Even simpler: the lexer's line comment reader calls skipWhitespace() after "//",
// so a line comment Val never starts with ' ' or '\t'. Block comments often do.
// Edge case: empty line comment "// " → Val = "".
// We'll use: if Val contains '\n' OR Val starts with ' ' or '\t', it's a block comment.
func tokenText(tok token.Token) string {
	switch tok.Type {
	case token.String:
		return "\"" + escapeStringVal(tok.Val) + "\""
	case token.StringStart:
		return "\"" + escapeStringVal(tok.Val) + "${"
	case token.StringMid:
		return "}" + escapeStringVal(tok.Val) + "${"
	case token.StringEnd:
		return "}" + escapeStringVal(tok.Val) + "\""
	case token.Comment:
		val := tok.Val
		// Multiline → must use /* */ (line comments can't span lines)
		if strings.ContainsRune(val, '\n') {
			return "/*" + val + "*/"
		}
		// Empty → use /* */ (// followed by newline would eat next line)
		if val == "" {
			return "/**/"
		}
		// Strip \r for line comments (lexer treats \r as whitespace)
		val = strings.ReplaceAll(val, "\r", "")
		if val == "" {
			return "/**/"
		}
		// Leading whitespace → use /* */ (lexer strips it after //)
		if val[0] == ' ' || val[0] == '\t' {
			return "/*" + val + "*/"
		}
		return "// " + val
	case token.DocComment:
		if tok.Val == "" {
			return "/**/" // empty doc comment → block to prevent newline eating
		}
		return "/// " + tok.Val
	default:
		return tok.Val
	}
}

// escapeStringVal re-escapes raw control characters that the lexer may have stored literally.
func escapeStringVal(val string) string {
	if !strings.ContainsAny(val, "\n\r\t") {
		return val
	}
	var sb strings.Builder
	for _, r := range val {
		switch r {
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// --- Main formatting ---

func (f *formatter) formatFile() {
	for !f.atEnd() {
		tok := f.peek()

		// Emit blank line if there was a gap in source
		f.emitBlankLineIfNeeded(tok)

		switch tok.Type {
		case token.Comment, token.DocComment:
			f.writeIndent()
			text := tokenText(tok)
			f.writeStr(text)
			// For multiline block comments, lastLine should be the last line of the comment
			f.lastLine = tok.Pos.Line + strings.Count(text, "\n")
			f.advance()
			f.newline()

		case token.Use:
			f.formatUse()
		case token.Val, token.Var:
			f.formatValVar()
		case token.Model, token.KwType, token.Interface, token.Extend, token.Error:
			f.formatFieldBlock()
		case token.Enum:
			f.formatEnum()
		case token.Sealed:
			f.formatSealed()
		case token.Api, token.Fn:
			f.formatApiOrFn()
		case token.Override:
			f.formatOverride()
		case token.Event:
			f.formatEvent()
		case token.On:
			f.formatOn()
		case token.Middleware:
			f.formatMiddleware()
		default:
			// Unknown top-level token — emit as-is
			f.writeIndent()
			f.writeStr(tokenText(tok))
			f.lastLine = tok.Pos.Line
			f.advance()
			f.newline()
		}
	}
}

// --- use ---

func (f *formatter) formatUse() {
	f.writeIndent()
	startLine := f.peek().Pos.Line
	var prevType token.Type
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != startLine {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
	f.emitEolComment(startLine)
	f.lastLine = startLine
	f.newline()
	f.needSpace = false
}

// --- val / var ---

func (f *formatter) formatValVar() {
	f.writeIndent()
	startLine := f.peek().Pos.Line
	var prevType token.Type
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != startLine {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
	f.emitEolComment(startLine)
	f.lastLine = startLine
	f.newline()
	f.needSpace = false
}

// --- Field blocks (model/type/interface/extend/error) ---

func (f *formatter) formatFieldBlock() {
	// Emit the declaration header: keyword Name [: Parent] [@annotations] {
	f.writeIndent()
	headerLine := f.peek().Pos.Line
	f.emitHeaderUntilBrace()

	if f.peek().Type != token.LBrace {
		f.lastLine = headerLine
		f.newline()
		return
	}

	// Emit " {"
	f.space()
	f.writeStr("{")
	f.advance() // consume LBrace
	f.newline()
	f.lastLine = headerLine

	// Collect fields for alignment
	fields := f.collectFields()
	maxName, maxType, _ := fieldMaxWidths(fields)

	// Emit aligned fields
	f.indent++
	prevFieldLine := headerLine
	for _, fld := range fields {
		prevFieldLine = f.emitFieldLine(fld, prevFieldLine, maxName, maxType)
	}
	f.indent--

	// Closing brace
	f.emitClosingBrace()
}

// emitHeaderUntilBrace emits header tokens until { is reached.
func (f *formatter) emitHeaderUntilBrace() {
	var prevType token.Type
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.LBrace {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		if tok.Type == token.RBrace {
			break // don't consume closing braces in headers
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
}

// fieldMaxWidths calculates max name, type, and default widths for alignment.
func fieldMaxWidths(fields []field) (maxName, maxType, maxDef int) {
	for _, fld := range fields {
		if fld.nameLen > maxName {
			maxName = fld.nameLen
		}
		if fld.typeLen > maxType {
			maxType = fld.typeLen
		}
		if fld.defLen > maxDef {
			maxDef = fld.defLen
		}
	}
	return
}

// emitFieldLine emits a single aligned field line (with comments), returning the updated prevFieldLine.
func (f *formatter) emitFieldLine(fld field, prevFieldLine, maxName, maxType int) int {
	// Standalone comments before this field
	for _, c := range fld.comments {
		if c.Pos.Line > prevFieldLine+1 {
			f.newline()
		}
		f.writeIndent()
		f.writeStr(tokenText(c))
		f.newline()
		prevFieldLine = c.Pos.Line
	}

	// Blank line between fields if original had one
	if len(fld.comments) == 0 && len(fld.tokens) > 0 {
		if fld.tokens[0].Pos.Line > prevFieldLine+1 {
			f.newline()
		}
	}

	f.writeIndent()

	// Raw line (non-field, e.g. fn inside interface)
	if fld.nameLen < 0 {
		f.emitRawFieldTokens(fld)
	} else {
		// Aligned field: name: Type [= default] [@annotations]
		name := fld.tokens[0].Val
		f.writeStr(name)
		f.writeStr(":")
		f.writeStr(strings.Repeat(" ", maxName-fld.nameLen+1))

		hasExtra := len(fld.annTokens) > 0 || len(fld.defTokens) > 0
		f.writeStr(writeTokensCompact(fld.typeTokens))
		if hasExtra {
			f.writeStr(strings.Repeat(" ", maxType-fld.typeLen))
		}

		if len(fld.defTokens) > 0 {
			f.space()
			f.writeStr(writeTokensCompact(fld.defTokens))
		}
		if len(fld.annTokens) > 0 {
			f.space()
			f.writeStr(writeTokensCompact(fld.annTokens))
		}
		if fld.eolComment != nil {
			f.space()
			f.writeStr(tokenText(*fld.eolComment))
		}
	}

	if len(fld.tokens) > 0 {
		prevFieldLine = fld.tokens[0].Pos.Line
	}
	f.newline()
	return prevFieldLine
}

// emitClosingBrace emits the closing } and a newline.
func (f *formatter) emitClosingBrace() {
	f.writeIndent()
	f.writeStr("}")
	if f.peek().Type == token.RBrace {
		f.lastLine = f.peek().Pos.Line
		f.advance()
	}
	f.newline()
	f.needSpace = false
}

// emitRawFieldTokens formats a raw (non-field) line, handling nested blocks with proper indentation.
func (f *formatter) emitRawFieldTokens(fld field) {
	hasBlock := false
	for _, tok := range fld.tokens {
		if tok.Type == token.LBrace {
			hasBlock = true
			break
		}
	}

	if !hasBlock {
		// Simple single-line raw tokens
		f.writeStr(writeTokensCompact(fld.tokens))
		if fld.eolComment != nil {
			f.space()
			f.writeStr(tokenText(*fld.eolComment))
		}
		return
	}

	// Has a block — format with proper indentation
	var prevType token.Type
	for i, tok := range fld.tokens {
		if tok.Type == token.LBrace {
			f.space()
			f.writeStr("{")
			f.newline()
			f.indent++
			// Format remaining tokens as body lines
			f.emitRawBodyTokens(fld.tokens[i+1:])
			return
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
	}
}

// emitRawBodyTokens formats tokens collected from a raw block body with proper indentation.
func (f *formatter) emitRawBodyTokens(tokens []token.Token) {
	// Group tokens by original line
	if len(tokens) == 0 {
		f.indent--
		f.writeIndent()
		f.writeStr("}")
		return
	}

	i := 0
	for i < len(tokens) {
		tok := tokens[i]

		// Closing brace at depth 0
		if tok.Type == token.RBrace {
			f.indent--
			f.writeIndent()
			f.writeStr("}")
			return
		}

		// Emit one line of tokens
		line := tok.Pos.Line
		f.writeIndent()
		var prevType token.Type
		for i < len(tokens) {
			tok = tokens[i]
			if tok.Pos.Line != line {
				break
			}
			// Defensive: stop at inner RBrace to avoid emitting it as content.
			// Unreachable in normal flow because the outer loop handles the
			// final RBrace via the i == len(tokens)-1 check above.
			if tok.Type == token.RBrace {
				break
			}
			f.emitTokenWithSpacing(tok, prevType)
			prevType = tok.Type
			i++
		}
		f.newline()
	}
	// collectRawBody always includes the closing }, so we should never get here.
	// Defensive fallback (unreachable in normal flow):
	f.indent--
	f.writeIndent()
	f.writeStr("}")
}

// collectFields gathers field descriptors from the current position until the matching }.
func (f *formatter) collectFields() []field {
	var fields []field
	var pendingComments []token.Token

	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.RBrace || tok.Type == token.EOF {
			break
		}

		// Collect standalone comments
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			pendingComments = append(pendingComments, tok)
			f.advance()
			continue
		}

		// Check if this is a field: Ident followed by Colon
		if tok.Type == token.Ident && f.peekAt(1).Type == token.Colon {
			fld := f.parseField()
			fld.comments = pendingComments
			pendingComments = nil
			fields = append(fields, fld)
		} else {
			// Non-field line inside block (e.g., fn inside interface, scope, etc.)
			// Emit as a raw line, consuming all tokens on the same line.
			rawLine := f.collectRawLine()
			rawLine.comments = pendingComments
			pendingComments = nil
			fields = append(fields, rawLine)
		}
	}
	return fields
}

// collectRawLine collects all tokens on the current line as a non-aligned "field".
// If the line contains a { block, it consumes the entire block including the closing }.
func (f *formatter) collectRawLine() field {
	fld := field{nameLen: -1} // nameLen = -1 signals this is a raw line
	line := f.peek().Pos.Line
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != line {
			break
		}
		if tok.Type == token.RBrace {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			c := f.advance()
			fld.eolComment = &c
			break
		}
		fld.tokens = append(fld.tokens, tok)
		f.advance()
		// If we just consumed a LBrace, consume the entire body and stop
		if tok.Type == token.LBrace {
			f.collectRawBody(&fld)
			break
		}
	}
	return fld
}

// collectRawBody consumes tokens for a block body (between { and matching }).
func (f *formatter) collectRawBody(fld *field) {
	depth := 1
	for !f.atEnd() && depth > 0 {
		tok := f.peek()
		// Track nested brace depth (defensive: only reachable with nested { } in raw bodies).
		if tok.Type == token.LBrace {
			depth++
		}
		if tok.Type == token.RBrace {
			depth--
		}
		fld.tokens = append(fld.tokens, tok)
		f.advance()
		if depth == 0 {
			break
		}
	}
}

// parseField parses a single field line: name: Type [= default] [@annotations] [// comment]
func (f *formatter) parseField() field {
	fld := field{}
	nameTok := f.advance() // Ident (field name)
	fld.tokens = append(fld.tokens, nameTok)
	fld.nameLen = len(nameTok.Val)

	f.advance() // Colon

	fieldLine := nameTok.Pos.Line

	f.parseFieldTypeTokens(&fld, fieldLine)
	f.parseFieldDefaultValue(&fld, fieldLine)
	f.parseFieldAnnotations(&fld, fieldLine)

	// End-of-line comment
	if f.isCommentOnLine(fieldLine) {
		c := f.advance()
		fld.eolComment = &c
	}

	return fld
}

// isCommentOnLine checks if the next token is a comment on the given line.
func (f *formatter) isCommentOnLine(line int) bool {
	tok := f.peek()
	return (tok.Type == token.Comment || tok.Type == token.DocComment) && tok.Pos.Line == line
}

// isFieldTerminator checks if a token ends field token collection.
func isFieldTerminator(tok token.Token) bool {
	return tok.Type == token.Comment || tok.Type == token.DocComment || tok.Type == token.RBrace
}

// parseFieldTypeTokens collects type tokens (until @, =, comment, newline, or }).
func (f *formatter) parseFieldTypeTokens(fld *field, fieldLine int) {
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != fieldLine {
			break
		}
		if tok.Type == token.At || tok.Type == token.Assign {
			break
		}
		if isFieldTerminator(tok) {
			break
		}
		fld.typeTokens = append(fld.typeTokens, tok)
		fld.tokens = append(fld.tokens, tok)
		f.advance()
	}
	fld.typeLen = compactLen(fld.typeTokens)
}

// parseFieldDefaultValue collects default value tokens (= xxx).
func (f *formatter) parseFieldDefaultValue(fld *field, fieldLine int) {
	if f.peek().Type != token.Assign || f.peek().Pos.Line != fieldLine {
		return
	}
	eqTok := f.advance() // =
	fld.defTokens = append(fld.defTokens, eqTok)
	fld.tokens = append(fld.tokens, eqTok)
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != fieldLine || tok.Type == token.At || isFieldTerminator(tok) {
			break
		}
		fld.defTokens = append(fld.defTokens, tok)
		fld.tokens = append(fld.tokens, tok)
		f.advance()
	}
	fld.defLen = compactLen(fld.defTokens)
}

// parseFieldAnnotations collects annotation tokens (@ ...).
func (f *formatter) parseFieldAnnotations(fld *field, fieldLine int) {
	for f.peek().Type == token.At && f.peek().Pos.Line == fieldLine {
		f.parseOneAnnotation(fld, fieldLine)
	}
}

// parseOneAnnotation collects tokens for a single annotation.
// annState tracks depth inside annotation parens/braces.
type annState struct {
	parenDepth int
	braceDepth int
}

func (a *annState) atTopLevel() bool {
	return a.parenDepth == 0 && a.braceDepth == 0
}

func (a *annState) updateDepth(tok token.Token) (done bool) {
	switch tok.Type {
	case token.LParen:
		a.parenDepth++
	case token.RParen:
		a.parenDepth--
		if a.parenDepth <= 0 && a.braceDepth == 0 {
			return true
		}
	case token.LBrace:
		a.braceDepth++
	case token.RBrace:
		a.braceDepth--
		if a.braceDepth <= 0 && a.parenDepth == 0 {
			return true
		}
	}
	return false
}

func (f *formatter) parseOneAnnotation(fld *field, fieldLine int) {
	var st annState
	for !f.atEnd() {
		tok := f.peek()
		if st.atTopLevel() && (tok.Pos.Line != fieldLine || isFieldTerminator(tok)) {
			break
		}
		fld.annTokens = append(fld.annTokens, tok)
		fld.tokens = append(fld.tokens, tok)
		f.advance()

		if st.updateDepth(tok) {
			return
		}
		// Bare annotation: Ident not followed by ( or {
		if st.atTopLevel() && tok.Type == token.Ident {
			next := f.peek()
			if next.Type != token.LParen && next.Type != token.LBrace {
				return
			}
		}
	}
}

// --- Enum ---

func (f *formatter) formatEnum() {
	f.writeIndent()

	startLine := f.peek().Pos.Line

	// Always format enums as multiline, even if input is single-line.
	f.emitHeaderUntilBrace()
	if f.peek().Type != token.LBrace {
		f.lastLine = startLine
		f.newline()
		return
	}
	f.space()
	f.writeStr("{")
	f.advance() // LBrace
	f.newline()
	f.lastLine = startLine

	f.indent++
	f.emitSimpleBodyItems()
	f.indent--

	f.emitClosingBrace()
}

// emitSimpleBodyItems emits simple body items (one token per line) until }.
func (f *formatter) emitSimpleBodyItems() {
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.RBrace {
			break
		}
		f.emitBlankLineIfNeeded(tok)
		f.writeIndent()
		f.writeStr(tokenText(tok))
		f.lastLine = tok.Pos.Line
		f.advance()
		f.newline()
	}
}

// --- Sealed ---

func (f *formatter) formatSealed() {
	f.writeIndent()
	startLine := f.peek().Pos.Line

	// Header: sealed Name {
	f.emitHeaderUntilBrace()
	if f.peek().Type != token.LBrace {
		f.lastLine = startLine
		f.newline()
		return
	}
	f.space()
	f.writeStr("{")
	f.advance() // LBrace
	f.newline()
	f.lastLine = startLine

	// Variants
	f.indent++
	f.emitSealedVariants()
	f.indent--

	f.emitClosingBrace()
}

// emitSealedVariants emits sealed variant lines until }.
func (f *formatter) emitSealedVariants() {
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.RBrace {
			break
		}
		f.emitBlankLineIfNeeded(tok)
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			f.writeIndent()
			f.writeStr(tokenText(tok))
			f.newline()
			f.lastLine = tok.Pos.Line
			f.advance()
			continue
		}
		f.emitSealedVariantLine()
	}
}

// emitSealedVariantLine emits one variant: Name or Name(params...).
func (f *formatter) emitSealedVariantLine() {
	variantLine := f.peek().Pos.Line
	f.writeIndent()
	var prevType token.Type
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != variantLine || tok.Type == token.RBrace {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
	f.lastLine = variantLine
	f.newline()
	f.needSpace = false
}

// --- API / Fn ---

func (f *formatter) formatApiOrFn() {
	f.writeIndent()
	startLine := f.peek().Pos.Line

	// Emit keyword + name
	kwTok := f.advance() // api or fn
	f.writeStr(tokenText(kwTok))
	f.space()
	nameTok := f.advance() // name
	f.writeStr(tokenText(nameTok))

	if f.peek().Type != token.LParen {
		// No params — emit rest of line
		f.emitRestOfLineFrom(startLine, nameTok.Type)
		return
	}

	f.formatParamList()
	f.emitDeclTail(true)
	f.emitOptionalBody(startLine)
}

func (f *formatter) formatOverride() {
	f.writeIndent()
	f.writeStr("override")
	f.advance() // override
	f.space()
	// Next should be api or fn
	if f.peek().Type == token.Api || f.peek().Type == token.Fn {
		f.formatOverrideApiOrFn()
	} else {
		// Already wrote "override ", so prevType 0 means no additional space
		f.emitRestOfLineFrom(f.peek().Pos.Line, 0)
	}
}

// formatOverrideApiOrFn handles the api/fn portion after the "override" keyword.
func (f *formatter) formatOverrideApiOrFn() {
	kwTok := f.advance()
	f.writeStr(tokenText(kwTok))
	f.space()
	nameTok := f.advance()
	f.writeStr(tokenText(nameTok))

	startLine := kwTok.Pos.Line
	if f.peek().Type == token.LParen {
		f.formatParamList()
	}
	f.emitDeclTailBounded(startLine)
	f.emitBodyBlock()
	f.newline()
	f.needSpace = false
}

// formatParamList formats parameters using inline or multiline style based on count and doc comments.
func (f *formatter) formatParamList() {
	paramCount := f.countParams()
	hasDocComments := f.paramsHaveDocComments()
	if paramCount > 3 || hasDocComments {
		f.formatMultilineParams()
	} else {
		f.formatInlineParams()
	}
}

// emitDeclTail emits tokens after the parameter list up to the body or end of declaration.
// When checkTopLevel is true, it also stops at top-level keywords and EOF.
func (f *formatter) emitDeclTail(checkTopLevel bool) {
	var prevType token.Type = token.RParen
	parenDepth := 0
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.LBrace && parenDepth == 0 {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		// Defensive: atEnd() already returns true for EOF, so this is unreachable
		// in normal flow, but guards against any future changes to atEnd().
		if checkTopLevel && tok.Type == token.EOF {
			break
		}
		if checkTopLevel && parenDepth == 0 && isTopLevelKeyword(tok.Type) {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
		switch tok.Type {
		case token.LParen:
			parenDepth++
		case token.RParen:
			parenDepth--
		}
	}
}

// emitDeclTailBounded emits tokens after the parameter list, stopping when the
// token line exceeds startLine+1 (used by override declarations).
func (f *formatter) emitDeclTailBounded(startLine int) {
	var prevType token.Type = token.RParen
	parenDepth := 0
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.LBrace && parenDepth == 0 {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		if tok.Pos.Line > startLine+1 && parenDepth == 0 {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
		switch tok.Type {
		case token.LParen:
			parenDepth++
		case token.RParen:
			parenDepth--
		}
	}
}

// emitOptionalBody emits an optional { body } block and trailing newline.
// If no body is present, lastLine is set to startLine.
func (f *formatter) emitOptionalBody(startLine int) {
	if f.peek().Type == token.LBrace {
		braceLine := f.peek().Pos.Line
		f.space()
		f.writeStr("{")
		f.advance()
		f.newline()
		f.lastLine = braceLine
		f.formatBody()
		f.writeIndent()
		f.writeStr("}")
		if f.peek().Type == token.RBrace {
			f.lastLine = f.peek().Pos.Line
			f.advance()
		}
	} else {
		f.lastLine = startLine
	}
	f.newline()
	f.needSpace = false
}

// emitBodyBlock emits an optional { body } block without trailing newline management.
func (f *formatter) emitBodyBlock() {
	if f.peek().Type != token.LBrace {
		return
	}
	f.space()
	f.writeStr("{")
	f.advance()
	f.newline()
	f.formatBody()
	f.writeIndent()
	f.writeStr("}")
	if f.peek().Type == token.RBrace {
		f.lastLine = f.peek().Pos.Line
		f.advance()
	}
}

func (f *formatter) countParams() int {
	count := 0
	depth := 0
	for i := f.pos; i < len(f.tokens); i++ {
		switch f.tokens[i].Type {
		case token.LParen:
			depth++
			if depth == 1 {
				count = 1
			}
		case token.RParen:
			depth--
			if depth == 0 {
				if count == 1 {
					// Check if there were actually any params
					// If LParen is immediately followed by RParen, count = 0
					for j := f.pos; j < i; j++ {
						if f.tokens[j].Type == token.LParen {
							if j+1 < len(f.tokens) && f.tokens[j+1].Type == token.RParen {
								return 0
							}
						}
					}
				}
				return count
			}
		case token.Comma:
			if depth == 1 {
				count++
			}
		}
	}
	return count
}

func (f *formatter) formatInlineParams() {
	// Emit ( params )
	f.writeStr("(")
	f.advance() // LParen
	depth := 1
	for !f.atEnd() && depth > 0 {
		tok := f.peek()
		if tok.Type == token.LParen {
			depth++
		}
		if tok.Type == token.RParen {
			depth--
			if depth == 0 {
				f.writeStr(")")
				f.advance()
				break
			}
		}
		if tok.Type == token.Comma {
			f.writeStr(",")
			f.advance()
			f.space()
			continue
		}
		if tok.Type == token.Colon {
			f.writeStr(":")
			f.advance()
			f.space()
			continue
		}
		if needSpaceBefore(tok.Type) && f.needSpace {
			f.space()
		}
		f.writeStr(tokenText(tok))
		f.needSpace = needSpaceAfter(tok.Type)
		f.advance()
	}
}

// paramsHaveDocComments checks if the upcoming param list contains any doc comments.
// It scans forward from the current position without advancing the formatter.
func (f *formatter) paramsHaveDocComments() bool {
	depth := 0
	for i := f.pos; i < len(f.tokens); i++ {
		switch f.tokens[i].Type {
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 {
				return false
			}
		case token.DocComment:
			if depth > 0 {
				return true
			}
		}
	}
	return false
}

// paramGroup holds tokens for a single parameter.
type paramGroup struct {
	comments []token.Token // doc comments preceding this parameter
	tokens   []token.Token
}

func (f *formatter) formatMultilineParams() {
	f.writeStr("(")
	f.advance() // LParen
	f.newline()

	f.indent++

	params := f.collectParamGroups()

	maxParamName := 0
	for _, p := range params {
		if len(p.tokens) > 0 && len(p.tokens[0].Val) > maxParamName {
			maxParamName = len(p.tokens[0].Val)
		}
	}

	for _, p := range params {
		// Emit doc comments before this parameter
		for _, c := range p.comments {
			f.writeIndent()
			f.writeStr(tokenText(c))
			f.newline()
		}
		f.writeIndent()
		f.emitAlignedParam(p, maxParamName)
		f.writeStr(",")
		f.newline()
	}
	f.indent--
	f.writeIndent()
	f.writeStr(")")
}

// collectParamGroups collects parameters separated by commas within parentheses.
// Doc comments (///) before a parameter are stored in the comments field.
func (f *formatter) collectParamGroups() []paramGroup {
	var params []paramGroup
	var cur paramGroup
	depth := 1

	for !f.atEnd() && depth > 0 {
		tok := f.advance()
		switch {
		case tok.Type == token.LParen:
			depth++
			cur.tokens = append(cur.tokens, tok)
		case tok.Type == token.RParen:
			depth--
			if depth == 0 {
				if len(cur.tokens) > 0 || len(cur.comments) > 0 {
					params = append(params, cur)
				}
			} else {
				cur.tokens = append(cur.tokens, tok)
			}
		case tok.Type == token.Comma && depth == 1:
			params = append(params, cur)
			cur = paramGroup{}
		case (tok.Type == token.DocComment || tok.Type == token.Comment) && depth == 1:
			// Doc/line comments before param tokens belong to the next param
			cur.comments = append(cur.comments, tok)
		default:
			cur.tokens = append(cur.tokens, tok)
		}
	}
	return params
}

// emitAlignedParam emits a single param line with alignment.
func (f *formatter) emitAlignedParam(p paramGroup, maxParamName int) {
	if len(p.tokens) >= 2 && p.tokens[1].Type == token.Colon {
		f.writeStr(p.tokens[0].Val)
		f.writeStr(":")
		f.writeStr(strings.Repeat(" ", maxParamName-len(p.tokens[0].Val)+1))
		for j := 2; j < len(p.tokens); j++ {
			if j > 2 {
				f.space()
			}
			f.writeStr(tokenText(p.tokens[j]))
		}
	} else {
		f.writeStr(writeTokensCompact(p.tokens))
	}
}

// --- Event ---

func (f *formatter) formatEvent() {
	f.writeIndent()
	startLine := f.peek().Pos.Line

	// event Name(params...)
	f.writeStr("event")
	f.advance()
	f.space()
	f.writeStr(tokenText(f.peek())) // name
	f.advance()

	if f.peek().Type == token.LParen {
		paramCount := f.countParams()
		hasDocComments := f.paramsHaveDocComments()
		if paramCount > 3 || hasDocComments {
			f.formatMultilineParams()
		} else {
			f.formatInlineParams()
		}
	}
	f.lastLine = startLine
	f.newline()
	f.needSpace = false
}

// --- On ---

func (f *formatter) formatOn() {
	f.writeIndent()
	startLine := f.peek().Pos.Line

	// on EventName [{ params -> body }] or @native
	f.writeStr("on")
	f.advance()
	f.space()
	f.writeStr(tokenText(f.peek())) // event name
	f.advance()

	// Remaining same-line tokens that aren't @ or { — emit inline
	if f.peek().Type != token.At && f.peek().Type != token.LBrace {
		f.emitRestOfLineFrom(startLine, token.Ident) // after event name
		return
	}

	// Check for @native or { body }
	if f.peek().Type == token.At {
		var prevType token.Type = token.Ident // after event name
		for !f.atEnd() {
			tok := f.peek()
			if tok.Pos.Line != startLine {
				break
			}
			f.emitTokenWithSpacing(tok, prevType)
			prevType = tok.Type
			f.advance()
		}
		f.lastLine = startLine
		f.newline()
		f.needSpace = false
		return
	}

	if f.peek().Type == token.LBrace {
		closeLine := f.findClosingBraceLine()
		if closeLine == f.peek().Pos.Line {
			f.formatOnInlineBody()
		} else {
			f.formatOnMultilineBody(startLine)
		}
	}
	f.newline()
	f.needSpace = false
}

// formatOnInlineBody handles: on Event { param -> expr }
func (f *formatter) formatOnInlineBody() {
	f.space()
	f.writeStr("{")
	f.advance()
	var prevType token.Type = token.LBrace
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.RBrace {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
	f.space()
	f.writeStr("}")
	if f.peek().Type == token.RBrace {
		f.lastLine = f.peek().Pos.Line
		f.advance()
	}
}

// formatOnMultilineBody handles: on Event { param ->\n  body\n}
func (f *formatter) formatOnMultilineBody(startLine int) {
	f.space()
	f.writeStr("{")
	f.advance()
	if f.peek().Pos.Line == startLine {
		var prevType token.Type = token.LBrace
		for !f.atEnd() {
			tok := f.peek()
			if tok.Pos.Line != startLine || tok.Type == token.RBrace {
				break
			}
			f.emitTokenWithSpacing(tok, prevType)
			prevType = tok.Type
			f.advance()
		}
	}
	f.newline()
	f.lastLine = startLine
	f.formatBody()
	f.writeIndent()
	f.writeStr("}")
	if f.peek().Type == token.RBrace {
		f.lastLine = f.peek().Pos.Line
		f.advance()
	}
}

// --- Middleware ---

func (f *formatter) formatMiddleware() {
	f.writeIndent()
	startLine := f.peek().Pos.Line
	var prevType token.Type

	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != startLine {
			break
		}
		if tok.Type == token.LBrace {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}

	if f.peek().Type == token.LBrace {
		f.space()
		f.writeStr("{")
		f.advance()
		f.newline()
		f.lastLine = startLine
		f.formatBody()
		f.writeIndent()
		f.writeStr("}")
		if f.peek().Type == token.RBrace {
			f.lastLine = f.peek().Pos.Line
			f.advance()
		}
	}
	f.lastLine = startLine
	f.newline()
	f.needSpace = false
}

// --- Body (statements inside api/fn/on/middleware) ---

func (f *formatter) formatBody() {
	f.indent++
	for !f.atEnd() {
		tok := f.peek()
		if tok.Type == token.RBrace {
			break
		}
		// Defensive: atEnd() already returns true for EOF, making this unreachable
		// in normal flow, but kept as a safety guard.
		if tok.Type == token.EOF {
			break
		}

		f.emitBlankLineIfNeeded(tok)

		if tok.Type == token.Comment || tok.Type == token.DocComment {
			f.writeIndent()
			f.writeStr(tokenText(tok))
			f.lastLine = tok.Pos.Line
			f.advance()
			f.newline()
			continue
		}

		// Emit statement
		stmtLine := tok.Pos.Line
		f.writeIndent()
		f.formatStatement(stmtLine)
	}
	f.indent--
}

// stmtState holds mutable state for statement formatting.
type stmtState struct {
	prevType   token.Type
	parenDepth int
	curLine    int
}

func (f *formatter) formatStatement(startLine int) {
	st := &stmtState{curLine: startLine}
	for !f.atEnd() {
		tok := f.peek()
		if f.stmtShouldBreak(tok, st) {
			break
		}
		if tok.Type == token.LBrace && st.parenDepth == 0 {
			if done := f.handleNestedBlock(startLine, &st.prevType); done {
				return
			}
			continue
		}
		if tok.Type == token.RBrace && st.parenDepth == 0 {
			if f.handleInlineRBrace(st) {
				return
			}
			continue
		}
		// Check line continuation BEFORE updating paren depth,
		// so that ) on a new line is still considered inside the paren group.
		if tok.Pos.Line != st.curLine {
			if f.handleLineContinuation(tok, st) {
				continue
			}
			break
		}
		f.emitTokenWithSpacing(tok, st.prevType)
		st.prevType = tok.Type
		// Update paren depth after emitting the token
		if tok.Type == token.LParen {
			st.parenDepth++
		}
		if tok.Type == token.RParen {
			st.parenDepth--
		}
		f.advance()
	}
	f.lastLine = st.curLine
	f.newline()
	f.needSpace = false
}

// stmtShouldBreak checks if the statement loop should exit.
func (f *formatter) stmtShouldBreak(tok token.Token, st *stmtState) bool {
	if tok.Type == token.RBrace && tok.Pos.Line != st.curLine && st.parenDepth == 0 {
		return true
	}
	// Defensive: atEnd() in the caller loop already returns true for EOF,
	// making this unreachable in normal flow, but kept as a safety guard.
	if tok.Type == token.EOF {
		return true
	}
	if tok.Type == token.Comment || tok.Type == token.DocComment {
		if tok.Pos.Line == st.curLine {
			f.space()
			f.writeStr(tokenText(tok))
			f.advance()
		}
		return true
	}
	return false
}

// handleInlineRBrace handles a } inside an inline block.
// Returns true if the statement is complete.
func (f *formatter) handleInlineRBrace(st *stmtState) bool {
	tok := f.peek()
	f.emitTokenWithSpacing(tok, st.prevType)
	st.prevType = tok.Type
	f.advance()
	st.curLine = tok.Pos.Line
	next := f.peek()
	// Chain continuation after }
	if next.Pos.Line != st.curLine && (next.Type == token.Dot || next.Type == token.SafeDot) {
		return false
	}
	// Same line continuation
	if next.Pos.Line == st.curLine && next.Type != token.RBrace && next.Type != token.EOF {
		return false
	}
	f.lastLine = st.curLine
	f.newline()
	f.needSpace = false
	return true
}

// handleLineContinuation handles tokens on a new line (parens, chains).
// Returns true if the statement continues (caller should continue loop).
func (f *formatter) handleLineContinuation(tok token.Token, st *stmtState) bool {
	if st.parenDepth > 0 {
		f.newline()
		f.writeIndent()
		// Closing paren: no extra indent; args: extra indent
		if tok.Type != token.RParen {
			f.writeStr("  ")
		}
		st.curLine = tok.Pos.Line
		st.prevType = 0
		return true
	}
	if st.parenDepth < 0 {
		st.parenDepth = 0
		f.newline()
		f.writeIndent()
		st.curLine = tok.Pos.Line
		st.prevType = 0
		return true
	}
	if tok.Type == token.Dot || tok.Type == token.SafeDot || tok.Type == token.Elvis {
		f.newline()
		f.writeIndent()
		f.writeStr("  ")
		st.curLine = tok.Pos.Line
		st.prevType = 0
		return true
	}
	return false
}

// handleNestedBlock handles a { block inside a statement.
// Returns true if the statement is complete (caller should return).
func (f *formatter) handleNestedBlock(startLine int, prevType *token.Type) bool {
	tok := f.peek()
	closeLine := f.findClosingBraceLine()
	if closeLine == tok.Pos.Line {
		// Single-line block — emit inline
		f.emitTokenWithSpacing(tok, *prevType)
		*prevType = tok.Type
		f.advance()
		return false
	}
	f.space()
	f.writeStr("{")
	f.lastLine = tok.Pos.Line
	f.advance()
	// lambda params: { ident (,ident)* -> body } — keep params on same line as {
	f.emitLambdaParams()
	f.newline()
	f.formatBody()
	f.writeIndent()
	f.writeStr("}")
	if f.peek().Type == token.RBrace {
		f.lastLine = f.peek().Pos.Line
		f.advance()
	}
	// Check if there's more on the same statement
	if f.peek().Pos.Line == f.lastLine && f.peek().Type != token.RBrace &&
		f.peek().Type != token.EOF {
		*prevType = token.RBrace
		return false
	}
	f.newline()
	f.needSpace = false
	return true
}

// emitLambdaParams checks if the next tokens form a lambda param list (ident (,ident)* ->)
// and emits them inline after {. Does nothing if the pattern doesn't match.
func (f *formatter) emitLambdaParams() {
	// scan ahead without consuming to verify the pattern
	i := f.pos
	if i >= len(f.tokens) || f.tokens[i].Type != token.Ident {
		return
	}
	j := i + 1
	for j+1 < len(f.tokens) && f.tokens[j].Type == token.Comma && f.tokens[j+1].Type == token.Ident {
		j += 2
	}
	if j >= len(f.tokens) || f.tokens[j].Type != token.Arrow {
		return
	}
	// pattern confirmed — emit: " ident, ident ->"
	for f.pos <= j {
		tok := f.peek()
		if tok.Type != token.Comma {
			f.space()
		}
		f.writeStr(tokenText(tok))
		f.advance()
	}
}

// findClosingBraceLine finds the line of the matching } for the current { position.
func (f *formatter) findClosingBraceLine() int {
	depth := 0
	for i := f.pos; i < len(f.tokens); i++ {
		switch f.tokens[i].Type {
		case token.LBrace:
			depth++
		case token.RBrace:
			depth--
			if depth == 0 {
				return f.tokens[i].Pos.Line
			}
		}
	}
	return -1
}

// --- Helpers ---

func (f *formatter) emitRestOfLineFrom(line int, prevType token.Type) {
	for !f.atEnd() {
		tok := f.peek()
		if tok.Pos.Line != line {
			break
		}
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			break
		}
		// Stop at next top-level declaration on same line
		if isTopLevelKeyword(tok.Type) {
			break
		}
		f.emitTokenWithSpacing(tok, prevType)
		prevType = tok.Type
		f.advance()
	}
	f.emitEolComment(line)
	f.lastLine = line
	f.newline()
	f.needSpace = false
}

// isTopLevelKeyword returns true if the token type starts a new top-level declaration.
func isTopLevelKeyword(t token.Type) bool {
	switch t {
	case token.Use, token.Val, token.Var,
		token.Model, token.KwType, token.Interface, token.Extend, token.Error,
		token.Enum, token.Sealed, token.Api, token.Fn,
		token.Override, token.Event, token.On, token.Middleware:
		return true
	}
	return false
}

func (f *formatter) emitEolComment(line int) {
	if f.peek().Type == token.Comment || f.peek().Type == token.DocComment {
		if f.peek().Pos.Line == line {
			f.space()
			f.writeStr(tokenText(f.peek()))
			f.advance()
		}
	}
}

// needSpaceAfter returns whether a space should be emitted after this token type.
func needSpaceAfter(t token.Type) bool {
	switch t {
	case token.LParen, token.LBracket, token.Dot, token.SafeDot,
		token.DotDot, token.At, token.Bang,
		token.StringStart, token.StringMid:
		return false
	default:
		return true
	}
}

// needSpaceBefore returns whether a space should be emitted before this token type.
func needSpaceBefore(t token.Type) bool {
	switch t {
	case token.RParen, token.RBracket, token.Comma, token.Dot,
		token.SafeDot, token.DotDot, token.DotDotDot, token.Question,
		token.Colon, token.LParen,
		token.StringMid, token.StringEnd:
		return false
	default:
		return true
	}
}

// emitTokenWithSpacing writes a token with proper spacing relative to the previous token.
func (f *formatter) emitTokenWithSpacing(tok token.Token, prevType token.Type) {
	if prevType != 0 && needSpaceAfter(prevType) && needSpaceBefore(tok.Type) {
		f.space()
	}
	f.writeStr(tokenText(tok))
}

// writeTokensCompact writes tokens with context-aware spacing.
func writeTokensCompact(toks []token.Token) string {
	var sb strings.Builder
	for i, tok := range toks {
		if i > 0 {
			prev := toks[i-1].Type
			if needSpaceAfter(prev) && needSpaceBefore(tok.Type) {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(tokenText(tok))
	}
	return sb.String()
}

// compactLen returns the rendered length of tokens with compact spacing.
func compactLen(toks []token.Token) int {
	return len(writeTokensCompact(toks))
}
