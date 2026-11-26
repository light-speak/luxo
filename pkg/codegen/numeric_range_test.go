package codegen

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestNumericValidationBoundRejectsUnsupportedAST(t *testing.T) {
	for _, value := range []ast.Expr{
		nil,
		&ast.Ident{Name: "limit"},
		&ast.UnaryExpr{Op: "!", Value: &ast.Literal{Kind: token.True}},
		&ast.UnaryExpr{Op: "-", Value: &ast.Ident{Name: "limit"}},
	} {
		if code, label, ok := numericValidationBound(value); ok || code != "" || label != "" {
			t.Fatal("unsupported AST became a numeric bound", code, label, ok)
		}
	}
}

func TestNumericValidationBoundKeepsLiteralLowering(t *testing.T) {
	// Malformed directive declarations need separate semantic diagnostics;
	// adding signed bounds must not silently remove an existing literal check.
	for _, literal := range []*ast.Literal{
		{Kind: token.Int, Value: "010"},
		{Kind: token.Float, Value: "1.5"},
		{Kind: token.String, Value: "10"},
	} {
		code, label, ok := numericValidationBound(literal)
		if !ok || code != compileNumericLiteral(literal) || label != literal.Value {
			t.Fatal("literal validation lowering changed", code, label, ok)
		}
	}
}

func TestNumericValidationBoundSigns(t *testing.T) {
	expression := &ast.UnaryExpr{Op: "+", Value: &ast.Literal{Kind: token.Int, Value: "010"}}
	code, label, ok := numericValidationBound(expression)
	if !ok || code != "+(10)" || label != "+010" {
		t.Fatal(code, label, ok)
	}
	field := testField("value", "Int", &ast.Directive{Name: "range", Args: []*ast.NamedArg{
		{Value: &ast.Ident{Name: "unsupported"}},
		{Value: &ast.Literal{Kind: token.Int, Value: "10"}},
	}})
	var output strings.Builder
	generateNumericValidation(&output, field, "value", "")
	if output.Len() != 0 {
		t.Fatal("unsupported AST became a validation bound", output.String())
	}
}

func TestSignedNumericRangeEmitsValidation(t *testing.T) {
	for _, sample := range []struct{ kind, bounds string }{
		{"Int", "-010, 020"}, {"Int", "-20, -10"},
		{"Float", "-1.5, 1.5"}, {"Float", "-0.0, 1.0"},
		{"Int", "--1, 10"},
	} {
		t.Run(sample.kind+sample.bounds, func(t *testing.T) {
			input := analyzePermissionFixture(t, "model Probe @noTime { value: "+sample.kind+" @range("+sample.bounds+") }")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			var output strings.Builder
			generateNumericValidation(&output, input.Files[0].Models[0].Fields[0], "value", "")
			if !strings.Contains(output.String(), "out of range") {
				t.Fatal("signed bounds silently removed validation", output.String())
			}
		})
	}
}

func TestGeneratedSignedNumericRangeCRUD(t *testing.T) {
	runGeneratedFixture(t, `model Probe @noTime @crud {
 id: Int @id
 value: Int @range(-010, 020)
 ratio: Float @range(-1.5, 1.5)
 optional: Int? @range(-20, -10)
}`, signedNumericRangeAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Probe": {"id": 1, "value": 2, "ratio": 3, "optional": 4},
	}})
}

const signedNumericRangeAssertions = `package generated
import (
 "bytes"
 "encoding/json"
 "fmt"
 "net/http/httptest"
 "testing"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSignedRangeCRUD(t *testing.T) {
 container, err := postgres.Run(t.Context(), testutil.PostgresImage(), postgres.WithDatabase("luxo_test"), postgres.WithUsername("test"), postgres.WithPassword("test"), postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t, container)
 if err != nil { t.Fatal(err) }
 connection, err := container.ConnectionString(t.Context(), "sslmode=disable")
 if err != nil { t.Fatal(err) }
 db, err := pg.NewDB(t.Context(), connection)
 if err != nil { t.Fatal(err) }
 t.Cleanup(db.Close)
 if _, err := pg.Exec(t.Context(), db, "CREATE TABLE probes(id bigint PRIMARY KEY,value bigint NOT NULL,ratio double precision NOT NULL,optional bigint)"); err != nil { t.Fatal(err) }
 app := NewFromDB(db)
 router := api.NewRouter()
 if err := RegisterSchema(router.Schema); err != nil { t.Fatal(err) }
 if err := RegisterHandlers(router, app); err != nil { t.Fatal(err) }
 if err := router.Prepare(); err != nil { t.Fatal(err) }
 for _, binary := range []bool{false, true} {
  t.Run(fmt.Sprint(binary), func(t *testing.T) {
   testRejectedSignedRanges(t, db, router, binary)
   testAcceptedSignedRanges(t, app, router, binary)
  })
 }
}

func callSignedRange(t *testing.T, router *api.Router, name string, binary bool, params map[string]any) int {
 t.Helper()
 var body []byte
 var err error
 if binary {
  mask, maskErr := codec.AppendSelectionMask(nil, []byte{1}, nil)
  if maskErr != nil { t.Fatal(maskErr) }
  body, err = api.EncodeBinaryRequest(router.Schema.APIs[name].ID, mask, params, router.Registry.ParamOrder(name))
 } else {
  payload := map[string]any{"$api": name, "$select": "id"}
  for key, value := range params { payload[key] = value }
  body, err = json.Marshal(payload)
 }
 if err != nil { t.Fatal(err) }
 request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
 request.Header.Set("Content-Type", "application/json")
 if binary {
  request.Header.Set("Content-Type", "application/x-luxo")
  request.Header.Set("X-Luxo-Mode", "binary")
 }
 response := httptest.NewRecorder()
 router.ServeHTTP(response, request)
 return response.Code
}

func testRejectedSignedRanges(t *testing.T, db *pg.DB, router *api.Router, binary bool) {
 tests := []struct{name, field string; value any}{
  {"int_below", "value", int64(-11)}, {"int_above", "value", int64(21)},
  {"float_below", "ratio", -1.5001}, {"float_above", "ratio", 1.5001},
  {"optional_below", "optional", int64(-21)}, {"optional_above", "optional", int64(-9)},
 }
 for index, sample := range tests {
  for _, name := range []string{"createProbe", "updateProbe"} {
   t.Run(sample.name+"/"+name, func(t *testing.T) {
    id := int64(100+index)
    if binary { id += 100 }
    if _, err := pg.Exec(t.Context(), db, "DELETE FROM probes WHERE id=$1", id); err != nil { t.Fatal(err) }
    params := map[string]any{"id": id, "value": int64(0), "ratio": 0.0, "optional": int64(-15)}
    if name == "updateProbe" {
     if _, err := pg.Exec(t.Context(), db, "INSERT INTO probes VALUES($1,0,0,-15)", id); err != nil { t.Fatal(err) }
     params = map[string]any{"id": id}
    }
    params[sample.field] = sample.value
    if status := callSignedRange(t, router, name, binary, params); status != 400 { t.Errorf("invalid value reached write: status=%d", status) }
    query := "SELECT count(*) FROM probes WHERE id=$1"
    expected := int64(0)
    if name == "updateProbe" {
     query += " AND value=0 AND ratio=0 AND optional=-15"
     expected = 1
    }
    count, err := pg.QueryScalar[int64](t.Context(), db, query, id)
    if err != nil || count != expected { t.Errorf("rejected write changed database: count=%d expected=%d error=%v", count, expected, err) }
   })
  }
 }
}

func testAcceptedSignedRanges(t *testing.T, app *App, router *api.Router, binary bool) {
 tests := []struct{name string; value int64; ratio float64; optional any}{
  {"lower", -10, -1.5, int64(-20)}, {"upper", 20, 1.5, int64(-10)}, {"null", 0, 0, nil},
 }
 for index, sample := range tests {
  t.Run(sample.name, func(t *testing.T) {
   id := int64(1000+index)
   if binary { id += 100 }
   params := map[string]any{"id": id, "value": sample.value, "ratio": sample.ratio, "optional": sample.optional}
   for _, name := range []string{"createProbe", "updateProbe"} {
    if status := callSignedRange(t, router, name, binary, params); status != 200 { t.Fatalf("valid bound %s: status=%d", name, status) }
    row, err := app.Probe.Find(t.Context(), id)
    if err != nil || row == nil { t.Fatal(row, err) }
    if row.Value != sample.value || row.Ratio != sample.ratio { t.Error("boundary value changed", row) }
    if sample.optional == nil {
     if row.Optional != nil { t.Error("null changed", row) }
    } else if row.Optional == nil || *row.Optional != sample.optional.(int64) { t.Error("optional boundary changed", row) }
   }
  })
 }
}
`

func numericRangeEmissionCases() []struct{ name, kind, bounds, condition, label string } {
	return []struct{ name, kind, bounds, condition, label string }{
		{"none", "Int", "", "", ""},
		{"positive", "Int", "0, 100", "value < 0 || value > 100", "0, 100"},
		{"signed_int", "Int", "-010, 020", "value < -(10) || value > 20", "-010, 020"},
		{"negative_int", "Int", "-20, -10", "value < -(20) || value > -(10)", "-20, -10"},
		{"nested_sign", "Int", "--1, 010", "value < -(-(1)) || value > 10", "--1, 010"},
		{"positive_float", "Float", "0.0, 1.5", "value < float64(0.0) || value > float64(1.5)", "0.0, 1.5"},
		{"signed_float", "Float", "-1.5, 1.5", "value < func(value float64) float64 { return -value }((float64(1.5))) || value > float64(1.5)", "-1.5, 1.5"},
		{"negative_zero", "Float", "-0.0, 1.0", "value < func(value float64) float64 { return -value }((float64(0.0))) || value > float64(1.0)", "-0.0, 1.0"},
		{"leading_zero_float", "Float", "00.25, 01.5", "value < float64(00.25) || value > float64(01.5)", "00.25, 01.5"},
	}
}

func numericRangeEmissionInput(t testing.TB, kind, bounds string) *semantic.Result {
	t.Helper()
	annotation := ""
	if bounds != "" {
		annotation = " @range(" + bounds + ")"
	}
	input := analyzePermissionFixture(t, "model Probe @noTime @crud { id: Int @id value: "+kind+annotation+" }")
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	return input
}

func TestNumericRangeValidationSource(t *testing.T) {
	for _, sample := range numericRangeEmissionCases() {
		t.Run(sample.name, func(t *testing.T) {
			input := numericRangeEmissionInput(t, sample.kind, sample.bounds)
			for _, indent := range []string{"", "\t\t"} {
				var output strings.Builder
				generateNumericValidation(&output, input.Files[0].Models[0].Fields[1], "value", indent)
				want := ""
				if sample.bounds != "" {
					want = indent + "if " + sample.condition + " {\n" + indent + "\treturn errors.BadRequest.WithData(errors.ParamError{Param: \"value\", Error: \"out of range [" + sample.label + "]\"})\n" + indent + "}\n"
				}
				if output.String() != want {
					t.Fatalf("validation bytes changed:\ngot %q\nwant %q", output.String(), want)
				}
			}
		})
	}
}

func TestValidationCheckSource(t *testing.T) {
	for _, sample := range []struct{ field, message, wantField, wantMessage string }{
		{"value", "out of range [0, 10]", `"value"`, `"out of range [0, 10]"`},
		{"quote\"\\\n\t", "line\n\t\x00\"\\", `"quote\"\\\n\t"`, `"line\n\t\x00\"\\"`},
		{"数值", "范围 café\u2028", `"数值"`, `"范围 café\u2028"`},
		{"\xff", "\x7f", `"\xff"`, `"\x7f"`},
		{strings.Repeat("x", 256), strings.Repeat("\x00", 64), `"` + strings.Repeat("x", 256) + `"`, `"` + strings.Repeat(`\x00`, 64) + `"`},
	} {
		for _, prefix := range []string{"", "prefix\n"} {
			var output strings.Builder
			output.WriteString(prefix)
			writeValidationCheck(&output, "\t", sample.field, "value < 0", sample.message)
			want := prefix + "\tif value < 0 {\n\t\treturn errors.BadRequest.WithData(errors.ParamError{Param: " + sample.wantField + ", Error: " + sample.wantMessage + "})\n\t}\n"
			if output.String() != want {
				t.Fatalf("quoted validation changed: %q want %q", output.String(), want)
			}
		}
	}
}

func numericRangeGeneratedSource(t testing.TB, kind, bounds string) map[string]string {
	t.Helper()
	input := numericRangeEmissionInput(t, kind, bounds)
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Probe": {"id": 1, "value": 2}}})
	output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(output.Files))
	for name, content := range output.Files {
		files[name] = string(content)
	}
	return files
}

func numericRangeGeneratedDigest(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		fmt.Fprintf(digest, "%d:%s%d:%s", len(name), name, len(files[name]), files[name])
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

// These hashes pin every generated file from the original emitter.
func TestNumericRangeGeneratedSource(t *testing.T) {
	expected := map[string]string{
		"none":               "35e9958073d50868a153860c9542f0622d94a6694ef4f690c6a55e264a37ef3e",
		"positive":           "230a1f4b8b14746725b1d6f118b4b593186cf15bbc792c78501f6297575b41ec",
		"signed_int":         "a6cefb9f596004e5730d1213c2b24dccfe83650370776197f300810c03235f90",
		"negative_int":       "fec133ee7fc17ce74001f13a80af5f99b754f525d26e9e4fd18367349bead7ea",
		"nested_sign":        "6eb1c6c27b83efaa78c3f44c133e7cb0bec51db2ee05fb51eb501ef5972d92be",
		"positive_float":     "356ce7c0ee42161af9935395851f383221339c032d46bd60190304b7e89a681b",
		"signed_float":       "24953eb249614d1f00609f1c47181cf3a22fb9f27a6332b4b1fd956cf2c1d8df",
		"negative_zero":      "a8d6078bd1e699a8fd5c5d32e2f2bf882df5726cb92ec5900b57f48871fedc63",
		"leading_zero_float": "29d4f23c1ff517b00dd8f70cf0bcc258615e685393fe5e8333b55b0a85e94fd9",
	}
	for _, sample := range numericRangeEmissionCases() {
		t.Run(sample.name, func(t *testing.T) {
			files := numericRangeGeneratedSource(t, sample.kind, sample.bounds)
			if got := numericRangeGeneratedDigest(files); got != expected[sample.name] {
				t.Fatalf("generated bytes changed: %s want %s", got, expected[sample.name])
			}
		})
	}
}
