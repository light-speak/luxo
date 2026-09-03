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
	hasNativeStreams := resultHasNativeStreams(result)
	if len(apis) == 0 && !hasNativeStreams {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "native.gen.go")
	// Scan for time import need (DateTime params/return)
	needsTime := false
	for _, api := range apis {
		for _, p := range api.Params {
			if p.Type != nil && p.Type.Name == "DateTime" {
				needsTime = true
			}
		}
		if api.ReturnType != nil && api.ReturnType.Name == "DateTime" {
			needsTime = true
		}
	}
	if needsTime {
		b.WriteString("import (\n\t\"context\"\n\t\"time\"\n)\n\n")
	} else if len(apis) > 0 {
		b.WriteString("import \"context\"\n\n")
	}

	b.WriteString("// NativeResolver is the interface for @native API implementations.\n")
	b.WriteString("// Implement this interface in your resolver package.\n")
	b.WriteString("type NativeResolver interface {\n")
	if hasNativeStreams {
		b.WriteString("\tStreamResolver\n")
	}
	for _, api := range apis {
		fmt.Fprintf(&b, "\t%s(ctx context.Context", str.Capitalize(api.Name))
		for _, p := range api.Params {
			goType := resolveGoType(p.Type)
			fmt.Fprintf(&b, ", %s %s", p.Name, goType)
		}
		if api.ReturnType == nil {
			b.WriteString(") error\n")
		} else {
			fmt.Fprintf(&b, ") (%s, error)\n", resolveGoType(api.ReturnType))
		}
	}
	b.WriteString("}\n")

	return []byte(b.String())
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
