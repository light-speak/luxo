package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateSchemaFile produces schema.gen.go containing RegisterSchema
// that registers model and API metadata with the Luvia schema registry.
// This enables schema-driven Binary↔JSON conversion at the Luvia layer.
type schemaAPIInfo struct {
	name           string
	moduleName     string
	params         []*ast.ParamDecl
	optionalParams map[string]bool
	returnType     *ast.TypeRef
	paginated      bool
	stream         bool
	directives     []*ast.Directive
}

func generateSchemaFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	// Track which module owns each model
	modelOwner := make(map[string]string)
	modelFields := make(map[string]map[string]bool)
	if globalEventCtx != nil {
		for name, module := range globalEventCtx.ModelModule {
			modelOwner[name] = module
		}
		for name, fields := range globalEventCtx.ModelFields {
			modelFields[name] = fields
		}
	}
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			modelOwner[m.Name] = modName
			fields := make(map[string]bool, len(m.Fields))
			for _, field := range m.Fields {
				fields[field.Name] = true
			}
			modelFields[m.Name] = fields
		}
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
			if !modelNames[ext.Name] {
				stubs = append(stubs, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
				modelNames[ext.Name] = true
			}
		}
	}

	apis := collectSchemaAPIs(result, enums, modelOwner, modelFields)

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
	b.WriteString("func RegisterSchema(s *schema.Schema) {\n")

	// Register models
	allModels := append(models, stubs...)
	for _, m := range allModels {
		writeModelRegistration(&b, m, modelOwner[m.Name], enums, extendFieldModules[m.Name])
	}

	// Register APIs
	for _, api := range apis {
		writeAPIRegistrationSchema(&b, api.name, api.moduleName, api.params, api.returnType, api.paginated, api.stream, enums, api.optionalParams, api.directives)
	}

	// Register type declarations (non-DB types like AuthPayload)
	for _, file := range result.Files {
		moduleName := moduleNameFromFile(file.Name)
		for _, t := range file.Types {
			writeTypeRegistration(&b, t, moduleName, enums)
		}
		for _, enumDecl := range file.Enums {
			writeEnumRegistration(&b, enumDecl, moduleName)
		}
	}

	b.WriteString("}\n")

	return []byte(b.String())
}

// buildCrudAPIInfo constructs schemaAPIInfo for a single CRUD operation.
// collectSchemaAPIs collects all API metadata (CRUD + compiled + service + batchLoad + resolve).
func collectSchemaAPIs(result *semantic.Result, enums map[string]bool, modelOwner map[string]string, modelFields map[string]map[string]bool) []schemaAPIInfo {
	var apis []schemaAPIInfo
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if hasCrud(m) {
				for _, op := range crudOperations(m) {
					apis = append(apis, buildCrudAPIInfo(m, op, modName, enums))
				}
			}
			if hasCrud(m) || globalEventCtx != nil && globalEventCtx.remotePKModels[m.Name] {
				apis = append(apis, schemaAPIInfo{
					name: "svc:batchLoad:" + m.Name, moduleName: modName,
					params:     []*ast.ParamDecl{{Name: "keys", Type: &ast.TypeRef{Name: modelIDTypeName(m), IsList: true}}},
					returnType: &ast.TypeRef{Name: m.Name, IsList: true},
				})
			}
		}
		for _, api := range file.APIs {
			apis = append(apis, schemaAPIInfo{
				name: api.Name, moduleName: modName,
				params: api.Params, returnType: api.ReturnType, stream: hasDirective(api.Directives, "stream"), directives: api.Directives,
			})
		}
		for _, fn := range file.Functions {
			if hasDirective(fn.Directives, "service") {
				apis = append(apis, schemaAPIInfo{
					name: "svc:" + fn.Name, moduleName: modName,
					params: fn.Params, returnType: fn.ReturnType,
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
				fk := inferFederationForeignKey(&ast.ModelDecl{Name: ext.Name}, f)
				apis = append(apis, schemaAPIInfo{
					name: "svc:resolve:" + f.Type.Name + ":" + fk, moduleName: modName,
					params:     []*ast.ParamDecl{{Name: "keys", Type: &ast.TypeRef{Name: externalModelIDTypeName(ext.Name), IsList: true}}},
					returnType: &ast.TypeRef{Name: f.Type.Name, IsList: true},
				})
			}
		}
	}
	if len(result.Files) > 0 && globalEventCtx != nil {
		moduleName := moduleNameFromFile(result.Files[0].Name)
		for _, call := range globalEventCtx.remoteLoadCalls[moduleName] {
			params := make([]*ast.ParamDecl, len(call.argNames))
			for i, argName := range call.argNames {
				params[i] = &ast.ParamDecl{Name: argName, Type: &ast.TypeRef{Name: call.argTypeNames[i], IsList: true}}
			}
			apis = append(apis, schemaAPIInfo{
				name: loadServiceName(call), moduleName: moduleName,
				params: params, returnType: &ast.TypeRef{Name: call.modelName, IsList: true},
			})
		}
	}
	return apis
}

func buildCrudAPIInfo(model *ast.ModelDecl, op, modName string, enums map[string]bool) schemaAPIInfo {
	modelName := model.Name
	apiName := crudAPIName(modelName, op)
	ai := schemaAPIInfo{name: apiName, moduleName: modName}
	idType := &ast.TypeRef{Name: "Int"}
	if field := primaryKeyField(model); field != nil {
		idType = &ast.TypeRef{Name: field.Type.Name}
	}
	idParam := []*ast.ParamDecl{{Name: "id", Type: idType}}
	switch op {
	case "get":
		ai.returnType = &ast.TypeRef{Name: modelName}
		ai.params = idParam
	case "list":
		ai.returnType = &ast.TypeRef{Name: modelName, IsList: true}
		ai.paginated = true
		ai.params = []*ast.ParamDecl{
			{Name: "page", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "pageSize", Type: &ast.TypeRef{Name: "Int"}},
		}
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
		ai.params = []*ast.ParamDecl{{Name: "ids", Type: &ast.TypeRef{Name: idType.Name, IsList: true}}}
	}
	return ai
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
		if field.Type == nil || skipHandlerField(field, enums) ||
			(update && (field.Name == primaryKeyFieldName(model) || hasDirective(field.Directives, "immutable"))) {
			continue
		}
		params = append(params, &ast.ParamDecl{Name: field.Name, Type: field.Type, Default: field.Default})
	}
	return params
}

// writeModelRegistration generates schema.RegisterModel for one model.
// extendModules maps fieldName → source module for extend fields.
func writeModelRegistration(b *strings.Builder, m *ast.ModelDecl, moduleName string, enums map[string]bool, extendModules map[string]string) {
	name := m.Name
	fmt.Fprintf(b, "\ts.RegisterModel(&schema.Model{\n")
	fmt.Fprintf(b, "\t\tName: %q,\n", name)
	if moduleName != "" {
		fmt.Fprintf(b, "\t\tModule: %q,\n", moduleName)
	}
	fmt.Fprintf(b, "\t\tFields: []schema.Field{\n")

	for _, f := range m.Fields {
		if f.Type == nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}

		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		relation := isRelationField(f, enums)

		// Scalar fields: write type info for Binary↔JSON
		if !relation {
			fieldType := luxoTypeToSchemaType(f.Type.Name, enums)
			fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, Nullable: %v, Computed: %v, PrimaryKey: %v},\n",
				fieldID, f.Name, fieldType, f.Type.Nullable, f.Computed != nil, f.Name == primaryKeyFieldName(m))
			continue
		}

		// Relation fields: include for federation (Module + ForeignKey)
		fieldType := "FieldModel"
		module := extendModules[f.Name]
		fk := ""
		if module != "" {
			fk = inferForeignKey(m, f, enums)
		}
		fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q, IsList: %v, Relation: true, Module: %q, ForeignKey: %q},\n",
			fieldID, f.Name, fieldType, f.Type.Name, f.Type.IsList, module, fk)
	}

	fmt.Fprintf(b, "\t\t},\n")
	fmt.Fprintf(b, "\t})\n")
}

// writeTypeRegistration generates schema.RegisterType for a type declaration.
func writeTypeRegistration(b *strings.Builder, t *ast.TypeDecl, moduleName string, enums map[string]bool) {
	fmt.Fprintf(b, "\ts.RegisterType(&schema.TypeDecl{\n")
	fmt.Fprintf(b, "\t\tName: %q,\n", t.Name)
	fmt.Fprintf(b, "\t\tModule: %q,\n", moduleName)
	fmt.Fprintf(b, "\t\tFields: []schema.Field{\n")

	for _, f := range t.Fields {
		if f.Type == nil {
			continue
		}
		fieldID := getModelFieldID(t.Name, f.Name)
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
		fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q, Nullable: %v, IsList: %v, Relation: %v},\n",
			fieldID, f.Name, fieldType, f.Type.Name, f.Type.Nullable, f.Type.IsList, relation)
	}

	fmt.Fprintf(b, "\t\t},\n")
	fmt.Fprintf(b, "\t})\n")
}

func writeEnumRegistration(b *strings.Builder, enumDecl *ast.EnumDecl, moduleName string) {
	fmt.Fprintf(b, "\ts.RegisterEnum(&schema.Enum{Name: %q, Module: %q, Values: []string{", enumDecl.Name, moduleName)
	for index, value := range enumDecl.Values {
		if index > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", value)
	}
	b.WriteString("}})\n")
}

// writeAPIRegistrationSchema generates schema.RegisterAPI for one API.
func writeAPIRegistrationSchema(b *strings.Builder, name, moduleName string, params []*ast.ParamDecl, returnType *ast.TypeRef, paginated, stream bool, enums map[string]bool, optionalParams map[string]bool, directives ...[]*ast.Directive) {
	apiID := getAPIID(name)
	fmt.Fprintf(b, "\ts.RegisterAPI(&schema.API{\n")
	fmt.Fprintf(b, "\t\tID: %d, Name: %q, Module: %q,\n", apiID, name, moduleName)
	if returnType != nil {
		fmt.Fprintf(b, "\t\tReturnType: %q, ReturnList: %v,\n", returnType.Name, returnType.IsList)
	}
	if paginated {
		fmt.Fprintf(b, "\t\tPaginated: true,\n")
	}
	if stream {
		fmt.Fprintf(b, "\t\tStream: true,\n")
	}
	// @deprecated
	if len(directives) > 0 {
		for _, d := range directives[0] {
			if d.Name == "deprecated" {
				fmt.Fprintf(b, "\t\tDeprecated: true,\n")
				if len(d.Args) > 0 {
					if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
						fmt.Fprintf(b, "\t\tDeprecatedReason: %q,\n", lit.Value)
					}
				}
			}
		}
	}
	if len(params) > 0 {
		fmt.Fprintf(b, "\t\tParams: []schema.Param{\n")
		for _, p := range params {
			paramID := getAPIParamID(name, p.Name)
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
			if p.Default != nil || optionalParams[p.Name] {
				b.WriteString(", HasDefault: true")
			}
			b.WriteString("},\n")
		}
		fmt.Fprintf(b, "\t\t},\n")
	}
	fmt.Fprintf(b, "\t})\n")
}

func luxoParamToSchemaType(typeName string, enums map[string]bool) string {
	typeID := luxoTypeToSchemaType(typeName, enums)
	if typeID == "FieldModel" {
		return "FieldJSON"
	}
	return typeID
}

func getAPIParamID(apiName, paramName string) int {
	if apiParamIDs == nil {
		return 0
	}
	if params, ok := apiParamIDs[apiName]; ok {
		return params[paramName]
	}
	return 0
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
func BuildSchemaJSON(result *semantic.Result, enums map[string]bool) ([]byte, error) {
	s := schema.New()
	buildSchemaModels(s, result, enums)
	buildSchemaAPIs(s, result, enums)
	buildSchemaEnums(s, result)
	buildSchemaTypes(s, result, enums)
	return s.ToJSON()
}

func buildSchemaModels(s *schema.Schema, result *semantic.Result, enums map[string]bool) {
	modelModule := make(map[string]string)
	modelDecls := make(map[string]*ast.ModelDecl)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			modelModule[m.Name] = modName
			modelDecls[m.Name] = m
			s.RegisterModel(schemaModelFromDecl(m, modName, enums))
		}
	}

	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			stub := &schema.Model{Name: ext.Name, Module: modelModule[ext.Name]}
			owner := modelDecls[ext.Name]
			for _, f := range ext.Fields {
				field, ok := schemaFieldFromDecl(ext.Name, f, "", enums)
				if !ok {
					continue
				}
				if owner == nil || !modelDeclHasField(owner, f.Name) {
					field.Module = modName
					if field.Relation {
						field.ForeignKey = inferFederationForeignKey(&ast.ModelDecl{Name: ext.Name}, f)
					}
				}
				stub.Fields = append(stub.Fields, field)
			}
			s.RegisterModel(stub)
		}
	}
}

func schemaModelFromDecl(model *ast.ModelDecl, module string, enums map[string]bool) *schema.Model {
	result := &schema.Model{Name: model.Name, Module: module}
	primaryKey := primaryKeyFieldName(model)
	for _, field := range model.Fields {
		converted, ok := schemaFieldFromDecl(model.Name, field, primaryKey, enums)
		if ok {
			result.Fields = append(result.Fields, converted)
		}
	}
	return result
}

func schemaFieldFromDecl(modelName string, field *ast.FieldDecl, primaryKey string, enums map[string]bool) (schema.Field, bool) {
	if field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
		return schema.Field{}, false
	}
	return schema.Field{
		ID:         getModelFieldID(modelName, field.Name),
		Name:       field.Name,
		Type:       luxoTypeToSchemaFieldType(field.Type.Name, enums),
		TypeName:   field.Type.Name,
		Nullable:   field.Type.Nullable,
		IsList:     field.Type.IsList,
		Relation:   isRelationField(field, enums),
		Computed:   field.Computed != nil,
		PrimaryKey: field.Name == primaryKey,
	}, true
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
	// Check explicit @by directive
	byDir := findDirective(f.Directives, "by")
	if byDir != nil {
		remoteKey, _ := extractByArgs(byDir)
		return remoteKey
	}
	// Auto-infer: hasMany/hasOne use "{modelName}{PrimaryKey}".
	if f.Type != nil && f.Type.IsList {
		return relationForeignKeyName(m)
	}
	return "id"
}

func inferFederationForeignKey(model *ast.ModelDecl, field *ast.FieldDecl) string {
	if by := findDirective(field.Directives, "by"); by != nil {
		if remote, _ := extractByArgs(by); remote != "" {
			return remote
		}
	}
	return relationForeignKeyName(model)
}

func relationForeignKeyName(model *ast.ModelDecl) string {
	keyName := externalModelIDFieldName(model.Name)
	if field := primaryKeyField(model); field != nil {
		keyName = field.Name
	}
	return str.LowerFirst(model.Name) + str.Capitalize(keyName)
}

func buildSchemaAPIs(s *schema.Schema, result *semantic.Result, enums map[string]bool) {
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if !hasCrud(m) {
				continue
			}
			for _, op := range crudOperations(m) {
				apiName := crudAPIName(m.Name, op)
				a := &schema.API{ID: getAPIID(apiName), Name: apiName, Module: modName}
				idParam := schemaParamForModelID(apiName, m, enums, false)
				switch op {
				case "get":
					a.ReturnType = m.Name
					a.Params = []schema.Param{idParam}
				case "list":
					a.ReturnType = m.Name
					a.ReturnList = true
					a.Paginated = true
					a.Params = []schema.Param{
						{ID: getAPIParamID(apiName, "page"), Name: "page", Type: schema.FieldInt},
						{ID: getAPIParamID(apiName, "pageSize"), Name: "pageSize", Type: schema.FieldInt},
					}
				case "create":
					a.ReturnType = m.Name
					a.Params = schemaCRUDFieldParams(apiName, m, enums, false)
				case "update":
					a.ReturnType = m.Name
					a.Params = append([]schema.Param{idParam}, schemaCRUDFieldParams(apiName, m, enums, true)...)
				case "delete":
					a.ReturnType = "Int"
					a.Params = []schema.Param{idParam}
				case "deleteMany":
					a.ReturnType = "Int"
					a.Params = []schema.Param{schemaParamForModelID(apiName, m, enums, true)}
				}
				s.RegisterAPI(a)
			}
		}
		for _, api := range file.APIs {
			a := &schema.API{ID: getAPIID(api.Name), Name: api.Name, Module: modName}
			if api.ReturnType != nil {
				a.ReturnType = api.ReturnType.Name
				a.ReturnList = api.ReturnType.IsList
			}
			a.Paginated = hasDirective(api.Directives, "paginate")
			a.Stream = hasDirective(api.Directives, "stream")
			for _, p := range api.Params {
				a.Params = append(a.Params, schema.Param{
					ID: getAPIParamID(api.Name, p.Name), Name: p.Name,
					Type:       luxoParamToSchemaFieldType(p.Type.Name, enums),
					TypeName:   p.Type.Name,
					IsList:     p.Type.IsList,
					Nullable:   p.Type.Nullable,
					HasDefault: p.Default != nil,
				})
			}
			s.RegisterAPI(a)
		}
	}
}

func schemaParamForModelID(apiName string, model *ast.ModelDecl, enums map[string]bool, list bool) schema.Param {
	typeName := "Int"
	if field := primaryKeyField(model); field != nil {
		typeName = field.Type.Name
	}
	name := "id"
	if list {
		name = "ids"
	}
	return schema.Param{
		ID: getAPIParamID(apiName, name), Name: name,
		Type: luxoParamToSchemaFieldType(typeName, enums), TypeName: typeName, IsList: list,
	}
}

func schemaCRUDFieldParams(apiName string, model *ast.ModelDecl, enums map[string]bool, update bool) []schema.Param {
	params := make([]schema.Param, 0, len(model.Fields))
	for _, field := range model.Fields {
		if field.Type == nil || skipHandlerField(field, enums) ||
			(update && (field.Name == primaryKeyFieldName(model) || hasDirective(field.Directives, "immutable"))) {
			continue
		}
		params = append(params, schema.Param{
			ID: getAPIParamID(apiName, field.Name), Name: field.Name,
			Type: luxoParamToSchemaFieldType(field.Type.Name, enums), TypeName: field.Type.Name,
			IsList: field.Type.IsList, Nullable: field.Type.Nullable, HasDefault: update || field.Type.Nullable || field.Default != nil,
		})
	}
	return params
}

func buildSchemaEnums(s *schema.Schema, result *semantic.Result) {
	for _, file := range result.Files {
		for _, e := range file.Enums {
			s.RegisterEnum(&schema.Enum{Name: e.Name, Module: moduleNameFromFile(file.Name), Values: e.Values})
		}
	}
}

func buildSchemaTypes(s *schema.Schema, result *semantic.Result, enums map[string]bool) {
	for _, file := range result.Files {
		for _, t := range file.Types {
			st := &schema.TypeDecl{Name: t.Name, Module: moduleNameFromFile(file.Name)}
			for _, f := range t.Fields {
				if f.Type == nil {
					continue
				}
				st.Fields = append(st.Fields, schema.Field{
					ID: getModelFieldID(t.Name, f.Name), Name: f.Name,
					Type: luxoTypeToSchemaFieldType(f.Type.Name, enums), TypeName: f.Type.Name,
					Nullable: f.Type.Nullable, IsList: f.Type.IsList,
				})
			}
			s.RegisterType(st)
		}
	}
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

// luxoParamToSchemaFieldType maps structured params to their canonical JSON
// wire representation while TypeName retains the generated SDK type.
func luxoParamToSchemaFieldType(typeName string, enums map[string]bool) schema.FieldType {
	typeID := luxoTypeToSchemaFieldType(typeName, enums)
	if typeID == schema.FieldModel {
		return schema.FieldJSON
	}
	return typeID
}
