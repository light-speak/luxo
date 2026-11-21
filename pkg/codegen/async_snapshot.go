package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

type asyncCopyType struct {
	ref    *ast.TypeRef
	goType string
	method string
}

type asyncCopyObject struct {
	name        string
	sealedValue bool
}

// Snapshot helpers are concrete and emitted only for actually captured types.
type asyncSnapshotGenerator struct {
	fields  map[string][]fieldInfo
	refs    map[string][]*ast.FieldDecl
	methods map[string]string
	queue   []asyncCopyType
	objects []asyncCopyObject
	sealed  *sealedPlan
	owner   *GeneratorContext
}

func (g *GeneratorContext) newAsyncSnapshots(result *semantic.Result, enums map[string]bool) *asyncSnapshotGenerator {
	snapshot := &asyncSnapshotGenerator{fields: make(map[string][]fieldInfo), refs: make(map[string][]*ast.FieldDecl), methods: make(map[string]string), sealed: g.sealed, owner: g}
	if result == nil {
		return snapshot
	}
	add := func(name string, fields []*ast.FieldDecl) {
		snapshot.refs[name] = fields
		snapshot.fields[name], _, _ = g.collectFieldInfos(fields, enums)
	}
	for _, file := range result.Files {
		for _, declaration := range file.Types {
			add(declaration.Name, declaration.Fields)
		}
		for _, declaration := range file.Models {
			fields := declaration.Fields
			if isSoftDelete(declaration) && !hasDeletedAtField(fields) {
				fields = append(append([]*ast.FieldDecl(nil), fields...), softDeleteField())
			}
			add(declaration.Name, fields)
		}
		for _, declaration := range file.Extends {
			model := g.extendStubModel(declaration)
			add(model.Name, model.Fields)
		}
	}
	return snapshot
}

func (g *asyncSnapshotGenerator) mutable(ref *ast.TypeRef) bool {
	if ref.Name == "Channel" && !ref.IsList {
		return false
	}
	_, object := g.fields[ref.Name]
	object = object || g.sealed != nil && g.sealed.declarations[ref.Name] != nil
	return object || ref.Nullable || ref.IsList || ref.Name == "Bytes" || ref.Name == "JSON"
}

func (g *asyncSnapshotGenerator) copy(ref *ast.TypeRef, goType, value string) string {
	if !g.mutable(ref) {
		return value
	}
	method, exists := g.methods[goType]
	if !exists {
		method = "value" + strconv.Itoa(len(g.queue))
		g.methods[goType] = method
		g.queue = append(g.queue, asyncCopyType{ref: ref, goType: goType, method: method})
	}
	return "s." + method + "(" + value + ")"
}

func (g *GeneratorContext) generateAsyncSnapshots(packageName string) []byte {
	snapshot := g.asyncSnapshots
	if snapshot == nil || len(snapshot.queue) == 0 {
		return nil
	}
	var methods strings.Builder
	for index := 0; index < len(snapshot.queue); index++ {
		snapshot.writeCopy(&methods, snapshot.queue[index])
	}
	var b strings.Builder
	writeHeader(&b, packageName, "async.gen.go")
	snapshot.writeImports(&b)
	snapshot.writeState(&b)
	b.WriteString(methods.String())
	return []byte(b.String())
}

func (g *asyncSnapshotGenerator) writeImports(b *strings.Builder) {
	var types strings.Builder
	bytes := false
	for _, value := range g.queue {
		types.WriteString(value.goType)
		bytes = bytes || value.ref.Name == "Bytes" || value.ref.Name == "JSON"
	}
	b.WriteString("import (\n")
	if bytes {
		b.WriteString("\"bytes\"\n")
	}
	for _, dependency := range []struct{ qualifier, path string }{
		{"json.", "encoding/json"}, {"time.", "time"},
		{"uuid.", "github.com/google/uuid"}, {"decimal.", "github.com/shopspring/decimal"},
	} {
		if strings.Contains(types.String(), dependency.qualifier) {
			fmt.Fprintf(b, "%q\n", dependency.path)
		}
	}
	b.WriteString(")\n")
}

func (g *asyncSnapshotGenerator) writeCopy(b *strings.Builder, value asyncCopyType) {
	fmt.Fprintf(b, "func (s *_luxoAsyncSnapshot) %s(value %s) %s {\n", value.method, value.goType, value.goType)
	switch {
	case value.ref.IsList:
		g.writeListCopy(b, value)
	case strings.HasPrefix(value.goType, "*"):
		if _, object := g.fields[value.ref.Name]; object || g.isSealed(value.ref.Name) {
			g.writeObjectPointer(b, value)
		} else {
			base := *value.ref
			base.Nullable = false
			fmt.Fprintf(b, "if value == nil {return nil}; out := %s; return &out\n", g.copy(&base, strings.TrimPrefix(value.goType, "*"), "*value"))
		}
	case value.ref.Name == "Bytes" || value.ref.Name == "JSON":
		b.WriteString("return bytes.Clone(value)\n")
	case g.isSealed(value.ref.Name):
		g.writeSealedCopy(b, value)
	default:
		b.WriteString("out := value\n")
		g.writeFields(b, value.ref.Name, "out", "value")
		b.WriteString("return out\n")
	}
	b.WriteString("}\n")
}

func (g *asyncSnapshotGenerator) writeListCopy(b *strings.Builder, value asyncCopyType) {
	element := *value.ref
	element.IsList, element.Nullable = false, false
	elementType := strings.TrimPrefix(value.goType, "[]")
	fmt.Fprintf(b, "if value == nil {return nil}; out := make(%s,len(value))\n", value.goType)
	if !g.mutable(&element) {
		b.WriteString("copy(out,value)\n")
	} else {
		fmt.Fprintf(b, "for index := range value {out[index] = %s}\n", g.copy(&element, elementType, "value[index]"))
	}
	b.WriteString("return out\n")
}

func (g *asyncSnapshotGenerator) writeObjectPointer(b *strings.Builder, value asyncCopyType) {
	name := value.ref.Name
	g.objects = append(g.objects, asyncCopyObject{name: name})
	tag := len(g.objects) - 1
	fmt.Fprintf(b, `if value == nil {return nil}
if out := s.seen%s[value]; out != nil {return out}
out := new(%s)
if s.seen%s == nil {s.seen%s = make(map[*%s]*%s)}
s.seen%s[value] = out
s.queue%s = append(s.queue%s,struct{source,target *%s}{value,out})
if len(s.queue%s)==1 {s.ready=append(s.ready,%d)}
return out
}
func (s *_luxoAsyncSnapshot) fill%s(out,value *%s) {
*out = *value
`, name, name, name, name, name, name, name, name, name, name, name, tag, name, name)
	g.writeFields(b, name, "out", "value")
}

func (g *asyncSnapshotGenerator) writeFields(b *strings.Builder, name, target, source string) {
	if g.isSealed(name) {
		fmt.Fprintf(b, "*%s = %s\n", target, g.copy(&ast.TypeRef{Name: name}, name, "*"+source))
		return
	}
	for index, field := range g.fields[name] {
		ref := g.refs[name][index].Type
		if ref != nil && g.mutable(ref) {
			fmt.Fprintf(b, "%s.%s = %s\n", target, field.goName, g.copy(ref, field.goType, source+"."+field.goName))
		}
	}
}

func (g *asyncSnapshotGenerator) writeState(b *strings.Builder) {
	b.WriteString("// Each launch owns its clone graph; channels remain explicit shared handles.\ntype _luxoAsyncSnapshot struct {\n")
	if len(g.objects) > 0 {
		b.WriteString("ready []int\n")
	}
	for tag, object := range g.objects {
		name := object.name
		if object.sealedValue {
			fmt.Fprintf(b, "_seenSealed%d map[%s]%s\n_queueSealed%d []struct{source,target %s}\n", tag, name, name, tag, name)
		} else {
			fmt.Fprintf(b, "seen%s map[*%s]*%s\nqueue%s []struct{source,target *%s}\n", name, name, name, name, name)
		}
	}
	b.WriteString("}\nfunc (s *_luxoAsyncSnapshot) finish() {\n")
	if len(g.objects) > 0 {
		b.WriteString("for ready:=0;ready<len(s.ready);ready++ { switch s.ready[ready] {\n")
		for tag, object := range g.objects {
			if object.sealedValue {
				fmt.Fprintf(b, "case %d: for index:=0;index<len(s._queueSealed%d);index++ { pair:=s._queueSealed%d[index];s._fillSealed%d(pair.target,pair.source) };s._queueSealed%d=nil\n", tag, tag, tag, tag, tag)
			} else {
				name := object.name
				fmt.Fprintf(b, "case %d: for index:=0;index<len(s.queue%s);index++ { pair:=s.queue%s[index];s.fill%s(pair.target,pair.source) };s.queue%s=nil\n", tag, name, name, name, name)
			}
		}
		b.WriteString("} }\n")
	}
	b.WriteString("}\n")
}
