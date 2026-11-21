package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// Field helpers are emitted once; writers only make direct, typed calls.
func (g *GeneratorContext) withTransformFunctions(result *semantic.Result, declarations []*ast.ModelDecl, enums map[string]bool) (*GeneratorContext, string) {
	copy := *g
	copy.fieldTransforms = nil
	functions := collectFunctionDeclarations(result)
	targets := make(map[string]string)
	var helpers []*ast.FnDecl
	for _, declaration := range declarations {
		for _, field := range declaration.Fields {
			if field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
				continue
			}
			directive := findDirective(field.Directives, "transform")
			if directive == nil || !(fieldBodyCallsFunction(directive.Body, functions) || fieldBodyPropagatesError(directive.Body)) {
				continue
			}
			name := fmt.Sprintf("_luxoTransform%d", len(helpers))
			if copy.fieldTransforms == nil {
				copy.fieldTransforms = make(map[*ast.FieldDecl]string)
			}
			copy.fieldTransforms[field] = name
			helpers = append(helpers, fieldDirectiveFunction(field, directive, name))
		}
	}
	if len(helpers) == 0 {
		return g, ""
	}
	dependencies := make(map[string]*ast.FnDecl)
	for _, helper := range helpers {
		collectFieldFunctions(helper, functions, dependencies, targets, "_luxoFieldFn")
	}
	var body strings.Builder
	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		copy.compileStaticFunction(&body, dependencies[name], nil, enums, functions, targets, targets[name])
	}
	for _, helper := range helpers {
		copy.compileStaticFunction(&body, helper, nil, enums, functions, targets, helper.Name)
	}
	return &copy, body.String()
}

func fieldBodyPropagatesError(body *ast.Block) bool {
	found := false
	ast.Walk(body, ast.Visitor{
		Stmt: func(statement ast.Stmt) {
			if assignment, ok := statement.(*ast.AssignStmt); ok && assignment.Op != "=" {
				found = true
			}
		},
		Expr: func(expression ast.Expr) { found = found || fieldExpressionMayFail(expression) },
	})
	return found
}

// Keep fallible field bodies on the same typed compiler/error ABI as functions.
func fieldExpressionMayFail(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.UnaryExpr:
		method, _ := checkedIntegerNegationMethod(value)
		return value.Op == "?" || method != ""
	case *ast.BinaryExpr:
		method, _ := checkedIntegerMethod(value.Op, value.Left, value.Right)
		return method != "" || value.Left.GetTypeTag() == "Decimal" && value.Right.GetTypeTag() == "Decimal" && decimalArithmeticMethod(value.Op) != ""
	case *ast.MemberExpr:
		_, unit := durationUnits[value.Field]
		return unit && value.GetTypeTag() == "Duration" && isIntScalar(value.Object)
	case *ast.CallExpr:
		member, ok := value.Func.(*ast.MemberExpr)
		return ok && (member.Field == "repeat" || member.Field == "substring") && member.Object.GetTypeTag() == "String"
	}
	return false
}

func fieldBodyCallsFunction(body *ast.Block, functions map[string]*ast.FnDecl) bool {
	found := false
	ast.WalkExprs(body, func(expression ast.Expr) {
		if call, ok := expression.(*ast.CallExpr); ok {
			if name, ok := call.Func.(*ast.Ident); ok && functions[name.Name] != nil {
				found = true
			}
		}
	})
	return found
}

func collectFieldFunctions(fn *ast.FnDecl, functions, dependencies map[string]*ast.FnDecl, targets map[string]string, prefix string) {
	visit := func(expression ast.Expr) {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			return
		}
		name, ok := call.Func.(*ast.Ident)
		if !ok || functions[name.Name] == nil || dependencies[name.Name] != nil {
			return
		}
		dependency := functions[name.Name]
		if dependency.Body == nil || hasDirective(dependency.Directives, "native") || hasDirective(dependency.Directives, "service") {
			panic(&InternalCompilerError{Detail: "field function requires unavailable runtime dependency " + name.Name})
		}
		dependencies[name.Name] = dependency
		targets[name.Name] = fmt.Sprintf("%s%d%s", prefix, len(name.Name), name.Name)
		collectFieldFunctions(dependency, functions, dependencies, targets, prefix)
	}
	ast.WalkExprs(fn.Body, visit)
	for _, parameter := range fn.Params {
		ast.WalkExpr(parameter.Default, visit)
	}
}

func (g *GeneratorContext) writeFieldTransform(b *strings.Builder, field *ast.FieldDecl, source, indent string) string {
	helper := g.fieldTransforms[field]
	if helper == "" {
		return writeTransformDirectiveAt(b, field, source, indent)
	}
	name := field.Name + "Transformed"
	if !field.Type.Nullable {
		writeFieldTransformCall(b, helper, name, source, indent)
		return name
	}
	fmt.Fprintf(b, "%svar %s %s\n%sif %s != nil {\n", indent, name, resolveGoType(field.Type), indent, source)
	input := source
	output := name + "Value"
	if !field.Type.IsList {
		input = "*" + source
	}
	writeFieldTransformCall(b, helper, output, input, indent+"\t")
	if !field.Type.IsList {
		output = "&" + output
	}
	fmt.Fprintf(b, "%s\t%s = %s\n%s}\n", indent, name, output, indent)
	return name
}

func writeFieldTransformCall(b *strings.Builder, helper, target, input, indent string) {
	fmt.Fprintf(b, "%s%s, err := %s(%s)\n%sif err != nil { return err }\n", indent, target, helper, input, indent)
}

func mergeFieldFunctionImportNeeds(needs *writeJSONImportNeeds, packages generatedImports) {
	needs.strings = needs.strings || packages.has("strings")
	needs.time = needs.time || packages.has("time")
	needs.str = needs.str || packages.has("str")
	needs.uuid = needs.uuid || packages.has("uuid")
	needs.decimal = needs.decimal || packages.has("decimal")
}

func writeFieldFunctionExtraImports(b *strings.Builder, packages generatedImports) {
	writeOptionalStandardImports(b, packages, "strconv", "math", "slices")
	for _, name := range []string{"errors", "convert", "lux"} {
		if packages.has(name) {
			path := "github.com/light-speak/luxo/pkg/lux"
			if name != "lux" {
				path += "/" + name
			}
			fmt.Fprintf(b, "\t%q\n", path)
		}
	}
}

// Checked expressions, calls and Result propagation require a fallible save ABI.
func fallibleSave(field *ast.FieldDecl) bool {
	directive := findDirective(field.Directives, "beforeSave")
	if directive == nil {
		return false
	}
	found := fieldBodyPropagatesError(directive.Body)
	ast.WalkExprs(directive.Body, func(expression ast.Expr) {
		if call, ok := expression.(*ast.CallExpr); ok {
			if _, ok := call.Func.(*ast.Ident); ok {
				found = true
			}
		}
	})
	return found
}

func hasFallibleSave(fields []*ast.FieldDecl) bool {
	for _, field := range fields {
		if fallibleSave(field) || hasDirective(field.Directives, "hash") {
			return true
		}
	}
	return false
}

func saveBodyName(model string, field *ast.FieldDecl) string {
	return savePreparationName(model, field.Name) + "Body"
}

func (g *GeneratorContext) generateSaveFunctions(result *semantic.Result, enums map[string]bool) string {
	var body strings.Builder
	var helpers []*ast.FnDecl
	for _, file := range result.Files {
		for _, model := range file.Models {
			for _, field := range model.Fields {
				generateSavePreparation(&body, model.Name, field)
				generateHashPreparation(&body, model.Name, field)
				if fallibleSave(field) {
					helpers = append(helpers, fieldDirectiveFunction(field, findDirective(field.Directives, "beforeSave"), saveBodyName(model.Name, field)))
				}
			}
		}
	}
	if len(helpers) == 0 {
		return body.String()
	}
	functions := collectFunctionDeclarations(result)
	dependencies := make(map[string]*ast.FnDecl)
	targets := make(map[string]string)
	for _, helper := range helpers {
		collectFieldFunctions(helper, functions, dependencies, targets, "_luxoSaveFn")
	}
	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g.compileStaticFunction(&body, dependencies[name], nil, enums, functions, targets, targets[name])
	}
	for _, helper := range helpers {
		g.compileStaticFunction(&body, helper, nil, enums, functions, targets, helper.Name)
	}
	return body.String()
}

func generateFallibleSavePreparation(b *strings.Builder, model string, field *ast.FieldDecl) {
	typ := resolveGoType(field.Type)
	fmt.Fprintf(b, "func %s(value %s) (%s, error) {\n", savePreparationName(model, field.Name), typ, typ)
	input := "value"
	if field.Type.Nullable {
		b.WriteString("\tif value == nil { return nil, nil }\n")
		if !field.Type.IsList {
			input = "*value"
		}
	}
	if field.Type.Nullable && !field.Type.IsList {
		fmt.Fprintf(b, "\tprepared, err := %s(%s)\n\tif err != nil { return nil, err }\n\treturn &prepared, nil\n}\n\n", saveBodyName(model, field), input)
		return
	}
	fmt.Fprintf(b, "\treturn %s(%s)\n}\n\n", saveBodyName(model, field), input)
}

func writeSaveFunctionImports(b *strings.Builder, packages generatedImports) {
	if packages.has("luxocrypto") {
		b.WriteString("\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
	writeOptionalStandardImports(b, packages, "strings", "strconv", "math", "slices")
	if packages.has("errors") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	}
	writeRuntimeUtilityImports(b, packages)
}

func hashPreparationName(model string, field *ast.FieldDecl) string {
	return "hash" + savePreparationName(model, field.Name)
}

func generateHashPreparation(b *strings.Builder, model string, field *ast.FieldDecl) {
	if field.Type == nil || !hasDirective(field.Directives, "hash") {
		return
	}
	typ := resolveGoType(field.Type)
	fmt.Fprintf(b, "func %s(value %s) (%s,error) {\n", hashPreparationName(model, field), typ, typ)
	if field.Type.Nullable {
		b.WriteString("if value == nil { return nil,nil }\nhashed,err := luxocrypto.HashPassword(*value)\nif err != nil { return nil,err }\nreturn &hashed,nil\n}\n\n")
	} else {
		b.WriteString("return luxocrypto.HashPassword(value)\n}\n\n")
	}
}
