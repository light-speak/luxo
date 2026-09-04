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
	return defaultGenerator().generateEventFile(result, packageName)
}

func (g *GeneratorContext) generateEventFile(result *semantic.Result, packageName string) []byte {
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

	crossModuleImports := g.collectCrossModuleEventImports(result, listeners, currentModule)
	enums := CollectEnumsFromResult(result)
	objects := collectEventObjectTypes(result)

	// Check if listener bodies need time import
	needsTime := eventsUseType(events, "DateTime") || eventsUseType(events, "Duration")
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
	if len(events) > 0 {
		b.WriteString("\t\"fmt\"\n")
	}
	if eventsNeedJSON(events, enums, objects) {
		b.WriteString("\t\"encoding/json\"\n")
	}
	if needsTime {
		b.WriteString("\t\"time\"\n")
	}
	if len(events) > 0 {
		if eventsUseObjects(events, objects) {
			b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
		}
		b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	if eventsUseType(events, "UUID") {
		b.WriteString("\n\t\"github.com/google/uuid\"\n")
	}
	if eventsUseType(events, "Decimal") {
		b.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	// Sort cross-module imports for deterministic output
	modNames := make([]string, 0, len(crossModuleImports))
	for modName := range crossModuleImports {
		modNames = append(modNames, modName)
	}
	sort.Strings(modNames)
	for _, modName := range modNames {
		fmt.Fprintf(&b, "\t%s \"%s/%s/luxo\"\n", crossModuleImports[modName], g.events.ModulePath, modName)
	}
	b.WriteString(")\n\n")

	// Event structs + MarshalLuxo/UnmarshalLuxo
	for _, e := range events {
		generateEventStruct(&b, e)
		g.generateEventCodec(&b, e, enums, objects)
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

	// RegisterEvents function — wires all on-listeners
	g.generateRegisterEvents(&b, listeners, currentModule, currentModule, modelMap, enums)

	// Generate Unmarshal functions for LOCAL events (exported for cross-module use)
	for _, e := range events {
		eventType := e.Name + "Event"
		fmt.Fprintf(&b, "// Unmarshal%s is the wire decoder for %s events.\n", e.Name, e.Name)
		fmt.Fprintf(&b, "func Unmarshal%s(data []byte, v any) error {\n", e.Name)
		fmt.Fprintf(&b, "\tif e, ok := v.(*%s); ok {\n", eventType)
		fmt.Fprintf(&b, "\t\treturn e.UnmarshalLuxo(data)\n")
		b.WriteString("\t}\n")
		fmt.Fprintf(&b, "\treturn fmt.Errorf(%q, v)\n", "event "+e.Name+": expected *"+eventType+", got %T")
		b.WriteString("}\n\n")
	}

	return []byte(b.String())
}

func collectEventObjectTypes(result *semantic.Result) map[string]bool {
	objects := make(map[string]bool)
	for _, file := range result.Files {
		for _, model := range file.Models {
			objects[model.Name] = true
		}
		for _, declaration := range file.Types {
			objects[declaration.Name] = true
		}
	}
	return objects
}

func eventsUseType(events []*ast.EventDecl, name string) bool {
	for _, eventDecl := range events {
		for _, param := range eventDecl.Params {
			if param.Type != nil && param.Type.Name == name {
				return true
			}
		}
	}
	return false
}

func eventsUseObjects(events []*ast.EventDecl, objects map[string]bool) bool {
	for _, eventDecl := range events {
		for _, param := range eventDecl.Params {
			if param.Type != nil && objects[param.Type.Name] {
				return true
			}
		}
	}
	return false
}

func eventsNeedJSON(events []*ast.EventDecl, enums, objects map[string]bool) bool {
	for _, eventDecl := range events {
		for _, param := range eventDecl.Params {
			if param.Type == nil {
				return true
			}
			if param.Type.Name == "JSON" {
				return true
			}
			if !isEventBuiltin(param.Type.Name) && !enums[param.Type.Name] && !objects[param.Type.Name] {
				return true
			}
		}
	}
	return false
}

func isEventBuiltin(name string) bool {
	switch name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON":
		return true
	}
	return false
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
func (g *GeneratorContext) generateEventCodec(b *strings.Builder, e *ast.EventDecl, enums, objects map[string]bool) {
	typeName := e.Name + "Event"

	// MarshalLuxo
	fmt.Fprintf(b, "// MarshalLuxo encodes %s to Luxo binary format.\n", typeName)
	fmt.Fprintf(b, "func (e %s) MarshalLuxo() []byte {\n", typeName)
	b.WriteString("\tvar enc codec.Encoder\n")
	for _, p := range e.Params {
		g.writeEventMarshalField(b, e.Name, p, enums[p.Type.Name], objects[p.Type.Name])
	}
	b.WriteString("\tenc.WriteEnd()\n")
	b.WriteString("\treturn enc.Bytes()\n")
	b.WriteString("}\n\n")

	// UnmarshalLuxo
	fmt.Fprintf(b, "// UnmarshalLuxo decodes %s from Luxo binary format.\n", typeName)
	fmt.Fprintf(b, "func (e *%s) UnmarshalLuxo(data []byte) error {\n", typeName)
	for _, p := range e.Params {
		if p.Type != nil && !p.Type.Nullable && g.eventFieldID(e.Name, p.Name) != 0 {
			fmt.Fprintf(b, "\tvar seen%s bool\n", str.Capitalize(p.Name))
		}
	}
	b.WriteString("\tdec := codec.NewDecoder(data)\n")
	b.WriteString("\tfor dec.NextField() {\n")
	b.WriteString("\t\tswitch dec.FieldID() {\n")
	for _, p := range e.Params {
		g.writeEventUnmarshalField(b, e.Name, p, enums[p.Type.Name], objects[p.Type.Name])
		if p.Type != nil && !p.Type.Nullable && g.eventFieldID(e.Name, p.Name) != 0 {
			fmt.Fprintf(b, "\t\t\tseen%s = true\n", str.Capitalize(p.Name))
		}
	}
	b.WriteString("\t\tdefault: dec.SkipField()\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif err := dec.Err(); err != nil { return err }\n")
	for _, p := range e.Params {
		if p.Type != nil && !p.Type.Nullable && g.eventFieldID(e.Name, p.Name) != 0 {
			fmt.Fprintf(b, "\tif !seen%s { return fmt.Errorf(%q) }\n", str.Capitalize(p.Name), "event "+e.Name+": missing required field "+p.Name)
		}
	}
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

func (g *GeneratorContext) writeEventMarshalField(b *strings.Builder, eventName string, param *ast.ParamDecl, isEnum, isObject bool) {
	fieldID := g.eventFieldID(eventName, param.Name)
	if fieldID == 0 || param.Type == nil {
		return
	}
	value := "e." + str.Capitalize(param.Name)
	if param.Type.IsList {
		writeEventListMarshal(b, fieldID, value, param.Type.Name, isEnum, isObject)
		return
	}
	if isObject {
		writeEventObjectMarshal(b, fieldID, value, param.Type.Nullable)
		return
	}
	if isEventBuiltin(param.Type.Name) || isEnum {
		writeEventScalarMarshal(b, fieldID, value, param.Type, isEnum)
		return
	}
	fmt.Fprintf(b, "\tif data, err := json.Marshal(%s); err == nil { enc.WriteFieldBytes(%d, data) }\n", value, fieldID)
}

func writeEventScalarMarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, isEnum bool) {
	fmt.Fprintf(b, "\tenc.WriteFieldHeader(%d)\n", fieldID)
	encoded := value
	indent := "\t"
	if ref.Nullable {
		fmt.Fprintf(b, "\tif %s == nil { enc.WriteNull() } else {\n", value)
		b.WriteString("\t\tenc.WritePresent()\n")
		encoded = "(*" + value + ")"
		indent = "\t\t"
	}
	fmt.Fprintf(b, "%s%s\n", indent, eventScalarWriteStatement(ref.Name, encoded, isEnum))
	if ref.Nullable {
		b.WriteString("\t}\n")
	}
}

func eventScalarWriteStatement(typeName, value string, isEnum bool) string {
	if isEnum {
		return fmt.Sprintf("enc.WriteString(string(%s))", value)
	}
	switch typeName {
	case "Int":
		return "enc.WriteInt(" + value + ")"
	case "Float":
		return "enc.WriteFloat(" + value + ")"
	case "String":
		return "enc.WriteString(" + value + ")"
	case "Boolean":
		return "enc.WriteBool(" + value + ")"
	case "DateTime":
		return "enc.WriteInt(" + value + ".Unix())"
	case "Duration":
		return "enc.WriteInt(int64(" + value + "))"
	case "UUID":
		return "enc.WriteUUID([16]byte(" + value + "))"
	case "Decimal":
		return "enc.WriteString(" + value + ".String())"
	case "Bytes", "JSON":
		return "enc.WriteBytes(" + value + ")"
	}
	return ""
}

func writeEventListMarshal(b *strings.Builder, fieldID int, value, typeName string, isEnum, isObject bool) {
	fmt.Fprintf(b, "\tenc.WriteFieldHeader(%d)\n", fieldID)
	fmt.Fprintf(b, "\tenc.WriteArrayHeader(len(%s))\n", value)
	fmt.Fprintf(b, "\tfor i := range %s {\n", value)
	item := value + "[i]"
	if isObject {
		writeEventObjectValue(b, item, "\t\t")
	} else if isEventBuiltin(typeName) || isEnum {
		fmt.Fprintf(b, "\t\t%s\n", eventScalarWriteStatement(typeName, item, isEnum))
	} else {
		fmt.Fprintf(b, "\t\tdata, _ := json.Marshal(%s)\n", item)
		b.WriteString("\t\tenc.WriteBytes(data)\n")
	}
	b.WriteString("\t}\n")
}

func writeEventObjectMarshal(b *strings.Builder, fieldID int, value string, nullable bool) {
	fmt.Fprintf(b, "\tenc.WriteFieldHeader(%d)\n", fieldID)
	if nullable {
		fmt.Fprintf(b, "\tif %s == nil { enc.WriteNull() } else {\n", value)
		b.WriteString("\t\tenc.WritePresent()\n")
		writeEventObjectValue(b, value, "\t\t")
		b.WriteString("\t}\n")
		return
	}
	b.WriteString("\t{\n")
	writeEventObjectValue(b, value, "\t\t")
	b.WriteString("\t}\n")
}

func writeEventObjectValue(b *strings.Builder, value, indent string) {
	fmt.Fprintf(b, "%sbuf := api.GetBuf()\n", indent)
	fmt.Fprintf(b, "%s%s.WriteLuxo(buf, nil)\n", indent, value)
	fmt.Fprintf(b, "%senc.WriteBytes(buf.B)\n", indent)
	fmt.Fprintf(b, "%sapi.PutBuf(buf)\n", indent)
}

func (g *GeneratorContext) writeEventUnmarshalField(b *strings.Builder, eventName string, param *ast.ParamDecl, isEnum, isObject bool) {
	fieldID := g.eventFieldID(eventName, param.Name)
	if fieldID == 0 || param.Type == nil {
		return
	}
	value := "e." + str.Capitalize(param.Name)
	if param.Type.IsList {
		writeEventListUnmarshal(b, fieldID, value, param.Type.Name, isEnum, isObject)
		return
	}
	if isObject {
		writeEventObjectUnmarshal(b, fieldID, value, param.Type)
		return
	}
	if isEventBuiltin(param.Type.Name) || isEnum {
		writeEventScalarUnmarshal(b, fieldID, value, param.Type, isEnum)
		return
	}
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	fmt.Fprintf(b, "\t\t\tif err := json.Unmarshal(dec.ReadBytes(), &%s); err != nil { return err }\n", value)
}

func writeEventScalarUnmarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, isEnum bool) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	if ref.Name == "Decimal" {
		writeEventDecimalUnmarshal(b, value, ref.Nullable)
		return
	}
	fmt.Fprintf(b, "\t\t\t%s = %s\n", value, eventScalarReadExpression(ref.Name, isEnum, ref.Nullable))
}

func writeEventDecimalUnmarshal(b *strings.Builder, value string, nullable bool) {
	if nullable {
		fmt.Fprintf(b, "\t\t\tif raw := dec.ReadStringPtr(); raw != nil { parsed, err := decimal.NewFromString(*raw); if err != nil { return err }; %s = &parsed }\n", value)
		return
	}
	fmt.Fprintf(b, "\t\t\tparsed, err := decimal.NewFromString(dec.ReadString()); if err != nil { return err }; %s = parsed\n", value)
}

func eventScalarReadExpression(typeName string, isEnum, nullable bool) string {
	if nullable {
		switch typeName {
		case "Int":
			return "dec.ReadIntPtr()"
		case "Float":
			return "dec.ReadFloatPtr()"
		case "String":
			return "dec.ReadStringPtr()"
		case "Boolean":
			return "dec.ReadBoolPtr()"
		case "Bytes", "JSON":
			if typeName == "JSON" {
				return "func() *json.RawMessage { raw := dec.ReadBytesValuePtr(); if raw == nil { return nil }; value := json.RawMessage(*raw); return &value }()"
			}
			return "dec.ReadBytesValuePtr()"
		default:
			return eventNullableReadClosure(typeName, isEnum)
		}
	}
	if isEnum {
		return typeName + "(dec.ReadString())"
	}
	switch typeName {
	case "Int":
		return "dec.ReadInt()"
	case "Float":
		return "dec.ReadFloat()"
	case "String":
		return "dec.ReadString()"
	case "Boolean":
		return "dec.ReadBool()"
	case "DateTime":
		return "time.Unix(dec.ReadInt(), 0).UTC()"
	case "Duration":
		return "time.Duration(dec.ReadInt())"
	case "UUID":
		return "uuid.UUID(dec.ReadUUID())"
	case "Bytes":
		return "dec.ReadBytes()"
	case "JSON":
		return "json.RawMessage(dec.ReadBytes())"
	}
	return ""
}

func eventNullableReadClosure(typeName string, isEnum bool) string {
	goType := mapBaseType(typeName)
	read := "dec.ReadIntPtr()"
	conversion := "time.Duration(*raw)"
	switch {
	case isEnum:
		read = "dec.ReadStringPtr()"
		conversion = typeName + "(*raw)"
	case typeName == "DateTime":
		conversion = "time.Unix(*raw, 0).UTC()"
	case typeName == "UUID":
		read = "dec.ReadUUIDPtr()"
		conversion = "uuid.UUID(*raw)"
	}
	return "func() *" + goType + " { raw := " + read + "; if raw == nil { return nil }; value := " + conversion + "; return &value }()"
}

func writeEventListUnmarshal(b *strings.Builder, fieldID int, value, typeName string, isEnum, isObject bool) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	b.WriteString("\t\t\tcount := dec.ReadArrayLength()\n")
	fmt.Fprintf(b, "\t\t\t%s = make([]%s, count)\n", value, mapBaseType(typeName))
	fmt.Fprintf(b, "\t\t\tfor i := range %s {\n", value)
	item := value + "[i]"
	if isObject {
		writeEventObjectReadValue(b, item, "\t\t\t\t")
	} else if typeName == "Decimal" {
		b.WriteString("\t\t\t\tparsed, err := decimal.NewFromString(dec.ReadString()); if err != nil { return err }\n")
		fmt.Fprintf(b, "\t\t\t\t%s = parsed\n", item)
	} else if isEventBuiltin(typeName) || isEnum {
		fmt.Fprintf(b, "\t\t\t\t%s = %s\n", item, eventScalarReadExpression(typeName, isEnum, false))
	} else {
		fmt.Fprintf(b, "\t\t\t\tif err := json.Unmarshal(dec.ReadBytes(), &%s); err != nil { return err }\n", item)
	}
	b.WriteString("\t\t\t}\n")
}

func writeEventObjectUnmarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	if ref.Nullable {
		b.WriteString("\t\t\tif dec.ReadBool() {\n")
		fmt.Fprintf(b, "\t\t\t\t%s = &%s{}\n", value, ref.Name)
		writeEventObjectReadValue(b, value, "\t\t\t\t")
		b.WriteString("\t\t\t}\n")
		return
	}
	writeEventObjectReadValue(b, value, "\t\t\t")
}

func writeEventObjectReadValue(b *strings.Builder, value, indent string) {
	fmt.Fprintf(b, "%sraw := dec.ReadBytes()\n", indent)
	fmt.Fprintf(b, "%snested := codec.NewDecoder(raw)\n", indent)
	fmt.Fprintf(b, "%s%s.ReadLuxo(nested)\n", indent, value)
	fmt.Fprintf(b, "%sif err := nested.Err(); err != nil { return err }\n", indent)
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
func (g *GeneratorContext) generateRegisterEvents(b *strings.Builder, listeners []*ast.OnDecl, moduleName string, currentModule string, models map[string]*ast.ModelDecl, enums map[string]bool) {
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
		if g.events != nil {
			evModule := g.events.EventModule[l.EventName]
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
				generator: g,
				b:         b,
				indent:    "\t\t",
				models:    models,
				enums:     enums,
				vars:      make(map[string]valType),
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
func (g *GeneratorContext) collectCrossModuleEventImports(result *semantic.Result, listeners []*ast.OnDecl, currentModule string) map[string]string {
	imports := make(map[string]string)
	if g.events == nil {
		return imports
	}
	for _, l := range listeners {
		evModule := g.events.EventModule[l.EventName]
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
					evModule := g.events.EventModule[emit.EventName]
					if evModule != "" && evModule != currentModule {
						imports[evModule] = evModule + "_luxo"
					}
				}
			}
		}
	}
	return imports
}
