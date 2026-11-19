package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
)

// Data has a static concrete type. Null is not an instance of a non-null type.
// Even a statically known result must evaluate the subject and propagate errors.
func (c *compiler) compileTypeTest(expr *ast.BinaryExpr) string {
	target, ok := expr.Right.(*ast.Ident)
	list, _ := expr.Left.(*ast.ListExpr)
	emptyList := list != nil && len(list.Items) == 0
	if !ok || target.GetTypeTag() == "" || expr.Left == nil || expr.Left.GetTypeTag() == "" && !emptyList {
		panic(&InternalCompilerError{Detail: fmt.Sprintf("%s: type test requires analyzed value and type operands", expr.Pos)})
	}
	if isNullLiteral(expr.Left) || emptyList {
		return "false"
	}
	value := c.compileBooleanValue(expr.Left)
	matching := !expr.Left.IsListType() && c.matchesTypeTest(expr.Left.GetTypeTag(), target.GetTypeTag())
	if matching && expr.Left.IsNullable() {
		return "(" + value + " != nil)"
	}
	c.write("_ = %s", value)
	if matching {
		return "true"
	}
	return "false"
}

func (c *compiler) matchesTypeTest(actual, target string) bool {
	if actual == target {
		return true
	}
	model := c.models[actual]
	if model == nil || len(model.Parents) == 0 {
		return false
	}
	return c.modelHasType(actual, target, make(map[string]bool))
}

func (c *compiler) modelHasType(name, target string, seen map[string]bool) bool {
	model := c.models[name]
	if model == nil || seen[name] {
		return false
	}
	seen[name] = true
	for _, parent := range model.Parents {
		if parent == target || c.modelHasType(parent, target, seen) {
			return true
		}
	}
	return false
}
