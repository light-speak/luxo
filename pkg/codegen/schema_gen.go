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
	name       string
	moduleName string
	params     []*ast.ParamDecl
	returnType *ast.TypeRef
	paginated  bool
	stream     bool
	directives []*ast.Directive
}

func generateSchemaFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	// Track which module owns each model
	modelOwner := make(map[string]string)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			modelOwner[m.Name] = modName
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
					extendFieldModules[ext.Name][f.Name] = modName
				}
			}
			if !modelNames[ext.Name] {
				stubs = append(stubs, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
				modelNames[ext.Name] = true
			}
		}
	}

	apis := collectSchemaAPIs(result, enums, modelOwner)

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
		writeAPIRegistrationSchema(&b, api.name, api.moduleName, api.params, api.returnType, api.paginated, api.stream, api.directives)
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
func collectSchemaAPIs(result *semantic.Result, enums map[string]bool, modelOwner map[string]string) []schemaAPIInfo {
	var apis []schemaAPIInfo
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if !hasCrud(m) {
				continue
			}
			for _, op := range crudOperations(m) {
				apis = append(apis, buildCrudAPIInfo(m.Name, op, modName))
			}
			apis = append(apis, schemaAPIInfo{
				name: "svc:batchLoad:" + m.Name, moduleName: modName,
				returnType: &ast.TypeRef{Name: m.Name, IsList: true},
			})
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
				if f.Type == nil || !isRelationField(f, enums) {
					continue
				}
				fk := inferForeignKey(&ast.ModelDecl{Name: ext.Name}, f, enums)
				apis = append(apis, schemaAPIInfo{
					name: "svc:resolve:" + f.Type.Name + ":" + fk, moduleName: modName,
					returnType: &ast.TypeRef{Name: f.Type.Name, IsList: true},
				})
			}
		}
	}
	return apis
}

func buildCrudAPIInfo(modelName, op, modName string) schemaAPIInfo {
	apiName := crudAPIName(modelName, op)
	ai := schemaAPIInfo{name: apiName, moduleName: modName}
	idParam := []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}
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
	case "create", "update":
		ai.returnType = &ast.TypeRef{Name: modelName}
	case "delete":
		ai.params = idParam
	}
	return ai
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
		if f.Type == nil || f.Computed != nil {
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
			fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, Nullable: %v},\n",
				fieldID, f.Name, fieldType, f.Type.Nullable)
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
func writeAPIRegistrationSchema(b *strings.Builder, name, moduleName string, params []*ast.ParamDecl, returnType *ast.TypeRef, paginated, stream bool, directives ...[]*ast.Directive) {
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
			isList := false
			nullable := false
			if p.Type != nil {
				pType = luxoTypeToSchemaType(p.Type.Name, nil)
				isList = p.Type.IsList
				nullable = p.Type.Nullable
			}
			fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, TypeName: %q", paramID, p.Name, pType, p.Type.Name)
			if isList {
				b.WriteString(", IsList: true")
			}
			if nullable {
				b.WriteString(", Nullable: true")
			}
			if p.Default != nil {
				b.WriteString(", HasDefault: true")
			}
			b.WriteString("},\n")
		}
		fmt.Fprintf(b, "\t\t},\n")
	}
	fmt.Fprintf(b, "\t})\n")
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
	default:
		return "FieldString" // enum/unknown → string
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
	// Collect models and track which module owns each model
	var models []*ast.ModelDecl
	modelModule := make(map[string]string) // modelName → owning module
	modelNames := make(map[string]bool)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			models = append(models, m)
			modelNames[m.Name] = true
			modelModule[m.Name] = modName
		}
	}

	// Collect extend fields with their source module
	// extendFields[modelName] = [{field, module}]
	type extendFieldInfo struct {
		field  *ast.FieldDecl
		module string
	}
	extendFields := make(map[string][]extendFieldInfo)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, ext := range file.Extends {
			for _, f := range ext.Fields {
				extendFields[ext.Name] = append(extendFields[ext.Name], extendFieldInfo{field: f, module: modName})
			}
			if !modelNames[ext.Name] {
				// Cross-module stub — model defined in another module
				models = append(models, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
				modelNames[ext.Name] = true
			}
		}
	}

	for _, m := range models {
		sm := &schema.Model{Name: m.Name, Module: modelModule[m.Name]}
		ownerModule := modelModule[m.Name]
		for _, f := range m.Fields {
			if f.Type == nil || f.Computed != nil {
				continue
			}
			if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
				continue
			}
			sf := schema.Field{
				ID:       getModelFieldID(m.Name, f.Name),
				Name:     f.Name,
				Type:     luxoTypeToSchemaFieldType(f.Type.Name, enums),
				TypeName: f.Type.Name,
				Nullable: f.Type.Nullable,
				IsList:   f.Type.IsList,
				Relation: isRelationField(f, enums),
			}
			// Check if this field comes from an extend (different module)
			for _, ef := range extendFields[m.Name] {
				if ef.field.Name == f.Name && ef.module != ownerModule {
					sf.Module = ef.module
					sf.ForeignKey = inferForeignKey(m, f, enums)
					break
				}
			}
			sm.Fields = append(sm.Fields, sf)
		}
		s.RegisterModel(sm)
	}
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
	// Auto-infer: hasMany → "{modelName}Id", belongsTo → "id"
	if f.Type != nil && f.Type.IsList {
		return str.LowerFirst(m.Name) + "Id"
	}
	return "id"
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
				switch op {
				case "get":
					a.ReturnType = m.Name
					a.Params = []schema.Param{{ID: getAPIParamID(apiName, "id"), Name: "id", Type: schema.FieldInt}}
				case "list":
					a.ReturnType = m.Name
					a.ReturnList = true
					a.Paginated = true
					a.Params = []schema.Param{
						{ID: getAPIParamID(apiName, "page"), Name: "page", Type: schema.FieldInt},
						{ID: getAPIParamID(apiName, "pageSize"), Name: "pageSize", Type: schema.FieldInt},
					}
				case "create", "update":
					a.ReturnType = m.Name
				case "delete":
					a.Params = []schema.Param{{ID: getAPIParamID(apiName, "id"), Name: "id", Type: schema.FieldInt}}
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
					Type:       luxoTypeToSchemaFieldType(p.Type.Name, enums),
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
	default:
		return schema.FieldString
	}
}
