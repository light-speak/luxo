package codegen

import "testing"

func TestGroupScanScalarMappings(t *testing.T) {
	for _, test := range []struct {
		name, scanner, expression string
		checked                   bool
	}{
		{"Int", "Int64Value", "value.Int64", false},
		{"Float", "Float64Value", "value.Float64", false},
		{"String", "TextValue", "value.String", false},
		{"Boolean", "BoolValue", "value.Bool", false},
		{"UUID", "UUIDValue", "uuid.UUID(value.Bytes)", false},
		{"DateTime", "TimeValue", "pg.DateTimeFromValue(value)", true},
		{"Duration", "DurationValue", "pg.DurationFromValue(value)", true},
		{"Decimal", "DecimalValue", "pg.DecimalFromValue(value)", true},
		{"JSON", "", "", false},
		{"Bytes", "", "", false},
		{"Object", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := groupScanType(test.name, false); got != test.scanner {
				t.Fatalf("scanner = %q, want %q", got, test.scanner)
			}
			got, checked := groupScanExpression(test.name, "value", false)
			if got != test.expression || checked != test.checked {
				t.Fatalf("expression = %q/%v, want %q/%v", got, checked, test.expression, test.checked)
			}
		})
	}
	if groupScanType("State", true) != "TextValue" {
		t.Fatal("enum must use a textual scanner")
	}
	if got, checked := groupScanExpression("State", "value", true); got != "State(value.String)" || checked {
		t.Fatalf("enum conversion = %q/%v", got, checked)
	}
}

func TestGeneratedBooleanQueryConditions(t *testing.T) {
	runGeneratedFixture(t, `
model Flag @noTime {
 id: Int @id
 enabled: Boolean
 optional: Boolean?
 label: String
}
type FlagSummary { count: Int }
api unequalFlag(): [FlagSummary] {
 return Flag.where { it.enabled != true }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api nullFlag(): [FlagSummary] {
 return Flag.where { it.optional == null }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api nonnullFlag(): [FlagSummary] {
 return Flag.where { it.optional != null }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api nullableUnequalFlag(): [FlagSummary] {
 return Flag.where { it.optional != true }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api nullableUnequalOrNullFlag(): [FlagSummary] {
 return Flag.where { it.optional != true || it.optional == null }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api directUnequalFlag(): [FlagSummary] {
 return Flag.where(it.enabled != true).groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api bareFlag(): [FlagSummary] {
 return Flag.where { it.enabled }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api negatedFlag(): [FlagSummary] {
 return Flag.where { !it.enabled }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api negatedGroup(): [FlagSummary] {
 return Flag.where { !(it.enabled && it.id > 1) }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api negatedNullableComparison(): [FlagSummary] {
 return Flag.where { !(it.optional == true) }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api negatedMembership(): [FlagSummary] {
 return Flag.where { !(it.id in [1, 3]) }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api constantFlag(): [FlagSummary] {
 return Flag.where { true }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api constantFalseFlag(): [FlagSummary] {
 return Flag.where { !true }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api parameterFlag(allow: Boolean): [FlagSummary] {
 return Flag.where { allow && !it.enabled }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api negatedPattern(prefix: String): [FlagSummary] {
 return Flag.where { !it.label.startsWith(prefix) }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
api bareNamedFlag(): [FlagSummary] {
 return Flag.where { row -> !row.enabled }.groupBy { it.enabled }.select { FlagSummary { count: it.count() } }
}
`, booleanQueryAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Flag": {"id": 1, "enabled": 2, "optional": 3, "label": 4}, "FlagSummary": {"count": 1},
	}})
}

const booleanQueryAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestBooleanQueryHTTP(t *testing.T){
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE flags(id bigint PRIMARY KEY,enabled boolean NOT NULL,optional boolean,label text NOT NULL); INSERT INTO flags VALUES(1,true,true,'a%_one'),(2,false,false,'another'),(3,true,NULL,'three')");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)}
 for name,want:=range map[string]int64{"unequalFlag":1,"nullFlag":1,"nonnullFlag":2,"nullableUnequalFlag":1,"nullableUnequalOrNullFlag":2,"directUnequalFlag":1,"bareFlag":2,"negatedFlag":1,"negatedGroup":2,"negatedNullableComparison":1,"negatedMembership":1,"constantFlag":3,"constantFalseFlag":0,"parameterFlag":1,"negatedPattern":2,"bareNamedFlag":1}{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("%s/%v",name,binary),func(t *testing.T){
  var params []map[string]any;if name=="parameterFlag"{params=append(params,map[string]any{"allow":true})}
  if name=="negatedPattern"{params=append(params,map[string]any{"prefix":"a%"})}
  response:=groupedResponse(t,router,name,"count",[]byte{1},binary,params...)
  if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
  var values []FlagSummary
  if binary{values,err=ReadColumnarFlagSummary(response.Body.Bytes());if err!=nil{t.Fatal(err)}}else{
   var envelope struct{Data []FlagSummary};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};values=envelope.Data
  }
  var count int64;for _,value:=range values{count+=value.Count}
  if count!=want{t.Fatalf("count=%d want=%d values=%+v",count,want,values)}
  if name=="parameterFlag"{
   response=groupedResponse(t,router,name,"count",[]byte{1},binary,map[string]any{"allow":false})
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if binary{rows,err:=ReadColumnarFlagSummary(response.Body.Bytes());if err!=nil||len(rows)!=0{t.Fatal(rows,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":[]")){t.Fatal(response.Body.String())}
  }
 })}}
}
`

func TestGeneratedTypedGroupedQuery(t *testing.T) {
	runGeneratedFixture(t, `
enum SaleState { OPEN CLOSED }
model Sale @noTime {
 id: Int @id
 state: SaleState
 points: Int
 optional: Int?
 scope eligible = where(points >= 6)
}
type SaleSummary {
 count: Int
 state: SaleState
 minimum: Int?
}
api summarizeSales(): [SaleSummary] {
 return Sale.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
fn groupedSales(): [SaleSummary] {
 return Sale.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeViaFunction(): [SaleSummary] { return groupedSales() }
api summarizeFiltered(): [SaleSummary] {
 return Sale.where(it.points > 0).groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeViaVariable(): [SaleSummary] {
 val filtered = Sale.where(it.points > 0)
 val summary = filtered.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
 return summary
}
api summarizeLambda(): [SaleSummary] {
 return Sale.where { it.points >= 6 }.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeNamedLambda(): [SaleSummary] {
 return Sale.where { sale -> sale.points >= 6 && (sale.state == SaleState.OPEN || sale.points == 4) }.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeScoped(): [SaleSummary] @scope(eligible) {
 return Sale.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeScopedVariable(): [SaleSummary] @scope(eligible) {
 val filtered = Sale.where { row -> row.points < 9 }
 return filtered.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeHaving(minimum: Int = 1): [SaleSummary] {
 return Sale.where(it.points > 0).groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }.having { group -> group.count() > minimum && (group.key == SaleState.OPEN || group.sum { it.points } > 20) }
  .having { it.min { it.optional } != null }
}
api summarizeHavingNull(): [SaleSummary] {
 return Sale.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }.having { it.min { it.optional } == null && it.sum { it.optional } == 0 }
}
api summarizeEnumParameter(state: SaleState = SaleState.OPEN): [SaleSummary] {
 return Sale.where { it.state == state }.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
api summarizeEnumNamed(state: SaleState = SaleState.OPEN): [SaleSummary] {
 return Sale.where(state: state).groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
fn saleState(value: SaleState): SaleState { return value }
api summarizeEnumFunction(state: SaleState = SaleState.OPEN): [SaleSummary] {
 return Sale.where { it.state == saleState(state) }.groupBy { it.state }.select { SaleSummary {
  count: it.count(), state: it.key, minimum: it.min { it.optional }
 } }
}
`, typedGroupAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Sale":        {"id": 1, "state": 2, "points": 3, "optional": 4},
		"SaleSummary": {"count": 1, "state": 2, "minimum": 3},
	}})
}

const typedGroupAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api"
"github.com/light-speak/luxo/pkg/lux/codec"
"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go"
"github.com/testcontainers/testcontainers-go/modules/postgres")
func newTypedGroupFixture(t testing.TB)(*api.Router,*pg.DB){
 t.Helper()
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE sales(id bigint PRIMARY KEY,state text,points bigint,optional bigint); INSERT INTO sales VALUES(1,'OPEN',4,7),(2,'OPEN',6,3),(3,'CLOSED',9,NULL)");err!=nil{t.Fatal(err)}
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)}
 return router,db
}
func TestTypedGroupHTTP(t *testing.T){
 router,db:=newTypedGroupFixture(t)
 var err error
 for _,name:=range []string{"summarizeSales","summarizeViaFunction","summarizeFiltered","summarizeViaVariable","summarizeLambda","summarizeNamedLambda","summarizeScoped","summarizeScopedVariable","summarizeHaving","summarizeHavingNull","summarizeEnumParameter","summarizeEnumNamed"}{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("%s/%v",name,binary),func(t *testing.T){
  response:=groupedResponse(t,router,name,"count,state,minimum",[]byte{7},binary)
  if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
  var values []SaleSummary
  if binary{values,err=ReadColumnarSaleSummary(response.Body.Bytes());if err!=nil{t.Fatal(err)}}else{
   var envelope struct{Data []struct{Count int64;State string;Minimum *int64}}
   if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)}
   for _,value:=range envelope.Data{values=append(values,SaleSummary{Count:value.Count,State:SaleState(value.State),Minimum:value.Minimum})}
  }
  wantGroups:=2;if name=="summarizeNamedLambda"||name=="summarizeScopedVariable"||name=="summarizeHaving"||name=="summarizeHavingNull"||name=="summarizeEnumParameter"||name=="summarizeEnumNamed"{wantGroups=1}
  if len(values)!=wantGroups{t.Fatalf("missing grouped output: %+v",values)}
  wantState:=SaleStateOPEN;if name=="summarizeHavingNull"{wantState=SaleStateCLOSED}
  if wantGroups==1&&values[0].State!=wantState{t.Fatalf("wrong logical grouping: %+v",values)}
  wantOpen:=int64(2);if name=="summarizeLambda"||name=="summarizeNamedLambda"||name=="summarizeScoped"||name=="summarizeScopedVariable"{wantOpen=1}
  for _,value:=range values{switch value.State{
   case SaleStateOPEN:if value.Count!=wantOpen||value.Minimum==nil||*value.Minimum!=3{t.Fatalf("open: %+v",value)}
   case SaleStateCLOSED:if value.Count!=1||value.Minimum!=nil{t.Fatalf("closed: %+v",value)}
   default:t.Fatalf("unknown state: %+v",value)
  }}
 })}}
 testGroupedSelection(t,router)
 testEnumQueryParameters(t,router)
 testGroupedErrorsAndEmpty(t,router,db)
}
func BenchmarkTypedGroupHTTP(b *testing.B){
 router,_:=newTypedGroupFixture(b)
 for _,name:=range []string{"summarizeSales","summarizeHaving"}{for _,binary:=range []bool{false,true}{
  mode:="JSON";if binary{mode="Binary"}
  b.Run(name+"/"+mode,func(b *testing.B){
   body:=groupedRequestBody(b,router,name,"count,state,minimum",[]byte{7},binary)
   execute:=func()*httptest.ResponseRecorder{
    request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
    if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
    response:=httptest.NewRecorder();router.ServeHTTP(response,request)
    return response
   }
   response:=execute()
   if response.Code!=200{b.Fatal(response.Code,response.Body.String())}
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){response=execute();if response.Code!=200{b.Fatal(response.Code,response.Body.String())}}
   b.StopTimer()
   var values []SaleSummary
   if binary{var err error;values,err=ReadColumnarSaleSummary(response.Body.Bytes());if err!=nil{b.Fatal(err)}}else{
    var envelope struct{Data []SaleSummary};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{b.Fatal(err)};values=envelope.Data
   }
   want:=2;if name=="summarizeHaving"{want=1}
   if len(values)!=want{b.Fatalf("got %d groups, want %d",len(values),want)}
   for _,value:=range values{if value.State==SaleStateOPEN&&(value.Count!=2||value.Minimum==nil||*value.Minimum!=3){b.Fatalf("invalid open group: %+v",value)}}
  })
 }}
}
func testEnumQueryParameters(t *testing.T,router *api.Router){
 t.Helper()
 for _,name:=range []string{"summarizeEnumParameter","summarizeEnumNamed","summarizeEnumFunction"}{for _,binary:=range []bool{false,true}{for _,state:=range []string{"OPEN","CLOSED"}{
  response:=groupedResponse(t,router,name,"count,state",[]byte{3},binary,map[string]any{"state":state})
  if response.Code!=200{t.Fatal(name,binary,state,response.Code,response.Body.String())}
  var values []SaleSummary
  if binary{var err error;values,err=ReadColumnarSaleSummary(response.Body.Bytes());if err!=nil{t.Fatal(err)}}else{
   var envelope struct{Data []SaleSummary};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};values=envelope.Data
  }
  want:=int64(2);if state=="CLOSED"{want=1}
  if len(values)!=1||values[0].State!=SaleState(state)||values[0].Count!=want{t.Fatalf("%s/%v/%s: %+v",name,binary,state,values)}
 }}}
}
func testGroupedSelection(t *testing.T,router *api.Router){
 t.Helper()
 for _,binary:=range []bool{false,true}{
  response:=groupedResponse(t,router,"summarizeSales","count",[]byte{1},binary)
  if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{
   values,err:=ReadColumnarSaleSummary(response.Body.Bytes());if err!=nil||len(values)!=2{t.Fatal(values,err)}
   if values[0].Count+values[1].Count!=3{t.Fatal("wrong count",values)}
   for _,value:=range values{if value.State!=""||value.Minimum!=nil{t.Fatalf("unselected fields returned: %+v",value)}}
  }else{
   if bytes.Contains(response.Body.Bytes(),[]byte("\"state\""))||bytes.Contains(response.Body.Bytes(),[]byte("\"minimum\"")){t.Fatal("unselected fields returned",response.Body.String())}
   var envelope struct{Data []struct{Count int64}}
   if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||len(envelope.Data)!=2||envelope.Data[0].Count+envelope.Data[1].Count!=3{t.Fatal(envelope,err)}
  }
 }
}
func testGroupedErrorsAndEmpty(t *testing.T,router *api.Router,db *pg.DB){
 t.Helper()
 for _,state:=range []string{"NULL","'UNKNOWN'"}{
  if _,err:=pg.Exec(t.Context(),db,"UPDATE sales SET state="+state+" WHERE id=1");err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   response:=groupedResponse(t,router,"summarizeSales","count,state,minimum",[]byte{7},binary)
   if response.Code==200{t.Fatal("invalid database enum accepted",response.Body.String())}
  }
 }
 if _,err:=pg.Exec(t.Context(),db,"DELETE FROM sales");err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  response:=groupedResponse(t,router,"summarizeSales","count,state,minimum",[]byte{7},binary)
  if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{values,err:=ReadColumnarSaleSummary(response.Body.Bytes());if err!=nil||len(values)!=0{t.Fatal(values,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":[]")){t.Fatal("empty result lost",response.Body.String())}
 }
}
`

const typedGroupRequestHelper = `
func groupedRequestBody(t testing.TB,router *api.Router,name,fields string,bitmap []byte,binary bool,parameters ...map[string]any)[]byte{
 t.Helper()
 var params map[string]any;if len(parameters)>0{params=parameters[0]}
 payload:=map[string]any{"$api":name,"$select":fields};for key,value:=range params{payload[key]=value}
 body,err:=json.Marshal(payload);if err!=nil{t.Fatal(err)}
 if binary{
  var mask []byte
  if len(bitmap)>0{mask,err=codec.AppendSelectionMask(nil,bitmap,nil);if err!=nil{t.Fatal(err)}}
  body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
 }
 return body
}
func groupedResponse(t *testing.T,router *api.Router,name,fields string,bitmap []byte,binary bool,parameters ...map[string]any)*httptest.ResponseRecorder{
 t.Helper()
 body:=groupedRequestBody(t,router,name,fields,bitmap,binary,parameters...)
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 return response
}
`

func TestGeneratedTypedGroupScalarProjection(t *testing.T) {
	runGeneratedFixture(t, `
model Metric @noTime {
 id: Int @id
 keyId: UUID?
 enabled: Boolean?
 title: String?
 amount: Decimal?
 observedAt: DateTime?
 elapsed: Duration?
 ratio: Float?
 points: Int
}
type MetricSummary {
 id: Int
 keyId: UUID?
 enabled: Boolean?
 title: String?
 amount: Decimal
 observedAt: DateTime?
 elapsed: Duration?
 ratio: Float?
 total: Int
 mean: Float
}
api metricSummary(): [MetricSummary] {
 return Metric.groupBy { [it.id, it.keyId, it.enabled, it.title] }.select { MetricSummary {
  id: it.key.id, keyId: it.key.keyId, enabled: it.key.enabled, title: it.key.title,
  amount: it.sum { it.amount }, observedAt: it.min { it.observedAt },
  elapsed: it.max { it.elapsed }, ratio: it.min { it.ratio },
  total: it.sum { it.points }, mean: it.avg { it.points }
 } }
}
type MetricCount { count: Int }
fn checkedBound(value: Int): Result<Int> @native
api metricHaving(keyId: UUID, enabled: Boolean, title: String, amount: Decimal, before: DateTime, ratio: Float): [MetricCount] {
 return Metric.groupBy { [it.keyId, it.enabled, it.title] }.select { MetricCount { count: it.count() } }
  .having { group -> group.key.keyId == keyId && group.key.enabled == enabled && group.key.title == title }
  .having { it.sum { it.amount } >= amount && it.min { it.observedAt } <= before && it.min { it.ratio } > ratio }
}
api metricHavingFunction(minimum: Int): [MetricCount] {
 return Metric.groupBy { it.id }.select { MetricCount { count: it.count() } }
  .having { it.count() >= checkedBound(minimum)? }
}
`, typedGroupScalarAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Metric":        {"id": 1, "keyId": 2, "enabled": 3, "title": 4, "amount": 5, "observedAt": 6, "elapsed": 7, "ratio": 8, "points": 9},
		"MetricSummary": {"id": 1, "keyId": 2, "enabled": 3, "title": 4, "amount": 5, "observedAt": 6, "elapsed": 7, "ratio": 8, "total": 9, "mean": 10},
		"MetricCount":   {"count": 1},
	}})
}

const typedGroupScalarAssertions = `package generated
import("bytes";"context";"encoding/json";"errors";"fmt";"net/http/httptest";"testing";"time"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
type groupHavingResolver struct{calls int;fail bool}
func(r *groupHavingResolver)CheckedBound(_ context.Context,value int64)(int64,error){r.calls++;if r.fail{return 0,errors.New("bound failed")};return value,nil}
func TestGroupedScalarHTTP(t *testing.T){
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE metrics(id bigint PRIMARY KEY,key_id uuid,enabled boolean,title text,amount numeric,observed_at timestamptz,elapsed interval,ratio double precision,points bigint); INSERT INTO metrics VALUES (1,'531f2d00-20e5-47f2-b874-22b6fc76328b',true,'你好',12345678901234567890.123456789,'2026-09-24T01:02:03.123456Z',interval '2 days 1 microsecond',1.25,7),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,2)");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);resolver:=&groupHavingResolver{};app.Resolver=resolver
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 testHavingScalars(t,router)
 testHavingFunction(t,router,resolver)
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprint(binary),func(t *testing.T){
  response:=groupedResponse(t,router,"metricSummary","id,keyId,enabled,title,amount,observedAt,elapsed,ratio,total,mean",[]byte{255,3},binary)
  if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
  if !binary{
   for _,text:=range []string{"12345678901234567890.123456789","531f2d00-20e5-47f2-b874-22b6fc76328b","2026-09-24T01:02:03.123456Z","你好","\"keyId\":null"}{if !bytes.Contains(response.Body.Bytes(),[]byte(text)){t.Fatalf("missing %q in %s",text,response.Body.String())}}
   return
  }
  values,err:=ReadColumnarMetricSummary(response.Body.Bytes());if err!=nil||len(values)!=2{t.Fatalf("values=%+v err=%v",values,err)}
  for _,value:=range values{switch value.Id{
   case 1:
    if value.KeyId==nil||value.KeyId.String()!="531f2d00-20e5-47f2-b874-22b6fc76328b"||value.Enabled==nil||!*value.Enabled||value.Title==nil||*value.Title!="你好"{t.Fatalf("keys=%+v",value)}
    if value.Amount.String()!="12345678901234567890.123456789"||value.ObservedAt==nil||value.ObservedAt.Nanosecond()!=123456000||value.Elapsed==nil||*value.Elapsed!=48*time.Hour+time.Microsecond||value.Ratio==nil||*value.Ratio!=1.25||value.Total!=7||value.Mean!=7{t.Fatalf("aggregates=%+v",value)}
   case 2:if value.KeyId!=nil||value.Enabled!=nil||value.Title!=nil||!value.Amount.IsZero()||value.ObservedAt!=nil||value.Elapsed!=nil||value.Ratio!=nil||value.Total!=2||value.Mean!=2{t.Fatalf("nulls=%+v",value)}
   default:t.Fatalf("unexpected row: %+v",value)
  }}
 })}
 for _,assignment:=range []string{"elapsed=interval '1 month'","observed_at='infinity'::timestamptz","amount='NaN'::numeric","ratio='Infinity'::double precision"}{
  if _,err:=pg.Exec(t.Context(),db,"UPDATE metrics SET elapsed=interval '2 days',observed_at=now(),amount=1,ratio=1 WHERE id=1; UPDATE metrics SET "+assignment+" WHERE id=1");err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   response:=groupedResponse(t,router,"metricSummary","id,keyId,enabled,title,amount,observedAt,elapsed,ratio,total,mean",[]byte{255,3},binary)
   if response.Code==200{t.Fatal("invalid aggregate encoded successfully",assignment,response.Body.String())}
  }
 }
}
func testHavingScalars(t *testing.T,router *api.Router){
 t.Helper()
 for _,binary:=range []bool{false,true}{for _,sample:=range []struct{key string;value any}{
  {},{"keyId","00000000-0000-0000-0000-000000000000"},{"enabled",false},{"title","你好' OR TRUE --"},
  {"amount","12345678901234567890.123456790"},{"before","2026-09-24T01:02:03.123455Z"},{"ratio",1.25},
 }{t.Run(fmt.Sprintf("having/%v/%s",binary,sample.key),func(t *testing.T){
  params:=map[string]any{"keyId":"531f2d00-20e5-47f2-b874-22b6fc76328b","enabled":true,"title":"你好","amount":"12345678901234567890.123456789","before":"2026-09-24T01:02:03.123456Z","ratio":1.0}
  want:=1;if sample.key!=""{params[sample.key]=sample.value;want=0}
  response:=groupedResponse(t,router,"metricHaving","count",[]byte{1},binary,params)
  checkMetricCount(t,response,binary,want)
 })}}
}
func testHavingFunction(t *testing.T,router *api.Router,resolver *groupHavingResolver){
 t.Helper()
 for _,binary:=range []bool{false,true}{for _,fail:=range []bool{false,true}{
  resolver.calls=0;resolver.fail=fail
  response:=groupedResponse(t,router,"metricHavingFunction","count",[]byte{1},binary,map[string]any{"minimum":int64(1)})
  if resolver.calls!=1{t.Fatalf("bound evaluated %d times",resolver.calls)}
  if fail{if response.Code!=500{t.Fatal("missing bound failure",response.Code,response.Body.String())}}else{checkMetricCount(t,response,binary,2)}
 }}
 resolver.fail=false
}
func checkMetricCount(t *testing.T,response *httptest.ResponseRecorder,binary bool,want int){
 t.Helper();if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
 var values []MetricCount
 if binary{var err error;values,err=ReadColumnarMetricCount(response.Body.Bytes());if err!=nil{t.Fatal(err)}}else{
  var envelope struct{Data []MetricCount};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};values=envelope.Data
 }
 if len(values)!=want{t.Fatalf("values=%+v want=%d",values,want)}
 for _,value:=range values{if value.Count!=1{t.Fatal("incorrect count",value)}}
}
`
