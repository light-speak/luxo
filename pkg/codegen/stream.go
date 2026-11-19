package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// streamInfo describes a @stream API for codegen.
type streamInfo struct {
	apiName         string
	eventName       string // from @stream(EventName), empty if no event source
	isNative        bool   // @native — Go resolver handles matching
	hasBody         bool   // has lambda matcher body
	params          []*ast.ParamDecl
	body            *ast.Block       // lambda body for matcher
	eventParams     []*ast.ParamDecl // event parameters (for decoding)
	eventDecl       *ast.EventDecl
	returnType      *ast.TypeRef
	payload         *ast.ParamDecl
	payloadKind     streamPayloadKind
	payloadEnum     bool
	payloadIdentity bool
	identityFields  []int
	eventModule     string
	payloadModule   string
	sourceModule    string
	enums           map[string]bool
	requiresAuth    bool
	auth            *ast.Directive
}

type streamPayloadKind uint8

const (
	streamPayloadScalar streamPayloadKind = iota
	streamPayloadModel
	streamPayloadType
)

type streamTypes struct {
	events map[string]*ast.EventDecl
	models map[string]bool
	types  map[string]bool
	enums  map[string]bool
	fields map[string][]*ast.FieldDecl
}

// collectStreams finds all @stream APIs in the semantic result.
func collectStreams(result *semantic.Result) []streamInfo {
	return defaultGenerator().collectStreams(result)
}

func (g *GeneratorContext) collectStreams(result *semantic.Result) []streamInfo {
	if !hasStreamAPI(result) {
		return nil
	}
	declarations := g.collectStreamTypes(result)
	var streams []streamInfo
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if !hasDirective(api.Directives, "stream") {
				continue
			}
			si := g.newStreamInfo(file, api, declarations)
			if si.payload == nil {
				g.classifyStreamPayload(&si, api.ReturnType, declarations.models, declarations.types, declarations.enums)
			}
			if si.returnType != nil {
				si.payloadIdentity = streamOutputUsesIdentity(si.returnType.Name, declarations)
				if si.payloadIdentity {
					si.identityFields = g.streamIdentityFields(si.returnType.Name, declarations)
				}
			}
			streams = append(streams, si)
		}
	}
	return streams
}

// Unknown cross-module object declarations cannot prove identity independence.
// Walk each reachable type once, including recursive snapshots.
func streamOutputUsesIdentity(root string, declarations streamTypes) bool {
	pending := []string{root}
	visited := make(map[string]bool)
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[name] {
			continue
		}
		visited[name] = true
		fields, known := declarations.fields[name]
		if !known && (declarations.models[name] || declarations.types[name]) {
			return true
		}
		for _, field := range fields {
			if hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
				continue
			}
			if streamFieldUsesIdentity(field) {
				return true
			}
			if field.Type != nil {
				pending = append(pending, field.Type.Name)
			}
		}
	}
	return false
}

func (g *GeneratorContext) streamIdentityFields(name string, declarations streamTypes) []int {
	var ids []int
	for _, field := range declarations.fields[name] {
		if field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
			continue
		}
		if !streamFieldUsesIdentity(field) && !streamOutputUsesIdentity(field.Type.Name, declarations) {
			continue
		}
		id := g.modelFieldID(name, field.Name)
		if id == 0 {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

func streamFieldUsesIdentity(field *ast.FieldDecl) bool {
	usesIdentity := false
	for _, directive := range field.Directives {
		if directive.Name != "visible" {
			continue
		}
		ast.WalkExpr(directive.SingleExpression(), func(expr ast.Expr) {
			if ident, ok := expr.(*ast.Ident); ok && ident.Name == "my" {
				usesIdentity = true
			}
		})
	}
	return usesIdentity
}

func hasStreamAPI(result *semantic.Result) bool {
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "stream") {
				return true
			}
		}
	}
	return false
}

func (g *GeneratorContext) collectStreamTypes(result *semantic.Result) streamTypes {
	declarations := streamTypes{
		events: make(map[string]*ast.EventDecl),
		models: make(map[string]bool),
		types:  make(map[string]bool),
		enums:  make(map[string]bool),
		fields: make(map[string][]*ast.FieldDecl),
	}
	for _, file := range result.Files {
		for _, eventDecl := range file.Events {
			declarations.events[eventDecl.Name] = eventDecl
		}
		for _, model := range file.Models {
			declarations.models[model.Name] = true
			declarations.fields[model.Name] = model.Fields
		}
		for _, typeDecl := range file.Types {
			declarations.types[typeDecl.Name] = true
			declarations.fields[typeDecl.Name] = typeDecl.Fields
		}
		for _, declaration := range file.Sealeds {
			collectSealedStreamTypes(&declarations, declaration)
		}
		for _, enum := range file.Enums {
			declarations.enums[enum.Name] = true
		}
	}
	g.mergeGlobalStreamTypes(&declarations)
	return declarations
}

// Variant nodes retain case tags while following identity-dependent payloads.
func collectSealedStreamTypes(declarations *streamTypes, owner *ast.SealedDecl) {
	declarations.types[owner.Name] = true
	fields := make([]*ast.FieldDecl, 0, len(owner.Variants))
	for _, variant := range owner.Variants {
		name := owner.Name + "." + variant.Name
		fields = append(fields, &ast.FieldDecl{Name: variant.Name, Type: &ast.TypeRef{Name: name}})
		declarations.types[name] = true
		payload := make([]*ast.FieldDecl, len(variant.Fields))
		for index, field := range variant.Fields {
			payload[index] = sealedField(field)
		}
		declarations.fields[name] = payload
	}
	declarations.fields[owner.Name] = fields
}

func (g *GeneratorContext) mergeGlobalStreamTypes(declarations *streamTypes) {
	if g.events == nil {
		return
	}
	for name, eventDecl := range g.events.Events {
		if declarations.events[name] == nil {
			declarations.events[name] = eventDecl
		}
	}
	for name := range g.events.ModelModule {
		declarations.models[name] = true
	}
	for name := range g.events.TypeModule {
		declarations.types[name] = true
	}
	for name := range g.events.EnumModule {
		declarations.enums[name] = true
	}
}

func (g *GeneratorContext) newStreamInfo(file *ast.File, api *ast.ApiDecl, declarations streamTypes) streamInfo {
	si := streamInfo{
		apiName:      api.Name,
		isNative:     hasDirective(api.Directives, "native"),
		hasBody:      api.Body != nil,
		params:       api.Params,
		body:         api.Body,
		returnType:   api.ReturnType,
		sourceModule: moduleNameFromFile(file.Name),
		enums:        declarations.enums,
		requiresAuth: hasDirective(api.Directives, "auth") || hasDirective(api.Directives, "role"),
		auth:         findDirective(api.Directives, "auth"),
	}
	si.eventName = streamEventName(api)
	if si.eventName == "" {
		return si
	}
	if g.events != nil {
		si.eventModule = g.events.EventModule[si.eventName]
	}
	g.attachStreamEvent(&si, declarations.events[si.eventName], declarations)
	return si
}

func streamEventName(api *ast.ApiDecl) string {
	for _, directive := range api.Directives {
		if directive.Name != "stream" || len(directive.Args) == 0 {
			continue
		}
		if ident, ok := directive.Args[0].Value.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func (g *GeneratorContext) attachStreamEvent(si *streamInfo, eventDecl *ast.EventDecl, declarations streamTypes) {
	if eventDecl == nil {
		return
	}
	si.eventDecl = eventDecl
	si.eventParams = eventDecl.Params
	for _, param := range eventDecl.Params {
		if sameTypeRef(param.Type, si.returnType) {
			si.payload = param
			g.classifyStreamPayload(si, param.Type, declarations.models, declarations.types, declarations.enums)
			return
		}
	}
}

func (g *GeneratorContext) classifyStreamPayload(si *streamInfo, ref *ast.TypeRef, models, types, enums map[string]bool) {
	if ref == nil {
		return
	}
	if models[ref.Name] {
		si.payloadKind = streamPayloadModel
	} else if types[ref.Name] {
		si.payloadKind = streamPayloadType
	}
	si.payloadEnum = enums[ref.Name]
	if g.events == nil {
		return
	}
	switch si.payloadKind {
	case streamPayloadModel:
		si.payloadModule = g.events.ModelModule[ref.Name]
	case streamPayloadType:
		si.payloadModule = g.events.TypeModule[ref.Name]
	case streamPayloadScalar:
		if si.payloadEnum && !g.usesSharedEnums() {
			si.payloadModule = g.events.EnumModule[ref.Name]
		}
	}
}

// generateStreamFile produces stream.gen.go containing:
// - RegisterStreams() — binds @stream APIs to event bus
// - Matcher functions for @stream APIs with lambda bodies
// - @native matcher stubs (delegating to resolver)
func generateStreamFile(result *semantic.Result, packageName string) []byte {
	return defaultGenerator().generateStreamFile(result, packageName)
}

func (g *GeneratorContext) generateStreamFile(result *semantic.Result, packageName string) []byte {
	g = g.bindingContext(result, nil)
	var streams []streamInfo
	if g.bindingPlan != nil {
		streams = g.bindingPlan.streams
	}
	if len(streams) == 0 {
		return nil
	}

	var b strings.Builder
	g.writeRegisterStreams(&b, streams)
	models, _ := collectInferredAPIs(result)
	g.writeStreamResolverInterface(&b, streams, models)
	writePlannedStreamInputOwners(&b, g.bindingPlan.owners, CollectEnumsFromResult(result))

	// Generate matcher functions
	enums := CollectEnumsFromResult(result)
	for _, si := range streams {
		if si.isNative && si.eventName != "" {
			generateNativeStreamMatcher(&b, si)
		} else if si.hasBody && !si.isNative {
			g.generateLuxoStreamMatcher(&b, si, enums)
		} else if si.requiresAuth {
			generateAuthStreamMatcher(&b, si)
		}
	}

	body := b.String()
	var out strings.Builder
	writeHeader(&out, packageName, "stream.gen.go")
	g.writeStreamImports(&out, streams, body)
	out.WriteString(body)
	return []byte(out.String())
}

func (g *GeneratorContext) writeStreamImports(b *strings.Builder, streams []streamInfo, body string) {
	packages := generatedPackages(body)
	needsContext := false
	needsAuth := false
	remoteModules := make(map[string]bool)
	for _, si := range streams {
		needsAuth = needsAuth || si.requiresAuth
		if si.eventName != "" || si.isNative {
			needsContext = true
		}
		if si.eventModule != "" && si.eventModule != si.sourceModule {
			remoteModules[si.eventModule] = true
		}
		if si.payloadModule != "" && si.payloadModule != si.sourceModule {
			remoteModules[si.payloadModule] = true
		}
		if g.events != nil && !g.usesSharedEnums() && si.body != nil {
			collectStreamEnumModules(si.body, g.events.EnumModule, si.sourceModule, remoteModules)
		}
	}

	b.WriteString("import (\n")
	if packages.has("bytes") {
		b.WriteString("\t\"bytes\"\n")
	}
	if needsContext {
		b.WriteString("\t\"context\"\n")
	}
	if packages.has("fmt") {
		b.WriteString("\t\"fmt\"\n")
	}
	writeScalarTypeImports(b, body)
	writeOptionalStandardImports(b, packages, "math")
	if packages.has("lux") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	if needsAuth {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/auth\"\n")
	}
	if packages.has("errors") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	}
	if packages.has("codec") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	keepReferencedGeneratedModules(body, remoteModules)
	modules := make([]string, 0, len(remoteModules))
	for module := range remoteModules {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		fmt.Fprintf(b, "\t%s_luxo %q\n", module, g.events.ModulePath+"/"+module+"/luxo")
	}
	b.WriteString(")\n\n")
}

func (g *GeneratorContext) writeRegisterStreams(b *strings.Builder, streams []streamInfo) {
	b.WriteString("// RegisterStreams binds @stream APIs to the event bus.\n")
	b.WriteString("// Events trigger StreamHub.Dispatch which pushes to matching WS subscribers.\n")
	b.WriteString("// On failure, close the owned bus and discard this startup router; do not retry partial registration.\n")
	b.WriteString("func RegisterStreams(router *api.Router, bus event.Bus, resolver StreamResolver) error {\n")
	for _, si := range streams {
		if id := g.apiID(si.apiName); id != 0 {
			writeRegistryRoute(b, si.apiName, id)
			g.writeAPIParameterRegistration(b, si.apiName, si.params, si.enums)
		}
		fmt.Fprintf(b, "\trouter.RequireStream(%q)\n", si.apiName)
	}

	for _, si := range streams {
		if si.isNative || si.eventName == "" {
			continue
		}

		matcherName := "nil"
		if si.hasBody || si.isNative || si.requiresAuth {
			matcherName = "match" + str.Capitalize(si.apiName)
		}

		if si.eventName != "" {
			fmt.Fprintf(b, "\t// %s @stream(%s)\n", si.apiName, si.eventName)
			writeTypedStreamRegistration(b, si, matcherName)
			continue
		}
	}

	b.WriteString("\tif resolver == nil { return nil }\n")
	// Register native event matchers and source-less handlers only where the
	// owning service injected its resolver. Gateways inject RPC proxies instead.
	for _, si := range streams {
		if !si.isNative {
			continue
		}
		if si.eventName != "" {
			matcherName := "match" + str.Capitalize(si.apiName)
			fmt.Fprintf(b, "\t// %s @stream(%s) @native\n", si.apiName, si.eventName)
			writeTypedStreamRegistration(b, si, matcherName)
		} else {
			writeNativeStreamPreparation(b, si)
		}
	}

	b.WriteString("\treturn nil\n}\n\n")
}

func writeTypedStreamRegistration(b *strings.Builder, si streamInfo, matcherName string) {
	eventPrefix := streamModulePrefix(si.eventModule, si.sourceModule)
	eventType := eventPrefix + si.eventName + "Event"
	payloadField := "payload." + str.Capitalize(si.payload.Name)
	fmt.Fprintf(b, "\tif bus == nil { return fmt.Errorf(%q) }\n", "stream "+si.apiName+": event bus is required")
	writeEventStreamPreparation(b, si)
	fmt.Fprintf(b, "\tif err := event.OnValueDecode[%s](bus, %q, ", eventType, si.eventName)
	writeEventValueDecoder(b, eventType)
	fmt.Fprintf(b, ", func(ctx context.Context, payload %s) error {\n", eventType)
	dispatch, encoderIdentity, identity := "Dispatch", "", ""
	if si.payloadIdentity {
		dispatch, encoderIdentity, identity = "DispatchIdentity", ", identity any", "identity"
		if len(si.identityFields) != 0 {
			dispatch = "DispatchIdentitySelection"
		}
	}
	if si.isNative {
		fmt.Fprintf(b, "\t\treturn stream%s.%s(func(params *%sStreamParams, identity any) (bool, error) { return %s(payload, params, identity, resolver), nil }, func(mask []byte, binary bool%s) ([]byte, error) {\n", str.Capitalize(si.apiName), dispatch, str.Capitalize(si.apiName), matcherName, encoderIdentity)
	} else {
		fmt.Fprintf(b, "\t\treturn stream%s.%s(%s, func(mask []byte, binary bool%s) ([]byte, error) {\n", str.Capitalize(si.apiName), dispatch, preparedStreamMatcher(si, matcherName), encoderIdentity)
	}
	writeStreamEncoderBody(b, si, payloadField, identity, "\t\t\t")
	if dispatch == "DispatchIdentitySelection" {
		b.WriteString("\t\t}, func(mask []byte) bool {\n\t\t\tmask = codec.SelectionMaskFields(mask)\n\t\t\treturn ")
		for index, id := range si.identityFields {
			if index != 0 {
				b.WriteString(" && ")
			}
			fmt.Fprintf(b, "!codec.FieldMaskHas(mask, %d)", id)
		}
		b.WriteString("\n\t\t})\n")
	} else {
		b.WriteString("\t\t})\n")
	}
	fmt.Fprintf(b, "\t}); err != nil { return fmt.Errorf(%q, err) }\n", "stream "+si.apiName+": register "+si.eventName+": %w")
}

func writeStreamEncoderBody(b *strings.Builder, si streamInfo, value, identity, indent string) {
	fmt.Fprintf(b, "%sbuf := api.GetBuf()\n", indent)
	if identity != "" {
		fmt.Fprintf(b, "%sbuf.Identity = %s\n", indent, identity)
	}
	writeStreamRootEncoding(b, si, value, indent)
	fmt.Fprintf(b, "%sdata := append([]byte(nil), buf.B...)\n", indent)
	fmt.Fprintf(b, "%sapi.PutBuf(buf)\n", indent)
	fmt.Fprintf(b, "%sif !binary {\n", indent)
	fmt.Fprintf(b, "%s\treturn router.StreamPayloadJSON(%q, data)\n", indent, si.apiName)
	fmt.Fprintf(b, "%s}\n", indent)
	fmt.Fprintf(b, "%sreturn data, nil\n", indent)
}

func writeStreamRootEncoding(b *strings.Builder, si streamInfo, value, indent string) {
	if !si.returnType.Nullable {
		writeStreamPayloadEncoding(b, si, value, indent)
		return
	}
	fmt.Fprintf(b, "%sif %s {\n", indent, nullableNullCondition(si.returnType, value))
	fmt.Fprintf(b, "%s\tbuf.B = codec.AppendNull(buf.B)\n", indent)
	fmt.Fprintf(b, "%s} else {\n", indent)
	fmt.Fprintf(b, "%s\tbuf.B = codec.AppendPresent(buf.B)\n", indent)
	if !si.returnType.IsList {
		value = "(*" + value + ")"
	}
	writeStreamPayloadEncoding(b, si, value, indent+"\t")
	fmt.Fprintf(b, "%s}\n", indent)
}

func preparedStreamMatcher(si streamInfo, matcherName string) string {
	if matcherName == "nil" {
		return "nil"
	}
	if !si.hasBody {
		return "func(params *" + str.Capitalize(si.apiName) + "StreamParams, identity any) (bool, error) { return " + matcherName + "(params, identity), nil }"
	}
	expr := extractMatcherExpr(si.body)
	fields := collectItFields(expr)
	params := make(map[string]*ast.ParamDecl, len(si.eventParams))
	for _, param := range si.eventParams {
		params[param.Name] = param
	}
	args := make([]string, 0, len(fields)+2)
	for _, name := range fields {
		param := params[name]
		if param != nil {
			args = append(args, streamEventValue("payload."+str.Capitalize(name), param.Type, si.enums[param.Type.Name]))
		}
	}
	args = append(args, "params", "identity")
	result := matcherName + "(" + strings.Join(args, ", ") + ")"
	if !conditionHasCheckedArithmetic(expr) {
		result += ", nil"
	}
	return "func(params *" + str.Capitalize(si.apiName) + "StreamParams, identity any) (bool, error) { return " + result + " }"
}

func streamEventValue(value string, ref *ast.TypeRef, isEnum bool) string {
	if isEnum {
		return "string(" + value + ")"
	}
	switch ref.Name {
	case "Duration":
		return "int64(" + value + ")"
	case "UUID":
		return "[16]byte(" + value + ")"
	default:
		return value
	}
}

func streamModulePrefix(module, source string) string {
	if module == "" || module == source {
		return ""
	}
	return module + "_luxo."
}

func writeStreamPayloadEncoding(b *strings.Builder, si streamInfo, value, indent string) {
	ref := si.returnType
	scalarName := ref.Name
	if si.payloadEnum {
		scalarName = streamModulePrefix(si.payloadModule, si.sourceModule) + scalarName
	}
	if ref.IsList {
		if appendExpr, ok := binaryScalarAppend(scalarName, "buf.B", "item", si.payloadEnum); ok {
			writeBinaryAppend(b, indent, "buf.B", fmt.Sprintf("codec.AppendArrayHeader(buf.B, len(%s))", value), "api.PutBuf(buf); return nil, err")
			fmt.Fprintf(b, "%sfor _, item := range %s {\n", indent, value)
			writeBinaryAppend(b, indent+"\t", "buf.B", appendExpr, "api.PutBuf(buf); return nil, err")
			fmt.Fprintf(b, "%s}\n", indent)
			return
		}
		writeBinaryCall(b, indent, fmt.Sprintf("%sWriteColumnar%s(buf, %s, mask)", streamModulePrefix(si.payloadModule, si.sourceModule), ref.Name, value), "api.PutBuf(buf); return nil, err")
		return
	}
	if appendExpr, ok := binaryScalarAppend(scalarName, "buf.B", value, si.payloadEnum); ok {
		writeBinaryAppend(b, indent, "buf.B", appendExpr, "api.PutBuf(buf); return nil, err")
		return
	}
	writeBinaryCall(b, indent, fmt.Sprintf("%s.WriteLuxo(buf, mask)", value), "api.PutBuf(buf); return nil, err")
}

func writeStreamResolverInterface(b *strings.Builder, streams []streamInfo, modelSets ...map[string]*ast.ModelDecl) {
	defaultGenerator().writeStreamResolverInterface(b, streams, modelSets...)
}

func (g *GeneratorContext) writeStreamResolverInterface(b *strings.Builder, streams []streamInfo, modelSets ...map[string]*ast.ModelDecl) {
	b.WriteString("// StreamResolver provides typed @stream @native implementations.\n")
	b.WriteString("type StreamResolver interface {\n")
	for _, si := range streams {
		if !si.isNative {
			continue
		}
		if si.eventName != "" {
			eventType := streamModulePrefix(si.eventModule, si.sourceModule) + si.eventName + "Event"
			fmt.Fprintf(b, "\tMatch%s(event %s, params *%sStreamParams, identity any) bool\n", str.Capitalize(si.apiName), eventType, str.Capitalize(si.apiName))
			continue
		}
		fmt.Fprintf(b, "\tHandle%s(ctx context.Context, params *%sStreamParams, identity any, stream *api.TypedStream[%s]) error\n", str.Capitalize(si.apiName), str.Capitalize(si.apiName), streamReturnGoType(si))
	}
	b.WriteString("}\n\n")

	for _, si := range streams {
		g.writeStreamParameterSnapshot(b, si, si.enums, modelSets...)
		writePreparedStreamAuth(b, si)
		if si.isNative && si.eventName == "" {
			generateNativeStreamHandler(b, si)
		}
	}
}

func streamReturnGoType(si streamInfo) string {
	if si.returnType == nil {
		return "struct{}"
	}
	base := resolveGoType(&ast.TypeRef{Name: streamModulePrefix(si.payloadModule, si.sourceModule) + si.returnType.Name, Nullable: si.returnType.Nullable && !si.returnType.IsList})
	if si.returnType.IsList {
		if si.payloadKind == streamPayloadModel {
			return "[]*" + base
		}
		return "[]" + base
	}
	return base
}

// generateNativeStreamHandler generates a handler wrapper for @stream @native without event source.
// It delegates to resolver.Handle<ApiName>(ctx, params, identity, stream).
func generateNativeStreamHandler(b *strings.Builder, si streamInfo) {
	name := "handleStream" + str.Capitalize(si.apiName)
	resolverMethod := "Handle" + str.Capitalize(si.apiName)
	returnType := streamReturnGoType(si)

	fmt.Fprintf(b, "// %s invokes resolver.%s on each subscription.\n", name, resolverMethod)
	fmt.Fprintf(b, "func %s(ctx context.Context, params *%sStreamParams, identity any, stream *api.Stream, router *api.Router, resolver StreamResolver) error {\n", name, str.Capitalize(si.apiName))
	fmt.Fprintf(b, "\ttypedStream := api.NewTypedStream(stream, func(payload %s, mask []byte, binary bool) ([]byte, error) {\n", returnType)
	writeStreamEncoderBody(b, si, "payload", "identity", "\t\t")
	b.WriteString("\t})\n")
	fmt.Fprintf(b, "\treturn resolver.%s(ctx, params, identity, typedStream)\n", resolverMethod)
	fmt.Fprintf(b, "}\n\n")
}

// generateNativeStreamMatcher generates a matcher that delegates to Go resolver.
func generateNativeStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)
	resolverName := "Match" + str.Capitalize(si.apiName)
	eventType := streamModulePrefix(si.eventModule, si.sourceModule) + si.eventName + "Event"

	fmt.Fprintf(b, "// %s delegates to resolver.%s (Go @native matcher).\n", name, resolverName)
	fmt.Fprintf(b, "func %s(event %s, params *%sStreamParams, identity any, resolver StreamResolver) bool {\n", name, eventType, str.Capitalize(si.apiName))
	fmt.Fprintf(b, "\treturn resolver.%s(event, params, identity)\n", resolverName)
	fmt.Fprintf(b, "}\n\n")
}

// generateLuxoStreamMatcher compiles a @stream body to a Go matcher.
// Body uses implicit `it` for event data, bare idents for subscription params.
// Event values are decoded once by event.OnDecode and captured by the dispatch closure.
func generateLuxoStreamMatcher(b *strings.Builder, si streamInfo, enums map[string]bool) {
	defaultGenerator().generateLuxoStreamMatcher(b, si, enums)
}

func (g *GeneratorContext) generateLuxoStreamMatcher(b *strings.Builder, si streamInfo, enums map[string]bool) {
	name := "match" + str.Capitalize(si.apiName)

	// Extract the boolean expression from body
	expr := extractMatcherExpr(si.body)
	if expr == nil {
		panic(&InternalCompilerError{Detail: "invalid stream matcher body for " + si.apiName})
	}

	// Collect all `it.field` accesses to know what to decode
	itFields := collectItFields(expr)
	usedParams := collectStreamParams(expr, si.params)

	fields := make(map[string]*ast.ParamDecl, len(si.eventParams))
	for _, p := range si.eventParams {
		fields[p.Name] = p
	}

	// Build param name set for resolving bare identifiers
	paramSet := make(map[string]*ast.ParamDecl)
	for _, p := range si.params {
		paramSet[p.Name] = p
	}

	fmt.Fprintf(b, "// %s — compiled from @stream body expression.\n", name)
	fmt.Fprintf(b, "func %s(", name)
	for _, fieldName := range itFields {
		param := fields[fieldName]
		if param == nil {
			panic(&InternalCompilerError{Detail: "unknown stream matcher field " + fieldName + " for " + si.apiName})
		}
		goType := streamFieldGoType(param.Type, enums[param.Type.Name])
		fmt.Fprintf(b, "ev_%s %s, ", fieldName, goType)
	}
	checked := conditionHasCheckedArithmetic(expr)
	resultType := "bool"
	if checked {
		resultType = "(bool, error)"
	}
	fmt.Fprintf(b, "params *%sStreamParams, identity any) %s {\n", str.Capitalize(si.apiName), resultType)
	for _, param := range usedParams {
		fmt.Fprintf(b, "\tparam_%s := %s\n", param.Name, streamEventValue("params."+str.Capitalize(param.Name), param.Type, enums[param.Type.Name]))
	}

	// Compile the boolean expression
	identityExpr := "identity"
	if conditionUsesIdentity(expr) {
		identityExpr = "_identity"
	}
	exprCode, ok := g.compileStreamCondition(expr, paramSet, enums, si.sourceModule, identityExpr)
	if !ok {
		panic(&InternalCompilerError{Detail: "unsupported stream matcher expression for " + si.apiName})
	}
	if checked {
		reader := ""
		if identityExpr == "_identity" {
			reader = "api.NewIdentityReader(identity)"
		}
		exprCode = compileCheckedCondition(expr, func(value ast.Expr) (string, bool) {
			return g.compileStreamCondition(value, paramSet, enums, si.sourceModule, identityExpr)
		}, reader)
		fmt.Fprintf(b, "\treturn %s\n}\n\n", exprCode)
		return
	}
	if identityExpr == "_identity" {
		b.WriteString("\t_identity := api.NewIdentityReader(identity)\n")
		exprCode = "_identity.Allowed(" + exprCode + ")"
	}
	fmt.Fprintf(b, "\treturn %s\n", exprCode)

	fmt.Fprintf(b, "}\n\n")
}

func generateAuthStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)
	fmt.Fprintf(b, "func %s(params *%sStreamParams, identity any) bool {\n", name, str.Capitalize(si.apiName))
	b.WriteString("\t_ = params\n")
	b.WriteString("\treturn true\n")
	b.WriteString("}\n\n")
}

// extractMatcherExpr extracts the boolean expression from a @stream body.
// Body should contain a single ExprStmt with a boolean expression.
func extractMatcherExpr(body *ast.Block) ast.Expr {
	if body == nil || len(body.Stmts) != 1 {
		return nil
	}
	if statement, ok := body.Stmts[0].(*ast.ExprStmt); ok {
		return statement.Expr
	}
	return nil
}

// collectItFields walks the expression and collects all `it.field` member accesses.
func collectItFields(expr ast.Expr) []string {
	var fields []string
	seen := make(map[string]bool)
	walkExpr(expr, func(e ast.Expr) {
		if me, ok := e.(*ast.MemberExpr); ok {
			if ident, ok := me.Object.(*ast.Ident); ok && ident.Name == "it" {
				if !seen[me.Field] {
					seen[me.Field] = true
					fields = append(fields, me.Field)
				}
			}
		}
	})
	return fields
}

func collectStreamParams(expr ast.Expr, params []*ast.ParamDecl) []*ast.ParamDecl {
	byName := make(map[string]*ast.ParamDecl, len(params))
	for _, param := range params {
		byName[param.Name] = param
	}
	seen := make(map[string]bool)
	var result []*ast.ParamDecl
	walkExpr(expr, func(candidate ast.Expr) {
		ident, ok := candidate.(*ast.Ident)
		if !ok || seen[ident.Name] {
			return
		}
		param := byName[ident.Name]
		if param == nil {
			return
		}
		seen[ident.Name] = true
		result = append(result, param)
	})
	return result
}

// walkExpr recursively walks an expression tree calling fn on each node.
func walkExpr(expr ast.Expr, fn func(ast.Expr)) {
	if expr == nil {
		return
	}
	fn(expr)
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		walkExpr(e.Left, fn)
		walkExpr(e.Right, fn)
	case *ast.UnaryExpr:
		walkExpr(e.Value, fn)
	case *ast.MemberExpr:
		walkExpr(e.Object, fn)
	case *ast.CallExpr:
		walkExpr(e.Func, fn)
		for _, arg := range e.Args {
			walkExpr(arg.Value, fn)
		}
	}
}

// compileStreamExpr compiles a boolean expression to Go code.
// Resolves: it.field → ev_field, param ident → params.Type("name"), my → identity.
func compileStreamExpr(expr ast.Expr, params map[string]*ast.ParamDecl, enums map[string]bool, sourceModule string) (string, bool) {
	return defaultGenerator().compileStreamExpr(expr, params, enums, sourceModule)
}

func (g *GeneratorContext) compileStreamExpr(expr ast.Expr, params map[string]*ast.ParamDecl, enums map[string]bool, sourceModule string) (string, bool) {
	return g.compileStreamCondition(expr, params, enums, sourceModule, "identity")
}

func (g *GeneratorContext) compileStreamCondition(expr ast.Expr, params map[string]*ast.ParamDecl, enums map[string]bool, sourceModule, identityExpr string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		left, leftOK := g.compileStreamCondition(e.Left, params, enums, sourceModule, identityExpr)
		right, rightOK := g.compileStreamCondition(e.Right, params, enums, sourceModule, identityExpr)
		if !leftOK || !rightOK || e.Op == "in" || e.Op == "is" {
			return "", false
		}
		left = inferMyFieldType(left, e.Left, e.Right, identityExpr)
		right = inferMyFieldType(right, e.Right, e.Left, identityExpr)
		if expression, ok := compileFloatArithmetic(e, left, right); ok {
			return expression, true
		}
		return fmt.Sprintf("%s %s %s", groupBinaryOperand(e.Left, left), e.Op, groupBinaryOperand(e.Right, right)), true

	case *ast.UnaryExpr:
		val, ok := g.compileStreamCondition(e.Value, params, enums, sourceModule, identityExpr)
		return compileUnaryOperation(e, groupBinaryOperand(e.Value, val)), ok

	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "it" {
			return "ev_" + e.Field, true
		}
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "my" {
			if identityExpr == "_identity" && enums[e.GetTypeTag()] {
				prefix := ""
				if g.events != nil && !g.usesSharedEnums() {
					prefix = streamModulePrefix(g.events.EnumModule[e.GetTypeTag()], sourceModule)
				}
				return compileIdentityEnumCondition(e, prefix+e.GetTypeTag()), true
			}
			return compileMyField(e, identityExpr), true
		}
		if ident, ok := e.Object.(*ast.Ident); ok && enums[ident.Name] {
			prefix := ""
			if g.events != nil && !g.usesSharedEnums() {
				prefix = streamModulePrefix(g.events.EnumModule[ident.Name], sourceModule)
			}
			return fmt.Sprintf("string(%s%s%s)", prefix, ident.Name, e.Field), true
		}
		return "", false

	case *ast.Ident:
		// Check if it's a subscription param
		if _, ok := params[e.Name]; ok {
			return "param_" + e.Name, true
		}
		// my → identity
		if e.Name == "my" {
			return "identity", true
		}
		return e.Name, true

	case *ast.Literal:
		return compileConditionLiteral(e)

	default:
		return "", false
	}
}

func collectStreamEnumModules(body *ast.Block, modules map[string]string, sourceModule string, targets map[string]bool) {
	if len(modules) == 0 {
		return
	}
	ast.WalkExprs(body, func(expr ast.Expr) {
		member, ok := expr.(*ast.MemberExpr)
		if !ok {
			return
		}
		ident, ok := member.Object.(*ast.Ident)
		if !ok {
			return
		}
		name := ident.Name
		if name == "my" {
			name = member.GetTypeTag()
		}
		module := modules[name]
		if module != "" && module != sourceModule {
			targets[module] = true
		}
	})
}

func streamFieldGoType(ref *ast.TypeRef, isEnum bool) string {
	if isEnum {
		return "string"
	}
	switch ref.Name {
	case "Int":
		return "int64"
	case "Float":
		return "float64"
	case "String":
		return "string"
	case "Boolean":
		return "bool"
	case "Duration":
		return "int64"
	case "UUID":
		return "[16]byte"
	default:
		return ""
	}
}

// Removed: inferIdentityType — now uses shared inferMyFieldType from identity.go
