package codegen

import "testing"

func TestGeneratedWhereMembership(t *testing.T) {
	runGeneratedFixture(t, `
enum State { OPEN CLOSED }
model Probe @noTime {
 id: Int @id
 points: Int
 title: String
 ratio: Float
 flag: Boolean
 stamp: DateTime
 key: UUID
 amount: Decimal
 state: State
 optional: Int?
 scope eligible = where(points >= 6)
}
api byRange(low: Int, high: Int): [Probe] { return Probe.where { it.points in low..high }.orderBy(id.asc).all() }
api byInt(values: [Int]): [Probe] { return Probe.where { it.points in values }.orderBy(id.asc).all() }
api byText(values: [String]): [Probe] { return Probe.where { it.title in values }.orderBy(id.asc).all() }
api byFloat(values: [Float]): [Probe] { return Probe.where { it.ratio in values }.orderBy(id.asc).all() }
api byBool(values: [Boolean]): [Probe] { return Probe.where { it.flag in values }.orderBy(id.asc).all() }
api byTime(values: [DateTime]): [Probe] { return Probe.where { it.stamp in values }.orderBy(id.asc).all() }
api byUUID(values: [UUID]): [Probe] { return Probe.where { it.key in values }.orderBy(id.asc).all() }
api byDecimal(values: [Decimal]): [Probe] { return Probe.where { it.amount in values }.orderBy(id.asc).all() }
api byEnum(values: [State]): [Probe] { return Probe.where { it.state in values }.orderBy(id.asc).all() }
api byEmpty(): [Probe] { return Probe.where { it.points in [] }.all() }
api byLiteral(): [Probe] { return Probe.where { it.points in [4, 6] }.orderBy(id.asc).all() }
api byNullable(): [Probe] { return Probe.where { it.optional in [4] }.orderBy(id.asc).all() }
api byEnumLiteral(): [Probe] { return Probe.where { it.state in [State.OPEN] }.orderBy(id.asc).all() }
fn selectedPoints(): [Int] { return [4, 6] }
api byFunction(): [Probe] { return Probe.where { row -> row.points in selectedPoints() && row.id > 0 }.orderBy(id.asc).all() }
api bySameName(points: [Int]): [Probe] { return Probe.where { it.points in points }.orderBy(id.asc).all() }
api bySameNameNamed(points: [Int]): [Probe] { return Probe.where { row -> row.points in points }.orderBy(id.asc).all() }
api bySameNameShorthand(points: [Int]): [Probe] { return Probe.where(points in points).orderBy(id.asc).all() }
api bySameNameFiltered(points: [Int]): [Probe] { return Probe.where { it.points in points.filter { it > 4 } }.orderBy(id.asc).all() }
api filterLoaded(values: [Int]): [Probe] { return Probe.where { it.points in values }.orderBy(id.asc).all().filter { it.points > 4 } }
api mapLoaded(values: [Int]): [Int] { return Probe.where { it.points in values }.orderBy(id.asc).all().map { it.id } }
api byFind(points: [Int]): [Probe] { return Probe.find(where: it.points in points, order: id.asc) }
api byFindFunction(points: [Int]): [Probe] { return find(Probe, where: it.points in points, order: id.asc) }
api byFindMany(points: [Int]): [Probe] { return Probe.findMany(where: it.points in points, order: id.asc) }
api byContains(title: String): [Probe] { return Probe.where { it.title.contains(title) }.orderBy(id.asc).all() }
api byStarts(title: String): [Probe] { return Probe.where { row -> row.title.startsWith(title) }.orderBy(id.asc).all() }
api byEnds(title: String): [Probe] { return Probe.where(it.title.endsWith(title)).orderBy(id.asc).all() }
api byBareContains(title: String): [Probe] { return Probe.where(title.contains(title)).orderBy(id.asc).all() }
api byFindContains(title: String): [Probe] { return Probe.find(where: it.title.contains(title), order: id.asc) }
api byFindWindow(points: [Int], skip: Int): [Probe] { return Probe.find(where: it.points in points, order: id.desc, limit: 1, offset: skip) }
api byScopedFind(points: [Int]): [Probe] @scope(eligible) { return Probe.find(where: it.points in points, order: id.asc) }
api updateMatching(points: [Int]): Int { return Probe.updateMany(where: it.points in points && it.id > 100, title: "changed") }
api deleteMatching(points: [Int]): Int { return Probe.deleteMany(where: it.points in points && it.id > 100) }
`, membershipAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Probe": {"id": 1, "points": 2, "title": 3, "ratio": 4, "flag": 5, "stamp": 6, "key": 7, "amount": 8, "state": 9, "optional": 10},
	}})
}

const membershipAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestMembershipHTTP(t *testing.T){
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE probes(id bigint PRIMARY KEY,points bigint,title text,ratio double precision,flag boolean,stamp timestamptz,key uuid,amount numeric,state text,optional bigint); INSERT INTO probes VALUES(1,4,'你好',1.25,true,'2026-09-24T01:02:03.123456Z','531f2d00-20e5-47f2-b874-22b6fc76328b',12345678901234567890.123456789,'OPEN',4),(2,9,'other',2.5,false,'2026-09-25T01:02:03Z','00000000-0000-0000-0000-000000000000',2,'CLOSED',NULL),(3,6,'other',2.5,false,'2026-09-25T01:02:03Z','00000000-0000-0000-0000-000000000000',2,'CLOSED',NULL)");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  for _,sample:=range []struct{name string;params map[string]any;ids []int64}{
   {"byRange",map[string]any{"low":int64(4),"high":int64(6)},[]int64{1,3}},
   {"byRange",map[string]any{"low":int64(9),"high":int64(4)},nil},
   {"byRange",map[string]any{"low":int64(-9223372036854775807-1),"high":int64(9223372036854775807)},[]int64{1,2,3}},
   {"byInt",map[string]any{"values":[]int64{4}},[]int64{1}},
   {"byText",map[string]any{"values":[]string{"你好"}},[]int64{1}},
   {"byText",map[string]any{"values":[]string{"你好' OR TRUE --"}},nil},
   {"byFloat",map[string]any{"values":[]float64{1.25}},[]int64{1}},
   {"byBool",map[string]any{"values":[]bool{true}},[]int64{1}},
   {"byTime",map[string]any{"values":[]string{"2026-09-24T01:02:03.123456Z"}},[]int64{1}},
   {"byUUID",map[string]any{"values":[]string{"531f2d00-20e5-47f2-b874-22b6fc76328b"}},[]int64{1}},
   {"byDecimal",map[string]any{"values":[]string{"12345678901234567890.123456789"}},[]int64{1}},
   {"byEnum",map[string]any{"values":[]string{"OPEN"}},[]int64{1}},
   {"byEmpty",nil,nil},{"byLiteral",nil,[]int64{1,3}},{"byNullable",nil,[]int64{1}},
   {"byEnumLiteral",nil,[]int64{1}},{"byFunction",nil,[]int64{1,3}},
   {"bySameName",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"bySameNameNamed",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"bySameNameShorthand",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"bySameNameFiltered",map[string]any{"points":[]int64{4,6}},[]int64{3}},
   {"filterLoaded",map[string]any{"values":[]int64{4,6}},[]int64{3}},
   {"byFind",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"byFindFunction",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"byFindMany",map[string]any{"points":[]int64{4,6}},[]int64{1,3}},
   {"byContains",map[string]any{"title":"你好"},[]int64{1}},
   {"byContains",map[string]any{"title":"%"},nil},
   {"byContains",map[string]any{"title":"_' OR TRUE --"},nil},
   {"byStarts",map[string]any{"title":"oth"},[]int64{2,3}},
   {"byEnds",map[string]any{"title":"er"},[]int64{2,3}},
   {"byBareContains",map[string]any{"title":"the"},[]int64{2,3}},
   {"byFindContains",map[string]any{"title":"the"},[]int64{2,3}},
   {"byFindWindow",map[string]any{"points":[]int64{4,6,9},"skip":int64(1)},[]int64{2}},
   {"byScopedFind",map[string]any{"points":[]int64{4,6}},[]int64{3}},
  }{t.Run(fmt.Sprintf("%s/%v",sample.name,binary),func(t *testing.T){
   checkMembership(t,router,sample.name,binary,sample.params,sample.ids)
  })}
  for _,name:=range []string{"byInt","byText","byFloat","byBool","byTime","byUUID","byDecimal","byEnum"}{
   checkMembership(t,router,name,binary,map[string]any{"values":[]any{}},nil)
  }
  checkMappedMembership(t,router,binary,false)
  if _,err:=pg.Exec(t.Context(),db,"INSERT INTO probes SELECT id+100,points+100,title,ratio,flag,stamp,key,amount,state,optional FROM probes WHERE id<100");err!=nil{t.Fatal(err)}
  checkMutatedMembership(t,router,binary,"updateMatching")
  checkMembership(t,router,"byText",binary,map[string]any{"values":[]string{"changed"}},[]int64{101})
  checkMutatedMembership(t,router,binary,"deleteMatching")
  checkMembership(t,router,"byText",binary,map[string]any{"values":[]string{"changed"}},nil)
  checkMembership(t,router,"byInt",binary,map[string]any{"values":[]int64{4}},[]int64{1})
  if _,err:=pg.Exec(t.Context(),db,"DELETE FROM probes WHERE id>100");err!=nil{t.Fatal(err)}
 }
 db.Close()
 for _,binary:=range []bool{false,true}{checkMappedMembership(t,router,binary,true)}
}
func checkMutatedMembership(t *testing.T,router *api.Router,binary bool,name string){
 t.Helper();response:=groupedResponse(t,router,name,"",nil,binary,map[string]any{"points":[]int64{104}})
 if response.Code!=200{t.Fatalf("%s status=%d body=%s",name,response.Code,response.Body.String())}
 var count int64
 if binary{decoder:=codec.NewDecoder(response.Body.Bytes());count=decoder.ReadInt();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{
  var envelope struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};count=envelope.Data
 }
 if count!=1{t.Fatalf("%s affected=%d want=1",name,count)}
}
func checkMappedMembership(t *testing.T,router *api.Router,binary,fail bool){
 t.Helper();body:=[]byte("{\"$api\":\"mapLoaded\",\"values\":[4,6]}")
 if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs["mapLoaded"].ID,nil,map[string]any{"values":[]int64{4,6}},[]api.ParamMeta{{Name:"values",Type:"Int",IsList:true,FieldID:1}});if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 status:=200;if fail{status=500}
 if response.Code!=status{t.Fatalf("mapped status=%d want=%d body=%s",response.Code,status,response.Body.String())}
 if fail{return}
 var ids []int64
 if binary{decoder:=codec.NewDecoder(response.Body.Bytes());ids=decoder.ReadIntArray();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{
  var envelope struct{Data []int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};ids=envelope.Data
 }
 if len(ids)!=2||ids[0]!=1||ids[1]!=3{t.Fatalf("mapped ids=%v",ids)}
}
func checkMembership(t *testing.T,router *api.Router,name string,binary bool,params map[string]any,want []int64){
 t.Helper();response:=groupedResponse(t,router,name,"id",[]byte{1},binary,params)
 if response.Code!=200{t.Fatalf("%s binary=%v status=%d body=%s",name,binary,response.Code,response.Body.String())}
 var ids []int64
 if binary{values,err:=ReadColumnarProbe(response.Body.Bytes());if err!=nil{t.Fatal(err)};for _,value:=range values{ids=append(ids,value.Id)}}else{
  var envelope struct{Data []struct{Id int64}};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)}
  for _,value:=range envelope.Data{ids=append(ids,value.Id)}
 }
 if len(ids)!=len(want){t.Fatalf("%s binary=%v ids=%v want=%v",name,binary,ids,want)}
 for i,id:=range ids{if id!=want[i]{t.Fatalf("%s ids=%v want=%v",name,ids,want)}}
}
`
