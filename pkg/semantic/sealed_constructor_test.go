package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

const sealedConstructorDeclaration = `sealed Outcome {
 Success(value: Int, note: String = "ok")
 Failure(reason: String)
 Pending
}`

func TestSealedConstructorsUseDeclaredPayloads(t *testing.T) {
	for _, expression := range []string{
		`Outcome.Success(7)`, `Outcome.Success(note: "named", value: 7)`,
		`Outcome.Failure(reason: "failed")`, `Outcome.Pending()`,
	} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, sealedConstructorDeclaration+"\napi probe(): Outcome { return "+expression+" }")
			if len(result.Errors) != 0 {
				t.Fatal(result.Errors)
			}
			call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			member := call.Func.(*ast.MemberExpr)
			if !member.SealedConstructor || call.ConstructorType == nil || call.ConstructorType.Name != "Outcome" || call.GetTypeTag() != "Outcome" {
				t.Fatal("constructor lost its concrete sealed owner")
			}
		})
	}
}

func TestSealedConstructorsValidateArguments(t *testing.T) {
	for _, sample := range []struct{ expression, error string }{
		{`Outcome.Missing()`, "unknown sealed variant"},
		{`Outcome.Success()`, "missing required argument"},
		{`Outcome.Success(other: 7)`, "unknown argument"},
		{`Outcome.Success(value: "wrong")`, "expects 'Int'"},
		{`Outcome.Success(value: null)`, "expects 'Int'"},
		{`Outcome.Success(value: 1, value: 2)`, "duplicate argument"},
		{`Outcome.Success(value: 1, "after")`, "positional argument cannot follow"},
	} {
		t.Run(sample.expression, func(t *testing.T) {
			result := analyze(t, sealedConstructorDeclaration+"\napi probe(): Outcome { return "+sample.expression+" }")
			expectError(t, result, sample.error)
		})
	}
}

func TestSealedPayloadUsesStaticListInsteadOfVariadicParameters(t *testing.T) {
	expectError(t, analyze(t, `sealed Outcome { Values(...items: Int) }`), "cannot be variadic")
	expectNoErrors(t, analyze(t, `sealed Outcome { Values(items: [Int]) }
api echo(items: [Int]): Outcome { return Outcome.Values(items) }
`))
}

func TestSealedCompilationNormalizesLargePayloads(t *testing.T) {
	var fields, values strings.Builder
	for index := range 17 {
		if index != 0 {
			fields.WriteString(", ")
			values.WriteString(", ")
		}
		fmt.Fprintf(&fields, "field%d: Int", index)
		fmt.Fprintf(&values, "%d", index)
	}
	source := analyze(t, "sealed Outcome { Value("+fields.String()+") } api create(): Outcome { return Outcome.Value("+values.String()+") }")
	expectNoErrors(t, source)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	original := source.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	compiled := view.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if len(compiled.Args) != 17 {
		t.Fatal("large payload lost arguments")
	}
	for index, argument := range compiled.Args {
		if argument.Name != fmt.Sprintf("field%d", index) || argument.ExpectedType == nil || argument.ExpectedType.Name != "Int" || original.Args[index].Name != "" || original.Args[index].ExpectedType != nil {
			t.Fatal("large payload normalization changed source or lost a field", index)
		}
	}
}

func TestSealedConstructorHonorsModuleVisibility(t *testing.T) {
	for _, owner := range []string{"common", "private"} {
		t.Run(owner, func(t *testing.T) {
			declaration := parseFileWithName(t, `sealed Outcome { Pending }`, "origin/"+owner+"/outcome.luxo")
			caller := parseFileWithName(t, `api create(): Outcome { return Outcome.Pending() }`, "origin/consumer/use.luxo")
			result := AnalyzeModules([]*ast.File{declaration, caller})
			if owner == "common" {
				expectNoErrors(t, result)
			} else {
				expectError(t, result, "from module 'private'")
			}
		})
	}
}

func TestSealedListWhenRejectsVariantPatterns(t *testing.T) {
	result := analyze(t, `sealed Outcome { Pending }
api inspect(values: [Outcome]): Int { return when(values) { is Pending -> 1 } }
`)
	expectError(t, result, "sealed variant patterns require a scalar subject")
	expectError(t, result, "when expression must have 'else' branch")
}

func TestSealedConstructorRespectsLexicalBindings(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
fn identity(Outcome: Int): Int { return Outcome }
api probe(Outcome: Int): Int { return identity(Outcome) }
`)
	expectNoErrors(t, result)
	result = analyze(t, sealedConstructorDeclaration+`
api probe(Outcome: Outcome): Outcome { return Outcome.Success(7) }
`)
	expectError(t, result, "has no field")
}

func TestSealedDeclarationDoesNotReplaceSameNameFunction(t *testing.T) {
	for _, source := range []string{
		`fn Outcome(value: Int): Int { return value }
api probe(): Int { return Outcome(7) }`,
		`fn Outcome(value: Outcome): Outcome { return value }
api probe(input: Outcome): Outcome { return Outcome(input) }`,
	} {
		expectNoErrors(t, analyze(t, sealedConstructorDeclaration+"\n"+source))
	}
}

func TestSealedFunctionShadowCannotActAsVariantNamespace(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
fn Outcome(value: Outcome): Outcome { return value }
api probe(): Outcome { return Outcome.Success(7) }
`)
	expectError(t, result, "must be called directly")
}

func TestSealedCompilationNamesOnlyClonedPositionalArguments(t *testing.T) {
	for _, sample := range []struct {
		expression string
		want       []string
	}{
		{`Outcome.Success(7, "positional")`, []string{"value", "note"}},
		{`Outcome.Success(7, note: "mixed")`, []string{"value", "note"}},
		{`Outcome.Success(7)`, []string{"value"}},
		{`Outcome.Success(note: "named", value: 7)`, []string{"note", "value"}},
		{`Outcome.Pending()`, nil},
	} {
		t.Run(sample.expression, func(t *testing.T) {
			source := analyze(t, sealedConstructorDeclaration+"\napi probe(): Outcome { return "+sample.expression+" }")
			expectNoErrors(t, source)
			original := source.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			names := make([]string, len(original.Args))
			expected := make([]*ast.TypeRef, len(original.Args))
			for index, argument := range original.Args {
				names[index], expected[index] = argument.Name, argument.ExpectedType
			}
			view, err := CompilationView(source)
			if err != nil {
				t.Fatal(err)
			}
			cloned := view.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			if cloned == original || len(cloned.Args) != len(sample.want) {
				t.Fatal("compilation must own its constructor without materializing omitted defaults")
			}
			for index, argument := range cloned.Args {
				if argument.Name != sample.want[index] || argument.ExpectedType == nil || argument.ExpectedType.Name != []string{"Int", "String"}[sealedArgumentTestPosition(argument.Name)] {
					t.Fatalf("argument %d lost its declared payload binding: %#v", index, argument)
				}
				if original.Args[index].Name != names[index] || original.Args[index].ExpectedType != expected[index] {
					t.Fatal("compilation modified the editor argument metadata")
				}
			}
		})
	}
}

func TestSealedCompilationPreservesGenericPayloadArgumentType(t *testing.T) {
	source := analyze(t, `type Box<T> { value: T }
sealed Outcome { Value(box: Box<Int>) }
api probe(): Outcome { return Outcome.Value(Box<Int>(value: 7)) }
`)
	expectNoErrors(t, source)
	original := source.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	cloned := view.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	argument := cloned.Args[0]
	if argument.Name != "box" || argument.ExpectedType.Name == "Box" || len(argument.ExpectedType.TypeArgs) != 0 {
		t.Fatalf("compiled payload argument did not retain its concrete type: %#v", argument)
	}
	if original.Args[0].Name != "" || original.Args[0].ExpectedType != nil {
		t.Fatal("generic payload normalization changed the source argument")
	}
}

func sealedArgumentTestPosition(name string) int {
	if name == "value" {
		return 0
	}
	return 1
}

func TestSealedConstructorReanalysisClearsMetadata(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+"\napi probe(): Outcome { return Outcome.Success(7) }")
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	member := call.Func.(*ast.MemberExpr)
	member.Field = "Missing"
	result = Analyze(result.Files)
	expectError(t, result, "unknown sealed variant")
	if member.SealedConstructor || call.ConstructorType != nil {
		t.Fatal("rejected edited source retained constructor authority")
	}
}

func TestSealedWhenRejectsUnknownVariant(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
api probe(value: Outcome): Int {
 return when(value) { is Missing -> 1 else -> 0 }
}
`)
	expectError(t, result, "unknown sealed variant")
}

func TestNullableSealedWhenRequiresNullCoverage(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
api probe(value: Outcome?): Int {
 return when(value) { is Success -> 1 is Failure -> 2 is Pending -> 3 }
}
`)
	expectError(t, result, "null")
}

func TestSealedWhenPreservesTheWholeSubject(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
fn identity(value: Outcome): Outcome {
 return when(value) { is Success -> value is Failure -> value is Pending -> value }
}

`)
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	when := result.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.WhenExpr)
	for _, branch := range when.Branches {
		if branch.Body.GetTypeTag() != "Outcome" || branch.Body.IsModelType() {
			t.Fatal("sealed narrowing changed the value into a database model")
		}
	}
}

func TestSealedRequiresAVariantConstructor(t *testing.T) {
	for _, expression := range []string{`Outcome()`, `Outcome{}`, `Outcome`} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, sealedConstructorDeclaration+"\napi probe(): Outcome { return "+expression+" }")
			expectError(t, result, "requires a variant constructor")
		})
	}
}

func TestEmptySealedIsUninhabited(t *testing.T) {
	for _, body := range []string{
		`api probe(value: Empty?): Empty? { return value }`,
		`api probe(values: [Empty]): [Empty] { return values }`,
		`api probe(): [Empty] { return [] }`,
		`api probe(): Empty? { return null }`,
	} {
		t.Run(body, func(t *testing.T) {
			expectNoErrors(t, analyze(t, "sealed Empty {}\n"+body))
		})
	}
	for _, expression := range []string{`Empty()`, `Empty{}`, `Empty`, `Empty.Missing()`} {
		t.Run(expression, func(t *testing.T) {
			result := analyze(t, "sealed Empty {}\napi probe(): Empty { return "+expression+" }")
			if len(result.Errors) == 0 {
				t.Fatal("an uninhabited sealed declaration acquired a value")
			}
		})
	}
}

func TestEmptySealedWhenRequiresAnInferredValueType(t *testing.T) {
	result := analyze(t, `sealed Empty {}
api probe(value: Empty): Int { return when(value) {} }
`)
	expectError(t, result, "cannot infer the result type")
}

func TestSealedCaseNarrowingIsInvalidatedByReassignment(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
api probe(value: Outcome): Int {
 var current = value
 return when(current) {
  is Success -> {
   current = Outcome.Pending()
   current.value
  }
  is Failure -> 0
  is Pending -> 0
 }
}
`)
	expectError(t, result, "has no field")
}

func TestNullableSealedWhenPreservesNarrowedValueTag(t *testing.T) {
	result := analyze(t, sealedConstructorDeclaration+`
fn probe(value: Outcome?): Outcome {
 return when(value) {
  null -> Outcome.Pending()
  is Success -> value
  is Failure -> value
  is Pending -> value
 }
}
`)
	expectNoErrors(t, result)
	when := result.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.WhenExpr)
	for _, branch := range when.Branches {
		if branch.IsType == "" {
			continue
		}
		value := branch.Body.(*ast.Ident)
		if !value.NarrowedTag || value.IsNullable() || value.IsModelType() || value.GetTypeTag() != "Outcome" {
			t.Fatal("nullable sealed branch lost its original value representation")
		}
	}
}

func TestSealedConstructorRejectsMissingASTValues(t *testing.T) {
	for _, mutate := range []func(*ast.CallExpr){
		func(call *ast.CallExpr) { call.Args = []*ast.NamedArg{nil} },
		func(call *ast.CallExpr) { call.Args[0].Value = nil },
	} {
		result := analyze(t, sealedConstructorDeclaration+`api probe(): Outcome { return Outcome.Success(7) }`)
		expectNoErrors(t, result)
		call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
		mutate(call)
		checked := Analyze(result.Files)
		expectError(t, checked, "constructor argument requires a value")
		if call.ConstructorType != nil || call.Func.(*ast.MemberExpr).SealedConstructor {
			t.Fatal("damaged constructor retained authority")
		}
	}
}

func TestSealedGenericTemplateDefaultRemainsConstant(t *testing.T) {
	source := analyze(t, sealedConstructorDeclaration+`
type Box<T> { value: T outcome: Outcome = Outcome.Success(7) }
api probe(): Box<Int> { return Box<Int>(value: 1) }
`)
	expectError(t, source, "must be a compile-time constant")
}
