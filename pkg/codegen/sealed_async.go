package codegen

import (
	"fmt"
	"strings"
)

func (g *asyncSnapshotGenerator) isSealed(name string) bool {
	return g.sealed != nil && g.sealed.declarations[name] != nil
}

func (g *asyncSnapshotGenerator) writeSealedCopy(b *strings.Builder, value asyncCopyType) {
	name := value.ref.Name
	tag := len(g.objects)
	g.objects = append(g.objects, asyncCopyObject{name: name, sealedValue: true})
	fmt.Fprintf(b, "if !value.LuxoIsValid(){return value};if out,ok:=s._seenSealed%d[value];ok{return out};var out %s\nswitch value.LuxoVariantTag(){\n", tag, name)
	for _, variant := range g.sealed.declarations[name].Variants {
		id := g.owner.modelFieldID(name, variant.Name)
		fmt.Fprintf(b, "case %d:out=(%s{}).NewVariant%d(", id, name, id)
		for _, field := range variant.Fields {
			fmt.Fprintf(b, "value.AsVariant%d().F%d,", id, g.owner.modelFieldID(name+"."+variant.Name, field.Name))
		}
		b.WriteString(")\n")
	}
	b.WriteString("}\n")
	fmt.Fprintf(b, "if s._seenSealed%d==nil{s._seenSealed%d=make(map[%s]%s)};s._seenSealed%d[value]=out\ns._queueSealed%d=append(s._queueSealed%d,struct{source,target %s}{value,out});if len(s._queueSealed%d)==1{s.ready=append(s.ready,%d)};return out\n}\n", tag, tag, name, name, tag, tag, tag, name, tag, tag)
	fmt.Fprintf(b, "func(s *_luxoAsyncSnapshot)_fillSealed%d(out,value %s){switch value.LuxoVariantTag(){\n", tag, name)
	for _, variant := range g.sealed.declarations[name].Variants {
		id := g.owner.modelFieldID(name, variant.Name)
		fmt.Fprintf(b, "case %d:\n", id)
		for _, field := range variant.Fields {
			if !g.mutable(field.Type) {
				continue
			}
			fieldID := g.owner.modelFieldID(name+"."+variant.Name, field.Name)
			source := fmt.Sprintf("value.AsVariant%d().F%d", id, fieldID)
			goType := compiledFunctionGoType(field.Type, g.owner.modelDeclarations)
			fmt.Fprintf(b, "out.AsVariant%d().F%d=%s\n", id, fieldID, g.copy(field.Type, goType, source))
		}
	}
	b.WriteString("}\n")
}
