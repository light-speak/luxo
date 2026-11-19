package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func (g *GeneratorContext) writeSealedCodec(b *strings.Builder, owner *ast.SealedDecl, enums map[string]bool) {
	g.writeSealedBinaryWriter(b, owner)
	g.writeSealedBinaryReader(b, owner, false)
	g.writeSealedBinaryReader(b, owner, true)
	g.writeSealedJSONReader(b, owner)
	g.writeSealedLists(b, owner)
	g.writeSealedJSONDepthCheck(b, owner)
	for _, variant := range owner.Variants {
		g.writeSealedPayloadWriter(b, owner, variant, enums)
		g.writeSealedPayloadReader(b, owner, variant, enums, false)
		g.writeSealedPayloadReader(b, owner, variant, enums, true)
		g.writeSealedPayloadJSONReader(b, owner, variant, enums)
		fields := make([]*ast.FieldDecl, len(variant.Fields))
		for index, field := range variant.Fields {
			fields[index] = sealedField(field)
		}
		g.writeJSONInputDepthCheck(b, g.sealed.payloadNames[variant], fields, enums)
	}
}

func (g *GeneratorContext) writeSealedJSONDepthCheck(b *strings.Builder, owner *ast.SealedDecl) {
	fmt.Fprintf(b, "func(*%s)%s(reader *jsonutil.DepthReader,depth int)error{\nif reader.Kind()!='{'{return reader.JSON()};if depth>=codec.MaxModelDepth{return codec.ErrModelDepth};return reader.Object(func(name []byte)error{switch string(name){\n", owner.Name, g.valueDepthMethod(owner.Name, "checkJSONInputDepth"))
	for _, variant := range owner.Variants {
		fmt.Fprintf(b, "case %q:return (*%s)(nil).checkJSONInputDepth(reader,depth+1)\n", variant.Name, g.sealed.payloadNames[variant])
	}
	b.WriteString("default:return reader.JSON()\n}})}\n")
}

func (g *GeneratorContext) writeSealedBinaryWriter(b *strings.Builder, owner *ast.SealedDecl) {
	method := g.valueDepthMethod(owner.Name, "writeLuxoDepth")
	fmt.Fprintf(b, "func (v %s) WriteLuxo(buf *api.ResponseBuf, mask []byte) error { return v.%s(buf,mask,1) }\nfunc (v %s) %s(buf *api.ResponseBuf, mask []byte, depth int) error {\nif depth >= codec.MaxModelDepth { return codec.ErrModelDepth }\nswitch v._tag {\n", owner.Name, method, owner.Name, method)
	for _, variant := range owner.Variants {
		tag := g.modelFieldID(owner.Name, variant.Name)
		fmt.Fprintf(b, "case %d:\nif v._v%d == nil { return fmt.Errorf(%q) }\n", tag, tag, owner.Name+": invalid active payload")
		fmt.Fprintf(b, "buf.B = codec.AppendVarint(buf.B,%d)\n_frame := buf.BeginMessage()\n", tag)
		fmt.Fprintf(b, "if len(mask) != 0 { child, nested := codec.SelectionMaskNested(mask,%d); if !nested || !codec.FieldMaskHas(codec.SelectionMaskFields(mask),%d) { buf.B=append(buf.B,0,0); buf.EndMessage(_frame); return nil }; mask=child }\n", tag, tag)
		fmt.Fprintf(b, "if err:=v._v%d.writeLuxoDepth(buf,mask,depth+1);err!=nil{return err};buf.EndMessage(_frame);return nil\n", tag)
	}
	fmt.Fprintf(b, "default:return fmt.Errorf(%q,v._tag)\n}\n}\n", owner.Name+": invalid variant tag %d")
}

func (g *GeneratorContext) writeSealedBinaryReader(b *strings.Builder, owner *ast.SealedDecl, input bool) {
	method, private := "ReadLuxo", "readLuxoDepth"
	if input {
		method, private = "ReadLuxoInput", "readLuxoInputDepth"
	}
	depthMethod := g.valueDepthMethod(owner.Name, private)
	fmt.Fprintf(b, "func(v *%s)%s(dec *codec.Decoder){v.%s(dec,1)}\nfunc(v *%s)%s(dec *codec.Decoder,depth int){\nif depth>=codec.MaxModelDepth{dec.Fail(codec.ErrModelDepth);return}\nif !dec.NextField(){dec.Fail(fmt.Errorf(%q));return}\n_tag:=dec.FieldID();", owner.Name, method, depthMethod, owner.Name, depthMethod, owner.Name+": expected nonzero variant tag")
	if len(owner.Variants) != 0 {
		b.WriteString("_raw:=dec.ReadBytes();if dec.Err()!=nil{return}\n")
	}
	b.WriteString("switch _tag{\n")
	for _, variant := range owner.Variants {
		tag := g.modelFieldID(owner.Name, variant.Name)
		fmt.Fprintf(b, "case %d:\n_payload:=&%s{};_child:=codec.NewDecoder(_raw);_payload.%s(_child,depth+1);dec.Fail(_child.Finish());if dec.Finish()==nil{*v=%s{_tag:%d,_v%d:_payload}}\n", tag, g.sealed.payloadNames[variant], private, owner.Name, tag, tag)
	}
	fmt.Fprintf(b, "default:dec.Fail(fmt.Errorf(%q,_tag))\n}\n}\n", owner.Name+": unknown variant tag %d")
}

func (g *GeneratorContext) writeSealedJSONReader(b *strings.Builder, owner *ast.SealedDecl) {
	method := g.valueDepthMethod(owner.Name, "readJSONInputDepth")
	fmt.Fprintf(b, "func(v *%s)ReadJSONInput(raw []byte)error{return v.%s(raw,1)}\nfunc(v *%s)%s(raw []byte,depth int)error{\nif depth>=codec.MaxModelDepth{return codec.ErrModelDepth}\nvar out %s;seen:=false\nif err:=jsonutil.VisitObject(raw,func(name,value []byte)error{if seen{return fmt.Errorf(%q)};seen=true;switch string(name){\n", owner.Name, method, owner.Name, method, owner.Name, owner.Name+": expected exactly one variant")
	for _, variant := range owner.Variants {
		tag := g.modelFieldID(owner.Name, variant.Name)
		fmt.Fprintf(b, "case %q:payload:=&%s{};if err:=payload.readJSONInputDepth(value,depth+1);err!=nil{return err};out=%s{_tag:%d,_v%d:payload};return nil\n", variant.Name, g.sealed.payloadNames[variant], owner.Name, tag, tag)
	}
	fmt.Fprintf(b, "default:return fmt.Errorf(%q,name)\n}});err!=nil{return err};if !seen{return fmt.Errorf(%q)};*v=out;return nil\n}\n", owner.Name+": unknown variant %q", owner.Name+": expected exactly one variant")
}

func (g *GeneratorContext) writeSealedLists(b *strings.Builder, owner *ast.SealedDecl) {
	name := owner.Name
	writer := g.depthColumnarFunction("WriteColumnar" + name)
	reader := g.depthColumnarFunction("ReadColumnar" + name)
	fmt.Fprintf(b, "func WriteColumnar%s(buf *api.ResponseBuf,items []%s,mask []byte)error{return %s(buf,items,mask,1)}\nfunc %s(buf *api.ResponseBuf,items []%s,mask []byte,depth int)error{\nif len(items)>codec.MaxArrayElements{return fmt.Errorf(\"sealed list exceeds limit\")};buf.B=codec.AppendVarint(buf.B,uint64(len(items)));for i:=range items{frame:=buf.BeginMessage();if err:=items[i].%s(buf,mask,depth);err!=nil{return err};buf.EndMessage(frame)};return nil\n}\n", name, name, writer, writer, name, g.valueDepthMethod(name, "writeLuxoDepth"))
	fmt.Fprintf(b, "func ReadColumnar%s(raw []byte)([]%s,error){return %s(raw,1)}\nfunc %s(raw []byte,depth int)([]%s,error){dec:=codec.NewDecoder(raw);count:=dec.ReadArrayLength();if !dec.CheckElementBytes(count,1){return nil,dec.Err()};items:=make([]%s,count);for i:=range items{data:=dec.ReadBytes();if dec.Err()!=nil{break};child:=codec.NewDecoder(data);items[i].%s(child,depth);dec.Fail(child.Finish());if dec.Err()!=nil{break}};if err:=dec.Finish();err!=nil{return nil,err};return items,nil}\n", name, name, reader, reader, name, name, g.valueDepthMethod(name, "readLuxoDepth"))
	fmt.Fprintf(b, "func ReadJSONInput%sList(raw []byte)([]%s,error){\n", name, name)
	g.writeJSONInputList(b, name, false)
	b.WriteString("}\n")
}

func (g *GeneratorContext) writeSealedPayloadWriter(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, enums map[string]bool) {
	payload := g.sealed.payloadNames[variant]
	fmt.Fprintf(b, "func(v *%s)writeLuxoDepth(buf *api.ResponseBuf,selectionMask []byte,depth int)error{\nif depth>codec.MaxModelDepth{return codec.ErrModelDepth};", payload)
	if len(variant.Fields) != 0 {
		b.WriteString("mask:=codec.SelectionMaskFields(selectionMask)\n")
	}
	b.WriteString("_arenaLength:=0\n")
	for _, field := range variant.Fields {
		if !isArenaField(sealedField(field), enums) {
			continue
		}
		id := g.modelFieldID(owner.Name+"."+variant.Name, field.Name)
		fmt.Fprintf(b, "if codec.FieldMaskHas(mask,%d){", id)
		if field.Type.Nullable {
			fmt.Fprintf(b, "if v.F%d!=nil{_arenaLength+=len(*v.F%d)}", id, id)
		} else {
			fmt.Fprintf(b, "_arenaLength+=len(v.F%d)", id)
		}
		b.WriteString("}\n")
	}
	b.WriteString("buf.B=codec.AppendVarint(buf.B,uint64(_arenaLength))\n")
	for _, parameter := range variant.Fields {
		id := g.modelFieldID(owner.Name+"."+variant.Name, parameter.Name)
		field, value := sealedField(parameter), "v.F"+strconv.Itoa(id)
		fmt.Fprintf(b, "if codec.FieldMaskHas(mask,%d){\n", id)
		if isRelationField(field, enums) {
			g.writeSealedNestedOutput(b, field, strconv.Itoa(id), value)
		} else {
			writeModelFieldEncoding(b, field, strconv.Itoa(id), value, enums, "\t")
		}
		b.WriteString("}\n")
	}
	b.WriteString("buf.B=append(buf.B,0);return nil\n}\n")
}

func (g *GeneratorContext) writeSealedNestedOutput(b *strings.Builder, field *ast.FieldDecl, id, value string) {
	fmt.Fprintf(b, "childMask%s,_:=codec.SelectionMaskNested(selectionMask,%s);buf.B=codec.AppendVarint(buf.B,%s)\n", id, id, id)
	if field.Type.Nullable {
		fmt.Fprintf(b, "if %s==nil{buf.B=codec.AppendNull(buf.B)}else{buf.B=codec.AppendPresent(buf.B)\n", value)
	}
	if field.Type.IsList && g.sealedDeclaration(field.Type.Name) != nil {
		fmt.Fprintf(b, "if err:=%s(buf,%s,childMask%s,depth+1);err!=nil{return err}\n", g.depthColumnarFunction("WriteColumnar"+field.Type.Name), value, id)
		if field.Type.Nullable {
			b.WriteString("}\n")
		}
		return
	}
	fmt.Fprintf(b, "frame%s:=buf.BeginMessage()\n", id)
	if field.Type.IsList {
		fmt.Fprintf(b, "if err:=%s(buf,%s,childMask%s,depth+1);err!=nil{return err}\n", g.depthColumnarFunction("WriteColumnar"+field.Type.Name), value, id)
	} else {
		fmt.Fprintf(b, "if err:=%s.%s(buf,childMask%s,depth+1);err!=nil{return err}\n", value, g.valueDepthMethod(field.Type.Name, "writeLuxoDepth"), id)
	}
	fmt.Fprintf(b, "buf.EndMessage(frame%s)\n", id)
	if field.Type.Nullable {
		b.WriteString("}\n")
	}
}

func (g *GeneratorContext) writeSealedPayloadReader(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, enums map[string]bool, input bool) {
	method := "readLuxoDepth"
	if input {
		method = "readLuxoInputDepth"
	}
	fmt.Fprintf(b, "func(v *%s)%s(dec *codec.Decoder,depth int){\nif depth>codec.MaxModelDepth{dec.Fail(codec.ErrModelDepth);return};dec.SkipArenaHeader()\n", g.sealed.payloadNames[variant], method)
	if input && len(variant.Fields) != 0 {
		fmt.Fprintf(b, "var seen [%d]bool\n", len(variant.Fields))
	}
	b.WriteString("for dec.NextField(){switch dec.FieldID(){\n")
	for index, parameter := range variant.Fields {
		id := g.modelFieldID(owner.Name+"."+variant.Name, parameter.Name)
		fmt.Fprintf(b, "case %d:\n", id)
		field, target := sealedField(parameter), "v.F"+strconv.Itoa(id)
		if isRelationField(field, enums) {
			g.writeSealedNestedInput(b, field, target, input)
		} else {
			writeReadLuxoValue(b, field, target, enums)
		}
		if input {
			fmt.Fprintf(b, "seen[%d]=true\n", index)
		}
	}
	b.WriteString("default:dec.Fail(fmt.Errorf(\"unknown sealed payload field %d\",dec.FieldID()))\n}}\nif dec.Err()!=nil{return}\n")
	if input {
		g.writeSealedInputPresence(b, owner, variant, enums)
	}
	b.WriteString("}\n")
}

func (g *GeneratorContext) writeSealedInputPresence(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, enums map[string]bool) {
	for index, field := range variant.Fields {
		fmt.Fprintf(b, "if !seen[%d]{\n", index)
		if field.Default == nil {
			fmt.Fprintf(b, "dec.Fail(fmt.Errorf(%q));return\n", owner.Name+"."+variant.Name+": missing field "+field.Name)
		} else {
			writeObjectDefaultAssignment(b, sealedField(field), "v.F"+strconv.Itoa(g.modelFieldID(owner.Name+"."+variant.Name, field.Name)), "", enums)
		}
		b.WriteString("}\n")
	}
}

func (g *GeneratorContext) writeSealedNestedInput(b *strings.Builder, field *ast.FieldDecl, target string, input bool, pointerScalars ...bool) {
	if field.Type.Nullable {
		fmt.Fprintf(b, "if !dec.ReadBool(){%s=nil}else{\n", target)
	}
	if field.Type.IsList {
		g.writeSealedNestedListInput(b, field, target, input)
	} else {
		method := "readLuxoDepth"
		if input {
			method = "readLuxoInputDepth"
		}
		method = g.valueDepthMethod(field.Type.Name, method)
		pointer := field.Type.Nullable || g.isModelReference(field.Type.Name) || len(pointerScalars) != 0 && pointerScalars[0]
		if pointer {
			fmt.Fprintf(b, "%s=&%s{}\n", target, field.Type.Name)
		}
		if g.sealedDeclaration(field.Type.Name) != nil || !input {
			fmt.Fprintf(b, "{raw:=dec.ReadBytes();child:=codec.NewDecoder(raw);if dec.Err()==nil{%s.%s(child,depth+1);dec.Fail(child.Finish())}}\n", target, method)
		} else {
			fmt.Fprintf(b, "%s.%s(dec,depth+1)\n", target, method)
		}
	}
	if field.Type.Nullable {
		b.WriteString("}\n")
	}
}

func (g *GeneratorContext) writeSealedNestedListInput(b *strings.Builder, field *ast.FieldDecl, target string, input bool) {
	if g.sealedDeclaration(field.Type.Name) == nil && !input {
		value := "items"
		if g.isModelReference(field.Type.Name) {
			value = "reference" + field.Type.Name + "List(items)"
		}
		fmt.Fprintf(b, "{items,err:=%s(dec.ReadBytes(),depth+1);dec.Fail(err);%s=%s}\n", g.depthColumnarFunction("ReadColumnar"+field.Type.Name), target, value)
		return
	}
	goType := compiledFunctionGoType(&ast.TypeRef{Name: field.Type.Name, IsList: true}, g.modelDeclarations)
	method := "readLuxoDepth"
	if input {
		method = "readLuxoInputDepth"
	}
	method = g.valueDepthMethod(field.Type.Name, method)
	fmt.Fprintf(b, "{count:=dec.ReadArrayLength();if dec.CheckElementBytes(count,1){%s=make(%s,count);for i:=range %s{if dec.Err()!=nil{break};", target, goType, target)
	if g.isModelReference(field.Type.Name) {
		fmt.Fprintf(b, "%s[i]=&%s{};", target, field.Type.Name)
	}
	if g.sealedDeclaration(field.Type.Name) != nil {
		fmt.Fprintf(b, "raw:=dec.ReadBytes();child:=codec.NewDecoder(raw);%s[i].%s(child,depth+1);dec.Fail(child.Finish())", target, method)
	} else {
		fmt.Fprintf(b, "%s[i].%s(dec,depth+1)", target, method)
	}
	b.WriteString("}}}\n")
}

func (g *GeneratorContext) writeSealedPayloadJSONReader(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, enums map[string]bool) {
	fmt.Fprintf(b, "func(v *%s)readJSONInputDepth(raw []byte,depth int)error{\nif depth>codec.MaxModelDepth{return codec.ErrModelDepth}\n", g.sealed.payloadNames[variant])
	if len(variant.Fields) != 0 {
		fmt.Fprintf(b, "var fields [%d][]byte\n", len(variant.Fields))
	}
	b.WriteString("if err:=jsonutil.VisitObject(raw,func(name,value []byte)error{switch string(name){\n")
	for index, field := range variant.Fields {
		fmt.Fprintf(b, "case %q:\n", field.Name)
		g.writeJSONInputPreviousDepth(b, sealedField(field), enums, index, false)
	}
	b.WriteString("default:return fmt.Errorf(\"unknown sealed payload field %q\",name)\n}});err!=nil{return err}\n")
	fmt.Fprintf(b, "var out %s\n", g.sealed.payloadNames[variant])
	for index, field := range variant.Fields {
		g.writeSealedJSONInputField(b, owner, variant, field, index, enums)
	}
	b.WriteString("*v=out;return nil\n}\n")
}

func (g *GeneratorContext) writeSealedJSONInputField(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, field *ast.ParamDecl, index int, enums map[string]bool) {
	target := "out.F" + strconv.Itoa(g.modelFieldID(owner.Name+"."+variant.Name, field.Name))
	fmt.Fprintf(b, "if raw:=fields[%d];raw==nil{\n", index)
	if field.Default == nil {
		fmt.Fprintf(b, "return fmt.Errorf(%q)\n", owner.Name+"."+variant.Name+": missing field "+field.Name)
	} else {
		writeObjectDefaultAssignment(b, sealedField(field), target, "", enums)
	}
	b.WriteString("}else{\n")
	if field.Type.Nullable {
		b.WriteString("if string(raw)!=\"null\"{\n")
	}
	goType := mapBaseType(field.Type.Name)
	if field.Type.IsList {
		goType = "[]" + goType
	}
	fmt.Fprintf(b, "value,err:=func()(%s,error){\n", goType)
	if field.Type.IsList {
		g.writeJSONInputList(b, field.Type.Name, enums[field.Type.Name], false)
	} else {
		g.writeJSONInputScalar(b, field.Type.Name, enums[field.Type.Name], "raw", "", false)
	}
	b.WriteString("}();if err!=nil{return err}\n")
	value := "value"
	if field.Type.IsList && g.isModelReference(field.Type.Name) {
		value = "reference" + field.Type.Name + "List(value)"
	} else if !field.Type.IsList && (field.Type.Nullable || g.isModelReference(field.Type.Name)) {
		value = "&value"
	}
	fmt.Fprintf(b, "%s=%s\n", target, value)
	if field.Type.Nullable {
		b.WriteString("}\n")
	}
	b.WriteString("}\n")
}
