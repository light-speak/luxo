package codegen

import "testing"

func TestGeneratedAsyncStringOwnership(t *testing.T) {
	runGeneratedFixture(t, asyncStringOwnershipSource, asyncStringOwnershipAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}},
		APIs:        map[string]int{"capture": 101},
		APIParams:   map[string]map[string]int{"capture": {"sequence": 1, "data": 2, "document": 3, "payload": 4}},
	})
}

const asyncStringOwnershipSource = `model Counter @noTime { id: Int @id value: Int }
fn inspectLater(sequence: Int, data: String, document: JSON, payload: Bytes): Result<Int> @native
api capture(sequence: Int, data: String, document: JSON, payload: Bytes): Int {
 async { val _ = inspectLater(sequence, data, document, payload)? }
 return 42
}
`
const asyncStringOwnershipAssertions = `package generated

import (
 "bytes"
 "context"
 "encoding/base64"
 "encoding/json"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
 "time"

 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/h2transport"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/light-speak/luxo/pkg/lux/rpc"
 "nhooyr.io/websocket"
)

type captureResult struct { sequence int64; data, document, payload string }
type captureResolver struct {
 entered chan int64
 release chan struct{}
 once sync.Once
 results chan captureResult
}
func (r *captureResolver) InspectLater(_ context.Context, sequence int64, data string, document json.RawMessage, payload []byte) (int64,error) {
 r.entered <- sequence
 <-r.release
 r.results <- captureResult{sequence,data,string(document),string(payload)}
 return 0,nil
}
func (r *captureResolver) finish() {r.once.Do(func(){close(r.release)})}

type captureListener struct {net.Listener; accepted atomic.Int64}
func(l *captureListener) Accept()(net.Conn,error){
 conn,err:=l.Listener.Accept()
 if err==nil {l.accepted.Add(1)}
 return conn,err
}

type captureCall func(context.Context,int64,string,string)
func captureRouter(t *testing.T,r *captureResolver)*api.Router{
 t.Helper()
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:r});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 return router
}
func captureBinary(t *testing.T,sequence int64,data,document string)[]byte{
 var params codec.Encoder
 params.WriteFieldInt(1,sequence)
 if err:=params.WriteFieldString(2,data);err!=nil{t.Fatal(err)}
 params.WriteFieldBytes(3,[]byte(document))
 params.WriteFieldBytes(4,[]byte(data))
 params.WriteEnd()
 return params.Bytes()
}
func captureBody(t *testing.T,binary bool,sequence int64,data,document string)[]byte{
 if binary {
  header:=codec.AppendVarint(nil,101)
  header=codec.AppendVarint(header,0)
  return append(header,captureBinary(t,sequence,data,document)...)
 }
 // Fixed valid UTF-8 samples also carry independently owned JSON and Bytes.
 return []byte(fmt.Sprintf("{\"$id\":%d,\"$api\":\"capture\",\"sequence\":%d,\"data\":%q,\"document\":%s,\"payload\":%q}",sequence,sequence,data,document,base64.StdEncoding.EncodeToString([]byte(data))))
}
func captureRPC(t *testing.T,router *api.Router)captureCall{
 t.Helper()
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 counted:=&captureListener{Listener:listener}
 server:=rpc.NewServer(router)
 done:=make(chan error,1)
 go func(){done<-server.Serve(counted)}()
 client:=rpc.NewClient(listener.Addr().String())
 t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)};if counted.accepted.Load()!=1{t.Error("RPC connections",counted.accepted.Load())}})
 return func(ctx context.Context,sequence int64,data,document string){
  body,err:=client.CallWithMaskContext(ctx,"",101,nil,captureBinary(t,sequence,data,document))
  if err!=nil||!bytes.Equal(body,codec.AppendSvarint(nil,42)){t.Fatal("RPC response",body,err)}
 }
}
func captureHTTP(t *testing.T,router *api.Router,binary,h2,dedicated bool)captureCall{
 t.Helper()
 server:=httptest.NewUnstartedServer(router)
 var connections atomic.Int64
 server.Config.ConnState=func(_ net.Conn,state http.ConnState){if state==http.StateNew{connections.Add(1)}}
 server.EnableHTTP2=h2
 var transport *h2transport.Server
 if dedicated{
  var err error
  transport,err=h2transport.NewServer(router);if err!=nil{t.Fatal(err)}
  if err:=transport.ConfigureHTTPServer(server.Config);err!=nil{t.Fatal(err)}
 }
 if h2{server.StartTLS()}else{server.Start()}
 client:=server.Client()
 t.Cleanup(func(){client.CloseIdleConnections();if transport!=nil{transport.Close()};server.Close();if connections.Load()!=1{t.Error("HTTP connections",connections.Load())}})
 return func(ctx context.Context,sequence int64,data,document string){
  req,err:=http.NewRequestWithContext(ctx,"POST",server.URL,bytes.NewReader(captureBody(t,binary,sequence,data,document)))
  if err!=nil{t.Fatal(err)}
  contentType:="application/json";if binary{contentType="application/x-luxo"}
  req.Header.Set("Content-Type",contentType)
  if binary{req.Header.Set("X-Luxo-Mode","binary")}
  response,err:=client.Do(req);if err!=nil{t.Fatal(err)}
  body,readErr:=io.ReadAll(response.Body);response.Body.Close()
  major:=1;if h2{major=2}
  if response.StatusCode!=200||response.ProtoMajor!=major||readErr!=nil{t.Fatal("HTTP response",response.Status,response.Proto,string(body),readErr)}
  if binary {if !bytes.Equal(body,codec.AppendSvarint(nil,42)){t.Fatalf("Binary HTTP response: %x",body)};return}
  var envelope struct{Data *int64 ` + "`" + `json:"data"` + "`" + `}
  if err:=json.Unmarshal(body,&envelope);err!=nil||envelope.Data==nil||*envelope.Data!=42{t.Fatal("JSON HTTP response",string(body),err)}
 }
}
func captureWS(t *testing.T,router *api.Router,binary bool)captureCall{
 t.Helper()
 server:=httptest.NewServer(router)
 conn,_,err:=websocket.Dial(t.Context(),"ws"+strings.TrimPrefix(server.URL,"http"),nil)
 if err!=nil{server.Close();t.Fatal(err)}
 t.Cleanup(func(){conn.CloseNow();server.Close()})
 return func(ctx context.Context,sequence int64,data,document string){
  mode:=websocket.MessageText
  body:=captureBody(t,binary,sequence,data,document)
  if binary{mode=websocket.MessageBinary;header:=codec.AppendVarint([]byte{api.BinaryFrameCallRequest},uint64(sequence));body=append(header,body...)}
  if err:=conn.Write(ctx,mode,body);err!=nil{t.Fatal(err)}
  got,reply,err:=conn.Read(ctx)
  if err!=nil||got!=mode{t.Fatal("WS response",got,err)}
  if binary{want:=codec.AppendSvarint(codec.AppendVarint([]byte{api.BinaryFrameCallSuccess},uint64(sequence)),42);if !bytes.Equal(reply,want){t.Fatalf("Binary WS response: %x",reply)};return}
  var envelope struct{ID int64 ` + "`" + `json:"$id"` + "`" + `;Data *int64 ` + "`" + `json:"data"` + "`" + `}
  if err:=json.Unmarshal(reply,&envelope);err!=nil||envelope.ID!=sequence||envelope.Data==nil||*envelope.Data!=42{t.Fatal("JSON WS response",string(reply),err)}
 }
}
func TestAsyncStringCaptureTransports(t *testing.T){
 for _,transport:=range []string{"LRPC","H1/JSON","H1/Binary","H2/JSON","H2/Binary","LuxoH2/JSON","LuxoH2/Binary","WS/JSON","WS/Binary"}{
  t.Run(transport,func(t *testing.T){
   resolver:=&captureResolver{entered:make(chan int64,3),release:make(chan struct{}),results:make(chan captureResult,3)}
   router:=captureRouter(t,resolver)
   var call captureCall
   switch{
   case transport=="LRPC":call=captureRPC(t,router)
   case strings.HasPrefix(transport,"WS/"):call=captureWS(t,router,strings.HasSuffix(transport,"Binary"))
   default:call=captureHTTP(t,router,strings.HasSuffix(transport,"Binary"),strings.HasPrefix(transport,"H2/")||strings.HasPrefix(transport,"LuxoH2/"),strings.HasPrefix(transport,"LuxoH2/"))
   }
   defer resolver.finish()
   ctx,cancel:=context.WithTimeout(t.Context(),10*time.Second);defer cancel()
   samples:=[]struct{data,document string}{{"AAA","{\"n\":1}"},{"中","{\"n\":2}"},{"CCC","{\"n\":3}"}}
   for i,sample:=range samples{
    sequence:=int64(i+1)
    call(ctx,sequence,sample.data,sample.document)
    select{case entered:=<-resolver.entered:if entered!=sequence{t.Fatal("sequence",entered,sequence)};case<-ctx.Done():t.Fatal(ctx.Err())}
   }
   resolver.finish()
   seen:=[3]bool{}
   for range samples{
    select{
    case result:=<-resolver.results:
     if result.sequence<1||result.sequence>3||seen[result.sequence-1]{t.Fatal("invalid or duplicate sequence",result.sequence)};seen[result.sequence-1]=true
     want:=samples[result.sequence-1]
     if result.data!=want.data{t.Errorf("String request %d changed after response: got=%q want=%q",result.sequence,result.data,want.data)}
     if result.document!=want.document{t.Errorf("JSON request %d changed after response: got=%q want=%q",result.sequence,result.document,want.document)}
     if result.payload!=want.data{t.Errorf("Bytes request %d changed after response: got=%q want=%q",result.sequence,result.payload,want.data)}
    case<-ctx.Done():t.Fatal(ctx.Err())
    }
   }
   t.Log("String/JSON/Bytes records=3; all captured before response")
  })
 }
}
`
