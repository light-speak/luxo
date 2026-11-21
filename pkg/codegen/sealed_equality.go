package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

type sealedEqualityPlan struct {
	comparable map[string]bool
	contexts   map[string]*sealedEqualityContext
	ordered    []*sealedEqualityContext
}

type sealedEqualityContext struct {
	name  string
	types []string
	ids   map[string]int
	emit  bool
}

func (g *GeneratorContext) prepareSealedEquality(result *semantic.Result) {
	plan := &sealedEqualityPlan{comparable: make(map[string]bool), contexts: make(map[string]*sealedEqualityContext)}
	parents, children := make(map[string][]string), make(map[string][]string)
	var pending, names []string
	for name, owner := range g.sealed.declarations {
		names = append(names, name)
		plan.comparable[name] = true
		for _, variant := range owner.Variants {
			for _, field := range variant.Fields {
				if g.sealedDeclaration(field.Type.Name) != nil && !field.Type.IsList {
					children[name] = append(children[name], field.Type.Name)
					parents[field.Type.Name] = append(parents[field.Type.Name], name)
				} else if !g.sealedFieldSupportsEquality(field.Type, result) {
					if plan.comparable[name] {
						pending = append(pending, name)
					}
					plan.comparable[name] = false
				}
			}
		}
	}
	for index := 0; index < len(pending); index++ {
		for _, parent := range parents[pending[index]] {
			if plan.comparable[parent] {
				plan.comparable[parent] = false
				pending = append(pending, parent)
			}
		}
	}
	slices.Sort(names)
	contexts := make(map[string]*sealedEqualityContext)
	for _, name := range names {
		if !plan.comparable[name] || len(children[name]) == 0 || !sealedEqualityRecursive(name, children) {
			continue
		}
		types := sealedEqualityClosure(name, children)
		key := strings.Join(types, "\x00")
		context := contexts[key]
		if context == nil {
			context = &sealedEqualityContext{name: g.reserveSealedSymbol("_luxoSealedEquality" + strconv.Itoa(len(contexts))), types: types, ids: make(map[string]int)}
			for index, name := range types {
				context.ids[name] = index
			}
			contexts[key] = context
			plan.ordered = append(plan.ordered, context)
		}
		plan.contexts[name] = context
	}
	g.sealed.equality = plan
}

func sealedEqualityRecursive(root string, edges map[string][]string) bool {
	seen := make(map[string]bool)
	pending := append([]string(nil), edges[root]...)
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if name == root {
			return true
		}
		if !seen[name] {
			seen[name] = true
			pending = append(pending, edges[name]...)
		}
	}
	return false
}

func sealedEqualityClosure(root string, edges map[string][]string) []string {
	seen := make(map[string]bool)
	pending := []string{root}
	var names []string
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
			pending = append(pending, edges[name]...)
		}
	}
	slices.Sort(names)
	return names
}

func (g *GeneratorContext) sealedFieldSupportsEquality(ref *ast.TypeRef, result *semantic.Result) bool {
	if ref == nil || ref.IsList || ref.Name == "Bytes" || ref.Name == "JSON" {
		return false
	}
	if declaration := g.typeDeclarations[ref.Name]; declaration != nil {
		if ref.Nullable {
			return true
		}
		for _, field := range declaration.Fields {
			if field.Type == nil || field.Type.IsList || field.Type.Name == "Bytes" || field.Type.Name == "JSON" {
				return false
			}
		}
		return true
	}
	typ := result.Types[ref.Name]
	if typ == nil {
		return false
	}
	switch typ.Kind {
	case semantic.TypeInt, semantic.TypeFloat, semantic.TypeString, semantic.TypeBool, semantic.TypeDateTime, semantic.TypeDuration, semantic.TypeUUID, semantic.TypeDecimal, semantic.TypeEnum, semantic.TypeModel:
		return true
	default:
		return false
	}
}

func (g *GeneratorContext) writeSealedEquality(b *strings.Builder, owner *ast.SealedDecl, enums map[string]bool) {
	if !g.sealed.equality.comparable[owner.Name] {
		return
	}
	fmt.Fprintf(b, "func(left %s)LuxoEqual(right %s)(bool,error){\nif !left.LuxoIsValid()||!right.LuxoIsValid(){return false,fmt.Errorf(%q)};if left.LuxoVariantTag()!=right.LuxoVariantTag(){return false,nil};switch left.LuxoVariantTag(){\n", owner.Name, owner.Name, "invalid sealed value in equality")
	for _, variant := range owner.Variants {
		fmt.Fprintf(b, "case %d:\n", g.modelFieldID(owner.Name, variant.Name))
		if context := g.sealed.equality.contexts[owner.Name]; context != nil && g.sealedVariantHasSum(variant) {
			context.emit = true
			fmt.Fprintf(b, "var state %s;if err:=state.add%d(left,right);err!=nil{return false,err};return state.run()\n", context.name, context.ids[owner.Name])
		} else {
			g.writeSealedEqualityFields(b, owner, variant, nil, enums)
		}
	}
	b.WriteString("default:return false,fmt.Errorf(\"invalid sealed value in equality\")\n}}\n")
}

func (g *GeneratorContext) sealedVariantHasSum(variant *ast.SealedVariant) bool {
	for _, field := range variant.Fields {
		if g.sealedDeclaration(field.Type.Name) != nil {
			return true
		}
	}
	return false
}

func (g *GeneratorContext) writeSealedEqualityFields(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, context *sealedEqualityContext, enums map[string]bool) {
	tag := g.modelFieldID(owner.Name, variant.Name)
	if len(variant.Fields) != 0 {
		fmt.Fprintf(b, "l,r:=left.AsVariant%d(),right.AsVariant%d()\n", tag, tag)
	}
	for _, field := range variant.Fields {
		id := g.modelFieldID(owner.Name+"."+variant.Name, field.Name)
		left, right := "l.F"+strconv.Itoa(id), "r.F"+strconv.Itoa(id)
		if g.sealedDeclaration(field.Type.Name) != nil {
			g.writeSealedEqualityChild(b, context, field.Type, left, right)
		} else {
			fmt.Fprintf(b, "if !(%s){return false,nil}\n", sealedScalarEquality(field.Type, left, right, enums[field.Type.Name]))
		}
	}
	b.WriteString("return true,nil\n")
}

func sealedScalarEquality(ref *ast.TypeRef, left, right string, enum bool) string {
	goType, method := mapBaseType(ref.Name), ""
	switch ref.Name {
	case "DateTime":
		method = "Equal"
	case "Decimal":
		method = "Equal"
	default:
		if !enum && ref.Name != "Int" && ref.Name != "Float" && ref.Name != "String" && ref.Name != "Boolean" && ref.Name != "Duration" && ref.Name != "UUID" {
			return left + " == " + right
		}
	}
	if ref.Nullable {
		l, r := &ast.Ident{}, &ast.Ident{}
		l.SetNullable(true)
		r.SetNullable(true)
		return compileNullableScalarEquality(&ast.BinaryExpr{Op: "==", Left: l, Right: r}, left, right, goType, method)
	}
	if ref.Name == "Decimal" {
		return "lux.CompareDecimal(" + left + "," + right + ") == 0"
	}
	if method != "" {
		return left + "." + method + "(" + right + ")"
	}
	return left + " == " + right
}

func (g *GeneratorContext) writeSealedEqualityChild(b *strings.Builder, context *sealedEqualityContext, ref *ast.TypeRef, left, right string) {
	if ref.Nullable {
		fmt.Fprintf(b, "if %s==nil||%s==nil{if %s!=nil&&!%s.LuxoIsValid()||%s!=nil&&!%s.LuxoIsValid(){return false,fmt.Errorf(\"invalid sealed value in equality\")};if %s!=%s{return false,nil}}else{\n", left, right, left, left, right, right, left, right)
		left, right = "*"+left, "*"+right
	}
	if context == nil {
		fmt.Fprintf(b, "if equal,err:=(%s).LuxoEqual(%s);err!=nil||!equal{return equal,err}\n", left, right)
	} else {
		fmt.Fprintf(b, "if err:=s.add%d(%s,%s);err!=nil{return false,err}\n", context.ids[ref.Name], left, right)
	}
	if ref.Nullable {
		b.WriteString("}\n")
	}
}

func (g *GeneratorContext) writeSealedEqualityStates(b *strings.Builder, enums map[string]bool) {
	for _, context := range g.sealed.equality.ordered {
		if !context.emit {
			continue
		}
		fmt.Fprintf(b, "type %s struct{ready []int\n", context.name)
		for index, name := range context.types {
			typ := g.sealedEqualityType(name)
			fmt.Fprintf(b, "seen%d map[struct{left,right %s}]struct{}\nqueue%d []struct{left,right %s}\n", index, typ, index, typ)
		}
		b.WriteString("}\n")
		g.writeSealedEqualityRun(b, context)
		for index, name := range context.types {
			g.writeSealedEqualityTask(b, context, index, g.sealedDeclaration(name), enums)
		}
	}
}

func (g *GeneratorContext) writeSealedEqualityRun(b *strings.Builder, context *sealedEqualityContext) {
	fmt.Fprintf(b, "func(s *%s)run()(bool,error){for next:=0;next<len(s.ready);next++{switch s.ready[next]{\n", context.name)
	for index := range context.types {
		fmt.Fprintf(b, "case %d:for i:=0;i<len(s.queue%d);i++{pair:=s.queue%d[i];equal,err:=s.check%d(pair.left,pair.right);if err!=nil||!equal{return equal,err}};s.queue%d=nil\n", index, index, index, index, index)
	}
	b.WriteString("}};return true,nil}\n")
}

func (g *GeneratorContext) writeSealedEqualityTask(b *strings.Builder, context *sealedEqualityContext, index int, owner *ast.SealedDecl, enums map[string]bool) {
	typ := g.sealedEqualityType(owner.Name)
	fmt.Fprintf(b, "func(s *%s)add%d(left,right %s)error{if !left.LuxoIsValid()||!right.LuxoIsValid(){return fmt.Errorf(\"invalid sealed value in equality\")};pair:=struct{left,right %s}{left,right};if _,seen:=s.seen%d[pair];seen{return nil};if s.seen%d==nil{s.seen%d=make(map[struct{left,right %s}]struct{})};s.seen%d[pair]=struct{}{};s.queue%d=append(s.queue%d,pair);if len(s.queue%d)==1{s.ready=append(s.ready,%d)};return nil}\n", context.name, index, typ, typ, index, index, index, typ, index, index, index, index, index)
	fmt.Fprintf(b, "func(s *%s)check%d(left,right %s)(bool,error){if left.LuxoVariantTag()!=right.LuxoVariantTag(){return false,nil};switch left.LuxoVariantTag(){\n", context.name, index, typ)
	for _, variant := range owner.Variants {
		fmt.Fprintf(b, "case %d:\n", g.modelFieldID(owner.Name, variant.Name))
		g.writeSealedEqualityFields(b, owner, variant, context, enums)
	}
	b.WriteString("default:return false,fmt.Errorf(\"invalid sealed value in equality\")\n}}\n")
}

func (g *GeneratorContext) sealedEqualityType(name string) string {
	if g.valueTypes != nil {
		if public := g.valueTypes.publicTypes[name]; public != "" {
			return public
		}
	}
	return name
}
