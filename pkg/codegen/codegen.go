package codegen

import (
	"fmt"
	"go/format"
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

// Generate produces Go source files from semantic analysis result.
// EventContext holds cross-module event information for codegen.
// Built once from all modules, passed to each module's Generate call.
type EventContext struct {
	// EventModule maps event name → defining module name
	EventModule map[string]string
	// Events stores declarations needed when another module binds a stream.
	Events map[string]*ast.EventDecl
	// ModelModule maps model name → owning module name for cross-module RPC routing.
	ModelModule map[string]string
	// TypeModule and EnumModule track non-model wire type ownership.
	TypeModule map[string]string
	EnumModule map[string]string
	// ModelIDType maps model name → Luxo type name of its id field.
	ModelIDType map[string]string
	// ModelIDField maps model name → schema-declared primary-key field name.
	ModelIDField map[string]string
	// ModelFields records fields declared by each model's owning module. Extend
	// projections reuse these fields; only absent names are federation additions.
	ModelFields map[string]map[string]bool
	// remoteLoadCalls maps the owning module to named load patterns used by
	// another module. The owner uses these to generate internal RPC endpoints.
	remoteLoadCalls map[string][]loadCallInfo
	// remotePKModels marks models referenced by cross-module extend declarations.
	remotePKModels map[string]bool
	// ModulePath is the Go module import path prefix (e.g., "github.com/luxo-studio/service")
	ModulePath string
}

// BuildEventContext scans all files to build the event → module mapping.
func BuildEventContext(allFiles []*ast.File, modulePath string) *EventContext {
	ctx := &EventContext{
		EventModule:     make(map[string]string),
		Events:          make(map[string]*ast.EventDecl),
		ModelModule:     make(map[string]string),
		TypeModule:      make(map[string]string),
		EnumModule:      make(map[string]string),
		ModelIDType:     make(map[string]string),
		ModelIDField:    make(map[string]string),
		ModelFields:     make(map[string]map[string]bool),
		remoteLoadCalls: make(map[string][]loadCallInfo),
		remotePKModels:  make(map[string]bool),
		ModulePath:      modulePath,
	}
	for _, file := range allFiles {
		modName := moduleNameFromFile(file.Name)
		for _, model := range file.Models {
			ctx.ModelModule[model.Name] = modName
			ctx.ModelIDType[model.Name] = modelIDTypeName(model)
			ctx.ModelIDField[model.Name] = primaryKeyFieldName(model)
			fields := make(map[string]bool, len(model.Fields))
			for _, field := range model.Fields {
				fields[field.Name] = true
			}
			ctx.ModelFields[model.Name] = fields
		}
		for _, ev := range file.Events {
			ctx.EventModule[ev.Name] = modName
			ctx.Events[ev.Name] = ev
		}
		for _, declaration := range file.Types {
			ctx.TypeModule[declaration.Name] = modName
		}
		for _, enum := range file.Enums {
			ctx.EnumModule[enum.Name] = modName
		}
	}
	for _, file := range allFiles {
		sourceModule := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			if owner := ctx.ModelModule[ext.Name]; owner != "" && owner != sourceModule {
				ctx.remotePKModels[ext.Name] = true
			}
		}
	}
	seenLoads := make(map[string]bool)
	for _, call := range collectLoadCalls(&semantic.Result{Files: allFiles}) {
		if len(call.argNames) == 0 {
			continue
		}
		owner := ctx.ModelModule[call.modelName]
		if owner == "" || owner == call.sourceModule {
			continue
		}
		key := loadServiceName(call)
		if seenLoads[key] {
			continue
		}
		seenLoads[key] = true
		ctx.remoteLoadCalls[owner] = append(ctx.remoteLoadCalls[owner], call)
	}
	return ctx
}

func modelIDTypeName(model *ast.ModelDecl) string {
	if field := primaryKeyField(model); field != nil {
		return field.Type.Name
	}
	return "Int"
}

// Generate creates a generator for one compilation and returns every generated
// source file. Callers that need stable wire IDs or cross-module metadata should
// construct a GeneratorContext with NewGenerator and call its Generate method.
func Generate(result *semantic.Result, packageName string, driver DBDriver, softModels ...map[string]bool) (*GenerateResult, error) {
	generator, err := NewGenerator(GeneratorConfig{Driver: driver})
	if err != nil {
		return nil, err
	}
	var soft map[string]bool
	if len(softModels) > 0 {
		soft = softModels[0]
	}
	return generator.Generate(result, packageName, soft)
}

func (g *GeneratorContext) Generate(result *semantic.Result, packageName string, softModels map[string]bool) (*GenerateResult, error) {
	gr := &GenerateResult{
		Files: make(map[string][]byte),
	}
	enums := collectEnums(result)

	gr.Files["model.gen.go"] = g.generateModelFile(result, packageName, enums)

	if dbSrc := g.generateDBFile(result, packageName, enums); dbSrc != nil {
		gr.Files["db.gen.go"] = dbSrc
	}
	if appSrc := generateAppFile(result, packageName, enums, g.driver); appSrc != nil {
		gr.Files["app.gen.go"] = appSrc
	}
	if dlSrc := g.generateDataLoaderFile(result, packageName, enums, softModels); dlSrc != nil {
		gr.Files["dataloader.gen.go"] = dlSrc
	}
	if handlerSrc := g.generateHandlerFile(result, packageName, enums); handlerSrc != nil {
		gr.Files["handler.gen.go"] = handlerSrc
	}
	if nativeSrc := GenerateNativeFile(result, packageName); nativeSrc != nil {
		gr.Files["native.gen.go"] = nativeSrc
	}

	// event.gen.go — typed event structs + emit functions + listener registration
	if eventSrc := g.generateEventFile(result, packageName); eventSrc != nil {
		gr.Files["event.gen.go"] = eventSrc
	}
	if streamSrc := g.generateStreamFile(result, packageName); streamSrc != nil {
		gr.Files["stream.gen.go"] = streamSrc
	}

	// error.gen.go — typed error constructors
	if errorSrc := generateErrorFile(result, packageName); errorSrc != nil {
		gr.Files["error.gen.go"] = errorSrc
	}

	// writejson.gen.go — per-model WriteLuxo + ReadLuxo + WriteColumnar for binary serialization
	if wjSrc := g.generateWriteJSONFile(result, packageName, enums); wjSrc != nil {
		gr.Files["writejson.gen.go"] = wjSrc
	}

	// schema.gen.go — model/API metadata for Luvia schema-driven Binary↔JSON conversion
	if schemaSrc := g.generateSchemaFile(result, packageName, enums); schemaSrc != nil {
		gr.Files["schema.gen.go"] = schemaSrc
	}

	for name, src := range gr.Files {
		formatted, err := formatGeneratedChecked(src)
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", name, err)
		}
		gr.Files[name] = formatted
	}
	return gr, nil
}

// formatGenerated runs gofmt (go/format) on generated Go source so emitted
// files pass the repo-wide gofmt gate without a separate formatting step.
// On a format error — which would mean the generator produced invalid Go —
// the raw source is returned so the compiler surfaces the real error with
// meaningful line numbers instead of a swallowed formatting failure.
func formatGenerated(src []byte) []byte {
	formatted, err := formatGeneratedChecked(src)
	if err != nil {
		return src
	}
	return formatted
}

func formatGeneratedChecked(src []byte) ([]byte, error) {
	formatted, err := format.Source(src)
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

// generateModelFile produces the model.gen.go file containing enums and structs.
func generateModelFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	return defaultGenerator().generateModelFile(result, packageName, enums)
}

func (g *GeneratorContext) generateModelFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var b strings.Builder

	writeHeader(&b, packageName, "model.gen.go")
	g.writeImports(&b, result.Files)

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
			g.generateExtendStub(&b, ext)
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
	json    bool
	uuid    bool
	decimal bool
	hash    bool
	auth    bool
}

// scanModelFieldImports checks a single field for import needs in model.gen.go.
func scanModelFieldImports(f *ast.FieldDecl, needs *modelImportNeeds) {
	if f.Type == nil {
		return
	}
	switch f.Type.Name {
	case "DateTime", "Duration":
		needs.time = true
	case "UUID":
		needs.uuid = true
	case "Decimal":
		needs.decimal = true
	case "JSON":
		needs.json = true
	}
}

// writeImports writes import block for model.gen.go.
func writeImports(b *strings.Builder, files []*ast.File) {
	defaultGenerator().writeImports(b, files)
}

func (g *GeneratorContext) writeImports(b *strings.Builder, files []*ast.File) {
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
		for _, ext := range file.Extends {
			switch g.externalModelIDTypeName(ext.Name) {
			case "UUID":
				needs.uuid = true
			}
		}
	}

	if !needs.time && !needs.json && !needs.uuid && !needs.decimal && !needs.hash && !needs.auth {
		return
	}

	b.WriteString("import (\n")
	// stdlib group
	if needs.auth {
		b.WriteString("\t\"fmt\"\n")
		b.WriteString("\t\"os\"\n")
	}
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	if needs.json {
		b.WriteString("\t\"encoding/json\"\n")
	}
	// third-party group
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
func (g *GeneratorContext) generateDBFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
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
	g.writeDBImports(&b, result.Files)

	// Collect model names to skip generating scanners for models that already exist
	modelNames := make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			modelNames[m.Name] = true
		}
	}

	g.generateExtendScanners(&b, result.Files, modelNames)

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
	json    bool
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
	case "JSON":
		needs.json = true
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
	hasNativeAPIs := len(collectNativeAPIs(result)) > 0 || resultHasNativeStreams(result)

	var b strings.Builder
	writeHeader(&b, packageName, "app.gen.go")

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	if hasEvents {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/queue\"\n")
	fmt.Fprintf(&b, "\tpg %q\n", driver.DriverImport())
	b.WriteString(")\n\n")

	// App struct
	b.WriteString("// App is the entry point for all database operations.\n")
	b.WriteString("type App struct {\n")
	b.WriteString("\tDB *pg.DB\n")
	if hasEvents {
		b.WriteString("\tEventBus event.Bus\n")
	}
	b.WriteString("\tQueue queue.Queue\n")
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
	defaultGenerator().writeDBImports(b, files)
}

func (g *GeneratorContext) writeDBImports(b *strings.Builder, files []*ast.File) {
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
	if needs.json {
		b.WriteString("\t\"encoding/json\"\n")
	}
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	fmt.Fprintf(b, "\tpg %q\n", g.driver.DriverImport())
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
func (g *GeneratorContext) generateExtendScanners(b *strings.Builder, files []*ast.File, modelNames map[string]bool) {
	seen := make(map[string]bool)
	for _, file := range files {
		for _, ext := range file.Extends {
			if modelNames[ext.Name] || seen[ext.Name] {
				continue
			}
			seen[ext.Name] = true
			idFieldName := g.externalModelIDFieldName(ext.Name)
			fields := []*ast.FieldDecl{{Name: idFieldName, Type: &ast.TypeRef{Name: g.externalModelIDTypeName(ext.Name)}, Directives: []*ast.Directive{{Name: "id"}}}}
			for _, f := range ext.Fields {
				if f.Name == idFieldName || f.Computed != nil || f.Type == nil || f.Type.IsList {
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
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON":
		return false
	}
	return true
}
