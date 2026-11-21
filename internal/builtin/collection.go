package builtin

// CollectionKind describes the implemented list member contract shared by
// semantic analysis and code generation, not a catalog of planned methods.
type CollectionKind uint8

const (
	NotCollectionMethod CollectionKind = iota
	CollectionProperty
	CollectionLambda
	CollectionJoin
)

// CollectionMethod classifies only members backed by static generated code.
func CollectionMethod(name string) CollectionKind {
	switch name {
	case "size", "isEmpty":
		return CollectionProperty
	case "map", "filter", "forEach", "any", "sortBy", "sumOf":
		return CollectionLambda
	case "joinToString":
		return CollectionJoin
	default:
		return NotCollectionMethod
	}
}
