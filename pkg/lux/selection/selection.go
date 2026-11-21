package selection

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/str"
)

// Format returns the canonical compact representation of a selection tree.
func Format(fields []*Field) string {
	if len(fields) == 0 {
		return ""
	}
	var builder strings.Builder
	writeSelection(&builder, fields)
	return builder.String()
}

func writeSelection(builder *strings.Builder, fields []*Field) {
	for index, field := range fields {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(field.Name)
		if len(field.Children) == 0 {
			continue
		}
		builder.WriteByte('{')
		writeSelection(builder, field.Children)
		builder.WriteByte('}')
	}
}

// Field represents a selected field, optionally with nested sub-selections.
type Field struct {
	Name     string
	Children []*Field // nil = leaf field
}

// At borrows a selected subtree without copying it. Nil retains the all-fields
// contract; an absent path returns an explicit empty selection.
func At(fields []*Field, path ...string) []*Field {
	for _, name := range path {
		if fields == nil {
			return nil
		}
		found := false
		for _, field := range fields {
			if field != nil && field.Name == name {
				fields, found = field.Children, true
				break
			}
		}
		if !found {
			return []*Field{}
		}
	}
	return fields
}

// EnsureField returns fields with name selected. It preserves nil because nil
// means all fields, and never mutates the caller-owned selection slice.
func EnsureField(fields []*Field, name string) []*Field {
	if fields == nil {
		return nil
	}
	for _, field := range fields {
		if field.Name == name {
			return fields
		}
	}
	result := make([]*Field, len(fields), len(fields)+1)
	copy(result, fields)
	return append(result, &Field{Name: name})
}

// Merge combines two partial selection trees without mutating either input.
// A nil tree means all fields and therefore dominates a partial selection.
func Merge(left, right []*Field) []*Field {
	if left == nil || right == nil {
		return nil
	}
	result := cloneFields(left)
	positions := make(map[string]int, len(result))
	for index, field := range result {
		positions[field.Name] = index
	}
	for _, field := range right {
		index, exists := positions[field.Name]
		if !exists {
			positions[field.Name] = len(result)
			result = append(result, cloneField(field))
			continue
		}
		result[index].Children = mergeChildren(result[index].Children, field.Children)
	}
	return result
}

func mergeChildren(left, right []*Field) []*Field {
	if left == nil || right == nil {
		return nil
	}
	return Merge(left, right)
}

func cloneFields(fields []*Field) []*Field {
	if fields == nil {
		return nil
	}
	result := make([]*Field, len(fields))
	for index, field := range fields {
		result[index] = cloneField(field)
	}
	return result
}

func cloneField(field *Field) *Field {
	if field == nil {
		return nil
	}
	return &Field{Name: field.Name, Children: cloneFields(field.Children)}
}

// SQLColumns extracts leaf field names as snake_case SQL column names.
// Relation fields (with Children) are excluded — they're not DB columns.
// Always includes "id" if not already selected (needed for DataLoader FK resolution).
func SQLColumns(fields []*Field) []string {
	if len(fields) == 0 {
		return nil // nil = SELECT *
	}
	cols := make([]string, 0, len(fields)+1)
	cols = append(cols, "id") // always include id
	for _, f := range fields {
		if f.Children != nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		if col == "id" {
			continue // already added
		}
		cols = append(cols, col)
	}
	if len(cols) == 1 { // only "id", no user fields → SELECT *
		return nil
	}
	return cols
}

// SQLColumnsOr returns SQLColumns if $select is provided, otherwise returns fallback.
// Used to exclude @hidden fields when no $select is given.
func SQLColumnsOr(fields []*Field, fallback []string) []string {
	cols := SQLColumns(fields)
	if cols != nil {
		return cols
	}
	return fallback
}

// Parse parses a $select string into a field selection tree.
//
// Grammar:
//
//	selection_list = selection (',' selection)*
//	selection      = IDENT ('{' selection_list '}')?
//	IDENT          = (UnicodeLetter | _) (UnicodeLetter | _ | ASCIIDigit)*
//
// Example: "name,email,posts{title,comments{content}}"
func Parse(input string) ([]*Field, error) {
	p := &parser{input: input}
	fields, err := p.parseList()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.input) {
		return nil, fmt.Errorf("unexpected character '%c' at position %d", p.input[p.pos], p.pos)
	}
	return fields, nil
}

type parser struct {
	input string
	pos   int
}

// parseList parses: selection (',' selection)*
func (p *parser) parseList() ([]*Field, error) {
	var fields []*Field
	field, err := p.parseField()
	if err != nil {
		return nil, err
	}
	if field == nil {
		return nil, nil
	}
	fields = append(fields, field)
	for p.pos < len(p.input) && p.input[p.pos] == ',' {
		p.pos++
		field, err := p.parseField()
		if err != nil {
			return nil, err
		}
		if field == nil {
			return nil, fmt.Errorf("expected field name after ',' at position %d", p.pos)
		}
		fields = append(fields, field)
	}

	return fields, nil
}

// parseField parses: IDENT ('{' selection_list '}')?
func (p *parser) parseField() (*Field, error) {
	p.skipSpaces()
	name, err := p.readIdent()
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, nil
	}

	f := &Field{Name: name}

	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '{' {
		p.pos++ // skip '{'
		children, err := p.parseList()
		if err != nil {
			return nil, err
		}
		if len(children) == 0 {
			return nil, fmt.Errorf("empty selection block for '%s' at position %d", name, p.pos)
		}
		if p.pos >= len(p.input) || p.input[p.pos] != '}' {
			return nil, fmt.Errorf("expected '}' for '%s' at position %d", name, p.pos)
		}
		p.pos++ // skip '}'
		p.skipSpaces()
		f.Children = children
	}

	return f, nil
}

// readIdent preserves the DSL's Unicode letters and ASCII digits without normalizing names.
func (p *parser) readIdent() (string, error) {
	start := p.pos
	if start >= len(p.input) {
		return "", nil
	}
	first := p.input[start]
	if first >= utf8.RuneSelf {
		return p.readUnicodeIdent(start)
	}
	if !isIdentStart(first) {
		return "", nil
	}
	for offset, c := range []byte(p.input[start+1:]) {
		if isIdentStart(c) || c-'0' <= 9 {
			continue
		}
		p.pos = start + offset + 1
		if c >= utf8.RuneSelf {
			return p.readUnicodeIdent(start)
		}
		return p.input[start:p.pos], nil
	}
	p.pos = len(p.input)
	return p.input[start:], nil
}

func (p *parser) readUnicodeIdent(start int) (string, error) {
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if c < utf8.RuneSelf {
			if !isIdentStart(c) && !(p.pos > start && c >= '0' && c <= '9') {
				break
			}
			p.pos++
			continue
		}
		r, width := utf8.DecodeRuneInString(p.input[p.pos:])
		if r == utf8.RuneError && width == 1 {
			return "", fmt.Errorf("invalid UTF-8 at position %d", p.pos)
		}
		if !unicode.IsLetter(r) {
			break
		}
		p.pos += width
	}
	return p.input[start:p.pos], nil
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func isIdentStart(c byte) bool {
	return (c|0x20)-'a' <= 'z'-'a' || c == '_'
}
