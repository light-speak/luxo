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
