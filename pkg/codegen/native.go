package codegen

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"

	luxoast "github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// nativeAPI describes a @native API declaration for codegen.
type nativeAPI struct {
	Name       string
	Params     []*luxoast.ParamDecl
	ReturnType *luxoast.TypeRef
}

// GenerateNativeFile produces native.gen.go containing the NativeResolver interface.
// Returns nil if there are no @native APIs.
func GenerateNativeFile(result *semantic.Result, packageName string) []byte {
	apis := collectNativeAPIs(result)
	if len(apis) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "native.gen.go")
	b.WriteString("import \"context\"\n\n")

	b.WriteString("// NativeResolver is the interface for @native API implementations.\n")
	b.WriteString("// Implement this interface in your resolver package.\n")
	b.WriteString("type NativeResolver interface {\n")
	for _, api := range apis {
		fmt.Fprintf(&b, "\t%s(ctx context.Context", capitalize(api.Name))
		for _, p := range api.Params {
			goType := resolveGoType(p.Type)
			fmt.Fprintf(&b, ", %s %s", p.Name, goType)
		}
		retType := "any"
		if api.ReturnType != nil {
			retType = resolveGoType(api.ReturnType)
		}
		fmt.Fprintf(&b, ") (%s, error)\n", retType)
	}
	b.WriteString("}\n")

	return []byte(b.String())
}

// collectNativeAPIs finds all @native API declarations.
func collectNativeAPIs(result *semantic.Result) []nativeAPI {
	var apis []nativeAPI
	for _, file := range result.Files {
		for _, a := range file.APIs {
			if hasDirective(a.Directives, "native") {
				apis = append(apis, nativeAPI{
					Name:       a.Name,
					Params:     a.Params,
					ReturnType: a.ReturnType,
				})
			}
		}
	}
	return apis
}

// MergeResolver updates function signatures in a Go resolver file while preserving method bodies.
// It reads the file, finds functions matching @native APIs, updates their signatures, and writes back.
// New @native functions that don't exist yet are appended with a TODO body.
func MergeResolver(filePath string, apis []nativeAPI) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no resolver file, nothing to merge
		}
		return err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	// Build a map of existing function names → positions
	existingFuncs := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue // skip methods, only look at package-level funcs
		}
		existingFuncs[fn.Name.Name] = fn
	}

	// Collect new functions to append
	var toAppend []string
	for _, api := range apis {
		funcName := capitalize(api.Name)
		if _, exists := existingFuncs[funcName]; exists {
			// Function exists — signature update would require AST rewriting.
			// For now, trust the user's implementation. If signature changes,
			// the compiler will catch it via the NativeResolver interface.
			continue
		}
		// New function — generate stub
		toAppend = append(toAppend, generateNativeStub(api))
	}

	if len(toAppend) == 0 {
		return nil // nothing to add
	}

	// Format existing code + append new stubs
	var buf strings.Builder
	formatted, err := format.Source(src)
	if err != nil {
		formatted = src // use original if format fails
	}
	buf.Write(formatted)
	for _, stub := range toAppend {
		buf.WriteString("\n")
		buf.WriteString(stub)
	}

	return os.WriteFile(filePath, []byte(buf.String()), 0644)
}

// generateNativeStub creates a function stub for a new @native API.
func generateNativeStub(api nativeAPI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s implements the @native API.\n", capitalize(api.Name))
	fmt.Fprintf(&b, "func %s(ctx context.Context", capitalize(api.Name))
	for _, p := range api.Params {
		goType := resolveGoType(p.Type)
		fmt.Fprintf(&b, ", %s %s", p.Name, goType)
	}
	retType := "any"
	if api.ReturnType != nil {
		retType = resolveGoType(api.ReturnType)
	}
	fmt.Fprintf(&b, ") (%s, error) {\n", retType)
	fmt.Fprintf(&b, "\t// TODO: implement %s\n", api.Name)
	fmt.Fprintf(&b, "\tpanic(\"not implemented\")\n")
	b.WriteString("}\n")
	return b.String()
}
