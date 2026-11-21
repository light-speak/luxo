package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenericValueFieldsUseConcreteArguments(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"scalar", `type Box<T> { value: T } fn read(box: Box<Int>): Int { return box.value }`},
		{"nullable", `type Box<T> { value: T } fn read(box: Box<String?>): String? { return box.value }`},
		{"object", `type Box<T> { value: T } type Item { id: Int } fn read(box: Box<Item>): Int { return box.value.id }`},
		{"nested", `type Box<T> { value: T } type Wrap<U> { box: Box<U> } fn read(value: Wrap<String>): String { return value.box.value }`},
		{"forward", `type Wrap<U> { box: Box<U> } type Box<T> { value: T } fn read(value: Wrap<Int>): Int { return value.box.value }`},
		{"page", `type Item { id: Int } fn read(value: Page<Item>): [Item] { return value.items }`},
		{"cursor", `type Item { id: Int } fn read(value: Cursor<Item>): [Item] { return value.items }`},
		{"list_argument", `type Box<T> { value: T } fn read(box: Box<[Int]>): [Int] { return box.value }`},
		{"recursive", `type Node<T> { value: T next: Node<T>? } fn read(node: Node<Int>): Int { return node.next?.value ?: 0 }`},
		{"independent", `type Box<T> { value: T } fn number(box: Box<Int>): Int { return box.value } fn text(box: Box<String>): String { return box.value }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			expectNoErrors(t, analyze(t, sample.source))
		})
	}
}

func TestGenericInstantiationPreservesTemplateAndListBoundaries(t *testing.T) {
	result := analyze(t, `type Box<T> { value: T } fn read(value: Box<Int>): Int { return value.value }`)
	expectNoErrors(t, result)
	if field := result.Types["Box"].Fields["value"]; field.Type.Name != "T" || field.Type.Kind != TypeGeneric {
		t.Fatalf("instantiation mutated the generic declaration: %#v", field.Type)
	}
	for _, reference := range []string{"Bucket<[Int]>", "Bucket<String?>", "Page<[Int]>", "Cursor<String?>"} {
		result := analyze(t, `type Bucket<T> { items: [T] } fn read(value: `+reference+`): Int { return 1 }`)
		expectError(t, result, "list elements")
	}
}

func TestGenericValueArgumentsRequireConcreteDataRepresentation(t *testing.T) {
	for _, source := range []string{
		`type Box<T> { value: T } fn read(value: Box<Channel<Int>>): Int { return 1 }`,
		`type Box<T> { value: T } fn read(value: Box<[Channel<Int>]>): Int { return 1 }`,
		`type Box<T> { value: T } fn read(value: Box<Page<Channel<Int>>>): Int { return 1 }`,
		`type Box<T> { value: T } interface Contract { id: Int } fn read(value: Box<Contract>): Int { return 1 }`,
		`type Box<T> { value: T } fn read(value: Box<(Int, String)>): Int { return 1 }`,
	} {
		expectError(t, analyze(t, source), "concrete data representation")
	}
	expectNoErrors(t, analyze(t, `type Wrap<T> { value: T } type Box<U> { value: Wrap<U> } fn read(value: Box<Int>): Int { return value.value.value }`))
}

func TestExpandingGenericRecursionReturnsDiagnostic(t *testing.T) {
	result := analyze(t, `type Grow<T> { next: Grow<Grow<T>>? } fn read(value: Grow<Int>): Int { return 1 }`)
	expectError(t, result, "generic instantiation expands")
}

func TestGenericCompilationKeepsSourceTypeReferences(t *testing.T) {
	result := analyze(t, `type Box<T> { value: T } fn read(value: Box<Int>): Int { return value.value }`)
	expectNoErrors(t, result)
	ref := result.Files[0].Functions[0].Params[0].Type
	if ref.Name != "Box" || len(ref.TypeArgs) != 1 || ref.TypeArgs[0].Name != "Int" {
		t.Fatalf("source type reference changed: %#v", ref)
	}
	if result.valueTypes == nil || result.valueTypes.templates["Box"] != result.Files[0].Types[0] {
		t.Fatal("generic declaration identity was lost")
	}
	resolved := result.valueTypes.references[ref]
	if resolved == nil || resolved.Fields["value"].Type.Name != "Int" {
		t.Fatalf("missing concrete source-reference metadata: %#v", resolved)
	}
	if ordinary := analyze(t, `fn read(value: Int): Int { return value }`); ordinary.valueTypes != nil {
		t.Fatal("ordinary compilation allocated a generic compilation plan")
	}
}

func TestGenericValueArgumentCountsAreChecked(t *testing.T) {
	for _, reference := range []string{"Box", "Box<Int, String>", "Page", "Page<Int, String>", "Cursor", "Cursor<Int, String>", "Plain<Int>"} {
		t.Run(reference, func(t *testing.T) {
			result := analyze(t, `type Box<T> { value: T } type Plain { value: Int } fn read(value: `+reference+`): Int { return 1 }`)
			expectError(t, result, "type argument")
		})
	}
}

func TestChannelCannotCrossWireOrStorageBoundaries(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"model", `model Item @noTime { id: Int @id value: Channel<Int> }`},
		{"field_default", `model Item @noTime { id: Int @id value: Channel<Int> = null }`},
		{"type", `type Packet { value: Channel<Int> }`},
		{"nested_type", `type Packet { value: [Channel<Int>] }`},
		{"interface", `interface Packet { value: Channel<Int> }`},
		{"extend", `model Item @noTime { id: Int @id } extend Item { value: Channel<Int> }`},
		{"api_argument", `api readValue(value: Channel<Int>): Int { return 1 }`},
		{"api_return", `api readValue(): Channel<Int> @native`},
		{"stream", `api readValue(): Channel<Int> @stream @native`},
		{"service_argument", `fn readValue(value: Channel<Int>): Int @service @native`},
		{"service_return", `fn readValue(): Channel<Int> @service @native`},
		{"service_result", `fn readValue(): Result<Channel<Int>> @service @native`},
		{"service_nested_result", `fn readValue(): Result<[Channel<Int>]> @service @native`},
		{"tuple", `type Packet { value: (Int, Channel<Int>) }`},
		{"error", `error Rejected(value: Channel<Int>) { code: 400 message: "rejected" }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			expectError(t, analyze(t, sample.source), "Channel<T> is only valid in local functions and variables")
		})
	}
}

func TestChannelEventPayloadRemainsRejected(t *testing.T) {
	expectError(t, analyze(t, `event Ready(value: Channel<Int>)`), "unsupported wire type")
}

func TestChannelKeepsLocalFunctionAndAPIImplementationUsage(t *testing.T) {
	for _, source := range []string{
		`fn source(): Channel<Int> { val ch: Channel<Int> = Channel(1) return ch }`,
		`fn source(): Result<Channel<Int>> @native`,
		`fn readValue(ch: Channel<Int>): Int { return <-ch }`,
		`fn sendValue(ch: Channel<Int>): Result<Int> @native`,
		`api readValue(): Int { val ch: Channel<Int> = Channel(1) ch <- 42 return <-ch }`,
		`fn readValue(): Int @service { val ch: Channel<Int> = Channel(1) ch <- 42 return <-ch }`,
	} {
		expectNoErrors(t, analyze(t, source))
	}
}

func TestNonGenericTypesRejectIgnoredArguments(t *testing.T) {
	for _, name := range []string{"Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "State", "Item", "Abstract"} {
		t.Run(name, func(t *testing.T) {
			const declarations = `enum State { OPEN CLOSED } model Item @noTime { id: Int @id } interface Abstract { id: Int }`
			expectError(t, analyze(t, declarations+` type Packet { value: `+name+`<String> }`), "does not accept type arguments")
		})
	}
}

func TestSealedPayloadTypeBoundaries(t *testing.T) {
	for _, sample := range []struct{ kind, diagnostic string }{
		{"Channel<Int>", "Channel<T> is only valid"},
		{"[Channel<Int>]", "Channel<T> is only valid"},
		{"(Int, Channel<Int>)", "Channel<T> is only valid"},
		{"Result<Int>", "Result"},
		{"Int<String>", "does not accept type arguments"},
		{"String<Int>", "does not accept type arguments"},
		{"State<Int>", "does not accept type arguments"},
		{"Item<Int>", "does not accept type arguments"},
		{"Abstract<Int>", "does not accept type arguments"},
		{"MissingPayload", "unknown type"},
	} {
		t.Run(sample.kind, func(t *testing.T) {
			source := `enum State { READY } model Item @noTime { id: Int @id }
interface Abstract { id: Int }
sealed Outcome { Value(value: ` + sample.kind + `) }`
			expectError(t, analyze(t, source), sample.diagnostic)
		})
	}
}

func TestSealedPayloadRequiresConcreteDataRepresentation(t *testing.T) {
	for _, reference := range []string{"Abstract", "Abstract?", "[Abstract]", "[Abstract]?", "(Int, String)", "(Abstract, Int)"} {
		t.Run(reference, func(t *testing.T) {
			source := "interface Abstract { id: Int }\nsealed Outcome {\n Value(value: " + reference + ")\n Empty\n}"
			result := analyze(t, source)
			expectError(t, result, "sealed payload field 'value' requires a concrete data representation")
			if len(result.Errors) != 1 || result.Errors[0].Pos.Line != 3 {
				t.Fatalf("expected one diagnostic at the payload field, got %v", result.Errors)
			}
		})
	}
}

func TestSealedPayloadBoundaryPreservesConcreteAndLocalTypes(t *testing.T) {
	result := analyze(t, `interface Identified { id: Int }
model Item : Identified @noTime { id: Int @id }
enum State { READY }
type Box<T> { value: T }
sealed Outcome {
 Value(owner: Item?, state: State, data: JSON, raw: Bytes, values: [Int]?, box: Box<String>, next: Outcome?)
 Empty
}
fn unpack(): Int { val (number, text) = await { 1 "two" } return number }
fn read(item: Item): Int { return item.id }`)
	expectNoErrors(t, result)
	if result.Types["Item"].Parents[0] != result.Types["Identified"] || result.Types["Identified"].Kind != TypeInterface {
		t.Fatal("sealed payload validation changed interface inheritance")
	}
	if len(result.Types["Outcome"].Variants[0].Fields) != 7 {
		t.Fatal("sealed payload validation lost concrete fields")
	}
}

func TestSealedPayloadParameterRules(t *testing.T) {
	for _, sample := range []struct{ source, diagnostic string }{
		{`sealed Outcome { Same(value: Int) Same(other: String) }`, "duplicate sealed variant"},
		{`sealed Outcome { Value(value: Int, value: String) }`, "duplicate parameter"},
		{`sealed Outcome { Value(value: Int = "bad") }`, "expects 'Int'"},
		{`fn source(): Int { return 1 } sealed Outcome { Value(value: Int = source()) }`, "compile-time constant"},
	} {
		expectError(t, analyze(t, sample.source), sample.diagnostic)
	}
}

func TestSealedFieldMetadata(t *testing.T) {
	result := analyze(t, `sealed Outcome {
 Empty
	Value(count: Int = 1, text: String? = null, owner: Later)
}
type Later { id: Int }`)
	expectNoErrors(t, result)
	fields := result.Types["Outcome"].Variants[1].Fields
	for index, name := range []string{"Int", "String", "Later"} {
		field := fields[index]
		if field.Type == nil || field.Type.Name != name {
			t.Errorf("field %s lost its resolved type %s: %+v", field.Name, name, field.Type)
		}
		if field.Pos.Line == 0 || field.Pos.File == "" {
			t.Errorf("field %s lost its source location", field.Name)
		}
	}
	if fields[1].Type != nil && (!fields[1].Type.Nullable || !fields[1].Nullable) {
		t.Error("nullable variant field became required")
	}
	if !fields[0].HasDefault || !fields[1].HasDefault || fields[2].HasDefault {
		t.Error("variant field default metadata does not match the declaration")
	}
}

func TestSealedRecursiveAndListFields(t *testing.T) {
	result := analyze(t, `sealed Tree { Empty Node(value: Int, children: [Tree], parent: Tree?) }`)
	expectNoErrors(t, result)
	fields := result.Types["Tree"].Variants[1].Fields
	if !fields[1].Type.IsList || fields[1].Type.Name != "Tree" || !fields[2].Nullable {
		t.Fatalf("recursive payload metadata changed: %+v", fields)
	}
}

func TestSealedDuplicateDeclarationKeepsAcceptedPayload(t *testing.T) {
	for _, samePosition := range []bool{false, true} {
		first := parseFileWithName(t, `sealed Outcome { Value(first: Int, second: String) }`, "first.luxo")
		duplicate := parseFileWithName(t, `sealed Outcome { Value(ignored: Boolean) }`, "duplicate.luxo")
		if samePosition {
			duplicate.Sealeds[0].Pos = first.Sealeds[0].Pos
		}
		result := Analyze([]*ast.File{first, duplicate})
		expectError(t, result, "already declared")
		fields := result.Types["Outcome"].Variants[0].Fields
		if len(fields) != 2 || fields[0].Name != "first" || fields[0].Type.Name != "Int" || fields[1].Type.Name != "String" {
			t.Fatalf("duplicate declaration replaced the accepted payload: %+v", fields)
		}
	}
}

func TestSealedResolverSkipsUnownedDeclarations(t *testing.T) {
	position := token.Position{File: "source.luxo", Line: 1}
	for _, typ := range []*ResolvedType{
		nil,
		{Kind: TypeCustom, Pos: position},
		{Kind: TypeSealed},
		{Kind: TypeSealed, Pos: position, Variants: []*SealedVariantInfo{{Name: "Fragment"}}},
	} {
		a := New()
		a.types["Outcome"] = typ
		a.resolveSealedFields(&ast.File{Sealeds: []*ast.SealedDecl{{Name: "Outcome", Pos: position}}})
		if len(a.errors) != 0 {
			t.Fatal(a.errors)
		}
	}
}

func TestGenericParametersCannotEscapeTheirDeclaration(t *testing.T) {
	for _, source := range []string{
		`model Stray @noTime { id: Int @id value: T }`,
		`type Stray { value: T }`,
		`type Other<U> { value: T }`,
		`interface Stray { value: T }`,
		`sealed Stray { Value(value: T) }`,
		`event Stray(value: T)`,
		`error Stray(value: T) { code: 400 message: "invalid" }`,
		`fn stray(value: T): Int { return 1 }`,
		`api stray(value: T): Int { return 1 }`,
		`fn stray(): Int { val value: T = 1 return 1 }`,
	} {
		expectError(t, analyze(t, `type Box<T> { value: T } `+source), "unknown type 'T'")
	}
}

func TestGenericParametersResolveInsideTheirDeclaration(t *testing.T) {
	result := analyze(t, `type Box<T> { value: T optional: T? many: [T] }
type Other<U> { value: U nested: Box<U> }`)
	expectNoErrors(t, result)
	if result.Types["T"] != nil || result.Types["U"] != nil {
		t.Fatal("generic parameters escaped into the global type catalog")
	}
	box := result.Types["Box"]
	for _, name := range []string{"value", "optional", "many"} {
		if typ := box.Fields[name].Type; typ.Kind != TypeGeneric || typ.Name != "T" {
			t.Fatalf("local generic field %s has type %+v", name, typ)
		}
	}
	if !box.Fields["optional"].Nullable || !box.Fields["many"].Type.IsList {
		t.Fatal("generic parameter modifiers were lost")
	}
}

func TestGenericParameterShadowsOnlyItsOwnTypeReferences(t *testing.T) {
	result := analyze(t, `type Box<Int> { value: Int } fn number(): Int { return 42 }`)
	expectNoErrors(t, result)
	if result.Types["Box"].Fields["value"].Type.Kind != TypeGeneric || result.Types["Int"].Kind != TypeInt {
		t.Fatal("local parameter shadowing changed a built-in type")
	}
	if result.Scope.Lookup("number").Type.Kind != TypeInt {
		t.Fatal("generic parameter remained visible to an unrelated function")
	}
}

func TestGenericParameterDeclarationsRejectDuplicateNames(t *testing.T) {
	expectError(t, analyze(t, `type Box<T, T> { value: T }`), "duplicate type parameter")
	expectError(t, analyze(t, `type Box<T> { value: T<Int> }`), "type parameter 'T' does not accept type arguments")
}

func TestGenericParameterDoesNotReserveGlobalTypeName(t *testing.T) {
	box := parseFileWithName(t, `type Box<T> { value: T }`, "origin/box.luxo")
	concrete := parseFileWithName(t, `type T { id: Int }`, "origin/concrete.luxo")
	for _, files := range [][]*ast.File{{box, concrete}, {concrete, box}} {
		result := AnalyzeModules(files)
		expectNoErrors(t, result)
		if result.Types["T"].Kind != TypeCustom || result.Types["Box"].Fields["value"].Type.Kind != TypeGeneric {
			t.Fatal("global type and local parameter became the same declaration")
		}
	}
}

func TestGenericParameterVisibilityKeepsNestedConcreteChecks(t *testing.T) {
	for _, local := range []string{"Int", "Result", "Channel"} {
		result := analyze(t, "type Box<"+local+"> { value: "+local+" }")
		expectNoErrors(t, result)
		if result.Types["Box"].Fields["value"].Type.Kind != TypeGeneric {
			t.Fatal("local type parameter was resolved as a built-in type", local)
		}
	}
	for _, source := range []string{
		`type Box<T> { value: (T, Hidden) }`,
		`type Box<T> { value: [Hidden<T>] }`,
	} {
		box := parseFileWithName(t, source, "origin/box.luxo")
		hidden := parseFileWithName(t, `type Hidden<U> { value: U }`, "origin/hidden.luxo")
		expectError(t, AnalyzeModules([]*ast.File{box, hidden}), "type 'Hidden' is from module 'hidden'")
	}
}

func BenchmarkAnalyzeLocalChannelBoundaries(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		for index := range count {
			fmt.Fprintf(&source, "fn relay%d(ch: Channel<Int>, value: Int): Int { ch <- value return <-ch }\n", index)
		}
		file := parseFile(b, source.String())
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeGenericParameterScopes(b *testing.B) {
	for _, kind := range []string{"ordinary", "generic", "modules"} {
		for _, count := range []int{1, 128} {
			var source strings.Builder
			for index := range count {
				if kind == "ordinary" {
					fmt.Fprintf(&source, "type Packet%d { value: Int values: [String] }\n", index)
				} else {
					fmt.Fprintf(&source, "type Box%d<T> { value: T values: [T] }\n", index)
				}
			}
			file := parseFile(b, source.String())
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var result *Result
					if kind == "modules" {
						result = AnalyzeModules([]*ast.File{file})
					} else {
						result = Analyze([]*ast.File{file})
					}
					if len(result.Errors) != 0 {
						b.Fatal(result.Errors)
					}
				}
			})
		}
	}
}
