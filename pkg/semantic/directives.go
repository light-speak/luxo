package semantic

import "github.com/light-speak/luxo/pkg/ast"

// DirectiveContext defines where a directive can be applied.
type DirectiveContext int

const (
	OnField      DirectiveContext = 1 << iota // field in model/interface/type/extend
	OnModel                                   // model declaration
	OnApi                                     // api declaration
	OnFn                                      // fn declaration
	OnMiddleware                              // middleware declaration
	OnEvent                                   // event declaration
	OnComputed                                // computed field (val ... get)
)

// ParamDef defines an expected parameter for a directive.
type ParamDef struct {
	Name     string // parameter name (empty = positional)
	Required bool   // whether parameter is required
}

// DirectiveDef defines a built-in directive with its validation rules.
type DirectiveDef struct {
	Name           string           // directive name without @
	Contexts       DirectiveContext // bitmask of allowed contexts
	Params         []ParamDef       // expected parameters (nil = no params)
	MaxArgs        int              // max number of args (-1 = unlimited, 0 = same as len(Params))
	TypeConstraint []string         // field type must be one of these (nil = any type)
	HasBody        bool             // whether directive allows a { body } block
	Description    string           // brief description for docs/errors
}

// contextName returns the display name for a directive context.
func contextName(ctx DirectiveContext) string {
	switch ctx {
	case OnField:
		return "field"
	case OnModel:
		return "model"
	case OnApi:
		return "api"
	case OnFn:
		return "fn"
	case OnMiddleware:
		return "middleware"
	case OnEvent:
		return "event"
	case OnComputed:
		return "computed"
	}
	return "unknown"
}

// contextNames returns a comma-separated list of allowed context names.
func contextNames(ctx DirectiveContext) string {
	var names []string
	for _, c := range []DirectiveContext{OnField, OnModel, OnApi, OnFn, OnMiddleware, OnEvent, OnComputed} {
		if ctx&c != 0 {
			names = append(names, contextName(c))
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	result := names[0]
	for i := 1; i < len(names); i++ {
		result += ", " + names[i]
	}
	return result
}

// builtinDirectives is the authoritative registry of all built-in directives.
var builtinDirectives = []DirectiveDef{
	// ===== Model-level directives =====
	{
		Name: "crud", Contexts: OnModel,
		Params:      []ParamDef{{Name: "only"}, {Name: "except"}},
		MaxArgs:     2,
		Description: "control CRUD API generation",
	},
	{
		Name: "soft", Contexts: OnModel,
		Description: "enable soft delete (deletedAt timestamp)",
	},
	{
		Name: "noTime", Contexts: OnModel,
		Description: "disable automatic createdAt/updatedAt timestamps",
	},
	{
		Name: "withAuth", Contexts: OnModel,
		Params:      []ParamDef{{Name: "stores", Required: true}, {Name: "default"}},
		MaxArgs:     2,
		HasBody:     false,
		Description: "mark model as auth subject, auto-generates .createToken()/.verify()/.refreshToken(). stores: fields to store in JWT token. default: true means @auth without args defaults to this model. JWT secret/expires/refresh configured via .env",
	},

	// ===== Field-level directives — identity & indexing =====
	{
		Name: "id", Contexts: OnField,
		Description: "mark field as primary key",
	},
	{
		Name: "unique", Contexts: OnField | OnModel,
		Description: "add unique constraint",
	},
	{
		Name: "index", Contexts: OnField | OnModel,
		Params:      []ParamDef{{Name: "fields"}},
		MaxArgs:     1,
		Description: "add database index",
	},

	// ===== Field-level directives — visibility & security =====
	{
		Name: "hidden", Contexts: OnField,
		Description: "exclude from API responses",
	},
	{
		Name: "internal", Contexts: OnField,
		Description: "server-only field, not exposed to clients",
	},
	{
		Name: "visible", Contexts: OnField,
		Params:      []ParamDef{{}}, // positional: condition expression
		MaxArgs:     1,
		HasBody:     true,
		Description: "conditionally visible based on expression",
	},
	{
		Name: "mask", Contexts: OnField,
		Params:         []ParamDef{{Name: "pattern"}},
		MaxArgs:        1,
		TypeConstraint: []string{"String"},
		Description:    "mask field value in responses (e.g., phone: 138****1234)",
	},
	{
		Name: "hash", Contexts: OnField,
		TypeConstraint: []string{"String"},
		Description:    "auto-hash value before save (bcrypt)",
	},
	{
		Name: "encrypt", Contexts: OnField,
		TypeConstraint: []string{"String"},
		Description:    "auto-encrypt value before save",
	},
	{
		Name: "immutable", Contexts: OnField,
		Description: "field cannot be changed after creation",
	},

	// ===== Field-level directives — data transformation =====
	{
		Name: "transform", Contexts: OnField,
		HasBody:     true,
		Description: "transform field value on read",
	},
	{
		Name: "beforeSave", Contexts: OnField,
		HasBody:     true,
		Description: "transform field value before save",
	},

	// ===== Field-level directives — query =====
	{
		Name: "filterable", Contexts: OnField,
		Description: "enable filtering on this field in list queries",
	},
	{
		Name: "sortable", Contexts: OnField,
		Description: "enable sorting on this field in list queries",
	},
	{
		Name: "search", Contexts: OnField,
		TypeConstraint: []string{"String"},
		Description:    "enable full-text search index",
	},
	{
		Name: "auto", Contexts: OnField,
		Description: "auto-generated value (e.g., UUID, sequence)",
	},

	// ===== Field-level directives — lifecycle =====
	{
		Name: "deprecated", Contexts: OnField | OnApi,
		Params:      []ParamDef{{Name: "reason"}},
		MaxArgs:     1,
		Description: "mark as deprecated with optional reason",
	},
	{
		Name: "reserved", Contexts: OnField,
		Description: "field name is reserved for future use",
	},

	// ===== Field-level directives — validation (String) =====
	{
		Name: "email", Contexts: OnField,
		TypeConstraint: []string{"String"},
		Description:    "validate email format",
	},
	{
		Name: "varchar", Contexts: OnField,
		Params:         []ParamDef{{}}, // positional: length
		MaxArgs:        1,
		TypeConstraint: []string{"String"},
		Description:    "set VARCHAR length",
	},
	{
		Name: "pattern", Contexts: OnField,
		Params:         []ParamDef{{}}, // positional: regex
		MaxArgs:        1,
		TypeConstraint: []string{"String"},
		Description:    "validate against regex pattern",
	},
	{
		Name: "minLength", Contexts: OnField,
		Params:         []ParamDef{{}}, // positional: min
		MaxArgs:        1,
		TypeConstraint: []string{"String"},
		Description:    "minimum string length",
	},
	{
		Name: "maxLength", Contexts: OnField,
		Params:         []ParamDef{{}}, // positional: max
		MaxArgs:        1,
		TypeConstraint: []string{"String"},
		Description:    "maximum string length",
	},
	{
		Name: "notBlank", Contexts: OnField,
		TypeConstraint: []string{"String"},
		Description:    "string must not be empty or whitespace",
	},

	// ===== Field-level directives — validation (Numeric) =====
	{
		Name: "range", Contexts: OnField,
		Params:      []ParamDef{{}}, // positional
		MaxArgs:     2,
		Description: "range constraint for numeric fields",
	},

	// ===== Field-level directives — database types =====
	{
		Name: "length", Contexts: OnField,
		Params:      []ParamDef{{}},
		MaxArgs:     1,
		Description: "set column length",
	},
	{
		Name: "serial", Contexts: OnField,
		Description: "auto-incrementing integer (SERIAL)",
	},
	{
		Name: "bigint", Contexts: OnField,
		Description: "64-bit integer (BIGINT)",
	},
	{
		Name: "smallint", Contexts: OnField,
		Description: "16-bit integer (SMALLINT)",
	},
	{
		Name: "decimal", Contexts: OnField,
		Params:      []ParamDef{{Name: "precision"}, {Name: "scale"}},
		MaxArgs:     2,
		Description: "fixed-point decimal (DECIMAL(p, s))",
	},
	{
		Name: "uuid", Contexts: OnField,
		Description: "UUID type column",
	},
	{
		Name: "inet", Contexts: OnField,
		Description: "IP address type (INET)",
	},
	{
		Name: "point", Contexts: OnField,
		Description: "geographic point type (POINT)",
	},
	{
		Name: "brin", Contexts: OnField,
		Description: "BRIN index (for time-series / append-only data)",
	},
	{
		Name: "date", Contexts: OnField,
		Description: "date-only column (DATE)",
	},
	{
		Name: "time", Contexts: OnField,
		Description: "time-only column (TIME)",
	},
	{
		Name: "vector", Contexts: OnField,
		Params:      []ParamDef{{Name: "dim"}},
		MaxArgs:     1,
		Description: "vector embedding column (pgvector)",
	},

	// ===== Computed field directives =====
	{
		Name: "count", Contexts: OnComputed,
		Description: "count aggregation",
	},
	{
		Name: "sum", Contexts: OnComputed,
		Params:      []ParamDef{{Name: "field"}},
		MaxArgs:     1,
		Description: "sum aggregation",
	},
	{
		Name: "avg", Contexts: OnComputed,
		Params:      []ParamDef{{Name: "field"}},
		MaxArgs:     1,
		Description: "average aggregation",
	},
	{
		Name: "min", Contexts: OnComputed,
		Params:      []ParamDef{{Name: "field"}},
		MaxArgs:     1,
		Description: "min aggregation",
	},
	{
		Name: "max", Contexts: OnComputed,
		Params:      []ParamDef{{Name: "field"}},
		MaxArgs:     1,
		Description: "max aggregation",
	},

	// ===== API/Fn-level directives =====
	{
		Name: "auth", Contexts: OnApi | OnFn,
		Params:      []ParamDef{{Name: "permission"}, {Name: "own"}},
		MaxArgs:     -1,
		HasBody:     false,
		Description: "require authentication, optional Model... identity, permission lambda, own resource check",
	},
	{
		Name: "native", Contexts: OnApi | OnFn | OnMiddleware,
		Description: "implementation provided by Go (not Luxo)",
	},
	{
		Name: "cache", Contexts: OnApi | OnComputed,
		Params:      []ParamDef{{Name: "ttl"}},
		MaxArgs:     1,
		Description: "cache response with TTL",
	},
	{
		Name: "rateLimit", Contexts: OnApi,
		Params:      []ParamDef{{Name: "max"}, {Name: "window"}},
		MaxArgs:     2,
		Description: "rate limit API endpoint",
	},
	{
		Name: "scope", Contexts: OnApi,
		Params:      []ParamDef{}, // positional scope names
		MaxArgs:     -1,
		Description: "apply query scope(s) to API",
	},
	{
		Name: "stream", Contexts: OnApi,
		Description: "enable streaming response",
	},
	{
		Name: "paginate", Contexts: OnApi,
		Params:      []ParamDef{{Name: "defaultPageSize"}},
		MaxArgs:     1,
		Description: "auto-inject pagination params and wrap return type in Page<T>",
	},
}

// directiveRegistry is the lookup table built from builtinDirectives.
var directiveRegistry map[string]*DirectiveDef

func init() {
	directiveRegistry = make(map[string]*DirectiveDef, len(builtinDirectives))
	for i := range builtinDirectives {
		directiveRegistry[builtinDirectives[i].Name] = &builtinDirectives[i]
	}
}

// LookupDirective returns the definition of a directive by name, or nil if unknown.
func LookupDirective(name string) *DirectiveDef {
	return directiveRegistry[name]
}

// AllDirectiveNames returns all registered directive names (for typo suggestions).
func AllDirectiveNames() []string {
	names := make([]string, 0, len(directiveRegistry))
	for name := range directiveRegistry {
		names = append(names, name)
	}
	return names
}

// validateDirective checks a directive against its definition and context.
func (a *Analyzer) validateDirective(d *ast.Directive, ctx DirectiveContext, fieldTypeName string) {
	def := LookupDirective(d.Name)
	if def == nil {
		a.warnUnknownDirective(d)
		return
	}
	if def.Contexts&ctx == 0 {
		a.addError(d.Pos, "@%s cannot be used on %s, allowed on: %s / @%s 不能用在 %s 上，允许用在：%s",
			d.Name, contextName(ctx), contextNames(def.Contexts),
			d.Name, contextName(ctx), contextNames(def.Contexts))
		return
	}
	a.checkDirectiveArgs(d, def)
	a.checkDirectiveType(d, def, fieldTypeName)
}

// warnUnknownDirective reports an unknown directive with typo suggestion.
func (a *Analyzer) warnUnknownDirective(d *ast.Directive) {
	closest := findClosestString(d.Name, AllDirectiveNames())
	if closest != "" {
		a.addWarning(d.Pos, "unknown directive '@%s', did you mean '@%s'? / 未知指令 '@%s'，你是不是想写 '@%s'？", d.Name, closest, d.Name, closest)
	} else {
		a.addWarning(d.Pos, "unknown directive '@%s' / 未知指令 '@%s'", d.Name, d.Name)
	}
}

// checkDirectiveArgs validates argument count and required params.
func (a *Analyzer) checkDirectiveArgs(d *ast.Directive, def *DirectiveDef) {
	maxArgs := def.MaxArgs
	if maxArgs == 0 {
		maxArgs = len(def.Params)
	}
	if maxArgs >= 0 && len(d.Args) > maxArgs {
		a.addError(d.Pos, "@%s expects at most %d argument(s), got %d / @%s 最多接受 %d 个参数，得到 %d 个",
			d.Name, maxArgs, len(d.Args), d.Name, maxArgs, len(d.Args))
	}
	for _, p := range def.Params {
		if !p.Required {
			continue
		}
		if !hasNamedArg(d.Args, p.Name) {
			a.addError(d.Pos, "@%s requires parameter '%s' / @%s 需要参数 '%s'",
				d.Name, p.Name, d.Name, p.Name)
		}
	}
}

// hasNamedArg checks if a named argument exists in the argument list.
func hasNamedArg(args []*ast.NamedArg, name string) bool {
	for _, arg := range args {
		if arg.Name == name {
			return true
		}
	}
	return false
}

// checkDirectiveType validates type constraints for field directives.
func (a *Analyzer) checkDirectiveType(d *ast.Directive, def *DirectiveDef, fieldTypeName string) {
	if fieldTypeName == "" {
		return
	}
	if len(def.TypeConstraint) > 0 && !stringInSlice(fieldTypeName, def.TypeConstraint) {
		a.addError(d.Pos, "@%s can only be used on %s fields, got '%s' / @%s 只能用在 %s 字段上，得到 '%s'",
			d.Name, def.TypeConstraint[0], fieldTypeName,
			d.Name, def.TypeConstraint[0], fieldTypeName)
	}
	if d.Name == "range" && fieldTypeName != "Int" && fieldTypeName != "Float" {
		a.addError(d.Pos, "@%s can only be used on numeric fields, got '%s' / @%s 只能用在数字字段上，得到 '%s'",
			d.Name, fieldTypeName, d.Name, fieldTypeName)
	}
}

// stringInSlice checks if a string exists in a slice.
func stringInSlice(s string, list []string) bool {
	for _, v := range list {
		if s == v {
			return true
		}
	}
	return false
}
