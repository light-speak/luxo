package semantic

import "github.com/light-speak/luxo/pkg/ast"

// Compilation-only metadata never replaces the source tree used by editor tools.
type valueTypeCompilation struct {
	templates    map[string]*ast.TypeDecl
	references   map[*ast.TypeRef]*ResolvedType
	sealed       bool
	compiled     bool
	origins      map[string]string
	declarations map[string]CompiledValueDeclaration
	sealeds      map[string]CompiledSealedDeclaration
	uses         map[string]map[string]bool
	injected     map[*ast.File]bool
	events       map[string]*ast.EventDecl
}

// CompiledValueDeclaration identifies an immutable concrete declaration and its
// canonical source owner. Callers must not mutate the declaration graph.
type CompiledValueDeclaration struct {
	Declaration *ast.TypeDecl
	File        string
}

// CompiledSealedDeclaration retains the normalized closed sum and its owner.
// The declaration graph is read-only and independent of editor source nodes.
type CompiledSealedDeclaration struct {
	Declaration *ast.SealedDecl
	File        string
}

type genericAnalysis struct {
	templates  map[string]*ast.TypeDecl
	instances  []*ResolvedType
	fields     map[string]map[string]*FieldInfo
	references map[*ast.TypeRef]*ResolvedType
	ready      bool
	sealed     bool
}

func (a *Analyzer) ensureValueTypes() *genericAnalysis {
	if a.valueTypes == nil {
		a.valueTypes = &genericAnalysis{ready: a.typePassDone}
	}
	return a.valueTypes
}

func (a *Analyzer) genericTemplate(name string) *ast.TypeDecl {
	if a.valueTypes == nil {
		return nil
	}
	return a.valueTypes.templates[name]
}

func (a *Analyzer) valueTypeCompilation() *valueTypeCompilation {
	if len(a.errors) != 0 || a.valueTypes == nil {
		return nil
	}
	return &valueTypeCompilation{
		templates:  a.valueTypes.templates,
		references: a.valueTypes.references,
		sealed:     a.valueTypes.sealed,
	}
}
