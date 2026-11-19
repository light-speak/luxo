package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func nativeEventNames(listeners []*ast.OnDecl) []string {
	var names []string
	seen := make(map[string]bool)
	for _, listener := range listeners {
		if listener.Native && !seen[listener.EventName] {
			seen[listener.EventName] = true
			names = append(names, listener.EventName)
		}
	}
	return names
}

func resultHasNativeEvents(result *semantic.Result) bool {
	for _, file := range result.Files {
		for _, listener := range file.Listeners {
			if listener.Native {
				return true
			}
		}
	}
	return false
}

func (g *GeneratorContext) eventTypePrefix(name, module string) string {
	if g.events != nil {
		if owner := g.events.EventModule[name]; owner != "" && owner != module {
			return owner + "_luxo."
		}
	}
	return ""
}

func (g *GeneratorContext) writeNativeEventHandlers(b *strings.Builder, names []string, module string) {
	if len(names) == 0 {
		return
	}
	b.WriteString("// NativeEventHandlers supplies required typed event implementations.\n")
	b.WriteString("type NativeEventHandlers struct {\n")
	for _, name := range names {
		fmt.Fprintf(b, "\tOn%s func(context.Context, %s%sEvent) error\n", name, g.eventTypePrefix(name, module), name)
	}
	b.WriteString("}\n\n")
}

func writeNativeEventChecks(b *strings.Builder, names []string) {
	if len(names) == 0 {
		return
	}
	b.WriteString("\tif app == nil { return fmt.Errorf(\"native event handlers require an app\") }\n")
	for _, name := range names {
		fmt.Fprintf(b, "\tnativeOn%s := app.EventHandlers.On%s\n", name, name)
		fmt.Fprintf(b, "\tif nativeOn%s == nil { return fmt.Errorf(%q) }\n", name, "native event handler On"+name+" is not configured")
	}
}

func writeNativeEventRegistration(b *strings.Builder, listener *ast.OnDecl, module, eventType string) {
	if listener.Broadcast {
		fmt.Fprintf(b, "\tif err := event.OnValueDecode(bus, %q, ", listener.EventName)
	} else {
		fmt.Fprintf(b, "\tif err := event.OnQueueValueDecode(bus, %q, %q, ", listener.EventName, module)
	}
	writeEventValueDecoder(b, eventType)
	fmt.Fprintf(b, ", nativeOn%s); err != nil { return err }\n", listener.EventName)
}
