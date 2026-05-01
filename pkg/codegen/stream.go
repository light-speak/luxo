package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// streamInfo describes a @stream API for codegen.
type streamInfo struct {
	apiName   string
	eventName string // from @stream(EventName), empty if no event source
	isNative  bool   // @native — Go resolver handles matching
	hasBody   bool   // has lambda matcher body
	params    []*ast.ParamDecl
	body      *ast.Block // lambda body for matcher
}

// collectStreams finds all @stream APIs in the semantic result.
func collectStreams(result *semantic.Result) []streamInfo {
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
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	b.WriteString(")\n\n")

	// RegisterStreams — called at startup, binds events to StreamHub
	b.WriteString("// RegisterStreams binds @stream APIs to the event bus.\n")
	b.WriteString("// Events trigger StreamHub.Dispatch which pushes to matching WS subscribers.\n")
	b.WriteString("func RegisterStreams(router *api.Router, bus event.Bus) {\n")

	for _, si := range streams {
		if si.isNative && si.eventName == "" {
			// @stream @native without event source — Go handles everything
			// User registers via resolver, nothing to generate
			continue
		}

		matcherName := "nil"
		if si.hasBody || si.isNative {
			matcherName = "match" + str.Capitalize(si.apiName)
		}

		if si.eventName != "" {
			// Bind event → StreamHub dispatch
			fmt.Fprintf(&b, "\t// %s @stream(%s)\n", si.apiName, si.eventName)
			fmt.Fprintf(&b, "\tbus.On(%q, func(ctx context.Context, payload any) {\n", si.eventName)
			fmt.Fprintf(&b, "\t\tif data, ok := payload.([]byte); ok {\n")
			fmt.Fprintf(&b, "\t\t\trouter.Streams.DispatchEncoded(%q, data, %s)\n", si.apiName, matcherName)
			fmt.Fprintf(&b, "\t\t}\n")
			fmt.Fprintf(&b, "\t})\n")
		}

		// Register matcher on router
		if matcherName != "nil" {
			fmt.Fprintf(&b, "\trouter.HandleStream(%q, %s)\n", si.apiName, matcherName)
		}
	}

	b.WriteString("}\n\n")

	// Generate matcher functions
	for _, si := range streams {
		if si.isNative {
			generateNativeStreamMatcher(&b, si)
		} else if si.hasBody {
			generateLuxoStreamMatcher(&b, si)
		}
	}

	return []byte(b.String())
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

// generateLuxoStreamMatcher compiles a @stream body lambda to a Go matcher.
// Example: `danmaku -> danmaku.roomId == roomId`
// Compiles to: func matchWatchDanmaku(data []byte, params *StreamParams, identity any) bool
func generateLuxoStreamMatcher(b *strings.Builder, si streamInfo) {
	name := "match" + str.Capitalize(si.apiName)

	fmt.Fprintf(b, "// %s — compiled from @stream body lambda.\n", name)
	fmt.Fprintf(b, "func %s(data []byte, params *api.StreamParams, identity any) bool {\n", name)

	// The lambda body needs to be compiled.
	// For now, generate a placeholder that always returns true.
	// Full lambda compilation requires the expression compiler with stream context.
	// TODO: compile lambda body (data -> bool expression)
	fmt.Fprintf(b, "\t// TODO: compile @stream lambda body\n")
	fmt.Fprintf(b, "\t_ = data\n")
	fmt.Fprintf(b, "\t_ = params\n")
	fmt.Fprintf(b, "\t_ = identity\n")
	fmt.Fprintf(b, "\treturn true\n")

	fmt.Fprintf(b, "}\n\n")
}
