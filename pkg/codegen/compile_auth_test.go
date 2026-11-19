package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestFormalGenerationRejectsInvalidAuthStores(t *testing.T) {
	samples := []struct {
		name, message string
		mutate        func(*ast.ModelDecl, *ast.Directive)
	}{
		{"missing", "requires an explicit stores field list", func(_ *ast.ModelDecl, d *ast.Directive) { d.Args = nil }},
		{"not_list", "requires an explicit stores field list", func(_ *ast.ModelDecl, d *ast.Directive) {
			d.Args[0].Value = &ast.Literal{Kind: token.String, Value: "id"}
		}},
		{"not_field", "stores requires field names", func(_ *ast.ModelDecl, d *ast.Directive) {
			d.Args[0].Value.(*ast.ListExpr).Items[0] = &ast.Literal{Kind: token.String, Value: "id"}
		}},
		{"unknown", "stores has unknown or untyped field absent", func(_ *ast.ModelDecl, d *ast.Directive) {
			d.Args[0].Value.(*ast.ListExpr).Items[0] = &ast.Ident{Name: "absent"}
		}},
		{"untyped", "stores has unknown or untyped field id", func(m *ast.ModelDecl, _ *ast.Directive) { m.Fields[0].Type = nil }},
		{"repeated", "stores repeats field id", func(_ *ast.ModelDecl, d *ast.Directive) {
			list := d.Args[0].Value.(*ast.ListExpr)
			list.Items = append(list.Items, &ast.Ident{Name: "id"})
		}},
	}
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			input, config := authStoresGenerationInput(t)
			generator, err := NewGenerator(config)
			if err != nil {
				t.Fatal(err)
			}
			before, err := generator.Generate(input, "generated", nil)
			if err != nil {
				t.Fatal(err)
			}
			model := input.Files[0].Models[0]
			sample.mutate(model, findDirective(model.Directives, "withAuth"))
			generated, err := generator.Generate(input, "generated", nil)
			if generated != nil || err == nil || !strings.Contains(err.Error(), "model User @withAuth "+sample.message) {
				t.Fatalf("invalid authentication stores produced output or lost its diagnostic: output=%v error=%v", generated != nil, err)
			}
			recovered, _ := authStoresGenerationInput(t)
			after, err := generator.Generate(recovered, "generated", nil)
			if err != nil || after == nil || len(after.Files) != len(before.Files) {
				t.Fatalf("valid generation after rejection failed: %v", err)
			}
			for name, content := range before.Files {
				if string(after.Files[name]) != string(content) {
					t.Errorf("rejected stores contaminated subsequent %s", name)
				}
			}
		})
	}
}

func authStoresGenerationInput(t *testing.T) (*semantic.Result, GeneratorConfig) {
	t.Helper()
	input := analyzePermissionFixture(t, `model User @noTime @withAuth(stores: [id, role]) {
 id: Int @id
 role: String
}
api value(): Int @auth { return 7 }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}}})
	return input, GeneratorConfig{IDs: ids}
}

func TestPermissionSourceGenerationBoundary(t *testing.T) {
	for _, condition := range []string{`true`, `my.role == "admin" || my.role == "member"`, `my.role != 42`, `my.role != null`, `my.role == false`, `my.role != expectedRole`, `my.role != my.role`, `my.role == "admin"`} {
		t.Run(condition, func(t *testing.T) {
			source := "val expectedRole = \"admin\"\n" + `api restricted(): Int @auth(permission: { ` + condition + ` }) { return 42 }`
			input := analyzePermissionFixture(t, source)
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"restricted": 1}}})
			if condition == `my.role != 42` || condition == `my.role == false` {
				if len(input.Errors) == 0 || err == nil || generated != nil || !strings.Contains(err.Error(), "incompatible types") {
					t.Fatalf("incompatible permission escaped semantic validation: %v", err)
				}
				return
			}
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			if condition == `my.role == "admin"` {
				if err != nil || !strings.Contains(string(generated.Files["handler.gen.go"]), `value, valid := identity.Get("role").String()`) {
					t.Fatalf("supported permission changed: %v", err)
				}
			} else if condition == `true` || condition == `my.role == "admin" || my.role == "member"` || condition == `my.role != my.role` {
				if err != nil || generated == nil || !strings.Contains(string(generated.Files["handler.gen.go"]), "_identity.Allowed(") {
					t.Fatalf("typed permission failed generation: %v", err)
				}
			} else if err == nil || generated != nil || !strings.Contains(err.Error(), "permission") {
				t.Fatalf("unsupported permission generated silently: %v", err)
			}
		})
	}
}

func analyzePermissionFixture(t testing.TB, source string) *semantic.Result {
	t.Helper()
	tokens, lexErrors := lexer.New(source, "permission.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("permission.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	return semantic.Analyze([]*ast.File{file})
}

func TestIdentityScalarReaderExcludesDeclaredModelList(t *testing.T) {
	input := analyzePermissionFixture(t, `model User @noTime @withAuth(stores: [peers]) {
 id: Int @id
 peers: [User] @by(id, id)
}
api current(): Int @auth { return my.peers.size }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	var member *ast.MemberExpr
	ast.WalkExprs(input.Files[0].APIs[0].Body, func(expression ast.Expr) {
		value, ok := expression.(*ast.MemberExpr)
		if !ok || value.Field != "peers" {
			return
		}
		if object, ok := value.Object.(*ast.Ident); ok && object.Name == "my" {
			member = value
		}
	})
	if member == nil || member.GetTypeTag() != "User" || !member.IsListType() {
		t.Fatalf("declared identity model list lost its resolved type: %#v", member)
	}
	// Model lists use the object decoder before the optional scalar reader.
	if got := identityValueReader(member); got != "" {
		t.Fatalf("model list received a scalar identity reader: %q", got)
	}
}

func TestPermissionEnumConstantGeneration(t *testing.T) {
	input := analyzePermissionFixture(t, `enum Role { ADMIN MEMBER }
model User @withAuth(stores: [role]) {
 id: Int @id
 role: Role
}
api restricted(): Int @auth(permission: { my.role == Role.ADMIN }) { return 42 }`)
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}}})
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, generated, permissionEnumAssertions)
}

const permissionEnumAssertions = `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestEnumPermission(t *testing.T){
 cfg:=&auth.Config{Secret:"enum-permission-fixture",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {` + "`" + `{"role":"ADMIN"}` + "`" + `,200},{` + "`" + `{"role":"MEMBER"}` + "`" + `,403},
  {` + "`" + `{"role":"UNKNOWN"}` + "`" + `,403},{` + "`" + `{"role":null}` + "`" + `,403},
  {` + "`" + `{"role":42}` + "`" + `,403},{` + "`" + `{}` + "`" + `,403},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte(` + "`" + `{"$api":"restricted"}` + "`" + `)
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["restricted"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Authorization","Bearer "+token)
   if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request)
   if response.Code!=sample.status{t.Fatalf("%s binary=%v status=%d want=%d body=%s",sample.raw,binary,response.Code,sample.status,response.Body)}
   if sample.status==200{want:=[]byte(` + "`" + `{"data":42}` + "`" + `);if binary{want=[]byte{84}};if !bytes.Equal(response.Body.Bytes(),want){t.Fatalf("success value changed: %q",response.Body.Bytes())}}
  }
 }
}
`

func TestFormalGenerationRejectsIgnoredPermission(t *testing.T) {
	for name, body := range map[string]*ast.Block{
		"missing":     nil,
		"empty":       {},
		"multiple":    {Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "true"}}, &ast.ExprStmt{Expr: &ast.Ident{Name: "false"}}}},
		"return":      {Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "true"}}}},
		"unsupported": {Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "true"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			input := result(&ast.File{APIs: []*ast.ApiDecl{{Name: "restricted", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{
				{Name: "native"}, {Name: "auth", Args: []*ast.NamedArg{{Name: "permission", Value: &ast.LambdaExpr{Body: body}}}},
			}}}})
			config := GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"restricted": 1}}}
			generated, err := Generate(input, "generated", config)
			if err == nil || generated != nil || !strings.Contains(err.Error(), "permission") {
				t.Fatalf("ignored permission produced output: error=%v", err)
			}
		})
	}
}

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
 token,err:=auth.SignRefreshForSubject(cfg,"User",valid.AppendClaimsJSON);if err!=nil{t.Fatal(err)}
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
  token,err:=auth.SignRefreshForSubject(&enabled,"User",(&User{Id:42,Name:"old"}).AppendClaimsJSON);if err!=nil{t.Fatal(err)}
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

func TestGeneratedServiceIdentityScope(t *testing.T) {
	runGeneratedFixture(t, `fn currentIdentity(): Int @service @auth { return my.id }`, `package generated
import("bytes";"net";"testing";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestServiceIdentity(t *testing.T){
 t.Setenv("JWT_SECRET","service-identity-audit-secret");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 signed,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":9007199254740993}")...),nil});if err!=nil{t.Fatal(err)}
 gateway:=luvia.New();router:=gateway.Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,&App{});err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 var params codec.Encoder;params.WriteEnd()
 for _,bearer:=range []string{"", "invalid",signed}{
  data,err:=client.CallWithMaskContext(t.Context(),bearer,1,nil,params.Bytes())
  if bearer==signed{if err!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,9007199254740993)){t.Fatal(data,err)}}else if err==nil{t.Fatal("unauthenticated service succeeded",data)}
 }
}
`, StableIDs{APIs: map[string]int{"svc:currentIdentity": 1}})
}

func TestGeneratedNamedAuthRole(t *testing.T) {
	runGeneratedFixture(t, `api restricted(): Int @auth(role: "admin") { return 42 }`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")

func TestNamedRole(t *testing.T){
 t.Setenv("JWT_SECRET","named-role-audit-secret");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,role:=range []string{"member","admin"}{
  signed,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":42,\"role\":\""+role+"\"}")...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"restricted\"}");if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["restricted"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+signed)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   want:=403;if role=="admin"{want=200};if out.Code!=want{t.Fatalf("role=%s binary=%t got %d want %d: %s",role,binary,out.Code,want,out.Body)}
  }
 }
}
`, StableIDs{})
}

func TestGeneratedStoredIdentityScalarTypes(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @withAuth(stores: [id, name]) {
 id: Int @id
 name: Int
}

api currentTeam(): Int @auth { return my.name }
`, `package generated
import "testing"
func TestStoredIdentityCompiles(t *testing.T) {}
`)
}

func TestGeneratedPermissionRejectsMissingOrInvalidIdentityField(t *testing.T) {
	runGeneratedFixture(t, `api restricted(): Int @auth(permission: { my.role != "blocked" }) { return 42 }`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")

func TestStrictIdentityPermission(t *testing.T){
 t.Setenv("JWT_SECRET","strict-identity-test-only");t.Setenv("JWT_EXPIRES","1h");t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{name,raw string;status int}{
  {"anonymous","",401},
  {"invalid-token","",401},
  {"refresh-token","{\"id\":42,\"role\":\"member\"}",401},
  {"missing","{\"id\":42}",403},
  {"null","{\"id\":42,\"role\":null}",403},
  {"wrong-kind","{\"id\":42,\"role\":123}",403},
  {"boolean","{\"id\":42,\"role\":false}",403},
  {"object","{\"id\":42,\"role\":{}}",403},
  {"array","{\"id\":42,\"role\":[]}",403},
  {"empty","{\"id\":42,\"role\":\"\"}",200},
  {"blocked","{\"id\":42,\"role\":\"blocked\"}",403},
  {"allowed","{\"id\":42,\"role\":\"member\"}",200},
 }{
  var signed string
  if sample.raw!=""{
   issue:=auth.Sign;if sample.name=="refresh-token"{issue=auth.SignRefresh}
   signed,err=issue(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  }else if sample.name=="invalid-token"{signed="invalid"}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"restricted\"}");if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["restricted"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};if signed!=""{req.Header.Set("Authorization","Bearer "+signed)}
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Errorf("%s binary=%v status=%d body=%s",sample.name,binary,out.Code,out.Body)}
  }
 }
}
`, StableIDs{})
}

func TestGeneratedAuthReceiverIsolation(t *testing.T) {
	for _, name := range []string{"W", "Value"} {
		t.Run(name, func(t *testing.T) {
			source := "model " + name + " @noTime @withAuth(stores: [id]) { id: Int @id }"
			runGeneratedFixture(t, source, strings.ReplaceAll(`package generated
import("testing";"time";"github.com/light-speak/luxo/pkg/lux/auth")
func TestAuthReceiver(t *testing.T){
 t.Setenv("JWT_SECRET","receiver-isolation-test-only");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 model:=&AUTHMODEL{Id:9007199254740993}
 raw,err:=model.AppendClaimsJSON(nil);if err!=nil||string(raw)!="{\"id\":9007199254740993}"{t.Fatal(string(raw),err)}
 for _,issue:=range []func()(string,error){model.CreateToken,func()(string,error){return model.CreateTokenWithExpiry(time.Minute)}}{
  token,err:=issue();if err!=nil{t.Fatal(err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil||claims.ID()!=model.Id{t.Fatal(claims,err)}
 }
 refresh,err:=model.CreateRefreshToken();if err!=nil{t.Fatal(err)}
 token,err:=model.RefreshToken(refresh);if err!=nil{t.Fatal(err)}
 claims,err:=auth.Verify(cfg,token);if err!=nil||claims.ID()!=model.Id{t.Fatal(claims,err)}
 var missing *AUTHMODEL
 if raw,err:=missing.AppendClaimsJSON([]byte("prefix"));err==nil||string(raw)!="prefix"{t.Fatal(string(raw),err)}
}
`, "AUTHMODEL", name), StableIDs{ModelFields: map[string]map[string]int{name: {"id": 1}}})
		})
	}
}

func TestGeneratedUnicodeModelReceiver(t *testing.T) {
	runGeneratedFixture(t, `
model Équipe @noTime @withAuth(stores: [id]) { id: Int @id secret: String @hash }
type Élément { id: Int label: String }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestUnicodeReceivers(t *testing.T){
 source:=&Équipe{Id:42,Secret:"not-a-password-hash"}
 if source.VerifySecret("password"){t.Fatal("invalid hash accepted")}
 buf:=&api.ResponseBuf{}
 if err:=source.WriteLuxo(buf,nil);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(buf.B);var target Équipe;target.ReadLuxo(dec)
 if dec.Err()!=nil||target.Id!=source.Id||target.Secret!=source.Secret{t.Fatal(target,dec.Err())}
 buf=&api.ResponseBuf{}
 item:=Élément{Id:43,Label:"精确文本"}
 if err:=item.WriteLuxo(buf,nil);err!=nil{t.Fatal(err)}
 dec=codec.NewDecoder(buf.B);var result Élément;result.ReadLuxo(dec)
 if dec.Err()!=nil||result!=item{t.Fatal(result,dec.Err())}
 raw,err:=source.AppendClaimsJSON(nil);if err!=nil||string(raw)!="{\"id\":42}"{t.Fatal(string(raw),err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Équipe": {"id": 1, "secret": 2}, "Élément": {"id": 1, "label": 2}}})
}

func TestGeneratedModelOwnershipCannotBeForged(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime @crud @auth(Admin, own: "name") {
 id: Int @id
 name: Int
}
`, `package generated
import("bytes";"os";"strconv";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestForgedOwnership(t *testing.T){
 ctx:=t.Context()
 image:=os.Getenv("LUXO_TEST_POSTGRES_IMAGE");if image==""{image="postgres:17-alpine"}
 container,err:=postgres.Run(ctx,image,postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL); INSERT INTO users VALUES(1,99)");err!=nil{t.Fatal(err)}
 t.Setenv("JWT_SECRET","model-own-audit-secret");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 signed,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":42}")...),nil});if err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,operation:=range []string{"getUser","updateUser","deleteUser"}{for _,binary:=range []bool{false,true}{
  if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES(1,99) ON CONFLICT(id) DO UPDATE SET name=99");err!=nil{t.Fatal(err)}
  body:=[]byte("{\"$api\":\""+operation+"\",\"$select\":\"id,name\",\"id\":1,\"name\":42}")
  if binary{params:=map[string]any{"id":int64(1)};if operation=="updateUser"{params["name"]=int64(42)};mask,maskErr:=codec.AppendSelectionMask(nil,[]byte{3},nil);if maskErr!=nil{t.Fatal(maskErr)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[operation].ID,mask,params,router.Registry.ParamOrder(operation));if err!=nil{t.Fatal(err)}}
  req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+signed)
  out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
  row,err:=app.User.Where(UserWhere.Id.Eq(1)).First(ctx);if err!=nil{t.Fatalf("api=%s binary=%t lookup=%v",operation,binary,err)}
  t.Logf("api=%s binary=%t status=%d",operation,binary,out.Code)
  rejected:=out.Code==403||(operation!="updateUser"&&(out.Code==400||out.Code==404))
  if !rejected||row==nil||row.Name!=99{t.Errorf("api=%s binary=%t status=%d row=%v body=%s",operation,binary,out.Code,row,out.Body)}
 }}
 for _,sample:=range []struct{name,claims,operation string;owner,next int64;status int}{
  {"own-read",`+"`"+`{"id":42}`+"`"+`,"getUser",42,42,200},
  {"own-update",`+"`"+`{"id":42}`+"`"+`,"updateUser",42,42,200},
  {"own-delete",`+"`"+`{"id":42}`+"`"+`,"deleteUser",42,42,200},
  {"owner-transfer",`+"`"+`{"id":42}`+"`"+`,"updateUser",42,99,403},
  {"admin-transfer",`+"`"+`{"role":"Admin"}`+"`"+`,"updateUser",99,100,200},
  {"missing-id",`+"`"+`{}`+"`"+`,"updateUser",0,0,403},
  {"null-id",`+"`"+`{"id":null}`+"`"+`,"updateUser",0,0,403},
 }{for _,binary:=range []bool{false,true}{
  if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET name=$1",sample.owner);err!=nil{t.Fatal(err)}
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.claims...),nil});if err!=nil{t.Fatal(err)}
  body:=[]byte("{\"$api\":\""+sample.operation+"\",\"$select\":\"id,name\",\"id\":1")
  if sample.operation=="updateUser"{body=strconv.AppendInt(append(body,[]byte(",\"name\":")...),sample.next,10)};body=append(body,'}')
  if binary{params:=map[string]any{"id":int64(1)};if sample.operation=="updateUser"{params["name"]=sample.next};mask,e:=codec.AppendSelectionMask(nil,[]byte{3},nil);if e!=nil{t.Fatal(e)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.operation].ID,mask,params,router.Registry.ParamOrder(sample.operation));if err!=nil{t.Fatal(err)}}
  req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
  out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
  if out.Code!=sample.status{t.Errorf("%s binary=%v status=%d want=%d body=%s",sample.name,binary,out.Code,sample.status,out.Body)}
  row,err:=app.User.Where(UserWhere.Id.Eq(1)).First(ctx);if err!=nil{t.Fatal(err)}
  if sample.operation=="deleteUser"&&sample.status==200{if row!=nil{t.Error("own delete failed")};continue}
  want:=sample.owner;if sample.operation=="updateUser"&&sample.status==200{want=sample.next}
  if row==nil||row.Name!=want{t.Errorf("%s unexpected mutation: %v",sample.name,row)}
 }}
}
`)
}

func TestGeneratedVisibleNegation(t *testing.T) {
	for _, sample := range []struct {
		expression string
		visible    bool
	}{
		{"{ !true }", false}, {"{ !false }", true}, {"{ (my.id == 0 || my.id == 1) && my.id == 2 }", false}, {"{ my.id == 2 && (my.id == 1 || my.id == 0) }", false},
		{"(!true)", false}, {"(!false)", true},
		{"(5m == 300s)", true}, {"(1d != 24h)", false}, {"(-5m == -300s)", true},
	} {
		t.Run(sample.expression, func(t *testing.T) {
			runGeneratedTransactionFixture(t, strings.ReplaceAll(`
model User @noTime {
 id: Int @id
 name: String? @visible CONDITION
}

`, "CONDITION", sample.expression), strings.ReplaceAll(`package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestVisibleNegation(t *testing.T){
 secret:="must-not-leak";user:=&User{Id:42,Name:&secret}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{
  "row":func(buf *api.ResponseBuf)error{return user.WriteLuxo(buf,mask)},
  "column pointers":func(buf *api.ResponseBuf)error{return WriteColumnarUser(buf,[]*User{user},mask)},
  "column values":func(buf *api.ResponseBuf)error{return WriteColumnarUserValues(buf,[]User{*user},mask)},
 } {
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(name,err)}
  if bytes.Contains(buf.B,[]byte(secret))!=EXPECTED_VISIBLE{t.Fatalf("%s incorrect visibility: %x",name,buf.B)}
 }
}
`, "EXPECTED_VISIBLE", fmt.Sprint(sample.visible)))
		})
	}
}

func TestGeneratedVisibleEnumConstant(t *testing.T) {
	for condition, expected := range map[string]bool{"Role.ADMIN == Role.ADMIN": true, "Role.ADMIN == Role.MEMBER": false} {
		t.Run(condition, func(t *testing.T) {
			runGeneratedTransactionFixture(t, strings.ReplaceAll(`
enum Role { ADMIN MEMBER }
model User @noTime {
 id: Int @id
 name: String? @visible(Role.ADMIN == Role.ADMIN)
}

`, "Role.ADMIN == Role.ADMIN", condition), strings.ReplaceAll(`package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestVisibleEnumConstant(t *testing.T) {
 value:="visible-value";record:=&User{Id:1,Name:&value}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{
 "row":func(buf *api.ResponseBuf)error{return record.WriteLuxo(buf,mask)},
 "pointers":func(buf *api.ResponseBuf)error{return WriteColumnarUser(buf,[]*User{record},mask)},
 "values":func(buf *api.ResponseBuf)error{return WriteColumnarUserValues(buf,[]User{*record},mask)},
 }{buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(name,err)};if bytes.Contains(buf.B,[]byte(value)) != EXPECTED{t.Fatalf("%s missing visible value: %x",name,buf.B)}}
}
`, "EXPECTED", fmt.Sprint(expected)))
		})
	}
}

func TestGeneratedVisibleRejectsMissingIdentityRole(t *testing.T) {
	testGeneratedRoleVisibility(t, `my.role != "blocked"`)
}

func TestGeneratedVisibleNegationRejectsInvalidIdentity(t *testing.T) {
	testGeneratedRoleVisibility(t, `!(my.role == "blocked")`)
}

func TestGeneratedVisibleResolvedIdentityType(t *testing.T) {
	testGeneratedRoleVisibility(t, `my.role == ("mem" + "ber")`)
}

func testGeneratedRoleVisibility(t *testing.T, condition string) {
	t.Helper()
	runGeneratedTransactionFixture(t, strings.ReplaceAll(`
model User @noTime {
 id: Int @id
 name: String? @visible(CONDITION)
}
`, "CONDITION", condition), `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec")
func TestVisibleIdentityRole(t *testing.T) {
 t.Setenv("JWT_SECRET","visible-identity-test-only");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 secret:="private-field-must-not-leak";record:=&User{Id:1,Name:&secret}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name,raw string;allowed bool}{
  {"anonymous","",false},{"missing","{\"id\":42}",false},
  {"null","{\"id\":42,\"role\":null}",false},{"wrong-kind","{\"id\":42,\"role\":123}",false},
  {"blocked","{\"id\":42,\"role\":\"blocked\"}",false},{"member","{\"id\":42,\"role\":\"member\"}",true},
 } {
  var identity any
  if sample.raw!=""{signed,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)};identity,err=auth.Verify(cfg,signed);if err!=nil{t.Fatal(err)}}
  for name,write:=range map[string]func(*api.ResponseBuf)error{
   "row":func(buf *api.ResponseBuf)error{return record.WriteLuxo(buf,mask)},
   "pointers":func(buf *api.ResponseBuf)error{return WriteColumnarUser(buf,[]*User{record},mask)},
   "values":func(buf *api.ResponseBuf)error{return WriteColumnarUserValues(buf,[]User{*record},mask)},
  } {
   buf:=&api.ResponseBuf{Identity:identity};err:=write(buf)
   leaked:=err==nil&&bytes.Contains(buf.B,[]byte(secret))
   if leaked!=sample.allowed{t.Errorf("%s/%s visible=%v expected=%v error=%v",sample.name,name,leaked,sample.allowed,err)}
  }
 }
}
`)
}

func TestGeneratedBodyIdentityValidityAndShortCircuit(t *testing.T) {
	runGeneratedFixture(t, `api current(): Int @auth { return my.id }
api skipped(): Boolean @auth { return true || my.id == 42 }
api checked(): Boolean @auth { return false || my.id == 42 }
`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")

func TestBodyIdentity(t *testing.T){
 t.Setenv("JWT_SECRET","body-identity-test");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;valid bool}{
  {"{}",false},{"{\"id\":null}",false},{"{\"id\":\"42\"}",false},
  {"{\"id\":false}",false},{"{\"id\":0}",true},{"{\"id\":42}",true},
 }{
  signed,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"current","skipped","checked"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+signed)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   expected:=200;if !sample.valid && name!="skipped"{expected=401}
   if out.Code!=expected{t.Errorf("%s %s binary=%v status=%d expected=%d body=%s",name,sample.raw,binary,out.Code,expected,out.Body)}
  }}
 }
}
`, StableIDs{})
}

func TestGeneratedStoredIdentityTypeMatrix(t *testing.T) {
	for _, kind := range []string{"Int", "String", "Boolean", "Float", "Duration", "DateTime", "UUID", "Decimal", "Bytes", "JSON", "[Int]", "[String]", "[Boolean]", "[Float]", "[Duration]", "[DateTime]", "[UUID]", "[Decimal]", "[Bytes]", "[JSON]"} {
		t.Run(kind, func(t *testing.T) {
			source := "model User @withAuth(stores: [id, name]) { id: Int @id name: " + kind + " }\napi current(): " + kind + " @auth { return my.name }"
			runGeneratedTransactionFixture(t, source, `package generated
import "testing"
func TestStoredTypeCompiles(t *testing.T){}
`)
		})
	}
}

func TestGeneratedIdentityDurationFunctionArgument(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @withAuth(stores: [id, name]) { id: Int @id name: Duration }
fn preserve(value: Duration): Duration { return value }
api current(): Duration @auth { return preserve(my.name) }
api skipped(): Boolean @auth { return true || my.name == 0s }
api checked(): Boolean @auth { return false || my.name == 0s }
`, identityDurationAssertions)
}

const identityDurationAssertions = `package generated
import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "net"
 "net/http"
 "net/http/httptest"
 "strconv"
 "sync/atomic"
 "testing"
 "time"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/light-speak/luxo/pkg/lux/rpc"
)

type identityDurationCase struct{label,raw string;valid bool}
type identityDurationFixture struct{router *api.Router;endpoint http.Handler;client *rpc.Client}

func TestIdentityDurationRoundTrip(t *testing.T){
 t.Setenv("JWT_SECRET","identity-duration-chain");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 base,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 listener:=&identityDurationListener{Listener:base}
 service:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-service.Serve(listener)}()
 client:=rpc.NewClient(listener.Addr().String())
 fixture:=identityDurationFixture{router,endpoint,client}
 t.Cleanup(func(){client.Close();service.Close();if err:=<-done;err!=nil{t.Error(err)}})
 for _,sample:=range []identityDurationCase{
  {"zero","0",true},{"nanosecond","1",true},{"negative","-1",true},
  {"beyond-js-safe","9007199254740993",true},
  {"maximum","9223372036854775807",true},{"minimum","-9223372036854775808",true},
  {"missing","",false},{"null","null",false},{"text","\"1ns\"",false},
  {"boolean","false",false},{"fraction","0.5",false},{"exponent","1e0",false},
  {"overflow","9223372036854775808",false},{"underflow","-9223372036854775809",false},
 }{
  raw:="{}";if sample.raw!=""{raw="{\"name\":"+sample.raw+"}"}
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,raw...),nil});if err!=nil{t.Fatal(err)}
  for _,route:=range []string{"json","binary","lrpc"}{for _,name:=range []string{"current","skipped","checked"}{
   t.Run(sample.label+"/"+route+"/"+name,func(t *testing.T){
    fixture.check(t,token,route,name,sample)
   })
  }}
 }
 if got:=listener.accepted.Load();got!=1{t.Fatalf("LRPC did not reuse one authenticated connection: %d",got)}
}

func (f identityDurationFixture) check(t *testing.T,token,route,name string,sample identityDurationCase){
 t.Helper()
 status:=200;if !sample.valid&&name!="skipped"{status=401}
 var data []byte
 if route=="lrpc"{
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
  result,callErr:=f.client.CallWithMaskContext(ctx,token,f.router.Schema.APIs[name].ID,nil,[]byte{0})
  failure,ok:=errors.AsType[*luxerrors.AppError](callErr)
  if status==401{if !ok||failure.Code!=status{t.Fatalf("LRPC error=%v want=%d",callErr,status)};return}
  if callErr!=nil{t.Fatal(callErr)};data=result
 }else{
  response:=identityDurationHTTP(t,f.router,f.endpoint,token,name,route=="binary")
  if response.Code!=status{t.Fatalf("status=%d want=%d body=%s",response.Code,status,response.Body)}
  if status!=200{return};data=response.Body.Bytes()
  if route=="json"{var body struct{Data json.RawMessage};if err:=json.Unmarshal(data,&body);err!=nil{t.Fatal(err)};data=body.Data}
 }
 expected:=sample.raw;if name!="current"{expected=strconv.FormatBool(name=="skipped"||sample.raw=="0")}
 requireIdentityDurationValue(t,data,expected,name=="current",route!="json")
}

type identityDurationListener struct{net.Listener;accepted atomic.Int64}
func (l *identityDurationListener) Accept()(net.Conn,error){
 connection,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return connection,err
}

func identityDurationHTTP(t *testing.T,router *api.Router,endpoint http.Handler,token,name string,binary bool)*httptest.ResponseRecorder{
 t.Helper()
 body:=[]byte("{\"$api\":\""+name+"\"}")
 if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Authorization","Bearer "+token)
 request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request);return response
}

func requireIdentityDurationValue(t *testing.T,data []byte,expected string,duration,binary bool){
 t.Helper()
 if !binary{if string(data)!=expected{t.Fatalf("JSON value=%s want=%s",data,expected)};return}
 decoder:=codec.NewDecoder(data)
 if duration{
  want,err:=strconv.ParseInt(expected,10,64);if err!=nil{t.Fatal(err)}
  if got:=decoder.ReadInt();got!=want{t.Fatalf("Binary nanoseconds=%d want=%d",got,want)}
 }else{
  want,err:=strconv.ParseBool(expected);if err!=nil{t.Fatal(err)}
  if got:=decoder.ReadBool();got!=want{t.Fatalf("Binary condition=%v want=%v",got,want)}
 }
 if err:=decoder.Finish();err!=nil{t.Fatal(err)}
}
`

func TestGeneratedIdentityEnumRejectsUndeclaredMember(t *testing.T) {
	runGeneratedTransactionFixture(t, `
enum Role { ADMIN MEMBER }
model User @withAuth(stores: [id, name]) { id: Int @id name: Role }
api current(): Boolean @auth { return my.name != Role.ADMIN }
`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestEnumIdentity(t *testing.T){
 t.Setenv("JWT_SECRET","enum-identity-test");t.Setenv("JWT_EXPIRES","1h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {`+"`"+`{"name":"MEMBER"}`+"`"+`,200},{`+"`"+`{"name":"ADMIN"}`+"`"+`,200},
  {`+"`"+`{"name":"UNKNOWN"}`+"`"+`,401},{`+"`"+`{"name":""}`+"`"+`,401},{`+"`"+`{"name":null}`+"`"+`,401},
 }{
  signed,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte(`+"`"+`{"$api":"current"}`+"`"+`)
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+signed)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Errorf("%s binary=%v status=%d expected=%d",sample.raw,binary,out.Code,sample.status)}
  }
 }
}
`)
}

func TestGeneratedEnumIdentityConditions(t *testing.T) {
	runGeneratedFixture(t, `
enum Role { ADMIN MEMBER }
model User @noTime @withAuth(stores: [id, name]) {
 id: Int @id
 name: Role
}
model View @noTime {
 id: Int @id
 name: String? @visible(!(my.name == Role.ADMIN))
}
event Changed(id: Int)
api negative(): Int @auth @stream(Changed) { my.name != Role.ADMIN }
api negated(): Int @auth @stream(Changed) { !(my.name == Role.ADMIN) }
api skipped(): Int @auth @stream(Changed) { true || my.name == Role.ADMIN }
`, `package generated
import("bytes";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec")
func TestEnumConditions(t *testing.T){
 cfg:=&auth.Config{Secret:"enum-conditions",Expires:time.Hour}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 secret:="MEMBER";record:=&View{Id:1,Name:&secret}
 for _,sample:=range []struct{raw string;want bool}{
  {"{}",false},{"{\"name\":null}",false},{"{\"name\":42}",false},
  {"{\"name\":\"UNKNOWN\"}",false},{"{\"name\":\"\"}",false},
  {"{\"name\":\"ADMIN\"}",false},{"{\"name\":\"MEMBER\"}",true},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  identity,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  if got:=matchNegative(&NegativeStreamParams{},identity);got!=sample.want{t.Errorf("negative %s: %v",sample.raw,got)}
  if got:=matchNegated(&NegatedStreamParams{},identity);got!=sample.want{t.Errorf("negated %s: %v",sample.raw,got)}
  if !matchSkipped(&SkippedStreamParams{},identity){t.Errorf("short circuit %s",sample.raw)}
  for name,write:=range map[string]func(*api.ResponseBuf)error{
   "row":func(buf *api.ResponseBuf)error{return record.WriteLuxo(buf,mask)},
   "pointers":func(buf *api.ResponseBuf)error{return WriteColumnarView(buf,[]*View{record},mask)},
   "values":func(buf *api.ResponseBuf)error{return WriteColumnarViewValues(buf,[]View{*record},mask)},
  }{
   buf:=&api.ResponseBuf{Identity:identity};err:=write(buf)
   visible:=err==nil&&bytes.Contains(buf.B,[]byte("MEMBER"))
   if visible!=sample.want{t.Errorf("%s/%s visible=%v err=%v",sample.raw,name,visible,err)}
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}, "View": {"id": 1, "name": 2}}, EventFields: map[string]map[string]int{"Changed": {"id": 1}}})
}

func TestGeneratedModelOwnershipVariantsCompile(t *testing.T) {
	for _, directives := range []string{"", "@soft"} {
		t.Run(directives, func(t *testing.T) {
			runGeneratedFixture(t, "model User @crud @auth(Admin, own: \"name\") "+directives+` {
 id: Int @id
 name: Int?
}
api countUsersByName
`, `package generated
import "testing"
func TestOwnershipVariantsCompile(t *testing.T){}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "createdAt": 3, "updatedAt": 4, "deletedAt": 5}}})
		})
	}
}

func TestGeneratedExplicitOwnershipRejectsInvalidIdentityID(t *testing.T) {
	runGeneratedFixture(t, `api checkOwner(id: Int): Int @auth(Admin, own: "id") { return 42 }`, `package generated
import("bytes";"testing";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")

func TestExplicitOwnership(t *testing.T){
 t.Setenv("JWT_SECRET","explicit-own-test");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {"{}",403},{"{\"id\":null}",403},{"{\"id\":false}",403},{"{\"id\":\"0\"}",403},
  {"{\"id\":0}",200},{"{\"id\":1}",403},{"{\"role\":\"Admin\"}",200},
 }{
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"checkOwner\",\"id\":0}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["checkOwner"].ID,nil,map[string]any{"id":int64(0)},router.Registry.ParamOrder("checkOwner"));if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Errorf("claims=%s binary=%v status=%d want=%d",sample.raw,binary,out.Code,sample.status)}
  }
 }
}
`, StableIDs{})
}

func TestGeneratedModelOwnershipInternalFieldCompiles(t *testing.T) {
	runGeneratedTransactionFixture(t, `model User @noTime @crud @auth(Admin, own: "name") {
 id: Int @id
 name: Int = 42 @internal
}`, `package generated
import "testing"
func TestInternalOwnershipCompiles(t *testing.T){}
`)
}

func TestGeneratedIdentityEnumArrayValidity(t *testing.T) {
	runGeneratedTransactionFixture(t, `
enum Role { ADMIN MEMBER }
model User @withAuth(stores: [id, name]) { id: Int @id name: [Role] }
api current(): [Role] @auth { return my.name }
fn accepts(values: [Role]): Boolean { return true }
api skipped(): Boolean @auth { return true || accepts(my.name) }
`, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestEnumArrayIdentity(t *testing.T){
 cfg:=&auth.Config{Secret:"enum-array-identity-test",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {"{}",401},{"{\"name\":null}",401},{"{\"name\":\"ADMIN\"}",401},
  {"{\"name\":[42]}",401},{"{\"name\":[null]}",401},{"{\"name\":[\"UNKNOWN\"]}",401},
  {"{\"name\":[\"MEMBER\",\"UNKNOWN\"]}",401},{"{\"name\":[]}",200},
  {"{\"name\":[\"ADMIN\",\"MEMBER\"]}",200},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"current","skipped"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   expectedStatus:=sample.status;if name=="skipped"{expectedStatus=200}
   if out.Code!=expectedStatus{t.Errorf("%s binary=%v status=%d expected=%d body=%s",sample.raw,binary,out.Code,expectedStatus,out.Body)}
   if name=="current" && sample.status==200 && out.Code==200{
    expected:=[]byte("[]");if sample.raw!="{\"name\":[]}"{expected=[]byte("[\"ADMIN\",\"MEMBER\"]")}
    if binary{actual,err:=schema.BinaryScalarListToJSON(nil,out.Body.Bytes(),"String");if err!=nil||!bytes.Equal(actual,expected){t.Fatalf("binary values %s: %v",actual,err)}}else if !bytes.Contains(out.Body.Bytes(),append([]byte("\"data\":"),expected...)){t.Fatalf("JSON values: %s",out.Body)}
   }
  }}
 }
}
`)
}

func TestGeneratedNullableIdentityTypesCompile(t *testing.T) {
	for _, kind := range []string{"Int?", "String?", "Boolean?", "Float?", "DateTime?", "Duration?", "UUID?", "Decimal?", "Bytes?", "JSON?", "[Int]?", "Role?", "[Role]?"} {
		t.Run(kind, func(t *testing.T) {
			source := "enum Role { ADMIN MEMBER }\nmodel User @withAuth(stores: [id, name]) { id: Int @id name: " + kind + " }\napi current(): " + kind + " @auth { return my.name }"
			runGeneratedTransactionFixture(t, source, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestNullableIdentityCompiles(t *testing.T){
 cfg:=&auth.Config{Secret:"nullable-identity-matrix",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {"{}",401},{"{\"name\":null}",200},{"{\"name\":  null  }",200},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Fatalf("%s binary=%v status=%d expected=%d body=%s",sample.raw,binary,out.Code,sample.status,out.Body)}
   if out.Code==200{if binary{if !bytes.Equal(out.Body.Bytes(),[]byte{0}){t.Fatalf("null: %x",out.Body.Bytes())}}else if !bytes.Contains(out.Body.Bytes(),[]byte("\"data\":null")){t.Fatal(out.Body)}}
  }
 }
}
`)
		})
	}
}

func TestGeneratedNullableIdentityPresentValue(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @withAuth(stores: [id, name]) { id: Int @id name: Int? }
api current(): Int? @auth { return my.name }
api fallback(): Int @auth { return my.name ?: 7 }
fn preserve(value: Int?): Int? { return value }
api forwarded(): Int? @auth { return preserve(my.name) }
`, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestPresentIdentity(t *testing.T){
 cfg:=&auth.Config{Secret:"nullable-identity-test",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw,want string;status int}{
  {`+"`"+`{"name":0}`+"`"+`,"0",200},{`+"`"+`{"name":42}`+"`"+`,"42",200},
  {`+"`"+`{"name":-1}`+"`"+`,"-1",200},{`+"`"+`{"name":9223372036854775807}`+"`"+`,"9223372036854775807",200},
  {`+"`"+`{"name":null}`+"`"+`,"null",200},{"{}","",401},
  {`+"`"+`{"name":"0"}`+"`"+`,"",401},{`+"`"+`{"name":true}`+"`"+`,"",401},
  {`+"`"+`{"name":0.5}`+"`"+`,"",401},{`+"`"+`{"name":9223372036854775808}`+"`"+`,"",401},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"current","fallback","forwarded"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Errorf("%s %s binary=%v status=%d expected=%d body=%s",name,sample.raw,binary,out.Code,sample.status,out.Body);continue}
   if sample.status!=200{continue}
   expected:=[]byte(sample.want);actual:=out.Body.Bytes()
   if name=="fallback"&&sample.want=="null"{expected=[]byte("7")}
   if binary{
    if name!="fallback"&&sample.want=="null"{if !bytes.Equal(actual,[]byte{0}){t.Fatalf("invalid null: %x",actual)};continue}
    if name!="fallback"{if len(actual)==0||actual[0]!=1{t.Fatalf("missing presence: %x",actual)};actual=actual[1:]}
    actual,err=schema.BinaryScalarToJSON(nil,actual,"Int");if err!=nil||!bytes.Equal(actual,expected){t.Fatalf("value %s: %v",actual,err)}
   }else if !bytes.Contains(actual,append([]byte("\"data\":"),expected...)){t.Fatalf("value %s",actual)}
  }}
 }
}
`)
}

func TestStructuredIdentityRejectsUnmappedStorage(t *testing.T) {
	result := analyzePermissionFixture(t, `
type Detail { level: Int }
model User @withAuth(stores: [id, name]) { id: Int @id name: Detail }
api current(): Int @auth { return my.name.level }
`)
	generated, err := Generate(result, "generated", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"current": 1}}})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "has no database mapping") {
		t.Fatalf("invalid storage generated output: %v", err)
	}
	for _, diagnostic := range result.Errors {
		if strings.Contains(diagnostic.Message, "has no database mapping") {
			return
		}
	}
	t.Fatalf("unmapped identity storage accepted: %v", result.Errors)
}

func TestGeneratedNullableIdentityListStates(t *testing.T) {
	for _, sample := range []struct{ kind, expression, result, valid, invalid string }{
		{"Int", "my.name", "Int", `[0,42]`, `[0,"wrong"]`},
		{"Role", "my.name", "Role", `["ADMIN","MEMBER"]`, `["ADMIN","UNKNOWN"]`},
		{"Detail", "my.name?.map { it.level }", "Int", `[{"id":1,"level":0,"userId":1},{"id":2,"level":42,"userId":1}]`, `[{"id":1,"level":0,"userId":1},{}]`},
	} {
		t.Run(sample.kind, func(t *testing.T) {
			source := `enum Role { ADMIN MEMBER }
model Detail @noTime { id: Int @id level: Int userId: Int }
model User @withAuth(stores: [id, name]) { id: Int @id name: [` + sample.kind + `]? }
error Rejected(user: User) { code: 422 message: "error.rejected" }
api current(): [` + sample.result + `]? @auth { return ` + sample.expression + ` }
`
			want := "[0,42]"
			wireType := "Int"
			if sample.kind == "Role" {
				want = sample.valid
				wireType = "String"
			}
			values := "var validClaim=" + fmt.Sprintf("%q", `{"name":`+sample.valid+`}`) + "\nvar invalidClaim=" + fmt.Sprintf("%q", `{"name":`+sample.invalid+`}`) + "\nvar expectedList=" + fmt.Sprintf("%q", want) + "\nvar wireType=" + fmt.Sprintf("%q", wireType) + "\n"
			if sample.kind == "Detail" {
				values += `
func TestModelListStaticClaimsAndErrorData(t *testing.T) {
 value:=&User{Id:1,Name:[]*Detail{{Id:1,Level:42,UserId:1}}}
 raw,err:=value.AppendClaimsJSON(nil)
 if err!=nil||!bytes.Contains(raw,[]byte("\"level\":42")){t.Fatalf("claims %s: %v",raw,err)}
 data:=NewRejected(*value).Data
 raw,err=data.AppendJSON(nil)
 if err!=nil||!bytes.Contains(raw,[]byte("\"level\":42")){t.Fatalf("error data %s: %v",raw,err)}
 value.Name[0]=nil
 if raw,err=value.AppendClaimsJSON([]byte("prefix:"));err==nil||string(raw)!="prefix:"{t.Fatalf("nil claims %s: %v",raw,err)}
 if raw,err=data.AppendJSON([]byte("prefix:"));err==nil||string(raw)!="prefix:"{t.Fatalf("nil error data %s: %v",raw,err)}
}
`
			}
			runGeneratedFixture(t, source, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/schema")
`+values+`
func TestNullableListStates(t *testing.T){
 cfg:=&auth.Config{Secret:"nullable-list-identity",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw,want string;status int}{
  {"{}","",401},{"{\"name\":null}","null",200},{"{\"name\":[]}","[]",200},
  {validClaim,expectedList,200},{invalidClaim,"",401},{"{\"name\":[null]}","",401},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Fatalf("%s binary=%v status=%d expected=%d body=%s",sample.raw,binary,out.Code,sample.status,out.Body)}
   if sample.status!=200{continue}
   actual:=out.Body.Bytes()
   if binary{
    if sample.want=="null"{if !bytes.Equal(actual,[]byte{0}){t.Fatalf("null: %x",actual)};continue}
    if len(actual)==0||actual[0]!=1{t.Fatalf("non-null list lost: %x",actual)}
    actual,err=schema.BinaryScalarListToJSON(nil,actual[1:],wireType)
    if err!=nil||!bytes.Equal(actual,[]byte(sample.want)){t.Fatalf("list %s expected %s: %v",actual,sample.want,err)}
   }else if !bytes.Contains(actual,[]byte("\"data\":"+sample.want)){t.Fatalf("value %s",actual)}
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}, "Detail": {"id": 1, "level": 2, "userId": 3}}})
		})
	}
}

func TestGeneratedNullableModelIdentityValues(t *testing.T) {
	runGeneratedFixture(t, `
model Detail @noTime { id: Int @id level: Int }
model User @withAuth(stores: [id, name]) { id: Int @id detailId: Int? name: Detail? }
api current(): Int? @auth { return my.name?.level }
api fallback(): Int @auth { return my.name?.level ?: 7 }
fn readDetail(value: Detail?): Int? { return value?.level }
api forwarded(): Int? @auth { return readDetail(my.name) }
api skipped(): Boolean @auth { return true || my.name?.level == null }
fn issueModelAccess(value: User): String { return value.createToken() }
fn issueModelCustom(value: User): String { return value.createToken(expires: 5m) }
fn issueModelRefresh(value: User): String { return value.createRefreshToken() }
`, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/schema")
func TestNullableModelIdentity(t *testing.T){
 cfg:=&auth.Config{Secret:"nullable-model-identity",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw,want string;status int}{
{"{}","",401},
{"{\"name\":null}","null",200},
{"{\"name\":{}}","",401},
{"{\"name\":{\"id\":1}}","",401},
{"{\"name\":{\"id\":1,\"level\":null}}","",401},
{"{\"name\":{\"id\":1,\"level\":\"0\"}}","",401},
{"{\"name\":{\"id\":1,\"level\":0}}","0",200},
{"{\"name\":{\"id\":1,\"level\":42}}","42",200},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"current","fallback","forwarded","skipped"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   status:=sample.status;want:=sample.want;kind:="Int"
   if name=="skipped"{status=200;want="true";kind="Boolean"}
   if name=="fallback"&&want=="null"{want="7"}
   if out.Code!=status{t.Errorf("%s %s binary=%v status=%d expected=%d body=%s",name,sample.raw,binary,out.Code,status,out.Body);continue}
   if status!=200{continue}
   actual:=out.Body.Bytes()
   if binary{
    if want=="null"{if !bytes.Equal(actual,[]byte{0}){t.Fatalf("invalid null: %x",actual)};continue}
    if name=="current"||name=="forwarded"{if len(actual)==0||actual[0]!=1{t.Fatalf("missing presence: %x",actual)};actual=actual[1:]}
    actual,err=schema.BinaryScalarToJSON(nil,actual,kind);if err!=nil||!bytes.Equal(actual,[]byte(want)){t.Fatalf("%s value %s expected %s: %v",name,actual,want,err)}
   }else if !bytes.Contains(actual,[]byte("\"data\":"+want)){t.Fatalf("value %s",actual)}
  }}
 }
}
`+"\nconst requiredIdentityModel = false\n"+modelIdentityIssuanceAssertions, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "detailId": 3}, "Detail": {"id": 1, "level": 2}}})
}

func TestGeneratedModelIdentityCompiles(t *testing.T) {
	runGeneratedFixture(t, `
model Detail @noTime { id: Int @id level: Int }
model User @withAuth(stores: [id, name]) { id: Int @id detailId: Int name: Detail }
error Rejected(user: User) { code: 422 message: "error.rejected" }
api current(): Int @auth { return my.name.level }
fn issueModelAccess(value: User): String { return value.createToken() }
fn issueModelCustom(value: User): String { return value.createToken(expires: 5m) }
fn issueModelRefresh(value: User): String { return value.createRefreshToken() }
`, `package generated
import("strings";"bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestStructuredIdentity(t *testing.T){
 cfg:=&auth.Config{Secret:"structured-identity-test",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,sample:=range []struct{raw string;status int}{
  {"{}",401},{"{\"name\":null}",401},{"{\"name\":\"wrong\"}",401},
  {"{\"name\":{}}",401},{"{\"name\":{\"id\":1}}",401},
  {"{\"name\":{\"id\":1,\"level\":null}}",401},{"{\"name\":{\"id\":1,\"level\":\"0\"}}",401},
  {"{\"name\":{\"id\":1,\"level\":0}}",200},
 }{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=sample.status{t.Fatalf("%s binary=%v status=%d expected=%d body=%s",sample.raw,binary,out.Code,sample.status,out.Body)}
   if out.Code==200 {if binary {if !bytes.Equal(out.Body.Bytes(),[]byte{0}){t.Fatal(out.Body)}}else if !bytes.Contains(out.Body.Bytes(),[]byte("\"data\":0")){t.Fatal(out.Body)}}
  }
 }
}
func TestRequiredModelIdentityWrite(t *testing.T){
 user:=&User{Id:42,DetailId:1}
 if raw,err:=user.AppendClaimsJSON([]byte("prefix:"));err==nil||string(raw)!="prefix:"{t.Fatalf("required model claims: %s %v",raw,err)}
 if raw,err:=NewRejected(*user).Data.AppendJSON([]byte("prefix:"));err==nil||string(raw)!="prefix:"{t.Fatalf("required nested model error: %s %v",raw,err)}
 user.Name=&Detail{Id:1,Level:42}
 if raw,err:=user.AppendClaimsJSON(nil);err!=nil||!bytes.Contains(raw,[]byte("\"level\":42")){t.Fatalf("valid claims: %s %v",raw,err)}
 if raw,err:=NewRejected(*user).Data.AppendJSON(nil);err!=nil||!bytes.Contains(raw,[]byte("\"level\":42")){t.Fatalf("valid nested error: %s %v",raw,err)}
}
func TestStructuredIdentityReader(t *testing.T){
 raw:=[]byte("{\"id\":1,\"level\":42}")
 var value Detail
 if err:=value.readIdentityInput(raw,63);err!=nil||value.Level!=42{t.Fatal(value,err)}
 if err:=value.readIdentityInput(raw,64);err==nil{t.Fatal("depth limit bypassed")}
 if value.Level!=42{t.Fatal("failed read changed previous value")}
 values,err:=readIdentityDetailList(append(append([]byte("["),raw...),']'))
 if err!=nil||len(values)!=1||values[0].Level!=42{t.Fatal(values,err)}
 if _,err:=readIdentityDetailList([]byte("[{\"id\":1,\"level\":42},{}]"));err==nil{t.Fatal("partial list accepted")}
 var ordinary Detail
 if err:=ordinary.ReadJSONInput([]byte("{}"));err!=nil{t.Fatal("ordinary input contract changed",err)}
}
var identityModelSink []*Detail
func BenchmarkIdentityModelRead(b *testing.B){
 b.Run("one",func(b *testing.B){
  raw:=[]byte("{\"id\":1,\"level\":42}");var value Detail;b.ReportAllocs()
  for b.Loop(){if err:=value.readIdentityInput(raw,0);err!=nil{b.Fatal(err)}}
  if value.Level!=42{b.Fatal(value)}
 })
 b.Run("128",func(b *testing.B){
  raw:=[]byte("["+strings.Repeat("{\"id\":1,\"level\":42},",127)+"{\"id\":1,\"level\":42}]");b.ReportAllocs()
  for b.Loop(){values,err:=readIdentityDetailList(raw);if err!=nil{b.Fatal(err)};identityModelSink=values}
 })
}

`+"\nconst requiredIdentityModel = true\n"+modelIdentityIssuanceAssertions, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "detailId": 3}, "Detail": {"id": 1, "level": 2}}})
}

func TestGeneratedIdentityCannotReadOmittedNestedFields(t *testing.T) {
	for _, directive := range []string{"hidden", "internal"} {
		t.Run(directive, func(t *testing.T) {
			source := `model Detail @noTime { id: Int @id level: Int @` + directive + ` }
model User @withAuth(stores: [id, name]) { id: Int @id detailId: Int name: Detail }
api current(): Boolean @auth { return my.name.level == 0 }
fn readLevel(value: Detail): Int { return value.level }
api forwarded(): Boolean @auth { return readLevel(my.name) == 0 }
api skipped(): Boolean @auth { return true || my.name.level == 0 }
`
			runGeneratedFixture(t, source, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestOmittedIdentityField(t *testing.T){
 cfg:=&auth.Config{Secret:"omitted-identity-test",Expires:time.Hour}
 user:=&User{Id:42,DetailId:1,Name:&Detail{Id:1,Level:99}}
 token,err:=auth.SignForSubject(cfg,"User",user.AppendClaimsJSON);if err!=nil{t.Fatal(err)}
 claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
 if bytes.Contains([]byte(claims.Raw()),[]byte("level")){t.Fatal("hidden field entered token")}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,name:=range []string{"current","forwarded","skipped"}{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+name+"\"}")
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
  req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
  if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
  out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
  expected:=401;if name=="skipped"{expected=200}
  if out.Code!=expected{t.Errorf("omitted identity read: %s binary=%v status=%d expected=%d body=%s",name,binary,out.Code,expected,out.Body)}
 }}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "detailId": 3}, "Detail": {"id": 1, "level": 2}}})
		})
	}
}

func TestGeneratedIdentityPrimaryKeyTypes(t *testing.T) {
	for _, kind := range []string{"String", "UUID"} {
		t.Run(kind, func(t *testing.T) {
			source := "model User @noTime @withAuth(stores: [id]) { id: " + kind + " @id }\napi current(): " + kind + " @auth { return my.id }"
			runGeneratedFixture(t, source, strings.ReplaceAll(identityPrimaryKeyAssertions, "$TYPE", kind), StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
		})
	}
}

const identityPrimaryKeyAssertions = `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/schema")
func TestTypedIdentityID(t *testing.T){
 cfg:=&auth.Config{Secret:"typed-identity-id",Expires:time.Hour}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 valid:=[]string{"\"user-42\"","\"\""}
 if "$TYPE"=="UUID"{valid=[]string{"\"00112233-4455-6677-8899-aabbccddeeff\"","\"00000000-0000-0000-0000-000000000000\""}}
 values:=append(valid,"null","42","false","[]","{}")
 for index,value:=range values{
  token,err:=auth.SignForSubject(cfg,"User",func(dst []byte)([]byte,error){return append(dst,"{\"id\":"+value+"}"...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+token)
   out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   expected:=200;if index>=len(valid){expected=401}
   if out.Code!=expected{t.Fatalf("id %s binary=%v: status=%d body=%s",value,binary,out.Code,out.Body)}
   if expected!=200{continue}
   if binary{actual,err:=schema.BinaryScalarToJSON(nil,out.Body.Bytes(),"$TYPE");if err!=nil||string(actual)!=value{t.Fatalf("id %s: %s %v",value,actual,err)}}else if !bytes.Contains(out.Body.Bytes(),[]byte("\"data\":"+value)){t.Fatal(out.Body)}
  }
 }
}
`

func TestGeneratedDefaultIdentityUsesDeclaredSubject(t *testing.T) {
	primary := `model User @noTime @withAuth(stores: [id], default: true) { id: String @id }`
	secondary := `model Admin @noTime @withAuth(stores: [id]) { id: Int @id }`
	for _, source := range []string{primary + "\n" + secondary, secondary + "\n" + primary} {
		runGeneratedFixture(t, source+"\napi current(): String @auth { return my.id }",
			strings.ReplaceAll(identityPrimaryKeyAssertions, "$TYPE", "String"),
			StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Admin": {"id": 1}}})
	}
}

func TestGeneratedDefaultIdentityInheritedID(t *testing.T) {
	for _, flag := range []string{"", ", default: false", ", default: true"} {
		source := `model User : Base @noTime @withAuth(stores: [id]` + flag + `) { name: String }
model Base @noTime { id: String @id }
api current(): String @auth { return my.id }`
		runGeneratedFixture(t, source, strings.ReplaceAll(identityPrimaryKeyAssertions, "$TYPE", "String"),
			StableIDs{ModelFields: map[string]map[string]int{"Base": {"id": 1}, "User": {"id": 1, "name": 2}}})
	}
}

func TestGeneratedDefaultIdentityRejectsOtherSubject(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id], default: true) { id: Int @id }
model Admin @noTime @withAuth(stores: [id]) { id: Int @id }
event Changed(value: Int)
api watch(): Int @auth @stream(Changed) { true }
api current(): Int @auth { return my.id }`, `package generated
import("bytes";"net";"errors";"github.com/light-speak/luxo/pkg/lux/rpc";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestSubjectBoundary(t *testing.T){
 t.Setenv("JWT_SECRET","subject-boundary-test");t.Setenv("JWT_EXPIRES","1h");t.Setenv("JWT_REFRESH","true")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 adminRefresh,err:=(&Admin{Id:42}).CreateRefreshToken();if err!=nil{t.Fatal(err)}
 if _,err:=(&User{}).RefreshToken(adminRefresh);err==nil{t.Fatal("Admin refreshed into User")}
 userRefresh,err:=(&User{Id:42}).CreateRefreshToken();if err!=nil{t.Fatal(err)}
 renewed,err:=(&User{Id:99}).RefreshToken(userRefresh);if err!=nil{t.Fatal(err)}
 renewedClaims,err:=auth.Verify(cfg,renewed);if err!=nil||renewedClaims.Subject()!="User"||renewedClaims.ID()!=42{t.Fatal("renewal changed signed identity",err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 client:=rpc.NewClient(listener.Addr().String())
 t.Cleanup(func(){client.Close();server.Close();if err:=<-done;err!=nil{t.Error(err)}})
 for _,issuer:=range []struct{name string; sign func()(string,error);status int}{
  {"User",(&User{Id:42}).CreateToken,200},
  {"Admin",(&Admin{Id:42}).CreateToken,401},
  {"",func()(string,error){return auth.Sign(cfg,(&User{Id:42}).AppendClaimsJSON)},401},
 }{
  token,err:=issuer.sign();if err!=nil{t.Fatal(err)}
   claims,err:=auth.Verify(cfg,token);if err!=nil||claims.Subject()!=issuer.name{t.Fatalf("signed subject=%s want=%s err=%v",claims.Subject(),issuer.name,err)}
   for _,identity:=range []any{claims,&claims}{
    err:=authorizeStreamWatch(&api.Request{Buf:&api.ResponseBuf{}},identity)
    if (err==nil)!=(issuer.status==200){t.Errorf("stream issuer=%s error=%v",issuer.name,err)}
   }
   _,rpcErr:=client.CallWithMaskContext(t.Context(),token,router.Schema.APIs["current"].ID,nil,nil)
   if issuer.status==200 && rpcErr!=nil{t.Errorf("valid RPC: %v",rpcErr)}
   if issuer.status==401{var rejected *luxerrors.AppError;if !errors.As(rpcErr,&rejected)||rejected.Code!=401{t.Errorf("other-subject RPC: %v",rpcErr)}}
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
   req.Header.Set("Authorization","Bearer "+token);out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   if out.Code!=issuer.status{t.Errorf("issuer=%s binary=%v status=%d want=%d body=%s",issuer.name,binary,out.Code,issuer.status,out.Body)}
  }
 }
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}}, ModelFields: map[string]map[string]int{"User": {"id": 1}, "Admin": {"id": 1}}})
}

func TestFormalGenerationRejectsAuthFieldCollision(t *testing.T) {
	for _, name := range []string{"createToken", "createRefreshToken", "refreshToken", "verify"} {
		t.Run(name, func(t *testing.T) {
			parent := "model Base { " + name + ": Int }\n"
			child := "model User: Base @withAuth(stores: [id]) { id: Int @id }\n"
			for _, source := range []string{
				"model User @withAuth(stores: [id]) { id: Int @id\n" + name + ": Int }",
				parent + child, child + parent,
			} {
				input := analyzePermissionFixture(t, source)
				generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Base": {name: 1}, "User": {"id": 1, name: 2}}}})
				if err == nil || generated != nil || !strings.Contains(err.Error(), "conflicts with @withAuth") {
					t.Fatalf("conflicting auth field generated output: %v", err)
				}
			}
		})
	}
}

func TestGeneratedHashVerificationMatchesSemanticMethod(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime @withAuth(stores: [id]) { id: Int @id secret: String @hash }
fn short(user: User, plain: String): Boolean { return user.verify(plain) }
fn check(user: User, plain: String): Boolean { return user.verifySecret(plain) }
`, `package generated
import("testing"; luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestVerifySecret(t *testing.T){
 hash,err:=luxocrypto.HashPassword("secret");if err!=nil{t.Fatal(err)}
 app:=&App{}
 for _,plain:=range []string{"secret","wrong"}{
  valid,err:=app.check(t.Context(),&User{Id:1,Secret:hash},plain)
  if err!=nil||valid!=(plain=="secret"){t.Fatal(valid,err)}
  short,err:=app.short(t.Context(),&User{Id:1,Secret:hash},plain);if err!=nil||short!=valid{t.Fatal(short,err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "secret": 2}}})
}

func TestFormalGenerationRejectsInvalidPasswordArguments(t *testing.T) {
	for _, method := range []string{"verify", "verifyPassword"} {
		for _, args := range []string{``, `1`, `null`, `"a", "b"`, `password: "a"`, `plain: "a", plain: "b"`} {
			t.Run(method+"("+args+")", func(t *testing.T) {
				input := analyzePermissionFixture(t, `model User @withAuth(stores: [id]) { id: Int @id password: String @hash }
fn check(user: User): Boolean { return user.`+method+`(`+args+`) }`)
				if len(input.Errors) == 0 {
					t.Fatal("invalid password arguments accepted")
				}
				generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}}})
				if err == nil || generated != nil {
					t.Fatal("invalid password call generated output", err)
				}
			})
		}
	}
}

func TestGeneratedPasswordVerificationNamedArgument(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id password: String @hash }
fn check(user: User, plain: String): Boolean { return user.verifyPassword(plain: plain) }
`, `package generated
import("testing"; luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestPasswordNamedArgument(t *testing.T){
 hash,err:=luxocrypto.HashPassword("secret");if err!=nil{t.Fatal(err)}
 app:=&App{}
 for _,plain:=range []string{"secret","wrong"}{
  valid,err:=app.check(t.Context(),&User{Id:1,Password:hash},plain)
  if err!=nil||valid!=(plain=="secret"){t.Fatal(valid,err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}})
}

func TestGeneratedAuthRejectsInheritedVerifierIdentity(t *testing.T) {
	runGeneratedFixture(t, `api current(): Int @auth { return my.id }`, `package generated
import("bytes";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")

func TestVerifierBoundary(t *testing.T){
 auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 outer:=&auth.Config{Secret:"outer-verifier-secret",Expires:time.Hour}
 signed,err:=auth.Sign(outer,func(dst []byte)([]byte,error){return append(dst,[]byte("{\"id\":42}")...),nil});if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sameKey:=range []bool{false,true}{
  inner:=*outer;if !sameKey{inner.Secret="inner-verifier-secret"}
  handler:=luvia.AuthMiddleware(outer,luvia.AuthMiddleware(&inner,router))
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\"current\"}")
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["current"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");req.Header.Set("Authorization","Bearer "+signed)
   if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
   out:=httptest.NewRecorder();handler.ServeHTTP(out,req)
   want:=401;if sameKey{want=200};if out.Code!=want{t.Fatalf("sameKey=%t binary=%t status=%d body=%s",sameKey,binary,out.Code,out.Body)}
   if sameKey&&binary&&!bytes.Equal(out.Body.Bytes(),codec.AppendSvarint(nil,42)){t.Fatal("valid identity changed",out.Body.Bytes())}
  }
 }
}
`, StableIDs{})
}

func TestFormalGenerationRejectsImplicitAuthMethods(t *testing.T) {
	for _, call := range []string{"createToken()", "createRefreshToken()", `refreshToken("token")`} {
		input := analyzePermissionFixture(t, `model Base @withAuth(stores: [id]) { id: Int @id }
model Child: Base { label: String }
fn issue(value: Child): String { return value.`+call+` }`)
		generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Base": {"id": 1}, "Child": {"id": 1, "label": 2}}}})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "requires @withAuth on model 'Child'") {
			t.Fatal("implicit auth generated output", err)
		}
	}
}

func TestGeneratedExplicitChildAuthMethods(t *testing.T) {
	runGeneratedFixture(t, `
model Base @noTime @withAuth(stores: [id]) { id: Int @id }
model Child: Base @noTime @withAuth(stores: [id]) { label: String }
fn issueAccess(value: Child): String { return value.createToken() }
fn issueRefresh(value: Child): String { return value.createRefreshToken() }
fn exchange(value: Child, token: String): String { return value.refreshToken(token) }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/auth")
func TestExplicitChildTokens(t *testing.T){
 t.Setenv("JWT_SECRET","explicit-child-auth-test");t.Setenv("JWT_REFRESH","true")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 app:=&App{};value:=&Child{Id:42,Label:"child"}
 access,err:=app.issueAccess(t.Context(),value);if err!=nil{t.Fatal(err)}
 claims,err:=auth.Verify(cfg,access);if err!=nil||claims.ID()!=42{t.Fatal("access identity",claims,err)}
 refresh,err:=app.issueRefresh(t.Context(),value);if err!=nil{t.Fatal(err)}
 claims,err=auth.VerifyRefresh(cfg,refresh);if err!=nil||claims.ID()!=42{t.Fatal("refresh identity",claims,err)}
 access,err=app.exchange(t.Context(),value,refresh);if err!=nil{t.Fatal(err)}
 claims,err=auth.Verify(cfg,access);if err!=nil||claims.ID()!=42{t.Fatal("exchanged identity",claims,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Base": {"id": 1}, "Child": {"id": 1, "label": 2}}})
}

func TestGeneratedInheritedAndMultipleHashVerification(t *testing.T) {
	runGeneratedFixture(t, `
model Child: Base @noTime @withAuth(stores: [id]) { label: String }
model Base @noTime { id: Int @id secret: String @hash }
model Credentials @noTime { id: Int @id secret: String @hash password: String @hash }
fn inherited(user: Child, plain: String): Boolean { return user.verifySecret(plain) && user.verify(plain) }
fn secret(user: Credentials, plain: String): Boolean { return user.verifySecret(plain: plain) }
fn password(user: Credentials, plain: String): Boolean { return user.verifyPassword(plain: plain) }
`, `package generated
import("testing"; luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestHashSelection(t *testing.T){
 first,err:=luxocrypto.HashPassword("first");if err!=nil{t.Fatal(err)}
 second,err:=luxocrypto.HashPassword("second");if err!=nil{t.Fatal(err)}
 app:=&App{}; child:=&Child{Id:1,Secret:first,Label:"child"}; credentials:=&Credentials{Id:2,Secret:first,Password:second}
 for _,plain:=range []string{"first","second","wrong"}{
  valid,err:=app.inherited(t.Context(),child,plain);if err!=nil||valid!=(plain=="first"){t.Fatal("inherited",plain,valid,err)}
  valid,err=app.secret(t.Context(),credentials,plain);if err!=nil||valid!=(plain=="first"){t.Fatal("secret",plain,valid,err)}
  valid,err=app.password(t.Context(),credentials,plain);if err!=nil||valid!=(plain=="second"){t.Fatal("password",plain,valid,err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"Base": {"id": 1, "secret": 2}, "Child": {"id": 1, "secret": 2, "label": 3}, "Credentials": {"id": 1, "secret": 2, "password": 3},
	}})
}

func TestGeneratedExplicitAuthSubject(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id], default: true) { id: Int @id }
model Admin @noTime @withAuth(stores: [id, enabled]) { id: String @id enabled: Boolean }
event Named(value: String)
api admin(): String @auth(subject: Admin, permission: { my.enabled }) { return my.id }
api watchAdmin(): String @auth(subject: Admin, permission: { my.enabled }) @stream(Named) { true }
api user(): Int @auth { return my.id }`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestExplicitSubject(t *testing.T){
 t.Setenv("JWT_SECRET","explicit-subject");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,issuer:=range []struct{sign func()(string,error);admin,user int}{
  {(&User{Id:42}).CreateToken,401,200},
  {(&Admin{Id:"admin-42",Enabled:true}).CreateToken,200,401},
  {(&Admin{Id:"admin-42",Enabled:false}).CreateToken,403,401},
  {func()(string,error){return auth.SignForSubject(cfg,"Admin",func(dst []byte)([]byte,error){return append(dst,`+"`"+`{"id":"admin-42"}`+"`"+`...),nil})},403,401},
  {func()(string,error){return auth.SignForSubject(cfg,"Admin",func(dst []byte)([]byte,error){return append(dst,`+"`"+`{"id":"admin-42","enabled":null}`+"`"+`...),nil})},403,401},
  {func()(string,error){return auth.SignForSubject(cfg,"Admin",func(dst []byte)([]byte,error){return append(dst,`+"`"+`{"id":"admin-42","enabled":"true"}`+"`"+`...),nil})},403,401},
 }{
  token,err:=issuer.sign();if err!=nil{t.Fatal(err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  if err:=authorizeStreamWatchAdmin(&api.Request{Buf:&api.ResponseBuf{}},claims);(err==nil)!=(issuer.admin==200){t.Errorf("stream subject=%s err=%v",claims.Subject(),err)}
  for _,name:=range []string{"admin","user"}{for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+name+"\"}");contentType:="application/json"
   if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};contentType="application/x-luxo"}
   req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type",contentType);req.Header.Set("Authorization","Bearer "+token)
   if binary{req.Header.Set("X-Luxo-Mode","binary")};out:=httptest.NewRecorder();endpoint.ServeHTTP(out,req)
   want:=issuer.user;if name=="admin"{want=issuer.admin};if out.Code!=want{t.Fatalf("%s subject=%s binary=%v status=%d want=%d body=%s",name,claims.Subject(),binary,out.Code,want,out.Body)}
  }}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Admin": {"id": 1, "enabled": 2}}, EventFields: map[string]map[string]int{"Named": {"value": 1}}})
}

func TestInheritedUnmappedStorageRejectsGeneration(t *testing.T) {
	for _, parent := range []string{"type", "interface"} {
		result := analyzePermissionFixture(t, "type Detail { level: Int }\n"+parent+" Base { detail: Detail }\nmodel Record : Base { id: Int @id }")
		generated, err := Generate(result, "generated", GeneratorConfig{})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "has no database mapping") {
			t.Fatalf("unmapped inherited storage generated: %v", err)
		}
	}
}

func TestAbstractStorageRejectsPublicGeneration(t *testing.T) {
	for _, declaration := range []string{"interface Detail { level: Int }", "sealed Detail { Value(level: Int) }"} {
		result := analyzePermissionFixture(t, declaration+"\nmodel Record { id: Int @id detail: Detail }")
		output, err := Generate(result, "generated", GeneratorConfig{})
		if err == nil || output != nil || !strings.Contains(err.Error(), "has no database mapping") {
			t.Fatalf("invalid storage generated: %v", err)
		}
		schema, err := BuildSchemaJSON(result, nil, GeneratorConfig{})
		if err == nil || schema != nil || !strings.Contains(err.Error(), "has no database mapping") {
			t.Fatalf("invalid storage exported: %v", err)
		}
	}
}

func TestOmittedAPIReturnRejectsPublicGeneration(t *testing.T) {
	for _, body := range []string{"{}", "{ 42 }", "{ val value = 42 }"} {
		t.Run(body, func(t *testing.T) {
			result := analyzePermissionFixture(t, "api ping() "+body)
			generated, err := Generate(result, "generated", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"ping": 1}}})
			if err == nil || generated != nil || !strings.Contains(err.Error(), "must declare a return type") {
				t.Fatalf("invalid API generated output: %v", err)
			}
			schema, err := BuildSchemaJSON(result, nil, GeneratorConfig{})
			if err == nil || schema != nil || !strings.Contains(err.Error(), "must declare a return type") {
				t.Fatalf("invalid API exported schema: %v", err)
			}
		})
	}
}

const modelIdentityIssuanceAssertions = `
func TestModelIdentityIssuance(t *testing.T){
 t.Setenv("JWT_SECRET","model-identity-issuance");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 app:=&App{}
 for _,sample:=range []struct{name string;user *User;valid bool}{
  {"nil identity",nil,false},
  {"nil model",&User{Id:42},!requiredIdentityModel},
  {"valid model",&User{Id:42,Name:&Detail{Id:1,Level:42}},true},
 }{t.Run(sample.name,func(t *testing.T){
  for _,kind:=range []string{"access","custom","refresh"}{
   var signed string;var err error
   switch kind{
   case "access":signed,err=app.issueModelAccess(t.Context(),sample.user)
   case "custom":signed,err=app.issueModelCustom(t.Context(),sample.user)
   case "refresh":signed,err=app.issueModelRefresh(t.Context(),sample.user)
   }
   if !sample.valid{if err==nil||signed!=""{t.Fatalf("%s returned token for invalid model: %q %v",kind,signed,err)};continue}
   if err!=nil||signed==""{t.Fatalf("%s issuance failed: %v",kind,err)}
   if kind=="refresh"{
    if _,err=auth.Verify(cfg,signed);err==nil{t.Fatal("refresh accepted as access")}
    signed,err=sample.user.RefreshToken(signed);if err!=nil{t.Fatal("refresh failed",err)}
   }
   claims,err:=auth.Verify(cfg,signed);if err!=nil||claims.ID()!=42{t.Fatal("identity lost",err)}
   want:="null";if sample.user.Name!=nil{want="{\"id\":1,\"level\":42}"}
   if got:=claims.Get("name").Raw();got!=want{t.Fatalf("%s model changed: %s expected %s",kind,got,want)}
  }
 })}
}
`

func TestGeneratedOwnershipAfterSavePreparation(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalizeOwner(value: Int): Int { return value + 1 }
model User @noTime @crud @auth(Admin, own: "name") { id: Int @id name: Int @beforeSave { normalizeOwner(it) } }
`, `package generated
import("bytes";"os";"fmt";"testing";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/codec")
func TestPreparedOwnership(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL)");err!=nil{t.Fatal(err)}
 t.Setenv("JWT_SECRET","prepared-owner-test");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,operation:=range []string{"createUser","updateUser"}{for _,binary:=range []bool{false,true}{for _,admin:=range []bool{false,true}{for _,input:=range []int64{41,42}{
  t.Run(fmt.Sprintf("%s/binary=%t/admin=%t/value=%d",operation,binary,admin,input),func(t *testing.T){
   if _,err:=pg.Exec(ctx,db,"DELETE FROM users");err!=nil{t.Fatal(err)}
   if operation=="updateUser"{if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES(1,42)");err!=nil{t.Fatal(err)}}
   claims:=[]byte("{\"id\":42}");if admin{claims=[]byte("{\"role\":\"Admin\"}")}
   token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,claims...),nil});if err!=nil{t.Fatal(err)}
   body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"$select\":\"id,name\",\"id\":1,\"name\":%d}",operation,input))
   if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[operation].ID,mask,map[string]any{"id":int64(1),"name":input},router.Registry.ParamOrder(operation));if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")};request.Header.Set("Authorization","Bearer "+token)
   response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request)
   allowed:=admin||input==41;status:=403;if allowed{status=200};if response.Code!=status{t.Fatalf("status=%d want=%d body=%s",response.Code,status,response.Body)}
   row,err:=NewFromDB(db).User.Where(UserWhere.Id.Eq(1)).First(ctx);if err!=nil{t.Fatal(err)}
   if !allowed&&operation=="createUser"{if row!=nil{t.Fatal("unauthorized create")};return}
   want:=int64(42);if allowed{want=input+1};if row==nil||row.Name!=want{t.Fatal("wrong final ownership",row)}
  })
 }}}}
}
`)
}

func TestGeneratedLiteralHashMethodReceiver(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime { id: Int @id password: String @hash }
fn verifyLiteral(stored: String, plain: String): Boolean { return (User { id: 1, password: stored }).verifyPassword(plain: plain) }
fn verifyVariable(stored: String, plain: String): Boolean { val user = User { id: 1, password: stored } return user.verifyPassword(plain: plain) }
fn makeUser(stored: String): User { return User { id: 1, password: stored } }
fn verifyResult(stored: String, plain: String): Boolean { return makeUser(stored: stored).verifyPassword(plain: plain) }
fn hashValue(): Result<String> @native
fn plainValue(): Result<String> @native
fn verifyEffectful(): Boolean { return (User { id: 1, password: hashValue()? }).verifyPassword(plain: plainValue()?) }

`, `package generated
import("context";"errors";"slices";"testing";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
var literalHashErr=errors.New("hash unavailable")
type literalResolver struct{stored string;calls []string;fail bool}
func(r *literalResolver)HashValue(context.Context)(string,error){r.calls=append(r.calls,"hash");if r.fail{return "",literalHashErr};return r.stored,nil}
func(r *literalResolver)PlainValue(context.Context)(string,error){r.calls=append(r.calls,"plain");return "secret",nil}
func TestLiteralHashReceiver(t *testing.T){
 stored,err:=luxocrypto.HashPassword("secret");if err!=nil{t.Fatal(err)}
 resolver:=&literalResolver{stored:stored};app:=&App{Resolver:resolver}
 valid,effectErr:=app.verifyEffectful(t.Context());if effectErr!=nil||!valid||!slices.Equal(resolver.calls,[]string{"hash","plain"}){t.Fatal(valid,effectErr,resolver.calls)}
 resolver.calls=nil;resolver.fail=true;if _,err:=app.verifyEffectful(t.Context());!errors.Is(err,literalHashErr)||!slices.Equal(resolver.calls,[]string{"hash"}){t.Fatal(err,resolver.calls)}
 for _,check:=range []func(string,string)(bool,error){
  func(hash,plain string)(bool,error){return app.verifyLiteral(t.Context(),hash,plain)},
  func(hash,plain string)(bool,error){return app.verifyVariable(t.Context(),hash,plain)},
  func(hash,plain string)(bool,error){return app.verifyResult(t.Context(),hash,plain)},
 } {for _,sample:=range []struct{plain string;want bool}{{"secret",true},{"wrong",false}}{got,err:=check(stored,sample.plain);if err!=nil||got!=sample.want{t.Fatal(got,err,sample.want)}}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}})
}

func TestGeneratedLiteralAuthMethodReceiver(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime @withAuth(stores: [id]) { id: Int @id password: String @hash }
fn literalToken(): String { return (User { id: 1, password: "stored" }).createToken() }
fn literalExpiry(): String { return (User { id: 1, password: "stored" }).createToken(1h) }
fn literalRefresh(): String { return (User { id: 1, password: "stored" }).createRefreshToken() }
fn literalRefreshToken(old: String): String { return (User { id: 1, password: "stored" }).refreshToken(old) }
fn literalVerify(stored: String, plain: String): Boolean { return (User { id: 1, password: stored }).verify(plain) }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/auth";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
func TestLiteralAuthReceiver(t *testing.T){
 t.Setenv("JWT_SECRET","literal-method-test");t.Setenv("JWT_EXPIRES","1h");t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 app:=&App{}
 for _,issue:=range []func()(string,error){func()(string,error){return app.literalToken(t.Context())},func()(string,error){return app.literalExpiry(t.Context())},func()(string,error){return app.literalRefresh(t.Context())}}{token,err:=issue();if err!=nil||token==""{t.Fatal(token,err)}}
 old,err:=app.literalRefresh(t.Context());if err!=nil{t.Fatal(err)};token,err:=app.literalRefreshToken(t.Context(),old);if err!=nil||token==""{t.Fatal(token,err)}
 hash,err:=luxocrypto.HashPassword("secret");if err!=nil{t.Fatal(err)};ok,err:=app.literalVerify(t.Context(),hash,"secret");if err!=nil||!ok{t.Fatal(ok,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "password": 2}}})
}

func TestPermissionUnsupportedSourceFallbacks(t *testing.T) {
	for _, condition := range []string{`my.role.length > 0`, `my.id / 2 > 0`} {
		t.Run(condition, func(t *testing.T) {
			source := `api restricted(): Int @auth(permission: { ` + condition + ` }) { return 42 }`
			input := analyzePermissionFixture(t, source)
			t.Logf("retained semantic diagnostics: %v", input.Errors)
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"restricted": 1}}})
			if generated != nil || err == nil {
				t.Fatalf("unsupported permission produced output: output=%v error=%v", generated != nil, err)
			}
			if len(input.Errors) == 0 && !strings.Contains(err.Error(), "unsupported @auth permission") {
				t.Fatalf("unsupported permission lost its explicit diagnostic: %v", err)
			}
		})
	}
}
