package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func conditionHasCheckedArithmetic(expression ast.Expr) bool {
	found := false
	walkExpr(expression, func(value ast.Expr) {
		found = found || fieldExpressionMayFail(value)
	})
	return found
}

// Reuse ordinary typed lowering and its short-circuit/error boundaries. Only
// condition leaves differ: claims, event fields and prepared parameters.
func compileCheckedCondition(expression ast.Expr, leaf func(ast.Expr) (string, bool), identityReader string) string {
	return compileConditionWithCompiler(&compiler{}, expression, leaf, identityReader)
}

func compileConditionWithCompiler(c *compiler, expression ast.Expr, leaf func(ast.Expr) (string, bool), identityReader string) string {
	bindings := make(map[ast.Expr]string)
	walkExpr(expression, func(value ast.Expr) {
		switch value.(type) {
		case *ast.Ident, *ast.MemberExpr:
			if code, ok := leaf(value); ok {
				if value.GetTypeTag() == "Duration" && !value.IsListType() && !value.IsNullable() {
					code = "time.Duration(" + code + ")"
				}
				bindings[value] = code
			}
		}
	})
	var body strings.Builder
	c.b, c.indent, c.inFunction = &body, "\t", true
	c.functionResult, c.expressionBindings = &ast.TypeRef{Name: "Boolean"}, bindings
	if identityReader != "" {
		c.write("_identity := %s", identityReader)
	}
	value := c.compileExpr(expression)
	if identityReader != "" {
		value = "_identity.Allowed(" + value + ")"
	}
	c.write("return %s, nil", value)
	return "func() (_value bool, _err error) {\n" + body.String() + "}()"
}

func compileCheckedVisibleCondition(expression ast.Expr, enums map[string]bool, reader string) string {
	return compileVisibleWithCompiler(&compiler{}, expression, enums, reader)
}

func compileVisibleWithCompiler(c *compiler, expression ast.Expr, enums map[string]bool, reader string) string {
	identity := "buf.Identity"
	if reader != "" {
		identity = "_identity"
	}
	return compileConditionWithCompiler(c, expression, func(value ast.Expr) (string, bool) {
		code := compileVisibleCondition(value, enums, identity)
		return code, code != ""
	}, reader)
}

func writeVisibleConditionAt(b *strings.Builder, field *ast.FieldDecl, enums map[string]bool, indent string) string {
	condition := compileVisibleDirectiveExprWithEnums(field, enums)
	fallible := condition != "" && conditionHasCheckedArithmetic(findDirective(field.Directives, "visible").SingleExpression())
	return writeVisibleConditionPlanAt(b, visibleConditionPlan{code: condition, fallible: fallible}, indent)
}

func writeVisibleConditionPlanAt(b *strings.Builder, plan visibleConditionPlan, indent string) string {
	if !plan.fallible {
		return plan.code
	}
	b.WriteString(indent + "_visible, _visibleErr := " + plan.code + "\n")
	b.WriteString(indent + "if _visibleErr != nil { return _visibleErr }\n")
	return "_visible"
}

// Typed visibility plans belong to this generated file, never to a shared generator.
// Copy configuration by value; own one compiler context only when a typed condition needs it.
type writeJSONContext struct {
	GeneratorContext
	enums          map[string]bool
	typedVisible   map[*ast.FieldDecl]visibleConditionPlan
	typedGenerator *GeneratorContext
}

type visibleConditionPlan struct {
	code     string
	fallible bool
}

func (w *writeJSONContext) prepareVisibleCondition(field *ast.FieldDecl, directive *ast.Directive, usesIdentity bool) visibleConditionPlan {
	if plan, ok := w.typedVisible[field]; ok {
		return plan
	}
	reader := ""
	if usesIdentity {
		reader = "api.NewIdentityReader(buf.Identity)"
		if directive.AuthSubject != "" {
			reader = fmt.Sprintf("api.NewIdentityReaderForSubject(buf.Identity, %q)", directive.AuthSubject)
		}
	}
	if w.typedGenerator == nil {
		generation := w.GeneratorContext
		w.typedGenerator = &generation
	}
	c := &compiler{generator: w.typedGenerator, models: w.modelDeclarations, enums: w.enums}
	plan := visibleConditionPlan{code: compileVisibleWithCompiler(c, directive.SingleExpression(), w.enums, reader), fallible: true}
	if w.typedVisible == nil {
		w.typedVisible = make(map[*ast.FieldDecl]visibleConditionPlan)
	}
	w.typedVisible[field] = plan
	return plan
}

func (w *writeJSONContext) writeVisibleConditionAt(b *strings.Builder, field *ast.FieldDecl, enums map[string]bool, indent string) string {
	if w.typedVisible != nil {
		if plan, ok := w.typedVisible[field]; ok {
			return writeVisibleConditionPlanAt(b, plan, indent)
		}
	}
	return writeVisibleConditionAt(b, field, enums, indent)
}
