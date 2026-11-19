package codegen

import "testing"

const rangeMaterializationSource = `
fn materializedRange(start: Int, end: Int): [Int] { return start..end }
api rangeList(start: Int, end: Int): [Int] { return start..end }
api rangeThroughFunction(start: Int, end: Int): [Int] { return materializedRange(start, end) }
`

func TestGeneratedRangeMaterializationTransports(t *testing.T) {
	runGeneratedFixture(t, rangeMaterializationSource, rangeMaterializationAssertions, StableIDs{
		APIs: map[string]int{"rangeList": 101, "rangeThroughFunction": 102},
		APIParams: map[string]map[string]int{
			"rangeList": {"start": 1, "end": 2}, "rangeThroughFunction": {"start": 1, "end": 2},
		},
	}, rangeMaterializationBenchmarks)
}

const rangeMaterializationAssertions = `package generated
import (
 "bytes"
 "errors"
 "fmt"
 "io"
 "math"
 "net"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/h2transport"
 "github.com/light-speak/luxo/pkg/lux/rpc"
 "nhooyr.io/websocket"
)
func rangeRouter(t *testing.T)*api.Router {
 t.Helper();router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 return router
}
func rangeParams(start,end int64)[]byte {
 var enc codec.Encoder
 enc.WriteFieldInt(1,start);enc.WriteFieldInt(2,end);enc.WriteEnd()
 return enc.Bytes()
}
func rangeBody(id int,name string,start,end int64,binary bool)[]byte {
 if !binary{return fmt.Appendf(nil,"{\"$api\":%q,\"start\":%d,\"end\":%d}",name,start,end)}
 header:=codec.AppendVarint(nil,uint64(id));header=codec.AppendVarint(header,0)
 return append(header,rangeParams(start,end)...)
}
func requireRangeError(t *testing.T,err error) {
 t.Helper();var failure *luxerrors.AppError
 if !errors.As(err,&failure)||failure.Code!=400||failure.Data==nil{t.Errorf("range failure: %v",err);return}
 data,encodeErr:=failure.Data.AppendJSON(nil)
 if encodeErr!=nil||!bytes.Contains(data,[]byte("range"))||!bytes.Contains(data,[]byte("too large")){t.Errorf("range data: %s %v",data,encodeErr)}
}
func TestRangeFunctions(t *testing.T) {
 _,err:=(&App{}).materializedRange(t.Context(),math.MinInt64,math.MaxInt64)
 requireRangeError(t,err)
}
func TestRangeHTTP(t *testing.T) {
 for _,mode:=range []string{"H1","H2","LuxoH2"}{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprint(mode,"/",binary),func(t *testing.T){
  router:=rangeRouter(t);server:=httptest.NewUnstartedServer(router);server.EnableHTTP2=mode!="H1"
  var dedicated *h2transport.Server
  if mode=="LuxoH2"{var err error;dedicated,err=h2transport.NewServer(router);if err!=nil{t.Fatal(err)};if err:=dedicated.ConfigureHTTPServer(server.Config);err!=nil{t.Fatal(err)}}
  if mode=="H1"{server.Start()}else{server.StartTLS()};client:=server.Client()
  defer func(){client.CloseIdleConnections();if dedicated!=nil{dedicated.Close()};server.Close()}()
  for index,name:=range []string{"rangeList","rangeThroughFunction"}{for _,phase:=range []int{0,1,2,0}{
   invalid:=phase!=0;start,end:=int64(-1),int64(1);status:=200
   if phase==1{start,end=math.MinInt64,math.MaxInt64;status=400};if phase==2{start,end=0,65536;status=400}
   request,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL,bytes.NewReader(rangeBody(101+index,name,start,end,binary)));if err!=nil{t.Fatal(err)}
   request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   response,err:=client.Do(request);if err!=nil{t.Fatal(err)};body,readErr:=io.ReadAll(response.Body);response.Body.Close()
   major:=2;if mode=="H1"{major=1};if response.ProtoMajor!=major||response.StatusCode!=status||readErr!=nil{t.Errorf("%s %s %s %s %v",name,response.Status,response.Proto,body,readErr)}
   if invalid&&!bytes.Contains(body,[]byte("too large")){t.Errorf("lost error data: %s",body)}
   if !invalid&&!binary&&string(body)!="{\"data\":[-1,0,1]}"{t.Errorf("range result: %s",body)}
  }}
 })}}
}
func TestRangeLRPC(t *testing.T) {
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(rangeRouter(t));done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 client:=rpc.NewClient(listener.Addr().String());defer func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 for _,id:=range []int{101,102}{
  for _,bounds:=range [][2]int64{{math.MinInt64,math.MaxInt64},{0,65536}}{
   _,err:=client.CallWithMaskContext(t.Context(),"",id,nil,rangeParams(bounds[0],bounds[1]));requireRangeError(t,err)
  }
  body,err:=client.CallWithMaskContext(t.Context(),"",id,nil,rangeParams(0,65535));if err!=nil{t.Fatal("maximum range rejected",err)}
  count,size:=codec.ReadVarint(body,0);if size<=0||count!=65536{t.Fatalf("bad maximum count: %d %x",count,body[:min(len(body),16)])}
  offset:=size;for index:=int64(0);index<65536;index++{value,read:=codec.ReadSvarint(body,offset);if read<=0||value!=index{t.Fatal("maximum list changed",index,value,read)};offset+=read}
  if offset!=len(body){t.Fatal("trailing maximum list data")}
 }
}
func TestRangeWebSocket(t *testing.T) {
 server:=httptest.NewServer(rangeRouter(t));defer server.Close()
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprint(binary),func(t *testing.T){
  conn,_,err:=websocket.Dial(t.Context(),"ws"+strings.TrimPrefix(server.URL,"http")+"/luvia",nil);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
  id:=uint64(0)
  for index,name:=range []string{"rangeList","rangeThroughFunction"}{for _,phase:=range []int{1,2,0}{
   id++;invalid:=phase!=0;start,end:=int64(1),int64(2)
   if phase==1{start,end=math.MinInt64,math.MaxInt64};if phase==2{start,end=0,65536}
   body:=fmt.Appendf(nil,"{\"$id\":%d,\"$api\":%q,\"start\":%d,\"end\":%d}",id,name,start,end)
   kind:=websocket.MessageText
   if binary{kind=websocket.MessageBinary;body=codec.AppendVarint([]byte{1},id);body=append(body,rangeBody(101+index,name,start,end,true)...)}
   if err:=conn.Write(t.Context(),kind,body);err!=nil{t.Fatal(err)}
   gotKind,got,err:=conn.Read(t.Context());if err!=nil||gotKind!=kind{t.Fatal(gotKind,err)}
   if binary{if len(got)<2{t.Fatalf("short frame: %x",got)};gotID,n:=codec.ReadVarint(got,1);want:=byte(2);if invalid{want=3};if got[0]!=want||gotID!=id||n<=0{t.Errorf("bad frame: %x",got)}}
   if invalid&&(!bytes.Contains(got,[]byte("too large"))||!binary&&!bytes.Contains(got,[]byte("\"code\":400"))){t.Errorf("lost range failure: %s",got)}
  }}
 })}
}
`
