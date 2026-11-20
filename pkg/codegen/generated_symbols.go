package codegen

import (
	"fmt"
	goast "go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"maps"
	"slices"
	"strings"
)

type generatedSymbolKey struct{ owner, name string }

type generatedSymbol struct {
	position token.Pos
	kind     string
}

type generatedSymbols struct {
	positions *token.FileSet
	names     map[generatedSymbolKey]generatedSymbol
}

// Inspect actual output instead of maintaining a second catalog of template names.
// This is a generation-only namespace check, not Go type checking.
func validateGeneratedSymbols(files map[string][]byte) error {
	return prepareGeneratedSymbols(files, false)
}

// Format and check the same parse tree to avoid parsing generated bodies twice.
// Go 1.27.1 preserves parsed-template grammar across identifier-only renames.
// Comment/semicolon gating and list comma rules keep the renamed output parseable.
// Retain formatting errors for inputs outside that normal producer contract.
func prepareGeneratedSymbols(files map[string][]byte, exportUnicode bool) error {
	return prepareGeneratedSources(files, exportUnicode, false)
}

// Source reuse requires exclusive ownership of every buffer. Generate owns its
// temporary package and publishes it only after every file passes validation.
func prepareGeneratedSources(files map[string][]byte, exportUnicode, reuseSource bool) error {
	symbols := generatedSymbols{positions: token.NewFileSet(), names: make(map[generatedSymbolKey]generatedSymbol)}
	const mode = parser.SkipObjectResolution | parser.ParseComments
	names := slices.Sorted(maps.Keys(files))
	formattedSources := make([][]byte, len(names))
	for index, name := range names {
		source := files[name]
		file, err := parser.ParseFile(symbols.positions, name, source, mode)
		if err != nil {
			return fmt.Errorf("format %s: %w", name, err)
		}
		if exportUnicode {
			exportGeneratedUnicodeIdentifiers(file)
		}
		for _, declaration := range file.Decls {
			if err := symbols.addDeclaration(declaration); err != nil {
				return err
			}
		}
		formatted, err := formatParsedGeneratedFile(symbols.positions, file, source, reuseSource)
		if err != nil {
			return fmt.Errorf("format %s: %w", name, err)
		}
		formattedSources[index] = formatted
	}
	for index, name := range names {
		files[name] = formattedSources[index]
	}
	return nil
}

func formatParsedGeneratedFile(positions *token.FileSet, file *goast.File, source []byte, reuseSource bool) ([]byte, error) {
	var output formattedSource
	// ParseFile has copied the token text. Reuse the generator's own storage.
	if reuseSource {
		output = source[:0]
	}
	output = slices.Grow(output, len(source))
	var err error
	goast.SortImports(positions, file)
	if hasNoncanonicalNumberSpelling(file) {
		// Use gofmt's literal normalization for Go spellings the printer preserves.
		err = format.Node(&output, positions, file)
	} else {
		// format.Node makes a copy of files with grouped imports through reprinting
		// and reparsing. This tree is owned by codegen and its imports are sorted.
		printerConfig := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
		err = printerConfig.Fprint(&output, positions, file)
	}
	return output, err
}

// Formatting can add enough whitespace to exceed the raw source's capacity.
// A slice appends into that storage and uses Go's slice growth when it fills.
type formattedSource []byte

func (s *formattedSource) Write(p []byte) (int, error) {
	*s = append(*s, p...)
	return len(p), nil
}

func hasNoncanonicalNumberSpelling(file *goast.File) bool {
	for node := range goast.Preorder(file) {
		if literal, ok := node.(*goast.BasicLit); ok {
			switch literal.Kind {
			case token.IMAG:
				return true
			case token.INT, token.FLOAT:
				if strings.ContainsAny(literal.Value, "XOBEP") {
					return true
				}
			}
		}
	}
	return false
}

func (symbols *generatedSymbols) addDeclaration(declaration goast.Decl) error {
	switch value := declaration.(type) {
	case *goast.FuncDecl:
		if value.Recv == nil && value.Name.Name != "init" {
			return symbols.add("", value.Name, "function")
		}
	case *goast.GenDecl:
		for _, specification := range value.Specs {
			switch item := specification.(type) {
			case *goast.TypeSpec:
				if err := symbols.addType(item); err != nil {
					return err
				}
			case *goast.ValueSpec:
				for _, name := range item.Names {
					if err := symbols.add("", name, value.Tok.String()); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (symbols *generatedSymbols) addType(specification *goast.TypeSpec) error {
	if specification.Name.Name == "_" {
		return nil
	}
	if err := symbols.add("", specification.Name, "type"); err != nil {
		return err
	}
	typ := specification.Type
	for {
		parentheses, ok := typ.(*goast.ParenExpr)
		if !ok {
			break
		}
		typ = parentheses.X
	}
	if declaration, ok := typ.(*goast.InterfaceType); ok {
		for _, method := range declaration.Methods.List {
			for _, name := range method.Names {
				if err := symbols.add(specification.Name.Name, name, "interface method"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (symbols *generatedSymbols) add(owner string, name *goast.Ident, kind string) error {
	if name.Name == "_" {
		return nil
	}
	key := generatedSymbolKey{owner: owner, name: name.Name}
	if previous, exists := symbols.names[key]; exists {
		display := name.Name
		if owner != "" {
			display = owner + "." + display
		}
		return fmt.Errorf("generated Go symbol %q is declared more than once: %s (%s) and %s (%s)", display,
			symbols.positions.Position(previous.position), previous.kind, symbols.positions.Position(name.Pos()), kind)
	}
	symbols.names[key] = generatedSymbol{position: name.Pos(), kind: kind}
	return nil
}
