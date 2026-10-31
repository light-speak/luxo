package codegen

import "testing"

func TestGeneratedNullableStreamRoot(t *testing.T) {
	runGeneratedFixture(t, `
api watchValue(): Int? @stream @native
api watchList(): [Int]? @stream @native
`, nullableStreamAssertions, StableIDs{})
}

func TestGeneratedNullableEventStreamRoot(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { value: Int }
event ValueChanged(value: Int?)
event SnapshotChanged(value: Snapshot?)
api watchValue(): Int? @stream(ValueChanged) { true }
api watchSnapshot(): Snapshot? @stream(SnapshotChanged) { true }
`, nullableEventStreamAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Snapshot": {"value": 1}},
		EventFields: map[string]map[string]int{"ValueChanged": {"value": 1}, "SnapshotChanged": {"value": 1}},
	})
}

const nullableEventStreamAssertions = `package generated
import("testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/event")
func TestNullableEventStream(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(8);defer bus.Close();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 for _,test:=range []struct{name string;want []string}{{"watchValue",[]string{"null","0"}},{"watchSnapshot",[]string{"null", "{\"value\":42}"}}}{
  sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:test.name,FieldMask:[]byte{2}});if err!=nil{t.Fatal(err)}
  if test.name=="watchValue"{
   value:=int64(0);if err:=EmitValueChanged(t.Context(),bus,ValueChangedEvent{});err!=nil{t.Fatal(err)}
   if err:=EmitValueChanged(t.Context(),bus,ValueChangedEvent{Value:&value});err!=nil{t.Fatal(err)}
  }else{
   if err:=EmitSnapshotChanged(t.Context(),bus,SnapshotChangedEvent{});err!=nil{t.Fatal(err)}
   if err:=EmitSnapshotChanged(t.Context(),bus,SnapshotChangedEvent{Value:&Snapshot{Value:42}});err!=nil{t.Fatal(err)}
  }
  for _,want:=range test.want{select{case data,ok:=<-sub.Messages():
    if !ok{t.Fatal("null was treated as completion")};got,err:=router.StreamPayloadJSON(test.name,data)
    if err!=nil||string(got)!=want{t.Fatalf("%s got %s want %s: %v",test.name,got,want,err)}
   case<-time.After(3*time.Second):t.Fatal("missing stream message")}}
  sub.Close()
 }
}
`

const nullableStreamAssertions = `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
type nullableStreamResolver struct{}
func(*nullableStreamResolver)HandleWatchValue(_ context.Context,_ *WatchValueStreamParams,_ any,stream *api.TypedStream[*int64])error{
 if err:=stream.Send(nil);err!=nil{return err};value:=int64(0);return stream.Send(&value)
}
func(*nullableStreamResolver)HandleWatchList(_ context.Context,_ *WatchListStreamParams,_ any,stream *api.TypedStream[[]int64])error{
 if err:=stream.Send(nil);err!=nil{return err};if err:=stream.Send([]int64{});err!=nil{return err};return stream.Send([]int64{42})
}
func TestNullableStreamRoots(t *testing.T){
 router:=api.NewRouter()
 for index,name:=range []string{"watchValue","watchList"}{if err:=router.Schema.RegisterAPI(&schema.API{ID:index+1,Name:name,ReturnType:"Int",ReturnList:index==1,ReturnNullable:true,Stream:true});err!=nil{t.Fatal(err)}}
 if err:=RegisterStreams(router,nil,&nullableStreamResolver{});err!=nil{t.Fatal(err)}
 for _,test:=range []struct{name string;want []string}{{"watchValue",[]string{"null","0"}},{"watchList",[]string{"null","[]","[42]"}}}{
  sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:test.name});if err!=nil{t.Fatal(err)}
  count:=0;for data:=range sub.Messages(){got,err:=router.StreamPayloadJSON(test.name,data);if err!=nil{t.Fatal(err)};if count>=len(test.want)||string(got)!=test.want[count]{t.Fatalf("%s frame %d: %s",test.name,count,got)};count++}
  sub.Close();if sub.Err()!=nil||count!=len(test.want){t.Fatal(count,sub.Err())}
 }
}
`

func TestGeneratedNullableRoot(t *testing.T) {
	runGeneratedFixture(t, `
model Probe @noTime { id: Int @id }
type Snapshot { id: Int }
enum State { OPEN CLOSED }
api optionalRoot(id: Int): Probe? { return Probe.where(id: id).first() }
api optionalVariable(id: Int): Probe? {
 val item = Probe.where(id: id).first()
 return item
}
api optionalInt(value: Int?): Int? { return value }
api optionalBool(value: Boolean?): Boolean? { return value }
api optionalText(value: String?): String? { return value }
api optionalList(value: [Int]?): [Int]? { return value }
api optionalSnapshot(value: Snapshot?): Snapshot? { return value }
api optionalState(value: State?): State? { return value }
api optionalBytes(value: Bytes?): Bytes? { return value }
api optionalJSON(value: JSON?): JSON? { return value }
api constantInt(): Int? { return 42 }
api constantNull(): Int? { return null }
api constantSnapshot(): Snapshot? { return Snapshot { id: 42 } }
api constantList(): [Int]? { return [42] }
api constantNullList(): [Int]? { return null }
api nativeInt(value: Int?): Int? @native
api nativeSnapshot(value: Snapshot?): Snapshot? @native
api nativeList(value: [Int]?): [Int]? @native
fn serviceValue(value: Int?): Int? @service { return value }
fn nativeServiceValue(value: Int?): Int? @service @native
`, nullableRootAssertions+typedGroupRequestHelper, StableIDs{ModelFields: map[string]map[string]int{
		"Probe": {"id": 1}, "Snapshot": {"id": 1},
	}})
}

const nullableRootAssertions = `package generated
import("bytes";"context";"encoding/json";"fmt";"maps";"net";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestNullableRoots(t *testing.T){
 container,err:=postgres.Run(t.Context(),"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE probes(id bigint PRIMARY KEY); INSERT INTO probes VALUES(42)");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);app.Resolver=&nullableResolver{}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 tests:=[]struct{name string;params map[string]any;want string}{
 {"optionalRoot",map[string]any{"id":99},"null"},
 {"optionalRoot",map[string]any{"id":42},"{\"id\":42}"},
 {"optionalVariable",map[string]any{"id":99},"null"},
 {"optionalVariable",map[string]any{"id":42},"{\"id\":42}"},
 {"optionalInt",map[string]any{"value":nil},"null"},
 {"optionalInt",map[string]any{"value":0},"0"},
 {"optionalBool",map[string]any{"value":nil},"null"},
 {"optionalBool",map[string]any{"value":false},"false"},
 {"optionalText",map[string]any{"value":nil},"null"},
 {"optionalText",map[string]any{"value":""},"\"\""},
 {"optionalList",map[string]any{"value":nil},"null"},
 {"optionalList",map[string]any{"value":[]any{}},"[]"},
 {"optionalList",map[string]any{"value":[]any{42}},"[42]"},
 {"optionalSnapshot",map[string]any{"value":nil},"null"},
 {"optionalSnapshot",map[string]any{"value":map[string]any{"id":42}},"{\"id\":42}"},
 {"optionalState",map[string]any{"value":nil},"null"},
 {"optionalState",map[string]any{"value":"OPEN"},"\"OPEN\""},
 {"optionalBytes",map[string]any{"value":nil},"null"},
 {"optionalBytes",map[string]any{"value":""},"\"\""},
 {"optionalJSON",map[string]any{"value":nil},"null"},
 {"optionalJSON",map[string]any{"value":map[string]any{"number":42}},"{\"number\":42}"},
 {"constantInt",nil,"42"},{"constantNull",nil,"null"},
 {"constantSnapshot",nil,"{\"id\":42}"},{"constantList",nil,"[42]"},{"constantNullList",nil,"null"},
 {"nativeInt",map[string]any{"value":nil},"null"},{"nativeInt",map[string]any{"value":42},"42"},
 {"nativeSnapshot",map[string]any{"value":nil},"null"},{"nativeSnapshot",map[string]any{"value":map[string]any{"id":42}},"{\"id\":42}"},
 {"nativeList",map[string]any{"value":nil},"null"},{"nativeList",map[string]any{"value":[]any{}},"[]"},
 }
 for index,test:=range tests{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("%d/%s/%v",index,test.name,binary),func(t *testing.T){
  meta:=router.Schema.APIs[test.name];if !meta.ReturnNullable{t.Fatal("lost nullable schema")}
  fields:="";var mask []byte;if meta.ReturnType=="Snapshot"||meta.ReturnType=="Probe"{fields="id";mask=[]byte{1}}
  params:=maps.Clone(test.params)
  if binary&&params["value"]!=nil{switch test.name{
   case "optionalSnapshot","nativeSnapshot":message,err:=router.Schema.JSONToBinary("Snapshot",[]byte(test.want));if err!=nil{t.Fatal(err)};params["value"]=api.BinaryMessage(message)
   case "optionalBytes":params["value"]=[]byte{}
   case "optionalJSON":params["value"]=json.RawMessage(test.want)
  }}
  response:=groupedResponse(t,router,test.name,fields,mask,binary,params)
  if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
  var got []byte
  if binary{
   data:=response.Body.Bytes();if len(data)==0{t.Fatal("empty body is not null")}
   if test.want=="null"&&!bytes.Equal(data,[]byte{0}){t.Fatalf("null envelope: %x",data)}
   if test.want!="null"&&data[0]!=1{t.Fatalf("present envelope: %x",data)}
   got,err=router.StreamPayloadJSON(test.name,data);if err!=nil{t.Fatal(err)}
  }else{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};got=envelope.Data}
  if string(got)!=test.want{t.Fatalf("got %s; want %s",got,test.want)}
 })}}
}
type nullableResolver struct{}
func(*nullableResolver)NativeInt(_ context.Context,value *int64)(*int64,error){return value,nil}
func(*nullableResolver)NativeSnapshot(_ context.Context,value *Snapshot)(*Snapshot,error){return value,nil}
func(*nullableResolver)NativeList(_ context.Context,value []int64)([]int64,error){return value,nil}
func(*nullableResolver)NativeServiceValue(_ context.Context,value *int64)(*int64,error){return value,nil}
func TestNullableServiceRPC(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,&App{Resolver:&nullableResolver{}});err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,name:=range []string{"svc:serviceValue","svc:nativeServiceValue"}{for _,present:=range []bool{false,true}{
  meta:=router.Schema.APIs[name];if !meta.ReturnNullable{t.Fatal("internal root nullability lost")}
  enc:=codec.Encoder{};var value *int64;number:=int64(42);if present{value=&number};enc.WriteFieldIntPtr(meta.Params[0].ID,value);enc.WriteEnd()
  data,err:=client.CallWithMaskContext(t.Context(),"",meta.ID,nil,enc.Bytes());if err!=nil{t.Fatal(err)}
  want:=[]byte{0};if present{want=[]byte{1,84}};if !bytes.Equal(data,want){t.Fatalf("%s: %x want %x",name,data,want)}
 }}
}
`
