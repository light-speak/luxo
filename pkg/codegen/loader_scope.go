package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func collectScopeAwareFunctions(functions map[string]*ast.FnDecl) map[string]bool {
	return collectFunctionRequirements(functions, func(call *ast.CallExpr) bool {
		_, load := directLoadModel(call)
		return load
	})
}

func bodyCallsScopedFunction(body *ast.Block, functions map[string]bool) bool {
	if len(functions) == 0 {
		return false
	}
	found := false
	ast.WalkExprs(body, func(expr ast.Expr) {
		if call, ok := expr.(*ast.CallExpr); ok {
			if name, ok := call.Func.(*ast.Ident); ok && functions[name.Name] {
				found = true
			}
		}
	})
	return found
}

// A block isolates temporary bindings from every user parameter and local.
func writeRequestScope(b *strings.Builder, indent, contextName string) {
	fmt.Fprintf(b, "%s{\n%sscopeCtx, scopeErr := dataloader.NewScope(%s)\n", indent, indent+"\t", contextName)
	fmt.Fprintf(b, "%s\tif scopeErr != nil { return scopeErr }\n", indent)
	fmt.Fprintf(b, "%s\t%s = scopeCtx\n%s}\n", indent, contextName, indent)
}

func generateRelationSelectionCheck(b *strings.Builder, modelName string, relations []Relation) {
	fmt.Fprintf(b, "func has%sSelectedRelations(fields []*selection.Field) bool {\n", modelName)
	b.WriteString("\tfor _, field := range fields {\n\t\tif field.Children == nil { continue }\n\t\tswitch field.Name {\n\t\tcase ")
	for i, relation := range relations {
		if i != 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", relation.FieldName)
	}
	b.WriteString(":\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n\n")
}

// Trace paths are framework value changes. Rebase only when tracing is active;
// ordinary relation traversal preserves the existing request scope unchanged.
func writeTraceScope(b *strings.Builder, indent string) {
	fmt.Fprintf(b, "%sif api.DebugTrace(traceCtx) != nil {\n", indent)
	writeRequestScope(b, indent+"\t", "traceCtx")
	fmt.Fprintf(b, "%s}\n", indent)
}
