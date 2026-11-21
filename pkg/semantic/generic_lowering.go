package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
)

type concreteValueType struct {
	key         string
	declaration *ast.TypeDecl
	resolved    *ResolvedType
	owner       int
}

type concreteClone struct {
	nodes     *ast.CloneMap
	arguments map[string]*ast.TypeRef
	owner     int
}

type valueTypeLowering struct {
	source    *Result
	view      *Result
	instances map[string]*concreteValueType
	names     map[string]string
	owners    map[string]int
	files     map[string]int
	clones    []concreteClone
	origins   map[string]string
	uses      map[string]map[string]bool
}

// RequiresTypeCompilation is false for ordinary source and does not allocate.
func (r *Result) RequiresTypeCompilation() bool {
	return r != nil && r.valueTypes != nil
}

// CompilationView materializes static value types without modifying analysis or
// editor nodes. Ordinary compilations reuse their read-only analysis directly.
func CompilationView(source *Result) (*Result, error) {
	if source == nil {
		return nil, fmt.Errorf("type compilation requires a semantic result")
	}
	if len(source.Errors) != 0 {
		return nil, fmt.Errorf("type compilation requires successful semantic analysis: %s", source.Errors[0].Message)
	}
	if source.valueTypes == nil {
		return source, nil
	}
	if source.valueTypes.compiled {
		return projectCompiledValueTypes(source), nil
	}
	files, nodes, err := ast.CloneFiles(withoutGenericTemplates(source.Files))
	if err != nil {
		return nil, fmt.Errorf("clone compilation source: %w", err)
	}
	view := *source
	view.Files, view.Types = files, maps.Clone(source.Types)
	view.GenericExprTypes = nil
	view.CachePolicies, view.ExecutionCacheDependencies = nil, nil
	plan := newValueTypeLowering(source, &view)
	plan.clones = append(plan.clones, concreteClone{nodes: nodes, owner: -1})
	if err := plan.lowerReferences(nodes, nil, -1); err != nil {
		return nil, err
	}
	if err := plan.rebindMetadata(); err != nil {
		return nil, err
	}
	plan.appendDeclarations()
	view.valueTypes = plan.compilationCatalog()
	return &view, nil
}

// ConcreteTypeOrigins returns an independent catalog for stable wire-ID reuse.
func (r *Result) ConcreteTypeOrigins() map[string]string {
	if r == nil || r.valueTypes == nil || !r.valueTypes.compiled {
		return nil
	}
	return maps.Clone(r.valueTypes.origins)
}

// CompiledValueDeclarations returns an independent map of read-only definitions.
func (r *Result) CompiledValueDeclarations() map[string]CompiledValueDeclaration {
	if r == nil || r.valueTypes == nil || !r.valueTypes.compiled {
		return nil
	}
	return maps.Clone(r.valueTypes.declarations)
}

// CompiledSealedDeclarations returns an independent map of read-only sums.
func (r *Result) CompiledSealedDeclarations() map[string]CompiledSealedDeclaration {
	if r == nil || r.valueTypes == nil || !r.valueTypes.compiled {
		return nil
	}
	return maps.Clone(r.valueTypes.sealeds)
}

// CompiledValueEvents retains canonical event types when a service view selects
// only part of the compilation. Returned declarations are read-only.
func (r *Result) CompiledValueEvents() map[string]*ast.EventDecl {
	if r == nil || r.valueTypes == nil || !r.valueTypes.compiled {
		return nil
	}
	return maps.Clone(r.valueTypes.events)
}

func withoutGenericTemplates(files []*ast.File) []*ast.File {
	result := slices.Clone(files)
	for index, file := range files {
		copy := *file
		copy.Types = nil
		for _, declaration := range file.Types {
			if len(declaration.TypeParams) == 0 {
				copy.Types = append(copy.Types, declaration)
			}
		}
		result[index] = &copy
	}
	return result
}

func newValueTypeLowering(source, view *Result) *valueTypeLowering {
	plan := &valueTypeLowering{source: source, view: view,
		instances: make(map[string]*concreteValueType), names: make(map[string]string),
		owners: make(map[string]int), files: make(map[string]int), origins: make(map[string]string), uses: make(map[string]map[string]bool)}
	for index, file := range source.Files {
		plan.files[file.Name] = index
		for _, declaration := range file.Types {
			plan.owners[declaration.Name] = index
		}
		for _, declaration := range file.Models {
			plan.owners[declaration.Name] = index
		}
		for _, declaration := range file.Enums {
			plan.owners[declaration.Name] = index
		}
		for _, declaration := range file.Sealeds {
			plan.owners[declaration.Name] = index
		}
	}
	return plan
}

func (p *valueTypeLowering) lowerReferences(nodes *ast.CloneMap, arguments map[string]*ast.TypeRef, owner int) error {
	if err := normalizeSealedCompilationArguments(p.source, nodes, p.clones[0].nodes); err != nil {
		return err
	}
	var owners map[*ast.TypeRef]int
	if owner < 0 {
		owners = p.argumentOwners(nodes)
	}
	references := make([]*ast.TypeRef, 0, len(nodes.TypeRefs))
	for reference := range nodes.TypeRefs {
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool {
		left, right := references[i].Pos, references[j].Pos
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Offset != right.Offset {
			return left.Offset < right.Offset
		}
		return references[i].Name < references[j].Name
	})
	for _, reference := range references {
		currentOwner := owner
		if currentOwner < 0 {
			currentOwner = p.files[reference.Pos.File]
			if assigned, exists := owners[reference]; exists {
				currentOwner = assigned
			}
		}
		concrete, err := p.lowerReference(reference, arguments, currentOwner)
		if err != nil {
			return err
		}
		*nodes.TypeRefs[reference] = *concrete
	}
	return nil
}

func (p *valueTypeLowering) lowerReference(reference *ast.TypeRef, arguments map[string]*ast.TypeRef, owner int) (*ast.TypeRef, error) {
	logical, err := substituteTypeReference(reference, arguments, 0)
	if err != nil || logical == nil {
		return logical, err
	}
	return p.lowerLogicalReference(logical, owner)
}

func (p *valueTypeLowering) lowerLogicalReference(logical *ast.TypeRef, owner int) (*ast.TypeRef, error) {
	if len(logical.TypeArgs) != 0 && (p.source.valueTypes.templates[logical.Name] != nil || logical.Name == "Page" || logical.Name == "Cursor") {
		instance, err := p.instantiateValueType(logical, owner)
		if err != nil {
			return nil, err
		}
		p.recordUse(owner, instance.declaration.Name)
		copy := *logical
		copy.Name, copy.TypeArgs = instance.declaration.Name, nil
		return &copy, nil
	}
	copy := *logical
	if typ := p.source.Types[logical.Name]; typ != nil && (typ.Kind == TypeCustom || typ.Kind == TypeSealed) {
		p.recordUse(owner, logical.Name)
	}
	var err error
	copy.TypeArgs, err = p.lowerLogicalArguments(logical.TypeArgs, owner)
	if err == nil {
		copy.Tuple, err = p.lowerLogicalArguments(logical.Tuple, owner)
	}
	return &copy, err
}

func (p *valueTypeLowering) lowerLogicalArguments(references []*ast.TypeRef, owner int) ([]*ast.TypeRef, error) {
	if references == nil {
		return nil, nil
	}
	result := make([]*ast.TypeRef, len(references))
	for index, reference := range references {
		concrete, err := p.lowerLogicalReference(reference, owner)
		if err != nil {
			return nil, err
		}
		result[index] = concrete
	}
	return result, nil
}

// Clone-map keys and validated recursive type arguments are non-nil on the normal lowering path.
// Retain the nil-source guard for incomplete metadata.
func substituteTypeReference(source *ast.TypeRef, arguments map[string]*ast.TypeRef, depth int) (*ast.TypeRef, error) {
	if source == nil {
		return nil, nil
	}
	if depth >= 128 {
		return nil, fmt.Errorf("generic type reference is cyclic or exceeds 128 levels")
	}
	if argument := arguments[source.Name]; argument != nil && len(source.TypeArgs) == 0 {
		copy, err := substituteTypeReference(argument, nil, depth+1)
		if err != nil {
			return nil, err
		}
		if source.IsList && (copy.IsList || copy.Nullable || len(copy.Tuple) != 0) {
			return nil, fmt.Errorf("list elements must be non-null, non-list named types after generic substitution")
		}
		copy.IsList, copy.Nullable = copy.IsList || source.IsList, copy.Nullable || source.Nullable
		copy.Pos = source.Pos
		return copy, nil
	}
	copy := *source
	var err error
	copy.TypeArgs, err = substituteTypeArguments(source.TypeArgs, arguments, depth+1)
	if err == nil {
		copy.Tuple, err = substituteTypeArguments(source.Tuple, arguments, depth+1)
	}
	return &copy, err
}

func substituteTypeArguments(source []*ast.TypeRef, arguments map[string]*ast.TypeRef, depth int) ([]*ast.TypeRef, error) {
	if source == nil {
		return nil, nil
	}
	result := make([]*ast.TypeRef, len(source))
	for index, argument := range source {
		if argument == nil {
			return nil, fmt.Errorf("generic type argument requires a type reference")
		}
		value, err := substituteTypeReference(argument, arguments, depth)
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}

func (p *valueTypeLowering) instantiateValueType(reference *ast.TypeRef, owner int) (*concreteValueType, error) {
	signature := *reference
	signature.IsList, signature.Nullable = false, false
	key := formatTypeRef(&signature)
	if instance := p.instances[key]; instance != nil {
		return instance, nil
	}
	template := p.source.valueTypes.templates[reference.Name]
	if template == nil {
		template = builtinValueTemplate(reference.Name)
	}
	if template == nil || len(template.TypeParams) != len(reference.TypeArgs) {
		return nil, fmt.Errorf("invalid generic instantiation %q", key)
	}
	digest := sha256.Sum256([]byte(key))
	name := "Luxo" + reference.Name + "_" + hex.EncodeToString(digest[:8])
	if p.source.Types[name] != nil || p.names[name] != "" && p.names[name] != key {
		return nil, fmt.Errorf("generated generic type name %q conflicts with a declaration", name)
	}
	files, nodes, err := ast.CloneFiles([]*ast.File{{Types: []*ast.TypeDecl{template}}})
	if err != nil {
		return nil, fmt.Errorf("clone generic type %q: %w", key, err)
	}
	declaration := files[0].Types[0]
	declaration.Name, declaration.TypeParams = name, nil
	declarationOwner := p.declarationOwner(reference, owner)
	instance := &concreteValueType{key: key, declaration: declaration, owner: declarationOwner,
		resolved: &ResolvedType{Kind: TypeCustom, Name: name, Fields: make(map[string]*FieldInfo), Pos: reference.Pos}}
	p.instances[key], p.names[name], p.origins[name] = instance, key, reference.Name
	p.view.Types[name] = instance.resolved
	arguments := make(map[string]*ast.TypeRef, len(reference.TypeArgs))
	for index, parameter := range template.TypeParams {
		arguments[parameter] = reference.TypeArgs[index]
	}
	p.clones = append(p.clones, concreteClone{nodes: nodes, arguments: arguments, owner: owner})
	if err := p.lowerReferences(nodes, arguments, owner); err != nil {
		return nil, err
	}
	for _, field := range declaration.Fields {
		if field.Type != nil {
			instance.resolved.Fields[field.Name] = &FieldInfo{Name: field.Name, Type: p.resolvedReference(field.Type),
				Nullable: field.Type.Nullable, HasDefault: field.Default != nil, Pos: field.Pos, Doc: field.Doc}
		}
	}
	return instance, nil
}

// The caller reaches this fallback only for Page or Cursor after checking declared templates.
// Keep the default for inconsistent template metadata.
func builtinValueTemplate(name string) *ast.TypeDecl {
	var fields []*ast.FieldDecl
	items := &ast.FieldDecl{Name: "items", Type: &ast.TypeRef{Name: "T", IsList: true}}
	switch name {
	case "Page":
		fields = []*ast.FieldDecl{items, {Name: "total", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "page", Type: &ast.TypeRef{Name: "Int"}}, {Name: "pageSize", Type: &ast.TypeRef{Name: "Int"}}}
	case "Cursor":
		fields = []*ast.FieldDecl{items, {Name: "nextCursor", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			{Name: "hasMore", Type: &ast.TypeRef{Name: "Boolean"}}}
	default:
		return nil
	}
	return &ast.TypeDecl{Name: name, TypeParams: []string{"T"}, Fields: fields}
}

func (p *valueTypeLowering) resolvedReference(reference *ast.TypeRef) *ResolvedType {
	base := p.view.Types[reference.Name]
	if base != nil && base.Nullable == reference.Nullable && base.IsList == reference.IsList && len(reference.TypeArgs) == 0 && len(reference.Tuple) == 0 {
		return base
	}
	result := &ResolvedType{Kind: TypeUnknown, Name: reference.Name}
	if base != nil {
		*result = *base
	}
	result.Nullable, result.IsList = reference.Nullable, reference.IsList
	result.TypeArgs = nil
	for _, argument := range reference.TypeArgs {
		result.TypeArgs = append(result.TypeArgs, p.resolvedReference(argument))
	}
	result.Tuple = nil
	for _, item := range reference.Tuple {
		result.Kind = TypeTuple
		result.Tuple = append(result.Tuple, p.resolvedReference(item))
	}
	return result
}

func (p *valueTypeLowering) rebindMetadata() error {
	for index := 0; index < len(p.clones); index++ {
		context := p.clones[index]
		if err := p.rebindExpressionTypes(context); err != nil {
			return err
		}
		p.rebindCacheMetadata(context.nodes)
	}
	return nil
}

func (p *valueTypeLowering) rebindExpressionTypes(context concreteClone) error {
	for source, target := range context.nodes.Exprs {
		resolved := p.source.GenericExprTypes[source]
		if resolved == nil {
			if argument := context.arguments[source.GetTypeTag()]; argument != nil {
				resolved = &ResolvedType{Name: source.GetTypeTag(), Nullable: source.IsNullable(), IsList: source.IsListType()}
			} else {
				continue
			}
		}
		owner := context.owner
		if owner < 0 {
			owner = p.files[source.GetPos().File]
		}
		reference, err := p.lowerReference(resolved.TypeRef(), context.arguments, owner)
		if err != nil {
			return err
		}
		concrete := p.resolvedReference(reference)
		target.SetTypeTag(concrete.Name)
		target.SetNullable(concrete.Nullable)
		target.SetListType(concrete.IsList)
		target.SetModelType(concrete.Kind == TypeModel || concrete.Kind == TypeQueryBuilder)
		target.SetEnumType(concrete.Kind == TypeEnum)
		if p.view.GenericExprTypes == nil {
			p.view.GenericExprTypes = make(map[ast.Expr]*ResolvedType)
		}
		p.view.GenericExprTypes[target] = concrete
		if call, ok := target.(*ast.CallExpr); ok && call.ConstructorType != nil && len(call.TypeArgs) != 0 {
			if callee, ok := call.Func.(*ast.Ident); ok {
				callee.Name = concrete.Name
				callee.SetTypeTag(concrete.Name)
			}
			call.TypeArgs = nil
		}
	}
	return nil
}

func (p *valueTypeLowering) rebindCacheMetadata(nodes *ast.CloneMap) {
	for source, target := range nodes.Directives {
		if policy, exists := p.source.CachePolicies[source]; exists {
			if p.view.CachePolicies == nil {
				p.view.CachePolicies = make(map[*ast.Directive]cachepolicy.Policy)
			}
			p.view.CachePolicies[target] = policy
		}
	}
	for source, target := range nodes.APIs {
		if dependencies, exists := p.source.ExecutionCacheDependencies[source]; exists {
			if p.view.ExecutionCacheDependencies == nil {
				p.view.ExecutionCacheDependencies = make(map[*ast.ApiDecl][]CacheDependency)
			}
			p.view.ExecutionCacheDependencies[target] = slices.Clone(dependencies)
		}
	}
}

func (p *valueTypeLowering) appendDeclarations() {
	names := make([]string, 0, len(p.instances))
	for name := range p.instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		instance := p.instances[name]
		p.view.Files[instance.owner].Types = append(p.view.Files[instance.owner].Types, instance.declaration)
	}
}
