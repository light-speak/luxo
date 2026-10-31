package semantic

import (
	"slices"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type eventPayloadField struct {
	ref    *ast.TypeRef
	path   string
	pos    token.Position
	parent int
}

// Check declarations after all files have resolved their fields. In particular,
// nullable forward references must not retain an incomplete Fields snapshot.
func (a *Analyzer) validateEventSnapshots(files []*ast.File) {
	var fields map[string][]*ast.FieldDecl
	for _, file := range files {
		for _, event := range file.Events {
			for _, param := range event.Params {
				if fields == nil {
					fields = eventSnapshotFields(files)
				}
				a.validateEventPayload(eventPayloadField{ref: param.Type, path: event.Name + "." + param.Name, pos: param.Pos}, fields)
			}
		}
	}
}

func eventSnapshotFields(files []*ast.File) map[string][]*ast.FieldDecl {
	fields := make(map[string][]*ast.FieldDecl)
	for _, file := range files {
		for _, declaration := range file.Types {
			fields[declaration.Name] = declaration.Fields
		}
	}
	return fields
}

// An iterative walk visits each snapshot type once per event parameter, so
// recursive declarations cannot overflow the compiler stack or loop forever.
func (a *Analyzer) validateEventPayload(root eventPayloadField, fields map[string][]*ast.FieldDecl) {
	root.parent = -1
	pending := []eventPayloadField{root}
	visited := make(map[string]bool)
	for index := 0; index < len(pending); index++ {
		field := pending[index]
		if field.ref == nil {
			continue
		}
		for _, argument := range field.ref.TypeArgs {
			pending = append(pending, eventPayloadField{argument, field.path, field.pos, field.parent})
		}
		for _, member := range field.ref.Tuple {
			pending = append(pending, eventPayloadField{member, field.path, field.pos, field.parent})
		}
		resolved := a.types[field.ref.Name]
		if resolved == nil {
			continue // Unknown types are reported by normal type resolution.
		}
		if resolved.Kind == TypeModel {
			path := eventPayloadPath(pending, index)
			a.addError(field.pos,
				"event payload '%s' cannot reference database model '%s'; declare an explicit type snapshot / 事件载荷 '%s' 不能引用数据库模型 '%s'，请声明显式 type 快照",
				path, resolved.Name, path, resolved.Name)
		} else if field.parent >= 0 && !isEventWireKind(resolved.Kind) {
			path := eventPayloadPath(pending, index)
			a.addError(field.pos,
				"event payload '%s' has unsupported wire type '%s' / 事件载荷 '%s' 使用了不支持的传输类型 '%s'",
				path, resolved.Name, path, resolved.Name)
		}
		if resolved.Kind != TypeCustom || visited[resolved.Name] {
			continue
		}
		visited[resolved.Name] = true
		for _, nested := range fields[resolved.Name] {
			pending = append(pending, eventPayloadField{nested.Type, nested.Name, nested.Pos, index})
		}
	}
}

func isEventWireKind(kind TypeKind) bool {
	switch kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeBytes, TypeJSON, TypeCustom, TypeEnum:
		return true
	default:
		return false
	}
}

// Build diagnostic paths only for rejected payloads, avoiding quadratic text
// allocation along a deep but valid snapshot graph.
func eventPayloadPath(fields []eventPayloadField, index int) string {
	var parts []string
	for index >= 0 {
		parts = append(parts, fields[index].path)
		index = fields[index].parent
	}
	slices.Reverse(parts)
	return strings.Join(parts, ".")
}
