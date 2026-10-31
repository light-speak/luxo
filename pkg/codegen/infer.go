package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

type InferredAPI = inferred.InferredAPI
type ClauseGroup = inferred.ClauseGroup
type InferClause = inferred.InferClause

// InferAPI parses a name-derived query using the shared compile-time contract.
func InferAPI(name string, models map[string]*ast.ModelDecl) *InferredAPI {
	return inferred.Parse(name, models)
}

func generateInferredHandler(b *strings.Builder, api *ast.ApiDecl, inf *InferredAPI, enums map[string]bool, m *ast.ModelDecl) {
	name := api.Name
	modelName := inf.ModelName

	// Auto-infer params if not declared
	params := api.Params
	if len(params) == 0 {
		params = buildInferredParams(inf, m)
	}

	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")
	if directive := findDirective(api.Directives, "auth"); directive != nil {
		writeAuthCheck(b, "\t\t", directive)
	} else if directive := findDirective(m.Directives, "auth"); directive != nil || hasDirective(m.Directives, "withAuth") {
		writeCRUDAuthCheck(b, "\t\t", directive)
	}

	// Parse params
	for _, p := range params {
		if p.Type != nil && enums[p.Type.Name] {
			writeInferredEnumParamExtraction(b, p, enums, "\t\t")
		} else {
			writeCallableParamExtraction(b, p, enums, "\t\t")
		}
	}
	cached := writeCachedBodyStart(b, api.Directives)

	// Build conditions
	writeInferredConditions(b, inf, m, params)

	rels := analyzeRelations(m, enums)
	writeInferredAction(b, inf, modelName, isSoftDelete(m), rels, m)
	writeCachedBodyEnd(b, cached)
	fmt.Fprintf(b, "\t}\n}\n\n")
}

// writeInferredConditions generates the WHERE conditions for an inferred handler.
func writeInferredConditions(b *strings.Builder, inf *InferredAPI, m *ast.ModelDecl, params []*ast.ParamDecl) {
	hasOr := len(inf.Groups) > 1
	paramIdx := 0

	if hasOr {
		fmt.Fprintf(b, "\t\tvar orParts []string\n")
		fmt.Fprintf(b, "\t\tvar orArgs []any\n")
		fmt.Fprintf(b, "\t\targIdx := 1\n")
		for _, group := range inf.Groups {
			fmt.Fprintf(b, "\t\t{\n")
			fmt.Fprintf(b, "\t\t\tvar parts []string\n")
			for _, clause := range group.Clauses {
				paramIdx = writeClauseSQL(b, clause, m, params, paramIdx)
			}
			fmt.Fprintf(b, "\t\t\torParts = append(orParts, \"(\"+strings.Join(parts, \" AND \")+\")\")\n")
			fmt.Fprintf(b, "\t\t}\n")
		}
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.RawCondition(strings.Join(orParts, \" OR \"), orArgs...)}\n")
	} else {
		fmt.Fprintf(b, "\t\tconds := []lux.Condition{\n")
		if len(inf.Groups) > 0 {
			for _, clause := range inf.Groups[0].Clauses {
				paramIdx = writeAndClause(b, clause, m, params, paramIdx)
			}
		}
		fmt.Fprintf(b, "\t\t}\n")
	}

	// Note: @soft deleted_at filter is already in Client.Where(), not added here
}

// writeAndClause writes a single AND clause condition.
func writeAndClause(b *strings.Builder, clause InferClause, m *ast.ModelDecl, params []*ast.ParamDecl, paramIdx int) int {
	col := str.ToSnakeCase(clause.Field)
	condType := fieldConditionType(getFieldTypeRefRaw(m, clause.Field))
	paramExpr := ""
	if paramIdx < len(params) {
		paramExpr = params[paramIdx].Name
	}

	// Zero-param ops
	if zp := writeZeroParamClause(b, clause.Op, condType, col); zp {
		return paramIdx
	}

	// String matching ops
	if sm := writeStringMatchClause(b, clause.Op, col, paramExpr); sm {
		return paramIdx + 1
	}

	switch clause.Op {
	case "between":
		param2 := ""
		if paramIdx+1 < len(params) {
			param2 = params[paramIdx+1].Name
		}
		fmt.Fprintf(b, "\t\t\tlux.New%s(%q).Between(%s, %s),\n", condType, col, paramExpr, param2)
		return paramIdx + 2
	case "in":
		fmt.Fprintf(b, "\t\t\tlux.New%s(%q).In(%s...),\n", condType, col, paramExpr)
	case "notIn":
		fmt.Fprintf(b, "\t\t\tlux.New%s(%q).NotIn(%s...),\n", condType, col, paramExpr)
	default:
		writeCondition(b, condType, col, opToMethod(clause.Op), paramExpr)
	}
	return paramIdx + 1
}

// writeInferredAction generates the query execution + response writing for an inferred handler.
func writeInferredAction(b *strings.Builder, inf *InferredAPI, modelName string, soft bool, rels []Relation, models ...*ast.ModelDecl) {
	hasRels := len(rels) > 0
	var model *ast.ModelDecl
	if len(models) > 0 {
		model = models[0]
	}
	switch inf.Action {
	case "count":
		fmt.Fprintf(b, "\t\tcount, err := app.%s.Where(conds...).Count(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, count)\n")

	case "exists":
		fmt.Fprintf(b, "\t\texists, err := app.%s.Where(conds...).Exists(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendBool(req.Buf.B, exists)\n")

	case "delete":
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, conds...)\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(conds...).Delete(ctx)\n", modelName)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n == 0 {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q})\n\t\t}\n", modelName)
		fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, n)\n")

	case "list":
		fmt.Fprintf(b, "\t\tcols := select%sSQLColumns(req.Select)\n", modelName)
		fmt.Fprintf(b, "\t\tq := app.%s.Where(conds...).Select(cols...)\n", modelName)
		if inf.OrderBy != "" {
			order := str.ToSnakeCase(inf.OrderBy)
			if inf.OrderDesc {
				fmt.Fprintf(b, "\t\tq = q.OrderBy(%q)\n", order+" DESC")
			} else {
				fmt.Fprintf(b, "\t\tq = q.OrderBy(%q)\n", order+" ASC")
			}
		}
		if inf.TopN > 0 {
			// Top/First: fixed limit, no pagination
			fmt.Fprintf(b, "\t\tresults, err := q.Limit(%d).All(ctx)\n", inf.TopN)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
			if hasRels {
				fmt.Fprintf(b, "\t\tif err := resolve%sListRelations(ctx, app, results, req.Select); err != nil {\n", modelName)
				fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
			}
			writeComputedResolve(b, model, "results", "\t\t")
			fmt.Fprintf(b, "\t\tif err := WriteColumnar%s(req.Buf, results, req.FieldMask); err != nil { return err }\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tresults, total, err := q.Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)\n")
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
			if hasRels {
				fmt.Fprintf(b, "\t\tif err := resolve%sListRelations(ctx, app, results, req.Select); err != nil {\n", modelName)
				fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
			}
			writeComputedResolve(b, model, "results", "\t\t")
			fmt.Fprintf(b, "\t\tif err := WriteColumnar%s(req.Buf, results, req.FieldMask); err != nil { return err }\n", modelName)
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, total)\n")
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))\n")
			fmt.Fprintf(b, "\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))\n")
		}

	default: // get
		fmt.Fprintf(b, "\t\tcols := select%sSQLColumns(req.Select)\n", modelName)
		fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(conds...).Select(cols...).First(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif result == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q})\n\t\t}\n", modelName)
		if hasRels {
			fmt.Fprintf(b, "\t\tif err := resolve%sRelations(ctx, app, result, req.Select); err != nil {\n", modelName)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		}
		writeComputedResolve(b, model, "[]*"+modelName+"{result}", "\t\t")
		fmt.Fprintf(b, "\t\tif err := result.WriteLuxo(req.Buf, req.FieldMask); err != nil { return err }\n")
	}

	fmt.Fprintf(b, "\t\treturn nil\n")
}

// writeZeroParamClause writes a zero-parameter clause (true/false/isNull/isNotNull).
// Returns true if handled.
func writeZeroParamClause(b *strings.Builder, op, condType, col string) bool {
	switch op {
	case "true":
		fmt.Fprintf(b, "\t\t\tlux.NewBoolField(%q).IsTrue(),\n", col)
	case "false":
		fmt.Fprintf(b, "\t\t\tlux.NewBoolField(%q).IsFalse(),\n", col)
	case "isNull":
		fmt.Fprintf(b, "\t\t\tlux.New%s(%q).IsNull(),\n", condType, col)
	case "isNotNull":
		fmt.Fprintf(b, "\t\t\tlux.New%s(%q).IsNotNull(),\n", condType, col)
	default:
		return false
	}
	return true
}

// writeStringMatchClause writes a string matching clause (like/containing/etc.).
// Returns true if handled.
func writeStringMatchClause(b *strings.Builder, op, col, paramExpr string) bool {
	switch op {
	case "containing":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(\"%%\" + lux.EscapeLike(%s) + \"%%\"),\n", col, paramExpr)
	case "notContaining":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).NotLike(\"%%\" + lux.EscapeLike(%s) + \"%%\"),\n", col, paramExpr)
	case "startswith":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(lux.EscapeLike(%s) + \"%%\"),\n", col, paramExpr)
	case "endswith":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(\"%%\" + lux.EscapeLike(%s)),\n", col, paramExpr)
	case "like":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(%s),\n", col, paramExpr)
	case "notLike":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).NotLike(%s),\n", col, paramExpr)
	case "ignoreCase":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).ILike(%s),\n", col, paramExpr)
	default:
		return false
	}
	return true
}

// opToMethod maps clause operator to Go condition method name.
func opToMethod(op string) string {
	switch op {
	case "eq":
		return "Eq"
	case "ne":
		return "Neq"
	case "gt":
		return "Gt"
	case "gte":
		return "Gte"
	case "lt":
		return "Lt"
	case "lte":
		return "Lte"
	default:
		return "Eq"
	}
}

// writeClauseSQL writes a clause as raw SQL parts for OR group building.
func writeClauseSQL(b *strings.Builder, clause InferClause, m *ast.ModelDecl, params []*ast.ParamDecl, paramIdx int) int {
	col := str.ToSnakeCase(clause.Field)

	if zp := writeClauseSQLZeroParam(b, clause.Op, col); zp {
		return paramIdx
	}

	paramExpr := ""
	if paramIdx < len(params) {
		paramExpr = params[paramIdx].Name
	}

	if sm := writeClauseSQLStringMatch(b, clause.Op, col, paramExpr); sm {
		return paramIdx + 1
	}

	switch clause.Op {
	case "between":
		param2 := ""
		if paramIdx+1 < len(params) {
			param2 = params[paramIdx+1].Name
		}
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s BETWEEN $\" + strconv.Itoa(argIdx) + \" AND $\" + strconv.Itoa(argIdx+1))\n", col)
		fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, %s, %s)\n", paramExpr, param2)
		fmt.Fprintf(b, "\t\t\targIdx += 2\n")
		return paramIdx + 2
	case "in":
		writeClauseSQLSet(b, col, "IN", paramExpr)
		return paramIdx + 1
	case "notIn":
		writeClauseSQLSet(b, col, "NOT IN", paramExpr)
		return paramIdx + 1
	}

	sqlOp := clauseOpToSQL(clause.Op)
	fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s %s $\" + strconv.Itoa(argIdx))\n", col, sqlOp)
	fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, %s)\n", paramExpr)
	fmt.Fprintf(b, "\t\t\targIdx++\n")
	return paramIdx + 1
}

// writeClauseSQLZeroParam writes zero-param SQL clauses. Returns true if handled.
func writeClauseSQLZeroParam(b *strings.Builder, op, col string) bool {
	switch op {
	case "true":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s = true\")\n", col)
	case "false":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s = false\")\n", col)
	case "isNull":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s IS NULL\")\n", col)
	case "isNotNull":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s IS NOT NULL\")\n", col)
	default:
		return false
	}
	return true
}

// writeClauseSQLStringMatch writes string matching SQL clauses. Returns true if handled.
func writeClauseSQLStringMatch(b *strings.Builder, op, col, paramExpr string) bool {
	switch op {
	case "containing":
		writeClauseSQLLike(b, col, "LIKE", "\"%%\" + lux.EscapeLike("+paramExpr+") + \"%%\"")
	case "notContaining":
		writeClauseSQLLike(b, col, "NOT LIKE", "\"%%\" + lux.EscapeLike("+paramExpr+") + \"%%\"")
	case "startswith":
		writeClauseSQLLike(b, col, "LIKE", "lux.EscapeLike("+paramExpr+") + \"%%\"")
	case "endswith":
		writeClauseSQLLike(b, col, "LIKE", "\"%%\" + lux.EscapeLike("+paramExpr+")")
	case "like":
		writeClauseSQLLike(b, col, "LIKE", paramExpr)
	case "notLike":
		writeClauseSQLLike(b, col, "NOT LIKE", paramExpr)
	case "ignoreCase":
		writeClauseSQLLike(b, col, "ILIKE", paramExpr)
	default:
		return false
	}
	return true
}

// writeClauseSQLLike writes a LIKE/NOT LIKE/ILIKE clause for OR groups.
func writeClauseSQLLike(b *strings.Builder, col, op, valExpr string) {
	fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s %s $\" + strconv.Itoa(argIdx))\n", col, op)
	fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, %s)\n", valExpr)
	fmt.Fprintf(b, "\t\t\targIdx++\n")
}

// writeClauseSQLSet writes an IN/NOT IN clause for OR groups.
// For simplicity, expands the slice with a loop.
func writeClauseSQLSet(b *strings.Builder, col, op, paramExpr string) {
	fmt.Fprintf(b, "\t\t\t{\n")
	fmt.Fprintf(b, "\t\t\t\tph := make([]string, len(%s))\n", paramExpr)
	fmt.Fprintf(b, "\t\t\t\tfor i := range %s {\n", paramExpr)
	fmt.Fprintf(b, "\t\t\t\t\tph[i] = \"$\" + strconv.Itoa(argIdx+i)\n")
	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t\tparts = append(parts, \"%s %s (\" + strings.Join(ph, \", \") + \")\")\n", col, op)
	fmt.Fprintf(b, "\t\t\t\tfor _, v := range %s {\n", paramExpr)
	fmt.Fprintf(b, "\t\t\t\t\torArgs = append(orArgs, v)\n")
	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t\targIdx += len(%s)\n", paramExpr)
	fmt.Fprintf(b, "\t\t\t}\n")
}

// clauseOpToSQL converts clause operator to SQL operator.
func clauseOpToSQL(op string) string {
	switch op {
	case "ne":
		return "!="
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	default:
		return "="
	}
}

// writeCondition generates a typed condition call.
func writeCondition(b *strings.Builder, condType, col, method, paramExpr string) {
	fmt.Fprintf(b, "\t\t\tlux.New%s(%q).%s(%s),\n", condType, col, method, paramExpr)
}

// fieldGoType returns the Go base type for a model field.
func fieldGoType(m *ast.ModelDecl, name string) string {
	for _, f := range m.Fields {
		if f.Name == name && f.Type != nil {
			return mapBaseType(f.Type.Name)
		}
	}
	return "string"
}

// ValidateInferredReturnType checks if the API return type matches the inferred action.
// Returns an error message if invalid, empty string if OK.
// If return type is nil, it's auto-inferred — always valid.
func ValidateInferredReturnType(api *ast.ApiDecl, inf *InferredAPI) string {
	if api.ReturnType == nil {
		return "" // auto-inferred, always valid
	}

	rt := api.ReturnType
	switch inf.Action {
	case "get":
		if rt.IsList {
			return fmt.Sprintf("%s: get returns single %s, not [%s]", api.Name, inf.ModelName, rt.Name)
		}
		if rt.Name != inf.ModelName {
			return fmt.Sprintf("%s: return type should be %s, got %s", api.Name, inf.ModelName, rt.Name)
		}
	case "list":
		if !rt.IsList {
			return fmt.Sprintf("%s: list returns [%s], not %s", api.Name, inf.ModelName, rt.Name)
		}
		if rt.Name != inf.ModelName {
			return fmt.Sprintf("%s: return type should be [%s], got [%s]", api.Name, inf.ModelName, rt.Name)
		}
	case "count":
		if rt.Name != "Int" {
			return fmt.Sprintf("%s: count returns Int, got %s", api.Name, rt.Name)
		}
	case "exists":
		if rt.Name != "Boolean" {
			return fmt.Sprintf("%s: exists returns Boolean, got %s", api.Name, rt.Name)
		}
	}
	return ""
}

func inferredAPIContract(api *ast.ApiDecl, models map[string]*ast.ModelDecl) *ast.ApiDecl {
	return inferred.Contract(api, models)
}
func buildInferredParams(inf *InferredAPI, model *ast.ModelDecl) []*ast.ParamDecl {
	return inferred.Parameters(inf, model)
}
func inferParamName(clause InferClause) string { return inferred.ParamName(clause) }
func getFieldTypeRef(model *ast.ModelDecl, name string) *ast.TypeRef {
	return inferred.FieldType(model, name)
}

// getFieldTypeRefRaw returns the original TypeRef from the model field (for fieldConditionType).
func getFieldTypeRefRaw(m *ast.ModelDecl, name string) *ast.TypeRef {
	for _, f := range m.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}
