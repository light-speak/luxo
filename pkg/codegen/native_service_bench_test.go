package codegen

const nativeServiceAuthorizationBenchmark = `package generated
import("context";"net/http";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/schema")
var nativeServiceByteSink byte
func BenchmarkNativeServiceAuthorization(b *testing.B){
 b.Run("RegisterSchema",func(b *testing.B){b.ReportAllocs();for b.Loop(){catalog:=schema.New();if err:=RegisterSchema(catalog);err!=nil{b.Fatal(err)}}})
 b.Setenv("JWT_SECRET","native-service-benchmark-only");b.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();b.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();b.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{b.Fatal(err)};token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":42,\"role\":\"admin\"}")...),nil});if err!=nil{b.Fatal(err)}
 var ctx context.Context
 middleware:=luvia.AuthMiddleware(cfg,http.HandlerFunc(func(_ http.ResponseWriter,request *http.Request){ctx=request.Context()}))
 request:=httptest.NewRequest("POST","/",nil);request.Header.Set("Authorization","Bearer "+token);middleware.ServeHTTP(httptest.NewRecorder(),request)
 if ctx==nil||luvia.Identity(ctx)==nil{b.Fatal("missing verified benchmark identity")}
 application:=&App{Resolver:&nativeServiceResolver{}};router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{b.Fatal(err)};if err:=RegisterServiceFns(router,application);err!=nil{b.Fatal(err)}
 for _,sample:=range []struct{name string;handler api.HandlerFunc}{
  {"nativePlain",_handleService_nativePlain(application)},{"nativeAuth",_handleService_nativeAuth(application)},{"nativeRole",_handleService_nativeRole(application)},{"nativePermission",_handleService_nativePermission(application)},
  {"compiledRole",_handleService_compiledRole(application)},{"compiledPermission",_handleService_compiledPermission(application)},
 }{b.Run(sample.name,func(b *testing.B){
  name:="svc:"+sample.name;body,err:=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"value":int64(42)},router.Registry.ParamOrder(name));if err!=nil{b.Fatal(err)}
  var req api.Request;if err:=router.Registry.ParseBinaryRequestInto(body,&req);err!=nil{b.Fatal(err)};req.Buf=&api.ResponseBuf{B:make([]byte,0,32)}
  if err:=sample.handler(ctx,&req);err!=nil||len(req.Buf.B)!=1||req.Buf.B[0]!=84{b.Fatal("bad initial handler output",req.Buf.B,err)}
  b.ReportAllocs();b.ResetTimer();for b.Loop(){req.Buf.Reset();if err:=sample.handler(ctx,&req);err!=nil{b.Fatal(err)};nativeServiceByteSink=req.Buf.B[0]}
 })}
}
`
