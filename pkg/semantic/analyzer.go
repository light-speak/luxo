package semantic

import (
	"fmt"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
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
	// GenericExprTypes preserves generic value arguments, excluding the transient Result ABI.
	GenericExprTypes map[ast.Expr]*ResolvedType
	// ExecutionCacheDependencies contains compiled model reads and declared native
	// dependencies reachable from each cached API, before client-selected relations.
	ExecutionCacheDependencies map[*ast.ApiDecl][]CacheDependency
	// CachePolicies contains value snapshots, never runtime policy-name lookups.
	CachePolicies map[*ast.Directive]cachepolicy.Policy
	valueTypes    *valueTypeCompilation
}

// Options binds source-controlled contracts to one isolated compilation.
type Options struct {
	CachePolicies   *cachepolicy.Catalog
	ModuleIsolation bool
}

// Analyzer owns the mutable state of one semantic compilation.
// Use Analyze or AnalyzeModules for independent concurrent compilations.
type Analyzer struct {
	defaultAuthSubject         string
	authIdentities             map[string]*ResolvedType
	scope                      *Scope
	types                      map[string]*ResolvedType
	typeParameters             []string // Visible only while resolving one generic declaration.
	valueTypes                 *genericAnalysis
	rejectedTypeDeclarations   map[typeDeclarationKey]bool // Allocated only for rejected source declarations.
	functions                  map[string]*ast.FnDecl
	apiBodyReturns             map[*ast.ApiDecl]*ResolvedType
	events                     map[string]*ast.EventDecl
	errors                     []Error
	errorConstructors          map[string]*ast.ErrorDecl
	warnings                   []Warning
	inLambda                   bool // Database-query restricted collection or predicate context.
	inTransaction              bool // true when checking inside a transaction block
	inAwait                    bool // true when checking inside an await block
	typePassDone               bool
	loopDepth                  int      // Explicit loops reachable in the current execution scope.
	callTarget                 ast.Expr // exact callee being checked, not its nested receivers
	resultOperand              int      // positive while checking the direct operand of Result<T>?
	expectedReturn             *ResolvedType
	asyncResult                *ResolvedType // Read-only Void metadata, allocated once per analysis.
	files                      []*ast.File
	currentFile                *ast.File // file being analyzed in Pass 4
	executionCacheDependencies map[*ast.ApiDecl][]CacheDependency
	cacheModelReads            map[ast.Expr]CacheDependency
	cachePolicies              *cachepolicy.Catalog
	resolvedCachePolicies      map[*ast.Directive]cachepolicy.Policy
	genericExprTypes           map[ast.Expr]*ResolvedType
	integerConstants           map[ast.Expr]integerConstant

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
	return AnalyzeWithOptions(files, Options{})
}

// AnalyzeModules creates an isolated semantic context with module boundaries.
func AnalyzeModules(files []*ast.File) *Result {
	return AnalyzeWithOptions(files, Options{ModuleIsolation: true})
}

// AnalyzeWithOptions resolves policy references once, alongside type checking.
// Each compilation owns its AST (type checking annotates expressions), while
// immutable policy catalogs can be shared. Policy references are not rewritten.
func AnalyzeWithOptions(files []*ast.File, options Options) *Result {
	a := newAnalyzer()
	a.cachePolicies = options.CachePolicies
	if options.ModuleIsolation {
		a.moduleMap = BuildFileModuleMap(files)
	}
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
	a.integerConstants = nil
	a.runDeclarationPass(files)
	a.runTypePass(files)
	a.runBodyPass(files)
	a.runPostAnalysisPass(files)
	if len(a.errors) == 0 {
		files = inferred.ExpandedFiles(files)
		a.validateAllRelationKeys(files)
		for _, file := range files {
			for _, model := range file.Models {
				a.validateInheritedStoredFields(model)
				if len(model.Parents) > 0 {
					a.validatePrimaryKeyFields(model)
				}
				if field := inferred.InvalidSoftDeleteTimestamp(model); field != nil {
					a.addError(field.Pos, "soft-delete field '%s.deletedAt' must be a persisted DateTime? scalar / 软删除字段 '%s.deletedAt' 必须是持久化的 DateTime? 标量", model.Name, model.Name)
				}
			}
		}
	}

	return &Result{
		Scope:                      a.scope,
		Types:                      a.types,
		Files:                      files,
		Errors:                     a.errors,
		Warnings:                   a.warnings,
		GenericExprTypes:           a.genericExprTypes,
		ExecutionCacheDependencies: a.executionCacheDependencies,
		CachePolicies:              a.resolvedCachePolicies,
		valueTypes:                 a.valueTypeCompilation(),
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
	a.checkInheritanceCycles(files)
	for _, file := range files {
		a.resolveFields(file)
	}
	a.resolveGenericInstanceFields()
	a.validateComputedFields(files)
	a.validateInferredParameters(files)
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
	a.selectDefaultIdentity(files)
	if id := a.types["Identity"].Fields["id"]; id.Type == nil {
		id.Type = a.types["Int"]
	}
	// A declared stores field takes precedence; otherwise roles use JWT strings.
	if role := a.types["Identity"].Fields["role"]; role.Type == nil {
		role.Type = a.types["String"]
	}
	// Cache effects cannot publish dependencies after declaration/type errors.
	// Still check every body below so independent diagnostics remain available.
	if len(a.errors) == 0 && hasCachedAPI(files) {
		a.cacheModelReads = make(map[ast.Expr]CacheDependency)
	}
	// Common declarations are service source dependencies, independent of the
	// directory listing order. Retain the caller's file and local declaration order.
	for _, file := range files {
		if len(file.Globals) == 0 {
			continue
		}
		if module := a.moduleMap[file.Name]; module != nil && module.IsCommon {
			a.currentFile = file
			a.checkGlobalDeclarations(file)
		}
	}
	for _, file := range files {
		a.currentFile = file
		if module := a.moduleMap[file.Name]; module == nil || !module.IsCommon {
			a.checkGlobalDeclarations(file)
		}
		a.checkBodies(file)
		a.checkFieldConditions(file)
	}
	a.currentFile = nil
}

func (a *Analyzer) runPostAnalysisPass(files []*ast.File) {
	// Pass 4: validate event cycles and annotate query plans.
	a.validateEventSnapshots(files)
	a.checkEventCycles()
	for _, file := range files {
		a.analyzeQueries(file)
	}
	a.validateCacheableDeclarations(files)
	a.validateCacheEffects(files)
	a.validatePaginationPlans(files)
}
