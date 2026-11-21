package semantic

import (
	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Key inference uses effective declarations, including inherited custom IDs.
// Build the primary-key catalog only when an implicit relation needs it.
func (a *Analyzer) validateAllRelationKeys(files []*ast.File) {
	keys := relationModelKeys{files: files}
	for _, file := range files {
		for _, model := range file.Models {
			a.validateModelRelationKeys(model, &keys)
		}
	}
}

type relationModelKeys struct {
	files    []*ast.File
	names    map[string]string
	prepared bool
}

func (keys *relationModelKeys) primaryKey(name string) string {
	if !keys.prepared {
		keys.prepared = true
		for _, file := range keys.files {
			for _, model := range file.Models {
				if key := inferred.PrimaryKeyName(model); key != "id" {
					if keys.names == nil {
						keys.names = make(map[string]string)
					}
					keys.names[model.Name] = key
				}
			}
		}
	}
	if key := keys.names[name]; key != "" {
		return key
	}
	return "id"
}

func (a *Analyzer) validateModelRelationKeys(model *ast.ModelDecl, keys *relationModelKeys) {
	for _, field := range model.Fields {
		if field.Type == nil || field.Computed != nil {
			continue
		}
		target := a.types[field.Type.Name]
		if target == nil || target.Kind != TypeModel {
			continue
		}
		if by, _ := findAPIDirective(field.Directives, "by"); by != nil {
			a.validateRelationKeyPair(model, field, by)
			continue
		}
		local, remote, _ := inferred.ImplicitRelationKeys(model, field, keys.primaryKey(field.Type.Name))
		a.checkRelationKeyPair(model, field, remote, local, field.Pos, "implicit relation")
	}
}

func (a *Analyzer) validateRelationKeyPair(model *ast.ModelDecl, field *ast.FieldDecl, directive *ast.Directive) {
	remote, local := directiveIdentifierArg(directive, 0), directiveIdentifierArg(directive, 1)
	if local == "" {
		local = inferred.PrimaryKeyName(model)
	}
	a.checkRelationKeyPair(model, field, remote, local, directive.Pos, "@by relation")
}

func (a *Analyzer) checkRelationKeyPair(model *ast.ModelDecl, field *ast.FieldDecl, remoteName, localName string, pos token.Position, kind string) {
	owner, target := a.types[model.Name], a.types[field.Type.Name]
	if owner == nil || target == nil || target.Kind != TypeModel {
		return
	}
	local, remote := owner.LookupField(localName), target.LookupField(remoteName)
	if local == nil || remote == nil {
		a.addError(pos, "%s '%s.%s' requires existing keys '%s.%s' and '%s.%s' / 关系的本地键和远端键必须存在", kind,
			model.Name, field.Name, model.Name, localName, target.Name, remoteName)
		return
	}
	if !supportedRelationKey(local) || !supportedRelationKey(remote) || local.Type.Name != remote.Type.Name {
		a.addError(pos, "%s '%s.%s' requires matching stored Int, String, or UUID scalar keys / 关系两侧必须是类型一致的持久化 Int、String 或 UUID 标量键", kind, model.Name, field.Name)
	}
}

func supportedRelationKey(field *FieldInfo) bool {
	return field.Type != nil && !field.Computed && !field.IsMethod && !field.Type.IsList && isPrimaryKeyType(field.Type.Name)
}
