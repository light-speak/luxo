package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestImplicitReturnMetadataPreservesTypeChecks(t *testing.T) {
	for _, test := range []struct {
		name, parameter, result string
		valid                   bool
	}{
		{"scalar", "Int", "Int", true},
		{"list", "[Int]", "[Int]", true},
		{"nullable", "Int?", "Int?", true},
		{"nullable_target", "Int", "Int?", true},
		{"nullable_list", "[Int]?", "[Int]?", true},
		{"nullable_source", "Int?", "Int", false},
		{"nullable_list_source", "[Int]?", "[Int]", false},
		{"list_to_scalar", "[Int]", "Int", false},
		{"scalar_to_list", "Int", "[Int]", false},
		{"different_scalar", "String", "Int", false},
		{"different_element", "[String]", "[Int]", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := analyze(t, "fn identity(value: "+test.parameter+"): "+test.result+" { value }")
			if test.valid {
				expectNoErrors(t, result)
			} else {
				expectError(t, result, "return expects")
			}
		})
	}
}

func TestCallableNamesRequireDirectInvocation(t *testing.T) {
	for _, declaration := range []string{
		`fn compute(): Int { return 1 }`,
		`fn compute(): Result<Int> @native`,
		`api compute(): Int { return 1 }`,
	} {
		for _, expression := range []string{
			`val alias = compute`,
			`return compute`,
			`val values = [compute]`,
			`val value = compute.toString()`,
			`accept(compute)`,
		} {
			t.Run(declaration+"/"+expression, func(t *testing.T) {
				result := analyzeCached(t, declaration+"\nfn accept(value: Int) {}\napi test(): Int {\n"+expression+"\nreturn 0\n}")
				message := "must be called directly"
				if strings.HasPrefix(declaration, "api ") {
					message = "cannot be called locally; extract a fn"
				}
				expectError(t, result, message)
			})
		}
	}
}

func TestCallableDirectInvocationAndShadowing(t *testing.T) {
	expectNoErrors(t, analyzeCached(t, `
fn native(value: String): Result<String> @native
fn compute(): String { return " value " }
api test(): Int {
  val result = native(compute())?
  return result.trim().uppercase().length
}

api shadowed(compute: Int): Int { return compute }
api mapped(): [String] { return [" value "].map { it.trim() } }
`))
}

func TestUncalledMethodCannotBecomeAnotherMethodReceiver(t *testing.T) {
	expectError(t, analyzeCached(t, `api test(): String { return " value ".trim.uppercase() }`), "is a method")
}

func TestCachedCallableAliasRejectedBeforeGeneration(t *testing.T) {
	expectError(t, analyzeCached(t, `
model User { id: Int @id }
fn mutate() { User.where(id: 1).delete() }
api cached(): Boolean @cache(policy: "preview") {
  val action = mutate
  action()
  return true
}
`), "must be called directly")
}

func TestAPIsCannotBeCalledLocally(t *testing.T) {
	for _, declaration := range []string{
		`api inner(value: Int = 7): Int { return value }`,
		`api inner(value: Int = 7): Int @native`,
	} {
		for _, caller := range []string{
			`api outer(): Int { return inner() }`,
			`fn outer(): Int { return inner(value: 1) }`,
			`api outer(): Int { val value = await { inner() } return value }`,
			`api outer(): Int { async { inner() } return 1 }`,
			`api outer(): [Int] { return [1].map { inner(it) } }`,
		} {
			t.Run(declaration+"/"+caller, func(t *testing.T) {
				expectError(t, analyze(t, declaration+"\n"+caller), "cannot be called locally; extract a fn")
			})
		}
	}
	expectError(t, analyze(t, `api recursive(): Int { return recursive() }`), "cannot be called locally; extract a fn")
	result := analyze(t, `api inner(value: Int): Int { return value } api outer(): Int { return inner(absent) }`)
	expectError(t, result, "cannot be called locally; extract a fn")
	expectError(t, result, "undefined")
}

func TestLocalAPICallAcrossFiles(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		callee := parseFileWithName(t, `api inner(): Int { return 7 }`, "callee.luxo")
		caller := parseFileWithName(t, `fn outer(): Int { return inner() }`, "caller.luxo")
		files := []*ast.File{callee, caller}
		if reverse {
			files[0], files[1] = files[1], files[0]
		}
		result := Analyze(files)
		if len(result.Errors) != 1 || result.Errors[0].Pos.File != "caller.luxo" ||
			!strings.Contains(result.Errors[0].Message, "cannot be called locally; extract a fn") {
			t.Fatalf("missing local API diagnostic at caller: %v", result.Errors)
		}
	}
}

func TestNonCallableExpressionsRejected(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"literal", `fn probe(): Int { return (1)() }`},
		{"array", `fn probe(): [Int] { return [1]() }`},
		{"binary", `fn probe(): Int { return (1 + 2)() }`},
		{"returned_scalar", `fn scalar(): Int { return 1 } fn probe(): Int { return scalar()() }`},
		{"data_field", `type Box { value: Int } fn probe(box: Box): Int { return box.value() }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, sample.source)
			found := false
			for _, diagnostic := range result.Errors {
				if strings.Contains(diagnostic.Message, "not callable") {
					found = true
					if diagnostic.Pos.File != "test.luxo" || diagnostic.Pos.Line < 1 || diagnostic.Pos.Col < 1 {
						t.Fatalf("call diagnostic lost its location: %+v", diagnostic)
					}
				}
			}
			if !found {
				t.Fatalf("non-callable expression accepted: %v", result.Errors)
			}
		})
	}
}

func TestNonCallableResolvedPropertiesRejected(t *testing.T) {
	for _, source := range []string{
		`enum Status { ACTIVE } fn probe(): Status { return Status.ACTIVE() }`,
		`fn probe(value: Int): Duration { return value.days() }`,
	} {
		result := analyze(t, source)
		expectError(t, result, "not callable")
	}
}

func TestNonCallableTargetPreservesExpressionDiagnostics(t *testing.T) {
	for _, source := range []string{
		`fn probe(): Int { return (missing + 1)() }`,
		`fn probe(): Int { return (1)(missing) }`,
		`type Box { value: Int } fn probe(box: Box): Int { return box.value(missing) }`,
	} {
		result := analyze(t, source)
		expectError(t, result, "not callable")
		expectError(t, result, "undefined")
	}
}

func TestDirectCallFormsPreserveStaticConsumers(t *testing.T) {
	expectNoErrors(t, analyze(t, `
enum Status { ACTIVE }
fn state(): Status { return Status.ACTIVE }
fn duration(value: Int): Duration { return value.days }
fn scalar(): Int { return 7 }
fn direct(): Int { return (scalar)() }
fn member(): String { return (" value ".trim)().uppercase() }
fn properties(value: String): Int {
 if value.isEmpty() { return value.size() }
 return value.length()
}
fn mapped(values: [String]): [String] { return values.map { value -> (value.trim)().uppercase() } }
fn transacted(): Int { transaction { val value = scalar() } return scalar() }
`))
}

func TestPublicFunctionIndexSkipsTypeNamedFunction(t *testing.T) {
	result := AnalyzeModules([]*ast.File{parseFileWithName(t, `type Shared { value: Int }
fn Shared(): Int { return 1 }
fn ordinary(): Int { return 7 }
fn invoke(): Int { return ordinary() }`, "bounded.luxo")})
	expectNoErrors(t, result)
	call := result.Files[0].Functions[2].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if call.GetTypeTag() != "Int" {
		t.Fatalf("independent function call lost its type: %q", call.GetTypeTag())
	}
	if result.Scope.LookupLocal("Shared").Kind != SymType {
		t.Fatal("source type binding was overwritten by an unused same-named function")
	}
}

func TestRejectedAuthModelPreservesSealedEqualityDiagnostics(t *testing.T) {
	files := []*ast.File{
		parseFileWithName(t, `type Subject { id: Int }
sealed Choice { Value(value: Subject) Empty }`, "origin/common/value.luxo"),
		parseFileWithName(t, `model Subject @withAuth(stores: [id]) { id: Int @id }`, "origin/common/rejected-model.luxo"),
		parseFileWithName(t, `fn equal(left: Choice, right: Choice): Boolean { return left == right }`, "origin/common/equality.luxo"),
	}
	result := AnalyzeModules(files)
	expectError(t, result, "already declared")
	found := false
	for _, diagnostic := range result.Errors {
		if strings.Contains(diagnostic.Message, "already declared") && diagnostic.Pos.File == files[1].Name {
			found = true
		}
		if strings.Contains(diagnostic.Message, "does not support equality") {
			t.Fatalf("method metadata hid the original declaration error behind an equality error: %v", result.Errors)
		}
	}
	if !found {
		t.Fatalf("rejected model lost its declaration diagnostic location: %v", result.Errors)
	}
	value := files[2].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value
	if value.GetTypeTag() != "Boolean" {
		t.Fatalf("declaration errors prevented independent body analysis: %q", value.GetTypeTag())
	}
}
