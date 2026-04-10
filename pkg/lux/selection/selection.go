package selection

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/str"
)

// Field represents a selected field, optionally with nested sub-selections.
type Field struct {
	Name     string
	Children []*Field // nil = leaf field
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
