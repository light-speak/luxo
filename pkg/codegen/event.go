package codegen

import (
	"fmt"
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

	if len(events) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "event.gen.go")

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if len(listeners) > 0 {
		b.WriteString("\n\t\"github.com/bytedance/sonic\"\n")
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	b.WriteString(")\n\n")

	// Event structs
	for _, e := range events {
		generateEventStruct(&b, e)
	}

	// Emit functions — pass struct directly, zero serialization
	for _, e := range events {
		generateEmitFunc(&b, e)
	}

	// RegisterEvents function — wires all on-listeners
	generateRegisterEvents(&b, listeners, packageName)

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
// Default uses OnQueue with moduleName as the queue group (competing consumers).
// Listeners with @broadcast use On (every instance receives).
// Handlers use type switch: struct for ChanBus, []byte for NATSBus.
func generateRegisterEvents(b *strings.Builder, listeners []*ast.OnDecl, moduleName string) {
	b.WriteString("// RegisterEvents registers all event listeners with the bus.\n")
	b.WriteString("func RegisterEvents(bus event.Bus) {\n")

	for _, l := range listeners {
		paramName := "payload"
		if len(l.Params) > 0 {
			paramName = l.Params[0]
		}
		if l.Broadcast {
			fmt.Fprintf(b, "\tbus.On(%q, func(ctx context.Context, payload any) {\n", l.EventName)
		} else {
			fmt.Fprintf(b, "\tbus.OnQueue(%q, %q, func(ctx context.Context, payload any) {\n", l.EventName, moduleName)
		}
		fmt.Fprintf(b, "\t\tvar %s %sEvent\n", paramName, l.EventName)
		fmt.Fprintf(b, "\t\tswitch v := payload.(type) {\n")
		fmt.Fprintf(b, "\t\tcase %sEvent:\n", l.EventName)
		fmt.Fprintf(b, "\t\t\t%s = v\n", paramName)
		fmt.Fprintf(b, "\t\tcase []byte:\n")
		fmt.Fprintf(b, "\t\t\tsonic.Unmarshal(v, &%s)\n", paramName)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\t_ = %s\n", paramName)
		b.WriteString("\t})\n")
	}

	b.WriteString("}\n")
}
