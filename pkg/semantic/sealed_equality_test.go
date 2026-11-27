package semantic

import "testing"

func TestSealedEqualityUsesSupportedPayloadRules(t *testing.T) {
	for _, payload := range []string{"Int", "Float", "String", "Boolean", "Duration", "UUID", "DateTime?", "Decimal?", "User", "Plain", "Plain?", "Loop?", "Box<Int>", "Box<User>"} {
		t.Run(payload, func(t *testing.T) {
			result := analyze(t, `model User @noTime { id: Int @id values: [Int] }
type Plain { value: Int }
type Box<T> { value: T }
sealed Loop { Next(value: Loop?) End }
sealed Outcome { Value(value: `+payload+`) Empty }
fn equal(left: Outcome, right: Outcome): Boolean { return left == right }
fn different(left: Outcome?, right: Outcome?): Boolean { return left != right }
`)
			expectNoErrors(t, result)
		})
	}
}

func TestSealedEqualityRejectsUnsupportedPayloadComparisons(t *testing.T) {
	for _, payload := range []string{"[Int]", "Bytes", "JSON", "Box<Bytes>", "WithList", "OutcomeList"} {
		t.Run(payload, func(t *testing.T) {
			result := analyze(t, `type Box<T> { value: T }
type WithList { values: [Int] }
sealed OutcomeList { Values(values: [Int]) }
sealed Outcome { Value(value: `+payload+`) Empty }
fn equal(left: Outcome, right: Outcome): Boolean { return left == right }
`)
			expectError(t, result, "does not support equality")
		})
	}
}

func TestSealedUncomparablePayloadStillAllowsTransportAndNullChecks(t *testing.T) {
	expectNoErrors(t, analyze(t, `sealed Outcome { Value(value: JSON) Empty }
api echo(value: Outcome): Outcome { return value }
fn absent(value: Outcome?): Boolean { return value == null }
`))
}

func TestSealedEqualityRejectsListOperandsAndUnresolvedFields(t *testing.T) {
	list := analyze(t, `sealed Outcome { Pending }
fn equal(left: [Outcome], right: [Outcome]): Boolean { return left == right }
`)
	expectError(t, list, "does not support equality for list values")
	unknown := analyze(t, `type Plain { value: Missing }
sealed Outcome { Value(value: Plain) }
fn equal(left: Outcome, right: Outcome): Boolean { return left == right }
`)
	expectError(t, unknown, "unknown type 'Missing'")
	expectError(t, unknown, "does not support equality")
}

func TestSealedComparisonRejectsOperandsWithoutACompatibleValue(t *testing.T) {
	for _, sample := range []struct {
		name       string
		expression string
	}{
		{"empty list", "value == []"},
		{"empty list on the left", "[] != value"},
		{"void call", "value == effect()"},
		{"void call on the left", "effect() != value"},
		{"untyped computed null", "value == (when { true -> null else -> null })"},
		{"empty list pattern", "when(value) { [] -> true else -> false }"},
		{"void call pattern", "when(value) { effect() -> true else -> false }"},
		{"untyped computed null pattern", "when(value) { (when { true -> null else -> null }) -> true else -> false }"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, `
sealed Outcome { Value(value: Int) Pending }
fn effect() {}
fn compare(value: Outcome?): Boolean { return `+sample.expression+` }
`)
			expectError(t, result, "sealed comparison requires")
		})
	}
}

func TestSealedComparisonAcceptsTypedAndSupportedNullValues(t *testing.T) {
	result := analyze(t, `
sealed Outcome { Value(value: Int) Pending }
fn compare(value: Outcome?, other: Outcome?, flag: Boolean): Boolean {
 val absent: Outcome? = null
 val choice = when { flag -> other else -> null }
 val selected = when(value) { null -> true choice -> false else -> true }
 val awaited = when(value) { await { null } -> true else -> false }
 return value == absent || null != value || value == await { null } || await { null } != value || selected || awaited
}
`)
	expectNoErrors(t, result)
}

func TestSealedPayloadFieldsRemainReadOnly(t *testing.T) {
	for _, operation := range []string{"=", "+="} {
		result := analyze(t, `sealed Outcome { Value(value: Int) Empty }
fn mutate(value: Outcome): Int {
 return when(value) { is Value -> { value.value `+operation+` 1 value.value } is Empty -> 0 }
}
`)
		expectError(t, result, "assignment target is not writable")
	}
}
