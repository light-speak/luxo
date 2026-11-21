package codegen

import (
	"encoding/hex"
	goast "go/ast"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

const goUnicodeIdentifierPrefix = "LuxoIdent_"

// Non-ASCII leading identifiers are exported by the static spelling map.
func normalizedGoNameIsExported(name string) bool {
	return name != "" && (name[0] >= utf8.RuneSelf || name[0] >= 'A' && name[0] <= 'Z')
}

func formatGeneratedWithIdentifiers(source []byte, exportUnicode bool) ([]byte, error) {
	if !exportUnicode {
		return formatGeneratedChecked(source)
	}
	files := map[string][]byte{"symbols.gen.go": source}
	if err := prepareGeneratedSymbols(files, true); err != nil {
		return nil, err
	}
	return files["symbols.gen.go"], nil
}

// The reserved output namespace also escapes literal source spellings. This
// injective mapping is shared by declarations and references in every module.
func unicodeGoIdentifier(name string) string {
	if strings.HasPrefix(name, goUnicodeIdentifierPrefix) {
		return goUnicodeIdentifierPrefix + hex.EncodeToString([]byte(name))
	}
	if name == "" || name[0] < utf8.RuneSelf {
		return name
	}
	first, _ := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(first) {
		return name
	}
	return goUnicodeIdentifierPrefix + hex.EncodeToString([]byte(name))
}

func exportGeneratedUnicodeIdentifiers(file *goast.File) {
	goast.Inspect(file, func(node goast.Node) bool {
		if identifier, ok := node.(*goast.Ident); ok {
			identifier.Name = unicodeGoIdentifier(identifier.Name)
		}
		return true
	})
}

func needsUnicodeGoName(name string) bool {
	return name != "" && (name[0] >= utf8.RuneSelf ||
		(name[0] == 'L' || name[0] == 'l') && (strings.HasPrefix(name, goUnicodeIdentifierPrefix) || strings.HasPrefix(name, "luxoIdent_")))
}

func unicodeGoFields(fields []*ast.FieldDecl) bool {
	for _, field := range fields {
		if needsUnicodeGoName(field.Name) {
			return true
		}
	}
	return false
}

func needsUnicodeGoExports(result *semantic.Result) bool {
	for name, resolved := range result.Types {
		if needsUnicodeGoName(name) {
			return true
		}
		for field := range resolved.Fields {
			if needsUnicodeGoName(field) {
				return true
			}
		}
	}
	for _, file := range result.Files {
		if len(result.Types) == 0 && unicodeGoDeclarations(file) {
			return true
		}
		for _, declaration := range file.Events {
			if needsUnicodeGoName(declaration.Name) || unicodeGoParams(declaration.Params) {
				return true
			}
		}
		for _, declaration := range file.Functions {
			if needsUnicodeGoName(declaration.Name) {
				return true
			}
		}
		for _, declaration := range file.APIs {
			if needsUnicodeGoName(declaration.Name) || unicodeGoParams(declaration.Params) {
				return true
			}
		}
	}
	return false
}

func unicodeGoDeclarations(file *ast.File) bool {
	for _, declaration := range file.Types {
		if needsUnicodeGoName(declaration.Name) || unicodeGoFields(declaration.Fields) {
			return true
		}
	}
	for _, declaration := range file.Models {
		if needsUnicodeGoName(declaration.Name) || unicodeGoFields(declaration.Fields) {
			return true
		}
	}
	for _, declaration := range file.Enums {
		if needsUnicodeGoName(declaration.Name) {
			return true
		}
	}
	for _, declaration := range file.Sealeds {
		if needsUnicodeGoName(declaration.Name) {
			return true
		}
	}
	return false
}

func (g *GeneratorContext) needsUnicodeGoExports(result *semantic.Result) bool {
	if needsUnicodeGoExports(result) {
		return true
	}
	if g.events != nil {
		for _, declaration := range g.events.Events {
			if unicodeGoParams(declaration.Params) {
				return true
			}
		}
	}
	return false
}

func unicodeGoParams(params []*ast.ParamDecl) bool {
	for _, parameter := range params {
		if needsUnicodeGoName(parameter.Name) {
			return true
		}
	}
	return false
}
