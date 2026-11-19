package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func (g *GeneratorContext) sealedEventTypes(result *semantic.Result) map[string]bool {
	var pending []string
	add := func(event *ast.EventDecl) {
		for _, field := range event.Params {
			if field.Type != nil {
				pending = append(pending, field.Type.Name)
			}
		}
	}
	for _, file := range result.Files {
		for _, event := range file.Events {
			add(event)
		}
	}
	if g.events != nil {
		for _, event := range g.events.Events {
			add(event)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	needed := make(map[string]bool)
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if needed[name] {
			continue
		}
		needed[name] = true
		if owner := g.sealedDeclaration(name); owner != nil {
			for _, variant := range owner.Variants {
				for _, field := range variant.Fields {
					pending = append(pending, field.Type.Name)
				}
			}
		} else if object := g.typeDeclarations[name]; object != nil {
			for _, field := range object.Fields {
				pending = append(pending, field.Type.Name)
			}
		}
	}
	return needed
}

func (g *GeneratorContext) writeSealedEventCodec(b *strings.Builder, owner *ast.SealedDecl, enums, objects map[string]bool) {
	g.writeSealedEventWriter(b, owner)
	g.writeSealedEventReader(b, owner)
	for _, variant := range owner.Variants {
		fields := make([]*ast.ParamDecl, len(variant.Fields))
		ids := make(map[string]int, len(fields))
		for index, field := range variant.Fields {
			id := g.modelFieldID(owner.Name+"."+variant.Name, field.Name)
			name := "F" + strconv.Itoa(id)
			fields[index] = &ast.ParamDecl{Name: name, Type: field.Type}
			ids[name] = id
		}
		codec := eventWireCodec{name: g.sealed.payloadNames[variant], marshalName: "marshalEventSnapshot", unmarshalName: "unmarshalEventSnapshot", fields: fields, ids: ids, objectFields: true, objectValues: true, generator: g}
		codec.write(b, enums, objects)
	}
}

func (g *GeneratorContext) writeSealedEventWriter(b *strings.Builder, owner *ast.SealedDecl) {
	fmt.Fprintf(b, "func(v *%s)%s(dst []byte,depth int)([]byte,error){if v==nil{return nil,event.ErrInvalidSnapshot};if depth>=event.MaxSnapshotDepth{return nil,event.ErrSnapshotDepth};switch v._tag{\n", owner.Name, g.valueDepthMethod(owner.Name, "marshalEventSnapshot"))
	for _, variant := range owner.Variants {
		tag := g.modelFieldID(owner.Name, variant.Name)
		fmt.Fprintf(b, "case %d:if v._v%d==nil{return nil,event.ErrInvalidSnapshot};buf:=api.GetBuf();raw,err:=v._v%d.marshalEventSnapshot(buf.B,depth+1);buf.B=raw;if err!=nil{api.PutBuf(buf);return nil,err};enc:=codec.NewEncoder(dst);enc.WriteVarint(%d);enc.WriteBytes(raw);api.PutBuf(buf);return enc.Bytes(),nil\n", tag, tag, tag, tag)
	}
	b.WriteString("default:return nil,event.ErrInvalidSnapshot\n}}\n")
}

func (g *GeneratorContext) writeSealedEventReader(b *strings.Builder, owner *ast.SealedDecl) {
	fmt.Fprintf(b, "func(v *%s)%s(data []byte,depth int)error{if v==nil{return event.ErrInvalidSnapshot};if depth>=event.MaxSnapshotDepth{return event.ErrSnapshotDepth};dec:=codec.NewDecoder(data);if !dec.NextField(){return event.ErrInvalidSnapshot};tag:=dec.FieldID();\n", owner.Name, g.valueDepthMethod(owner.Name, "unmarshalEventSnapshot"))
	if len(owner.Variants) != 0 {
		b.WriteString("raw:=dec.ReadBytes();if err:=dec.Finish();err!=nil{return err}\n")
	}
	b.WriteString("switch tag{\n")
	for _, variant := range owner.Variants {
		tag := g.modelFieldID(owner.Name, variant.Name)
		fmt.Fprintf(b, "case %d:payload:=&%s{};if err:=payload.unmarshalEventSnapshot(raw,depth+1);err!=nil{return err};*v=%s{_tag:%d,_v%d:payload};return nil\n", tag, g.sealed.payloadNames[variant], owner.Name, tag, tag)
	}
	b.WriteString("default:return event.ErrInvalidSnapshot\n}}\n")
}

func (g *eventSnapshotGenerator) writeSealed(b *strings.Builder, owner *ast.SealedDecl) {
	names := g.sealed.snapshots[owner.Name]
	for _, validate := range []bool{true, false} {
		name, result, parameters := names.clone, owner.Name, ""
		if validate {
			name, result, parameters = names.copy, "("+owner.Name+",error)", ",depth int"
		}
		fmt.Fprintf(b, "func %s(value %s%s)%s{\n", name, owner.Name, parameters, result)
		if validate {
			fmt.Fprintf(b, "if depth>=event.MaxSnapshotDepth{return %s{},event.ErrSnapshotDepth};depth++\n", owner.Name)
		}
		b.WriteString("switch value.LuxoVariantTag(){\n")
		for _, variant := range owner.Variants {
			g.writeSealedSnapshotCase(b, owner, variant, validate)
		}
		if validate {
			fmt.Fprintf(b, "default:return %s{},event.ErrInvalidSnapshot\n", owner.Name)
		} else {
			b.WriteString("default:return value\n")
		}
		b.WriteString("}}\n")
	}
}

func (g *eventSnapshotGenerator) writeSealedSnapshotCase(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, validate bool) {
	tag := g.owner.modelFieldID(owner.Name, variant.Name)
	fmt.Fprintf(b, "case %d:\n", tag)
	if validate || len(variant.Fields) != 0 {
		fmt.Fprintf(b, "payload:=value.AsVariant%d();\n", tag)
	}
	if validate {
		fmt.Fprintf(b, "if payload==nil{return %s{},event.ErrInvalidSnapshot}\n", owner.Name)
	}
	fmt.Fprintf(b, "out:=(%s{}).NewVariant%d(", owner.Name, tag)
	for _, field := range variant.Fields {
		fmt.Fprintf(b, "payload.F%d,", g.owner.modelFieldID(owner.Name+"."+variant.Name, field.Name))
	}
	b.WriteString(")\n")
	for _, field := range variant.Fields {
		id := g.owner.modelFieldID(owner.Name+"."+variant.Name, field.Name)
		target := fmt.Sprintf("out.AsVariant%d().F%d", tag, id)
		g.writeValue(b, field.Type, target, fmt.Sprintf("payload.F%d", id), false, validate, owner.Name+"{}", "")
	}
	if validate {
		b.WriteString("return out,nil\n")
	} else {
		b.WriteString("return out\n")
	}
}
