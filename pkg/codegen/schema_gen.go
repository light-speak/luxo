package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
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
		writeAPIRegistrationSchema(&b, api.name, api.moduleName, api.params, api.returnType, api.paginated)
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
func writeAPIRegistrationSchema(b *strings.Builder, name, moduleName string, params []*ast.ParamDecl, returnType *ast.TypeRef, paginated bool) {
	apiID := getAPIID(name)
	fmt.Fprintf(b, "\ts.RegisterAPI(&schema.API{\n")
	fmt.Fprintf(b, "\t\tID: %d, Name: %q, Module: %q,\n", apiID, name, moduleName)
	if returnType != nil {
		fmt.Fprintf(b, "\t\tReturnType: %q, ReturnList: %v,\n", returnType.Name, returnType.IsList)
	}
	if paginated {
		fmt.Fprintf(b, "\t\tPaginated: true,\n")
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

// Suppress unused import
var _ = str.Capitalize
