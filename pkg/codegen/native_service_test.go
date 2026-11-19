package codegen

import "testing"

func TestGeneratedNativeServiceAuthorization(t *testing.T) {
	runGeneratedFixture(t, `
fn loadBase(value: Int): Result<Int> @native
fn compiledNested(value: Int): Int @service @auth(role: "admin") { return loadBase(value)? }
fn nativePlain(value: Int): Int @service @native
fn nativeAuth(value: Int): Int @service @native @auth
fn nativeVoid() @service @native @auth
fn nativeRole(value: Int): Int @service @native @auth(role: "admin")
fn compiledRole(value: Int): Int @service @auth(role: "admin") { return value }
fn nativePermission(value: Int): Int @service @native @auth(permission: { my.role != "blocked" })
fn compiledPermission(value: Int): Int @service @auth(permission: { my.role != "blocked" }) { return value }
`, nativeServiceAuthAssertions, StableIDs{}, nativeServiceAuthorizationBenchmark)
}

const nativeServiceAuthAssertions = `package generated
import("bytes";"context";"errors";"net";"sync/atomic";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
type nativeServiceResolver struct{calls atomic.Int64}
func(r *nativeServiceResolver)LoadBase(ctx context.Context,value int64)(int64,error){return r.NativeRole(ctx,value)}
func(r *nativeServiceResolver)NativePlain(ctx context.Context,value int64)(int64,error){return r.NativeRole(ctx,value)}
func(r *nativeServiceResolver)NativeAuth(ctx context.Context,value int64)(int64,error){return r.NativeRole(ctx,value)}
func(r *nativeServiceResolver)NativeVoid(context.Context)error{r.calls.Add(1);return nil}
func(r *nativeServiceResolver)NativeRole(_ context.Context,value int64)(int64,error){r.calls.Add(1);if value<0{return 0,luxerrors.NotFound};return value,nil}
func(r *nativeServiceResolver)NativePermission(ctx context.Context,value int64)(int64,error){return r.NativeRole(ctx,value)}
func TestServiceAuthorizationOverRPC(t *testing.T){
 t.Setenv("JWT_SECRET","native-service-boundary-test-only");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;resolver:=&nativeServiceResolver{}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterServiceFns(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,name:=range []string{"nativeRole","compiledRole","nativePermission","compiledPermission"}{definition:=router.Schema.APIs["svc:"+name];found:=false;for _,directive:=range definition.Directives{found=found||directive=="auth"};if !found{t.Error("auth directive missing from service Schema",name)}}
 for _,sample:=range []struct{name,claims string;roleStatus,permissionStatus int}{
  {"anonymous","",401,401},{"invalid","",401,401},
  {"missing","{\"id\":42}",403,403},{"null","{\"id\":42,\"role\":null}",403,403},
  {"number","{\"id\":42,\"role\":1}",403,403},{"blocked","{\"id\":42,\"role\":\"blocked\"}",403,403},
  {"member","{\"id\":9007199254740993,\"role\":\"member\"}",403,200},{"admin","{\"id\":9007199254740993,\"role\":\"admin\"}",200,200},
 }{authStatus:=200;if sample.claims==""{authStatus=401};for _,method:=range []struct{name string;status int;native bool}{
  {"compiledNested",sample.roleStatus,true},{"nativeAuth",authStatus,true},{"nativeVoid",authStatus,true},{"nativeRole",sample.roleStatus,true},{"compiledRole",sample.roleStatus,false},{"nativePermission",sample.permissionStatus,true},{"compiledPermission",sample.permissionStatus,false},
 }{t.Run(sample.name+"/"+method.name,func(t *testing.T){
  var bearer string;if sample.claims!=""{bearer,err=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.claims...),nil});if err!=nil{t.Fatal(err)}}else if sample.name=="invalid"{bearer="invalid"}
  var params codec.Encoder;if method.name!="nativeVoid"{params.WriteFieldInt(1,42)};params.WriteEnd();before:=resolver.calls.Load()
  data,callErr:=client.CallWithMaskContext(t.Context(),bearer,router.Schema.APIs["svc:"+method.name].ID,nil,params.Bytes())
  if method.status==200 {expected:=codec.AppendSvarint(nil,42);if method.name=="nativeVoid"{expected=nil};if callErr!=nil||!bytes.Equal(data,expected){t.Fatalf("authorized call failed: %x %v",data,callErr)}}else{var failure *luxerrors.AppError;if !errors.As(callErr,&failure)||failure.Code!=method.status||len(data)!=0{t.Errorf("got %x/%v, want error %d",data,callErr,method.status)}}
  want:=before;if method.native&&method.status==200{want++};if resolver.calls.Load()!=want{t.Error("unauthorized native resolver executed")}
 })}}
 admin,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":42,\"role\":\"admin\"}")...),nil});if err!=nil{t.Fatal(err)}
 for _,method:=range []string{"nativeRole","compiledRole"}{for _,bearer:=range []string{"",admin}{for _,text:=range []bool{false,true}{
  var params codec.Encoder;if text{params.WriteFieldString(1,"wrong-type")};params.WriteEnd();before:=resolver.calls.Load()
  data,err:=client.CallWithMaskContext(t.Context(),bearer,router.Schema.APIs["svc:"+method].ID,nil,params.Bytes());var failure *luxerrors.AppError;want:=400
  if !errors.As(err,&failure)||failure.Code!=want||len(data)!=0||resolver.calls.Load()!=before{t.Fatalf("invalid RPC params must be rejected before native execution: %s bearer=%t text=%t got %x %v",method,bearer!="",text,data,err)}
 }}}
 for _,handler:=range []api.HandlerFunc{_handleService_nativeRole(&App{Resolver:resolver}),_handleService_compiledRole(&App{Resolver:resolver})}{before:=resolver.calls.Load();err:=handler(t.Context(),&api.Request{});var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=401||resolver.calls.Load()!=before{t.Fatal("generated auth did not precede parameter reads",err)}}
 var failed codec.Encoder;failed.WriteFieldInt(1,-1);failed.WriteEnd()
 data,err:=client.CallWithMaskContext(t.Context(),admin,router.Schema.APIs["svc:nativeRole"].ID,nil,failed.Bytes());var failure *luxerrors.AppError
 if !errors.As(err,&failure)||failure.Code!=404||len(data)!=0{t.Fatalf("native error became success: %x %v",data,err)}
 var valid codec.Encoder;valid.WriteFieldInt(1,42);valid.WriteEnd();data,err=client.CallWithMaskContext(t.Context(),admin,router.Schema.APIs["svc:nativeRole"].ID,nil,valid.Bytes());if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,42)){t.Fatal("error contaminated next RPC",data,err)}
}
`

func TestGeneratedNativeServiceResultContract(t *testing.T) {
	runGeneratedFixture(t, `
fn wrappedInt(): Result<Int> @service @native
fn wrappedOptional(present: Boolean): Result<Int?> @service @native
fn wrappedList(): Result<[Int]> @service @native
`, nativeServiceResultAssertions, StableIDs{})
}

const nativeServiceResultAssertions = `package generated
import("context";"net";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc")
type nativeServiceResultResolver struct{}
func(nativeServiceResultResolver)WrappedInt(context.Context)(int64,error){return 9007199254740993,nil}
func(nativeServiceResultResolver)WrappedOptional(_ context.Context,present bool)(*int64,error){if !present{return nil,nil};value:=int64(0);return &value,nil}
func(nativeServiceResultResolver)WrappedList(context.Context)([]int64,error){return []int64{0,9007199254740993},nil}
func TestServiceResultSchemaAndWire(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterServiceFns(router,&App{Resolver:nativeServiceResultResolver{}});err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name string;list,nullable bool}{{"wrappedInt",false,false},{"wrappedOptional",false,true},{"wrappedList",true,false}}{
  definition:=router.Schema.APIs["svc:"+sample.name];if !definition.Internal||definition.ReturnType!="Int"||definition.ReturnList!=sample.list||definition.ReturnNullable!=sample.nullable{t.Errorf("Go Result wrapper leaked into wire Schema: %+v",definition)}
 }
 if t.Failed(){return}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name string;present bool;json string}{{"wrappedInt",false,"9007199254740993"},{"wrappedOptional",false,"null"},{"wrappedOptional",true,"0"},{"wrappedList",false,"[0,9007199254740993]"}}{
  name:="svc:"+sample.name;var params codec.Encoder;if sample.name=="wrappedOptional"{params.WriteFieldBool(1,sample.present)};params.WriteEnd()
  data,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs[name].ID,nil,params.Bytes());if err!=nil{t.Fatal(err)}
  value,err:=router.StreamPayloadJSON(name,data);if err!=nil||string(value)!=sample.json{t.Fatalf("%s: %s %v want %s",name,value,err,sample.json)}
 }
}
`

func TestGeneratedNativeServiceSubjectAndPurpose(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime @withAuth(stores: [id, role], default: true) { id: Int @id role: String }
model Admin @noTime @withAuth(stores: [id, role]) { id: Int @id role: String }
fn nativeIdentity(value: Int): Result<Int> @service @native @auth
fn nativeAdmin(value: Int): Result<Int> @service @native @auth(role: "admin")
fn nativeExactIdentity(value: Int): Result<Int> @service @native @auth(permission: { my.id == 9007199254740993 && my.role == "admin" })
`, nativeServiceSubjectAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}, "Admin": {"id": 1, "role": 2}},
	})
}

const nativeServiceSubjectAssertions = `package generated
import("bytes";"context";"errors";"net";"sync/atomic";"testing";"time"
 "github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
type subjectServiceResolver struct{ calls atomic.Int64;apiIDs map[string]int }
func(r *subjectServiceResolver)NativeIdentity(ctx context.Context,value int64)(int64,error){
 r.calls.Add(1);if value<0{return 0,luxerrors.NotFound};identity:=luvia.Identity(ctx)
 if identity==nil{return 0,luxerrors.Unauthorized};return identity.ID(),nil
}
func(r *subjectServiceResolver)NativeAdmin(ctx context.Context,value int64)(int64,error){return r.NativeIdentity(ctx,value)}
func(r *subjectServiceResolver)NativeExactIdentity(ctx context.Context,value int64)(int64,error){return r.NativeIdentity(ctx,value)}
type countedServiceListener struct{net.Listener;accepted atomic.Int64}
func(l *countedServiceListener)Accept()(net.Conn,error){conn,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return conn,err}
type subjectServiceSample struct{name,token string;id int64;auth,role,permission int}

func subjectServiceConfig(t *testing.T)*auth.Config{
 t.Helper();t.Setenv("JWT_SECRET","subject-purpose-native-service-test-only");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","24h")
 auth.ResetConfig();auth.ResetVerifyCache();t.Cleanup(auth.ResetConfig);t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)};return cfg
}
func subjectServiceTokens(t *testing.T,cfg *auth.Config)[]subjectServiceSample{
 t.Helper();user:=&User{Id:9007199254740993,Role:"admin"};member:=&User{Id:user.Id,Role:"member"}
 lower:=&User{Id:9007199254740992,Role:"admin"}
 sign:=func(token string,err error)string{t.Helper();if err!=nil{t.Fatal(err)};return token}
 access:=sign(user.CreateToken());refresh:=sign(user.CreateRefreshToken())
 return []subjectServiceSample{
  {"admin",access,user.Id,200,200,200},
  {"anonymous","",0,401,401,401},{"bad-signature","not-a-token",0,401,401,401},
  {"other-subject",sign((&Admin{Id:user.Id,Role:"admin"}).CreateToken()),0,401,401,401},
  {"unknown-subject",sign(auth.SignForSubject(cfg,"Unknown",user.AppendClaimsJSON)),0,401,401,401},
  {"unbound",sign(auth.Sign(cfg,user.AppendClaimsJSON)),0,401,401,401},
  {"refresh",refresh,0,401,401,401},
  {"expired",sign(auth.SignForSubjectWithExpiry(cfg,"User",user.AppendClaimsJSON,-time.Hour)),0,401,401,401},
  {"member",sign(member.CreateToken()),member.Id,200,403,403},
  {"adjacent-int",sign(lower.CreateToken()),lower.Id,200,200,403},
  {"admin-again",access,user.Id,200,200,200},
 }
}
func subjectServiceClient(t *testing.T)(*rpc.Client,*subjectServiceResolver,*countedServiceListener){
 t.Helper();router:=luvia.New().Router;resolver:=&subjectServiceResolver{}
 if router.InternalRequestContext==nil{t.Fatal("missing production RPC auth context hook")}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 resolver.apiIDs=make(map[string]int)
 for _,name:=range []string{"nativeIdentity","nativeAdmin","nativeExactIdentity"}{resolver.apiIDs[name]=router.Schema.APIs["svc:"+name].ID}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 counted:=&countedServiceListener{Listener:listener};server:=rpc.NewServer(router);done:=make(chan error,1)
 go func(){done<-server.Serve(counted)}()
 client:=rpc.NewClient(listener.Addr().String())
 t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}})
 return client,resolver,counted
}
func requireSubjectServiceCall(t *testing.T,client *rpc.Client,resolver *subjectServiceResolver,name string,sample subjectServiceSample,status int,value int64){
 t.Helper();var params codec.Encoder;params.WriteFieldInt(1,value);params.WriteEnd()
 before:=resolver.calls.Load();ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 apiID:=resolver.apiIDs[name];if apiID==0{t.Fatalf("missing API %s",name)}
 data,err:=client.CallWithMaskContext(ctx,sample.token,apiID,nil,params.Bytes())
 if status==200{
  if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,sample.id)){t.Fatalf("%s: identity response %x %v, want %d",name,data,err,sample.id)}
 }else{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=status||len(data)!=0{t.Fatalf("%s: %x %v, want %d",name,data,err,status)}}
 want:=before;if status==200||status==404{want++};if resolver.calls.Load()!=want{t.Fatalf("%s: resolver calls %d want %d",name,resolver.calls.Load(),want)}
}
func TestNativeServiceSubjectAndPurpose(t *testing.T){
 cfg:=subjectServiceConfig(t);client,resolver,listener:=subjectServiceClient(t)
 samples:=subjectServiceTokens(t,cfg)
 for _,sample:=range samples{t.Run(sample.name,func(t *testing.T){
  for _,method:=range []struct{name string;status int}{{"nativeIdentity",sample.auth},{"nativeAdmin",sample.role},{"nativeExactIdentity",sample.permission}}{
   t.Run(method.name,func(t *testing.T){requireSubjectServiceCall(t,client,resolver,method.name,sample,method.status,42)})
  }
 })}
 admin:=samples[0]
 for _,name:=range []string{"nativeIdentity","nativeAdmin","nativeExactIdentity"}{
  requireSubjectServiceCall(t,client,resolver,name,admin,404,-1)
  requireSubjectServiceCall(t,client,resolver,name,admin,200,42)
 }
 if got:=listener.accepted.Load();got!=1{t.Fatalf("expected one reused TCP connection, got %d",got)}
}
`
