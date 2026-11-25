package inferred

import (
	"fmt"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// PaginationPlan describes compile-time list lineage, never runtime provenance.
type PaginationPlan struct {
	Queries  []ast.Expr
	Owners   map[ast.Expr]ast.Expr
	Branches map[ast.Expr]bool
}

type pageValue struct {
	owner   ast.Expr
	queries []ast.Expr
	bad     bool
	builder string
	limited bool
	object  *pageObject
	ref     bool
	opaque  ast.Expr
}

type paginationAnalysis struct {
	models    map[string]*ast.ModelDecl
	functions map[string]*ast.FnDecl
	scopes    []map[string]pageValue
	values    map[ast.Expr]pageValue
	branches  map[ast.Expr]bool
	queries   []ast.Expr
	returns   []pageValue
	used      map[ast.Expr]bool
	invalid   map[ast.Expr]token.Position
	active    map[string]bool
	depth     int
}

// PlanPagination rejects database transforms whose final window cannot be
// derived safely. Explicitly constructed lists keep their memory pagination.
func PlanPagination(api *ast.ApiDecl, models map[string]*ast.ModelDecl, functions map[string]*ast.FnDecl) (*PaginationPlan, error) {
	if api == nil || api.Body == nil || !paginationDirective(api) {
		return nil, nil
	}
	p := paginationAnalysis{models: models, functions: functions}
	p.block(api.Body, p.parameters(api.Params), true, true)
	for _, value := range p.returns {
		for _, query := range value.queries {
			if pos, invalid := p.invalid[query]; invalid || value.bad {
				if !invalid {
					pos = query.GetPos()
				}
				return nil, fmt.Errorf("%s: cannot safely push down pagination; move filtering and ordering into the query / 无法安全下推分页；请将过滤和排序写入数据库查询", pos)
			}
			if p.used == nil {
				p.used = make(map[ast.Expr]bool)
			}
			p.used[query] = true
		}
	}
	return p.plan(), nil
}

func paginationDirective(api *ast.ApiDecl) bool {
	for _, directive := range api.Directives {
		if directive.Name == "paginate" {
			return true
		}
	}
	return false
}

func (p *paginationAnalysis) plan() *PaginationPlan {
	if len(p.used) == 0 {
		return &PaginationPlan{}
	}
	plan := &PaginationPlan{Owners: make(map[ast.Expr]ast.Expr), Branches: make(map[ast.Expr]bool)}
	for _, query := range p.queries {
		if p.used[query] {
			plan.Queries = append(plan.Queries, query)
		}
	}
	for expr, value := range p.values {
		for _, query := range value.queries {
			if p.used[query] {
				plan.Owners[expr] = value.owner
				plan.Branches[value.owner] = p.branches[value.owner]
				break
			}
		}
	}
	return plan
}

func (p *paginationAnalysis) lookup(name string) pageValue {
	for index := len(p.scopes) - 1; index >= 0; index-- {
		if value, found := p.scopes[index][name]; found {
			return value
		}
	}
	return pageValue{}
}

func (p *paginationAnalysis) block(body *ast.Block, bindings map[string]pageValue, last, public bool) pageValue {
	if body == nil {
		return pageValue{}
	}
	p.scopes = append(p.scopes, bindings)
	defer func() { p.scopes = p.scopes[:len(p.scopes)-1] }()
	var result pageValue
	for index, statement := range body.Stmts {
		if expr, ok := statement.(*ast.ExprStmt); ok && last && index == len(body.Stmts)-1 {
			result = p.expression(expr.Expr)
			if public {
				p.returns = append(p.returns, result)
			}
			break
		}
		value, returned := p.statement(statement, public)
		if returned {
			return value
		}
	}
	return result
}

func (p *paginationAnalysis) conditionalBlock(body *ast.Block, bindings map[string]pageValue, last, public bool) pageValue {
	p.depth++
	defer func() { p.depth-- }()
	return p.block(body, bindings, last, public)
}

func (p *paginationAnalysis) statement(statement ast.Stmt, public bool) (pageValue, bool) {
	switch value := statement.(type) {
	case *ast.ValStmt:
		p.declaration(value)
	case *ast.AssignStmt:
		p.assignment(value)
	case *ast.ReturnStmt:
		result := p.expression(value.Value)
		if public {
			p.returns = append(p.returns, result)
		}
		return result, true
	case *ast.IfStmt:
		p.consumeInput(value.Condition)
		p.conditionalBlock(value.Then, nil, false, public)
	case *ast.ForStmt:
		p.consumeInput(value.Collection)
		p.conditionalBlock(value.Body, map[string]pageValue{value.VarName: p.iterationItem(value.Collection)}, false, public)
	case *ast.ExprStmt:
		if member, ok := value.Expr.(*ast.MemberExpr); ok && builtin.IsDebugProperty(member) {
			p.expression(member)
		} else {
			p.consumeInput(value.Expr)
		}
	case *ast.ThrowStmt:
		p.expression(value.Error)
	case *ast.EmitStmt:
		for _, argument := range value.Args {
			p.consumeInput(argument.Value)
		}
	}
	return pageValue{}, false
}

func (p *paginationAnalysis) declaration(statement *ast.ValStmt) {
	bindings := p.scopes[len(p.scopes)-1]
	if bindings == nil {
		bindings = make(map[string]pageValue)
		p.scopes[len(p.scopes)-1] = bindings
	}
	if await, ok := statement.Value.(*ast.AwaitExpr); ok && len(statement.Names) != 0 {
		p.destructureAwait(bindings, statement, await)
		return
	}
	bindings[statement.Name] = p.bindObject(p.expression(statement.Value), statement.Type)
}

func (p *paginationAnalysis) destructureAwait(bindings map[string]pageValue, statement *ast.ValStmt, await *ast.AwaitExpr) {
	// Initializers must not see any of the destination bindings.
	var inline [4]pageValue
	values := inline[:0]
	p.visitAwaitResults(await.Body, func(expr ast.Expr) {
		value := p.consumeInput(expr)
		if len(values) < len(statement.Names) {
			values = append(values, p.awaitResult(value))
		}
	})
	if len(values) == 1 {
		value, expected := values[0], statement.Type
		for _, name := range statement.Names {
			if name != "_" {
				value = p.bindObject(value, expected)
				bindings[name] = value
				expected = nil
			}
		}
		return
	}
	for index, value := range values {
		if name := statement.Names[index]; name != "_" {
			bindings[name] = value
		}
	}
}

func (p *paginationAnalysis) assignment(statement *ast.AssignStmt) {
	member, field := statement.Target.(*ast.MemberExpr)
	var base pageValue
	if field {
		base = p.expression(member.Object)
	}
	value := p.expression(statement.Value)
	if field && p.assignField(base, member, value, statement) {
		return
	}
	name, variable := statement.Target.(*ast.Ident)
	if !variable {
		p.consume(value, statement.Value)
		p.consume(p.expression(statement.Target), statement.Target)
		return
	}
	for index := len(p.scopes) - 1; index >= 0; index-- {
		if previous, found := p.scopes[index][name.Name]; found {
			if p.depth != 0 {
				p.consume(previous, statement.Target)
				p.consume(value, statement.Value)
				value = mergePageValues(previous, value)
				value.bad = true
			}
			expected := &ast.TypeRef{Name: name.GetTypeTag(), IsList: name.IsListType(), Nullable: name.IsNullable()}
			p.scopes[index][name.Name] = p.bindObject(value, expected)
			return
		}
	}
}

func (p *paginationAnalysis) consume(value pageValue, expression ast.Expr) {
	for _, query := range pageQueries(value) {
		p.invalidateQuery(query, expression.GetPos())
	}
}

func mergePageValues(left, right pageValue) pageValue {
	left, right = flattenPageObject(left), flattenPageObject(right)
	value := pageValue{owner: left.owner, builder: left.builder, bad: left.bad || right.bad, limited: left.limited || right.limited, opaque: left.opaque}
	if value.opaque == nil {
		value.opaque = right.opaque
	}
	if value.owner == nil {
		value.owner = right.owner
	}
	if value.builder == "" {
		value.builder = right.builder
	}
	if len(left.queries) == 0 {
		value.queries = right.queries
		return value
	}
	if len(right.queries) == 0 {
		value.queries = left.queries
		return value
	}
	value.queries = slices.Clone(left.queries)
	for _, query := range right.queries {
		if !slices.Contains(value.queries, query) {
			value.queries = append(value.queries, query)
		}
	}
	return value
}

func (p *paginationAnalysis) builder(expr ast.Expr) pageValue {
	switch value := expr.(type) {
	case *ast.Ident:
		binding := p.lookup(value.Name)
		if binding.builder != "" || len(binding.queries) != 0 {
			return binding
		}
		if p.models[value.Name] != nil {
			return pageValue{builder: value.Name}
		}
		name := strings.TrimSuffix(value.GetTypeTag(), "QueryBuilder")
		if strings.HasSuffix(value.GetTypeTag(), "QueryBuilder") && p.models[name] != nil {
			return pageValue{builder: name}
		}
	case *ast.CallExpr:
		if member, ok := value.Func.(*ast.MemberExpr); ok {
			base := p.builder(member.Object)
			base.limited = base.limited || member.Field == "limit" || member.Field == "offset"
			return base
		}
	}
	return pageValue{}
}
