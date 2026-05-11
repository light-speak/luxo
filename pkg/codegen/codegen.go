package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// GenerateResult holds all generated Go source files.
type GenerateResult struct {
	Files map[string][]byte // filename → Go source
}

// DBDriver represents the database backend for codegen.
type DBDriver string

const (
	DriverPG     DBDriver = "pg"
	DriverMySQL  DBDriver = "mysql"
	DriverSQLite DBDriver = "sqlite"
	DriverMongo  DBDriver = "mongo"
)

// DriverImport returns the Go import path for the database driver package.
func (d DBDriver) DriverImport() string {
	switch d {
	case DriverMySQL:
		return "github.com/light-speak/luxo/pkg/lux/mysql"
	case DriverSQLite:
		return "github.com/light-speak/luxo/pkg/lux/sqlite"
	case DriverMongo:
		return "github.com/light-speak/luxo/pkg/lux/mongo"
	default:
		return "github.com/light-speak/luxo/pkg/lux/pg"
	}
}

// DriverPkg returns the short package name for the driver.
func (d DBDriver) DriverPkg() string {
	switch d {
	case DriverMySQL:
		return "mysql"
	case DriverSQLite:
		return "sqlite"
	case DriverMongo:
		return "mongo"
	default:
		return "pg"
	}
}

// dbPkg is the short package name for the database driver (pg/mysql/sqlite/mongo).
// Set by Generate(), used by all codegen functions to emit correct package references.
var dbPkg = "pg"

// eventFieldIDs maps event_name → param_name → stable field ID from luxo.lock.
var eventFieldIDs map[string]map[string]int

// modelFieldIDs maps model_name → field_name → stable field ID from luxo.lock.
var modelFieldIDs map[string]map[string]int

// apiIDs maps api_name → stable API ID from luxo.lock.
var apiIDs map[string]int

// apiParamIDs maps api_name → param_name → stable field ID from luxo.lock.
var apiParamIDs map[string]map[string]int

// apiParamTypes maps api_name → param_name → Luxo type name (from AST).
// Set by SetAPIParamTypes, used by RegisterParams codegen for accurate binary metadata.
var apiParamTypes map[string]map[string]string

// SetAPIParamTypes sets the API param type map from AST analysis.
func SetAPIParamTypes(types map[string]map[string]string) {
	apiParamTypes = types
}

// SetAPIIDs sets the API ID map from lock file data.
func SetAPIIDs(ids map[string]int) {
	apiIDs = ids
}

// SetAPIParamIDs sets the API param field ID map from lock file data.
func SetAPIParamIDs(ids map[string]map[string]int) {
	apiParamIDs = ids
}

// getAPIID returns the stable API ID, or 0 if not found.
func getAPIID(name string) int {
	if apiIDs == nil {
		return 0
	}
	return apiIDs[name]
}

// getAPIParamIDs returns param field IDs for an API, or nil.
func getAPIParamIDs(name string) map[string]int {
	if apiParamIDs == nil {
		return nil
	}
	return apiParamIDs[name]
}

// SetEventFieldIDs sets the event field ID map from lock file data.
func SetEventFieldIDs(ids map[string]map[string]int) {
	eventFieldIDs = ids
}

// SetModelFieldIDs sets the model field ID map from lock file data.
func SetModelFieldIDs(ids map[string]map[string]int) {
	modelFieldIDs = ids
}

// getModelFieldID returns the stable field ID for a model field, or 0 if not found.
func getModelFieldID(modelName, fieldName string) int {
	if modelFieldIDs == nil {
		return 0
	}
	if m, ok := modelFieldIDs[modelName]; ok {
		return m[fieldName]
	}
	return 0
}

// getEventFieldID returns the stable field ID for an event param, or 0 if not found.
func getEventFieldID(eventName, paramName string) int {
	if eventFieldIDs == nil {
		return 0
	}
	if m, ok := eventFieldIDs[eventName]; ok {
		return m[paramName]
	}
	return 0
}

// Generate produces Go source files from semantic analysis result.
// EventContext holds cross-module event information for codegen.
// Built once from all modules, passed to each module's Generate call.
type EventContext struct {
	// EventModule maps event name → defining module name
	EventModule map[string]string
	// ModulePath is the Go module import path prefix (e.g., "github.com/luxo-studio/service")
	ModulePath string
}

// BuildEventContext scans all files to build the event → module mapping.
func BuildEventContext(allFiles []*ast.File, modulePath string) *EventContext {
	ctx := &EventContext{
		EventModule: make(map[string]string),
		ModulePath:  modulePath,
	}
	for _, file := range allFiles {
		modName := moduleNameFromFile(file.Name)
		for _, ev := range file.Events {
			ctx.EventModule[ev.Name] = modName
		}
	}
	return ctx
}

// Global event context — set before Generate calls for cross-module event support
var globalEventCtx *EventContext

// SetEventContext sets the global event context for cross-module event codegen.
func SetEventContext(ctx *EventContext) {
	globalEventCtx = ctx
}

func Generate(result *semantic.Result, packageName string, driver DBDriver, softModels ...map[string]bool) *GenerateResult {
	dbPkg = driver.DriverPkg()
	gr := &GenerateResult{
		Files: make(map[string][]byte),
	}
	enums := collectEnums(result)

	gr.Files["model.gen.go"] = generateModelFile(result, packageName, enums)

	if dbSrc := generateDBFile(result, packageName, enums, driver); dbSrc != nil {
		gr.Files["db.gen.go"] = dbSrc
	}
	if appSrc := generateAppFile(result, packageName, enums, driver); appSrc != nil {
		gr.Files["app.gen.go"] = appSrc
	}
	var soft map[string]bool
	if len(softModels) > 0 {
		soft = softModels[0]
	}
	if dlSrc := generateDataLoaderFile(result, packageName, enums, soft, driver); dlSrc != nil {
		gr.Files["dataloader.gen.go"] = dlSrc
	}
	if handlerSrc := generateHandlerFile(result, packageName, enums); handlerSrc != nil {
		gr.Files["handler.gen.go"] = handlerSrc
	}
	if nativeSrc := GenerateNativeFile(result, packageName); nativeSrc != nil {
		gr.Files["native.gen.go"] = nativeSrc
	}

	// event.gen.go — typed event structs + emit functions + listener registration
	if eventSrc := generateEventFile(result, packageName); eventSrc != nil {
		gr.Files["event.gen.go"] = eventSrc
	}

	// error.gen.go — typed error constructors
	if errorSrc := generateErrorFile(result, packageName); errorSrc != nil {
		gr.Files["error.gen.go"] = errorSrc
	}

	// writejson.gen.go — per-model WriteLuxo + ReadLuxo + WriteColumnar for binary serialization
	if wjSrc := generateWriteJSONFile(result, packageName, enums); wjSrc != nil {
		gr.Files["writejson.gen.go"] = wjSrc
	}

	// service_client.gen.go — type-safe RPC client stubs for fn @service
	if scSrc := generateServiceClientFile(result, packageName); scSrc != nil {
		gr.Files["service_client.gen.go"] = scSrc
	}

	// schema.gen.go — model/API metadata for Luvia schema-driven Binary↔JSON conversion
	if schemaSrc := generateSchemaFile(result, packageName, enums); schemaSrc != nil {
		gr.Files["schema.gen.go"] = schemaSrc
	}

	return gr
}

// generateModelFile produces the model.gen.go file containing enums and structs.
func generateModelFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var b strings.Builder

	writeHeader(&b, packageName, "model.gen.go")
	writeImports(&b, result.Files)

	// enums first (structs may reference them)
	for _, file := range result.Files {
		for _, e := range file.Enums {
			generateEnum(&b, e)
			b.WriteByte('\n')
		}
	}

	modelNames := make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			modelNames[m.Name] = true
		}
	}

	// extend stubs — generate minimal structs for external types (deduplicated)
	extendDone := make(map[string]bool)
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if modelNames[ext.Name] || extendDone[ext.Name] {
				continue
			}
			extendDone[ext.Name] = true
			generateExtendStub(&b, ext)
			b.WriteByte('\n')
		}
	}

	// model structs
	for _, file := range result.Files {
		for _, m := range file.Models {
			generateModel(&b, m, enums)
			b.WriteByte('\n')
		}
	}

	// type structs (non-DB plain data types like AuthPayload, ProjectOverview)
	for _, file := range result.Files {
		for _, t := range file.Types {
			generateTypeStruct(&b, t, enums)
			b.WriteByte('\n')
		}
	}

	return []byte(b.String())
}

func writeHeader(b *strings.Builder, packageName, sourceFile string) {
	fmt.Fprintf(b, "// Code generated by luxo. DO NOT EDIT.\n")
	fmt.Fprintf(b, "// Source: %s\n\n", sourceFile)
	fmt.Fprintf(b, "package %s\n\n", packageName)
}

// modelImportNeeds tracks which imports model.gen.go requires.
type modelImportNeeds struct {
	time    bool
	uuid    bool
	decimal bool
	hash    bool
	auth    bool
}

// scanModelFieldImports checks a single field for import needs in model.gen.go.
func scanModelFieldImports(f *ast.FieldDecl, needs *modelImportNeeds) {
	if f.Computed != nil || f.Type == nil {
		return
	}
	switch f.Type.Name {
	case "DateTime", "Duration":
		needs.time = true
	case "UUID":
		needs.uuid = true
	case "Decimal":
		needs.decimal = true
	}
}

// writeImports writes import block for model.gen.go.
func writeImports(b *strings.Builder, files []*ast.File) {
	var needs modelImportNeeds
	for _, file := range files {
		for _, m := range file.Models {
			if isSoftDelete(m) && !hasDeletedAtField(m.Fields) {
				needs.time = true
			}
			for _, f := range m.Fields {
				scanModelFieldImports(f, &needs)
				if hasDirective(f.Directives, "hash") {
					needs.hash = true
				}
			}
			if hasDirective(m.Directives, "withAuth") {
				needs.auth = true
			}
		}
		// type declarations may also need imports (DateTime, UUID, Decimal)
		for _, t := range file.Types {
			for _, f := range t.Fields {
				scanModelFieldImports(f, &needs)
			}
		}
	}

	if !needs.time && !needs.uuid && !needs.decimal && !needs.hash && !needs.auth {
		return
	}

	b.WriteString("import (\n")
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	if needs.uuid {
		b.WriteString("\n\t\"github.com/google/uuid\"\n")
	}
	if needs.decimal {
		b.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	if needs.hash {
		b.WriteString("\n\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
	if needs.auth {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/auth\"\n")
	}
	b.WriteString(")\n\n")
}

// generateDBFile produces the db.gen.go file containing query builders.
// Returns nil if there are no models.
func generateDBFile(result *semantic.Result, packageName string, enums map[string]bool, driver DBDriver) []byte {
	hasModels := false
	for _, file := range result.Files {
		if len(file.Models) > 0 {
			hasModels = true
			break
		}
	}
	if !hasModels {
		return nil
	}

	var b strings.Builder

	writeHeader(&b, packageName, "db.gen.go")
	writeDBImports(&b, result.Files)

	// Collect model names to skip generating scanners for models that already exist
	modelNames := make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			modelNames[m.Name] = true
		}
	}

	generateExtendScanners(&b, result.Files, modelNames)

	for _, file := range result.Files {
		for _, m := range file.Models {
			generateQueryBuilder(&b, m, enums)
			b.WriteByte('\n')
		}
	}

	return []byte(b.String())
}

// dbImportNeeds tracks which imports db.gen.go requires.
type dbImportNeeds struct {
	time    bool
	uuid    bool
	decimal bool
}

// scanDBModelImports checks model-level directives for import needs.
func scanDBModelImports(m *ast.ModelDecl, needs *dbImportNeeds) {
	if isSoftDelete(m) {
		needs.time = true // SoftDelete uses time.Now()
	}
}

// scanDBFieldImports checks a single field for import needs in db.gen.go.
func scanDBFieldImports(f *ast.FieldDecl, needs *dbImportNeeds) {
	if f.Computed != nil || f.Type == nil {
		return
	}
	// Auto-fill needs
	if f.Type.Name == "DateTime" && (f.Name == "createdAt" || f.Name == "updatedAt") {
		needs.time = true
	}
	if hasDirective(f.Directives, "auto") && f.Type.Name == "UUID" {
		needs.uuid = true
	}
	// CreateInput struct field types
	if hasDirective(f.Directives, "internal") || isAutoManaged(f) {
		return
	}
	switch f.Type.Name {
	case "DateTime", "Duration":
		needs.time = true
	case "UUID":
		needs.uuid = true
	case "Decimal":
		needs.decimal = true
	}
}

// generateAppFile produces app.gen.go containing the App struct that wires all Clients.
// Returns nil if there are no models.
func appNeedsGeneration(result *semantic.Result) (models []string, modelSet map[string]bool, needed bool) {
	modelSet = make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			models = append(models, m.Name)
			modelSet[m.Name] = true
		}
	}
	if len(models) > 0 {
		return models, modelSet, true
	}
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "native") {
				return models, modelSet, true
			}
		}
		if len(file.Events) > 0 {
			return models, modelSet, true
		}
	}
	return models, modelSet, false
}

func appNeedsEvents(result *semantic.Result) bool {
	for _, file := range result.Files {
		if len(file.Events) > 0 || len(file.Listeners) > 0 {
			return true
		}
		for _, api := range file.APIs {
			if api.Body != nil && stmtsContainEmit(api.Body.Stmts) {
				return true
			}
		}
	}
	return false
}

// stmtsContainEmit recursively checks whether any EmitStmt exists in nested blocks.
func stmtsContainEmit(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.EmitStmt:
			return true
		case *ast.IfStmt:
			if s.Then != nil && stmtsContainEmit(s.Then.Stmts) {
				return true
			}
		case *ast.ForStmt:
			if s.Body != nil && stmtsContainEmit(s.Body.Stmts) {
				return true
			}
		case *ast.ExprStmt:
			switch expr := s.Expr.(type) {
			case *ast.TransactionExpr:
				if expr.Body != nil && stmtsContainEmit(expr.Body.Stmts) {
					return true
				}
			case *ast.AsyncExpr:
				if expr.Body != nil && stmtsContainEmit(expr.Body.Stmts) {
					return true
				}
			case *ast.AwaitExpr:
				if expr.Body != nil && stmtsContainEmit(expr.Body.Stmts) {
					return true
				}
			}
		}
	}
	return false
}

func generateAppFile(result *semantic.Result, packageName string, enums map[string]bool, driver DBDriver) []byte {
	models, _, needed := appNeedsGeneration(result)
	if !needed {
		return nil
	}

	hasRelations := false
	for _, file := range result.Files {
		for _, m := range file.Models {
			if len(analyzeRelations(m, enums)) > 0 {
				hasRelations = true
				break
			}
		}
		if len(file.Extends) > 0 {
			hasRelations = true
		}
	}

	hasEvents := appNeedsEvents(result)
	hasNativeAPIs := len(collectNativeAPIs(result)) > 0

	var b strings.Builder
	writeHeader(&b, packageName, "app.gen.go")

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	if hasEvents {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	}
	fmt.Fprintf(&b, "\tpg %q\n", driver.DriverImport())
	b.WriteString(")\n\n")

	// App struct
	b.WriteString("// App is the entry point for all database operations.\n")
	b.WriteString("type App struct {\n")
	b.WriteString("\tDB *pg.DB\n")
	if hasEvents {
		b.WriteString("\tEventBus event.Bus\n")
	}
	if hasNativeAPIs {
		b.WriteString("\tResolver NativeResolver\n")
	}
	for _, name := range models {
		fmt.Fprintf(&b, "\t%s *%sClient\n", name, name)
	}
	// Extend models are accessed via DataLoader only — no Client field.
	if hasRelations {
		b.WriteString("\tloaders Loaders\n")
	}
	b.WriteString("}\n\n")

	// New function — uses lux.DBConfigFromEnv() for all DATABASE_* config
	b.WriteString("// New creates an App by reading DATABASE_* config from the environment.\n")
	b.WriteString("// Call env.Load(\".env\") before New() if using a .env file.\n")
	b.WriteString("func New(ctx context.Context) (*App, error) {\n")
	b.WriteString("\tcfg := lux.DBConfigFromEnv()\n")
	b.WriteString("\tdb, err := pg.NewDBWithConfig(ctx, cfg.ConnectionString(), cfg)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn nil, fmt.Errorf(\"luxo: connect to database: %w\", err)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn NewFromDB(db), nil\n")
	b.WriteString("}\n\n")

	// NewFromDB function
	b.WriteString("// NewFromDB creates an App from an existing *pg.DB.\n")
	b.WriteString("func NewFromDB(db *pg.DB) *App {\n")
	b.WriteString("\treturn &App{\n")
	b.WriteString("\t\tDB: db,\n")
	for _, name := range models {
		fmt.Fprintf(&b, "\t\t%s: &%sClient{db: db},\n", name, name)
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// Close function
	b.WriteString("// Close closes the database connection.\n")
	b.WriteString("func (a *App) Close() {\n")
	b.WriteString("\ta.DB.Close()\n")
	b.WriteString("}\n\n")

	// Tx function
	b.WriteString("// Tx runs fn inside a transaction. The App passed to fn uses the transaction\n")
	b.WriteString("// for all queries. If fn returns nil, the transaction commits; otherwise it rolls back.\n")
	b.WriteString("func (a *App) Tx(ctx context.Context, fn func(tx *App) error) error {\n")
	b.WriteString("\treturn a.DB.Tx(ctx, func(txDB *pg.DB) error {\n")
	b.WriteString("\t\treturn fn(NewFromDB(txDB))\n")
	b.WriteString("\t})\n")
	b.WriteString("}\n")

	return []byte(b.String())
}

// writeDBImports writes import block for db.gen.go.
func writeDBImports(b *strings.Builder, files []*ast.File) {
	var needs dbImportNeeds
	for _, file := range files {
		for _, m := range file.Models {
			scanDBModelImports(m, &needs)
			for _, f := range m.Fields {
				scanDBFieldImports(f, &needs)
			}
		}
	}

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	fmt.Fprintf(b, "\tpg %q\n", DBDriver(dbPkg).DriverImport())
	if needs.uuid {
		b.WriteString("\n\t\"github.com/google/uuid\"\n")
	}
	if needs.decimal {
		b.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	b.WriteString(")\n\n")
}

// generateExtendScanners generates scanners for extend models (Id + data fields only).
// Used by embedded mode DataLoader to scan DB rows for cross-module models.
func generateExtendScanners(b *strings.Builder, files []*ast.File, modelNames map[string]bool) {
	seen := make(map[string]bool)
	for _, file := range files {
		for _, ext := range file.Extends {
			if modelNames[ext.Name] || seen[ext.Name] {
				continue
			}
			seen[ext.Name] = true
			fields := []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}
			for _, f := range ext.Fields {
				if f.Computed != nil || f.Type == nil || f.Type.IsList {
					continue
				}
				if isRelationType(f.Type.Name) {
					continue
				}
				fields = append(fields, f)
			}
			generateScanner(b, &ast.ModelDecl{Name: ext.Name, Fields: fields})
			b.WriteByte('\n')
		}
	}
}

// isRelationType returns true for model/enum type names (not primitive types).
func isRelationType(name string) bool {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	switch name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes":
		return false
	}
	return true
}
