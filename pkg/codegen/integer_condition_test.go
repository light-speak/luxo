package codegen

import "testing"

func TestGeneratedCheckedConditionObjectLiteral(t *testing.T) {
	runGeneratedFixture(t, `type Guard { value: Int }
type Guarded {
 visible: String? @visible((Guard { value: 1 }).value + 1 > 0)
 hidden: String? @visible((Guard { value: 1 }).value + 1 < 0)
}`, `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
func TestCheckedConditionObjectLiteral(t *testing.T){
 shown,hidden:="shown","hidden-value";value:=Guarded{Visible:&shown,Hidden:&hidden}
 var output api.ResponseBuf
 if err:=value.WriteLuxo(&output,nil);err!=nil{t.Fatal(err)}
 if !bytes.Contains(output.B,[]byte("shown"))||bytes.Contains(output.B,[]byte("hidden-value")){t.Fatal("visibility changed",output.B)}
 registry:=schema.New();if err:=RegisterSchema(registry);err!=nil{t.Fatal(err)}
 raw,err:=schema.BinaryToJSON(nil,output.B,registry.LookupModel("Guarded"),registry)
 if err!=nil||string(raw)!="{\"visible\":\"shown\"}"{t.Fatal("visible object condition",string(raw),output.B,err)}
}`, StableIDs{ModelFields: map[string]map[string]int{"Guard": {"value": 1}, "Guarded": {"visible": 1, "hidden": 2}}})
}

func TestGeneratedCheckedDurationUnitCondition(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id, quota]) { id: Int @id quota: Int }
type Guarded { value: String? @visible(my.quota.days == my.id.days) }`, `package generated
import("errors";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestDurationUnitCondition(t *testing.T){
 cfg:=&auth.Config{Secret:"unit-condition",Expires:time.Hour}
 for _,sample:=range []struct{raw string;fails bool}{{"{\"id\":1,\"quota\":1}",false},{"{\"id\":1,\"quota\":9223372036854775807}",true}}{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
  text:="value";value:=Guarded{Value:&text};buf:=&api.ResponseBuf{Identity:claims}
  err=value.WriteLuxo(buf,mask)
  if sample.fails{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Fatal(err)}}else if err!=nil{t.Fatal(err)}
 }
}`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "quota": 2}, "Guarded": {"value": 1}}})
}

const integerConditionSource = `model User @noTime @withAuth(stores: [id, quota]) { id: Int @id quota: Int }
type Guarded { secret: String? @visible(my.quota + 1 < 0) safe: String? @visible(true || my.quota + 1 < 0) }
api restricted(): Int @auth(permission: { my.quota + 1 < 0 }) { return 7 }
api skipped(): Int @auth(permission: { true || my.quota + 1 < 0 }) { return 7 }
event Updated(value: Int)
api watch(limit: Int): Int @stream(Updated) { it.value + limit > 0 }
api skippedWatch(limit: Int): Int @stream(Updated) { true || it.value + limit > 0 }
api divided(divisor: Int): Int @stream(Updated) { it.value / divisor > 0 }
event Delayed(value: Duration)
api delayed(amount: Duration): Duration @stream(Delayed) { it.value + amount == 3ms }`

func TestGeneratedCheckedIntegerConditions(t *testing.T) {
	runGeneratedFixture(t, integerConditionSource, `package generated
import("context";"net";"math";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/rpc";"bytes";"errors";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestCheckedConditions(t *testing.T){
 cfg:=&auth.Config{Secret:"checked-conditions",Expires:time.Hour}
 token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":1,\"quota\":9223372036854775807}")...),nil});if err!=nil{t.Fatal(err)}
 claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
 private,safe:="private","safe";value:=Guarded{Secret:&private,Safe:&safe}
 for _,columnar:=range []bool{false,true}{
  mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
  buf:=&api.ResponseBuf{Identity:claims}
  if columnar{err=WriteColumnarGuarded(buf,[]Guarded{value},mask)}else{err=value.WriteLuxo(buf,mask)}
  var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Errorf("visible overflow must fail: columnar=%v err=%v",columnar,err)}
  mask,err=codec.AppendSelectionMask(nil,[]byte{2},nil);if err!=nil{t.Fatal(err)}
  buf=&api.ResponseBuf{Identity:claims}
  if columnar{err=WriteColumnarGuarded(buf,[]Guarded{value},mask)}else{err=value.WriteLuxo(buf,mask)}
  if err!=nil||!bytes.Contains(buf.B,[]byte("safe")){t.Fatal("short circuit",columnar,err)}
 }
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{name string;status int}{{"restricted",403},{"skipped",200}}{
  request:=httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+sample.name+"\"}"));request.Header.Set("Content-Type","application/json");request.Header.Set("Authorization","Bearer "+token)
  out:=httptest.NewRecorder();endpoint.ServeHTTP(out,request);if out.Code!=sample.status{t.Errorf("permission %s: %d %s",sample.name,out.Code,out.Body)}
 }
}
func TestCheckedStreamCondition(t *testing.T){
 allowed,err:=matchWatch(9223372036854775807,&WatchStreamParams{Limit:1},nil)
 var failure *luxerrors.AppError;if allowed||!errors.As(err,&failure)||failure.Code!=400{t.Fatal("stream overflow",allowed,err)}
 if allowed,err=matchWatch(40,&WatchStreamParams{Limit:2},nil);err!=nil||!allowed{t.Fatal(allowed,err)}
 if allowed,err=matchSkippedWatch(9223372036854775807,&SkippedWatchStreamParams{Limit:1},nil);err!=nil||!allowed{t.Fatal("skipped stream",allowed,err)}
 if allowed,err=matchDivided(1,&DividedStreamParams{},nil);allowed||!errors.As(err,&failure)||failure.Code!=400{t.Fatal("stream division",allowed,err)}
 if allowed,err=matchDelayed(9223372036854775807,&DelayedStreamParams{Amount:1},nil);allowed||!errors.As(err,&failure)||failure.Code!=400{t.Fatal("stream duration",allowed,err)}
 if allowed,err=matchDelayed(int64(time.Millisecond),&DelayedStreamParams{Amount:2*time.Millisecond},nil);err!=nil||!allowed{t.Fatal("valid duration",allowed,err)}
}
func TestCheckedStreamLRPC(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(8);defer bus.Close();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 var subs []*rpc.Subscription
 for _,limit:=range []int64{1,-1}{
  var params codec.Encoder;params.WriteFieldInt(1,limit);params.WriteEnd()
  sub,err:=client.SubscribeContext(ctx,"",3,nil,params.Bytes());if err!=nil{t.Fatal(err)};defer sub.Close();subs=append(subs,sub)
 }
 if err:=bus.Emit(ctx,"Updated",&UpdatedEvent{Value:math.MaxInt64});err!=nil{t.Fatal(err)}
 select{case _,open:=<-subs[0].Messages():if open{t.Fatal("overflow sent success")};case <-ctx.Done():t.Fatal(ctx.Err())}
 var failure *luxerrors.AppError;if !errors.As(subs[0].Err(),&failure)||failure.Code!=400{t.Fatal("missing stream failure",subs[0].Err())}
 for _,want:=range []int64{math.MaxInt64,42}{
  if want==42{if err:=bus.Emit(ctx,"Updated",&UpdatedEvent{Value:42});err!=nil{t.Fatal(err)}}
  select{case data,open:=<-subs[1].Messages():
   if !open{t.Fatal("healthy subscription closed",subs[1].Err())};value,end:=codec.ReadSvarint(data,0);if end!=len(data)||value!=want{t.Fatal(value,end)}
  case <-ctx.Done():t.Fatal(ctx.Err())}
 }
}

`, StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1, "quota": 2}, "Guarded": {"secret": 1, "safe": 2}},
		EventFields: map[string]map[string]int{"Updated": {"value": 1}, "Delayed": {"value": 1}},
		APIs:        map[string]int{"restricted": 1, "skipped": 2, "watch": 3, "skippedWatch": 4, "divided": 5, "delayed": 6},
		APIParams:   map[string]map[string]int{"watch": {"limit": 1}, "skippedWatch": {"limit": 1}, "divided": {"divisor": 1}, "delayed": {"amount": 1}},
	}, integerConditionBenchmarks)
}

func TestGeneratedCheckedIdentityStreamConditions(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id, quota]) { id: Int @id quota: Int }
event Updated(value: Int)
api checkedIdentity(): Int @auth @stream(Updated) { !(my.quota + 1 < it.value) }
api skippedIdentity(): Int @auth @stream(Updated) { true || my.quota + 1 < it.value }
api falseIdentity(): Int @auth @stream(Updated) { false && my.quota + 1 < it.value }
api readThenTrue(): Int @auth @stream(Updated) { my.quota + 1 >= it.value || true }`, checkedIdentityStreamAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1, "quota": 2}},
		EventFields: map[string]map[string]int{"Updated": {"value": 1}},
		APIs:        map[string]int{"checkedIdentity": 1, "skippedIdentity": 2, "falseIdentity": 3, "readThenTrue": 4},
	})
}

const checkedIdentityStreamAssertions = `package generated
import("errors";"testing";"time";"github.com/light-speak/luxo/pkg/lux/auth";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestCheckedIdentityMatches(t *testing.T) {
 cfg:=&auth.Config{Secret:"checked-identity-stream",Expires:time.Hour}
 for _,sample:=range []struct{name,raw string;identity any;matched,valid bool;code int}{
  {name:"valid",raw:"{\"id\":1,\"quota\":1}",matched:true,valid:true},
  {name:"below",raw:"{\"id\":1,\"quota\":-2}",valid:true},
  {name:"min",raw:"{\"id\":1,\"quota\":-9223372036854775808}",valid:true},
  {name:"missing",raw:"{\"id\":1}"},
  {name:"null",raw:"{\"id\":1,\"quota\":null}"},
  {name:"string",raw:"{\"id\":1,\"quota\":\"1\"}"},
  {name:"decimal",raw:"{\"id\":1,\"quota\":1.0}"},
  {name:"overflow",raw:"{\"id\":1,\"quota\":9223372036854775807}",valid:true,code:400},
  {name:"nil"},
  {name:"map",identity:map[string]any{"id":int64(1),"quota":int64(1)}},
  {name:"model",identity:&User{Id:1,Quota:1}},
 } {
  identity:=sample.identity
  if sample.raw!="" {
   token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
   claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)};identity=claims
  }
  for _,matcher:=range []struct{name string;run func(any)(bool,error);want bool;code int}{
   {"negated",func(identity any)(bool,error){return matchCheckedIdentity(1,&CheckedIdentityStreamParams{},identity)},sample.matched,sample.code},
   {"read_then_true",func(identity any)(bool,error){return matchReadThenTrue(1,&ReadThenTrueStreamParams{},identity)},sample.valid,sample.code},
   {"short_true",func(identity any)(bool,error){return matchSkippedIdentity(1,&SkippedIdentityStreamParams{},identity)},true,0},
   {"short_false",func(identity any)(bool,error){return matchFalseIdentity(1,&FalseIdentityStreamParams{},identity)},false,0},
  } {
   t.Run(sample.name+"/"+matcher.name,func(t *testing.T){
    allowed,err:=matcher.run(identity)
    if matcher.code!=0 {var failure *luxerrors.AppError;if allowed||!errors.As(err,&failure)||failure.Code!=matcher.code{t.Fatalf("allowed=%v error=%v want code=%d",allowed,err,matcher.code)};return}
    if err!=nil||allowed!=matcher.want{t.Fatalf("allowed=%v error=%v want=%v",allowed,err,matcher.want)}
   })
  }
 }
}
`
