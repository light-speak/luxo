package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateEventFile produces event.gen.go containing typed event structs,
// emit functions, and on-listener registrations.
// moduleName is used as the queue group for competing consumers.
// Returns nil if there are no events.
func generateEventFile(result *semantic.Result, packageName string) []byte {
	var events []*ast.EventDecl
	var listeners []*ast.OnDecl

	for _, file := range result.Files {
		events = append(events, file.Events...)
		listeners = append(listeners, file.Listeners...)
	}

	if len(events) == 0 && len(listeners) == 0 {
		return nil
	}

	// Determine current module name
	currentModule := ""
	if len(result.Files) > 0 {
		currentModule = moduleNameFromFile(result.Files[0].Name)
	}

	var b strings.Builder
	writeHeader(&b, packageName, "event.gen.go")

	crossModuleImports := collectCrossModuleEventImports(result, listeners, currentModule)

	// Check if listener bodies need time import
	needsTime := false
	for _, l := range listeners {
		if l.Body != nil {
			var feat handlerFeatures
			scanBodyForBuiltins(l.Body, &feat)
			if feat.hasTimeFunc {
				needsTime = true
				break
			}
		}
	}

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if needsTime {
		b.WriteString("\t\"time\"\n")
	}
	if len(events) > 0 {
		b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	// Sort cross-module imports for deterministic output
	modNames := make([]string, 0, len(crossModuleImports))
	for modName := range crossModuleImports {
		modNames = append(modNames, modName)
	}
	sort.Strings(modNames)
	for _, modName := range modNames {
		fmt.Fprintf(&b, "\t%s \"%s/%s/luxo\"\n", crossModuleImports[modName], globalEventCtx.ModulePath, modName)
	}
	b.WriteString(")\n\n")

	// Event structs + MarshalLuxo/UnmarshalLuxo
	for _, e := range events {
		generateEventStruct(&b, e)
		generateEventCodec(&b, e)
	}

	// Emit functions — pass struct directly, zero serialization
	for _, e := range events {
		generateEmitFunc(&b, e)
	}

	// Collect models for compiling on-handler bodies
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		models = append(models, file.Models...)
	}
	modelMap := make(map[string]*ast.ModelDecl, len(models))
	for _, m := range models {
		modelMap[m.Name] = m
	}

	// Collect enums for on-handler body compilation
	enums := CollectEnumsFromResult(result)

	// RegisterEvents function — wires all on-listeners
	generateRegisterEvents(&b, listeners, packageName, currentModule, modelMap, enums)

	// Generate Unmarshal functions for LOCAL events (exported for cross-module use)
	for _, e := range events {
		eventType := e.Name + "Event"
		fmt.Fprintf(&b, "// Unmarshal%s is the wire decoder for %s events.\n", e.Name, e.Name)
		fmt.Fprintf(&b, "func Unmarshal%s(data []byte, v any) error {\n", e.Name)
		fmt.Fprintf(&b, "\tif e, ok := v.(*%s); ok {\n", eventType)
		fmt.Fprintf(&b, "\t\treturn e.UnmarshalLuxo(data)\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn nil\n")
		b.WriteString("}\n\n")
	}

	return []byte(b.String())
}

// generateEventStruct generates a typed struct for an event declaration.
func generateEventStruct(b *strings.Builder, e *ast.EventDecl) {
	fmt.Fprintf(b, "// %sEvent is the payload for the %s event.\n", e.Name, e.Name)
	fmt.Fprintf(b, "type %sEvent struct {\n", e.Name)
	for _, p := range e.Params {
		goType := resolveGoType(p.Type)
		fmt.Fprintf(b, "\t%s %s `json:%q`\n", str.Capitalize(p.Name), goType, p.Name)
	}
	b.WriteString("}\n\n")
}

// generateEventCodec generates MarshalLuxo/UnmarshalLuxo for binary encoding.
// Field IDs come from luxo.lock (stable across schema evolution).
func generateEventCodec(b *strings.Builder, e *ast.EventDecl) {
	typeName := e.Name + "Event"

	// MarshalLuxo
	fmt.Fprintf(b, "// MarshalLuxo encodes %s to Luxo binary format.\n", typeName)
	fmt.Fprintf(b, "func (e *%s) MarshalLuxo() []byte {\n", typeName)
	b.WriteString("\tvar enc codec.Encoder\n")
	for _, p := range e.Params {
		fieldID := getEventFieldID(e.Name, p.Name)
		if fieldID == 0 {
			continue // no lock entry — skip (shouldn't happen after Update)
		}
		goField := str.Capitalize(p.Name)
		nullable := p.Type != nil && p.Type.Nullable
		switch resolveGoType(p.Type) {
		case "int64":
			if nullable {
				fmt.Fprintf(b, "\tenc.WriteFieldIntPtr(%d, e.%s)\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\tenc.WriteFieldInt(%d, e.%s)\n", fieldID, goField)
			}
		case "float64":
			if nullable {
				fmt.Fprintf(b, "\tenc.WriteFieldFloatPtr(%d, e.%s)\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\tenc.WriteFieldFloat(%d, e.%s)\n", fieldID, goField)
			}
		case "string":
			if nullable {
				fmt.Fprintf(b, "\tenc.WriteFieldStringPtr(%d, e.%s)\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\tenc.WriteFieldString(%d, e.%s)\n", fieldID, goField)
			}
		case "bool":
			if nullable {
				fmt.Fprintf(b, "\tenc.WriteFieldBoolPtr(%d, e.%s)\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\tenc.WriteFieldBool(%d, e.%s)\n", fieldID, goField)
			}
		default:
			// Complex types (models, lists) — skip for now, fall back to JSON
			fmt.Fprintf(b, "\t// TODO: complex type %s for field %s\n", resolveGoType(p.Type), p.Name)
		}
	}
	b.WriteString("\tenc.WriteEnd()\n")
	b.WriteString("\treturn enc.Bytes()\n")
	b.WriteString("}\n\n")

	// UnmarshalLuxo
	fmt.Fprintf(b, "// UnmarshalLuxo decodes %s from Luxo binary format.\n", typeName)
	fmt.Fprintf(b, "func (e *%s) UnmarshalLuxo(data []byte) error {\n", typeName)
	b.WriteString("\tdec := codec.NewDecoder(data)\n")
	b.WriteString("\tfor dec.NextField() {\n")
	b.WriteString("\t\tswitch dec.FieldID() {\n")
	for _, p := range e.Params {
		fieldID := getEventFieldID(e.Name, p.Name)
		if fieldID == 0 {
			continue
		}
		goField := str.Capitalize(p.Name)
		nullable := p.Type != nil && p.Type.Nullable
		switch resolveGoType(p.Type) {
		case "int64":
			if nullable {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadIntPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadInt()\n", fieldID, goField)
			}
		case "float64":
			if nullable {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadFloatPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadFloat()\n", fieldID, goField)
			}
		case "string":
			if nullable {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadStringPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadString()\n", fieldID, goField)
			}
		case "bool":
			if nullable {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadBoolPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: e.%s = dec.ReadBool()\n", fieldID, goField)
			}
		}
	}
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn dec.Err()\n")
	b.WriteString("}\n\n")
}

// generateEmitFunc generates a typed emit function.
// Passes the struct directly — ChanBus delivers zero-copy,
// NATSBus serializes at the wire boundary.
func generateEmitFunc(b *strings.Builder, e *ast.EventDecl) {
	fmt.Fprintf(b, "// Emit%s publishes a %s event.\n", e.Name, e.Name)
	fmt.Fprintf(b, "func Emit%s(ctx context.Context, bus event.Bus, e %sEvent) error {\n", e.Name, e.Name)
	fmt.Fprintf(b, "\treturn bus.Emit(ctx, %q, e)\n", e.Name)
	fmt.Fprintf(b, "}\n\n")
}

// generateRegisterEvents generates RegisterEvents that wires all on-listeners.
// Default uses OnQueueDecode with moduleName as the queue group (competing consumers).
// Listeners with @broadcast use OnDecode (every instance receives).
// Unmarshal uses Luxo binary (UnmarshalLuxo) for wire decoding.
func generateRegisterEvents(b *strings.Builder, listeners []*ast.OnDecl, moduleName string, currentModule string, models map[string]*ast.ModelDecl, enums map[string]bool) {
	if len(listeners) == 0 {
		b.WriteString("// RegisterEvents — no listeners in this module.\n")
		b.WriteString("func RegisterEvents(bus event.Bus, app *App) {}\n\n")
		return
	}
	b.WriteString("// RegisterEvents registers all event listeners with the bus.\n")
	b.WriteString("func RegisterEvents(bus event.Bus, app *App) {\n")

	for _, l := range listeners {
		paramName := "payload"
		if len(l.Params) > 0 {
			paramName = l.Params[0]
		}

		// Check if event is cross-module
		eventTypePrefix := ""
		unmarshalFunc := fmt.Sprintf("Unmarshal%s", l.EventName)
		if globalEventCtx != nil {
			evModule := globalEventCtx.EventModule[l.EventName]
			if evModule != "" && evModule != currentModule {
				eventTypePrefix = evModule + "_luxo."
				unmarshalFunc = evModule + "_luxo.Unmarshal" + l.EventName
			}
		}
		eventType := eventTypePrefix + l.EventName + "Event"
		if l.Broadcast {
			fmt.Fprintf(b, "\tevent.OnDecode(bus, %q, %s, func(ctx context.Context, %s %s) error {\n", l.EventName, unmarshalFunc, paramName, eventType)
		} else {
			fmt.Fprintf(b, "\tevent.OnQueueDecode(bus, %q, %q, %s, func(ctx context.Context, %s %s) error {\n", l.EventName, moduleName, unmarshalFunc, paramName, eventType)
		}
		// Compile on-handler body if present
		if l.Body != nil && len(l.Body.Stmts) > 0 {
			c := &compiler{
				b:      b,
				indent: "\t\t",
				models: models,
				enums:  enums,
				vars:   make(map[string]valType),
			}
			// Register event param as a known variable so event.field compiles correctly
			c.vars[paramName] = valType{name: l.EventName + "Event"}
			for _, stmt := range l.Body.Stmts {
				c.compileStmt(stmt)
			}
			fmt.Fprintf(b, "\t\treturn nil\n")
		} else {
			fmt.Fprintf(b, "\t\t_ = %s\n", paramName)
			fmt.Fprintf(b, "\t\treturn nil\n")
		}
		b.WriteString("\t})\n")
	}

	b.WriteString("}\n\n")

	// Note: Unmarshal functions are generated in generateEventFile, not here
}

// collectCrossModuleEventImports finds which event modules need to be imported.
func collectCrossModuleEventImports(result *semantic.Result, listeners []*ast.OnDecl, currentModule string) map[string]string {
	imports := make(map[string]string)
	if globalEventCtx == nil {
		return imports
	}
	for _, l := range listeners {
		evModule := globalEventCtx.EventModule[l.EventName]
		if evModule != "" && evModule != currentModule {
			imports[evModule] = evModule + "_luxo"
		}
	}
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if api.Body == nil {
				continue
			}
			for _, stmt := range api.Body.Stmts {
				if emit, ok := stmt.(*ast.EmitStmt); ok {
					evModule := globalEventCtx.EventModule[emit.EventName]
					if evModule != "" && evModule != currentModule {
						imports[evModule] = evModule + "_luxo"
					}
				}
			}
		}
	}
	return imports
}
