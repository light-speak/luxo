package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type callableKey struct {
	module string
	name   string
}

type callableDeclaration struct {
	key        callableKey
	pos        token.Position
	body       *ast.Block
	directives []*ast.Directive
	api        *ast.ApiDecl
}

// Effect analyses share declaration identity and local-before-common lookup.
func (a *Analyzer) visitCallables(files []*ast.File, visit func(callableDeclaration)) {
	for _, file := range files {
		module := a.fileModule(file.Name)
		for _, api := range file.APIs {
			visit(callableDeclaration{key: callableKey{module, api.Name}, pos: api.Pos,
				body: api.Body, directives: api.Directives, api: api})
		}
		for _, fn := range file.Functions {
			visit(callableDeclaration{key: callableKey{module, fn.Name}, pos: fn.Pos,
				body: fn.Body, directives: fn.Directives})
		}
	}
}

func resolveCallable[T any](index map[callableKey]T, module, name string) (callableKey, T, bool) {
	key := callableKey{module, name}
	value, ok := index[key]
	if !ok && module != "" {
		key.module = "common"
		value, ok = index[key]
	}
	return key, value, ok
}
