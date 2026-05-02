package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// streamInfo describes a @stream API for codegen.
type streamInfo struct {
	apiName     string
	eventName   string // from @stream(EventName), empty if no event source
	isNative    bool   // @native — Go resolver handles matching
	hasBody     bool   // has lambda matcher body
	params      []*ast.ParamDecl
	body        *ast.Block       // lambda body for matcher
	eventParams []*ast.ParamDecl // event parameters (for decoding)
}

// collectStreams finds all @stream APIs in the semantic result.
func collectStreams(result *semantic.Result) []streamInfo {
	// Build event lookup
	events := make(map[string]*ast.EventDecl)
	for _, file := range result.Files {
		for _, ev := range file.Events {
			events[ev.Name] = ev
		}
	}

	var streams []streamInfo
	for _, file := range result.Files {
		for _, api := range file.APIs {
			if !hasDirective(api.Directives, "stream") {
				continue
			}
			si := streamInfo{
				apiName:  api.Name,
				isNative: hasDirective(api.Directives, "native"),
				hasBody:  api.Body != nil && len(api.Body.Stmts) > 0,
				params:   api.Params,
				body:     api.Body,
			}
			// Extract event name from @stream(EventName)
			for _, d := range api.Directives {
				if d.Name == "stream" && len(d.Args) > 0 {
					if ident, ok := d.Args[0].Value.(*ast.Ident); ok {
						si.eventName = ident.Name
						// Attach event params for decoding
						if ev, ok := events[ident.Name]; ok {
							si.eventParams = ev.Params
						}
					}
				}
			}
			streams = append(streams, si)
		}
	}
	return streams
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
	for _, si := range streams {
		if si.isNative && si.eventName != "" {
			generateNativeStreamMatcher(&b, si)
		} else if si.hasBody && !si.isNative {
			generateLuxoStreamMatcher(&b, si)
		}
	}

	return []byte(b.String())
}

func writeStreamImports(b *strings.Builder, streams []streamInfo) {
	hasEventSource := false
	needsCodec := false
	for _, si := range streams {
		if si.eventName != "" {
			hasEventSource = true
		}
		if si.hasBody && !si.isNative && si.eventName != "" {
			needsCodec = true
		}
	}

	b.WriteString("import (\n")
	if hasEventSource {
		b.WriteString("\t\"context\"\n\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	if needsCodec {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	b.WriteString(")\n\n")
}

func writeRegisterStreams(b *strings.Builder, streams []streamInfo) {
	b.WriteString("// RegisterStreams binds @stream APIs to the event bus.\n")
	b.WriteString("// Events trigger StreamHub.Dispatch which pushes to matching WS subscribers.\n")
	b.WriteString("func RegisterStreams(router *api.Router, bus event.Bus) {\n")

	for _, si := range streams {
		if si.isNative && si.eventName == "" {
			continue
		}

		matcherName := "nil"
		if si.hasBody || si.isNative {
			matcherName = "match" + str.Capitalize(si.apiName)
		}

		if si.eventName != "" {
			fmt.Fprintf(b, "\t// %s @stream(%s)\n", si.apiName, si.eventName)
			fmt.Fprintf(b, "\tbus.On(%q, func(ctx context.Context, payload any) {\n", si.eventName)
			fmt.Fprintf(b, "\t\tif data, ok := payload.([]byte); ok {\n")
			fmt.Fprintf(b, "\t\t\trouter.Streams.DispatchEncoded(%q, data, %s)\n", si.apiName, matcherName)
			fmt.Fprintf(b, "\t\t}\n")
			fmt.Fprintf(b, "\t})\n")
		}

		if matcherName != "nil" {
			fmt.Fprintf(b, "\trouter.HandleStream(%q, %s)\n", si.apiName, matcherName)
		}
	}

	// Register @stream @native without event source
	for _, si := range streams {
		if si.isNative && si.eventName == "" {
			handlerName := "handleStream" + str.Capitalize(si.apiName)
			fmt.Fprintf(b, "\trouter.HandleStreamNative(%q, %s)\n", si.apiName, handlerName)
		}
	}

	b.WriteString("}\n\n")
}

func writeStreamResolverInterface(b *strings.Builder, streams []streamInfo) {
	var nativeNoEvent []streamInfo
	for _, si := range streams {
		if si.isNative && si.eventName == "" {
			nativeNoEvent = append(nativeNoEvent, si)
		}
	}
	if len(nativeNoEvent) == 0 {
		return
	}

	b.WriteString("// StreamResolver is the interface for @stream @native handlers without event source.\n")
	b.WriteString("// The handler pushes data via stream.Send() and returns when context is cancelled.\n")
	b.WriteString("type StreamResolver interface {\n")
	for _, si := range nativeNoEvent {
		fmt.Fprintf(b, "\tHandle%s(ctx context.Context, params *api.StreamParams, identity any, stream *api.Stream)\n", str.Capitalize(si.apiName))
	}
	b.WriteString("}\n\n")

	for _, si := range nativeNoEvent {
		generateNativeStreamHandler(b, si)
	}
}

// generateNativeStreamHandler generates a handler wrapper for @stream @native without event source.
// It delegates to resolver.Handle<ApiName>(ctx, params, identity, stream).
func generateNativeStreamHandler(b *strings.Builder, si streamInfo) {
	name := "handleStream" + str.Capitalize(si.apiName)
	resolverMethod := "Handle" + str.Capitalize(si.apiName)

	fmt.Fprintf(b, "// %s invokes resolver.%s on each subscription.\n", name, resolverMethod)
	fmt.Fprintf(b, "func %s(ctx context.Context, params *api.StreamParams, identity any, stream *api.Stream) {\n", name)
	fmt.Fprintf(b, "\tresolver.%s(ctx, params, identity, stream)\n", resolverMethod)
	fmt.Fprintf(b, "}\n\n")
}

// generateNativeStreamMatcher generates a matcher that delegates to Go resolver.
func generateNativeStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)
	resolverName := "Match" + str.Capitalize(si.apiName)

	fmt.Fprintf(b, "// %s delegates to resolver.%s (Go @native matcher).\n", name, resolverName)
	fmt.Fprintf(b, "func %s(data []byte, params *api.StreamParams, identity any) bool {\n", name)
	fmt.Fprintf(b, "\treturn resolver.%s(data, params, identity)\n", resolverName)
	fmt.Fprintf(b, "}\n\n")
}

// generateLuxoStreamMatcher compiles a @stream body to a Go matcher.
// Body uses implicit `it` for event data, bare idents for subscription params.
// Example: `it.roomId == roomId` compiles to decoder + comparison.
func generateLuxoStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)

	// Extract the boolean expression from body
	expr := extractMatcherExpr(si.body)
	if expr == nil {
		// No valid expression — generate compile error
		fmt.Fprintf(b, "func %s(data []byte, params *api.StreamParams, identity any) bool {\n", name)
		fmt.Fprintf(b, "\t_ = data; _ = params; _ = identity\n")
		fmt.Fprintf(b, "\tSTREAM_MATCHER_EMPTY() // @stream body must contain a boolean expression\n")
		fmt.Fprintf(b, "\treturn false\n")
		fmt.Fprintf(b, "}\n\n")
		return
	}

	// Collect all `it.field` accesses to know what to decode
	itFields := collectItFields(expr)

	// Build field info map from event params
	type fieldInfo struct {
		luxoType string
		fieldID  int
	}
	fields := make(map[string]fieldInfo)
	for _, p := range si.eventParams {
		fid := getEventFieldID(si.eventName, p.Name)
		typeName := ""
		if p.Type != nil {
			typeName = p.Type.Name
		}
		fields[p.Name] = fieldInfo{luxoType: typeName, fieldID: fid}
	}

	// Build param name set for resolving bare identifiers
	paramSet := make(map[string]*ast.ParamDecl)
	for _, p := range si.params {
		paramSet[p.Name] = p
	}

	fmt.Fprintf(b, "// %s — compiled from @stream body expression.\n", name)
	fmt.Fprintf(b, "func %s(data []byte, params *api.StreamParams, identity any) bool {\n", name)

	// Declare variables for decoded event fields
	for _, fieldName := range itFields {
		fi, ok := fields[fieldName]
		if !ok {
			continue
		}
		goType := streamFieldGoType(fi.luxoType)
		fmt.Fprintf(b, "\tvar ev_%s %s\n", fieldName, goType)
	}

	// Generate decoder loop
	fmt.Fprintf(b, "\tdec := codec.NewDecoder(data)\n")
	fmt.Fprintf(b, "\tfor dec.NextField() {\n")
	fmt.Fprintf(b, "\t\tswitch dec.FieldID() {\n")
	for _, fieldName := range itFields {
		fi, ok := fields[fieldName]
		if !ok {
			continue
		}
		readMethod := streamReadMethod(fi.luxoType)
		fmt.Fprintf(b, "\t\tcase %d:\n", fi.fieldID)
		fmt.Fprintf(b, "\t\t\tev_%s = dec.%s()\n", fieldName, readMethod)
	}
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t}\n")

	// Compile the boolean expression
	exprCode := compileStreamExpr(expr, paramSet)
	fmt.Fprintf(b, "\treturn %s\n", exprCode)

	fmt.Fprintf(b, "}\n\n")
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
func compileStreamExpr(expr ast.Expr, params map[string]*ast.ParamDecl) string {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		left := compileStreamExpr(e.Left, params)
		right := compileStreamExpr(e.Right, params)
		return fmt.Sprintf("%s %s %s", left, e.Op, right)

	case *ast.UnaryExpr:
		val := compileStreamExpr(e.Value, params)
		return fmt.Sprintf("%s%s", e.Op, val)

	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "it" {
			return "ev_" + e.Field
		}
		// my.id → api.IdentityID(identity), my.xxx → api.IdentityInt(identity, "xxx")
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "my" {
			if e.Field == "id" {
				return "api.IdentityID(identity)"
			}
			return fmt.Sprintf("api.IdentityInt(identity, %q)", e.Field)
		}
		obj := compileStreamExpr(e.Object, params)
		return fmt.Sprintf("%s.%s", obj, str.Capitalize(e.Field))

	case *ast.Ident:
		// Check if it's a subscription param
		if p, ok := params[e.Name]; ok {
			paramType := "Get"
			if p.Type != nil {
				paramType = streamParamMethod(p.Type.Name)
			}
			return fmt.Sprintf("params.%s(%q)", paramType, e.Name)
		}
		// my → identity
		if e.Name == "my" {
			return "identity"
		}
		return e.Name

	case *ast.Literal:
		switch e.Kind {
		case token.String:
			return fmt.Sprintf("%q", e.Value)
		default:
			return e.Value
		}

	default:
		return "false /* unsupported expr */"
	}
}

// streamFieldGoType maps Luxo type to Go type for decoded event fields.
func streamFieldGoType(luxoType string) string {
	switch luxoType {
	case "Int":
		return "int64"
	case "Float":
		return "float64"
	case "String":
		return "string"
	case "Boolean":
		return "bool"
	default:
		return "int64"
	}
}

// streamReadMethod maps Luxo type to codec.Decoder method name.
func streamReadMethod(luxoType string) string {
	switch luxoType {
	case "Int":
		return "ReadInt"
	case "Float":
		return "ReadFloat"
	case "String":
		return "ReadString"
	case "Boolean":
		return "ReadBool"
	default:
		return "ReadInt"
	}
}

// streamParamMethod maps Luxo type to StreamParams accessor method.
func streamParamMethod(luxoType string) string {
	switch luxoType {
	case "Int":
		return "Int"
	case "String":
		return "String"
	default:
		return "Get"
	}
}
