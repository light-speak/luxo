// Package inferred defines compile-time conventions shared by analysis and generation.
package inferred

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
)

// QueryWhere validates the complete optional predicate without allocating.
// Analysis and generation consume this same argument contract.
// Error suffixes join the caller's method context in the final diagnostic.
func QueryWhere(arguments []*ast.NamedArg) (ast.Expr, int, string) {
	var where ast.Expr
	for index, argument := range arguments {
		if argument == nil || argument.Name != "where" {
			return nil, index, " accepts only one named where argument"
		}
		if argument.Value == nil {
			return nil, index, " where requires a value"
		}
		if where != nil {
			return nil, index, " repeats where"
		}
		value := argument.Value
		if !queryPredicate(value) {
			return nil, index, " where must be a non-null Boolean"
		}
		where = value
	}
	return where, -1, ""
}

func queryPredicate(value ast.Expr) bool {
	if lambda, ok := value.(*ast.LambdaExpr); ok {
		value, _ = lambda.SingleExpression()
	}
	return value != nil && value.GetTypeTag() == "Boolean" && !value.IsListType() && !value.IsNullable()
}

// MutationField describes resolved write metadata without exposing SDK visibility.
type MutationField struct {
	TypeName  string
	List      bool
	Nullable  bool
	Computed  bool
	Immutable bool
	Relation  bool
}

// MutationArgument validates one named setter or query predicate before lowering.
func MutationArgument(argument *ast.NamedArg, field MutationField) (bool, string) {
	if argument == nil || argument.Name == "" {
		return false, " requires named field setters"
	}
	if argument.Value == nil {
		return false, " argument requires a value"
	}
	if argument.Name == "where" {
		if !queryPredicate(argument.Value) {
			return true, " where must be a non-null Boolean"
		}
		return true, ""
	}
	if field.TypeName == "" {
		return false, " setter field does not exist"
	}
	if field.Computed || field.Relation {
		return false, " setter requires a stored field"
	}
	if field.Immutable {
		return false, " cannot change an immutable field"
	}
	if !mutationValueMatches(argument.Value, field) {
		return false, " setter value does not match the field type"
	}
	return false, ""
}

func mutationValueMatches(value ast.Expr, field MutationField) bool {
	if literal, ok := value.(*ast.Literal); ok && literal.Kind == token.Null {
		return field.Nullable
	}
	if list, ok := value.(*ast.ListExpr); ok && len(list.Items) == 0 {
		return field.List
	}
	return mutationValueName(value) == field.TypeName && value.IsListType() == field.List && (!value.IsNullable() || field.Nullable)
}

func mutationValueName(value ast.Expr) string {
	if name := value.GetTypeTag(); name != "" {
		return name
	}
	if literal, ok := value.(*ast.Literal); ok {
		switch literal.Kind {
		case token.Int:
			return "Int"
		case token.Float:
			return "Float"
		case token.String:
			return "String"
		case token.True, token.False:
			return "Boolean"
		}
	}
	return ""
}

// IsWhereTerminal identifies database terminals with one optional named predicate.
func IsWhereTerminal(method string) bool {
	return method != "updateMany" && ModelTerminalType(method) != ""
}

// ModelTerminalType returns the scalar result for a resolved model terminal.
func ModelTerminalType(method string) string {
	switch method {
	case "deleteMany", "count", "updateMany":
		return "Int"
	case "exists":
		return "Boolean"
	}
	return ""
}

// Query-builder modifiers and predicate terminals require a type/query receiver.
// Instance update/delete/save and identity methods keep their existing ABI.
func RequiresModelQueryReceiver(method string) bool {
	switch method {
	case "where", "select", "orderBy", "limit", "offset", "groupBy":
		return true
	}
	return ModelTerminalType(method) != ""
}

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

// Parse tries to parse an API name into a structured query.
// Returns nil if the name doesn't match any known pattern.
func Parse(name string, models map[string]*ast.ModelDecl) *InferredAPI {
	result := &InferredAPI{}

	rest := extractActionPrefix(name, result)
	if result.Action == "" {
		return nil
	}

	// Split off OrderBy section
	orderIdx := strings.LastIndex(rest, "OrderBy")
	if orderIdx > 0 {
		result.OrderBy, result.OrderDesc = ParseOrderBy(rest[orderIdx+7:])
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
	modelName := modelPart
	if _, ok := models[modelName]; !ok {
		modelName = Singularize(modelPart)
	}
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
	orParts := SplitBySeparator(s, "Or")
	var groups []ClauseGroup

	for _, part := range orParts {
		andParts := SplitBySeparator(part, "And")
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

// SplitBySeparator splits "EmailAndRole" by "And" or "EmailOrPhone" by "Or".
// Ensures separator is between two uppercase-starting parts.
func SplitBySeparator(s, sep string) []string {
	var parts []string
	for offset := 0; len(sep) > 0 && offset < len(s); {
		found := strings.Index(s[offset:], sep)
		if found < 0 {
			break
		}
		idx := offset + found
		after := idx + len(sep)
		// Verify separator is between two uppercase-starting parts
		if idx > 0 && after < len(s) {
			next, _ := utf8.DecodeRuneInString(s[after:])
			if unicode.IsUpper(next) {
				parts = append(parts, s[:idx])
				s = s[after:]
				offset = 0
				continue
			}
		}
		offset = after
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

// Singularize strips trailing 's' for simple English plurals.
func Singularize(s string) string {
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

// ParseOrderBy parses "CreatedAtDesc" → ("createdAt", true)
func ParseOrderBy(s string) (string, bool) {
	if strings.HasSuffix(s, "Desc") {
		return str.LowerFirst(s[:len(s)-4]), true
	}
	if strings.HasSuffix(s, "Asc") {
		return str.LowerFirst(s[:len(s)-3]), false
	}
	return str.LowerFirst(s), false
}

// ParseOrderExpression accepts a field or it.field with an optional asc/desc suffix.
// For it.asc/it.desc, the final result is an alternative qualified column: callers
// use it only when no stored column named it exists, preserving suffix precedence.
func ParseOrderExpression(expression ast.Expr) (string, bool, string) {
	descending := false
	if member, ok := expression.(*ast.MemberExpr); ok {
		if member.SafeCall {
			return "", false, ""
		}
		if row, ok := member.Object.(*ast.Ident); ok && row.Name == "it" {
			if member.Field == "asc" || member.Field == "desc" {
				return "it", member.Field == "desc", member.Field
			}
			return member.Field, false, ""
		}
		if member.Field != "asc" && member.Field != "desc" {
			return "", false, ""
		}
		descending = member.Field == "desc"
		expression = member.Object
	}
	switch field := expression.(type) {
	case *ast.Ident:
		return field.Name, descending, ""
	case *ast.MemberExpr:
		if row, ok := field.Object.(*ast.Ident); ok && row.Name == "it" && !field.SafeCall {
			return field.Field, descending, ""
		}
	}
	return "", false, ""
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

// Contract derives schema metadata without mutating the source declaration.
func Contract(api *ast.ApiDecl, models map[string]*ast.ModelDecl) *ast.ApiDecl {
	if !needsContract(api) {
		return api
	}
	inf := Parse(api.Name, models)
	if inf == nil {
		return api
	}
	contract := *api
	if len(contract.Params) == 0 {
		contract.Params = Parameters(inf, models[inf.ModelName])
	}
	if contract.ReturnType == nil {
		switch inf.Action {
		case "get", "list":
			contract.ReturnType = &ast.TypeRef{Name: inf.ModelName, IsList: inf.Action == "list"}
		case "exists":
			contract.ReturnType = &ast.TypeRef{Name: "Boolean"}
		case "count":
			contract.ReturnType = &ast.TypeRef{Name: "Int"}
		}
	}
	if inf.Action == "list" && inf.TopN == 0 && !hasDirective(contract.Directives, "paginate") {
		contract.Directives = append(append([]*ast.Directive(nil), contract.Directives...), &ast.Directive{Name: "paginate"})
	}
	return &contract
}

func needsContract(api *ast.ApiDecl) bool {
	return api.Body == nil && !hasDirective(api.Directives, "native") && !hasDirective(api.Directives, "stream")
}

// ModelsForAPIs avoids constructing an index when all API contracts are explicit.
func ModelsForAPIs(files []*ast.File) map[string]*ast.ModelDecl {
	for _, file := range files {
		for _, api := range file.APIs {
			if needsContract(api) {
				return Models(files)
			}
		}
	}
	return nil
}

// Parameters auto-generates parameter declarations from inferred clauses.
// Each clause that requires a parameter produces one (or two for between).
func Parameters(inf *InferredAPI, m *ast.ModelDecl) []*ast.ParamDecl {
	var params []*ast.ParamDecl
	for _, group := range inf.Groups {
		for _, clause := range group.Clauses {
			switch clause.Op {
			case "true", "false", "isNull", "isNotNull":
				// Zero-param operations
				continue

			case "between":
				fieldType := FieldType(m, clause.Field)
				params = append(params,
					&ast.ParamDecl{Name: clause.Field + "From", Type: fieldType},
					&ast.ParamDecl{Name: clause.Field + "To", Type: fieldType},
				)

			case "in", "notIn":
				// Array param: [Int], [String], etc.
				fieldType := FieldType(m, clause.Field)
				params = append(params, &ast.ParamDecl{
					Name: ParamName(clause),
					Type: &ast.TypeRef{Name: fieldType.Name, IsList: true},
				})

			case "containing", "notContaining", "startswith", "endswith", "like", "notLike", "ignoreCase":
				// String operations always take string param
				params = append(params, &ast.ParamDecl{
					Name: ParamName(clause),
					Type: &ast.TypeRef{Name: "String"},
				})

			default:
				// eq, ne, gt, gte, lt, lte — same type as field
				params = append(params, &ast.ParamDecl{
					Name: ParamName(clause),
					Type: FieldType(m, clause.Field),
				})
			}
		}
	}
	return params
}

// ParamName generates a reasonable parameter name from a clause.
func ParamName(clause InferClause) string {
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

// FieldType returns a copy of the AST TypeRef for a model field.
func FieldType(m *ast.ModelDecl, name string) *ast.TypeRef {
	for _, f := range m.Fields {
		if f.Name == name && f.Type != nil {
			return &ast.TypeRef{Name: f.Type.Name}
		}
	}
	return &ast.TypeRef{Name: "String"}
}

// InvalidSoftDeleteTimestamp checks effective fields after inheritance expansion.
// An absent field is valid: the database generator supplies an implicit timestamp.
func InvalidSoftDeleteTimestamp(model *ast.ModelDecl) *ast.FieldDecl {
	if !hasDirective(model.Directives, "soft") {
		return nil
	}
	for _, field := range model.Fields {
		if field.Name != "deletedAt" {
			continue
		}
		typ := field.Type
		if typ == nil || typ.Name != "DateTime" || !typ.Nullable || typ.IsList || field.Computed != nil {
			return field
		}
	}
	return nil
}

func hasDirective(directives []*ast.Directive, name string) bool {
	for _, directive := range directives {
		if directive.Name == name {
			return true
		}
	}
	return false
}

// Models indexes local models and extension stubs for compile-time name resolution.
func Models(files []*ast.File) map[string]*ast.ModelDecl {
	models := make(map[string]*ast.ModelDecl)
	hasInheritance := false
	for _, file := range files {
		for _, model := range file.Models {
			models[model.Name] = model
			hasInheritance = hasInheritance || len(model.Parents) != 0
		}
		for _, extension := range file.Extends {
			if models[extension.Name] == nil {
				models[extension.Name] = &ast.ModelDecl{Name: extension.Name, Fields: extension.Fields}
			}
		}
	}
	if hasInheritance {
		resolveInheritedModels(models, files)
	}
	return models
}
