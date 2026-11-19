package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// A defined query type preserves the adapter layout without promoting raw writes.
func (g *GeneratorContext) generateQueryFacade(b *strings.Builder, name string, fields []*ast.FieldDecl, writable bool, directives ...[]*ast.Directive) {
	fmt.Fprintf(b, "type %sQuery pg.Query[%s]\n\n", name, name)
	for _, method := range []struct{ name, params, args string }{
		{"Where", "values ...lux.Condition", "values..."},
		{"Select", "values ...string", "values..."},
		{"OrderBy", "values ...string", "values..."},
		{"Limit", "value int", "value"}, {"Offset", "value int", "value"},
	} {
		fmt.Fprintf(b, "func (q *%sQuery) %s(%s) *%sQuery { (*pg.Query[%s])(q).%s(%s); return q }\n", name, method.name, method.params, name, name, method.name, method.args)
	}
	generateQueryReadMethods(b, name, fields)
	if writable {
		for _, method := range []string{"Delete", "SoftDelete"} {
			fmt.Fprintf(b, "func (q *%sQuery) %s(ctx context.Context) (int64,error) { return (*pg.Query[%s])(q).%s(ctx) }\n", name, method, name, method)
		}
		g.generatePreparedQueryUpdate(b, name, fields, directives...)
	}
}

func generateQueryReadMethods(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	for _, method := range []struct{ name, result string }{
		{"All", "([]*" + name + ",error)"}, {"AllWithCount", "([]*" + name + ",int64,error)"},
		{"First", "(*" + name + ",error)"}, {"FirstRequired", "(*" + name + ",error)"},
		{"Count", "(int64,error)"}, {"Exists", "(bool,error)"},
	} {
		fmt.Fprintf(b, "func (q *%sQuery) %s(ctx context.Context) %s { return (*pg.Query[%s])(q).%s(ctx) }\n", name, method.name, method.result, name, method.name)
	}
	for _, method := range []string{"Sum", "Min", "Max", "Avg", "SumFloat", "MinFloat", "MaxFloat"} {
		result := "int64"
		if method == "Avg" || strings.HasSuffix(method, "Float") {
			result = "float64"
		}
		generateQueryAggregateMethod(b, name, method, result)
	}
	for _, field := range fields {
		if field.Type != nil && field.Type.Name == "Decimal" {
			for _, method := range []string{"SumDecimal", "AvgDecimal", "MinDecimal", "MaxDecimal"} {
				generateQueryAggregateMethod(b, name, method, "decimal.Decimal")
			}
			break
		}
	}
	fmt.Fprintf(b, "func (q *%sQuery) AggregateBatch(ctx context.Context, specs ...lux.AggregateSpec) ([]int64,error) { return (*pg.Query[%s])(q).AggregateBatch(ctx,specs...) }\n", name, name)
	fmt.Fprintf(b, "func (q *%sQuery) AggregateBatchTo(ctx context.Context, targets []pg.AggregateDestination, specs ...lux.AggregateSpec) error { return (*pg.Query[%s])(q).AggregateBatchTo(ctx,targets,specs...) }\n", name, name)
	result := "R"
	if name == result {
		result = "_GroupResult"
	}
	fmt.Fprintf(b, "func (q *%[1]sQuery) GroupBy[%[2]s any](ctx context.Context, groups []string, columns []lux.GroupAgg, scan func(pg.Rows)(%[2]s,error), having ...lux.Condition) ([]%[2]s,error) { return (*pg.Query[%[1]s])(q).GroupBy(ctx,groups,columns,scan,having...) }\n\n", name, result)
}

func generateQueryAggregateMethod(b *strings.Builder, name, method, result string) {
	fmt.Fprintf(b, "func (q *%sQuery) %s(ctx context.Context, column string) (%s,error) { return (*pg.Query[%s])(q).%s(ctx,column) }\n", name, method, result, name, method)
}

// Bulk updates reuse the existing typed setter state and preparation rules.
func generateQueryUpdateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl, directives []*ast.Directive) {
	fmt.Fprintf(b, "type %sQueryUpdateBuilder struct { state %sUpdateBuilder; query *%sQuery }\n", name, name, name)
	fmt.Fprintf(b, "func (q *%sQuery) Update() *%sQueryUpdateBuilder { return &%sQueryUpdateBuilder{query:q} }\n", name, name, name)
	primary := primaryKeyField(&ast.ModelDecl{Name: name, Fields: fields})
	setters := mutationSetterFields(fields, primary, true, directives)
	for _, field := range setters {
		method := str.Capitalize(field.Name)
		fmt.Fprintf(b, "func (b *%sQueryUpdateBuilder) Set%s(value %s) *%sQueryUpdateBuilder { b.state.Set%s(value); return b }\n", name, method, resolveGoType(field.Type), name, method)
	}
	fmt.Fprintf(b, "func (b *%sQueryUpdateBuilder) Exec(ctx context.Context) (int64,error) {\n", name)
	if hasFallibleSave(setters) {
		b.WriteString("if b.state._prepareErr != nil { return 0,b.state._prepareErr }\n")
	}
	b.WriteString("update := b.state.UpdateBase\n")
	b.WriteString("return b.query.updatePrepared(ctx,update.Sets...)\n}\n\n")
}
