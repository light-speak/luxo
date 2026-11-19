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
	g = g.bindingContext(result, nil)
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

	crossModuleImports := g.collectCrossModuleEventImports(listeners, currentModule)
	g.addImportBindingSymbols(crossModuleImports)
	enums := CollectEnumsFromResult(result)
	objects := collectEventObjectTypes(result)

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if len(events) > 0 || len(nativeEventNames(listeners)) > 0 {
		b.WriteString("\t\"fmt\"\n")
	}
	if len(events) > 0 {
		if eventsUseObjects(events, objects) {
			b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
		}
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
		fmt.Fprintf(&b, "\t%s \"%s/%s/luxo\"\n", crossModuleImports[modName], g.events.ModulePath, modName)
	}
	importPrefix := b.String()
	b.Reset()

	// Event structs + MarshalLuxo/UnmarshalLuxo
	for _, e := range events {
		generateEventStruct(&b, e)
		g.generateEventCodec(&b, e, enums, objects)
	}
	g.generateEventSnapshots(&b, result, events, enums)

	// Emit functions use owned static snapshots in memory, encoding only for NATS.
	for _, e := range events {
		generateEmitFunc(&b, e)
	}

	// Reuse the API model registry so listeners also recognize extend projections.
	modelMap, _ := collectInferredAPIs(result)

	g.writeNativeEventHandlers(&b, nativeEventNames(listeners), currentModule)

	// RegisterEvents function — wires all on-listeners
	g.generateRegisterEvents(&b, listeners, currentModule, currentModule, modelMap, enums, result)

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

	body := b.String()
	var out strings.Builder
	out.WriteString(importPrefix)
	writeEventBodyImports(&out, body)
	out.WriteString(")\n\n")
	out.WriteString(body)
	return []byte(out.String())
}

// Imports follow generated references, including expressions nested in listeners.
func writeEventBodyImports(b *strings.Builder, body string) {
	writeScalarTypeImports(b, body)
	packages := generatedPackages(body)
	for _, name := range []string{"bytes", "strconv", "strings"} {
		if packages.has(name) {
			fmt.Fprintf(b, "\t%q\n", name)
		}
	}
	writeRuntimeUtilityImports(b, packages)
	for _, name := range []string{"errors", "selection", "luxolog"} {
		if packages.has(name) {
			fmt.Fprintf(b, "\t%q\n", "github.com/light-speak/luxo/pkg/lux/"+name)
		}
	}
	if packages.has("lux") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	}
	if packages.has("luxocrypto") {
		b.WriteString("\tluxocrypto \"github.com/light-speak/luxo/pkg/lux/crypto\"\n")
	}
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
		for _, declaration := range file.Sealeds {
			objects[declaration.Name] = true
		}
	}
	return objects
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
	c := eventWireCodec{name: e.Name + "Event", marshalName: "MarshalLuxo", unmarshalName: "UnmarshalLuxo", fields: e.Params, ids: g.ids.EventFields[e.Name], generator: g}
	c.write(b, enums, objects)
}

func (g *GeneratorContext) writeEventMarshalField(b *strings.Builder, eventName string, param *ast.ParamDecl, isEnum, isObject bool) {
	writeEventMarshalField(b, g.eventFieldID(eventName, param.Name), param, isEnum, isObject)
}

func writeEventMarshalField(b *strings.Builder, fieldID int, param *ast.ParamDecl, isEnum, isObject bool, methods ...string) {
	if fieldID == 0 || param.Type == nil {
		return
	}
	value := "e." + str.Capitalize(param.Name)
	if param.Type.IsList {
		writeEventListMarshal(b, fieldID, value, param.Type, isEnum, isObject, methods...)
		return
	}
	if isObject {
		writeEventObjectMarshal(b, fieldID, value, param.Type.Nullable, methods...)
		return
	}
	if isEventBuiltin(param.Type.Name) || isEnum {
		writeEventScalarMarshal(b, fieldID, value, param.Type, isEnum)
	}
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
		return fmt.Sprintf("if !%s.IsValid() { return nil, codec.ErrInvalidEnum }; ", value) + binaryCallStatement(fmt.Sprintf("enc.WriteString(string(%s))", value), "return nil, err")
	}
	switch typeName {
	case "Int":
		return "enc.WriteInt(" + value + ")"
	case "Float":
		return binaryCallStatement("enc.WriteFloat("+value+")", "return nil, err")
	case "String":
		return binaryCallStatement("enc.WriteString("+value+")", "return nil, err")
	case "Boolean":
		return "enc.WriteBool(" + value + ")"
	case "DateTime":
		return binaryCallStatement("enc.WriteDateTime("+value+")", "return nil, err")
	case "Duration":
		return "enc.WriteInt(int64(" + value + "))"
	case "UUID":
		return "enc.WriteUUID([16]byte(" + value + "))"
	case "Decimal":
		return binaryCallStatement("enc.WriteString(lux.FormatDecimal("+value+"))", "return nil, err")
	case "Bytes":
		return "enc.WriteBytes(" + value + ")"
	case "JSON":
		return binaryCallStatement("enc.WriteJSON("+value+")", "return nil, err")
	}
	return ""
}

func writeEventListMarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, isEnum, isObject bool, methods ...string) {
	fmt.Fprintf(b, "\tenc.WriteFieldHeader(%d)\n", fieldID)
	if ref.Nullable {
		fmt.Fprintf(b, "\tif %s == nil { enc.WriteNull() } else {\n\tenc.WritePresent()\n", value)
	}
	writeBinaryCall(b, "\t", fmt.Sprintf("enc.WriteArrayHeader(len(%s))", value), "return nil, err")
	fmt.Fprintf(b, "\tfor i := range %s {\n", value)
	item := value + "[i]"
	if isObject {
		writeEventObjectValue(b, item, "\t\t", methods...)
	} else if isEventBuiltin(ref.Name) || isEnum {
		fmt.Fprintf(b, "\t\t%s\n", eventScalarWriteStatement(ref.Name, item, isEnum))
	}
	b.WriteString("\t}\n")
	if ref.Nullable {
		b.WriteString("\t}\n")
	}
}

func writeEventObjectMarshal(b *strings.Builder, fieldID int, value string, nullable bool, methods ...string) {
	fmt.Fprintf(b, "\tenc.WriteFieldHeader(%d)\n", fieldID)
	if nullable {
		fmt.Fprintf(b, "\tif %s == nil { enc.WriteNull() } else {\n", value)
		b.WriteString("\t\tenc.WritePresent()\n")
		writeEventObjectValue(b, value, "\t\t", methods...)
		b.WriteString("\t}\n")
		return
	}
	b.WriteString("\t{\n")
	writeEventObjectValue(b, value, "\t\t", methods...)
	b.WriteString("\t}\n")
}

func writeEventObjectValue(b *strings.Builder, value, indent string, methods ...string) {
	method := "marshalEventSnapshot"
	if len(methods) != 0 {
		method = methods[0]
	}
	fmt.Fprintf(b, "%sbuf := api.GetBuf()\n", indent)
	fmt.Fprintf(b, "%sraw, err := %s.%s(buf.B, depth + 1)\n", indent, value, method)
	fmt.Fprintf(b, "%sif err != nil { api.PutBuf(buf); return nil, err }\n", indent)
	fmt.Fprintf(b, "%sbuf.B = raw\n%senc.WriteBytes(raw)\n%sapi.PutBuf(buf)\n", indent, indent, indent)
}

func (g *GeneratorContext) writeEventUnmarshalField(b *strings.Builder, eventName string, param *ast.ParamDecl, isEnum, isObject bool) {
	writeEventUnmarshalField(b, g.eventFieldID(eventName, param.Name), param, isEnum, isObject, false)
}

func writeEventUnmarshalField(b *strings.Builder, fieldID int, param *ast.ParamDecl, isEnum, isObject, objectPointer bool, methods ...string) {
	if fieldID == 0 || param.Type == nil {
		return
	}
	value := "e." + str.Capitalize(param.Name)
	if param.Type.IsList {
		writeEventListUnmarshal(b, fieldID, value, param.Type, isEnum, isObject, methods...)
		return
	}
	if isObject {
		method := "unmarshalEventSnapshot"
		if len(methods) != 0 {
			method = methods[0]
		}
		writeEventObjectUnmarshalMethod(b, fieldID, value, param.Type, method, objectPointer)
		return
	}
	if isEventBuiltin(param.Type.Name) || isEnum {
		writeEventScalarUnmarshal(b, fieldID, value, param.Type, isEnum)
	}
}

func writeEventScalarUnmarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, isEnum bool) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	if ref.Name == "Decimal" {
		writeEventDecimalUnmarshal(b, value, ref.Nullable)
		return
	}
	fmt.Fprintf(b, "\t\t\t%s = %s\n", value, eventScalarReadExpression(ref.Name, isEnum, ref.Nullable))
	if isEnum {
		writeEnumReadCheck(b, value, ref.Nullable, "\t\t\t")
	}
}

func writeEventDecimalUnmarshal(b *strings.Builder, value string, nullable bool) {
	if nullable {
		fmt.Fprintf(b, "\t\t\tif raw := dec.ReadStringPtr(); raw != nil { parsed, err := decimal.NewFromString(*raw); if err != nil { return err }; %s = &parsed } else { %s = nil }\n", value, value)
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
				return "func() *json.RawMessage { raw := dec.ReadJSONPtr(); if raw == nil { return nil }; value := json.RawMessage(*raw); return &value }()"
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
		return "dec.ReadDateTime()"
	case "Duration":
		return "time.Duration(dec.ReadInt())"
	case "UUID":
		return "uuid.UUID(dec.ReadUUID())"
	case "Bytes":
		return "dec.ReadBytes()"
	case "JSON":
		return "json.RawMessage(dec.ReadJSON())"
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
		return "dec.ReadDateTimePtr()"
	case typeName == "UUID":
		read = "dec.ReadUUIDPtr()"
		conversion = "uuid.UUID(*raw)"
	}
	return "func() *" + goType + " { raw := " + read + "; if raw == nil { return nil }; value := " + conversion + "; return &value }()"
}

func writeEventListUnmarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, isEnum, isObject bool, methods ...string) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	if ref.Nullable {
		fmt.Fprintf(b, "\t\t\tif !dec.ReadBool() { %s = nil; break }\n", value)
	}
	typeName := ref.Name
	if typeName == "DateTime" {
		fmt.Fprintf(b, "\t\t\t%s = dec.ReadDateTimeArray()\n", value)
		return
	}
	b.WriteString("\t\t\tcount := dec.ReadArrayLength()\n")
	b.WriteString("\t\t\tif !dec.CheckElementBytes(count, 1) { break }\n")
	fmt.Fprintf(b, "\t\t\t%s = make([]%s, count)\n", value, mapBaseType(typeName))
	fmt.Fprintf(b, "\t\t\tfor i := range %s {\n", value)
	item := value + "[i]"
	if isObject {
		writeEventObjectReadValue(b, item, "\t\t\t\t", methods...)
	} else if typeName == "Decimal" {
		b.WriteString("\t\t\t\tparsed, err := decimal.NewFromString(dec.ReadString()); if err != nil { return err }\n")
		fmt.Fprintf(b, "\t\t\t\t%s = parsed\n", item)
	} else if isEventBuiltin(typeName) || isEnum {
		fmt.Fprintf(b, "\t\t\t\t%s = %s\n", item, eventScalarReadExpression(typeName, isEnum, false))
		if isEnum {
			writeEnumReadCheck(b, item, false, "\t\t\t\t")
		}
	}
	b.WriteString("\t\t\t\tif dec.Err() != nil { break }\n")
	b.WriteString("\t\t\t}\n")
}

func writeEventObjectUnmarshal(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, objectPointer ...bool) {
	writeEventObjectUnmarshalMethod(b, fieldID, value, ref, "unmarshalEventSnapshot", objectPointer...)
}

func writeEventObjectUnmarshalMethod(b *strings.Builder, fieldID int, value string, ref *ast.TypeRef, method string, objectPointer ...bool) {
	fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
	if ref.Nullable {
		b.WriteString("\t\t\tif dec.ReadBool() {\n")
		fmt.Fprintf(b, "\t\t\t\t%s = &%s{}\n", value, ref.Name)
		writeEventObjectReadValue(b, value, "\t\t\t\t", method)
		fmt.Fprintf(b, "\t\t\t} else { %s = nil }\n", value)
		return
	}
	if len(objectPointer) > 0 && objectPointer[0] {
		fmt.Fprintf(b, "\t\t\t%s = &%s{}\n", value, ref.Name)
	}
	writeEventObjectReadValue(b, value, "\t\t\t", method)
}

func writeEventObjectReadValue(b *strings.Builder, value, indent string, methods ...string) {
	method := "unmarshalEventSnapshot"
	if len(methods) != 0 {
		method = methods[0]
	}
	fmt.Fprintf(b, "%sraw := dec.ReadBytes()\n", indent)
	fmt.Fprintf(b, "%sif err := dec.Err(); err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif err := %s.%s(raw, depth + 1); err != nil { return err }\n", indent, value, method)
}

// generateEmitFunc generates a typed emit function.
// ChanBus captures a validated independent snapshot; NATSBus serializes only
// at the wire boundary. Neither path uses an encode/decode copying round trip.
func generateEmitFunc(b *strings.Builder, e *ast.EventDecl) {
	fmt.Fprintf(b, "// Emit%s publishes a %s event.\n", e.Name, e.Name)
	fmt.Fprintf(b, "func Emit%s(ctx context.Context, bus event.Bus, e %sEvent) error {\n", e.Name, e.Name)
	fmt.Fprintf(b, "\treturn bus.Emit(ctx, %q, &e)\n", e.Name)
	fmt.Fprintf(b, "}\n\n")
}

// generateRegisterEvents generates RegisterEvents that wires all on-listeners.
// Default uses OnQueueValueDecode with moduleName as the queue group (competing consumers).
// Listeners with @broadcast use OnValueDecode (every instance receives).
// Unmarshal uses Luxo binary (UnmarshalLuxo) for wire decoding.
func (g *GeneratorContext) generateRegisterEvents(b *strings.Builder, listeners []*ast.OnDecl, moduleName string, currentModule string, models map[string]*ast.ModelDecl, enums map[string]bool, result *semantic.Result) {
	if len(listeners) == 0 {
		b.WriteString("// RegisterEvents — no listeners in this module.\n")
		b.WriteString("func RegisterEvents(bus event.Bus, app *App) error { return nil }\n\n")
		return
	}
	b.WriteString("// RegisterEvents registers all event listeners with the bus.\n")
	b.WriteString("func RegisterEvents(bus event.Bus, app *App) error {\n")
	writeNativeEventChecks(b, nativeEventNames(listeners))

	functions := collectFunctionDeclarations(result)
	nativeFunctions := collectNativeFunctionNames(result)
	compiledFunctions := collectCompiledFunctions(result)
	selectionFunctions := collectSelectionAwareFunctions(compiledFunctions, models)
	scopeFunctions := collectScopeAwareFunctions(compiledFunctions)
	explicitSelections := collectSelectionAwareFunctions(compiledFunctions)
	for _, l := range listeners {
		sourceName, paramName, bindings := g.listenerPayloadBinding(l)

		eventTypePrefix := g.eventTypePrefix(l.EventName, currentModule)
		eventType := eventTypePrefix + l.EventName + "Event"
		if l.Native {
			writeNativeEventRegistration(b, l, moduleName, eventType)
			continue
		}
		if l.Broadcast {
			fmt.Fprintf(b, "\tif err := event.OnValueDecode(bus, %q, ", l.EventName)
		} else {
			fmt.Fprintf(b, "\tif err := event.OnQueueValueDecode(bus, %q, %q, ", l.EventName, moduleName)
		}
		writeEventValueDecoder(b, eventType)
		fmt.Fprintf(b, ", func(ctx context.Context, %s %s) error {\n", paramName, eventType)
		// Compile on-handler body if present
		if l.Body != nil && len(l.Body.Stmts) > 0 {
			loadSelections := analyzeLoadProjections(l.Body, models, nil)
			if len(loadSelections) > 0 || bodyCallsScopedFunction(l.Body, scopeFunctions) {
				writeRequestScope(b, "\t\t", "ctx")
			}
			c := &compiler{
				generator:          g,
				b:                  b,
				indent:             "\t\t",
				models:             models,
				enums:              enums,
				vars:               make(map[string]valType),
				bindings:           bindings,
				functions:          functions,
				nativeFunctions:    nativeFunctions,
				selectionFunctions: selectionFunctions,
				explicitSelections: explicitSelections,
				clientSelection:    "nil",
				api:                &ast.ApiDecl{Pos: l.Pos},
				loadSelections:     loadSelections,
			}
			// Register event param as a known variable so event.field compiles correctly
			c.vars[sourceName] = valType{name: l.EventName + "Event"}
			for _, stmt := range l.Body.Stmts {
				c.compileStmt(stmt)
			}
			fmt.Fprintf(b, "\t\treturn nil\n")
		} else {
			fmt.Fprintf(b, "\t\t_ = %s\n", paramName)
			fmt.Fprintf(b, "\t\treturn nil\n")
		}
		b.WriteString("\t}); err != nil {\n\t\treturn err\n\t}\n")
	}

	b.WriteString("\treturn nil\n}\n\n")

	// Note: Unmarshal functions are generated in generateEventFile, not here
}

// A non-capturing decoder preserves static types without adding global symbols.
func writeEventValueDecoder(b *strings.Builder, eventType string) {
	fmt.Fprintf(b, "func(data []byte) (%s, error) { var decoded %s; err := decoded.UnmarshalLuxo(data); return decoded, err }", eventType, eventType)
}

// collectCrossModuleEventImports finds which event modules need to be imported.
func (g *GeneratorContext) collectCrossModuleEventImports(listeners []*ast.OnDecl, currentModule string) map[string]string {
	imports := make(map[string]string)
	if g.events == nil {
		return imports
	}
	add := func(name string) {
		module := g.events.EventModule[name]
		if module != "" && module != currentModule {
			imports[module] = module + "_luxo"
		}
	}
	visitor := ast.Visitor{Stmt: func(stmt ast.Stmt) {
		if emit, ok := stmt.(*ast.EmitStmt); ok {
			add(emit.EventName)
		}
	}}
	for _, listener := range listeners {
		add(listener.EventName)
		ast.Walk(listener.Body, visitor)
	}
	return imports
}

// Keep the source payload binding separate from callback implementation names.
func listenerPayloadBinding(listener *ast.OnDecl) (string, string, localBindings) {
	return defaultGenerator().listenerPayloadBinding(listener)
}

func (g *GeneratorContext) listenerPayloadBinding(listener *ast.OnDecl) (string, string, localBindings) {
	source := "it"
	if len(listener.Params) > 0 {
		source = listener.Params[0]
	}
	bindings := g.parameterBindings([]*ast.ParamDecl{{Name: source}}, listener.Body, nil, nil, "payload")
	name := bindings.name(source)
	if len(listener.Params) == 0 {
		name = "payload"
		if bindings == nil {
			bindings = make(localBindings)
		}
		bindings[source] = name
	}
	return source, name, bindings
}
