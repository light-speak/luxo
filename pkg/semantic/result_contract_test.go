package semantic

import (
	"strings"
	"testing"
)

func TestResultCannotBeAStoredOrArgumentValue(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"nested_native", `fn read(): Result<Result<Int>> @native`},
		{"nested_service", `fn read(): Result<Result<Int>> @service @native`},
		{"nested_list", `fn read(): Result<[Result<Int>]> @native`},
		{"model_field", `model Item @noTime { id: Int @id value: Result<Int> }`},
		{"type_field", `type Wrapped { value: Result<Int> }`},
		{"type_list_field", `type Wrapped { values: [Result<Int>] }`},
		{"fn_argument", `fn readValue(value: Result<Int>): Int { return 1 }`},
		{"native_argument", `fn readValue(value: Result<Int>): Result<Int> @native`},
		{"service_argument", `fn readValue(value: Result<Int>): Int @service @native`},
		{"api_argument", `api readValue(value: Result<Int>): Int { return 1 }`},
		{"event_argument", `event Ready(value: Result<Int>)`},
		{"error_data", `error Rejected(value: Result<Int>) { code: 400 message: "rejected" }`},
		{"nullable_local", `fn readValue() { val pending: Result<Int>? = null }`},
		{"list_local", `fn readValue() { val pending: [Result<Int>] = [] }`},
		{"channel_element", `fn readValue() { val ch: Channel<Result<Int>> = Channel(1) }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, sample.source)
			message := "Result<T> is only valid as the outer return type"
			if sample.name == "event_argument" {
				message = "unsupported wire type"
			}
			expectError(t, result, message)
		})
	}
}

func TestNativeResultPayloadKeepsSupportedShapes(t *testing.T) {
	for _, resultType := range []string{"Int", "Int?", "[Int]", "[Int]?", "Snapshot", "Snapshot?", "[Snapshot]", "JSON", "Bytes", "DateTime", "Duration", "Amount", "Channel<Int>"} {
		t.Run(resultType, func(t *testing.T) {
			result := analyze(t, `type Snapshot { id: Int } enum Amount { SMALL LARGE } fn read(): Result<`+resultType+`> @native`)
			expectNoErrors(t, result)
		})
	}
}

func TestIdentityContextCannotBeAnExplicitDataType(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"stored_claim", `model User @noTime @withAuth(stores: [id, peers]) { id: Int @id peers: [Identity] @by(id, id) } api probe(): Int @auth { return my.peers.size }`},
		{"model", `model Item @noTime { id: Int @id value: Identity }`},
		{"nullable_model", `model Item @noTime { id: Int @id value: Identity? }`},
		{"type", `type Packet { value: Identity }`},
		{"nullable_list", `type Packet { value: [Identity]? }`},
		{"interface", `interface Packet { value: Identity }`},
		{"sealed", `sealed Outcome { Value(value: Identity) }`},
		{"event", `event Ready(value: Identity)`},
		{"error", `error Rejected(value: Identity) { code: 400 message: "rejected" }`},
		{"extend_field", `model Item @noTime { id: Int @id } extend Item { value: Identity }`},
		{"api_parameter", `api probe(value: Identity): Int { return 1 }`},
		{"api_return", `api probe(): Identity @native`},
		{"fn_parameter", `fn probe(value: Identity): Int { return 1 }`},
		{"fn_return", `fn probe(): Identity { return Identity { id: 1, role: "admin" } }`},
		{"native_parameter", `fn probe(value: Identity): Result<Int> @native`},
		{"native_return", `fn probe(): Result<Identity> @native`},
		{"service_parameter", `fn probe(value: Identity): Int @service @native`},
		{"service_return", `fn probe(): Identity @service @native`},
		{"local", `fn probe() { val value: Identity? = null }`},
		{"local_list", `fn probe() { val value: [Identity] = [] }`},
		{"tuple", `type Packet { value: (Int, Identity) }`},
		{"generic", `type Packet { value: Page<Identity> }`},
		{"generic_list", `fn probe(): Result<[Identity]> @native`},
		{"custom_generic", `type Box<T> { value: T } type Packet { value: Box<Identity> }`},
		{"local_channel", `fn probe() { val values: Channel<Identity> = Channel(1) }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			expectIdentityContextDiagnostic(t, analyze(t, sample.source))
		})
	}
}

func expectIdentityContextDiagnostic(t *testing.T, result *Result) {
	t.Helper()
	for _, diagnostic := range result.Errors {
		if strings.Contains(diagnostic.Message, "Identity is a runtime authentication context") {
			if diagnostic.Pos.File == "" || diagnostic.Pos.Line < 1 || diagnostic.Pos.Col < 1 {
				t.Fatalf("Identity diagnostic lost its source position: %+v", diagnostic)
			}
			return
		}
	}
	t.Fatalf("Identity data reference has no context diagnostic: %v", result.Errors)
}

func TestIdentityContextCannotBeDeclaredOrConstructed(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"parent", `model Child: Identity @noTime { id: Int @id }`},
		{"extend_target", `extend Identity { id: Int }`},
		{"object_literal", `api probe(): Int { val value = Identity { id: 1, role: "admin" } return value.id }`},
		{"type_name_value", `api probe(): Int { val value = Identity return value.id }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			expectIdentityContextDiagnostic(t, analyze(t, sample.source))
		})
	}
}

func TestIdentityContextKeepsOrdinaryNominalAndLocalValues(t *testing.T) {
	result := analyze(t, `
model Base @noTime { id: Int @id }
model Item: Base @noTime { name: String }
extend Item { name: String }
api scalar(): Int { val Identity = 42 return Identity }
api object(): Int { val Identity = Item { id: 7, name: "item" } return Identity.id }
api namespace(): Int { return Item.count() }
`)
	expectNoErrors(t, result)
}

func TestIdentityContextKeepsAuthenticatedAccess(t *testing.T) {
	result := analyze(t, `
model Detail @noTime { id: Int @id }
model User @noTime @withAuth(stores: [id, name, detail]) {
 id: Int @id name: String detailId: Int detail: Detail
}
api identityID(): Int @auth { return my.id }
api identityName(): String @auth { return my.name }
api identityModel(): Int @auth { return my.detail.id }
api identityLoad(): String @auth { val loaded = my.load(name, role) return "done" }
`)
	expectNoErrors(t, result)
	identity := result.Types["Identity"]
	if identity == nil || identity.Kind != TypeModel || identity.Fields["load"].Type != identity {
		t.Fatal("runtime identity context or its load method was replaced")
	}
	if result.Types["User"].Fields["detail"].Type.Name != "Detail" {
		t.Fatal("a declared stored model lost its type")
	}
}

func TestIdentityTypeParameterKeepsDeclarationScope(t *testing.T) {
	result := analyze(t, `type Box<Identity> { value: Identity optional: Identity? many: [Identity] }
api probe(): Int @auth { return my.id }`)
	expectNoErrors(t, result)
	for _, name := range []string{"value", "optional", "many"} {
		if field := result.Types["Box"].Fields[name]; field.Type.Kind != TypeGeneric || field.Type.Name != "Identity" {
			t.Fatalf("local type parameter became the runtime identity: %+v", field)
		}
	}
	if result.Types["Identity"].Kind != TypeModel {
		t.Fatal("a local type parameter replaced the runtime identity")
	}
}
