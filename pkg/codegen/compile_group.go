package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
)

func (c *compiler) compileTypedGroup(plan *ast.GroupProjection) string {
	var b strings.Builder
	chain := flattenChain(plan.Input)
	if root, ok := chain[0].expr.(*ast.Ident); ok && root.Name == plan.Model {
		links := chain[1:]
		if len(links) == 0 || links[0].method != "where" {
			links = append([]chainLink{{method: "where"}}, links...)
		}
		b.WriteString(c.compileModelChain(plan.Model, links))
	} else {
		b.WriteString(c.compileExpr(plan.Input))
	}
	b.WriteString(".GroupBy(ctx, []string{")
	for _, key := range plan.Keys {
		fmt.Fprintf(&b, "%q,", str.ToSnakeCase(key))
	}
	b.WriteString("}, []lux.GroupAgg{")
	for _, column := range plan.Columns {
		fmt.Fprintf(&b, "{Fn:%q, Col:%q, Alias:%q},", strings.ToUpper(column.Aggregate), str.ToSnakeCase(column.Source), str.ToSnakeCase(column.Field))
	}
	b.WriteString("}, ")
	c.writeGroupScanner(&b, plan)
	for _, condition := range plan.Having {
		var arguments []string
		sql := c.groupHavingSQL(condition, &arguments)
		fmt.Fprintf(&b, ", lux.RawCondition(%q", sql)
		for _, argument := range arguments {
			fmt.Fprintf(&b, ", %s", argument)
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return b.String()
}

// Only static schema identifiers and validated operators enter SQL text.
// Values remain typed Go expressions passed as database bind parameters.
func (c *compiler) groupHavingSQL(condition *ast.GroupCondition, arguments *[]string) string {
	if condition.Operator == "&&" || condition.Operator == "||" {
		operator := " AND "
		if condition.Operator == "||" {
			operator = " OR "
		}
		left := c.groupHavingSQL(condition.Left, arguments)
		right := c.groupHavingSQL(condition.Right, arguments)
		return "(" + left + operator + right + ")"
	}
	column := condition.Column
	expression := (lux.GroupAgg{Fn: strings.ToUpper(column.Aggregate), Col: str.ToSnakeCase(column.Source)}).SQLExpression()
	operator := condition.Operator
	if literal, ok := condition.Value.(*ast.Literal); ok && literal.Kind == token.Null {
		if operator == "==" {
			return expression + " IS NULL"
		}
		return expression + " IS NOT NULL"
	}
	if operator == "==" {
		operator = "="
	}
	value := c.compileExpr(condition.Value)
	if c.enums[column.Type] {
		value = "string(" + value + ")"
	} else {
		value = resolveGoType(&ast.TypeRef{Name: column.Type}) + "(" + value + ")"
	}
	*arguments = append(*arguments, value)
	return fmt.Sprintf("%s %s $%d", expression, operator, len(*arguments))
}

func (c *compiler) writeGroupScanner(b *strings.Builder, plan *ast.GroupProjection) {
	fmt.Fprintf(b, "func(rows pg.Rows) (%s, error) {\nvar result %s\nvar values struct {\n", plan.Result, plan.Result)
	for _, column := range plan.Columns {
		fmt.Fprintf(b, "%s pg.%s\n", str.Capitalize(column.Field), groupScanType(column.Type, c.enums[column.Type]))
	}
	b.WriteString("}\nif err := rows.Scan(")
	for _, column := range plan.Columns {
		fmt.Fprintf(b, "&values.%s,", str.Capitalize(column.Field))
	}
	b.WriteString("); err != nil { return result, err }\n")
	for _, column := range plan.Columns {
		c.writeGroupScanAssignment(b, column)
	}
	b.WriteString("return result, nil\n}")
}

func groupScanType(name string, enum bool) string {
	if enum {
		return "TextValue"
	}
	switch name {
	case "Int":
		return "Int64Value"
	case "Float":
		return "Float64Value"
	case "String":
		return "TextValue"
	case "Boolean":
		return "BoolValue"
	case "DateTime":
		return "TimeValue"
	case "Duration":
		return "DurationValue"
	case "UUID":
		return "UUIDValue"
	case "Decimal":
		return "DecimalValue"
	}
	return "" // Semantic validation excludes non-scalar projections.
}

func (c *compiler) writeGroupScanAssignment(b *strings.Builder, column ast.GroupColumn) {
	field := str.Capitalize(column.Field)
	fmt.Fprintf(b, "if values.%s.Valid {\n", field)
	value, checked := groupScanExpression(column.Type, "values."+field, c.enums[column.Type])
	if checked {
		fmt.Fprintf(b, "value, err := %s\nif err != nil { return result, err }\n", value)
	} else {
		fmt.Fprintf(b, "value := %s\n", value)
	}
	if c.enums[column.Type] {
		b.WriteString("if !value.IsValid() { return result, codec.ErrInvalidEnum }\n")
	}
	pointer := ""
	if column.Nullable {
		pointer = "&"
	}
	fmt.Fprintf(b, "result.%s = %svalue\n}", field, pointer)
	if !column.Nullable {
		fmt.Fprintf(b, " else { return result, fmt.Errorf(%q) }", "pg: null grouped result for "+column.Field)
	}
	b.WriteByte('\n')
}

func groupScanExpression(name, source string, enum bool) (string, bool) {
	if enum {
		return name + "(" + source + ".String)", false
	}
	switch name {
	case "Int":
		return source + ".Int64", false
	case "Float":
		return source + ".Float64", false
	case "String":
		return source + ".String", false
	case "Boolean":
		return source + ".Bool", false
	case "UUID":
		return "uuid.UUID(" + source + ".Bytes)", false
	case "DateTime", "Duration", "Decimal":
		return "pg." + name + "FromValue(" + source + ")", true
	}
	return "", false // Semantic validation excludes non-scalar projections.
}
