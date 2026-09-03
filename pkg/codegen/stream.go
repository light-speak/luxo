package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// streamInfo describes a @stream API for codegen.
type streamInfo struct {
	apiName       string
	eventName     string // from @stream(EventName), empty if no event source
	isNative      bool   // @native — Go resolver handles matching
	hasBody       bool   // has lambda matcher body
	params        []*ast.ParamDecl
	body          *ast.Block       // lambda body for matcher
	eventParams   []*ast.ParamDecl // event parameters (for decoding)
	eventDecl     *ast.EventDecl
	returnType    *ast.TypeRef
	payload       *ast.ParamDecl
	payloadKind   streamPayloadKind
	payloadEnum   bool
	eventModule   string
	payloadModule string
	sourceModule  string
	enums         map[string]bool
	requiresAuth  bool
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
}

// collectStreams finds all @stream APIs in the semantic result.
func collectStreams(result *semantic.Result) []streamInfo {
	declarations := collectStreamTypes(result)
	var streams []streamInfo
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if !hasDirective(api.Directives, "stream") {
				continue
			}
			si := newStreamInfo(file, api, declarations)
			if si.payload == nil {
				classifyStreamPayload(&si, api.ReturnType, declarations.models, declarations.types, declarations.enums)
			}
			streams = append(streams, si)
		}
	}
	return streams
}

func collectStreamTypes(result *semantic.Result) streamTypes {
	declarations := streamTypes{
		events: make(map[string]*ast.EventDecl),
		models: make(map[string]bool),
		types:  make(map[string]bool),
		enums:  make(map[string]bool),
	}
	for _, file := range result.Files {
		for _, eventDecl := range file.Events {
			declarations.events[eventDecl.Name] = eventDecl
		}
		for _, model := range file.Models {
			declarations.models[model.Name] = true
		}
		for _, typeDecl := range file.Types {
			declarations.types[typeDecl.Name] = true
		}
		for _, enum := range file.Enums {
			declarations.enums[enum.Name] = true
		}
	}
	mergeGlobalStreamTypes(&declarations)
	return declarations
}

func mergeGlobalStreamTypes(declarations *streamTypes) {
	if globalEventCtx == nil {
		return
	}
	for name, eventDecl := range globalEventCtx.Events {
		if declarations.events[name] == nil {
			declarations.events[name] = eventDecl
		}
	}
	for name := range globalEventCtx.ModelModule {
		declarations.models[name] = true
	}
	for name := range globalEventCtx.TypeModule {
		declarations.types[name] = true
	}
	for name := range globalEventCtx.EnumModule {
		declarations.enums[name] = true
	}
}

func newStreamInfo(file *ast.File, api *ast.ApiDecl, declarations streamTypes) streamInfo {
	si := streamInfo{
		apiName:      api.Name,
		isNative:     hasDirective(api.Directives, "native"),
		hasBody:      api.Body != nil && len(api.Body.Stmts) > 0,
		params:       api.Params,
		body:         api.Body,
		returnType:   api.ReturnType,
		sourceModule: moduleNameFromFile(file.Name),
		enums:        declarations.enums,
		requiresAuth: hasDirective(api.Directives, "auth") || hasDirective(api.Directives, "role"),
	}
	si.eventName = streamEventName(api)
	if si.eventName == "" {
		return si
	}
	if globalEventCtx != nil {
		si.eventModule = globalEventCtx.EventModule[si.eventName]
	}
	attachStreamEvent(&si, declarations.events[si.eventName], declarations)
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

func attachStreamEvent(si *streamInfo, eventDecl *ast.EventDecl, declarations streamTypes) {
	if eventDecl == nil {
		return
	}
	si.eventDecl = eventDecl
	si.eventParams = eventDecl.Params
	for _, param := range eventDecl.Params {
		if sameStreamType(param.Type, si.returnType) {
			si.payload = param
			classifyStreamPayload(si, param.Type, declarations.models, declarations.types, declarations.enums)
			return
		}
	}
}

func classifyStreamPayload(si *streamInfo, ref *ast.TypeRef, models, types, enums map[string]bool) {
	if ref == nil {
		return
	}
	if models[ref.Name] {
		si.payloadKind = streamPayloadModel
	} else if types[ref.Name] {
		si.payloadKind = streamPayloadType
	}
	si.payloadEnum = enums[ref.Name]
	if globalEventCtx == nil {
		return
	}
	switch si.payloadKind {
	case streamPayloadModel:
		si.payloadModule = globalEventCtx.ModelModule[ref.Name]
	case streamPayloadType:
		si.payloadModule = globalEventCtx.TypeModule[ref.Name]
	}
}

func sameStreamType(left, right *ast.TypeRef) bool {
	if left == nil || right == nil || left.Name != right.Name || left.IsList != right.IsList || left.Nullable != right.Nullable || len(left.TypeArgs) != len(right.TypeArgs) || len(left.Tuple) != len(right.Tuple) {
		return false
	}
	for i := range left.TypeArgs {
		if !sameStreamType(left.TypeArgs[i], right.TypeArgs[i]) {
			return false
		}
	}
	for i := range left.Tuple {
		if !sameStreamType(left.Tuple[i], right.Tuple[i]) {
			return false
		}
	}
	return true
}

// generateStreamFile produces stream.gen.go containing:
// - RegisterStreams() — binds @stream APIs to event bus
// - Matcher functions for @stream APIs with lambda bodies
// - @native matcher stubs (delegating to resolver)
func generateStreamFile(result *semantic.Result, packageName string) []byte {
	streams := collectStreams(result)
	if len(streams) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "stream.gen.go")
	writeStreamImports(&b, streams)
	writeRegisterStreams(&b, streams)
	writeStreamResolverInterface(&b, streams)

	// Generate matcher functions
	enums := CollectEnumsFromResult(result)
	for _, si := range streams {
		if si.isNative && si.eventName != "" {
			generateNativeStreamMatcher(&b, si)
		} else if si.hasBody && !si.isNative {
			generateLuxoStreamMatcher(&b, si, enums)
		} else if si.requiresAuth {
			generateAuthStreamMatcher(&b, si)
		}
	}

	return []byte(b.String())
}

func writeStreamImports(b *strings.Builder, streams []streamInfo) {
	needsContext := false
	needsCodec := false
	remoteModules := make(map[string]bool)
	for _, si := range streams {
		if si.eventName != "" || si.isNative {
			needsContext = true
		}
		if si.payloadKind == streamPayloadScalar {
			needsCodec = true
		}
		if si.eventModule != "" && si.eventModule != si.sourceModule {
			remoteModules[si.eventModule] = true
		}
		if si.payloadKind != streamPayloadScalar && si.payloadModule != "" && si.payloadModule != si.sourceModule {
			remoteModules[si.payloadModule] = true
		}
		if globalEventCtx != nil && si.body != nil {
			for _, enumName := range collectStreamEnumRefs(si.body, globalEventCtx.EnumModule) {
				module := globalEventCtx.EnumModule[enumName]
				if module != "" && module != si.sourceModule {
					remoteModules[module] = true
				}
			}
		}
	}

	b.WriteString("import (\n")
	if needsContext {
		b.WriteString("\t\"context\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	if needsCodec {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	modules := make([]string, 0, len(remoteModules))
	for module := range remoteModules {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		fmt.Fprintf(b, "\t%s_luxo %q\n", module, globalEventCtx.ModulePath+"/"+module+"/luxo")
	}
	b.WriteString(")\n\n")
}

func writeRegisterStreams(b *strings.Builder, streams []streamInfo) {
	b.WriteString("// RegisterStreams binds @stream APIs to the event bus.\n")
	b.WriteString("// Events trigger StreamHub.Dispatch which pushes to matching WS subscribers.\n")
	b.WriteString("func RegisterStreams(router *api.Router, bus event.Bus, resolver StreamResolver) {\n")
	for _, si := range streams {
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

	b.WriteString("\tif resolver == nil { return }\n")
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
			handlerName := "handleStream" + str.Capitalize(si.apiName)
			fmt.Fprintf(b, "\trouter.HandleStreamNative(%q, func(ctx context.Context, params *api.StreamParams, identity any, stream *api.Stream) { %s(ctx, params, identity, stream, router, resolver) })\n", si.apiName, handlerName)
		}
	}

	b.WriteString("}\n\n")
}

func writeTypedStreamRegistration(b *strings.Builder, si streamInfo, matcherName string) {
	eventPrefix := streamModulePrefix(si.eventModule, si.sourceModule)
	eventType := eventPrefix + si.eventName + "Event"
	unmarshal := eventPrefix + "Unmarshal" + si.eventName
	payloadField := "payload." + str.Capitalize(si.payload.Name)
	fmt.Fprintf(b, "\trouter.HandleStream(%q, nil)\n", si.apiName)
	fmt.Fprintf(b, "\tevent.OnDecode[%s](bus, %q, %s, func(ctx context.Context, payload %s) error {\n", eventType, si.eventName, unmarshal, eventType)
	if si.isNative {
		fmt.Fprintf(b, "\t\trouter.Streams.DispatchPreparedEvent(%q, func(params *api.StreamParams, identity any) bool { return %s(payload, params, identity, resolver) }, func(mask []byte, binary bool) []byte {\n", si.apiName, matcherName)
	} else {
		fmt.Fprintf(b, "\t\trouter.Streams.DispatchPreparedEvent(%q, %s, func(mask []byte, binary bool) []byte {\n", si.apiName, preparedStreamMatcher(si, matcherName))
	}
	writeStreamEncoderBody(b, si, payloadField, "\t\t\t")
	b.WriteString("\t\t})\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t})\n")
}

func writeStreamEncoderBody(b *strings.Builder, si streamInfo, value, indent string) {
	fmt.Fprintf(b, "%sbuf := api.GetBuf()\n", indent)
	writeStreamPayloadEncoding(b, si, value, indent)
	fmt.Fprintf(b, "%sdata := append([]byte(nil), buf.B...)\n", indent)
	fmt.Fprintf(b, "%sapi.PutBuf(buf)\n", indent)
	fmt.Fprintf(b, "%sif !binary {\n", indent)
	fmt.Fprintf(b, "%s\treturn router.StreamPayloadJSON(%q, data)\n", indent, si.apiName)
	fmt.Fprintf(b, "%s}\n", indent)
	fmt.Fprintf(b, "%sreturn data\n", indent)
}

func preparedStreamMatcher(si streamInfo, matcherName string) string {
	if matcherName == "nil" {
		return "nil"
	}
	if !si.hasBody {
		return matcherName
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
	return "func(params *api.StreamParams, identity any) bool { return " + matcherName + "(" + strings.Join(args, ", ") + ") }"
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
	if ref.IsList {
		if appendExpr, ok := binaryScalarAppend(ref.Name, "buf.B", "item", si.payloadEnum); ok {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendArrayHeader(buf.B, len(%s))\n", indent, value)
			fmt.Fprintf(b, "%sfor _, item := range %s {\n", indent, value)
			fmt.Fprintf(b, "%s\tbuf.B = %s\n", indent, appendExpr)
			fmt.Fprintf(b, "%s}\n", indent)
			return
		}
		if si.payloadKind == streamPayloadModel {
			fmt.Fprintf(b, "%s%sWriteColumnar%sValues(buf, %s, mask)\n", indent, streamModulePrefix(si.payloadModule, si.sourceModule), ref.Name, value)
			return
		}
		fmt.Fprintf(b, "%s%sWriteColumnar%s(buf, %s, mask)\n", indent, streamModulePrefix(si.payloadModule, si.sourceModule), ref.Name, value)
		return
	}
	if appendExpr, ok := binaryScalarAppend(ref.Name, "buf.B", value, si.payloadEnum); ok {
		fmt.Fprintf(b, "%sbuf.B = %s\n", indent, appendExpr)
		return
	}
	fmt.Fprintf(b, "%s%s.WriteLuxo(buf, mask)\n", indent, value)
}

func writeStreamResolverInterface(b *strings.Builder, streams []streamInfo) {
	b.WriteString("// StreamResolver provides typed @stream @native implementations.\n")
	b.WriteString("type StreamResolver interface {\n")
	for _, si := range streams {
		if !si.isNative {
			continue
		}
		if si.eventName != "" {
			eventType := streamModulePrefix(si.eventModule, si.sourceModule) + si.eventName + "Event"
			fmt.Fprintf(b, "\tMatch%s(event %s, params *api.StreamParams, identity any) bool\n", str.Capitalize(si.apiName), eventType)
			continue
		}
		fmt.Fprintf(b, "\tHandle%s(ctx context.Context, params *api.StreamParams, identity any, stream *api.TypedStream[%s])\n", str.Capitalize(si.apiName), streamReturnGoType(si))
	}
	b.WriteString("}\n\n")

	for _, si := range streams {
		if si.isNative && si.eventName == "" {
			generateNativeStreamHandler(b, si)
		}
	}
}

func streamReturnGoType(si streamInfo) string {
	if si.returnType == nil {
		return "struct{}"
	}
	base := resolveGoType(&ast.TypeRef{Name: si.returnType.Name, Nullable: si.returnType.Nullable})
	if si.payloadModule != "" && si.payloadModule != si.sourceModule && si.payloadKind != streamPayloadScalar {
		base = streamModulePrefix(si.payloadModule, si.sourceModule) + base
	}
	if si.returnType.IsList {
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
	fmt.Fprintf(b, "func %s(ctx context.Context, params *api.StreamParams, identity any, stream *api.Stream, router *api.Router, resolver StreamResolver) {\n", name)
	writeStreamAuthGuard(b, si, "\t", "return")
	fmt.Fprintf(b, "\ttypedStream := api.NewTypedStream(stream, func(payload %s, mask []byte, binary bool) []byte {\n", returnType)
	writeStreamEncoderBody(b, si, "payload", "\t\t")
	b.WriteString("\t})\n")
	fmt.Fprintf(b, "\tresolver.%s(ctx, params, identity, typedStream)\n", resolverMethod)
	fmt.Fprintf(b, "}\n\n")
}

// generateNativeStreamMatcher generates a matcher that delegates to Go resolver.
func generateNativeStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)
	resolverName := "Match" + str.Capitalize(si.apiName)
	eventType := streamModulePrefix(si.eventModule, si.sourceModule) + si.eventName + "Event"

	fmt.Fprintf(b, "// %s delegates to resolver.%s (Go @native matcher).\n", name, resolverName)
	fmt.Fprintf(b, "func %s(event %s, params *api.StreamParams, identity any, resolver StreamResolver) bool {\n", name, eventType)
	writeStreamAuthGuard(b, si, "\t", "return false")
	fmt.Fprintf(b, "\treturn resolver.%s(event, params, identity)\n", resolverName)
	fmt.Fprintf(b, "}\n\n")
}

// generateLuxoStreamMatcher compiles a @stream body to a Go matcher.
// Body uses implicit `it` for event data, bare idents for subscription params.
// Event values are decoded once by event.OnDecode and captured by the dispatch closure.
func generateLuxoStreamMatcher(b *strings.Builder, si streamInfo, enums map[string]bool) {
	name := "match" + str.Capitalize(si.apiName)

	// Extract the boolean expression from body
	expr := extractMatcherExpr(si.body)
	if expr == nil {
		// No valid expression — generate compile error
		fmt.Fprintf(b, "func %s(params *api.StreamParams, identity any) bool {\n", name)
		fmt.Fprintf(b, "\t_ = params; _ = identity\n")
		fmt.Fprintf(b, "\tSTREAM_MATCHER_EMPTY() // @stream body must contain a boolean expression\n")
		fmt.Fprintf(b, "\treturn false\n")
		fmt.Fprintf(b, "}\n\n")
		return
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
			continue
		}
		goType := streamFieldGoType(param.Type, enums[param.Type.Name])
		fmt.Fprintf(b, "ev_%s %s, ", fieldName, goType)
	}
	b.WriteString("params *api.StreamParams, identity any) bool {\n")
	writeStreamAuthGuard(b, si, "\t", "return false")
	for _, param := range usedParams {
		method := streamParamMethod(param.Type.Name, enums[param.Type.Name])
		fmt.Fprintf(b, "\tparam_%s, ok_%s := params.Lookup%s(%q)\n", param.Name, param.Name, method, param.Name)
		fmt.Fprintf(b, "\tif !ok_%s { return false }\n", param.Name)
	}

	// Compile the boolean expression
	exprCode, ok := compileStreamExpr(expr, paramSet, enums, si.sourceModule)
	if !ok {
		exprCode = "false /* rejected by semantic analysis */"
	}
	fmt.Fprintf(b, "\treturn %s\n", exprCode)

	fmt.Fprintf(b, "}\n\n")
}

func generateAuthStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)
	fmt.Fprintf(b, "func %s(params *api.StreamParams, identity any) bool {\n", name)
	b.WriteString("\t_ = params\n")
	writeStreamAuthGuard(b, si, "\t", "return false")
	b.WriteString("\treturn true\n")
	b.WriteString("}\n\n")
}

func writeStreamAuthGuard(b *strings.Builder, si streamInfo, indent, rejection string) {
	if si.requiresAuth {
		fmt.Fprintf(b, "%sif api.IdentityID(identity) == 0 { %s }\n", indent, rejection)
	}
}

// extractMatcherExpr extracts the boolean expression from a @stream body.
// Body should contain a single ExprStmt with a boolean expression.
func extractMatcherExpr(body *ast.Block) ast.Expr {
	if body == nil || len(body.Stmts) == 0 {
		return nil
	}
	// Take the last non-nil ExprStmt as the matcher expression
	for i := len(body.Stmts) - 1; i >= 0; i-- {
		if es, ok := body.Stmts[i].(*ast.ExprStmt); ok && es.Expr != nil {
			return es.Expr
		}
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
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		left, leftOK := compileStreamExpr(e.Left, params, enums, sourceModule)
		right, rightOK := compileStreamExpr(e.Right, params, enums, sourceModule)
		if !leftOK || !rightOK || e.Op == "in" || e.Op == "is" {
			return "", false
		}
		left = inferMyFieldType(left, e.Left, e.Right, "identity")
		right = inferMyFieldType(right, e.Right, e.Left, "identity")
		return fmt.Sprintf("%s %s %s", left, e.Op, right), true

	case *ast.UnaryExpr:
		val, ok := compileStreamExpr(e.Value, params, enums, sourceModule)
		return fmt.Sprintf("%s%s", e.Op, val), ok

	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "it" {
			return "ev_" + e.Field, true
		}
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "my" {
			return compileMyField(e.Field, "identity"), true
		}
		if ident, ok := e.Object.(*ast.Ident); ok && enums[ident.Name] {
			prefix := ""
			if globalEventCtx != nil {
				prefix = streamModulePrefix(globalEventCtx.EnumModule[ident.Name], sourceModule)
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
		switch e.Kind {
		case token.String:
			return fmt.Sprintf("%q", e.Value), true
		default:
			return e.Value, true
		}

	default:
		return "", false
	}
}

func collectStreamEnumRefs(body *ast.Block, modules map[string]string) []string {
	seen := make(map[string]bool)
	var names []string
	ast.WalkExprs(body, func(expr ast.Expr) {
		member, ok := expr.(*ast.MemberExpr)
		if !ok {
			return
		}
		ident, ok := member.Object.(*ast.Ident)
		if !ok || modules[ident.Name] == "" || seen[ident.Name] {
			return
		}
		seen[ident.Name] = true
		names = append(names, ident.Name)
	})
	return names
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

// streamParamMethod maps Luxo type to StreamParams accessor method.
func streamParamMethod(luxoType string, isEnum bool) string {
	if isEnum {
		return "String"
	}
	switch luxoType {
	case "Int":
		return "Int"
	case "Float":
		return "Float"
	case "String":
		return "String"
	case "Boolean":
		return "Boolean"
	case "Duration":
		return "Duration"
	case "UUID":
		return "UUID"
	default:
		return ""
	}
}

// Removed: inferIdentityType — now uses shared inferMyFieldType from identity.go
