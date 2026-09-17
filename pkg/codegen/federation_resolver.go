package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type resolveEndpoint struct {
	model                                                    *ast.ModelDecl
	modelName, tableName, fkField, fkColumn, svcName, scanFn string
	keyType, keyGoType                                       string
	fkNullable                                               bool
}

func generateFederationResolvers(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateFederationResolvers(b, result, models, enums)
}

func (g *GeneratorContext) generateFederationResolvers(b *strings.Builder, result *semantic.Result, models []*ast.ModelDecl, enums map[string]bool) {
	endpoints := g.collectResolveEndpoints(result, models, enums)
	for _, ep := range endpoints {
		g.generateResolveHandler(b, ep, enums)
		generateResolveGroupWriter(b, ep)
	}
	g.registerResolveEndpoints(b, endpoints)
}

func (g *GeneratorContext) collectResolveEndpoints(result *semantic.Result, models []*ast.ModelDecl, enums map[string]bool) []resolveEndpoint {
	if len(result.Files) == 0 {
		return nil
	}
	module := moduleNameFromFile(result.Files[0].Name)
	owned := make(map[string]*ast.ModelDecl, len(models))
	for _, model := range models {
		owned[model.Name] = model
	}
	var endpoints []resolveEndpoint
	seen := make(map[string]bool)
	for _, file := range result.Files {
		if moduleNameFromFile(file.Name) != module {
			continue
		}
		for _, ext := range file.Extends {
			if owned[ext.Name] != nil {
				continue
			}
			for _, field := range ext.Fields {
				if field.Type == nil || g.ownerModelHasField(ext.Name, field.Name) || !isRelationField(field, enums) {
					continue
				}
				model := owned[field.Type.Name]
				if model == nil {
					continue
				}
				ep := g.resolveEndpoint(ext.Name, field, model)
				if seen[ep.svcName] {
					continue
				}
				seen[ep.svcName] = true
				endpoints = append(endpoints, ep)
			}
		}
	}
	return endpoints
}

func (g *GeneratorContext) resolveEndpoint(parent string, field *ast.FieldDecl, model *ast.ModelDecl) resolveEndpoint {
	fk := g.inferFederationForeignKey(&ast.ModelDecl{Name: parent}, field)
	keyType := g.externalModelIDTypeName(parent)
	fkField := modelFieldByName(model, fk)
	return resolveEndpoint{
		model: model, modelName: model.Name, tableName: str.ToSnakeCase(model.Name) + "s",
		fkField: fk, fkColumn: str.ToSnakeCase(fk), svcName: "svc:resolve:" + model.Name + ":" + fk,
		scanFn: "scan" + model.Name, keyType: keyType, keyGoType: mapBaseType(keyType),
		fkNullable: fkField != nil && fkField.Type != nil && fkField.Type.Nullable,
	}
}

func (g *GeneratorContext) generateResolveHandler(b *strings.Builder, ep resolveEndpoint, enums map[string]bool) {
	fmt.Fprintf(b, "// handleResolve%sBy%s handles %s with an explicit result shape.\n", ep.modelName, str.Capitalize(ep.fkField), ep.svcName)
	fmt.Fprintf(b, "func handleResolve%sBy%s(app *App) api.HandlerFunc {\n", ep.modelName, str.Capitalize(ep.fkField))
	b.WriteString("\treturn func(ctx context.Context, req *api.Request) error {\n")
	fmt.Fprintf(b, "\t\tkeys, err := req.Param%sArray(\"keys\")\n", ep.keyType)
	b.WriteString("\t\tif err != nil { return err }\n")
	b.WriteString("\t\tshape, err := req.ParamInt(\"shape\")\n\t\tif err != nil { return err }\n")
	b.WriteString("\t\tif shape < 0 || shape > 3 { return fmt.Errorf(\"resolver: invalid result shape %d\", shape) }\n")
	fmt.Fprintf(b, "\t\tfields := select%sSQLColumns(req.Select)\n", ep.modelName)
	generateRequiredSQLField(b, ep.fkColumn)
	fmt.Fprintf(b, "\t\tconds := []lux.Condition{lux.New%sField(%q).In(keys...)}\n", ep.keyType, ep.fkColumn)
	fmt.Fprintf(b, "\t\tquery, args := lux.BuildSelectSQL(%q, fields, conds, nil, 0, 0)\n", ep.tableName)
	fmt.Fprintf(b, "\t\trows, err := app.DB.QueryRows(ctx, %s, query, args...)\n", ep.scanFn)
	b.WriteString("\t\tif err != nil { return err }\n")
	writeComputedResolve(b, ep.model, "rows", "\t\t")
	if len(g.analyzeRelations(ep.model, enums)) > 0 {
		fmt.Fprintf(b, "\t\tif err := resolve%sListRelations(ctx, app, rows, req.Select); err != nil { return err }\n", ep.modelName)
	}
	fmt.Fprintf(b, "\t\treturn writeResolve%sBy%sGroups(req, keys, rows, shape)\n\t}\n}\n\n", ep.modelName, str.Capitalize(ep.fkField))
}

// Each group is already a canonical field value. The gateway never converts
// a concatenation of object rows into a nested columnar response.
func generateResolveGroupWriter(b *strings.Builder, ep resolveEndpoint) {
	fmt.Fprintf(b, "func writeResolve%sBy%sGroups(req *api.Request, keys []%s, rows []*%s, shape int64) (err error) {\n", ep.modelName, str.Capitalize(ep.fkField), ep.keyGoType, ep.modelName)
	b.WriteString("\tstart := len(req.Buf.B)\n\ttmp := api.GetBuf()\n")
	b.WriteString("\tdefer func() { api.PutBuf(tmp); if err != nil { req.Buf.B = req.Buf.B[:start] } }()\n")
	fmt.Fprintf(b, "\tgrouped := make(map[%s][]*%s, len(keys))\n", ep.keyGoType, ep.modelName)
	b.WriteString("\tfor _, row := range rows {\n")
	fmt.Fprintf(b, "\t\tfk := row.%s\n", str.Capitalize(ep.fkField))
	if ep.fkNullable {
		b.WriteString("\t\tif fk == nil { continue }\n\t\tgrouped[*fk] = append(grouped[*fk], row)\n")
	} else {
		b.WriteString("\t\tgrouped[fk] = append(grouped[fk], row)\n")
	}
	b.WriteString("\t}\n\treq.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(keys)))\n")
	b.WriteString("\tfor _, key := range keys {\n\t\titems := grouped[key]\n")
	b.WriteString("\t\tif shape&1 == 0 {\n")
	b.WriteString("\t\t\tif len(items) > 1 { return fmt.Errorf(\"resolver: multiple rows for single relation\") }\n")
	b.WriteString("\t\t\tif len(items) == 0 {\n\t\t\t\tif shape&2 == 0 { return fmt.Errorf(\"resolver: required relation missing\") }\n")
	b.WriteString("\t\t\t\treq.Buf.B = codec.AppendNull(req.Buf.B)\n\t\t\t\tcontinue\n\t\t\t}\n\t\t}\n")
	b.WriteString("\t\ttmp.Reset()\n\t\tif shape&1 != 0 {\n")
	fmt.Fprintf(b, "\t\t\tif err := WriteColumnar%s(tmp, items, req.FieldMask); err != nil { return err }\n", ep.modelName)
	b.WriteString("\t\t} else {\n\t\t\tif err := items[0].WriteLuxo(tmp, req.FieldMask); err != nil { return err }\n\t\t}\n")
	b.WriteString("\t\tif shape&2 != 0 { req.Buf.B = codec.AppendPresent(req.Buf.B) }\n")
	b.WriteString("\t\treq.Buf.B = codec.AppendBytes(req.Buf.B, tmp.B)\n\t}\n\treturn nil\n}\n\n")
}

func (g *GeneratorContext) registerResolveEndpoints(b *strings.Builder, endpoints []resolveEndpoint) {
	// The entry point always calls this, even for modules without resolvers.
	b.WriteString("// RegisterFederationResolvers registers cross-service resolve endpoints.\n")
	b.WriteString("func RegisterFederationResolvers(router *api.Router, app *App) {\n")
	for _, ep := range endpoints {
		fmt.Fprintf(b, "\trouter.Handle(%q, handleResolve%sBy%s(app))\n", ep.svcName, ep.modelName, str.Capitalize(ep.fkField))
		if id := g.apiID(ep.svcName); id > 0 {
			fmt.Fprintf(b, "\trouter.Registry.Register(%q, %d)\n", ep.svcName, id)
			fmt.Fprintf(b, "\trouter.Registry.RegisterParams(%q, []api.ParamMeta{{FieldID: 1, Name: \"keys\", Type: %q, IsList: true}, {FieldID: 2, Name: \"shape\", Type: \"Int\"}})\n", ep.svcName, ep.keyType)
		}
	}
	b.WriteString("}\n\n")
}
