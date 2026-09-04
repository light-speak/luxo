package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
)

// StableIDs contains the wire identifiers assigned by luxo.lock.
// A GeneratorContext takes an immutable snapshot of these maps at construction time.
type StableIDs struct {
	EventFields   map[string]map[string]int
	ModelFields   map[string]map[string]int
	APIs          map[string]int
	APIParams     map[string]map[string]int
	APIParamTypes map[string]map[string]string
}

// GeneratorConfig configures one isolated compilation.
type GeneratorConfig struct {
	Driver DBDriver
	IDs    StableIDs
	Events *EventContext
}

// GeneratorContext owns every piece of state used by one compilation.
// Separate generators can safely run concurrently without locks.
type GeneratorContext struct {
	driver DBDriver
	dbPkg  string
	ids    StableIDs
	events *EventContext
}

// Generator is kept as a source-compatible alias for GeneratorContext.
type Generator = GeneratorContext

func defaultGenerator() *GeneratorContext {
	return &GeneratorContext{driver: DriverPG, dbPkg: DriverPG.DriverPkg()}
}

// NewGenerator creates an isolated generator for one compilation.
func NewGenerator(config GeneratorConfig) (*GeneratorContext, error) {
	driver := config.Driver
	if driver == "" {
		driver = DriverPG
	}
	if err := validateDriver(driver); err != nil {
		return nil, err
	}
	return &GeneratorContext{
		driver: driver,
		dbPkg:  driver.DriverPkg(),
		ids:    cloneStableIDs(config.IDs),
		events: cloneEventContext(config.Events),
	}, nil
}

func validateDriver(driver DBDriver) error {
	switch driver {
	case DriverPG:
		return nil
	case DriverMySQL, DriverSQLite, DriverMongo:
		return fmt.Errorf("database driver %q is not implemented; use %q", driver, DriverPG)
	default:
		return fmt.Errorf("unknown database driver %q", driver)
	}
}

func cloneStableIDs(ids StableIDs) StableIDs {
	return StableIDs{
		EventFields:   cloneNestedIntMap(ids.EventFields),
		ModelFields:   cloneNestedIntMap(ids.ModelFields),
		APIs:          cloneIntMap(ids.APIs),
		APIParams:     cloneNestedIntMap(ids.APIParams),
		APIParamTypes: cloneNestedStringMap(ids.APIParamTypes),
	}
}

func cloneIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneNestedIntMap(source map[string]map[string]int) map[string]map[string]int {
	if source == nil {
		return nil
	}
	result := make(map[string]map[string]int, len(source))
	for key, values := range source {
		result[key] = cloneIntMap(values)
	}
	return result
}

func cloneNestedStringMap(source map[string]map[string]string) map[string]map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]map[string]string, len(source))
	for key, values := range source {
		cloned := make(map[string]string, len(values))
		for name, value := range values {
			cloned[name] = value
		}
		result[key] = cloned
	}
	return result
}

func cloneEventContext(source *EventContext) *EventContext {
	if source == nil {
		return nil
	}
	result := &EventContext{
		EventModule:     cloneStringMap(source.EventModule),
		Events:          cloneEventMap(source.Events),
		ModelModule:     cloneStringMap(source.ModelModule),
		TypeModule:      cloneStringMap(source.TypeModule),
		EnumModule:      cloneStringMap(source.EnumModule),
		ModelIDType:     cloneStringMap(source.ModelIDType),
		ModelIDField:    cloneStringMap(source.ModelIDField),
		ModelFields:     cloneBoolSetMap(source.ModelFields),
		remoteLoadCalls: cloneLoadCalls(source.remoteLoadCalls),
		remotePKModels:  cloneBoolMap(source.remotePKModels),
		ModulePath:      source.ModulePath,
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneEventMap(source map[string]*ast.EventDecl) map[string]*ast.EventDecl {
	if source == nil {
		return nil
	}
	result := make(map[string]*ast.EventDecl, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBoolSetMap(source map[string]map[string]bool) map[string]map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]map[string]bool, len(source))
	for key, values := range source {
		result[key] = cloneBoolMap(values)
	}
	return result
}

func cloneLoadCalls(source map[string][]loadCallInfo) map[string][]loadCallInfo {
	if source == nil {
		return nil
	}
	result := make(map[string][]loadCallInfo, len(source))
	for key, values := range source {
		cloned := make([]loadCallInfo, len(values))
		for i, value := range values {
			cloned[i] = value
			cloned[i].argNames = append([]string(nil), value.argNames...)
			cloned[i].argTypes = append([]string(nil), value.argTypes...)
			cloned[i].argTypeNames = append([]string(nil), value.argTypeNames...)
		}
		result[key] = cloned
	}
	return result
}

func (g *GeneratorContext) apiID(name string) int {
	return g.ids.APIs[name]
}

func (g *GeneratorContext) apiParamIDs(name string) map[string]int {
	return g.ids.APIParams[name]
}

func (g *GeneratorContext) apiParamID(apiName, paramName string) int {
	return g.ids.APIParams[apiName][paramName]
}

func (g *GeneratorContext) modelFieldID(modelName, fieldName string) int {
	return g.ids.ModelFields[modelName][fieldName]
}

func (g *GeneratorContext) eventFieldID(eventName, paramName string) int {
	return g.ids.EventFields[eventName][paramName]
}

func (g *GeneratorContext) ownerModelHasField(modelName, fieldName string) bool {
	return g.events != nil && g.events.ModelFields[modelName][fieldName]
}

func (g *GeneratorContext) externalModelIDFieldName(modelName string) string {
	if g.events != nil {
		if fieldName := g.events.ModelIDField[modelName]; fieldName != "" {
			return fieldName
		}
	}
	return "id"
}

func (g *GeneratorContext) externalModelIDTypeName(modelName string) string {
	if g.events != nil {
		if typeName := g.events.ModelIDType[modelName]; typeName != "" {
			return typeName
		}
	}
	return "Int"
}
