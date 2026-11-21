package codegen

import "testing"

func TestGeneratedArrayWriteBounds(t *testing.T) {
	runGeneratedFixture(t, `
type Envelope { flags: [Boolean] }
event Batch(flags: [Boolean])
api flags(): [Boolean] @native
fn sourceFlags(): Result<[Boolean]> @native
api compiledFlags(): [Boolean] { return sourceFlags()? }
api watchFlags(): [Boolean] @stream @native
`, arrayWriteBoundaryAssertions, StableIDs{
		APIs:        map[string]int{"flags": 1, "compiledFlags": 2, "watchFlags": 3},
		ModelFields: map[string]map[string]int{"Envelope": {"flags": 1}},
		EventFields: map[string]map[string]int{"Batch": {"flags": 1}},
	}, arraySnapshotBenchmarks)
}

const arrayWriteBoundaryAssertions = `package generated
import("bytes";"context";"errors";"io";"net";"net/http";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/h2transport";"github.com/light-speak/luxo/pkg/lux/rpc")
type arrayResolver struct { values []bool }
func(r arrayResolver)Flags(context.Context)([]bool,error){return r.values,nil}
func(r arrayResolver)SourceFlags(context.Context)([]bool,error){return r.values,nil}
func(r arrayResolver)HandleWatchFlags(_ context.Context,_ *WatchFlagsStreamParams,_ any,stream *api.TypedStream[[]bool])error{return stream.Send(r.values)}
func TestArrayOutputBounds(t *testing.T){
 for _,size:=range []int{0,2,codec.MaxArrayElements+1}{
  values:=make([]bool,size); invalid:=size>codec.MaxArrayElements
  event:=BatchEvent{Flags:values}
  raw,err:=event.MarshalLuxo()
  if invalid {if !errors.Is(err,codec.ErrInvalidArrayCount)||raw!=nil{t.Fatal("event wire",len(raw),err)}}else if err!=nil{t.Fatal(err)}
  snapshot,err:=event.SnapshotEvent()
  if invalid {if !errors.Is(err,codec.ErrInvalidArrayCount)||snapshot!=nil{t.Fatal("memory event",snapshot!=nil,err)}}else if err!=nil{t.Fatal(err)}
  item:=Envelope{Flags:values};buffer:=&api.ResponseBuf{}
  err=item.WriteLuxo(buffer,nil)
  if invalid {if !errors.Is(err,codec.ErrInvalidArrayCount){t.Fatal("object field",err)}}else if err!=nil{t.Fatal(err)}
  app:=&App{Resolver:arrayResolver{values:values}}
  for _,handler:=range []api.HandlerFunc{handleFlags(app),handleCompiledFlags(app)}{
   request:=&api.Request{Buf:&api.ResponseBuf{},BinaryMode:true}
   err=handler(t.Context(),request)
   if invalid {if !errors.Is(err,codec.ErrInvalidArrayCount){t.Fatal("unary",err)}}else if err!=nil{t.Fatal(err)}
  }
  router:=api.NewRouter()
  if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
  if err:=RegisterStreams(router,nil,app.Resolver);err!=nil{t.Fatal(err)}
  sub,err:=router.OpenInternalStream(t.Context(),&api.Request{API:"watchFlags",BinaryMode:true})
  if err!=nil{t.Fatal(err)}
  count:=0;for range sub.Messages(){count++};sub.Close();err=sub.Err()
  if invalid {if !errors.Is(err,codec.ErrInvalidArrayCount)||count!=0{t.Fatal("stream",count,err)}}else if err!=nil||count!=1{t.Fatal("valid stream",count,err)}
 }
}
func arrayRouter(t *testing.T,state *arrayResolver)*api.Router{
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:state});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)};return router
}
func TestArrayNetworkFailureIsolation(t *testing.T){
 for _,mode:=range []string{"H1","H2","LuxoH2"}{t.Run(mode,func(t *testing.T){
  state:=&arrayResolver{};router:=arrayRouter(t,state);server:=httptest.NewUnstartedServer(router);server.EnableHTTP2=mode!="H1"
  var dedicated *h2transport.Server
  if mode=="LuxoH2"{var err error;dedicated,err=h2transport.NewServer(router);if err!=nil{t.Fatal(err)};if err:=dedicated.ConfigureHTTPServer(server.Config);err!=nil{t.Fatal(err)}}
  if mode=="H1"{server.Start()}else{server.StartTLS()};client:=server.Client()
  defer func(){client.CloseIdleConnections();if dedicated!=nil{dedicated.Close()};server.Close()}()
  for _,id:=range []byte{1,2}{for _,invalid:=range []bool{true,false}{
   status:=200;state.values=[]bool{true,false};if invalid{status=500;state.values=make([]bool,codec.MaxArrayElements+1)}
   request,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL,bytes.NewReader([]byte{id,0,0}));if err!=nil{t.Fatal(err)}
   request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")
   response,err:=client.Do(request);if err!=nil{t.Fatal(err)};body,err:=io.ReadAll(response.Body);response.Body.Close()
   if response.StatusCode!=status||err!=nil{t.Fatal(response.Status,string(body),err)}
   if !invalid&&!bytes.Equal(body,[]byte{2,1,0}){t.Fatal("valid array changed",body)}
   if mode!="H1"&&response.ProtoMajor!=2{t.Fatal("HTTP/2 was not exercised")}
  }}
 })}
 state:=&arrayResolver{};server:=rpc.NewServer(arrayRouter(t,state));listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}();client:=rpc.NewClient(listener.Addr().String())
 defer func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 for _,id:=range []int{1,2}{for _,invalid:=range []bool{true,false}{
  state.values=[]bool{true,false};if invalid{state.values=make([]bool,codec.MaxArrayElements+1)}
  body,err:=client.CallWithMaskContext(t.Context(),"",id,nil,[]byte{0})
  if invalid {if err==nil{t.Fatal("invalid array succeeded over LRPC")}}else if err!=nil||!bytes.Equal(body,[]byte{2,1,0}){t.Fatal("LRPC valid array",body,err)}
 }}
}

`

// The reference is the preceding generator's validated, owned Bool-list snapshot,
// differing only in the newly added array-count guard.
const arraySnapshotBenchmarks = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/event")
var arraySnapshotSink event.Snapshot
func previousBatchSnapshot(e *BatchEvent)(event.Snapshot,error){
 if e==nil{return nil,event.ErrInvalidSnapshot}
 out:=*e;out.Flags=make([]bool,len(e.Flags));copy(out.Flags,e.Flags)
 return event.NewSnapshot(out,cloneBatchEvent)
}
func BenchmarkArraySnapshot(b *testing.B){
 for _,count:=range []int{0,128}{name:="empty";if count!=0{name="128"};value:=&BatchEvent{Flags:make([]bool,count)}
  for _,checked:=range []bool{false,true}{variant:="Before";if checked{variant="After"}
   b.Run(name+"/"+variant,func(b *testing.B){b.ReportAllocs();for b.Loop(){
    var err error
    if checked{arraySnapshotSink,err=value.SnapshotEvent()}else{arraySnapshotSink,err=previousBatchSnapshot(value)}
    if err!=nil{b.Fatal(err)}
   }})
  }
 }
}
`
