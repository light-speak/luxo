package codegen

import (
	"fmt"
	"strings"

	luxoast "github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
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
	models, _ := collectInferredAPIs(result)
	hasNativeStreams := resultHasNativeStreams(result)
	if len(apis) == 0 && !hasNativeStreams {
		return nil
	}

	var b strings.Builder
	b.WriteString("// NativeResolver is the interface for @native API implementations.\n")
	b.WriteString("// Implement this interface in your resolver package.\n")
	b.WriteString("type NativeResolver interface {\n")
	if hasNativeStreams {
		b.WriteString("\tStreamResolver\n")
	}
	seen := make(map[string]int, len(apis))
	for index, api := range apis {
		if previous, exists := seen[api.Name]; exists && sameNativeSignature(apis[previous], api) {
			continue
		}
		seen[api.Name] = index
		bindings := parameterBindings(api.Params, nil)
		fmt.Fprintf(&b, "\t%s(ctx context.Context", str.Capitalize(api.Name))
		for _, p := range api.Params {
			goType := nativeGoType(p.Type, models)
			if p.Spread {
				fmt.Fprintf(&b, ", %s ...%s", bindings.name(p.Name), goType)
				continue
			}
			fmt.Fprintf(&b, ", %s %s", bindings.name(p.Name), goType)
		}
		returnType := unwrapResultType(api.ReturnType)
		if returnType == nil {
			b.WriteString(") error\n")
		} else {
			fmt.Fprintf(&b, ") (%s, error)\n", nativeGoType(returnType, models))
		}
	}
	b.WriteString("}\n")

	body := b.String()
	var out strings.Builder
	writeHeader(&out, packageName, "native.gen.go")
	writeNativeImports(&out, body)
	out.WriteString(body)
	return []byte(out.String())
}

// A same-named API and native function may share one exact implementation contract.
func sameNativeSignature(left, right nativeAPI) bool {
	if len(left.Params) != len(right.Params) {
		return false
	}
	for index, param := range left.Params {
		other := right.Params[index]
		if param.Spread != other.Spread || !sameTypeRef(param.Type, other.Type) {
			return false
		}
	}
	a, b := unwrapResultType(left.ReturnType), unwrapResultType(right.ReturnType)
	return a == b || sameTypeRef(a, b)
}

// Derive imports from emitted signatures, including nullable/list/Result wrappers.
func writeNativeImports(b *strings.Builder, body string) {
	if !strings.Contains(body, "context.Context") {
		return
	}
	b.WriteString("import (\n\t\"context\"\n")
	writeScalarTypeImports(b, body)
	b.WriteString(")\n\n")
}

func unwrapResultType(ref *luxoast.TypeRef) *luxoast.TypeRef {
	if ref == nil {
		return nil
	}
	if ref.Name == "Result" && len(ref.TypeArgs) == 1 {
		return ref.TypeArgs[0]
	}
	return ref
}

// collectNativeAPIs finds all @native API and fn declarations.
func collectNativeAPIs(result *semantic.Result) []nativeAPI {
	var apis []nativeAPI
	for _, file := range result.Files {
		for _, a := range file.APIs {
			if hasDirective(a.Directives, "native") && !hasDirective(a.Directives, "stream") {
				apis = append(apis, nativeAPI{
					Name:       a.Name,
					Params:     a.Params,
					ReturnType: a.ReturnType,
				})
			}
		}
		for _, fn := range file.Functions {
			if hasDirective(fn.Directives, "native") {
				apis = append(apis, nativeAPI{
					Name:       fn.Name,
					Params:     fn.Params,
					ReturnType: fn.ReturnType,
				})
			}
		}
	}
	return apis
}

func resultHasNativeStreams(result *semantic.Result) bool {
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "native") && hasDirective(api.Directives, "stream") {
				return true
			}
		}
	}
	return false
}

// Native model lists use the same reference representation as compiled functions.
func nativeGoType(ref *luxoast.TypeRef, models map[string]*luxoast.ModelDecl) string {
	if ref != nil && ref.IsList && models[ref.Name] != nil {
		return compiledFunctionGoType(ref, models)
	}
	return resolveGoType(ref)
}
