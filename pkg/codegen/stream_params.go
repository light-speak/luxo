package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

func writeNativeStreamPreparation(b *strings.Builder, si streamInfo) {
	name := str.Capitalize(si.apiName)
	fmt.Fprintf(b, "\trouter.HandleStreamPrepared(%q, func(_ context.Context, req *api.Request, identity any) (api.PreparedStreamHandler, error) {\n", si.apiName)
	if si.requiresAuth {
		fmt.Fprintf(b, "\t\tif err := authorizeStream%s(req, identity); err != nil { return nil, err }\n", name)
	}
	fmt.Fprintf(b, "\t\tvar params %sStreamParams\n", name)
	b.WriteString("\t\tif err := params.readRequest(req); err != nil { return nil, err }\n")
	fmt.Fprintf(b, "\t\treturn func(ctx context.Context, stream *api.Stream) error { return handleStream%s(ctx, &params, identity, stream, router, resolver) }, nil\n", name)
	b.WriteString("\t})\n")
}

func writeEventStreamPreparation(b *strings.Builder, si streamInfo) {
	name := str.Capitalize(si.apiName)
	fmt.Fprintf(b, "\tstream%s := api.RegisterEventStream(router, %q, func(_ context.Context, req *api.Request, identity any) (%sStreamParams, error) {\n", name, si.apiName, name)
	fmt.Fprintf(b, "\t\tvar params %sStreamParams\n", name)
	if si.requiresAuth {
		fmt.Fprintf(b, "\t\tif err := authorizeStream%s(req, identity); err != nil { return params, err }\n", name)
	}
	b.WriteString("\t\terr := params.readRequest(req)\n\t\treturn params, err\n\t})\n")
}

func writePreparedStreamAuth(b *strings.Builder, si streamInfo) {
	if !si.requiresAuth {
		return
	}
	fmt.Fprintf(b, "func authorizeStream%s(req *api.Request, rawIdentity any) error {\n", str.Capitalize(si.apiName))
	b.WriteString("\tvar identity auth.Claims\n\tswitch value := rawIdentity.(type) {\n\tcase auth.Claims: identity = value\n\tcase *auth.Claims: if value == nil { return errors.Unauthorized }; identity = *value\n\tdefault: return errors.Unauthorized\n\t}\n\t_ = identity\n")
	if si.auth != nil {
		writeAuthorizationCheck(b, "\t", si.auth)
	}
	b.WriteString("\treturn nil\n}\n\n")
}

// Subscription values outlive the borrowed Request. Scalar readers already own
// strings and typed slices; raw JSON and Binary byte payloads require a copy.
func writeStreamParameterSnapshot(b *strings.Builder, si streamInfo, enums map[string]bool, modelSets ...map[string]*ast.ModelDecl) {
	defaultGenerator().writeStreamParameterSnapshot(b, si, enums, modelSets...)
}

func (g *GeneratorContext) writeStreamParameterSnapshot(b *strings.Builder, si streamInfo, enums map[string]bool, modelSets ...map[string]*ast.ModelDecl) {
	models := firstModelSet(modelSets)
	name := str.Capitalize(si.apiName) + "StreamParams"
	fmt.Fprintf(b, "// %s owns validated subscription parameters.\ntype %s struct {\n", name, name)
	for _, param := range si.params {
		fmt.Fprintf(b, "\t%s %s\n", str.Capitalize(param.Name), nativeGoType(param.Type, models))
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "func (snapshot *%s) readRequest(req *api.Request) error {\n", name)
	bindings := g.parameterBindings(si.params, nil, models, enums, name)
	for _, param := range si.params {
		writeCallableParamBinding(b, param, bindings.name(param.Name), enums, "\t", models)
		writeStreamParamOwnership(b, param.Type, bindings.name(param.Name), enums, "\t", models)
	}
	fmt.Fprintf(b, "\t*snapshot = %s{\n", name)
	for _, param := range si.params {
		fmt.Fprintf(b, "\t\t%s: %s,\n", str.Capitalize(param.Name), bindings.name(param.Name))
	}
	b.WriteString("\t}\n\treturn nil\n}\n\n")
}

func writeStreamParamOwnership(b *strings.Builder, ref *ast.TypeRef, value string, enums map[string]bool, indent string, modelSets ...map[string]*ast.ModelDecl) {
	if ref.Name == "JSON" {
		writeStreamOwnedValue(b, ref, value, enums, indent)
		return
	}
	if ref.Name != "Bytes" && (enums[ref.Name] || paramMethod(resolveGoType(&ast.TypeRef{Name: ref.Name})) != "") {
		return
	}
	// JSON object readers already create independent fields, including Bytes.
	fmt.Fprintf(b, "%sif req.Params == nil {\n", indent)
	if ref.IsList && firstModelSet(modelSets)[ref.Name] != nil {
		fmt.Fprintf(b, "%s\tfor _, item := range %s { ownStreamInput%s(item) }\n", indent, value, ref.Name)
	} else {
		writeStreamOwnedValue(b, ref, value, enums, indent+"\t")
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeStreamOwnedValue(b *strings.Builder, ref *ast.TypeRef, value string, enums map[string]bool, indent string) {
	if ref.IsList {
		fmt.Fprintf(b, "%sif %s != nil {\n", indent, value)
		fmt.Fprintf(b, "%s\t%s = append(%s{}, %s...)\n", indent, value, resolveGoType(ref), value)
		fmt.Fprintf(b, "%s\tfor index := range %s {\n", indent, value)
		base := &ast.TypeRef{Name: ref.Name}
		writeStreamOwnedValue(b, base, value+"[index]", enums, indent+"\t\t")
		fmt.Fprintf(b, "%s\t}\n%s}\n", indent, indent)
		return
	}
	if ref.Nullable {
		fmt.Fprintf(b, "%sif %s != nil {\n", indent, value)
		fmt.Fprintf(b, "%s\towned := *%s\n", indent, value)
		writeStreamOwnedValue(b, &ast.TypeRef{Name: ref.Name}, "owned", enums, indent+"\t")
		fmt.Fprintf(b, "%s\t%s = &owned\n%s}\n", indent, value, indent)
		return
	}
	if ref.Name == "Bytes" || ref.Name == "JSON" {
		fmt.Fprintf(b, "%s%s = bytes.Clone(%s)\n", indent, value, value)
		return
	}
	fmt.Fprintf(b, "%sownStreamInput%s(&%s)\n", indent, ref.Name, value)
}

type streamInputOwnerPlan struct {
	models  map[string]*ast.ModelDecl
	objects map[string][]*ast.FieldDecl
	names   []string
	seen    map[string]bool
}

func collectStreamInputOwners(result *semantic.Result, streams []streamInfo) streamInputOwnerPlan {
	var plan streamInputOwnerPlan
	if len(streams) == 0 {
		return plan
	}
	plan.objects = make(map[string][]*ast.FieldDecl)
	for _, file := range result.Files {
		for _, model := range file.Models {
			plan.objects[model.Name] = model.Fields
		}
		for _, typ := range file.Types {
			plan.objects[typ.Name] = typ.Fields
		}
	}
	var pending []string
	for _, stream := range streams {
		for _, param := range stream.params {
			if _, found := plan.objects[param.Type.Name]; found {
				pending = append(pending, param.Type.Name)
			}
		}
	}
	if len(pending) == 0 {
		return plan
	}
	plan.models, _ = collectInferredAPIs(result)
	plan.seen = make(map[string]bool)
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if plan.seen[name] {
			continue
		}
		plan.seen[name] = true
		plan.names = append(plan.names, name)
		for _, field := range plan.objects[name] {
			if !streamOwnedField(field) {
				continue
			}
			if _, nested := plan.objects[field.Type.Name]; nested {
				pending = append(pending, field.Type.Name)
			}
		}
	}
	return plan
}

func streamOwnedField(field *ast.FieldDecl) bool {
	return field.Type != nil && !hasDirective(field.Directives, "hidden") && !hasDirective(field.Directives, "internal")
}

func writeStreamInputOwners(b *strings.Builder, result *semantic.Result, streams []streamInfo, enums map[string]bool) {
	plan := collectStreamInputOwners(result, streams)
	writePlannedStreamInputOwners(b, &plan, enums)
}

func writePlannedStreamInputOwners(b *strings.Builder, plan *streamInputOwnerPlan, enums map[string]bool) {
	if plan == nil {
		return
	}
	for _, name := range plan.names {
		fmt.Fprintf(b, "func ownStreamInput%s(value *%s) {\n", name, name)
		for _, field := range plan.objects[name] {
			if streamOwnedField(field) {
				writeStreamInputFieldOwnership(b, field, plan.objects, enums, plan.models)
			}
		}
		b.WriteString("}\n\n")
	}
}

func writeStreamInputFieldOwnership(b *strings.Builder, field *ast.FieldDecl, objects map[string][]*ast.FieldDecl, enums map[string]bool, modelSets ...map[string]*ast.ModelDecl) {
	ref := field.Type
	value := "value." + str.Capitalize(field.Name)
	if _, nested := objects[ref.Name]; nested {
		if ref.IsList && len(modelSets) > 0 && modelSets[0][ref.Name] != nil {
			// Generated input readers already own the list and model storage.
			fmt.Fprintf(b, "\tfor _, item := range %s { ownStreamInput%s(item) }\n", value, ref.Name)
			return
		}
		if !ref.IsList {
			fmt.Fprintf(b, "\tif %s != nil { ownStreamInput%s(%s) }\n", value, ref.Name, value)
			return
		}
	} else if ref.Name != "Bytes" && ref.Name != "JSON" {
		return
	}
	writeStreamOwnedValue(b, ref, value, enums, "\t")
}
