package main

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func commonModuleGlobals(files []*ast.File) []*ast.ValStmt {
	var globals []*ast.ValStmt
	for _, file := range files {
		globals = append(globals, file.Globals...)
	}
	return globals
}

// Keep read-only analysis metadata intact when selecting a service's source.
// Constants precede local initializers without changing module ownership or
// importing the common module's runtime declarations and adapters.
func moduleCompilationResult(full *semantic.Result, files []*ast.File, shared []*ast.ValStmt) *semantic.Result {
	result := *full
	result.Files = files
	if len(files) == 0 || len(shared) == 0 {
		return &result
	}
	first := *files[0]
	first.Globals = make([]*ast.ValStmt, 0, len(shared)+len(first.Globals))
	first.Globals = append(first.Globals, shared...)
	first.Globals = append(first.Globals, files[0].Globals...)
	result.Files = slices.Clone(files)
	result.Files[0] = &first
	return &result
}
