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

// InferAPI tries to parse an API name into a structured query.
// Returns nil if the name doesn't match any known pattern.
func InferAPI(name string, models map[string]*ast.ModelDecl) *InferredAPI {
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
	// Comparison — longest first
	{"GreaterThanEqual", "gte"},
	{"LessThanEqual", "lte"},
	{"GreaterThan", "gt"},
	{"LessThan", "lt"},
	// Time-semantic aliases
	{"After", "gt"},
	{"Before", "lt"},
	// String matching
	{"NotContaining", "notContaining"},
	{"StartingWith", "startswith"},
	{"EndingWith", "endswith"},
	{"Containing", "containing"},
	{"IgnoreCase", "ignoreCase"},
	// Null checks
	{"IsNotNull", "isNotNull"},
	{"IsNull", "isNull"},
	{"NotNull", "isNotNull"},
	// Range
	{"Between", "between"},
	// Set membership
	{"NotIn", "notIn"},
	{"In", "in"},
	// String pattern
	{"NotLike", "notLike"},
	{"Like", "like"},
	// Negation
	{"Not", "ne"},
	// Boolean
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

	// Auto-infer params if not declared
	params := api.Params
	if len(params) == 0 {
		params = buildInferredParams(inf, m)
	}

	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// Parse params
	for _, p := range params {
		goType := resolveGoType(p.Type)
		method := paramMethod(goType)
		// Enum params are strings in JSON
		if method == "" && p.Type != nil && enums[p.Type.Name] {
			method = "String"
		}
		if method == "" {
			fmt.Fprintf(b, "\t\tvar %s %s\n", p.Name, goType)
			fmt.Fprintf(b, "\t\tif err := req.ParamJSON(%q, &%s); err != nil {\n", p.Name, p.Name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		}
	}

	// Build conditions
	writeInferredConditions(b, inf, m, params)

	rels := analyzeRelations(m, enums)
	writeInferredAction(b, inf, modelName, isSoftDelete(m), rels)
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
func writeInferredAction(b *strings.Builder, inf *InferredAPI, modelName string, soft bool, rels []Relation) {
	hasRels := len(rels) > 0
	switch inf.Action {
	case "count":
		fmt.Fprintf(b, "\t\tcount, err := app.%s.Where(conds...).Count(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, count)\n")
		fmt.Fprintf(b, "\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`{\"count\":`)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendInt(count)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\t}\n")

	case "exists":
		fmt.Fprintf(b, "\t\texists, err := app.%s.Where(conds...).Exists(ctx)\n", modelName)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendBool(req.Buf.B, exists)\n")
		fmt.Fprintf(b, "\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`{\"exists\":`)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendBool(exists)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\t}\n")

	case "delete":
		if soft {
			fmt.Fprintf(b, "\t\tn, err := app.%s.SoftDelete(ctx, conds...)\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tn, err := app.%s.Where(conds...).Delete(ctx)\n", modelName)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif n == 0 {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q})\n\t\t}\n", modelName)
		fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, n)\n")
		fmt.Fprintf(b, "\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`{\"deleted\":`)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendInt(n)\n")
		fmt.Fprintf(b, "\t\t\treq.Buf.AppendByte('}')\n")
		fmt.Fprintf(b, "\t\t}\n")

	case "list":
		if hasRels {
			fmt.Fprintf(b, "\t\tcols := selection.SQLColumns(req.Select)\n")
			writeInferredFKEnsure(b, rels)
			fmt.Fprintf(b, "\t\tq := app.%s.Where(conds...).Select(cols...)\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tq := app.%s.Where(conds...).Select(selection.SQLColumns(req.Select)...)\n", modelName)
		}
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
			lower := str.LowerFirst(modelName)
			fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(results)))\n")
			fmt.Fprintf(b, "\t\t\tfor _, item := range results {\n")
			fmt.Fprintf(b, "\t\t\t\titem.WriteLuxo(req.Buf, req.FieldMask)\n")
			fmt.Fprintf(b, "\t\t\t}\n")
			fmt.Fprintf(b, "\t\t} else {\n")
			fmt.Fprintf(b, "\t\t\t%sListJSON(results).WriteJSON(req.Buf, req.Select)\n", lower)
			fmt.Fprintf(b, "\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\tresults, total, err := q.Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)\n")
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
			if hasRels {
				fmt.Fprintf(b, "\t\tif err := resolve%sListRelations(ctx, app, results, req.Select); err != nil {\n", modelName)
				fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
			}
			lower := str.LowerFirst(modelName)
			fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(results)))\n")
			fmt.Fprintf(b, "\t\t\tfor _, item := range results {\n")
			fmt.Fprintf(b, "\t\t\t\titem.WriteLuxo(req.Buf, req.FieldMask)\n")
			fmt.Fprintf(b, "\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, total)\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))\n")
			fmt.Fprintf(b, "\t\t} else {\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`{\"items\":`)\n")
			fmt.Fprintf(b, "\t\t\t%sListJSON(results).WriteJSON(req.Buf, req.Select)\n", lower)
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`,\"total\":`)\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendInt(total)\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`,\"page\":`)\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendInt(int64(req.Page))\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendString(`,\"pageSize\":`)\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendInt(int64(req.PageSize))\n")
			fmt.Fprintf(b, "\t\t\treq.Buf.AppendByte('}')\n")
			fmt.Fprintf(b, "\t\t}\n")
		}

	default: // get
		if hasRels {
			fmt.Fprintf(b, "\t\tcols := selection.SQLColumns(req.Select)\n")
			writeInferredFKEnsure(b, rels)
			fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(conds...).Select(cols...).First(ctx)\n", modelName)
		} else {
			fmt.Fprintf(b, "\t\tresult, err := app.%s.Where(conds...).Select(selection.SQLColumns(req.Select)...).First(ctx)\n", modelName)
		}
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fmt.Fprintf(b, "\t\tif result == nil {\n\t\t\treturn errors.NotFound.WithData(errors.ResourceError{Resource: %q})\n\t\t}\n", modelName)
		if hasRels {
			fmt.Fprintf(b, "\t\tif err := resolve%sRelations(ctx, app, result, req.Select); err != nil {\n", modelName)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		}
		fmt.Fprintf(b, "\t\tif req.BinaryMode {\n")
		fmt.Fprintf(b, "\t\t\tresult.WriteLuxo(req.Buf, req.FieldMask)\n")
		fmt.Fprintf(b, "\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\tresult.WriteJSON(req.Buf, req.Select)\n")
		fmt.Fprintf(b, "\t\t}\n")
	}

	fmt.Fprintf(b, "\t\treturn nil\n")
	fmt.Fprintf(b, "\t}\n}\n\n")
}

// writeInferredFKEnsure writes ensureField calls for relation FK columns in inferred handlers.
func writeInferredFKEnsure(b *strings.Builder, rels []Relation) {
	seen := make(map[string]bool)
	for _, rel := range rels {
		col := str.ToSnakeCase(rel.LocalKey)
		if seen[col] {
			continue
		}
		seen[col] = true
		fmt.Fprintf(b, "\t\tcols = ensureField(cols, %q)\n", col)
	}
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

// buildInferredParams auto-generates parameter declarations from inferred clauses.
// Each clause that requires a parameter produces one (or two for between).
func buildInferredParams(inf *InferredAPI, m *ast.ModelDecl) []*ast.ParamDecl {
	var params []*ast.ParamDecl
	for _, group := range inf.Groups {
		for _, clause := range group.Clauses {
			switch clause.Op {
			case "true", "false", "isNull", "isNotNull":
				// Zero-param operations
				continue

			case "between":
				fieldType := getFieldTypeRef(m, clause.Field)
				params = append(params,
					&ast.ParamDecl{Name: clause.Field + "From", Type: fieldType},
					&ast.ParamDecl{Name: clause.Field + "To", Type: fieldType},
				)

			case "in", "notIn":
				// Array param: [Int], [String], etc.
				fieldType := getFieldTypeRef(m, clause.Field)
				params = append(params, &ast.ParamDecl{
					Name: inferParamName(clause),
					Type: &ast.TypeRef{Name: fieldType.Name, IsList: true},
				})

			case "containing", "notContaining", "startswith", "endswith", "like", "notLike", "ignoreCase":
				// String operations always take string param
				params = append(params, &ast.ParamDecl{
					Name: inferParamName(clause),
					Type: &ast.TypeRef{Name: "String"},
				})

			default:
				// eq, ne, gt, gte, lt, lte — same type as field
				params = append(params, &ast.ParamDecl{
					Name: inferParamName(clause),
					Type: getFieldTypeRef(m, clause.Field),
				})
			}
		}
	}
	return params
}

// inferParamName generates a reasonable parameter name from a clause.
func inferParamName(clause InferClause) string {
	switch clause.Op {
	case "containing", "notContaining":
		return "keyword"
	case "startswith":
		return clause.Field + "Prefix"
	case "endswith":
		return clause.Field + "Suffix"
	case "in", "notIn":
		return clause.Field + "s" // plural: ids, roles, etc.
	default:
		return clause.Field
	}
}

// getFieldTypeRef returns a copy of the AST TypeRef for a model field.
func getFieldTypeRef(m *ast.ModelDecl, name string) *ast.TypeRef {
	for _, f := range m.Fields {
		if f.Name == name && f.Type != nil {
			return &ast.TypeRef{Name: f.Type.Name}
		}
	}
	return &ast.TypeRef{Name: "String"}
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
