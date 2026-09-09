package selection

import (
	"fmt"
	"strings"

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
//	IDENT          = [a-zA-Z_][a-zA-Z0-9_]*
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
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return nil, nil
	}

	var fields []*Field
	f, err := p.parseField()
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	fields = append(fields, f)

	for {
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ',' {
			break
		}
		p.pos++ // skip ','
		f, err := p.parseField()
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, fmt.Errorf("expected field name after ',' at position %d", p.pos)
		}
		fields = append(fields, f)
	}

	return fields, nil
}

// parseField parses: IDENT ('{' selection_list '}')?
func (p *parser) parseField() (*Field, error) {
	p.skipSpaces()
	name := p.readIdent()
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
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != '}' {
			return nil, fmt.Errorf("expected '}' for '%s' at position %d", name, p.pos)
		}
		p.pos++ // skip '}'
		f.Children = children
	}

	return f, nil
}

// readIdent reads [a-zA-Z_][a-zA-Z0-9_]*
func (p *parser) readIdent() string {
	start := p.pos
	if p.pos >= len(p.input) {
		return ""
	}
	c := p.input[p.pos]
	if !isIdentStart(c) {
		return ""
	}
	p.pos++
	for p.pos < len(p.input) && isIdentPart(p.input[p.pos]) {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
