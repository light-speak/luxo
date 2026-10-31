package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"strings"
)

func (c *compiler) aggregateType(model, method, column string) string {
	if method == "count" {
		return "Int"
	}
	kind := "Int"
	if declaration := c.models[model]; declaration != nil {
		for _, field := range declaration.Fields {
			if field.Name == column && field.Type != nil {
				kind = field.Type.Name
				break
			}
		}
	}
	if method == "avg" && kind == "Int" {
		return "Float"
	}
	return kind
}

func (c *compiler) aggregateMethod(model string, link chainLink, column string) string {
	kind := c.aggregateType(model, link.method, column)
	if kind == "Decimal" {
		return link.method + "Decimal"
	}
	if kind == "Float" && link.method != "avg" {
		return link.method + "Float"
	}
	return link.method
}

func aggregateColumn(link chainLink) string {
	if len(link.args) == 1 {
		return extractLambdaField(link.args[0].Value)
	}
	return ""
}

func (c *compiler) aggregateValueType(expr ast.Expr, model string, link chainLink) valType {
	switch expr.GetTypeTag() {
	case "Int", "Float", "Decimal":
		return valType{name: expr.GetTypeTag()}
	default:
		return valType{name: c.aggregateType(model, link.method, aggregateColumn(link))}
	}
}

func (c *compiler) emitMixedAggregateGroup(group awaitAggregateGroup, base string, specs []string) bool {
	mixed := false
	targets := make([]string, len(group.tasks))
	for i, task := range group.tasks {
		kind := c.aggregateType(task.model, task.function, task.column)
		mixed = mixed || kind != "Int"
		targets[i] = "{" + kind + ": &" + task.varName + "}"
	}
	if !mixed {
		return false
	}
	c.write("g.Go(func() error {")
	c.write("\treturn %s.AggregateBatchTo(gctx, []%s.AggregateDestination{%s}, %s)", base, c.dbPackage(), strings.Join(targets, ", "), strings.Join(specs, ", "))
	c.write("})")
	return true
}
