package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedNamedPolicyExecutesStaticContract(t *testing.T) {
	runGeneratedNamedPolicy(t, "", nil)
}

func runGeneratedNamedPolicy(t *testing.T, additionalTests string, environment []string) {
	t.Helper()
	policies, err := cachepolicy.Parse([]byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"30s"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	declaration := cachedTestAPI(true)
	declaration.Directives = declaration.Directives[1:]
	declaration.Directives[0].Args = []*ast.NamedArg{{Name: "policy", Value: &ast.Literal{Kind: token.String, Value: "preview"}}}
	declaration.Directives[2].Args[0].Value = &ast.ListExpr{Items: []ast.Expr{&ast.Literal{Kind: token.String, Value: "pricing/rates"}}}
	control := &ast.ApiDecl{
		Name: "uncachedNative", Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
		ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}},
	}
	input := semantic.AnalyzeWithOptions([]*ast.File{{APIs: []*ast.ApiDecl{declaration, control}}}, semantic.Options{CachePolicies: policies})
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: StableIDs{
		APIs:      map[string]int{"cachedNative": 1, "uncachedNative": 2},
		APIParams: map[string]map[string]int{"cachedNative": {"id": 1}, "uncachedNative": {"id": 1}},
	}})
	generated, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, string(generated.Files["schema.gen.go"]), namedPolicyAssertions)
	if err := os.WriteFile(filepath.Join(dir, "handler.gen.go"), generated.Files["handler.gen.go"], 0600); err != nil {
		t.Fatal(err)
	}
	if additionalTests != "" {
		writeAdapterFixtureModule(t, dir, "pkg/lux/cache/redis")
		if err := os.WriteFile(filepath.Join(dir, "redis_test.go"), []byte(additionalTests), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"test", "-count=1", "-timeout=90s"}
	if additionalTests != "" {
		args = append(args, "-mod=mod")
	}
	if testing.Verbose() {
		args = append(args, "-v")
	}
	benchmark := os.Getenv("LUXO_NAMED_CACHE_BENCHMARK") == "1"
	if benchmark {
		args = append(args, "-run=^$", "-bench=^BenchmarkGeneratedTTLHTTP$", "-benchmem", "-benchtime=500ms")
	}
	command := exec.Command("go", append(args, ".")...)
	command.Dir = dir
	command.Env = append(os.Environ(), environment...)
	if additionalTests != "" {
		command.Env = append(command.Env, "GOWORK=off")
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated named policy: %v\n%s", err, output)
	} else if testing.Verbose() || benchmark {
		t.Logf("%s", output)
	}
}

// Adapter integration fixtures own their dependencies; the core module must
// not require Redis merely because generated integration tests exercise it.
func writeAdapterFixtureModule(t *testing.T, dir, adapter string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf(`module example.com/luxo-cache-fixture

go 1.27.1

require (
 github.com/light-speak/luxo v1.0.0-beta.1
 github.com/light-speak/luxo/%s v1.0.0-beta.1
)
replace github.com/light-speak/luxo => %s
replace github.com/light-speak/luxo/%s => %s
`, adapter, filepath.ToSlash(root), adapter, filepath.ToSlash(filepath.Join(root, adapter)))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
}

const namedPolicyAssertions = `package generated
import("bytes";"context";"encoding/json";"math";"net/http/httptest";"strings";"sync/atomic";"testing";"time";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/cache";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
type App struct{Resolver *policyResolver}
type policyResolver struct{calls atomic.Int64; beforeReturn func(context.Context,int64)error}
func(r *policyResolver)UncachedNative(ctx context.Context,id int64)(int64,error){return r.CachedNative(ctx,id)}
func(r *policyResolver)CachedNative(ctx context.Context,id int64)(int64,error){
 r.calls.Add(1)
 if r.beforeReturn!=nil {if err:=r.beforeReturn(ctx,id);err!=nil{return 0,err}}
 return id,nil
}
func TestGeneratedSchemaRejectsConflicts(t *testing.T){
 s:=schema.New()
 if err:=s.RegisterAPI(&schema.API{Name:"existing",ID:1});err!=nil{t.Fatal(err)}
 if err:=RegisterSchema(s);err==nil{t.Fatal("generated schema registration discarded API conflict")}
 if len(s.APIs)!=1||s.APIs["existing"]==nil{t.Fatal("failed API registration replaced existing binding")}
}
func TestGeneratedRegistryRejectsConflicts(t *testing.T){
 for _,parameters:=range []bool{false,true}{
  router:=api.NewRouter()
  if parameters{
   if err:=router.Registry.RegisterParams("cachedNative",[]api.ParamMeta{{Name:"id",Type:"String",FieldID:1}});err!=nil{t.Fatal(err)}
  }else{
   if err:=router.Registry.Register("other",1);err!=nil{t.Fatal(err)}
  }
  if err:=RegisterHandlers(router,&App{Resolver:&policyResolver{}});err==nil{t.Fatal("generated registration discarded conflict")}
 }
}
func TestGeneratedPolicy(t *testing.T){
 backend:=cache.NewMemory();defer backend.Close()
 binding,err:=api.NewResponseCache(backend,api.CacheNamespace{Project:"p",Environment:"e",Service:"s",Revision:"r"},api.ResponseOriginOptions{MaxConcurrent:16});if err!=nil{t.Fatal(err)}
 router:=api.NewRouterWithOptions(api.RouterOptions{ResponseCache:binding})
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 app:=&App{Resolver:&policyResolver{}}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 contract:=router.Schema.APIs["cachedNative"].CachePolicy
 if contract==nil||contract.Mode!="ttl"||contract.Scope!="private"||contract.TTL!=30*time.Second{t.Fatalf("lost policy: %+v",contract)}
 for range 2{
  req:=httptest.NewRequest("POST","/",strings.NewReader("{\"$api\":\"cachedNative\",\"id\":9223372036854775807}"));req.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder();router.ServeHTTP(response,req)
  if response.Code!=200||!strings.Contains(response.Body.String(),"9223372036854775807"){t.Fatalf("response %d: %s",response.Code,response.Body.String())}
 }
 if app.Resolver.calls.Load()!=1{t.Fatalf("cache miss count: %d",app.Resolver.calls.Load())}
 definition:=router.Schema.APIs["cachedNative"]
 binary,err:=api.EncodeBinaryRequest(definition.ID,nil,map[string]any{"id":int64(math.MaxInt64-1)},router.Registry.ParamOrder("cachedNative"));if err!=nil{t.Fatal(err)}
 for range 2{
  req:=httptest.NewRequest("POST","/",bytes.NewReader(binary));req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")
  response:=httptest.NewRecorder();router.ServeHTTP(response,req)
  if response.Code!=200||!bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,math.MaxInt64-1)){t.Fatalf("binary response %d: %x",response.Code,response.Body.Bytes())}
 }
 if app.Resolver.calls.Load()!=2{t.Fatalf("binary cache miss count: %d",app.Resolver.calls.Load())}
 raw,err:=json.Marshal(router.Schema);if err!=nil{t.Fatal(err)}
 if strings.Contains(string(raw),"CachePolicy")||strings.Contains(string(raw),"cachePolicy"){t.Fatalf("server policy entered SDK schema: %s",raw)}
 var imported schema.Schema
 if err:=json.Unmarshal([]byte("{\"apis\":{\"cachedNative\":{\"name\":\"cachedNative\",\"CachePolicy\":{\"mode\":\"ttl\"},\"cachePolicy\":{\"mode\":\"ttl\"}}}}"),&imported);err!=nil{t.Fatal(err)}
 if imported.APIs["cachedNative"].CachePolicy!=nil{t.Fatal("introspection forged server contract")}
}
`
