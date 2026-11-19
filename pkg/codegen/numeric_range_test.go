package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
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
