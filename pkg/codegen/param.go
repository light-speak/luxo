package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// Validate only at generation time; request handlers never infer missing types.
func validateRequestContracts(result *semantic.Result) error {
	if err := validateAuthStores(result); err != nil {
		return err
	}
	for _, file := range result.Files {
		for _, declaration := range file.APIs {
			if err := validateCacheContract(declaration, result); err != nil {
				return err
			}
			if err := validateCallableParamTypes("API", declaration.Name, declaration.Params); err != nil {
				return err
			}
		}
		for _, declaration := range file.Functions {
			if err := validateCallableParamTypes("function", declaration.Name, declaration.Params); err != nil {
				return err
			}
		}
		for _, model := range file.Models {
			if hasCrud(model) {
				if err := validateCRUDParameterTypes(model); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCacheContract(declaration *ast.ApiDecl, result *semantic.Result) error {
	count := 0
	for _, directive := range declaration.Directives {
		if directive.Name != "cache" {
			continue
		}
		count++
		if count > 1 {
			return fmt.Errorf("API %s requires exactly one @cache policy reference", declaration.Name)
		}
		if err := validateCacheDirective(declaration.Name, directive, result); err != nil {
			return err
		}
	}
	if count != 0 {
		sources, checked := result.ExecutionCacheDependencies[declaration]
		if !checked {
			return fmt.Errorf("API %s requires cache dependency analysis before code generation", declaration.Name)
		}
		for _, source := range sources {
			if source.Kind != semantic.CacheModelDependency && source.Kind != semantic.CacheExternalDependency {
				return fmt.Errorf("API %s has an unknown cache dependency kind", declaration.Name)
			}
		}
	}
	return nil
}

// Entry-only generation must not bypass the cache contract checked by handlers.
func validateEntryCacheContracts(result *semantic.Result) error {
	for _, file := range result.Files {
		for _, declaration := range file.APIs {
			if err := validateCacheContract(declaration, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCacheDirective(name string, directive *ast.Directive, result *semantic.Result) error {
	if len(directive.Args) == 1 && directive.Args[0].Name == "policy" {
		literal, ok := directive.Args[0].Value.(*ast.Literal)
		if !ok || literal.Kind != token.String || literal.Value == "" {
			return fmt.Errorf("API %s requires a static @cache policy reference", name)
		}
		policy, checked := result.CachePolicies[directive]
		if !checked {
			return fmt.Errorf("API %s requires @cache policy analysis before code generation", name)
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("API %s has an invalid cache policy: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("API %s requires @cache(policy: \"name\") resolved from luxo.cache.json; inline TTL is not a cache contract", name)
}

func validateCallableParamTypes(kind, name string, params []*ast.ParamDecl) error {
	for _, param := range params {
		if param.Type == nil {
			return fmt.Errorf("%s %s parameter %s requires an explicit type", kind, name, param.Name)
		}
	}
	return nil
}

func validateCRUDParameterTypes(model *ast.ModelDecl) error {
	for _, field := range model.Fields {
		if field.Type == nil {
			return fmt.Errorf("CRUD model %s field %s requires an explicit type", model.Name, field.Name)
		}
	}
	if field := primaryKeyField(model); field != nil {
		typ := field.Type
		if typ.Nullable || typ.IsList || (typ.Name != "Int" && typ.Name != "String" && typ.Name != "UUID") {
			return fmt.Errorf("CRUD model %s primary key %s must be a non-null Int, String or UUID", model.Name, field.Name)
		}
	}
	return nil
}

func writeRequiredParam(b *strings.Builder, variable, name, method, indent string) {
	fmt.Fprintf(b, "%s%s, err := req.Param%s(%q)\n", indent, variable, method, name)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
}

// Built-in arguments use concrete getters. Presence and nullability are
// generated branches, not inferred from a reflected destination pointer.
func writeBuiltinParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) bool {
	if param.Type == nil {
		return false
	}
	base := *param.Type
	base.Nullable = false
	method := paramMethod(resolveGoType(&base))
	if method == "" {
		return false
	}
	if !param.Type.Nullable {
		if param.Default != nil {
			writeOptionalParamExtraction(b, param, enums, method, indent)
		} else {
			writeRequiredParam(b, param.Name, param.Name, method, indent)
		}
		return true
	}
	writeNullableBuiltinParam(b, param, enums, method, indent)
	return true
}

func writeNullableBuiltinParam(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, method, indent string) {
	writeStructuredParamDeclaration(b, param, enums, indent)
	outer := indent
	if param.Default != nil {
		fmt.Fprintf(b, "%sif req.HasParam(%q) {\n", indent, param.Name)
		indent += "\t"
	}
	fmt.Fprintf(b, "%sif req.ParamIsNull(%q) {\n", indent, param.Name)
	fmt.Fprintf(b, "%s\t%s = nil\n", indent, param.Name)
	fmt.Fprintf(b, "%s} else {\n", indent)
	value := "_value" + str.Capitalize(param.Name)
	fmt.Fprintf(b, "%s\t%s, err := req.Param%s(%q)\n", indent, value, method, param.Name)
	fmt.Fprintf(b, "%s\tif err != nil { return err }\n", indent)
	if !param.Type.IsList {
		value = "&" + value
	}
	fmt.Fprintf(b, "%s\t%s = %s\n%s}\n", indent, param.Name, value, indent)
	if param.Default != nil {
		fmt.Fprintf(b, "%s}\n", outer)
	}
}
