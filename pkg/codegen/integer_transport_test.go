package codegen

import "testing"

func TestGeneratedDiscardedScalarStatements(t *testing.T) {
	runGeneratedFixture(t, `
fn ignored(value: Int): Int {
 value + 1
 "ignored"
 value > 0
 return 7
}
fn divide(value: Int): Int { 1 / value return 7 }
fn negate(value: Int): Int { -value return 7 }
`, `package generated
import("math";"testing")
func TestDiscardedScalarStatements(t *testing.T) {
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 if value,err:=app.ignored(t.Context(),1);err!=nil||value!=7{t.Fatal(value,err)}
 if value,err:=app.ignored(t.Context(),math.MaxInt64);err==nil||value!=0{t.Fatal("discarded addition lost its error",value,err)}
 if value,err:=app.divide(t.Context(),2);err!=nil||value!=7{t.Fatal(value,err)}
 if value,err:=app.divide(t.Context(),0);err==nil||value!=0{t.Fatal("discarded division lost its error",value,err)}
 if value,err:=app.negate(t.Context(),1);err!=nil||value!=7{t.Fatal(value,err)}
 if value,err:=app.negate(t.Context(),math.MinInt64);err==nil||value!=0{t.Fatal("discarded negation lost its error",value,err)}
}
`, StableIDs{})
}

func TestGeneratedCheckedArithmeticTransports(t *testing.T) {
	runGeneratedFixture(t, integerArithmeticSource, integerTransportAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"ArithmeticBox": {"id": 1, "value": 2, "touches": 3, "mutations": 4}},
		APIs:        map[string]int{"checked": 101}, APIParams: map[string]map[string]int{"checked": {"left": 1, "right": 2}},
	})
}

const integerTransportAssertions = `package generated
import("bytes";"errors";"fmt";"io";"math";"net";"net/http";"net/http/httptest";"sync/atomic";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/h2transport";"github.com/light-speak/luxo/pkg/lux/rpc")
func integerRouter(t *testing.T)*api.Router{
 t.Helper();router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)};return router
}
func integerParams(left,right int64)[]byte{var e codec.Encoder;e.WriteFieldInt(1,left);e.WriteFieldInt(2,right);e.WriteEnd();return e.Bytes()}
func TestCheckedHTTP(t *testing.T){
 for _,mode:=range []string{"H1","H2","LuxoH2"}{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprint(mode,"/",binary),func(t *testing.T){
  router:=integerRouter(t);server:=httptest.NewUnstartedServer(router);var accepted atomic.Int64
  server.Config.ConnState=func(_ net.Conn,state http.ConnState){if state==http.StateNew{accepted.Add(1)}}
  server.EnableHTTP2=mode!="H1"
  var dedicated *h2transport.Server
  if mode=="LuxoH2"{var err error;dedicated,err=h2transport.NewServer(router);if err!=nil{t.Fatal(err)};if err:=dedicated.ConfigureHTTPServer(server.Config);err!=nil{t.Fatal(err)}}
  if mode=="H1"{server.Start()}else{server.StartTLS()};client:=server.Client()
  defer func(){client.CloseIdleConnections();if dedicated!=nil{dedicated.Close()};server.Close();if accepted.Load()!=1{t.Error("connections",accepted.Load())}}()
  for _,sample:=range []struct{left,right int64;status int}{{40,2,200},{math.MaxInt64,1,400},{40,2,200}}{
   payload:=fmt.Appendf(nil,"{\"$api\":\"checked\",\"left\":%d,\"right\":%d}",sample.left,sample.right)
   if binary{header:=codec.AppendVarint(nil,101);header=codec.AppendVarint(header,0);payload=append(header,integerParams(sample.left,sample.right)...)}
   request,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL,bytes.NewReader(payload));if err!=nil{t.Fatal(err)}
   request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   response,err:=client.Do(request);if err!=nil{t.Fatal(err)};body,readErr:=io.ReadAll(response.Body);response.Body.Close()
   major:=2;if mode=="H1"{major=1};if response.ProtoMajor!=major||response.StatusCode!=sample.status||readErr!=nil{t.Fatal(response.Status,response.Proto,string(body),readErr)}
   if binary&&sample.status==200&&!bytes.Equal(body,codec.AppendSvarint(nil,42)){t.Fatalf("result %x",body)}
   if sample.status==400&&(!bytes.Contains(body,[]byte("Int.+"))||!bytes.Contains(body,[]byte("overflow"))){t.Fatalf("missing arithmetic error data: %s",body)}
  }
 })}}
}
type integerListener struct{net.Listener;accepted atomic.Int64}
func(l *integerListener)Accept()(net.Conn,error){conn,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return conn,err}
func TestCheckedLRPC(t *testing.T){
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};counted:=&integerListener{Listener:listener}
 server:=rpc.NewServer(integerRouter(t));done:=make(chan error,1);go func(){done<-server.Serve(counted)}()
 client:=rpc.NewClient(listener.Addr().String());defer func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)};if counted.accepted.Load()!=1{t.Error("connections",counted.accepted.Load())}}()
 for _,sample:=range []struct{left,right int64;invalid bool}{{40,2,false},{math.MaxInt64,1,true},{40,2,false}}{
  body,err:=client.CallWithMaskContext(t.Context(),"",101,nil,integerParams(sample.left,sample.right))
  if !sample.invalid{if err!=nil||!bytes.Equal(body,codec.AppendSvarint(nil,42)){t.Fatal(body,err)};continue}
  var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400||failure.Data==nil{t.Fatal(err)}
  data,err:=failure.Data.AppendJSON(nil);if err!=nil||!bytes.Contains(data,[]byte("Int.+"))||!bytes.Contains(data,[]byte("overflow")){t.Fatal(string(data),err)}
 }
}
`
