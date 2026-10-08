package semantic

import "testing"

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
				expectError(t, result, "must be called directly")
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
