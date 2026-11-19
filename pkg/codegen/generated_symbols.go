package codegen

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"slices"
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

// Go 1.27.1 preserves parsed-template grammar across identifier-only renames.
// Comment/semicolon gating and list comma rules keep the renamed output parseable.
// Retain formatting errors for inputs outside that normal producer contract.
func prepareGeneratedSymbols(files map[string][]byte, exportUnicode bool) error {
	symbols := generatedSymbols{positions: token.NewFileSet(), names: make(map[generatedSymbolKey]generatedSymbol)}
	mode := parser.SkipObjectResolution
	if exportUnicode {
		mode |= parser.ParseComments
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		file, err := parser.ParseFile(symbols.positions, name, files[name], mode)
		if err != nil {
			return fmt.Errorf("inspect generated Go symbols in %s: %w", name, err)
		}
		if exportUnicode && exportGeneratedUnicodeIdentifiers(file) {
			var output bytes.Buffer
			if err := format.Node(&output, symbols.positions, file); err != nil {
				return fmt.Errorf("format generated Go identifiers in %s: %w", name, err)
			}
			files[name] = output.Bytes()
		}
		for _, declaration := range file.Decls {
			if err := symbols.addDeclaration(declaration); err != nil {
				return err
			}
		}
	}
	return nil
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
