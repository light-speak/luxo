package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateSchemaFile produces schema.gen.go containing RegisterSchema
// that registers model and API metadata with the Luvia schema registry.
// This enables schema-driven Binary↔JSON conversion at the Luvia layer.
type schemaAPIInfo struct {
	name            string
	moduleName      string
	params          []*ast.ParamDecl
	optionalParams  map[string]bool
	returnType      *ast.TypeRef
	paginated       bool
	defaultPageSize int
	stream          bool
	internal        bool
	directives      []*ast.Directive
	description     string
	query           *schema.QueryCapabilities
}

func generateSchemaFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	return defaultGenerator().generateSchemaFile(result, packageName, enums)
}

func (g *GeneratorContext) generateSchemaFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	modelOwner, modelFields := g.schemaModelOwnership(result)
	for _, file := range result.Files {
		models = append(models, file.Models...)
	}

	// Collect extend stubs + build per-model extend field→module map
	// extendFieldModules[modelName][fieldName] = sourceModule
	extendFieldModules := make(map[string]map[string]string)
	modelNames := make(map[string]bool)
	for _, m := range models {
		modelNames[m.Name] = true
	}
	var stubs []*ast.ModelDecl
	stubByName := make(map[string]*ast.ModelDecl)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			// Record extend field → module mapping (only cross-module)
			owner := modelOwner[ext.Name]
			if modName != owner {
				if extendFieldModules[ext.Name] == nil {
					extendFieldModules[ext.Name] = make(map[string]string)
				}
				for _, f := range ext.Fields {
					if !modelFields[ext.Name][f.Name] {
						extendFieldModules[ext.Name][f.Name] = modName
					}
				}
			}
			stubs = appendSchemaStub(stubs, stubByName, modelNames, ext)
		}
	}

	apis := g.collectSchemaAPIsWithOwnership(result, enums, modelOwner, modelFields)

	declarationCount := 0
	for _, file := range result.Files {
		declarationCount += len(file.Types) + len(file.Enums)
	}
	if len(models) == 0 && len(stubs) == 0 && len(apis) == 0 && declarationCount == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "schema.gen.go")
	b.WriteString("import \"github.com/light-speak/luxo/pkg/lux/schema\"\n\n")

	b.WriteString("// RegisterSchema registers all model and API metadata with the schema registry.\n")
	b.WriteString("// Used by Luvia for schema-driven Binary↔JSON conversion.\n")
	b.WriteString("func RegisterSchema(s *schema.Schema) error {\n")

	// Register models
	allModels := append(models, stubs...)
	for _, m := range allModels {
		g.writeModelRegistration(&b, m, modelOwner[m.Name], enums, extendFieldModules[m.Name])
	}

	// Register APIs
	for _, api := range apis {
		g.writeAPIRegistrationSchema(&b, api, enums)
	}
	writeExecutionCacheRegistration(&b, result)

	// Register type declarations (non-DB types like AuthPayload)
	for _, file := range result.Files {
		moduleName := moduleNameFromFile(file.Name)
		for _, t := range file.Types {
			g.writeTypeRegistration(&b, t, moduleName, enums)
		}
		for _, enumDecl := range file.Enums {
			writeEnumRegistration(&b, enumDecl, moduleName)
		}
	}
	b.WriteString("\ts.InferTypeUsage()\n")

	b.WriteString("\treturn nil\n}\n")

	return []byte(b.String())
}

func appendSchemaStub(stubs []*ast.ModelDecl, byName map[string]*ast.ModelDecl, names map[string]bool, ext *ast.ExtendDecl) []*ast.ModelDecl {
	if !names[ext.Name] {
		stub := &ast.ModelDecl{Name: ext.Name, Fields: append([]*ast.FieldDecl(nil), ext.Fields...)}
		stubs = append(stubs, stub)
		byName[ext.Name], names[ext.Name] = stub, true
	} else if stub := byName[ext.Name]; stub != nil {
		for _, field := range ext.Fields {
			if modelFieldByName(stub, field.Name) == nil {
				stub.Fields = append(stub.Fields, field)
			}
		}
	}
	return stubs
}

// buildCrudAPIInfo constructs schemaAPIInfo for a single CRUD operation.
// collectSchemaAPIs collects all API metadata (CRUD + compiled + service + batchLoad + resolve).
func (g *GeneratorContext) collectSchemaAPIs(result *semantic.Result, enums map[string]bool) []schemaAPIInfo {
	owners, fields := g.schemaModelOwnership(result)
	return g.collectSchemaAPIsWithOwnership(result, enums, owners, fields)
}

func (g *GeneratorContext) collectSchemaAPIsWithOwnership(result *semantic.Result, enums map[string]bool, modelOwner map[string]string, modelFields map[string]map[string]bool) []schemaAPIInfo {
	var apis []schemaAPIInfo
	modelMap := inferred.ModelsForAPIs(result.Files)
	explicit := explicitAPINames(result)
	seenResolvers := make(map[[2]string]bool)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if hasCrud(m) {
				for _, op := range crudOperations(m) {
					if explicit[ast.CRUDAPIName(m.Name, op)] {
						continue
					}
					apis = append(apis, buildCrudAPIInfo(m, op, modName, enums))
				}
			}
			if hasCrud(m) || g.events != nil && g.events.remotePKModels[m.Name] {
				apis = append(apis, schemaAPIInfo{
					name: "svc:batchLoad:" + m.Name, moduleName: modName, internal: true,
					params: []*ast.ParamDecl{{
						Name: "keys", Type: &ast.TypeRef{Name: modelIDTypeName(m), IsList: true},
						Doc: "Primary keys to resolve in one batch.",
					}},
					returnType:  &ast.TypeRef{Name: m.Name, IsList: true},
					description: fmt.Sprintf("Batch-load %s records for cross-service field resolution.", m.Name),
				})
			}
		}
		for _, api := range file.APIs {
			api = inferredAPIContract(api, modelMap)
			paginated := hasDirective(api.Directives, "paginate")
			apis = append(apis, schemaAPIInfo{
				name: api.Name, moduleName: modName,
				params: api.EffectiveParams(), returnType: api.ReturnType,
				paginated: paginated, defaultPageSize: paginationDefaultPageSize(api, paginated),
				stream: hasDirective(api.Directives, "stream"), directives: api.Directives, description: api.Doc,
			})
		}
		for _, fn := range file.Functions {
			if hasDirective(fn.Directives, "service") {
				apis = append(apis, schemaAPIInfo{
					name: "svc:" + fn.Name, moduleName: modName, internal: true,
					params: fn.Params, returnType: fn.ReturnType, description: fn.Doc,
				})
			}
		}
		for _, ext := range file.Extends {
			if modelOwner[ext.Name] == modName {
				continue
			}
			for _, f := range ext.Fields {
				if f.Type == nil || modelFields[ext.Name][f.Name] || !isRelationField(f, enums) {
					continue
				}
				fk := g.inferFederationForeignKey(&ast.ModelDecl{Name: ext.Name}, f)
				name := "svc:resolve:" + f.Type.Name + ":" + fk
				key := [2]string{modName, name}
				if seenResolvers[key] {
					continue
				}
				seenResolvers[key] = true
				apis = append(apis, schemaAPIInfo{
					name: name, moduleName: modName, internal: true,
					params: []*ast.ParamDecl{{
						Name: "keys", Type: &ast.TypeRef{Name: g.externalModelIDTypeName(ext.Name), IsList: true},
						Doc: "Foreign keys to resolve in one batch.",
					}, {Name: "shape", Type: &ast.TypeRef{Name: "Int"}, Doc: "Canonical result shape: bit 0 is list, bit 1 nullable."}},
					returnType:  &ast.TypeRef{Name: f.Type.Name, IsList: true},
					description: fmt.Sprintf("Resolve %s.%s across services.", ext.Name, f.Name),
				})
			}
		}
	}
	if len(result.Files) > 0 && g.events != nil {
		moduleName := moduleNameFromFile(result.Files[0].Name)
		for _, call := range g.events.remoteLoadCalls[moduleName] {
			params := make([]*ast.ParamDecl, len(call.argNames))
			for i, argName := range call.argNames {
				params[i] = &ast.ParamDecl{Name: argName, Type: &ast.TypeRef{Name: call.argTypeNames[i], IsList: true}, Doc: "Join keys to resolve in one batch."}
			}
			apis = append(apis, schemaAPIInfo{
				name: loadServiceName(call), moduleName: moduleName, internal: true,
				params: params, returnType: &ast.TypeRef{Name: call.modelName, IsList: true},
				description: fmt.Sprintf("Load %s records from the owning service.", call.modelName),
			})
		}
	}
	return apis
}

func buildCrudAPIInfo(model *ast.ModelDecl, op, modName string, enums map[string]bool) schemaAPIInfo {
	modelName := model.Name
	apiName := ast.CRUDAPIName(modelName, op)
	ai := schemaAPIInfo{name: apiName, moduleName: modName, description: crudAPIDescription(modelName, op)}
	idType := &ast.TypeRef{Name: "Int"}
	if field := primaryKeyField(model); field != nil {
		idType = &ast.TypeRef{Name: field.Type.Name}
	}
	idParam := []*ast.ParamDecl{{Name: "id", Type: idType, Doc: fmt.Sprintf("Primary key of the %s record.", modelName)}}
	switch op {
	case "get":
		ai.returnType = &ast.TypeRef{Name: modelName}
		ai.params = idParam
	case "list":
		ai.query = modelQueryCapabilities(model, enums)
		ai.returnType = &ast.TypeRef{Name: modelName, IsList: true}
		ai.paginated = true
		ai.defaultPageSize = ast.DefaultPaginationPageSize
		ai.params = []*ast.ParamDecl{
			{Name: "page", Type: &ast.TypeRef{Name: "Int"}, Doc: "One-based page number."},
			{Name: "pageSize", Type: &ast.TypeRef{Name: "Int"}, Doc: "Maximum records returned per page."},
		}
		ai.optionalParams = map[string]bool{"page": true, "pageSize": true}
	case "create":
		ai.returnType = &ast.TypeRef{Name: modelName}
		ai.params = crudParamDecls(model, enums, false)
		ai.optionalParams = crudOptionalParams(ai.params, false)
	case "update":
		ai.returnType = &ast.TypeRef{Name: modelName}
		ai.params = append(idParam, crudParamDecls(model, enums, true)...)
		ai.optionalParams = crudOptionalParams(ai.params, true)
	case "delete":
		ai.returnType = &ast.TypeRef{Name: "Int"}
		ai.params = idParam
	case "deleteMany":
		ai.returnType = &ast.TypeRef{Name: "Int"}
		ai.params = []*ast.ParamDecl{{Name: "ids", Type: &ast.TypeRef{Name: idType.Name, IsList: true}, Doc: fmt.Sprintf("Primary keys of the %s records to delete.", modelName)}}
	}
	return ai
}

func crudAPIDescription(modelName, operation string) string {
	switch operation {
	case "get":
		return fmt.Sprintf("Fetch one %s record by primary key.", modelName)
	case "list":
		return fmt.Sprintf("List %s records with pagination.", modelName)
	case "create":
		return fmt.Sprintf("Create one %s record.", modelName)
	case "update":
		return fmt.Sprintf("Update one %s record by primary key.", modelName)
	case "delete":
		return fmt.Sprintf("Delete one %s record by primary key.", modelName)
	default:
		return fmt.Sprintf("Delete multiple %s records by primary key.", modelName)
	}
}

func crudOptionalParams(params []*ast.ParamDecl, update bool) map[string]bool {
	optional := make(map[string]bool)
	for _, param := range params {
		if param.Name != "id" && (update || param.Default != nil || (param.Type != nil && param.Type.Nullable)) {
			optional[param.Name] = true
		}
	}
	return optional
}

func crudParamDecls(model *ast.ModelDecl, enums map[string]bool, update bool) []*ast.ParamDecl {
	params := make([]*ast.ParamDecl, 0, len(model.Fields))
	for _, field := range model.Fields {
		if !isCRUDInputField(field, model, enums, update) {
			continue
		}
		params = append(params, &ast.ParamDecl{Name: field.Name, Type: field.Type, Default: field.Default, Doc: field.Doc})
	}
	return params
}

// writeModelRegistration generates schema.RegisterModel for one model.
// extendModules maps fieldName → source module for extend fields.
func (g *GeneratorContext) writeModelRegistration(b *strings.Builder, m *ast.ModelDecl, moduleName string, enums map[string]bool, extendModules map[string]string) {
	name := m.Name
	fmt.Fprintf(b, "\tif err := s.RegisterModel(&schema.Model{\n")
	fmt.Fprintf(b, "\t\tName: %q,\n", name)
	if moduleName != "" {
		fmt.Fprintf(b, "\t\tModule: %q,\n", moduleName)
	}
	writeSchemaDescription(b, "\t\t", m.Doc)
	writeSchemaDirectives(b, "\t\t", m.Directives)
	modelRelations := g.analyzeRelations(m, enums)
	g.writeComputedSources(b, m, modelRelations, moduleName)
	fmt.Fprintf(b, "\t\tFields: []schema.Field{\n")
	relations := relationMap(modelRelations)

	for _, f := range m.Fields {
		if f.Type == nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}

		fieldID := g.modelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		relation, isRelation := relations[f.Name]

		// Scalar fields: write type info for Binary↔JSON
		if !isRelation {
			fieldType := luxoTypeToSchemaType(f.Type.Name, enums)
			fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q, Nullable: %v, IsList: %v, Computed: %v, PrimaryKey: %v",
				fieldID, f.Name, fieldType, f.Type.Name, f.Type.Nullable, f.Type.IsList, f.Computed != nil, f.Name == primaryKeyFieldName(m))
			writeInlineFieldMetadata(b, f)
			b.WriteString("},\n")
			continue
		}

		// Relation fields: include for federation (Module + ForeignKey)
		fieldType := "FieldModel"
		module := extendModules[f.Name]
		fk := ""
		if module != "" {
			fk = g.inferFederationForeignKey(m, f)
		}
		fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q, Nullable: %v, IsList: %v, Relation: true, Module: %q, ForeignKey: %q, RelationKind: schema.%s, LocalKey: %q, RemoteKey: %q, TargetModule: %q",
			fieldID, f.Name, fieldType, f.Type.Name, f.Type.Nullable, f.Type.IsList, module, fk, schemaRelationKindName(relation.Type), relation.LocalKey, relation.RemoteKey, g.remoteModelModule(relation.TargetName))
		writeInlineFieldMetadata(b, f)
		b.WriteString("},\n")
	}

	fmt.Fprintf(b, "\t\t},\n")
	fmt.Fprintf(b, "\t}); err != nil { return err }\n")
}

// writeTypeRegistration generates schema.RegisterType for a type declaration.
func (g *GeneratorContext) writeTypeRegistration(b *strings.Builder, t *ast.TypeDecl, moduleName string, enums map[string]bool) {
	fmt.Fprintf(b, "\tif err := s.RegisterType(&schema.TypeDecl{\n")
	fmt.Fprintf(b, "\t\tName: %q,\n", t.Name)
	fmt.Fprintf(b, "\t\tModule: %q,\n", moduleName)
	writeSchemaDescription(b, "\t\t", t.Doc)
	fmt.Fprintf(b, "\t\tFields: []schema.Field{\n")

	for _, f := range t.Fields {
		if f.Type == nil {
			continue
		}
		fieldID := g.modelFieldID(t.Name, f.Name)
		if fieldID == 0 {
			continue
		}
		relation := isRelationField(f, enums)
		fieldType := luxoTypeToSchemaType(f.Type.Name, enums)
		if relation {
			// Nested type/model reference — the converter dispatches blob
			// decoding on Type==FieldModel; FieldString would misread the
			// blob as a scalar string array.
			fieldType = "FieldModel"
		}
		fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q, Nullable: %v, IsList: %v, Relation: %v",
			fieldID, f.Name, fieldType, f.Type.Name, f.Type.Nullable, f.Type.IsList, relation)
		writeInlineFieldMetadata(b, f)
		b.WriteString("},\n")
	}

	fmt.Fprintf(b, "\t\t},\n")
	fmt.Fprintf(b, "\t}); err != nil { return err }\n")
}

func writeEnumRegistration(b *strings.Builder, enumDecl *ast.EnumDecl, moduleName string) {
	fmt.Fprintf(b, "\ts.RegisterEnum(&schema.Enum{Name: %q, Module: %q, Description: %q, Values: []string{", enumDecl.Name, moduleName, enumDecl.Doc)
	for index, value := range enumDecl.Values {
		if index > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", value)
	}
	b.WriteString("}})\n")
}

func relationMap(relations []Relation) map[string]Relation {
	result := make(map[string]Relation, len(relations))
	for _, relation := range relations {
		result[relation.FieldName] = relation
	}
	return result
}

func schemaRelationKindName(kind RelationType) string {
	switch kind {
	case BelongsTo:
		return "RelationBelongsTo"
	case HasMany:
		return "RelationHasMany"
	default:
		return "RelationHasOne"
	}
}

func schemaRelationKind(kind RelationType) schema.RelationKind {
	switch kind {
	case BelongsTo:
		return schema.RelationBelongsTo
	case HasMany:
		return schema.RelationHasMany
	default:
		return schema.RelationHasOne
	}
}

func schemaDirectiveNames(directives []*ast.Directive) []string {
	if len(directives) == 0 {
		return nil
	}
	names := make([]string, len(directives))
	for index, directive := range directives {
		names[index] = directive.Name
	}
	return names
}

func writeSchemaDescription(b *strings.Builder, indent, description string) {
	if description != "" {
		fmt.Fprintf(b, "%sDescription: %q,\n", indent, description)
	}
}

func writeSchemaDirectives(b *strings.Builder, indent string, directives []*ast.Directive) {
	if len(directives) == 0 {
		return
	}
	fmt.Fprintf(b, "%sDirectives: []string{", indent)
	for index, directive := range directives {
		if index > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", directive.Name)
	}
	b.WriteString("},\n")
}

func writeInlineFieldMetadata(b *strings.Builder, field *ast.FieldDecl) {
	if field.Doc != "" {
		fmt.Fprintf(b, ", Description: %q", field.Doc)
	}
	if len(field.Directives) == 0 {
		return
	}
	b.WriteString(", Directives: []string{")
	for index, directive := range field.Directives {
		if index > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", directive.Name)
	}
	b.WriteString("}")
}

// writeAPIRegistrationSchema generates schema.RegisterAPI for one API.
func (g *GeneratorContext) writeAPIRegistrationSchema(b *strings.Builder, api schemaAPIInfo, enums map[string]bool) {
	apiID := g.apiID(api.name)
	fmt.Fprintf(b, "\tif err := s.RegisterAPI(&schema.API{\n")
	fmt.Fprintf(b, "\t\tID: %d, Name: %q, Module: %q,\n", apiID, api.name, api.moduleName)
	fmt.Fprintf(b, "\t\tInternal: %v,\n", api.internal)
	writeSchemaDescription(b, "\t\t", api.description)
	writeSchemaDirectives(b, "\t\t", api.directives)
	writeQueryCapabilities(b, api.query)
	if api.returnType != nil {
		fmt.Fprintf(b, "\t\tReturnType: %q, ReturnList: %v, ReturnNullable: %v,\n", api.returnType.Name, api.returnType.IsList, api.returnType.Nullable)
	}
	if api.paginated {
		fmt.Fprintf(b, "\t\tPaginated: true,\n")
		fmt.Fprintf(b, "\t\tDefaultPageSize: %d,\n", api.defaultPageSize)
	}
	if api.stream {
		fmt.Fprintf(b, "\t\tStream: true,\n")
	}
	writeAPIDeprecationSchema(b, api.directives)
	if len(api.params) > 0 {
		fmt.Fprintf(b, "\t\tParams: []schema.Param{\n")
		for _, p := range api.params {
			g.writeAPIParamSchema(b, api.name, p, enums, api.optionalParams[p.Name])
		}
		fmt.Fprintf(b, "\t\t},\n")
	}
	fmt.Fprintf(b, "\t}); err != nil { return err }\n")
}

func writeAPIDeprecationSchema(b *strings.Builder, directives []*ast.Directive) {
	for _, d := range directives {
		if d.Name != "deprecated" {
			continue
		}
		fmt.Fprintf(b, "\t\tDeprecated: true,\n")
		if len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				fmt.Fprintf(b, "\t\tDeprecatedReason: %q,\n", lit.Value)
			}
		}
	}
}

func (g *GeneratorContext) writeAPIParamSchema(b *strings.Builder, name string, p *ast.ParamDecl, enums map[string]bool, optional bool) {
	paramID := g.apiParamID(name, p.Name)
	pType := "FieldString"
	typeName := "String"
	isList := false
	nullable := false
	if p.Type != nil {
		typeName = p.Type.Name
		pType = luxoParamToSchemaType(typeName, enums)
		isList = p.Type.IsList
		nullable = p.Type.Nullable
	}
	fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q", paramID, p.Name, pType, typeName)
	if isList {
		b.WriteString(", IsList: true")
	}
	if nullable {
		b.WriteString(", Nullable: true")
	}
	if p.Default != nil || optional {
		b.WriteString(", HasDefault: true")
	}
	if description := schemaParamDescription(p); description != "" {
		fmt.Fprintf(b, ", Description: %q", description)
	}
	b.WriteString("},\n")
}

func schemaParamDescription(param *ast.ParamDecl) string {
	if param.Doc != "" {
		return param.Doc
	}
	switch param.Name {
	case "page":
		return "One-based page number."
	case "pageSize":
		return "Maximum records returned per page."
	default:
		return ""
	}
}

func luxoParamToSchemaType(typeName string, enums map[string]bool) string {
	return luxoTypeToSchemaType(typeName, enums)
}

// luxoTypeToSchemaType maps Luxo type name to schema.FieldType constant name.
func luxoTypeToSchemaType(typeName string, enums map[string]bool) string {
	if enums != nil && enums[typeName] {
		return "FieldEnum"
	}
	switch typeName {
	case "Int":
		return "FieldInt"
	case "Float":
		return "FieldFloat"
	case "String":
		return "FieldString"
	case "Boolean":
		return "FieldBool"
	case "DateTime":
		return "FieldDateTime"
	case "Duration":
		return "FieldDuration"
	case "Bytes":
		return "FieldBytes"
	case "UUID":
		return "FieldUUID"
	case "Decimal":
		return "FieldDecimal"
	case "JSON":
		return "FieldJSON"
	default:
		return "FieldModel"
	}
}

// BuildSchemaJSON builds the runtime Schema and serializes to JSON.
// Used by `luxo gen` to export luxo.schema.json for SDK tooling.
func BuildSchemaJSON(result *semantic.Result, enums map[string]bool, config GeneratorConfig) ([]byte, error) {
	generator, err := NewGenerator(config)
	if err != nil {
		return nil, err
	}
	return generator.BuildSchemaJSON(result, enums)
}

func (g *GeneratorContext) BuildSchemaJSON(result *semantic.Result, enums map[string]bool) ([]byte, error) {
	copy := *result
	copy.Files = inferred.ExpandedFiles(result.Files)
	result = &copy
	declaredEnums := collectEnums(result)
	for name, enabled := range enums {
		if enabled {
			declaredEnums[name] = true
		}
	}
	enums = g.allEnums(declaredEnums)
	if err := g.validateRegisteredParameters(result, enums); err != nil {
		return nil, err
	}
	if err := g.validateFieldIdentifiers(result); err != nil {
		return nil, err
	}
	s := schema.New()
	if err := g.buildSchemaModels(s, result, enums); err != nil {
		return nil, err
	}
	if err := g.buildSchemaAPIs(s, result, enums); err != nil {
		return nil, err
	}
	buildSchemaEnums(s, result)
	if err := g.buildSchemaTypes(s, result, enums); err != nil {
		return nil, err
	}
	s.InferTypeUsage()
	return s.ToJSON()
}

func (g *GeneratorContext) buildSchemaModels(s *schema.Schema, result *semantic.Result, enums map[string]bool) error {
	modelModule := make(map[string]string)
	modelDecls := make(map[string]*ast.ModelDecl)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			modelModule[m.Name] = modName
			modelDecls[m.Name] = m
			if err := s.RegisterModel(g.schemaModelFromDecl(m, modName, enums)); err != nil {
				return err
			}
		}
	}

	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			stub := &schema.Model{Name: ext.Name, Module: modelModule[ext.Name]}
			owner := modelDecls[ext.Name]
			extendModel := &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields}
			relations := relationMap(g.analyzeRelations(extendModel, enums))
			for _, f := range ext.Fields {
				if owner != nil && modelDeclHasField(owner, f.Name) {
					continue
				}
				relation, relationFound := relations[f.Name]
				field, ok := g.schemaFieldFromDecl(ext.Name, f, "", enums, relation, relationFound)
				if !ok {
					continue
				}
				field.Module = modName
				if field.Relation {
					field.ForeignKey = g.inferFederationForeignKey(&ast.ModelDecl{Name: ext.Name}, f)
				}
				stub.Fields = append(stub.Fields, field)
			}
			if err := s.RegisterModel(stub); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *GeneratorContext) schemaModelFromDecl(model *ast.ModelDecl, module string, enums map[string]bool) *schema.Model {
	result := &schema.Model{Name: model.Name, Module: module, Description: model.Doc, Directives: schemaDirectiveNames(model.Directives)}
	primaryKey := primaryKeyFieldName(model)
	modelRelations := g.analyzeRelations(model, enums)
	result.ComputedSources = g.computedModelSources(model, modelRelations, module)
	relations := relationMap(modelRelations)
	for _, field := range model.Fields {
		relation, relationFound := relations[field.Name]
		converted, ok := g.schemaFieldFromDecl(model.Name, field, primaryKey, enums, relation, relationFound)
		if ok {
			result.Fields = append(result.Fields, converted)
		}
	}
	return result
}

func (g *GeneratorContext) schemaFieldFromDecl(modelName string, field *ast.FieldDecl, primaryKey string, enums map[string]bool, relation Relation, relationFound bool) (schema.Field, bool) {
	if field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
		return schema.Field{}, false
	}
	result := schema.Field{
		ID:          g.modelFieldID(modelName, field.Name),
		Name:        field.Name,
		Type:        luxoTypeToSchemaFieldType(field.Type.Name, enums),
		TypeName:    field.Type.Name,
		Nullable:    field.Type.Nullable,
		IsList:      field.Type.IsList,
		Relation:    relationFound,
		Computed:    field.Computed != nil,
		PrimaryKey:  field.Name == primaryKey,
		Description: field.Doc,
		Directives:  schemaDirectiveNames(field.Directives),
	}
	if relationFound {
		result.RelationKind = schemaRelationKind(relation.Type)
		result.LocalKey = relation.LocalKey
		result.RemoteKey = relation.RemoteKey
		result.TargetModule = g.remoteModelModule(relation.TargetName)
	}
	return result, true
}

func modelDeclHasField(model *ast.ModelDecl, name string) bool {
	for _, field := range model.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

// inferForeignKey determines the FK field name for a relation field.
// For hasMany: remoteKey = lowerFirst(modelName) + "Id" (e.g., User → "userId")
// For belongsTo: localKey = lowerFirst(targetName) + "Id" (e.g., Post → "postId")
func inferForeignKey(m *ast.ModelDecl, f *ast.FieldDecl, enums map[string]bool) string {
	return defaultGenerator().inferForeignKey(m, f, enums)
}

func (g *GeneratorContext) inferForeignKey(m *ast.ModelDecl, f *ast.FieldDecl, enums map[string]bool) string {
	// Check explicit @by directive
	byDir := findDirective(f.Directives, "by")
	if byDir != nil {
		remoteKey, _ := extractByArgs(byDir)
		return remoteKey
	}
	// Auto-infer: hasMany/hasOne use "{modelName}{PrimaryKey}".
	if f.Type != nil && f.Type.IsList {
		return g.relationForeignKeyName(m)
	}
	return "id"
}

func (g *GeneratorContext) inferFederationForeignKey(model *ast.ModelDecl, field *ast.FieldDecl) string {
	if by := findDirective(field.Directives, "by"); by != nil {
		if remote, _ := extractByArgs(by); remote != "" {
			return remote
		}
	}
	return g.relationForeignKeyName(model)
}

func (g *GeneratorContext) relationForeignKeyName(model *ast.ModelDecl) string {
	keyName := g.externalModelIDFieldName(model.Name)
	if field := primaryKeyField(model); field != nil {
		keyName = field.Name
	}
	return str.LowerFirst(model.Name) + str.Capitalize(keyName)
}

func (g *GeneratorContext) buildSchemaAPIs(s *schema.Schema, result *semantic.Result, enums map[string]bool) error {
	explicit := explicitAPINames(result)
	modelMap := inferred.ModelsForAPIs(result.Files)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if !hasCrud(m) {
				continue
			}
			for _, op := range crudOperations(m) {
				apiName := ast.CRUDAPIName(m.Name, op)
				if explicit[apiName] {
					continue
				}
				a := &schema.API{ID: g.apiID(apiName), Name: apiName, Module: modName, Description: crudAPIDescription(m.Name, op)}
				idParam := g.schemaParamForModelID(apiName, m, enums, false)
				switch op {
				case "get":
					a.ReturnType = m.Name
					a.Params = []schema.Param{idParam}
				case "list":
					a.Query = modelQueryCapabilities(m, enums)
					a.ReturnType = m.Name
					a.ReturnList = true
					a.Paginated = true
					a.DefaultPageSize = ast.DefaultPaginationPageSize
					a.Params = []schema.Param{
						{ID: g.apiParamID(apiName, "page"), Name: "page", Type: schema.FieldInt, HasDefault: true, Description: "One-based page number."},
						{ID: g.apiParamID(apiName, "pageSize"), Name: "pageSize", Type: schema.FieldInt, HasDefault: true, Description: "Maximum records returned per page."},
					}
				case "create":
					a.ReturnType = m.Name
					a.Params = g.schemaCRUDFieldParams(apiName, m, enums, false)
				case "update":
					a.ReturnType = m.Name
					a.Params = append([]schema.Param{idParam}, g.schemaCRUDFieldParams(apiName, m, enums, true)...)
				case "delete":
					a.ReturnType = "Int"
					a.Params = []schema.Param{idParam}
				case "deleteMany":
					a.ReturnType = "Int"
					a.Params = []schema.Param{g.schemaParamForModelID(apiName, m, enums, true)}
				}
				if err := s.RegisterAPI(a); err != nil {
					return err
				}
			}
		}
		for _, api := range file.APIs {
			api = inferredAPIContract(api, modelMap)
			a := &schema.API{
				ID: g.apiID(api.Name), Name: api.Name, Module: modName,
				Description: api.Doc, Directives: schemaDirectiveNames(api.Directives),
			}
			if api.ReturnType != nil {
				a.ReturnType = api.ReturnType.Name
				a.ReturnList = api.ReturnType.IsList
				a.ReturnNullable = api.ReturnType.Nullable
			}
			a.Paginated = hasDirective(api.Directives, "paginate")
			a.DefaultPageSize = paginationDefaultPageSize(api, a.Paginated)
			a.Stream = hasDirective(api.Directives, "stream")
			for _, p := range api.EffectiveParams() {
				a.Params = append(a.Params, schema.Param{
					ID: g.apiParamID(api.Name, p.Name), Name: p.Name,
					Type:        luxoParamToSchemaFieldType(p.Type.Name, enums),
					TypeName:    p.Type.Name,
					IsList:      p.Type.IsList,
					Nullable:    p.Type.Nullable,
					HasDefault:  p.Default != nil,
					Description: schemaParamDescription(p),
				})
			}
			if err := s.RegisterAPI(a); err != nil {
				return err
			}
		}
	}
	return nil
}

func paginationDefaultPageSize(api *ast.ApiDecl, paginated bool) int {
	if !paginated {
		return 0
	}
	return api.DefaultPageSize()
}

func (g *GeneratorContext) schemaParamForModelID(apiName string, model *ast.ModelDecl, enums map[string]bool, list bool) schema.Param {
	typeName := "Int"
	if field := primaryKeyField(model); field != nil {
		typeName = field.Type.Name
	}
	name := "id"
	description := fmt.Sprintf("Primary key of the %s record.", model.Name)
	if list {
		name = "ids"
		description = fmt.Sprintf("Primary keys of the %s records.", model.Name)
	}
	return schema.Param{
		ID: g.apiParamID(apiName, name), Name: name,
		Type: luxoParamToSchemaFieldType(typeName, enums), TypeName: typeName, IsList: list, Description: description,
	}
}

func (g *GeneratorContext) schemaCRUDFieldParams(apiName string, model *ast.ModelDecl, enums map[string]bool, update bool) []schema.Param {
	params := make([]schema.Param, 0, len(model.Fields))
	for _, field := range model.Fields {
		if !isCRUDInputField(field, model, enums, update) {
			continue
		}
		params = append(params, schema.Param{
			ID: g.apiParamID(apiName, field.Name), Name: field.Name,
			Type: luxoParamToSchemaFieldType(field.Type.Name, enums), TypeName: field.Type.Name,
			IsList: field.Type.IsList, Nullable: field.Type.Nullable, HasDefault: update || field.Type.Nullable || field.Default != nil,
			Description: field.Doc,
		})
	}
	return params
}

func buildSchemaEnums(s *schema.Schema, result *semantic.Result) {
	for _, file := range result.Files {
		for _, e := range file.Enums {
			s.RegisterEnum(&schema.Enum{Name: e.Name, Module: moduleNameFromFile(file.Name), Values: e.Values, Description: e.Doc})
		}
	}
}

func (g *GeneratorContext) buildSchemaTypes(s *schema.Schema, result *semantic.Result, enums map[string]bool) error {
	for _, file := range result.Files {
		for _, t := range file.Types {
			st := &schema.TypeDecl{Name: t.Name, Module: moduleNameFromFile(file.Name), Description: t.Doc}
			for _, f := range t.Fields {
				if f.Type == nil {
					continue
				}
				st.Fields = append(st.Fields, schema.Field{
					ID: g.modelFieldID(t.Name, f.Name), Name: f.Name,
					Type: luxoTypeToSchemaFieldType(f.Type.Name, enums), TypeName: f.Type.Name,
					Nullable: f.Type.Nullable, IsList: f.Type.IsList,
					Description: f.Doc, Directives: schemaDirectiveNames(f.Directives),
				})
			}
			if err := s.RegisterType(st); err != nil {
				return err
			}
		}
	}
	return nil
}

func luxoTypeToSchemaFieldType(typeName string, enums map[string]bool) schema.FieldType {
	if enums != nil && enums[typeName] {
		return schema.FieldEnum
	}
	switch typeName {
	case "Int":
		return schema.FieldInt
	case "Float":
		return schema.FieldFloat
	case "String":
		return schema.FieldString
	case "Boolean":
		return schema.FieldBool
	case "DateTime":
		return schema.FieldDateTime
	case "Duration":
		return schema.FieldDuration
	case "Bytes":
		return schema.FieldBytes
	case "UUID":
		return schema.FieldUUID
	case "Decimal":
		return schema.FieldDecimal
	case "JSON":
		return schema.FieldJSON
	default:
		return schema.FieldModel
	}
}

func luxoParamToSchemaFieldType(typeName string, enums map[string]bool) schema.FieldType {
	return luxoTypeToSchemaFieldType(typeName, enums)
}
