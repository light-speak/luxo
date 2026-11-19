package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/semantic"
)

func (g *GeneratorContext) schemaFieldKind(name string, enums map[string]bool) schema.FieldType {
	if g.sealedDeclaration(name) != nil {
		return schema.FieldSealed
	}
	return luxoTypeToSchemaFieldType(name, enums)
}

func (g *GeneratorContext) schemaFieldKindName(name string, enums map[string]bool) string {
	if g.sealedDeclaration(name) != nil {
		return "FieldSealed"
	}
	return luxoTypeToSchemaType(name, enums)
}

func (g *GeneratorContext) sealedPayloadTypeName(name string, enums map[string]bool) string {
	switch g.schemaFieldKind(name, enums) {
	case schema.FieldModel, schema.FieldEnum, schema.FieldSealed:
		return name
	default:
		return ""
	}
}

func (g *GeneratorContext) schemaSealedDeclaration(declaration *ast.SealedDecl, module string, enums map[string]bool) *schema.Sealed {
	result := &schema.Sealed{Name: declaration.Name, Module: module, Description: declaration.Doc, Variants: make([]schema.Variant, len(declaration.Variants))}
	for index, variant := range declaration.Variants {
		converted := &result.Variants[index]
		converted.ID = g.modelFieldID(declaration.Name, variant.Name)
		converted.Name = variant.Name
		converted.Fields = make([]schema.Param, len(variant.Fields))
		for fieldIndex, field := range variant.Fields {
			converted.Fields[fieldIndex] = schema.Param{
				ID: g.modelFieldID(declaration.Name+"."+variant.Name, field.Name), Name: field.Name,
				Type: g.schemaFieldKind(field.Type.Name, enums), TypeName: g.sealedPayloadTypeName(field.Type.Name, enums),
				Nullable: field.Type.Nullable, IsList: field.Type.IsList, HasDefault: field.Default != nil, Description: field.Doc,
			}
		}
	}
	return result
}

func (g *GeneratorContext) buildSchemaSealeds(s *schema.Schema, result *semantic.Result, enums map[string]bool) error {
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			if err := s.RegisterSealed(g.schemaSealedDeclaration(declaration, moduleNameFromFile(file.Name), enums)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *GeneratorContext) writeSealedRegistration(b *strings.Builder, declaration *ast.SealedDecl, module string, enums map[string]bool) {
	fmt.Fprintf(b, "if err:=s.RegisterSealed(&schema.Sealed{Name:%q,Module:%q,Description:%q,Variants:[]schema.Variant{\n", declaration.Name, module, declaration.Doc)
	for _, variant := range declaration.Variants {
		fmt.Fprintf(b, "{ID:%d,Name:%q,Fields:[]schema.Param{\n", g.modelFieldID(declaration.Name, variant.Name), variant.Name)
		for _, field := range variant.Fields {
			fmt.Fprintf(b, "{ID:%d,Name:%q,Type:schema.%s,TypeName:%q,Nullable:%v,IsList:%v,HasDefault:%v,Description:%q},\n", g.modelFieldID(declaration.Name+"."+variant.Name, field.Name), field.Name, g.schemaFieldKindName(field.Type.Name, enums), g.sealedPayloadTypeName(field.Type.Name, enums), field.Type.Nullable, field.Type.IsList, field.Default != nil, field.Doc)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}});err!=nil{return err}\n")
}
