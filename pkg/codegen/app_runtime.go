package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func modulesNeedDatabase(modules []moduleInfo) bool {
	for _, module := range modules {
		if module.hasDatabase {
			return true
		}
	}
	return false
}

func writeStatelessModuleApp(b *strings.Builder, name string) {
	fmt.Fprintf(b, "\t%sApp, err := %s_luxo.New(ctx)\n", name, name)
	b.WriteString("\tif err != nil {\n")
	fmt.Fprintf(b, "\t\tfmt.Fprintf(os.Stderr, \"fatal: %s: %%v\\n\", err)\n", name)
	b.WriteString("\t\tos.Exit(1)\n\t}\n")
}

func fileAppUsesDatabase(file *ast.File) bool {
	if len(file.Models)+len(file.Extends) > 0 ||
		apisHaveDirective(file.APIs, "native") || functionsHaveDirective(file.Functions, "native") {
		return true
	}
	// Pure event declarations/listeners need a bus, not a database. Native
	// listeners remain opaque and may access the existing database binding.
	for _, listener := range file.Listeners {
		if listener.Native || bodyContainsTransaction(listener.Body) {
			return true
		}
	}
	for _, api := range file.APIs {
		if bodyContainsTransaction(api.Body) {
			return true
		}
	}
	for _, fn := range file.Functions {
		if bodyContainsTransaction(fn.Body) {
			return true
		}
	}
	for _, middleware := range file.Middlewares {
		if bodyContainsTransaction(middleware.Body) {
			return true
		}
	}
	return false
}

func appUsesDatabase(result *semantic.Result) bool {
	for _, file := range result.Files {
		if fileAppUsesDatabase(file) {
			return true
		}
	}
	return false
}

func generateStatelessAppFile(packageName string, events bool) []byte {
	var b strings.Builder
	writeHeader(&b, packageName, "app.gen.go")
	b.WriteString("import (\n\t\"context\"\n\t\"github.com/light-speak/luxo/pkg/lux/queue\"\n")
	if events {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	}
	b.WriteString(")\n\n// App holds runtime bindings without a database connection.\ntype App struct {\n\tQueue queue.Queue\n")
	if events {
		b.WriteString("\tEventBus event.Bus\n")
	}
	b.WriteString("}\n\n// New constructs an App without database configuration or I/O.\n")
	b.WriteString("func New(ctx context.Context) (*App, error) { return &App{}, nil }\n\n")
	b.WriteString("// Close has no owned resources; injected components remain caller-owned.\nfunc (a *App) Close() {}\n")
	return []byte(b.String())
}
