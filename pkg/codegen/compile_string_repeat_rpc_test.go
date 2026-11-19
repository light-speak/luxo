package codegen

import "testing"

func TestGeneratedCheckedRepeatOverLRPC(t *testing.T) {
	runGeneratedFixture(t, `
fn repeatService(value: String, count: Int): String @service { return value.repeat(count) }
`, `package generated
import("bytes";"errors";"math";"net";"strings";"sync/atomic";"testing";
 "github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";
 "github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
type countedRepeatListener struct{net.Listener;accepted atomic.Int32}
func(listener *countedRepeatListener)Accept()(net.Conn,error){connection,err:=listener.Listener.Accept();if err==nil{listener.accepted.Add(1)};return connection,err}
func TestRepeatLRPC(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 rawListener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 listener:=&countedRepeatListener{Listener:rawListener}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{value string;count int64;cause string}{
  {"ab",2,""},{"ab",-1,"repeat count must not be negative"},{"ab",1,""},
  {"ab",math.MaxInt64,"repeat result length overflows int"},{"",math.MaxInt64,""},
  {"abc",math.MaxInt64,"repeat result length overflows int"},{"中",2,""},
 }{
  var params codec.Encoder;if err:=params.WriteFieldString(1,sample.value);err!=nil{t.Fatal(err)};params.WriteFieldInt(2,sample.count);params.WriteEnd()
  body,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs["svc:repeatService"].ID,nil,params.Bytes())
  if sample.cause!=""{
   failure,ok:=errors.AsType[*luxerrors.AppError](err);if !ok||failure.Code!=400||failure.Internal||failure.Data==nil{t.Fatal(body,err)}
   raw,writeErr:=failure.Data.AppendJSON(nil);if writeErr!=nil||!bytes.Contains(raw,[]byte("String.repeat"))||!bytes.Contains(raw,[]byte(sample.cause)){t.Fatal(string(raw),writeErr)}
   continue
  }
  expected,encodeErr:=codec.AppendString(nil,strings.Repeat(sample.value,int(sample.count)))
  if err!=nil||encodeErr!=nil||!bytes.Equal(body,expected){t.Fatal(sample.value,sample.count,body,expected,err,encodeErr)}
 }
 if listener.accepted.Load()!=1{t.Fatalf("business errors replaced the connection: %d",listener.accepted.Load())}
}
`, StableIDs{})
}
