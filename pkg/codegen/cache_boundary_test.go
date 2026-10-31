package codegen

import (
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func analyzeCachedFixture(t *testing.T, files []*ast.File, modules bool) *semantic.Result {
	t.Helper()
	policies, err := cachepolicy.Parse([]byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1m"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return semantic.AnalyzeWithOptions(files, semantic.Options{CachePolicies: policies, ModuleIsolation: modules})
}

func TestGeneratePreservesNamedCachePolicyContract(t *testing.T) {
	policies, err := cachepolicy.Parse([]byte(`{"version":1,"policies":{"detail":{"mode":"ttl","ttl":"5m","scope":"private"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	declaration := cachedTestAPI(true)
	declaration.Directives = declaration.Directives[1:]
	declaration.Directives[0].Args = []*ast.NamedArg{{Name: "policy", Value: &ast.Literal{Kind: token.String, Value: "detail"}}}
	analyzed := semantic.AnalyzeWithOptions([]*ast.File{{APIs: []*ast.ApiDecl{declaration}}}, semantic.Options{CachePolicies: policies})
	if len(analyzed.Errors) != 0 {
		t.Fatal(analyzed.Errors)
	}
	generated, err := Generate(analyzed, "generated", cachedGenerationConfig())
	if err != nil {
		t.Fatal(err)
	}
	metadata := string(generated.Files["schema.gen.go"])
	if !strings.Contains(metadata, `CachePolicy = &schema.CachePolicy{Mode: "ttl", TTL: 300000000000, Scope: "private"}`) {
		t.Fatalf("named contract lost or downgraded: %s", metadata)
	}
	if !strings.Contains(string(generated.Files["handler.gen.go"]), "api.CacheResponsePolicy(") {
		t.Fatal("named contract not used by handler")
	}
	analyzed.CachePolicies = nil
	if generated, err := Generate(analyzed, "generated", cachedGenerationConfig()); err == nil || generated != nil {
		t.Fatal("missing compiled policy was guessed")
	}
}

func TestGenerateRejectsInvalidResolvedCachePolicy(t *testing.T) {
	for _, value := range []ast.Expr{
		nil, &ast.Ident{Name: "preview"},
		&ast.Literal{Kind: token.Int, Value: "1"},
		&ast.Literal{Kind: token.String, Value: ""},
		&ast.Literal{Kind: token.String, Value: "preview"},
	} {
		declaration := cachedTestAPI(true)
		directive := declaration.Directives[1]
		directive.Args = []*ast.NamedArg{{Name: "policy", Value: value}}
		input := result(&ast.File{APIs: []*ast.ApiDecl{declaration}})
		input.CachePolicies = map[*ast.Directive]cachepolicy.Policy{directive: {}}
		generated, err := Generate(input, "generated", cachedGenerationConfig())
		if err == nil || generated != nil || !strings.Contains(err.Error(), "policy") {
			t.Fatalf("invalid resolved policy accepted: %v", err)
		}
	}
}

func TestGenerateRejectsInjectedInvalidationPolicy(t *testing.T) {
	declaration := cachedTestAPI(true)
	input := analyzeCachedFixture(t, []*ast.File{{APIs: []*ast.ApiDecl{declaration}}}, false)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	input.CachePolicies[declaration.Directives[1]] = cachepolicy.Policy{
		Mode: cachepolicy.Mode("invalidation"), TTL: time.Minute, Scope: cachepolicy.Private,
	}
	generated, err := Generate(input, "generated", cachedGenerationConfig())
	if err == nil || generated != nil || !strings.Contains(err.Error(), "invalidation") {
		t.Fatalf("direct generator input bypassed TTL-only contract: %v", err)
	}
}

func TestGeneratedCacheAuthorizationAndIdentity(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "context"
    "github.com/light-speak/luxo/pkg/lux"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/errors"
    "github.com/light-speak/luxo/pkg/lux/luvia"
)
type App struct{ Resolver resolver; User countClient }
type resolver struct{}
func (resolver) CachedNative(ctx context.Context, id int64) (int64,error) { return luvia.Identity(ctx).ID()+id,nil }
func (resolver) PublicCached(ctx context.Context, id int64) (int64,error) {
    if identity:=luvia.Identity(ctx); identity!=nil {return identity.ID()+id,nil}
    return id,nil
}
type countClient struct{}
func (c countClient) Where(...lux.Condition) countClient { return c }
func (countClient) Count(ctx context.Context) (int64,error) { return luvia.Identity(ctx).ID()+42,nil }
`)
	for _, native := range []bool{false, true} {
		declaration := cachedTestAPI(native)
		if native {
			generateNativeAPIHandler(&source, declaration, nil, nil)
		} else {
			compileAPIBody(&source, declaration, nil, nil)
		}
		source.WriteString("func register" + declaration.Name + "(router *api.Router) { app:=&App{};")
		writeHandlerRegistrationMode(&source, declaration.Name, declaration.Directives, true)
		source.WriteString("}\n")
	}
	declaration := cachedTestAPI(false)
	declaration.Name, declaration.Body = "countUsersById", nil
	model := testModel("User", nil, []*ast.FieldDecl{testField("id", "Int")})
	generateInferredHandler(&source, declaration, &InferredAPI{
		Action: "count", ModelName: "User", Groups: []ClauseGroup{{Clauses: []InferClause{{Field: "id", Op: "eq"}}}},
	}, nil, model)
	source.WriteString("func registercountUsersById(router *api.Router) { app:=&App{};")
	writeHandlerRegistrationMode(&source, declaration.Name, declaration.Directives, true)
	source.WriteString("}\n")
	public := cachedTestAPI(true)
	public.Name, public.Directives = "publicCached", public.Directives[1:]
	generateNativeAPIHandler(&source, public, nil, nil)
	runGeneratedProtocolPackage(t, source.String(), cacheBoundaryAssertions+publicCacheAssertions+cacheBoundaryBenchmarks)
}

func TestInferredCacheInheritsModelAuthorization(t *testing.T) {
	for _, authName := range []string{"auth", "withAuth"} {
		declaration := cachedTestAPI(false)
		declaration.Directives = declaration.Directives[1:]
		model := testModel("User", []*ast.Directive{{Name: authName}}, []*ast.FieldDecl{testField("id", "Int")})
		var source strings.Builder
		generateInferredHandler(&source, declaration, &InferredAPI{
			Action: "count", ModelName: "User", Groups: []ClauseGroup{{Clauses: []InferClause{{Field: "id", Op: "eq"}}}},
		}, nil, model)
		code := source.String()
		authAt, readAt, cacheAt := strings.Index(code, "if identity == nil"), strings.Index(code, "req.ParamInt"), strings.Index(code, "api.CacheResponse")
		if authAt < 0 || readAt < authAt || cacheAt < readAt {
			t.Fatalf("%s cache preflight order: %s", authName, code)
		}
	}
}

func TestCachedBodyRequiresValidTTL(t *testing.T) {
	for _, value := range []ast.Expr{nil, &ast.Ident{Name: "invalid"}, &ast.Literal{Kind: token.String, Value: "invalid"}} {
		var source strings.Builder
		declaration := cachedTestAPI(false)
		declaration.Directives = []*ast.Directive{{Name: "cache", Args: []*ast.NamedArg{{Value: value}}}}
		compileAPIBody(&source, declaration, nil, nil)
		if strings.Contains(source.String(), "api.CacheResponse") {
			t.Fatalf("invalid TTL emitted cache code: %s", source.String())
		}
	}
}

func TestGenerateRejectsInvalidCacheTTLBeforeProducingFiles(t *testing.T) {
	valid := cachedTestAPI(true).Directives[1]
	for _, directives := range [][]*ast.Directive{
		{{Name: "cache"}},
		{{Name: "cache", Args: []*ast.NamedArg{{Name: "ttl", Value: &ast.Literal{Kind: token.Int, Value: "60"}}}}},
		{{Name: "cache", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Duration, Value: "1m"}}}}},
		{{Name: "cache", Args: []*ast.NamedArg{{Name: "tll", Value: &ast.Literal{Kind: token.Int, Value: "60"}}}}},
		{{Name: "cache", Args: []*ast.NamedArg{{Name: "ttl", Value: &ast.Literal{Kind: token.Int, Value: "0"}}}}},
		{{Name: "cache", Args: []*ast.NamedArg{{Name: "ttl", Value: &ast.Literal{Kind: token.Int, Value: "9223372037"}}}}},
		{valid, valid},
	} {
		api := cachedTestAPI(true)
		api.Directives = append(directives, &ast.Directive{Name: "native"})
		generated, err := Generate(result(&ast.File{APIs: []*ast.ApiDecl{api}}), "generated", cachedGenerationConfig())
		if err == nil || generated != nil || !strings.Contains(err.Error(), "@cache") {
			t.Fatalf("invalid cache generated files: %v", err)
		}
	}
}

func TestCacheTTLIsGeneratedAsValidatedNanoseconds(t *testing.T) {
	for _, value := range []string{`60`, `"060s"`} {
		api := cachedTestAPI(true)
		api.Directives = api.Directives[1:] // This test does not need authentication metadata.
		policies, err := cachepolicy.Parse([]byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":` + value + `}}}`))
		if err != nil {
			t.Fatal(err)
		}
		analyzed := semantic.AnalyzeWithOptions([]*ast.File{{APIs: []*ast.ApiDecl{api}}}, semantic.Options{CachePolicies: policies})
		if len(analyzed.Errors) != 0 {
			t.Fatal(analyzed.Errors)
		}
		generated, err := Generate(analyzed, "generated", cachedGenerationConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(generated.Files["schema.gen.go"]), "TTL: 60000000000") {
			t.Fatalf("TTL was not normalized from decimal seconds: %s", generated.Files["schema.gen.go"])
		}
	}
}

func TestGenerateRequiresNativeCacheDependencyAnalysis(t *testing.T) {
	api := cachedTestAPI(true)
	api.Directives = api.Directives[1:]
	file := &ast.File{APIs: []*ast.ApiDecl{api}}
	analyzed := analyzeCachedFixture(t, []*ast.File{file}, false)
	if len(analyzed.Errors) != 0 {
		t.Fatal(analyzed.Errors)
	}
	stripped := &semantic.Result{Files: analyzed.Files, CachePolicies: analyzed.CachePolicies}
	generated, err := Generate(stripped, "generated", cachedGenerationConfig())
	if err == nil || generated != nil || !strings.Contains(err.Error(), "cache dependency analysis") {
		t.Fatalf("generation accepted lost dependency metadata: %v", err)
	}
	stripped.ExecutionCacheDependencies = analyzed.ExecutionCacheDependencies
	if _, err := Generate(stripped, "generated", cachedGenerationConfig()); err != nil {
		t.Fatal(err)
	}
}

func cachedGenerationConfig() GeneratorConfig {
	return GeneratorConfig{Driver: DriverPG, IDs: StableIDs{
		APIs: map[string]int{"cachedNative": 1}, APIParams: map[string]map[string]int{"cachedNative": {"id": 1}},
	}}
}

func cachedTestAPI(native bool) *ast.ApiDecl {
	declaration := &ast.ApiDecl{
		Name: "cached", ReturnType: &ast.TypeRef{Name: "Int"},
		Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
		Directives: []*ast.Directive{
			{Name: "auth", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Admin"}}}},
			{Name: "cache", Args: []*ast.NamedArg{{Name: "policy", Value: &ast.Literal{Kind: token.String, Value: "preview"}}}},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.BinaryExpr{
			Left: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"},
			Op:   "+", Right: &ast.Ident{Name: "id"},
		}}}},
	}
	if native {
		declaration.Name, declaration.Body = "cachedNative", nil
		declaration.Directives = append(declaration.Directives, &ast.Directive{Name: "native"})
		declaration.Directives = append(declaration.Directives, &ast.Directive{Name: "cacheable", Args: []*ast.NamedArg{
			{Name: "dependsOn", Value: &ast.ListExpr{}},
		}})
	}
	return declaration
}

const cacheBoundaryAssertions = `package generated
import (
    "bytes"
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/auth"
    "github.com/light-speak/luxo/pkg/lux/cache"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/luvia"
    "github.com/light-speak/luxo/pkg/lux/schema"
)
type countedCache struct { cache.Cache; gets,hits int }
func cacheClaims(id int64,role,tenant string)auth.ClaimsWriter{
    return func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":%d,\"role\":%q,\"tenant\":%q}",id,role,tenant),nil}
}
func(c *countedCache)Get(ctx context.Context,key string)([]byte,error){
    c.gets++
    value,err:=c.Cache.Get(ctx,key)
    if value!=nil {c.hits++}
    return value,err
}
func TestCacheCannotBypassGeneratedAuthorization(t *testing.T) {
    memory:=&countedCache{Cache:cache.NewMemory()}
    t.Cleanup(func(){ _=memory.Close() })
    router:=cacheTestRouter(t,memory)
    for i,name:=range []string{"cached","cachedNative","countUsersById"} {
        router.Schema.RegisterAPI(&schema.API{Name:name,ID:i+1,ReturnType:"Int",CacheDependencies:&schema.CacheDependencies{},CachePolicy:&schema.CachePolicy{Mode:"ttl",TTL:time.Minute,Scope:"private"},Params:[]schema.Param{
            {Name:"id",ID:1,Type:schema.FieldInt},
        }})
    }
    router.Registry.RegisterSchemaAPIs(router.Schema)
    registercached(router)
    registercachedNative(router)
    registercountUsersById(router)
    if err:=router.Prepare();err!=nil {t.Fatal(err)}
    cfg:=&auth.Config{Secret:"cache-auth-regression-only-not-a-deployment-secret",Expires:time.Hour}
    sign:=func(id int64,role,tenant string)string{
        signed,err:=auth.Sign(cfg,cacheClaims(id,role,tenant))
        if err!=nil { t.Fatal(err) }
        return signed
    }
    admin,other,member:=sign(1,"Admin","a"),sign(3,"Admin","a"),sign(2,"Member","a")
    otherTenant:=sign(1,"Admin","b")
    endpoint:=luvia.AuthMiddleware(cfg,router)
    for i,name:=range []string{"cached","cachedNative","countUsersById"} {
        for _,binary:=range []bool{false,true} {
            for _,item:=range []struct{name,token string; status int; value int64; hit bool}{
                {"admin",admin,200,43,false},{"admin-hit",admin,200,43,true},
                {"anonymous","",401,0,false},{"member",member,403,0,false},
                {"other",other,200,45,false},{"other-hit",other,200,45,true},
                {"tenant",otherTenant,200,43,false},{"tenant-hit",otherTenant,200,43,true},
                {"invalid-token",admin+"invalid",401,0,false},
            } {
                body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"id\":42}",name))
                if binary {
                    body=codec.AppendSvarint([]byte{byte(i+1),0,1},42)
                    body=append(body,0)
                }
                request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body))
                request.Header.Set("Content-Type","application/json")
                if binary {request.Header.Set("X-Luxo-Mode","binary")}
                if item.token!="" {request.Header.Set("Authorization","Bearer "+item.token)}
                before,hits:=memory.gets,memory.hits
                response:=httptest.NewRecorder()
                endpoint.ServeHTTP(response,request)
                if response.Code!=item.status {t.Fatalf("%s/%s binary=%v: got HTTP %d want %d: %s",name,item.name,binary,response.Code,item.status,response.Body.String())}
                if item.status!=200 {
                    if memory.gets!=before {t.Fatalf("rejected identity accessed cache: %s",item.name)}
                    continue
                }
                if memory.gets!=before+1 || (memory.hits>hits)!=item.hit {t.Fatalf("%s cache access/hit mismatch: gets=%d hits=%d",item.name,memory.gets-before,memory.hits-hits)}
                expected:=[]byte(fmt.Sprintf("{\"data\":%d}",item.value))
                if binary {expected=codec.AppendSvarint(nil,item.value)}
                if !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()),expected) {t.Fatalf("identity response: got %s want %s",response.Body.Bytes(),expected)}
            }
        }
    }
}
func TestCacheReadsParametersBeforeLookup(t *testing.T) {
    memory:=&countedCache{Cache:cache.NewMemory()}
    t.Cleanup(func(){_=memory.Close()})
    cfg:=&auth.Config{Secret:"cache-parameter-regression-only-not-a-deployment-secret",Expires:time.Hour}
    signed,err:=auth.Sign(cfg,cacheClaims(1,"Admin",""))
    if err!=nil {t.Fatal(err)}
    for _,handler:=range []api.HandlerFunc{handleCached(&App{}),handleCachedNative(&App{}),handleCountUsersById(&App{})} {
        router:=cacheTestRouter(t,memory)
        router.Schema.RegisterAPI(&schema.API{ID:1,Name:"cached",ReturnType:"Int",CacheDependencies:&schema.CacheDependencies{},CachePolicy:&schema.CachePolicy{Mode:"ttl",TTL:time.Minute,Scope:"private"}})
        router.RequireCache("cached")
        router.Handle("cached",handler)
        if err:=router.Prepare();err!=nil {t.Fatal(err)}
        handler=router.ExportHandlers()["cached"]
        // Invoke the generated handler without Router normalization to prove the ordering.
        endpoint:=luvia.AuthMiddleware(cfg,http.HandlerFunc(func(_ http.ResponseWriter,r *http.Request) {
            req:=&api.Request{API:"cached",Buf:&api.ResponseBuf{}}
            if err:=handler(r.Context(),req);err==nil {t.Fatal("missing typed parameter accepted")}
        }))
        request:=httptest.NewRequest("POST","/",nil)
        request.Header.Set("Authorization","Bearer "+signed)
        endpoint.ServeHTTP(httptest.NewRecorder(),request)
    }
    if memory.gets!=0 {t.Fatalf("invalid parameters accessed cache %d times",memory.gets)}
}
func TestLocalJSONCannotUseWarmCacheBeforeValidation(t *testing.T) {
    memory:=&countedCache{Cache:cache.NewMemory()}
    t.Cleanup(func(){_=memory.Close()})
    router:=cacheTestRouter(t,memory)
    names:=[]string{"cached","cachedNative","countUsersById"}
    for i,name:=range names {
        router.Schema.RegisterAPI(&schema.API{Name:name,ID:i+1,ReturnType:"Int",CacheDependencies:&schema.CacheDependencies{},CachePolicy:&schema.CachePolicy{Mode:"ttl",TTL:time.Minute,Scope:"private"},Params:[]schema.Param{{Name:"id",ID:1,Type:schema.FieldInt}}})
    }
    router.Registry.RegisterSchemaAPIs(router.Schema)
    registercached(router);registercachedNative(router);registercountUsersById(router)
    if err:=router.Prepare();err!=nil {t.Fatal(err)}
    cfg:=&auth.Config{Secret:"local-input-cache-regression-only-not-a-deployment-secret",Expires:time.Hour}
    token,err:=auth.Sign(cfg,cacheClaims(1,"Admin",""))
    if err!=nil {t.Fatal(err)}
    endpoint:=luvia.AuthMiddleware(cfg,router)
    for _,name:=range names {
        for _,params:=range []string{"\"id\":42","\"id\":null","\"id\":\"42\"","\"id\":9223372036854775808","\"id\":42,\"extra\":0"} {
            request:=httptest.NewRequest("POST","/",bytes.NewBufferString(fmt.Sprintf("{\"$api\":%q,%s}",name,params)))
            request.Header.Set("Content-Type","application/json");request.Header.Set("Authorization","Bearer "+token)
            before:=memory.gets;response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request)
            want:=400;if params=="\"id\":42" {want=200}
            if response.Code!=want {t.Fatalf("%s/%s got %d: %s",name,params,response.Code,response.Body.String())}
            if want!=200 && memory.gets!=before {t.Fatalf("%s accessed warm cache with invalid input: %s",name,params)}
        }
    }
}
func cacheTestRouter(t testing.TB,backend cache.Cache)*api.Router {
    t.Helper()
    binding,err:=api.NewResponseCache(backend,api.CacheNamespace{Project:"project",Environment:"test",Service:"service",Revision:"revision"},api.ResponseOriginOptions{MaxConcurrent:16})
    if err!=nil {t.Fatal(err)}
    return api.NewRouterWithOptions(api.RouterOptions{ResponseCache:binding})
}
`

const publicCacheAssertions = `
func TestPublicCacheKeepsAnonymousPartitionSeparate(t *testing.T) {
    memory:=&countedCache{Cache:cache.NewMemory()}
    t.Cleanup(func(){_=memory.Close()})
    cfg:=&auth.Config{Secret:"cache-public-regression-only-not-a-deployment-secret",Expires:time.Hour}
    signed,err:=auth.Sign(cfg,cacheClaims(5,"",""))
    if err!=nil {t.Fatal(err)}
    router:=cacheTestRouter(t,memory)
    router.Schema.RegisterAPI(&schema.API{Name:"publicCached",ID:1,ReturnType:"Int",CacheDependencies:&schema.CacheDependencies{},CachePolicy:&schema.CachePolicy{Mode:"ttl",TTL:time.Minute,Scope:"private"},Params:[]schema.Param{{Name:"id",ID:1,Type:schema.FieldInt}}})
    router.Registry.RegisterSchemaAPIs(router.Schema)
    router.Handle("publicCached",handlePublicCached(&App{}))
    router.RequireCache("publicCached")
    if err:=router.Prepare();err!=nil {t.Fatal(err)}
    endpoint:=luvia.AuthMiddleware(cfg,router)
    for _,item:=range []struct{token string;value int;hit bool}{
        {"",42,false},{signed,47,false},{"",42,true},{signed,47,true},
        {signed+"invalid",42,true},
    }{
        request:=httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\"publicCached\",\"id\":42}"))
        request.Header.Set("Content-Type","application/json")
        if item.token!="" {request.Header.Set("Authorization","Bearer "+item.token)}
        response:=httptest.NewRecorder()
        hits:=memory.hits
        endpoint.ServeHTTP(response,request)
        if response.Code!=200 || response.Body.String()!=fmt.Sprintf("{\"data\":%d}",item.value) || (memory.hits>hits)!=item.hit {
            t.Fatalf("public cache crossed identity boundary: status=%d body=%s hits=%d",response.Code,response.Body.String(),memory.hits-hits)
        }
    }
}
`
