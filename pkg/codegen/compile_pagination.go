package codegen

import (
	"go/token"
	"strconv"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func (g *GeneratorContext) preparePaginationPlans(result *semantic.Result) error {
	g.paginationPlans = nil
	needed := false
	for _, file := range result.Files {
		for _, api := range file.APIs {
			needed = needed || api.Body != nil && hasDirective(api.Directives, "paginate")
		}
	}
	if !needed {
		return nil
	}
	models, _ := collectInferredAPIs(result)
	functions := collectFunctionDeclarations(result)
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body == nil || !hasDirective(api.Directives, "paginate") {
				continue
			}
			plan, err := inferred.PlanPagination(api, models, functions)
			if err != nil {
				return err
			}
			if g.paginationPlans == nil {
				g.paginationPlans = make(map[*ast.ApiDecl]*inferred.PaginationPlan)
			}
			g.paginationPlans[api] = plan
		}
	}
	return nil
}

func (c *compiler) initializePagination(plan *inferred.PaginationPlan) {
	if plan == nil {
		return
	}
	c.pagePlan = plan
	if len(plan.Queries) == 0 {
		return
	}
	c.pageQueries = make(map[ast.Expr]bool, len(plan.Queries))
	c.pageSlots = make(map[ast.Expr]string)
	for _, query := range plan.Queries {
		c.pageQueries[query] = true
	}
	ast.WalkExprs(c.api.Body, func(expr ast.Expr) {
		if plan.Owners[expr] != expr || c.pageSlots[expr] != "" {
			return
		}
		name := "_luxoPageTotal" + strconv.Itoa(len(c.pageSlots))
		c.pageSlots[expr] = name
		c.write("var %s int64", name)
		c.write("_ = %s", name)
	})
}

func (c *compiler) isPagedQuery(expr ast.Expr) bool {
	if c.pagePlan != nil {
		return c.pageQueries[expr]
	}
	return c.paginate && c.isModelQuery(expr) && isCountedPaginationQuery(expr)
}

func (c *compiler) pageTotal(expr ast.Expr) string {
	if c.pagePlan != nil {
		return c.pageSlots[c.pagePlan.Owners[expr]]
	}
	if name, ok := expr.(*ast.Ident); ok {
		return c.paginationTotals[name.Name]
	}
	return ""
}

func (c *compiler) pageBranch(expr ast.Expr) string {
	if c.pagePlan != nil && c.pagePlan.Branches[expr] {
		return c.pageTotal(expr)
	}
	return ""
}

func (c *compiler) queryAssignment(name string, expr ast.Expr, code string) string {
	if !c.isPagedQuery(expr) {
		c.write("%s, err := %s", name, code)
		return ""
	}
	total := c.nextPaginationName("ReadTotal")
	c.write("%s, %s, err := %s", name, total, code)
	return total
}

func (c *compiler) captureQueryTotal(expr ast.Expr, total string) string {
	if total == "" {
		return ""
	}
	if slot := c.pageTotal(expr); slot != "" {
		c.write("%s = %s", slot, total)
		return slot
	}
	return total
}

func (c *compiler) pageBranchValue(result string, expr ast.Expr, value string) string {
	if result == "" {
		return value
	}
	total := c.pageTotal(expr)
	if total == "" {
		name := c.nextPaginationName("BranchRows")
		if isNullLiteral(expr) {
			c.write("var %s %s", name, compiledFunctionGoType(c.api.ReturnType, c.models))
		} else {
			c.write("%s := %s", name, value)
		}
		value = name
		total = c.writeInMemoryPagination(name)
	} else if !token.IsIdentifier(value) {
		// An inline value closure can set its pagination total when it runs.
		value = c.captureExpressionValue(expr, value, "")
	}
	c.write("%s = %s", result, total)
	return value
}
