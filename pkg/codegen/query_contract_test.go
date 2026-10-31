package codegen

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedStrictQueryContract(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
 "context"
 "strings"
 "github.com/light-speak/luxo/pkg/lux"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/selection"
)
`)
	model := testModel("Probe", nil, []*ast.FieldDecl{
		testField("id", "Int", directive("id")),
		testField("status", "State", directive("filterable")),
		testField("count", "Int", directive("filterable"), directive("sortable")),
		testField("score", "Float", directive("filterable")),
		testField("active", "Boolean", directive("filterable")),
		testField("created", "DateTime", directive("filterable")),
		testField("title", "String", directive("filterable")),
		testField("body", "String", directive("filterable"), directive("search")),
		testField("owner", "UUID", directive("filterable")),
		testField("price", "Decimal", directive("filterable")),
		testField("raw", "JSON", directive("filterable")),
		{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}, Directives: []*ast.Directive{directive("filterable"), directive("sortable")}},
		testField("private", "String", directive("filterable"), directive("sortable"), directive("internal")),
	})
	enums := map[string]bool{"State": true}
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	generateFilterParser(&source, model, enums)
	generateSorterParser(&source, model)
	generateFilterParser(&source, testModel("Empty", nil, nil), nil)
	generateSorterParser(&source, testModel("Empty", nil, nil))
	generateHandler(&source, model, "list", enums, nil)
	source.WriteString(queryContractFixture)
	dir := writeProtocolFixture(t, source.String(), queryContractAssertions)
	for _, args := range [][]string{{"test", "-count=1", "."}, {"vet", "."}} {
		command := exec.Command("go", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("generated query %v: %v\n%s", args, err, output)
		}
	}
}

const queryContractFixture = `
type App struct { Probe *query }
type query struct { calls int }
func (q *query) Where(...lux.Condition) *query { q.calls++; return q }
func (q *query) Select(...string) *query { return q }
func (q *query) OrderBy(...string) *query { return q }
func (q *query) Limit(int) *query { return q }
func (q *query) Offset(int) *query { return q }
func (q *query) AllWithCount(context.Context) ([]int, int64, error) { return nil, 0, nil }
func selectProbeSQLColumns([]*selection.Field) []string { return nil }
func WriteColumnarProbe(buf *api.ResponseBuf, _ []int, _ []byte) error { buf.B=append(buf.B,0,0,0); return nil }
`

const queryContractAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

 "bytes"
 "encoding/json"
 "net/http/httptest"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/schema"
)

func TestInvalidQueryNeverReachesDatabase(t *testing.T) {
 for _, binary := range []bool{false,true} {
  for _, filter := range []api.Filter{
   {Field:"missing",Operator:"eq",Value:"OPEN"}, {Field:"id",Operator:"eq",Value:"42"}, {Field:"private",Operator:"eq",Value:"secret"},
   {Field:"status",Operator:"eq",Value:"FUTURE"}, {Field:"status",Operator:"ne",Value:"FUTURE"}, {Field:"status",Operator:"gt",Value:"OPEN"},
   {Field:"status",Operator:"contains",Value:"OPEN"}, {Field:"active",Operator:"eq",Value:"truue"}, {Field:"active",Operator:"gt",Value:"true"},
   {Field:"score",Operator:"eq",Value:"NaN"}, {Field:"score",Operator:"eq",Value:"Infinity"}, {Field:"count",Operator:"eq",Value:"bad"},
   {Field:"count",Operator:"eq",Value:"9223372036854775808"}, {Field:"created",Operator:"eq",Value:"bad"}, {Field:"title",Operator:"gt",Value:"x"},
   {Field:"title",Operator:"match",Value:"x"},
   {Field:"owner",Operator:"eq",Value:"bad"}, {Field:"owner",Operator:"gt",Value:"531f2d00-20e5-47f2-b874-22b6fc76328b"},
   {Field:"price",Operator:"eq",Value:"NaN"}, {Field:"raw",Operator:"eq",Value:"{}"}, {Field:"tags",Operator:"eq",Value:"x"},
  } {
   queryRequest(t,binary,[]api.Filter{{Field:"status",Operator:"eq",Value:"OPEN"},filter},nil,400)
  }
  for _, sorter := range []api.Sorter{{Field:"missing",Order:"asc"},{Field:"id",Order:"asc"},{Field:"private",Order:"desc"}} {
   queryRequest(t,binary,nil,[]api.Sorter{sorter},400)
  }
  queryRequest(t,binary,[]api.Filter{{Field:"status",Operator:"ne",Value:"CLOSED"},{Field:"count",Operator:"gte",Value:"42"}},[]api.Sorter{{Field:"count",Order:"desc"}},200)
  queryRequest(t,binary,[]api.Filter{{Field:"owner",Operator:"eq",Value:"531f2d00-20e5-47f2-b874-22b6fc76328b"},{Field:"price",Operator:"gte",Value:"12345678901234567890.123456789"}},nil,200)
  queryRequest(t,binary,[]api.Filter{{Field:"title",Operator:"contains",Value:"100%_"},{Field:"body",Operator:"match",Value:"luxo"}},nil,200)
  queryRequest(t,binary,nil,[]api.Sorter{{Field:"count",Order:"asc"}},200)
 }
}

func TestEmptyAndDirectQueryValidation(t *testing.T) {
 if _,err:=parseEmptyFilters(nil);err!=nil {t.Fatal(err)}
 if _,err:=parseEmptySorters(nil);err!=nil {t.Fatal(err)}
 if result,err:=parseEmptyFilters([]api.Filter{{Field:"id",Operator:"eq",Value:"1"}});err==nil || result!=nil {t.Fatal("empty model accepted filter")}
 if result,err:=parseEmptySorters([]api.Sorter{{Field:"id",Order:"asc"}});err==nil || result!=nil {t.Fatal("empty model accepted sorter")}
 if result,err:=parseProbeSorters([]api.Sorter{{Field:"count",Order:"asc"},{Field:"count",Order:"bad"}});err==nil || result!=nil {t.Fatal("invalid sorter published partial clauses")}
 if result,err:=parseProbeFilters([]api.Filter{{Field:"count",Operator:"eq",Value:"1"},{Field:"count",Operator:"unknown",Value:"2"}});err==nil || result!=nil {t.Fatal("invalid filter published partial conditions")}
}

func queryRequest(t *testing.T, binary bool, filters []api.Filter, sorters []api.Sorter, want int) {
 t.Helper()
 q:=&query{}
 router:=api.NewRouterWithOptions(api.RouterOptions{})
 router.Schema=schema.New()
 router.Schema.RegisterModel(&schema.Model{Name:"Probe",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt}}})
 router.Schema.RegisterAPI(&schema.API{ID:42,Name:"listProbes",ReturnType:"Probe",ReturnList:true,Paginated:true})
 router.Registry.RegisterSchemaAPIs(router.Schema)
 router.HandleLocal("listProbes",handleListProbes(&App{Probe:q}))
 params:=map[string]any{"$filters":filters,"$sorters":sorters}
 var body []byte; var err error
 if binary {body,err=api.EncodeBinaryRequest(42,testutil.Bytes(t)(codec.AppendSelectionMask(nil,testutil.Bytes(t)(codec.FieldMaskSet(nil,1)),nil)),params,nil)} else {
  params["$api"]="listProbes";params["$select"]="id"; body,err=json.Marshal(params)
 }
 if err!=nil {t.Fatal(err)}
 req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
 content:="application/json"; if binary {content="application/x-luxo";req.Header.Set("X-Luxo-Mode","binary")}
 req.Header.Set("Content-Type",content)
 recorder:=httptest.NewRecorder(); router.ServeHTTP(recorder,req)
 if recorder.Code!=want {t.Fatalf("binary=%v filters=%v sorters=%v HTTP=%d: %s",binary,filters,sorters,recorder.Code,recorder.Body.String())}
 if want==400 && q.calls!=0 {t.Fatalf("rejected query touched database %d times",q.calls)}
 if want==200 && q.calls!=1 {t.Fatalf("valid query calls=%d",q.calls)}
}
`
