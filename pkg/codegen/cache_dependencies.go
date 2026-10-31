package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/semantic"
)

// Preserve declaration order and the compiler's model/external distinction.
// Missing analysis stays nil; an explicitly checked empty set gets a value.
func writeExecutionCacheRegistration(b *strings.Builder, result *semantic.Result) {
	for _, file := range result.Files {
		for _, declaration := range file.APIs {
			dependencies, checked := result.ExecutionCacheDependencies[declaration]
			if !checked {
				continue
			}
			fmt.Fprintf(b, "\ts.APIs[%q].CacheDependencies = &schema.CacheDependencies{\n", declaration.Name)
			writeCacheDependencyValues(b, dependencies)
			b.WriteString("\t}\n")
			if policy, ok := result.CachePolicies[findDirective(declaration.Directives, "cache")]; ok {
				fmt.Fprintf(b, "\ts.APIs[%q].CachePolicy = &schema.CachePolicy{Mode: %q, TTL: %d, Scope: %q}\n", declaration.Name, policy.Mode, policy.TTL, policy.Scope)
			}
		}
	}
}

func writeCacheDependencyValues(b *strings.Builder, dependencies []semantic.CacheDependency) {
	if len(dependencies) == 0 {
		return
	}
	b.WriteString("\t\tModels: []schema.ComputedSource{\n")
	for _, source := range dependencies {
		if source.Kind == semantic.CacheModelDependency {
			fmt.Fprintf(b, "\t\t\t{Model: %q, Module: %q},\n", source.Name, source.Module)
		}
	}
	b.WriteString("\t\t},\n\t\tExternal: []string{\n")
	for _, source := range dependencies {
		if source.Kind == semantic.CacheExternalDependency {
			fmt.Fprintf(b, "\t\t\t%q,\n", source.Name)
		}
	}
	b.WriteString("\t\t},\n")
}
