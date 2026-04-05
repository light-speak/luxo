package semantic

import "github.com/light-speak/luxo/pkg/token"

// SymbolKind represents the kind of a symbol.
type SymbolKind int

const (
	SymModel SymbolKind = iota
	SymInterface
	SymEnum
	SymSealed
	SymType
	SymApi
	SymFn
	SymError
	SymField
	SymParam
	SymVariable
	SymMiddleware
	SymScope
	SymEvent
)

var symbolKindNames = map[SymbolKind]string{
	SymModel: "model", SymInterface: "interface", SymEnum: "enum",
	SymSealed: "sealed", SymType: "type", SymApi: "api", SymFn: "fn",
	SymError: "error", SymField: "field", SymParam: "param",
	SymVariable: "variable", SymMiddleware: "middleware", SymScope: "scope",
	SymEvent: "event",
}

func (k SymbolKind) String() string {
	if s, ok := symbolKindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Symbol represents a named declaration in the program.
type Symbol struct {
	Name    string
	Kind    SymbolKind
	Type    *ResolvedType
	Pos     token.Position
	Doc     string
	Module  string // which module this belongs to
	Mutable bool   // true for var, false for val
}

// Scope represents a lexical scope with symbols.
type Scope struct {
	parent   *Scope
	symbols  map[string]*Symbol
	narrowed map[string]*ResolvedType // type narrowing: T? → T after ?: check
}

// NewScope creates a new root scope.
func NewScope() *Scope {
	return &Scope{
		symbols: make(map[string]*Symbol),
	}
}

// Child creates a child scope.
func (s *Scope) Child() *Scope {
	return &Scope{
		parent:  s,
		symbols: make(map[string]*Symbol),
	}
}

// Define adds a symbol to this scope. Returns false if already defined.
func (s *Scope) Define(sym *Symbol) bool {
	if _, exists := s.symbols[sym.Name]; exists {
		return false
	}
	s.symbols[sym.Name] = sym
	return true
}

// Lookup looks up a symbol by name, walking up the scope chain.
func (s *Scope) Lookup(name string) *Symbol {
	if sym, ok := s.symbols[name]; ok {
		return sym
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return nil
}

// LookupLocal looks up a symbol in this scope only, not parents.
func (s *Scope) LookupLocal(name string) *Symbol {
	return s.symbols[name]
}

// Narrow records a type narrowing (e.g., T? → T after ?:).
func (s *Scope) Narrow(name string, typ *ResolvedType) {
	if s.narrowed == nil {
		s.narrowed = make(map[string]*ResolvedType)
	}
	s.narrowed[name] = typ
}

// LookupNarrowed returns the narrowed type for a variable, if any.
func (s *Scope) LookupNarrowed(name string) *ResolvedType {
	if s.narrowed != nil {
		if t, ok := s.narrowed[name]; ok {
			return t
		}
	}
	if s.parent != nil {
		return s.parent.LookupNarrowed(name)
	}
	return nil
}

// ResolvedTypeOf returns the effective type of a variable, considering narrowing.
func (s *Scope) ResolvedTypeOf(name string) *ResolvedType {
	if t := s.LookupNarrowed(name); t != nil {
		return t
	}
	if sym := s.Lookup(name); sym != nil {
		return sym.Type
	}
	return nil
}

// AllSymbols returns all symbols in this scope (not parents).
func (s *Scope) AllSymbols() []*Symbol {
	result := make([]*Symbol, 0, len(s.symbols))
	for _, sym := range s.symbols {
		result = append(result, sym)
	}
	return result
}

// LookupPrefix returns all symbols whose name starts with the given prefix.
// Used for auto-completion.
func (s *Scope) LookupPrefix(prefix string) []*Symbol {
	var result []*Symbol
	for name, sym := range s.symbols {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			result = append(result, sym)
		}
	}
	if s.parent != nil {
		result = append(result, s.parent.LookupPrefix(prefix)...)
	}
	return result
}

// FindAllReferences returns all positions where the given symbol name is referenced.
// This is populated by the analyzer during analysis.
type Reference struct {
	Pos  token.Position
	Kind string // "definition", "read", "write"
}
