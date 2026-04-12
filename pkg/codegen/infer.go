package codegen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// InferredAPI represents a parsed method name like getUserByEmailOrPhone.
type InferredAPI struct {
	Action    string        // "get", "list", "count", "delete", "exists"
	ModelName string        // "User", "Post"
	Groups    []ClauseGroup // OR-separated groups of AND clauses
	OrderBy   string        // "createdAt" (optional)
	OrderDesc bool          // true if ends with Desc
	TopN      int           // listTop10... → 10 (0 = no limit)
}

// ClauseGroup is a group of AND-ed clauses. Groups are OR-ed together.
// ByEmailOrPhone → [{email,eq}] OR [{phone,eq}]
// ByNameAndRoleOrEmail → [{name,eq},{role,eq}] OR [{email,eq}]
type ClauseGroup struct {
	Clauses []InferClause
}

// InferClause is a single field condition.
type InferClause struct {
	Field string // "email", "title", "age"
	Op    string // "eq", "containing", "gt", "lt", "gte", "lte", "between", "isNull", "isNotNull", "true", "false"
}

// inferAPI tries to parse an API name into a structured query.
// Returns nil if the name doesn't match any known pattern.
func inferAPI(name string, models map[string]*ast.ModelDecl) *InferredAPI {
	result := &InferredAPI{}

	rest := extractActionPrefix(name, result)
	if result.Action == "" {
		return nil
	}

	// Split off OrderBy section
	orderIdx := strings.LastIndex(rest, "OrderBy")
	if orderIdx > 0 {
		result.OrderBy, result.OrderDesc = parseOrderBy(rest[orderIdx+7:])
		rest = rest[:orderIdx]
	}

	// Extract model name: before "By"
	byIdx := strings.Index(rest, "By")
	var modelPart, fieldsPart string
	if byIdx > 0 {
		modelPart = rest[:byIdx]
		fieldsPart = rest[byIdx+2:]
	} else {
		modelPart = rest
	}

	// Resolve model name
	modelName := singularize(modelPart)
	if _, ok := models[modelName]; !ok {
		return nil
	}
	result.ModelName = modelName
	model := models[modelName]

	// Parse field clauses with Or support
	if fieldsPart != "" {
		groups := parseClauseGroups(fieldsPart, model)
		if groups == nil {
			return nil
		}
		result.Groups = groups
	}

	// Validate OrderBy field
	if result.OrderBy != "" && !hasField(model, result.OrderBy) {
		return nil
	}

	return result
}

// extractActionPrefix parses the action (get/list/count/exists/delete) and optional TopN.
func extractActionPrefix(name string, result *InferredAPI) string {
	for _, prefix := range []string{"list", "get", "count", "exists", "delete"} {
		if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
			continue
		}
		after := name[len(prefix):]
		if prefix == "list" {
			after = extractTopN(after, result)
		}
		if len(after) > 0 && unicode.IsUpper(rune(after[0])) {
			result.Action = prefix
			return after
		}
	}
	return ""
}

// extractTopN parses Top10 or First5 from the string, sets result.TopN.
func extractTopN(s string, result *InferredAPI) string {
	for _, keyword := range []string{"Top", "First"} {
		if !strings.HasPrefix(s, keyword) {
			continue
		}
		numStart := len(keyword)
		numEnd := numStart
		for numEnd < len(s) && s[numEnd] >= '0' && s[numEnd] <= '9' {
			numEnd++
		}
		if numEnd > numStart {
			n, _ := strconv.Atoi(s[numStart:numEnd])
			result.TopN = n
			return s[numEnd:]
		}
	}
	return s
}

// parseClauseGroups splits by Or, then each group by And.
// "EmailOrPhone" → [{email}] OR [{phone}]
// "NameAndRoleOrEmail" → [{name,role}] OR [{email}]
func parseClauseGroups(s string, model *ast.ModelDecl) []ClauseGroup {
	orParts := splitBySeparator(s, "Or")
	var groups []ClauseGroup

	for _, part := range orParts {
		andParts := splitBySeparator(part, "And")
		var clauses []InferClause
		for _, ap := range andParts {
			clause := parseOneClause(ap, model)
			if clause == nil {
				return nil
			}
			clauses = append(clauses, *clause)
		}
		groups = append(groups, ClauseGroup{Clauses: clauses})
	}
	return groups
}

// splitBySeparator splits "EmailAndRole" by "And" or "EmailOrPhone" by "Or".
// Ensures separator is between two uppercase-starting parts.
func splitBySeparator(s, sep string) []string {
	var parts []string
	for {
		idx := strings.Index(s, sep)
		if idx <= 0 || idx+len(sep) >= len(s) {
			break
		}
		// Verify separator is between two uppercase-starting parts
		if unicode.IsUpper(rune(s[idx+len(sep)])) {
			parts = append(parts, s[:idx])
			s = s[idx+len(sep):]
		} else {
			break
		}
	}
	if s != "" {
		parts = append(parts, s)
	}
	return parts
}

// Operator suffixes in order of longest-first to avoid prefix conflicts.
var operatorSuffixes = []struct {
	suffix string
	op     string
}{
	{"GreaterThanEqual", "gte"},
	{"LessThanEqual", "lte"},
	{"GreaterThan", "gt"},
	{"LessThan", "lt"},
	{"StartingWith", "startswith"},
	{"EndingWith", "endswith"},
	{"Containing", "containing"},
	{"IsNotNull", "isNotNull"},
	{"IsNull", "isNull"},
	{"NotNull", "isNotNull"},
	{"Between", "between"},
	{"NotLike", "notLike"},
	{"Like", "like"},
	{"Not", "ne"},
	{"True", "true"},
	{"False", "false"},
}

// parseOneClause parses "TitleContaining" or "Email" or "PublishedTrue".
func parseOneClause(s string, model *ast.ModelDecl) *InferClause {
	for _, op := range operatorSuffixes {
		if strings.HasSuffix(s, op.suffix) {
			field := str.LowerFirst(s[:len(s)-len(op.suffix)])
			if hasField(model, field) {
				return &InferClause{Field: field, Op: op.op}
			}
		}
	}
	// Default: eq
	field := str.LowerFirst(s)
	if hasField(model, field) {
		return &InferClause{Field: field, Op: "eq"}
	}
	return nil
}

// singularize strips trailing 's' for simple English plurals.
func singularize(s string) string {
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "ses") || strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "zes") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return s[:len(s)-1]
	}
	return s
}

// parseOrderBy parses "CreatedAtDesc" → ("createdAt", true)
func parseOrderBy(s string) (string, bool) {
	if strings.HasSuffix(s, "Desc") {
		return str.LowerFirst(s[:len(s)-4]), true
	}
	if strings.HasSuffix(s, "Asc") {
		return str.LowerFirst(s[:len(s)-3]), false
	}
	return str.LowerFirst(s), false
}

// hasField checks if model has a field with the given name.
func hasField(m *ast.ModelDecl, name string) bool {
	for _, f := range m.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// --- Handler generation ---

// generateInferredHandler generates a handler from an inferred API.
func generateInferredHandler(b *strings.Builder, api *ast.ApiDecl, inf *InferredAPI, enums map[string]bool, m *ast.ModelDecl) {
	name := api.Name
	modelName := inf.ModelName

	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// Parse params
	for _, p := range api.Params {
		method := paramMethod(resolveGoType(p.Type))
		fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	}

	// Build conditions
	writeInferredConditions(b, inf, m, api.Params)

	writeInferredAction(b, inf, modelName, isSoftDelete(m))
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
	fieldType := fieldGoType(m, clause.Field)
	paramExpr := ""
	if paramIdx < len(params) {
		paramExpr = params[paramIdx].Name
	}

	switch clause.Op {
	case "true":
		fmt.Fprintf(b, "\t\t\tlux.NewBoolField(%q).IsTrue(),\n", col)
		return paramIdx
	case "false":
		fmt.Fprintf(b, "\t\t\tlux.NewBoolField(%q).IsFalse(),\n", col)
		return paramIdx
	case "isNull":
		fmt.Fprintf(b, "\t\t\tlux.NewTimeField(%q).IsNull(),\n", col)
		return paramIdx
	case "isNotNull":
		fmt.Fprintf(b, "\t\t\tlux.NewTimeField(%q).IsNotNull(),\n", col)
		return paramIdx
	case "containing":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(\"%%\" + %s + \"%%\"),\n", col, paramExpr)
	case "startswith":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(%s + \"%%\"),\n", col, paramExpr)
	case "endswith":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(\"%%\" + %s),\n", col, paramExpr)
	case "like":
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).Like(%s),\n", col, paramExpr)
	case "between":
		param2 := ""
		if paramIdx+1 < len(params) {
			param2 = params[paramIdx+1].Name
		}
		fmt.Fprintf(b, "\t\t\tlux.NewIntField(%q).Gte(%s),\n", col, paramExpr)
		fmt.Fprintf(b, "\t\t\tlux.NewIntField(%q).Lte(%s),\n", col, param2)
		return paramIdx + 2
	default:
		writeCondition(b, fieldType, col, opToMethod(clause.Op), paramExpr)
	}
	return paramIdx + 1
}

// writeInferredAction generates the query execution + response writing for an inferred handler.
func writeInferredAction(b *strings.Builder, inf *InferredAPI, modelName string, soft bool) {
	switch inf.Action {
	case "count":
		fmt.Fprintf(b, "\t\tcount, err := app.%s.Where(conds...).Count(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"count\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(count)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")

	case "exists":
		fmt.Fprintf(b, "\t\texists, err := app.%s.Where(conds...).Exists(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"exists\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendBool(exists)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")

	case "delete":
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, conds...)\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(conds...).Delete(ctx)\n", modelName)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n == 0 {\n\t\t\treturn errors.NotFound.WithData(map[string]any{\"resource\": %q})\n\t\t}\n", modelName)
		fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"deleted\":`)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendInt(n)\n")
		fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")

	case "list":
		fmt.Fprintf(b, "\t\tq := app.%s.Where(conds...).Select(selection.SQLColumns(req.Select)...)\n", modelName)
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
			lower := str.LowerFirst(modelName)
			fmt.Fprintf(b, "\t\t%sListJSON(results).WriteJSON(req.Buf, req.Select)\n", lower)
		} else {
			fmt.Fprintf(b, "\t\tresults, total, err := q.Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)\n")
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
			lower := str.LowerFirst(modelName)
			fmt.Fprintf(b, "\t\treq.Buf.AppendString(`{\"items\":`)\n")
			fmt.Fprintf(b, "\t\t%sListJSON(results).WriteJSON(req.Buf, req.Select)\n", lower)
			fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"total\":`)\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendInt(total)\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"page\":`)\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendInt(int64(req.Page))\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendString(`,\"pageSize\":`)\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendInt(int64(req.PageSize))\n")
			fmt.Fprintf(b, "\t\treq.Buf.AppendByte('}')\n")
		}

	default: // get
		fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(conds...).Select(selection.SQLColumns(req.Select)...).First(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif result == nil {\n\t\t\treturn errors.NotFound.WithData(map[string]any{\"resource\": %q})\n\t\t}\n", modelName)
		fmt.Fprintf(b, "\t\tresult.WriteJSON(req.Buf, req.Select)\n")
	}

	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n}\n\n")
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

	switch clause.Op {
	case "true":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s = true\")\n", col)
		return paramIdx
	case "false":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s = false\")\n", col)
		return paramIdx
	case "isNull":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s IS NULL\")\n", col)
		return paramIdx
	case "isNotNull":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, \"%s IS NOT NULL\")\n", col)
		return paramIdx
	}

	paramExpr := ""
	if paramIdx < len(params) {
		paramExpr = params[paramIdx].Name
	}

	sqlOp := "="
	switch clause.Op {
	case "ne":
		sqlOp = "!="
	case "gt":
		sqlOp = ">"
	case "gte":
		sqlOp = ">="
	case "lt":
		sqlOp = "<"
	case "lte":
		sqlOp = "<="
	case "containing":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, fmt.Sprintf(\"%s LIKE $%%d\", argIdx))\n", col)
		fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, \"%%\" + %s + \"%%\")\n", paramExpr)
		fmt.Fprintf(b, "\t\t\targIdx++\n")
		return paramIdx + 1
	case "like":
		fmt.Fprintf(b, "\t\t\tparts = append(parts, fmt.Sprintf(\"%s LIKE $%%d\", argIdx))\n", col)
		fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, %s)\n", paramExpr)
		fmt.Fprintf(b, "\t\t\targIdx++\n")
		return paramIdx + 1
	}

	fmt.Fprintf(b, "\t\t\tparts = append(parts, fmt.Sprintf(\"%s %s $%%d\", argIdx))\n", col, sqlOp)
	fmt.Fprintf(b, "\t\t\torArgs = append(orArgs, %s)\n", paramExpr)
	fmt.Fprintf(b, "\t\t\targIdx++\n")
	return paramIdx + 1
}

// writeCondition generates a typed condition call.
func writeCondition(b *strings.Builder, fieldType, col, method, paramExpr string) {
	switch fieldType {
	case "int64":
		fmt.Fprintf(b, "\t\t\tlux.NewIntField(%q).%s(%s),\n", col, method, paramExpr)
	case "float64":
		fmt.Fprintf(b, "\t\t\tlux.NewFloatField(%q).%s(%s),\n", col, method, paramExpr)
	case "bool":
		fmt.Fprintf(b, "\t\t\tlux.NewBoolField(%q).Eq(%s),\n", col, paramExpr)
	default:
		fmt.Fprintf(b, "\t\t\tlux.NewStringField(%q).%s(%s),\n", col, method, paramExpr)
	}
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
func ValidateInferredReturnType(api *ast.ApiDecl, inf *InferredAPI) string {
	if api.ReturnType == nil {
		return fmt.Sprintf("inferred API %q must declare a return type", api.Name)
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
