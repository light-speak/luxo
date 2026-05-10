package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateSchemaFile produces schema.gen.go containing RegisterSchema
// that registers model and API metadata with the Luvia schema registry.
// This enables schema-driven Binary↔JSON conversion at the Luvia layer.
func generateSchemaFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		models = append(models, file.Models...)
	}

	// Collect extend stubs (cross-module models)
	modelNames := make(map[string]bool)
	for _, m := range models {
		modelNames[m.Name] = true
	}
	var stubs []*ast.ModelDecl
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if !modelNames[ext.Name] {
				stubs = append(stubs, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
				modelNames[ext.Name] = true
			}
		}
	}

	// Collect APIs (CRUD + declared + fn @service)
	type apiInfo struct {
		name       string
		moduleName string
		params     []*ast.ParamDecl
		returnType *ast.TypeRef
		paginated  bool
		directives []*ast.Directive
	}
	var apis []apiInfo

	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, m := range file.Models {
			if !hasCrud(m) {
				continue
			}
			// CRUD APIs
			for _, op := range crudOperations(m) {
				apiName := crudAPIName(m.Name, op)
				ai := apiInfo{name: apiName, moduleName: modName}
				switch op {
				case "get":
					ai.returnType = &ast.TypeRef{Name: m.Name}
				case "list":
					ai.returnType = &ast.TypeRef{Name: m.Name, IsList: true}
					ai.paginated = true
				case "create":
					ai.returnType = &ast.TypeRef{Name: m.Name}
				case "update":
					ai.returnType = &ast.TypeRef{Name: m.Name}
				}
				apis = append(apis, ai)
			}
		}
		for _, api := range file.APIs {
			apis = append(apis, apiInfo{
				name:       api.Name,
				moduleName: modName,
				params:     api.Params,
				returnType: api.ReturnType,
				directives: api.Directives,
			})
		}
		for _, fn := range file.Functions {
			if hasDirective(fn.Directives, "service") {
				apis = append(apis, apiInfo{
					name:       "svc:" + fn.Name,
					moduleName: modName,
					params:     fn.Params,
					returnType: fn.ReturnType,
				})
			}
		}
	}

	if len(models) == 0 && len(stubs) == 0 && len(apis) == 0 {
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
		writeModelRegistration(&b, m, enums)
	}

	// Register APIs
	for _, api := range apis {
		writeAPIRegistrationSchema(&b, api.name, api.moduleName, api.params, api.returnType, api.paginated, api.directives)
	}

	b.WriteString("}\n")

	return []byte(b.String())
}

// writeModelRegistration generates schema.RegisterModel for one model.
func writeModelRegistration(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	fmt.Fprintf(b, "\ts.RegisterModel(&schema.Model{\n")
	fmt.Fprintf(b, "\t\tName: %q,\n", name)
	fmt.Fprintf(b, "\t\tFields: []schema.Field{\n")

	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}

		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		fieldType := luxoTypeToSchemaType(f.Type.Name, enums)
		fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s, Nullable: %v},\n",
			fieldID, f.Name, fieldType, f.Type.Nullable)
	}

	fmt.Fprintf(b, "\t\t},\n")
	fmt.Fprintf(b, "\t})\n")
}

// writeAPIRegistrationSchema generates schema.RegisterAPI for one API.
func writeAPIRegistrationSchema(b *strings.Builder, name, moduleName string, params []*ast.ParamDecl, returnType *ast.TypeRef, paginated bool, directives ...[]*ast.Directive) {
	apiID := getAPIID(name)
	fmt.Fprintf(b, "\ts.RegisterAPI(&schema.API{\n")
	fmt.Fprintf(b, "\t\tID: %d, Name: %q, Module: %q,\n", apiID, name, moduleName)
	if returnType != nil {
		fmt.Fprintf(b, "\t\tReturnType: %q, ReturnList: %v,\n", returnType.Name, returnType.IsList)
	}
	if paginated {
		fmt.Fprintf(b, "\t\tPaginated: true,\n")
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
			if p.Type != nil {
				pType = luxoTypeToSchemaType(p.Type.Name, nil)
			}
			fmt.Fprintf(b, "\t\t\t{ID: %d, Name: %q, Type: schema.%s},\n", paramID, p.Name, pType)
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
	var models []*ast.ModelDecl
	modelNames := make(map[string]bool)
	for _, file := range result.Files {
		for _, m := range file.Models {
			models = append(models, m)
			modelNames[m.Name] = true
		}
	}
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if !modelNames[ext.Name] {
				models = append(models, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
				modelNames[ext.Name] = true
			}
		}
	}
	for _, m := range models {
		sm := &schema.Model{Name: m.Name}
		for _, f := range m.Fields {
			if f.Type == nil || f.Computed != nil {
				continue
			}
			if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
				continue
			}
			sm.Fields = append(sm.Fields, schema.Field{
				ID:       getModelFieldID(m.Name, f.Name),
				Name:     f.Name,
				Type:     luxoTypeToSchemaFieldType(f.Type.Name, enums),
				TypeName: f.Type.Name,
				Nullable: f.Type.Nullable,
				IsList:   f.Type.IsList,
				Relation: isRelationField(f, enums),
			})
		}
		s.RegisterModel(sm)
	}
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
				case "list":
					a.ReturnType = m.Name
					a.ReturnList = true
					a.Paginated = true
				case "create", "update":
					a.ReturnType = m.Name
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
			for _, p := range api.Params {
				a.Params = append(a.Params, schema.Param{
					ID: getAPIParamID(api.Name, p.Name), Name: p.Name,
					Type: luxoTypeToSchemaFieldType(p.Type.Name, enums),
				})
			}
			s.RegisterAPI(a)
		}
	}
}

func buildSchemaEnums(s *schema.Schema, result *semantic.Result) {
	for _, file := range result.Files {
		for _, e := range file.Enums {
			s.RegisterEnum(&schema.Enum{Name: e.Name, Values: e.Values})
		}
	}
}

func buildSchemaTypes(s *schema.Schema, result *semantic.Result, enums map[string]bool) {
	for _, file := range result.Files {
		for _, t := range file.Types {
			st := &schema.TypeDecl{Name: t.Name}
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
	default:
		return schema.FieldString
	}
}
