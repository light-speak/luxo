package codegen

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// receiverName takes a complete Unicode rune, never a partial UTF-8 byte.
func receiverName(name string) string {
	if strings.HasPrefix(name, "_") {
		return "_luxoReceiver"
	}
	_, size := utf8.DecodeRuneInString(name)
	return strings.ToLower(name[:size])
}

func validateStableIDs(ids StableIDs) error {
	if _, err := lockfile.ValidateAssignments("apis", ids.APIs, nil, codec.MaxAPIID); err != nil {
		return err
	}
	for _, group := range []struct {
		name  string
		ids   map[string]map[string]int
		limit int
	}{
		{"models", ids.ModelFields, codec.MaxSelectionFieldID},
		{"events", ids.EventFields, codec.MaxEventFieldID},
		{"params", ids.APIParams, lockfile.MaxParameterID},
	} {
		for name, assigned := range group.ids {
			if name == "" {
				return fmt.Errorf("stable IDs %s has an empty declaration name", group.name)
			}
			if _, err := lockfile.ValidateAssignments(group.name+"."+name, assigned, nil, group.limit); err != nil {
				return err
			}
		}
	}
	return nil
}

// Formal generation requires IDs for every emitted API and active parameter.
func (g *GeneratorContext) validateRegisteredParameters(result *semantic.Result, enums map[string]bool) error {
	for _, api := range g.collectSchemaAPIs(result, enums) {
		if g.apiID(api.name) == 0 {
			return fmt.Errorf("API %s has no stable ID", api.name)
		}
		for _, param := range api.params {
			if g.apiParamID(api.name, param.Name) == 0 {
				return fmt.Errorf("registered API parameter %s.%s has no stable ID", api.name, param.Name)
			}
		}
	}
	return nil
}

func (g *GeneratorContext) validateFieldIdentifiers(result *semantic.Result, enums map[string]bool) error {
	hasExtensions := false
	var names fieldNameValidation
	var enumNames enumSymbols
	for _, file := range result.Files {
		for _, declaration := range file.Enums {
			if err := enumNames.add(declaration); err != nil {
				return err
			}
		}
		hasExtensions = hasExtensions || len(file.Extends) > 0
		for _, model := range file.Models {
			if g.conflictsWithAppMember(model.Name, result, enums) {
				return fmt.Errorf("model %q conflicts with generated App member %q", model.Name, model.Name)
			}
		}
		if err := g.validateFileFieldIdentifiers(file, enums, &names); err != nil {
			return err
		}
	}
	if hasExtensions {
		if err := g.validateExternalKeyIdentifiers(result, enums); err != nil {
			return err
		}
	}
	if err := validateDatabaseIdentifiers(result, enums, g.sqlDialect); err != nil {
		return err
	}
	// This validated Go namespace belongs to the current compilation only.
	g.enumSymbols = enumNames
	return nil
}

// Conditional members only reserve names when they are actually emitted.
func (g *GeneratorContext) conflictsWithAppMember(name string, result *semantic.Result, enums map[string]bool) bool {
	switch name {
	case "DB", "Queue", "Close", "Tx":
		return true
	case "EventBus":
		return appNeedsEvents(result)
	case "EventHandlers":
		return resultHasNativeEvents(result)
	case "Resolver":
		return len(collectNativeAPIs(result)) > 0 || resultHasNativeStreams(result)
	case "SetLoaders", "loaders":
		return g.collectDataLoaderInputs(result, enums, nil).needed()
	default:
		return false
	}
}

// External stubs synthesize the owner's primary key even when extend only lists
// other fields. That key must keep its owner-assigned wire ID, never zero.
func (g *GeneratorContext) validateExternalKeyIdentifiers(result *semantic.Result, enums map[string]bool) error {
	localModels := make(map[string]bool)
	for _, file := range result.Files {
		for _, model := range file.Models {
			localModels[model.Name] = true
		}
	}
	for _, file := range result.Files {
		for _, extension := range file.Extends {
			if localModels[extension.Name] {
				continue
			}
			key := g.externalModelIDFieldName(extension.Name)
			if g.modelFieldID(extension.Name, key) == 0 {
				return fmt.Errorf("external model key %s.%s has no stable ID", extension.Name, key)
			}
			if err := validateExternalKeyNames(extension, key, enums); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *GeneratorContext) validateFileFieldIdentifiers(file *ast.File, enums map[string]bool, names *fieldNameValidation) error {
	for _, model := range file.Models {
		if err := validateSoftDeleteField(model, enums); err != nil {
			return err
		}
		if err := g.validateDeclaredFields(model.Name, model.Fields, names); err != nil {
			return err
		}
		if err := validateModelMethodNames(model, names.goNames); err != nil {
			return err
		}
	}
	for _, declaration := range file.Types {
		if err := g.validateDeclaredFields(declaration.Name, declaration.Fields, names); err != nil {
			return err
		}
	}
	for _, extension := range file.Extends {
		if err := g.validateDeclaredFields(extension.Name, extension.Fields, names); err != nil {
			return err
		}
		if err := validateHashMethodNames(extension.Name, extension.Fields, names.goNames, ""); err != nil {
			return err
		}
		if err := names.validateDatabaseColumnNames(extension.Name, extension.Fields, enums); err != nil {
			return err
		}
	}
	for _, event := range file.Events {
		if err := g.validateEventFieldIdentifiers(event, names); err != nil {
			return err
		}
	}
	return nil
}

// Match Capitalize without allocating a normalized string on successful checks.
type goFieldName struct {
	initial rune
	suffix  string
}

type fieldNameValidation struct {
	goNames         map[goFieldName]string
	columns         map[string]string
	databaseDialect lux.Dialect
}

func (v *fieldNameValidation) resetGoNames(count int) {
	if v.goNames == nil {
		v.goNames = make(map[goFieldName]string, count)
	} else {
		clear(v.goNames)
	}
}

func (v *fieldNameValidation) addGoName(owner, field string) (goFieldName, error) {
	name := normalizedGoFieldName(field)
	if previous, exists := v.goNames[name]; exists {
		return name, fmt.Errorf("%s fields %q and %q map to the same Go field %q", owner, previous, field, str.Capitalize(field))
	}
	v.goNames[name] = field
	return name, nil
}

func normalizedGoFieldName(field string) goFieldName {
	initial, size := utf8.DecodeRuneInString(field)
	return goFieldName{unicode.ToUpper(initial), field[size:]}
}

func (g *GeneratorContext) validateDeclaredFields(owner string, fields []*ast.FieldDecl, names *fieldNameValidation) error {
	names.resetGoNames(len(fields))
	for _, field := range fields {
		if g.modelFieldID(owner, field.Name) == 0 {
			return fmt.Errorf("model/type field %s.%s has no stable ID", owner, field.Name)
		}
		name, err := names.addGoName(owner, field.Name)
		if err != nil {
			return err
		}
		if isProtocolMethodName(name) {
			return fmt.Errorf("%s field %q conflicts with generated protocol method %q", owner, field.Name, str.Capitalize(field.Name))
		}
	}
	return nil
}

func (g *GeneratorContext) validateEventFieldIdentifiers(event *ast.EventDecl, names *fieldNameValidation) error {
	names.resetGoNames(len(event.Params))
	for _, param := range event.Params {
		if g.eventFieldID(event.Name, param.Name) == 0 {
			return fmt.Errorf("event field %s.%s has no stable ID", event.Name, param.Name)
		}
		name, err := names.addGoName(event.Name, param.Name)
		if err != nil {
			return err
		}
		switch name {
		case goFieldName{'M', "arshalLuxo"}, goFieldName{'U', "nmarshalLuxo"}, goFieldName{'S', "napshotEvent"}:
			return generatedMethodConflict(event.Name, param.Name, str.Capitalize(param.Name))
		}
	}
	return nil
}

// Only persisted fields participate in database column naming.
func (v *fieldNameValidation) validateDatabaseColumnNames(owner string, fields []*ast.FieldDecl, enums map[string]bool) error {
	if v.columns == nil {
		v.columns = make(map[string]string, len(fields))
	} else {
		clear(v.columns)
	}
	names := v.columns
	for _, field := range fields {
		if field.Type == nil || field.Computed != nil || isRelationField(field, enums) {
			continue
		}
		column := databaseFieldColumnName(field.Name)
		if v.databaseDialect != nil {
			if err := v.databaseDialect.ValidateIdentifier(column); err != nil {
				return fmt.Errorf("%s: database column %s.%s: %w", field.Pos, owner, field.Name, err)
			}
		}
		if previous, exists := names[column]; exists {
			return fmt.Errorf("%s fields %q and %q map to the same database column %q", owner, previous, field.Name, column)
		}
		names[column] = field.Name
	}
	return nil
}

// Lowercase ASCII field names are already in database form.
func databaseFieldColumnName(name string) string {
	for i := range len(name) {
		if name[i] >= utf8.RuneSelf || name[i] >= 'A' && name[i] <= 'Z' {
			return str.ToSnakeCase(name)
		}
	}
	return name
}

func validateExternalKeyNames(extension *ast.ExtendDecl, key string, enums map[string]bool) error {
	if err := validateHashMethodNames(extension.Name, extension.Fields, nil, key); err != nil {
		return err
	}
	return validateInjectedFieldNames(extension.Name, extension.Fields, key, databaseFieldColumnName(key), enums)
}

// Synthetic database fields must not collide with explicitly declared fields.
func validateInjectedFieldNames(owner string, fields []*ast.FieldDecl, key, columnKey string, enums map[string]bool) error {
	goKey := normalizedGoFieldName(key)
	if isProtocolMethodName(goKey) {
		return fmt.Errorf("%s field %q conflicts with generated protocol method %q", owner, key, str.Capitalize(key))
	}
	for _, field := range fields {
		if field.Name == key {
			continue
		}
		if normalizedGoFieldName(field.Name) == goKey {
			return fmt.Errorf("%s fields %q and %q map to the same Go field %q", owner, key, field.Name, str.Capitalize(key))
		}
		if field.Type != nil && field.Computed == nil && !isRelationField(field, enums) && databaseFieldColumnName(field.Name) == columnKey {
			return fmt.Errorf("%s fields %q and %q map to the same database column %q", owner, key, field.Name, columnKey)
		}
	}
	return nil
}

// An implicit soft-delete timestamp is database-only and needs no wire ID.
func validateSoftDeleteField(model *ast.ModelDecl, enums map[string]bool) error {
	if !isSoftDelete(model) {
		return nil
	}
	if inferred.InvalidSoftDeleteTimestamp(model) != nil {
		return fmt.Errorf("soft-delete field %s.deletedAt must be a persisted DateTime? scalar", model.Name)
	}
	return validateInjectedFieldNames(model.Name, model.Fields, "deletedAt", "deleted_at", enums)
}

// These receiver methods are emitted for models, types and external stubs.
// Columnar codecs are free functions, so their names do not reserve fields.
func isProtocolMethodName(name goFieldName) bool {
	switch name {
	case goFieldName{'W', "riteLuxo"}, goFieldName{'R', "eadLuxo"},
		goFieldName{'R', "eadLuxoInput"}, goFieldName{'R', "eadJSONInput"}:
		return true
	default:
		return false
	}
}

func validateModelMethodNames(model *ast.ModelDecl, fields map[goFieldName]string) error {
	if hasDirective(model.Directives, "withAuth") {
		for _, method := range []string{"CreateToken", "CreateTokenWithExpiry", "CreateRefreshToken", "RefreshToken", "AppendClaimsJSON"} {
			key := goFieldName{rune(method[0]), method[1:]}
			if field, exists := fields[key]; exists {
				return generatedMethodConflict(model.Name, field, method)
			}
		}
	}
	return validateHashMethodNames(model.Name, model.Fields, fields, "")
}

func validateHashMethodNames(owner string, fields []*ast.FieldDecl, names map[goFieldName]string, injected string) error {
	injectedKey := normalizedGoFieldName(injected)
	for _, field := range fields {
		if !hasDirective(field.Directives, "hash") {
			continue
		}
		normalized := normalizedGoFieldName(field.Name)
		suffix := "erify" + string(normalized.initial) + normalized.suffix
		key := goFieldName{'V', suffix}
		if name, exists := names[key]; exists {
			return generatedMethodConflict(owner, name, "V"+suffix)
		}
		if injected != "" && injectedKey == key {
			return generatedMethodConflict(owner, injected, "V"+suffix)
		}
	}
	return nil
}

func generatedMethodConflict(owner, field, method string) error {
	return fmt.Errorf("%s field %q conflicts with generated method %q", owner, field, method)
}
