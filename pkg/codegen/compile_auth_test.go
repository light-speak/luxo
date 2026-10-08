package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestAuthMethodRequiresIdentityModel(t *testing.T) {
	c := newCompiler(nil)
	c.models = map[string]*ast.ModelDecl{"Plain": {Name: "Plain"}, "User": {Name: "User", Directives: []*ast.Directive{{Name: "withAuth"}}}}
	for _, value := range []valType{{name: "unknown"}, {name: "Plain"}, {name: "User", isList: true}} {
		c.vars["value"] = value
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "value"}, Field: "createToken"}}
		if c.authMethod(call) != nil {
			t.Fatalf("ordinary value acquired auth method: %#v", value)
		}
	}
	if c.authMethod(&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "unregistered"}, Field: "createToken"}}) != nil {
		t.Fatal("unknown receiver accepted")
	}
}

func TestCompiledAuthMethodsPropagateErrors(t *testing.T) {
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
		Directives: []*ast.Directive{{Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "name"}}}}}}}}
	file := &ast.File{Models: []*ast.ModelDecl{model}}
	var source strings.Builder
	source.Write(generateModelFile(&semantic.Result{Files: []*ast.File{file}}, "generated", nil))
	for _, mode := range []string{"direct", "assigned", "discarded", "branch", "refresh", "expiry"} {
		c := newCompiler(nil)
		c.models = map[string]*ast.ModelDecl{"User": model}
		c.vars["user"] = valType{name: "User", isModel: true}
		c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "String"}
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "createToken"}}
		call.SetTypeTag("String")
		var expression ast.Expr = call
		switch mode {
		case "assigned":
			c.compileVal(&ast.ValStmt{Name: "value", Value: call})
			expression = &ast.Ident{Name: "value"}
		case "discarded":
			c.compileExprStmt(&ast.ExprStmt{Expr: call})
			expression = &ast.Literal{Kind: token.String, Value: "discarded"}
		case "branch":
			branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{Condition: &ast.Ident{Name: "enabled"}, Body: call}}, Else: &ast.Literal{Kind: token.String, Value: "inactive"}}
			branch.SetTypeTag("String")
			expression = branch
		case "refresh":
			call.Func.(*ast.MemberExpr).Field = "refreshToken"
			call.Args = []*ast.NamedArg{{Value: &ast.Ident{Name: "oldToken"}}}
		case "expiry":
			call.Args = []*ast.NamedArg{{Name: "expires", Value: &ast.Literal{Kind: token.Duration, Value: "5m"}}}
		}
		c.compileReturn(&ast.ReturnStmt{Value: expression})
		fmt.Fprintf(&source, "\nfunc %s(user *User, oldToken string, enabled bool) (_value string, err error) {\n%s}\n", mode, compilerOut(c))
	}
	runGeneratedProtocolPackage(t, source.String(), compiledAuthAssertions)
}

func TestAuthMethodsThroughSemanticAndGeneration(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @withAuth(stores: [id, name]) {
 id: Int @id
 name: String
}
fn issueDefault(user: User): String { user.createToken() }
fn issueCustom(user: User): String { user.createToken(expires: 5m) }
fn issueRefresh(user: User): String { user.createRefreshToken() }
fn renew(user: User, token: String): String { user.refreshToken(token: token) }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/auth")
func TestAuthFunctions(t *testing.T){
 t.Setenv("JWT_SECRET","semantic-auth-test");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 app:=&App{};user:=&User{Id:9007199254740993,Name:"東京"}
 for _,call:=range []func(context.Context,*User)(string,error){app.issueDefault,app.issueCustom}{
  signed,err:=call(t.Context(),user);if err!=nil||signed==""{t.Fatal("issue failed",err)}
  cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
  if _,err:=app.renew(t.Context(),user,signed);err==nil{t.Fatal("access token used for refresh")}
  refreshToken,err:=app.issueRefresh(t.Context(),user);if err!=nil{t.Fatal(err)}
  renewed,err:=app.renew(t.Context(),user,refreshToken);if err!=nil||renewed==""{t.Fatal("renew failed",err)}
  claims,err:=auth.Verify(cfg,renewed);if err!=nil||claims.ID()!=user.Id{t.Fatal("identity changed",err)}
  user.Name=string([]byte{0xff});if value,err:=call(t.Context(),user);err==nil||value!=""{t.Fatal("sign failure lost")};user.Name="東京"
 }
 if value,err:=app.renew(t.Context(),user,"invalid");err==nil||value!=""{t.Fatal("verify failure lost")}
 t.Setenv("JWT_REFRESH","false");auth.ResetConfig()
 if value,err:=app.issueRefresh(t.Context(),user);err==nil||value!=""{t.Fatal("disabled refresh issued a token")}
 auth.ResetConfig();t.Setenv("JWT_SECRET","")
 if value,err:=user.CreateTokenWithExpiry(1);err==nil||value!=""{t.Fatal("config failure lost")}
 if value,err:=app.issueRefresh(t.Context(),user);err==nil||value!=""{t.Fatal("refresh signing config failure lost")}
}
`)
}

const compiledAuthAssertions = `package generated
import("testing";"encoding/base64";"strconv";"strings";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/jsonutil")
func TestCompiledMethods(t *testing.T){
 t.Setenv("JWT_SECRET","compiled-auth-tests");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 valid:=&User{Name:"東京"};invalid:=&User{Name:string([]byte{0xff})}
 for _,call:=range []func(*User,string,bool)(string,error){direct,assigned,discarded,branch,expiry}{
  if value,err:=call(valid,"",true);err!=nil||value==""{t.Fatalf("valid call: %q %v",value,err)}
  if value,err:=call(invalid,"",true);err==nil||value!=""{t.Fatalf("lost signing error: %q %v",value,err)}
 }
 if value,err:=branch(invalid,"",false);err!=nil||value!="inactive"{t.Fatalf("inactive branch executed: %q %v",value,err)}
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 token,err:=auth.SignRefresh(cfg,valid.AppendClaimsJSON);if err!=nil{t.Fatal(err)}
 if value,err:=refresh(valid,token,true);err!=nil||value==""{t.Fatal("refresh failed",err)}
 if value,err:=refresh(valid,"invalid",true);err==nil||value!=""{t.Fatal("verification error swallowed")}
 custom,err:=expiry(valid,"",true);if err!=nil{t.Fatal(err)}
 body,err:=base64.RawURLEncoding.DecodeString(strings.Split(custom,".")[1]);if err!=nil{t.Fatal(err)}
 fields,err:=jsonutil.ParseObject(body);if err!=nil{t.Fatal(err)}
 exp,err:=strconv.ParseInt(string(fields["exp"]),10,64);if err!=nil{t.Fatal(err)}
 iat,err:=strconv.ParseInt(string(fields["iat"]),10,64);if err!=nil||exp-iat!=300{t.Fatalf("expiry ignored: %d %d %v",exp,iat,err)}
}
`

func TestGeneratedAuthRefreshHTTP(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @withAuth(stores: [id, name]) {
 id: Int @id
 name: String
}
fn fixtureIdentity(): Result<User> @native
api issueAccess(): String {
 val user = fixtureIdentity()?
 return user.createToken()
}
api issueRefresh(): String {
 val user = fixtureIdentity()?
 return user.createRefreshToken()
}
api exchange(token: String): String {
 val user = fixtureIdentity()?
 return user.refreshToken(token)
}
api identity(): Int @auth { return my.id }
`, generatedAuthHTTPAssertions)
}

const generatedAuthHTTPAssertions = `package generated
import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "math"
 "net"
 "net/http"
 "net/http/httptest"
 "os"
 "sync/atomic"
 "testing"
 "time"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/luvia"
)

// The fixture supplies a preauthenticated identity; it is not a login API.
type fixtureResolver struct { user *User; calls atomic.Int64 }
func (r *fixtureResolver) FixtureIdentity(context.Context)(User,error){r.calls.Add(1);return *r.user,nil}

type authHTTPFixture struct {
 router *api.Router
 server *httptest.Server
 client *http.Client
 resolver *fixtureResolver
 connections,requests atomic.Int64
 binary,http2 bool
}

func newAuthHTTPFixture(t testing.TB,binary bool,id int64)*authHTTPFixture{
 return newAuthHTTPProtocolFixture(t,binary,false,id)
}

func newAuthHTTPProtocolFixture(t testing.TB,binary,http2 bool,id int64)*authHTTPFixture{
 t.Helper()
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 resolver:=&fixtureResolver{user:&User{Id:id,Name:"東京"}}
 app:=&App{Resolver:resolver}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 f:=&authHTTPFixture{router:router,resolver:resolver,binary:binary,http2:http2}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 f.server=httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){f.requests.Add(1);endpoint.ServeHTTP(w,r)}))
 f.server.Config.ConnState=func(_ net.Conn,state http.ConnState){if state==http.StateNew{f.connections.Add(1)}}
 protocols:=new(http.Protocols)
 if http2{protocols.SetUnencryptedHTTP2(true)}else{protocols.SetHTTP1(true)}
 f.server.Config.Protocols=protocols
 f.server.Start();t.Cleanup(f.server.Close)
 transport:=&http.Transport{Protocols:protocols,MaxConnsPerHost:2}
 t.Cleanup(transport.CloseIdleConnections)
 f.client=&http.Client{Transport:transport,Timeout:5*time.Second}
 return f
}

func (f *authHTTPFixture) call(t testing.TB,name,token,bearer string)(int,[]byte){
 t.Helper()
 params:=map[string]any{}
 if name=="exchange"{params["token"]=token}
 var body []byte;var err error
 if f.binary{
  body,err=api.EncodeBinaryRequest(f.router.Schema.APIs[name].ID,nil,params,f.router.Registry.ParamOrder(name))
 }else{
  params["$api"]=name;body,err=json.Marshal(params)
 }
 if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
 request,err:=http.NewRequestWithContext(ctx,"POST",f.server.URL+"/luvia",bytes.NewReader(body));if err!=nil{t.Fatal(err)}
 request.Header.Set("Content-Type","application/json")
 if f.binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 if bearer!=""{request.Header.Set("Authorization","Bearer "+bearer)}
 response,err:=f.client.Do(request);if err!=nil{t.Fatal(err)}
 defer response.Body.Close()
 expectedProtocol:=1;if f.http2{expectedProtocol=2}
 if response.ProtoMajor!=expectedProtocol{t.Fatalf("protocol=%d want %d",response.ProtoMajor,expectedProtocol)}
 data,err:=io.ReadAll(response.Body);if err!=nil{t.Fatal(err)}
 return response.StatusCode,data
}

func (f *authHTTPFixture) stringResult(t testing.TB,name,token string)string{
 t.Helper()
 status,data:=f.call(t,name,token,"")
 if status!=200{t.Fatalf("%s failed: %d %s",name,status,data)}
 if f.binary{
  result,n:=codec.ReadString(data,0);if n!=len(data)||n<=0||result==""{t.Fatalf("invalid Binary string: %x",data)}
  return result
 }
 var envelope struct{Data string}
 if err:=json.Unmarshal(data,&envelope);err!=nil||envelope.Data==""{t.Fatalf("invalid JSON string: %s %v",data,err)}
 return envelope.Data
}

func (f *authHTTPFixture) assertIdentity(t testing.TB,bearer string,id int64,wantStatus int){
 t.Helper()
 status,data:=f.call(t,"identity","",bearer)
 if status!=wantStatus{t.Fatalf("identity status=%d want=%d body=%s",status,wantStatus,data)}
 if status!=200{return}
 if f.binary{
  actual,n:=codec.ReadSvarint(data,0);if n!=len(data)||actual!=id{t.Fatalf("identity changed: %x",data)}
 }else{
  var result struct{Data int64}
  if err:=json.Unmarshal(data,&result);err!=nil||result.Data!=id{t.Fatalf("identity changed: %s %v",data,err)}
 }
}

func TestAuthRefreshHTTP(t *testing.T){
 t.Setenv("JWT_SECRET","http-refresh-fixture-only");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 for _,binary:=range []bool{false,true}{for _,id:=range []int64{math.MinInt64,9007199254740993,math.MaxInt64}{
  t.Run(fmt.Sprintf("binary=%t/id=%d",binary,id),func(t *testing.T){
   f:=newAuthHTTPFixture(t,binary,id)
   access:=f.stringResult(t,"issueAccess","")
   refresh:=f.stringResult(t,"issueRefresh","")
   f.assertIdentity(t,access,id,200);f.assertIdentity(t,refresh,id,401);f.assertIdentity(t,"",id,401)
   for range 2{
    renewed:=f.stringResult(t,"exchange",refresh)
    f.assertIdentity(t,renewed,id,200)
    cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
    if _,err:=auth.VerifyRefresh(cfg,renewed);err==nil{t.Fatal("renewal returned another refresh token")}
   }
   for _,invalid:=range []string{access,"invalid",""}{
    if status,data:=f.call(t,"exchange",invalid,"");status==200{t.Fatalf("invalid refresh succeeded: %s",data)}
   }
  })
 }}
}

func TestDisabledRefreshHTTP(t *testing.T){
 t.Setenv("JWT_SECRET","http-refresh-disabled");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","false");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 for _,binary:=range []bool{false,true}{
  f:=newAuthHTTPFixture(t,binary,42)
  cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
  enabled:=*cfg;enabled.RefreshEnabled=true
  token,err:=auth.SignRefresh(&enabled,(&User{Id:42,Name:"old"}).AppendClaimsJSON);if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"issueRefresh","exchange"}{
   if status,data:=f.call(t,name,token,"");status==200{t.Fatalf("disabled %s succeeded: %s",name,data)}
  }
  f.assertIdentity(t,f.stringResult(t,"issueAccess",""),42,200)
 }
}

func configureAuthHTTPMeasurement(t testing.TB){
 t.Helper()
 t.Setenv("JWT_SECRET","http-refresh-measurement-only");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
}

func (f *authHTTPFixture) refreshLifecycle(t testing.TB){
 t.Helper()
 // Each serial iteration uses a different verified identity: its resulting
 // access token cannot borrow an earlier iteration's verification-cache entry.
 f.resolver.user.Id++
 refresh:=f.stringResult(t,"issueRefresh","")
 access:=f.stringResult(t,"exchange",refresh)
 f.assertIdentity(t,access,f.resolver.user.Id,200)
}

func (f *authHTTPFixture) verifyLifecycle(t testing.TB,iterations int64){
 t.Helper()
 if n:=f.connections.Load();n!=1{t.Fatalf("connection reuse failed: %d",n)}
 if n:=f.requests.Load();n!=iterations*3{t.Fatalf("HTTP request count=%d want %d",n,iterations*3)}
 if n:=f.resolver.calls.Load();n!=iterations*2{t.Fatalf("native call count=%d want %d",n,iterations*2)}
}

func TestAuthRefreshMeasurementPaths(t *testing.T){
 configureAuthHTTPMeasurement(t)
 for _,http2:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
  t.Run(fmt.Sprintf("http2=%t/binary=%t",http2,binary),func(t *testing.T){
   f:=newAuthHTTPProtocolFixture(t,binary,http2,9007199254740993)
   for range 3{f.refreshLifecycle(t)}
   f.verifyLifecycle(t,3)
  })
 }}
}

func BenchmarkGeneratedAuthRefreshHTTP(b *testing.B){
 configureAuthHTTPMeasurement(b)
 modes:=[]bool{false,true}
 if os.Getenv("LUXO_AUTH_HTTP_REVERSE")=="1"{modes=[]bool{true,false}}
 for _,http2:=range modes{for _,binary:=range modes{
  b.Run(fmt.Sprintf("http2=%t/binary=%t",http2,binary),func(b *testing.B){
   auth.ResetVerifyCache()
   f:=newAuthHTTPProtocolFixture(b,binary,http2,9007199254740993)
   f.refreshLifecycle(b)
   b.ReportAllocs()
   for b.Loop(){f.refreshLifecycle(b)}
   f.verifyLifecycle(b,int64(b.N)+1)
   b.ReportMetric(3,"requests/op")
  })
 }}
}
`
