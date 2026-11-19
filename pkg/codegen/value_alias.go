package codegen

import (
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type compiledValuePlan struct {
	methods     map[string]map[string]string
	columnar    map[string]string
	aliases     map[string]string
	events      map[string]bool
	publicTypes map[string]string
}

var valueDepthMethods = [...]string{"writeLuxoDepth", "readLuxoDepth", "readLuxoInputDepth", "readJSONInputDepth", "checkJSONInputDepth", "marshalEventSnapshot", "unmarshalEventSnapshot"}

func newCompiledValuePlan(result *semantic.Result, events *EventContext, catalog map[string]semantic.CompiledValueDeclaration) *compiledValuePlan {
	plan := &compiledValuePlan{methods: make(map[string]map[string]string), columnar: make(map[string]string), publicTypes: make(map[string]string)}
	current := ""
	if len(result.Files) != 0 && events != nil && events.ModulePath != "" {
		current = moduleNameFromFile(result.Files[0].Name)
		plan.aliases = make(map[string]string)
	}
	for name, entry := range catalog {
		plan.addCompiledNames(result, name, moduleNameFromFile(entry.File), current)
		occupied := make(map[string]bool, len(entry.Declaration.Fields))
		for _, field := range entry.Declaration.Fields {
			occupied[str.Capitalize(field.Name)] = true
		}
		methods := make(map[string]string, len(valueDepthMethods))
		for _, private := range valueDepthMethods {
			public := str.Capitalize(private)
			for occupied[public] {
				public += "_"
			}
			methods[private], occupied[public] = public, true
		}
		plan.methods[name] = methods
	}
	for name, entry := range result.CompiledSealedDeclarations() {
		plan.addCompiledNames(result, name, moduleNameFromFile(entry.File), current)
	}
	plan.events = compiledEventValueTypes(result, events, catalog)
	return plan
}

func (plan *compiledValuePlan) addCompiledNames(result *semantic.Result, name, owner, current string) {
	if !normalizedGoNameIsExported(name) {
		public := "LuxoPrivateType_" + hex.EncodeToString([]byte(name))
		for result.Types[public] != nil || result.Scope != nil && result.Scope.LookupReadOnly(public) != nil {
			public += "_"
		}
		plan.publicTypes[name] = public
	}
	for _, function := range []string{"ReadColumnar" + name, "WriteColumnar" + name} {
		public := function + "Depth"
		for result.Types[public] != nil || result.Scope != nil && result.Scope.LookupReadOnly(public) != nil {
			public += "_"
		}
		plan.columnar[function] = public
	}
	if current != "" && owner != current {
		plan.aliases[name] = owner
	}
}

func (g *GeneratorContext) valueAliasOwner(name string) string {
	if g.valueTypes == nil {
		return ""
	}
	return g.valueTypes.aliases[name]
}

func (g *GeneratorContext) valueDepthMethod(name, private string) string {
	if g.sealedDeclaration(name) != nil {
		switch private {
		case "writeLuxoDepth":
			return "WriteLuxoDepth"
		case "readLuxoDepth":
			return "ReadLuxoDepth"
		case "readLuxoInputDepth":
			return "ReadLuxoInputDepth"
		case "readJSONInputDepth":
			return "ReadJSONInputDepth"
		case "checkJSONInputDepth":
			return "CheckJSONInputDepth"
		case "marshalEventSnapshot":
			return "MarshalEventSnapshot"
		case "unmarshalEventSnapshot":
			return "UnmarshalEventSnapshot"
		}
	}
	if g.valueTypes != nil {
		if method := g.valueTypes.methods[name][private]; method != "" {
			return method
		}
	}
	return private
}

func (g *GeneratorContext) withoutValueAliases(result *semantic.Result) *semantic.Result {
	if g.valueTypes == nil || len(g.valueTypes.aliases) == 0 {
		return result
	}
	copy := *result
	copy.Files = slices.Clone(result.Files)
	for index, file := range result.Files {
		owned := *file
		owned.Types = nil
		for _, declaration := range file.Types {
			if g.valueTypes.aliases[declaration.Name] == "" {
				owned.Types = append(owned.Types, declaration)
			}
		}
		copy.Files[index] = &owned
	}
	return &copy
}

func (g *GeneratorContext) generateValueAliases(result *semantic.Result, packageName string) []byte {
	if g.valueTypes == nil || len(g.valueTypes.aliases) == 0 {
		return nil
	}
	var names []string
	for _, file := range result.Files {
		for _, declaration := range file.Types {
			if g.valueTypes.aliases[declaration.Name] != "" {
				names = append(names, declaration.Name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var b strings.Builder
	writeHeader(&b, packageName, "value_alias.gen.go")
	owners := make(map[string]string)
	b.WriteString("import (\n\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	for _, name := range names {
		owner := g.valueTypes.aliases[name]
		if owners[owner] == "" {
			alias := fmt.Sprintf("_luxoValueOwner%d", len(owners))
			owners[owner] = alias
			fmt.Fprintf(&b, "%s %q\n", alias, g.events.ModulePath+"/"+owner+"/luxo")
		}
	}
	b.WriteString(")\n\n")
	for _, name := range names {
		prefix := owners[g.valueTypes.aliases[name]] + "."
		public := name
		if alternate := g.valueTypes.publicTypes[name]; alternate != "" {
			public = alternate
		}
		fmt.Fprintf(&b, "type %s = %s%s\n", name, prefix, public)
		fmt.Fprintf(&b, "func ReadJSONInput%sList(raw []byte) ([]%s,error) { return %sReadJSONInput%sList(raw) }\n", name, name, prefix, name)
		fmt.Fprintf(&b, "func ReadColumnar%s(raw []byte) ([]%s,error) { return %sReadColumnar%s(raw) }\n", name, name, prefix, name)
		fmt.Fprintf(&b, "func %s(raw []byte,depth int) ([]%s,error) { return %s%s(raw,depth) }\n", g.depthColumnarFunction("ReadColumnar"+name), name, prefix, g.depthColumnarFunction("ReadColumnar"+name))
		writer := "WriteColumnar" + name
		fmt.Fprintf(&b, "func %s(buf *api.ResponseBuf,items []%s,mask []byte) error { return %s%s(buf,items,mask) }\n", writer, name, prefix, writer)
		fmt.Fprintf(&b, "func %s(buf *api.ResponseBuf,items []%s,mask []byte,depth int) error { return %s%s(buf,items,mask,depth) }\n\n", g.depthColumnarFunction(writer), name, prefix, g.depthColumnarFunction(writer))
	}
	return []byte(b.String())
}
