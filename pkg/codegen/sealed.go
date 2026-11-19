package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

type sealedPlan struct {
	declarations map[string]*ast.SealedDecl
	payloadNames map[*ast.SealedVariant]string
	events       map[string]bool
	snapshots    map[string]sealedSnapshotNames
	equality     *sealedEqualityPlan
}

type sealedSnapshotNames struct{ copy, clone string }

func (g *GeneratorContext) prepareSealeds(result *semantic.Result) error {
	g.sealed = nil
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			if declaration == nil {
				return fmt.Errorf("sealed declaration is missing")
			}
			if g.sealed == nil {
				g.sealed = &sealedPlan{declarations: make(map[string]*ast.SealedDecl)}
			}
			g.sealed.declarations[declaration.Name] = declaration
			for _, variant := range declaration.Variants {
				if variant == nil {
					return fmt.Errorf("sealed %s variant is missing", declaration.Name)
				}
				tag := g.modelFieldID(declaration.Name, variant.Name)
				if tag == 0 {
					return fmt.Errorf("sealed variant %s.%s has no stable tag", declaration.Name, variant.Name)
				}
				for _, field := range variant.Fields {
					if field == nil || field.Type == nil {
						return fmt.Errorf("sealed payload %s.%s field type is missing", declaration.Name, variant.Name)
					}
					if g.modelFieldID(declaration.Name+"."+variant.Name, field.Name) == 0 {
						return fmt.Errorf("sealed payload field %s.%s.%s has no stable ID", declaration.Name, variant.Name, field.Name)
					}
				}
			}
		}
	}
	return nil
}

func (g *GeneratorContext) prepareSealedGo(result *semantic.Result) {
	g.sealed.payloadNames = make(map[*ast.SealedVariant]string)
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			for _, variant := range declaration.Variants {
				tag := g.modelFieldID(declaration.Name, variant.Name)
				name := "_luxoSealed" + strconv.Itoa(len(declaration.Name)) + "_" + declaration.Name + "_" + strconv.Itoa(tag)
				g.sealed.payloadNames[variant] = g.reserveSealedSymbol(name)
			}
		}
	}
	g.prepareSealedEquality(result)
	g.sealed.events = g.sealedEventTypes(result)
	var names []string
	for name := range g.sealed.declarations {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if g.sealed.events[name] {
			if g.sealed.snapshots == nil {
				g.sealed.snapshots = make(map[string]sealedSnapshotNames)
			}
			g.sealed.snapshots[name] = sealedSnapshotNames{g.reserveSealedSymbol("_luxoSealedCopy" + name), g.reserveSealedSymbol("_luxoSealedClone" + name)}
		}
	}
}

func (g *GeneratorContext) reserveSealedSymbol(name string) string {
	for g.bindingPlan != nil && g.bindingPlan.contains(name) {
		name += "_"
	}
	if g.bindingPlan != nil {
		g.bindingPlan.addExact(name)
	}
	return name
}

func (g *GeneratorContext) sealedDeclaration(name string) *ast.SealedDecl {
	if g == nil || g.sealed == nil {
		return nil
	}
	return g.sealed.declarations[name]
}

func (g *GeneratorContext) generateSealedFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	if g.sealed == nil {
		return nil
	}
	g.prepareSealedGo(result)
	var body strings.Builder
	var objects map[string]bool
	owners := make(map[string]string)
	var imports strings.Builder
	owned := false
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			if owner := g.valueAliasOwner(declaration.Name); owner != "" {
				alias := owners[owner]
				if alias == "" {
					alias = g.reserveSealedSymbol("_luxoSealedOwner" + strconv.Itoa(len(owners)))
					owners[owner] = alias
					fmt.Fprintf(&imports, "import %s %q\n", alias, g.events.ModulePath+"/"+owner+"/luxo")
				}
				g.writeSealedAlias(&body, declaration.Name, alias+".")
				continue
			}
			owned = true
			g.writeSealedHolder(&body, declaration)
			g.writeSealedCodec(&body, declaration, enums)
			g.writeSealedEquality(&body, declaration, enums)
			if g.sealed.events[declaration.Name] {
				if objects == nil {
					objects = collectEventObjectTypes(result)
				}
				g.writeSealedEventCodec(&body, declaration, enums, objects)
			}
		}
	}
	g.writeSealedEqualityStates(&body, enums)
	var b strings.Builder
	writeHeader(&b, packageName, "sealed.gen.go")
	if owned {
		writeWriteJSONImports(&b, nil, generatedPackages(body.String()), body.String())
	} else {
		b.WriteString("import \"github.com/light-speak/luxo/pkg/lux/api\"\n")
	}
	b.WriteString(imports.String())
	if objects != nil {
		b.WriteString("import \"github.com/light-speak/luxo/pkg/lux/event\"\n")
	}
	b.WriteString(body.String())
	return []byte(b.String())
}

func (g *GeneratorContext) writeSealedHolder(b *strings.Builder, declaration *ast.SealedDecl) {
	fmt.Fprintf(b, "// %s is a closed, statically typed sum. Its zero value is invalid.\ntype %s struct {\n_tag uint16\n", declaration.Name, declaration.Name)
	for _, variant := range declaration.Variants {
		fmt.Fprintf(b, "_v%d *%s\n", g.modelFieldID(declaration.Name, variant.Name), g.sealed.payloadNames[variant])
	}
	b.WriteString("}\n")
	if g.valueTypes != nil && g.valueTypes.publicTypes[declaration.Name] != "" {
		fmt.Fprintf(b, "type %s = %s\n", g.valueTypes.publicTypes[declaration.Name], declaration.Name)
	}
	fmt.Fprintf(b, "func (v %s) LuxoVariantTag() uint16 { return v._tag }\n", declaration.Name)
	fmt.Fprintf(b, "func (v %s) LuxoIsValid() bool { switch v._tag {\n", declaration.Name)
	for _, variant := range declaration.Variants {
		tag := g.modelFieldID(declaration.Name, variant.Name)
		fmt.Fprintf(b, "case %d:return v._v%d!=nil\n", tag, tag)
	}
	b.WriteString("default:return false\n}}\n")
	for _, variant := range declaration.Variants {
		tag := g.modelFieldID(declaration.Name, variant.Name)
		payload := g.sealed.payloadNames[variant]
		fmt.Fprintf(b, "type %s struct {\n", payload)
		for _, field := range variant.Fields {
			fmt.Fprintf(b, "F%d %s\n", g.modelFieldID(declaration.Name+"."+variant.Name, field.Name), compiledFunctionGoType(field.Type, g.modelDeclarations))
		}
		b.WriteString("}\n")
		g.writeSealedConstructor(b, declaration, variant, tag, payload)
		fmt.Fprintf(b, "func (v %s) AsVariant%d() *%s { if v._tag != %d { return nil }; return v._v%d }\n", declaration.Name, tag, payload, tag, tag)
		if g.sealedVariantHasValueSlots(variant) {
			fmt.Fprintf(b, "func(v %s)LuxoCopyVariant%d()%s{if v._tag==%d&&v._v%d!=nil{payload:=*v._v%d;v._v%d=&payload};return v}\n", declaration.Name, tag, declaration.Name, tag, tag, tag, tag)
		}
	}
}

func (g *GeneratorContext) sealedVariantHasValueSlots(variant *ast.SealedVariant) bool {
	for _, field := range variant.Fields {
		if !field.Type.Nullable && !field.Type.IsList && g.typeDeclarations[field.Type.Name] != nil {
			return true
		}
	}
	return false
}

func (g *GeneratorContext) writeSealedConstructor(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant, tag int, payload string) {
	fmt.Fprintf(b, "func (%s) NewVariant%d(", owner.Name, tag)
	for index, field := range variant.Fields {
		fmt.Fprintf(b, "_f%d %s,", index, compiledFunctionGoType(field.Type, g.modelDeclarations))
	}
	fmt.Fprintf(b, ") %s { return %s{_tag:%d,_v%d:&%s{", owner.Name, owner.Name, tag, tag, payload)
	for index, field := range variant.Fields {
		fmt.Fprintf(b, "F%d:_f%d,", g.modelFieldID(owner.Name+"."+variant.Name, field.Name), index)
	}
	b.WriteString("}} }\n")
}

func sealedField(field *ast.ParamDecl) *ast.FieldDecl {
	return &ast.FieldDecl{Pos: field.Pos, Name: field.Name, Type: field.Type, Default: field.Default, Doc: field.Doc}
}

func (c *compiler) compileSealedConstructor(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || !member.SealedConstructor {
		return "", false
	}
	if c.generator == nil || call.ConstructorType == nil || c.generator.sealedDeclaration(call.ConstructorType.Name) == nil {
		panic(&InternalCompilerError{Detail: "sealed constructor owner metadata is missing"})
	}
	owner := c.generator.sealedDeclaration(call.ConstructorType.Name)
	for _, variant := range owner.Variants {
		if variant.Name == member.Field {
			arguments := c.compileSealedArguments(call, variant)
			return "(" + owner.Name + "{}).NewVariant" + strconv.Itoa(c.generator.modelFieldID(owner.Name, variant.Name)) + "(" + strings.Join(arguments, ", ") + ")", true
		}
	}
	panic(&InternalCompilerError{Detail: "sealed constructor variant metadata is missing"})
}

func (c *compiler) compileSealedArguments(call *ast.CallExpr, variant *ast.SealedVariant) []string {
	return c.compileDeclaredFunctionArguments(call, &ast.FnDecl{Params: variant.Fields})
}
