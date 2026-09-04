package semantic

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Error represents a semantic error.
type Error struct {
	Pos        token.Position
	Message    string
	Suggestion string // "did you mean ...?"
}

func (e Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Pos, e.Message)
	if e.Suggestion != "" {
		s += " (" + e.Suggestion + ")"
	}
	return s
}

// Warning represents a semantic warning.
type Warning struct {
	Pos     token.Position
	NameLen int // length of the identifier for diagnostic range
	Message string
}

// Result is the output of semantic analysis.
type Result struct {
	Scope    *Scope
	Types    map[string]*ResolvedType // all resolved types by name
	Files    []*ast.File              // original AST files (for codegen access to directives, bodies, etc.)
	Errors   []Error
	Warnings []Warning
}

// Analyzer owns the mutable state of one semantic compilation.
// Use Analyze or AnalyzeModules for independent concurrent compilations.
type Analyzer struct {
	scope          *Scope
	types          map[string]*ResolvedType
	errors         []Error
	warnings       []Warning
	inLambda       bool // true when checking inside a lambda body
	inTransaction  bool // true when checking inside a transaction block
	inAwait        bool // true when checking inside an await block
	inCallFunc     bool // true when checking the Func part of a CallExpr
	resultOperand  int  // positive while checking the direct operand of Result<T>?
	expectedReturn *ResolvedType
	files          []*ast.File
	currentFile    *ast.File // file being analyzed in Pass 4

	// module isolation
	moduleMap          FileModuleMap                         // file → module info
	typeOwners         *typeOwnership                        // type name → owning module
	extendVisible      map[string]map[string]bool            // module → set of type names visible via extend
	extendFieldVisible map[string]map[string]map[string]bool // module → modelName → set of field names visible via extend
}

// New creates a new Analyzer.
func New() *Analyzer {
	return newAnalyzer()
}

func newAnalyzer() *Analyzer {
	a := &Analyzer{
		scope:              NewScope(),
		types:              make(map[string]*ResolvedType),
		typeOwners:         newTypeOwnership(),
		extendVisible:      make(map[string]map[string]bool),
		extendFieldVisible: make(map[string]map[string]map[string]bool),
	}
	// register built-in types
	for name, typ := range BuiltinTypes() {
		a.types[name] = typ
		a.scope.Define(&Symbol{
			Name: name,
			Kind: SymType,
			Type: typ,
		})
	}
	return a
}

// Analyze creates an isolated semantic context for one compilation.
func Analyze(files []*ast.File) *Result {
	return newAnalyzer().analyzeInternal(files)
}

// AnalyzeModules creates an isolated semantic context with module boundaries.
func AnalyzeModules(files []*ast.File) *Result {
	a := newAnalyzer()
	a.moduleMap = BuildFileModuleMap(files)
	return a.analyzeInternal(files)
}

// Analyze performs semantic analysis on one or more parsed files.
// When module isolation is not needed (e.g., single-file analysis), use this method.
func (a *Analyzer) Analyze(files []*ast.File) *Result {
	return a.analyzeInternal(files)
}

// AnalyzeWithModules performs semantic analysis with module scope isolation.
// Files are grouped into modules based on their path under origin/.
// Cross-module type references are only allowed via extend declarations.
func (a *Analyzer) AnalyzeWithModules(files []*ast.File) *Result {
	a.moduleMap = BuildFileModuleMap(files)
	return a.analyzeInternal(files)
}

func (a *Analyzer) analyzeInternal(files []*ast.File) *Result {
	a.files = files
	a.runDeclarationPass(files)
	a.runTypePass(files)
	a.runBodyPass(files)
	a.runPostAnalysisPass(files)

	return &Result{
		Scope:    a.scope,
		Types:    a.types,
		Files:    files,
		Errors:   a.errors,
		Warnings: a.warnings,
	}
}

func (a *Analyzer) runDeclarationPass(files []*ast.File) {
	// Pass 1: collect all top-level declarations.
	for _, file := range files {
		a.collectDeclarations(file)
	}
	if a.moduleMap != nil {
		a.collectExtendVisibility()
		a.checkCrossModuleDuplicates()
	}
}

func (a *Analyzer) runTypePass(files []*ast.File) {
	// Pass 2: resolve inheritance, fields, directives, and module visibility.
	for _, file := range files {
		a.resolveInheritance(file)
	}
	for _, file := range files {
		a.resolveFields(file)
	}
	a.validateComputedFields(files)
	if a.moduleMap != nil {
		a.validateFederationExtendFields()
		a.checkModuleVisibility()
	}
	for _, file := range files {
		for _, api := range file.APIs {
			a.validateBareAPI(api)
		}
	}
}

func (a *Analyzer) runBodyPass(files []*ast.File) {
	// Pass 3: check API and function bodies.
	for _, file := range files {
		a.currentFile = file
		a.checkBodies(file)
	}
	a.currentFile = nil
}

func (a *Analyzer) runPostAnalysisPass(files []*ast.File) {
	// Pass 4: validate event cycles and annotate query plans.
	a.checkEventCycles()
	for _, file := range files {
		a.analyzeQueries(file)
	}
}
