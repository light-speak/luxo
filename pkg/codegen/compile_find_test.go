package codegen

import "testing"

func TestGeneratedFindMissingContract(t *testing.T) {
	runGeneratedFixture(t, `
model Probe @noTime {
 id: Int @id
 title: String
 scope allowed = where(id > 1)
}
api findDirect(id: Int): Probe { return Probe.find(id: id) }
api findVariable(id: Int): Probe {
 val found = Probe.find(id: id)
 return found
}
api findFunctionStyle(id: Int): Probe { return find(Probe, id: id) }
api findScoped(id: Int): Probe @scope(allowed) { return Probe.find(id: id) }
fn requiredProbe(id: Int): Probe { return Probe.find(id: id) }
api findViaFunction(id: Int): Int {
 val found = requiredProbe(id)
 return found.id
}
api findParallel(id: Int): Int {
 val (found, other) = await {
  Probe.find(id: id)
  Probe.find(id: 2)
 }
 return found.id + other.id
}
api firstMissing(id: Int): Boolean {
 val found = Probe.where(it.id == id).first()
 return found == null
}
api findFirstMissing(id: Int): Boolean {
 val found = Probe.findFirst(where: it.id == id)
 return found == null
}
api firstAnyMissing(): Boolean {
 val found = Probe.findFirst()
 return found == null
}
api firstOrderedMissing(): Boolean {
 val found = Probe.findFirst(order: id.desc)
 return found == null
}
api findAll(): [Probe] { return Probe.findMany() }
api findList(id: Int): [Probe] { return Probe.find(where: it.id == id) }
api findInsideTransaction(id: Int): Int {
 transaction {
  Probe.where(it.id == 2).update(title: "changed")
  val found = Probe.find(id: id)
  Probe.where(it.id == found.id).update(title: "found")
 }
 return 1
}
`, generatedFindAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Probe": {"id": 1, "title": 2},
	}})
}

const generatedFindAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestFindMissingHTTP(t *testing.T){
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE probes(id bigint PRIMARY KEY,title text NOT NULL); INSERT INTO probes VALUES(1,'one'),(2,'two')");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 t.Run("native-client-missing",func(t *testing.T){
  found,err:=app.Probe.Find(t.Context(),99)
  if found!=nil||err==nil||err.Error()!="NotFound"{t.Fatalf("client find=%v error=%v",found,err)}
 })
 for _,binary:=range []bool{false,true}{
  for _,name:=range []string{"findDirect","findVariable","findFunctionStyle","findScoped","findViaFunction","findParallel","findInsideTransaction"}{
   t.Run(fmt.Sprintf("%s/%v",name,binary),func(t *testing.T){
    fields,mask:="id,title",[]byte{3};if name=="findViaFunction"||name=="findParallel"||name=="findInsideTransaction"{fields,mask="",nil}
    response:=groupedResponse(t,router,name,fields,mask,binary,map[string]any{"id":int64(99)})
    assertFindError(t,response,binary,404,"NotFound")
   })
  }
  t.Run(fmt.Sprintf("scope/%v",binary),func(t *testing.T){
   response:=groupedResponse(t,router,"findScoped","id",[]byte{1},binary,map[string]any{"id":int64(1)})
   assertFindError(t,response,binary,404,"NotFound")
  })
  for _,name:=range []string{"firstMissing","findFirstMissing"}{t.Run(fmt.Sprintf("%s/%v",name,binary),func(t *testing.T){
   response:=groupedResponse(t,router,name,"",nil,binary,map[string]any{"id":int64(99)})
   if response.Code!=200{t.Fatalf("nullable lookup failed: %d %s",response.Code,response.Body.String())}
   if binary{dec:=codec.NewDecoder(response.Body.Bytes());if !dec.ReadBool()||dec.Finish()!=nil{t.Fatal("missing lookup must return true")}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":true")){t.Fatal(response.Body.String())}
  })}
  t.Run(fmt.Sprintf("list/%v",binary),func(t *testing.T){
   response:=groupedResponse(t,router,"findList","id",[]byte{1},binary,map[string]any{"id":int64(99)})
   if response.Code!=200{t.Fatalf("list lookup failed: %d %s",response.Code,response.Body.String())}
   if binary{items,err:=ReadColumnarProbe(response.Body.Bytes());if err!=nil||len(items)!=0{t.Fatal(items,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":[]")){t.Fatal(response.Body.String())}
  })
  t.Run(fmt.Sprintf("present/%v",binary),func(t *testing.T){
   response:=groupedResponse(t,router,"findDirect","id,title",[]byte{3},binary,map[string]any{"id":int64(2)})
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if !binary&&!bytes.Contains(response.Body.Bytes(),[]byte("\"title\":\"two\"")){t.Fatal("failed find must roll back preceding write",response.Body.String())}
  })
  for _,name:=range []string{"firstAnyMissing","firstOrderedMissing","findAll"}{t.Run(fmt.Sprintf("%s/%v",name,binary),func(t *testing.T){
   fields,mask:="",[]byte(nil);if name=="findAll"{fields,mask="id",[]byte{1}}
   response:=groupedResponse(t,router,name,fields,mask,binary)
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if name=="findAll"{
    if binary{values,err:=ReadColumnarProbe(response.Body.Bytes());if err!=nil||len(values)!=2{t.Fatal(values,err)}}else{
     var envelope struct{Data []Probe};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||len(envelope.Data)!=2{t.Fatal(envelope,err)}
    }
   }else if binary{dec:=codec.NewDecoder(response.Body.Bytes());if dec.ReadBool()||dec.Finish()!=nil{t.Fatal("present lookup must return false")}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":false")){t.Fatal(response.Body.String())}
  })}
 }
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("db-error/%v",binary),func(t *testing.T){
  if _,err:=pg.Exec(t.Context(),db,"DROP TABLE IF EXISTS probes");err!=nil{t.Fatal(err)}
  response:=groupedResponse(t,router,"findDirect","id",[]byte{1},binary,map[string]any{"id":int64(2)})
  if response.Code!=500{t.Fatalf("database failure must not become 404: %d %s",response.Code,response.Body.String())}
 })}
}
func assertFindError(t *testing.T,response *httptest.ResponseRecorder,binary bool,status int,name string){
 t.Helper();if response.Code!=status{t.Fatalf("status=%d want=%d body=%s",response.Code,status,response.Body.String())}
 if binary{failure,err:=api.DecodeBinaryError(response.Body.Bytes(),status);if err!=nil||failure.Name!=name{t.Fatal(failure,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"error\":\""+name+"\"")){t.Fatal(response.Body.String())}
}
`
