package codegen

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

type generatedImports uint32

func (imports generatedImports) has(name string) bool { return imports&generatedPackageBit(name) != 0 }

func writeStringUtilityImports(b *strings.Builder, packages generatedImports) {
	for _, name := range []string{"str", "convert"} {
		if packages.has(name) {
			fmt.Fprintf(b, "\t\"github.com/light-speak/luxo/pkg/lux/%s\"\n", name)
		}
	}
}

// writeScalarTypeImports shares value-type imports across emitted signatures.
func writeScalarTypeImports(b *strings.Builder, body string) {
	wanted := generatedPackageBit("json") | generatedPackageBit("time") |
		generatedPackageBit("uuid") | generatedPackageBit("decimal")
	packages := scanGeneratedPackages(body, wanted)
	for _, imp := range []struct{ name, path string }{
		{"json", "encoding/json"}, {"time", "time"},
		{"uuid", "github.com/google/uuid"}, {"decimal", "github.com/shopspring/decimal"},
	} {
		if packages.has(imp.name) {
			fmt.Fprintf(b, "\t%q\n", imp.path)
		}
	}
}

// generatedPackages normally recognizes package-root selectors without allocating
// tokens; ambiguous bindings receive scope resolution. Go literal boundaries come
// from the standard library;
// comments/whitespace preserve the preceding identifier, other tokens clear it.
// This scan is generation-only and retains no state between output files.
func generatedPackages(body string) generatedImports {
	return scanGeneratedPackages(body, 0)
}

func scanGeneratedPackages(body string, stopAfter generatedImports) generatedImports {
	var imports, possibleBindings generatedImports
	identifier := ""
	afterDot := false
	for index := 0; index < len(body); {
		ch := body[index]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			index++
			continue
		}
		if ch == '/' && index+1 < len(body) && (body[index+1] == '/' || body[index+1] == '*') {
			index = skipGeneratedComment(body, index)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quoted, err := strconv.QuotedPrefix(body[index:])
			if err != nil {
				return imports
			} // Invalid output is rejected by the generator's format/parse step.
			index += len(quoted)
			identifier, afterDot = "", false
			continue
		}
		if generatedIdentifierByte(ch) {
			possibleBindings |= generatedPackageBit(identifier)
			start := index
			for index < len(body) && generatedIdentifierByte(body[index]) {
				index++
			}
			identifier = ""
			if !afterDot {
				identifier = body[start:index]
			}
			afterDot = false
			continue
		}
		if ch != '.' {
			possibleBindings |= generatedPackageBit(identifier)
		}
		if ch == '.' {
			imports |= generatedPackageBit(identifier)
			if stopAfter != 0 && imports&stopAfter == stopAfter {
				return imports
			}
		}
		identifier, afterDot = "", ch == '.'
		index++
	}
	if stopAfter == 0 && imports&possibleBindings != 0 {
		return scopedGeneratedPackages(body, imports)
	}
	return imports
}

// Only ambiguous names need lexical scope resolution. Ordinary generated
// bodies retain the allocation-free selector scan; this is a cold codegen path.
func scopedGeneratedPackages(body string, fallback generatedImports) generatedImports {
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", "package generated\n"+body, 0)
	if err != nil {
		file, err = parser.ParseFile(token.NewFileSet(), "generated.go", "package generated\nfunc _() {\n"+body+"\n}", 0)
	}
	if err != nil {
		return fallback // The formal generation boundary rejects invalid Go.
	}
	var imports generatedImports
	goast.Inspect(file, func(node goast.Node) bool {
		selector, ok := node.(*goast.SelectorExpr)
		if ok {
			if root, ok := selector.X.(*goast.Ident); ok && root.Obj == nil {
				imports |= generatedPackageBit(root.Name)
			}
		}
		return true
	})
	return imports
}

func generatedIdentifierByte(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch >= 128
}

func skipGeneratedComment(body string, start int) int {
	terminator := "\n"
	if body[start+1] == '*' {
		terminator = "*/"
	}
	if end := strings.Index(body[start+2:], terminator); end >= 0 {
		return start + 2 + end + len(terminator)
	}
	return len(body)
}

func generatedPackageBit(name string) generatedImports {
	switch name {
	case "strconv":
		return 1 << 0
	case "strings":
		return 1 << 1
	case "luvia":
		return 1 << 2
	case "luxocrypto":
		return 1 << 3
	case "time":
		return 1 << 4
	case "json":
		return 1 << 5
	case "fmt":
		return 1 << 6
	case "regexp":
		return 1 << 7
	case "lux":
		return 1 << 8
	case "errors":
		return 1 << 9
	case "selection":
		return 1 << 10
	case "pg":
		return 1 << 11
	case "uuid":
		return 1 << 12
	case "decimal":
		return 1 << 13
	case "codec":
		return 1 << 14
	case "errgroup":
		return 1 << 15
	case "luxolog":
		return 1 << 16
	case "bytes":
		return 1 << 17
	case "jsonutil":
		return 1 << 18
	case "str":
		return 1 << 19
	case "convert":
		return 1 << 20
	case "context":
		return 1 << 21
	default:
		return 0
	}
}
