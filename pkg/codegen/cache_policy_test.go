package codegen

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedNamedPolicyExecutesStaticContract(t *testing.T) {
	runGeneratedNamedPolicy(t, "", nil)
}

func TestGeneratedCacheRequestContext(t *testing.T) {
	runGeneratedCompilation(t, generateCacheContextFixture(t), cacheContextAssertions)
}

const cacheContextSource = `
api localized(locale: String): String @native @cache(policy: "preview") @cacheable(dependsOn: [])
api localizedFailure(locale: String): String @native @cache(policy: "preview") @cacheable(dependsOn: [])
`

func generateCacheContextFixture(t *testing.T) *GenerateResult {
	t.Helper()
	tokens, lexErrors := lexer.New(cacheContextSource, "cache-context.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("cache-context.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	input := analyzeCachedFixture(t, []*ast.File{file}, false)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: StableIDs{
		APIs:      map[string]int{"localized": 1, "localizedFailure": 2},
		APIParams: map[string]map[string]int{"localized": {"locale": 1}, "localizedFailure": {"locale": 1}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &GenerateResult{Files: map[string][]byte{
		"schema.gen.go": generated.Files["schema.gen.go"], "handler.gen.go": generated.Files["handler.gen.go"],
	}}
}

func TestGeneratedCacheUnaryTransports(t *testing.T) {
	generated := generateCacheContextFixture(t)
	dir := writeProtocolFixture(t, "package generated\n", cacheContextAssertions)
	for name, data := range generated.Files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("cache-g1 artifact %s sha256=%x bytes=%d", name, sha256.Sum256(data), len(data))
	}
	if err := os.WriteFile(filepath.Join(dir, "transport_test.go"), []byte(cacheUnaryTransportAssertions), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("cache-g1 DSL sha256=%x fixture=%s", sha256.Sum256([]byte(cacheContextSource)), dir)
	command := exec.Command("go", "test", "-race", "-count=1", "-timeout=45s", "-run=^TestGeneratedCacheUnaryTransports$", "-v", ".")
	command.Dir = dir
	output, err := command.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("generated cache unary transports: %v", err)
	}
}

const cacheUnaryTransportAssertions = `package generated

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/rpc"
	"nhooyr.io/websocket"
)

type cacheUnaryCall func(uint64, string) (string, int)

func TestGeneratedCacheUnaryTransports(t *testing.T) {
	for _, transport := range []string{"ws-json", "ws-binary", "lrpc-binary"} {
		t.Run(transport, func(t *testing.T) {
			router, backend, resolver := contextCacheRouter(t)
			var call cacheUnaryCall
			if transport == "lrpc-binary" {
				call = cacheUnaryRPCCaller(t, router)
			} else {
				call = cacheUnaryWSCaller(t, router, transport == "ws-binary")
			}
			verifyCacheUnarySequence(t, call, backend, resolver)
		})
	}
}

func verifyCacheUnarySequence(t *testing.T, call cacheUnaryCall, backend *cache.MemoryCache, resolver *contextResolver) {
	t.Helper()
	steps := []struct {
		locale  string
		recover bool
		value   string
		code    int
		origins int64
		entries int
	}{
		{"Hit", true, "Hit", 0, 1, 1},
		{"Hit", true, "Hit", 0, 1, 1},
		{"Recover", false, "", 404, 2, 1},
		{"Recover", true, "Recover", 0, 3, 2},
		{"Recover", true, "Recover", 0, 3, 2},
	}
	for index, step := range steps {
		resolver.recover.Store(step.recover)
		ordinal := uint64(index + 1)
		value, code := call(ordinal, step.locale)
		if value != step.value || code != step.code || resolver.failures.Load() != step.origins || backend.Len() != step.entries {
			t.Fatalf("request=%d value=%q code=%d origins=%d entries=%d; want %+v", ordinal, value, code, resolver.failures.Load(), backend.Len(), step)
		}
		t.Logf("request=%d locale=%s code=%d origins=%d entries=%d", ordinal, step.locale, code, step.origins, step.entries)
	}
}

func cacheUnaryParams(t *testing.T, locale string) []byte {
	t.Helper()
	params, err := codec.AppendString(codec.AppendVarint(nil, 1), locale)
	if err != nil {
		t.Fatal(err)
	}
	return append(params, 0)
}

func cacheUnaryWSCaller(t *testing.T, router *api.Router, binary bool) cacheUnaryCall {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		defer close(done)
		router.ServeHTTP(w, request)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("owned WebSocket endpoint=%s binary=%t", server.URL, binary)
	t.Cleanup(func() {
		if err := conn.CloseNow(); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned WebSocket handler did not stop")
		}
	})
	return func(id uint64, locale string) (string, int) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		kind, request := cacheUnaryWSRequest(t, router, binary, id, locale)
		if err := conn.Write(ctx, kind, request); err != nil {
			t.Fatal(err)
		}
		actualKind, response, err := conn.Read(ctx)
		if err != nil || actualKind != kind {
			t.Fatalf("WebSocket response kind=%v error=%v", actualKind, err)
		}
		return decodeCacheUnaryWS(t, binary, id, response)
	}
}

func cacheUnaryWSRequest(t *testing.T, router *api.Router, binary bool, id uint64, locale string) (websocket.MessageType, []byte) {
	t.Helper()
	if binary {
		request := codec.AppendVarint([]byte{api.BinaryFrameCallRequest}, id)
		request = codec.AppendVarint(request, uint64(router.Schema.APIs["localizedFailure"].ID))
		request = append(request, 0)
		return websocket.MessageBinary, append(request, cacheUnaryParams(t, locale)...)
	}
	value, err := json.Marshal(locale)
	if err != nil {
		t.Fatal(err)
	}
	request := strconv.AppendUint([]byte("{\"$id\":"), id, 10)
	request = append(request, []byte(",\"$api\":\"localizedFailure\",\"locale\":")...)
	return websocket.MessageText, append(append(request, value...), '}')
}

func decodeCacheUnaryWS(t *testing.T, binary bool, id uint64, body []byte) (string, int) {
	t.Helper()
	if binary {
		actualID, size := codec.ReadVarint(body, 1)
		if size <= 0 || actualID != id {
			t.Fatalf("WebSocket ID=%d want=%d body=%x", actualID, id, body)
		}
		if body[0] == api.BinaryFrameCallError {
			failure, err := api.DecodeBinaryError(body[1+size:], 0)
			if err != nil {
				t.Fatal(err)
			}
			return "", failure.Code
		}
		if body[0] != api.BinaryFrameCallSuccess {
			t.Fatalf("unexpected WebSocket frame %x", body)
		}
		return decodeCacheUnaryString(t, body[1+size:]), 0
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	var actualID uint64
	if err := json.Unmarshal(envelope["$id"], &actualID); err != nil || actualID != id {
		t.Fatalf("WebSocket ID=%d want=%d error=%v", actualID, id, err)
	}
	if data := envelope["code"]; len(data) != 0 {
		var code int
		if err := json.Unmarshal(data, &code); err != nil {
			t.Fatal(err)
		}
		return "", code
	}
	var value string
	if err := json.Unmarshal(envelope["data"], &value); err != nil {
		t.Fatal(err)
	}
	return value, 0
}

func decodeCacheUnaryString(t *testing.T, data []byte) string {
	t.Helper()
	decoder := codec.NewDecoder(data)
	value := decoder.ReadString()
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	return value
}

type cacheUnaryListener struct {
	net.Listener
	accepted atomic.Int64
}

func (listener *cacheUnaryListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err == nil {
		listener.accepted.Add(1)
	}
	return connection, err
}

func cacheUnaryRPCCaller(t *testing.T, router *api.Router) cacheUnaryCall {
	t.Helper()
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &cacheUnaryListener{Listener: base}
	server := rpc.NewServer(router)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := rpc.NewClient(listener.Addr().String())
	t.Logf("owned LRPC endpoint=%s; serial responses have no wire call ID", listener.Addr())
	t.Cleanup(func() {
		client.Close()
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if listener.accepted.Load() != 1 {
			t.Errorf("LRPC accepted %d connections, want one reused connection", listener.accepted.Load())
		}
	})
	return func(_ uint64, locale string) (string, int) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		body, err := client.CallWithMaskContext(ctx, "", router.Schema.APIs["localizedFailure"].ID, nil, cacheUnaryParams(t, locale))
		if err != nil {
			var failure *luxerrors.AppError
			if !errors.As(err, &failure) {
				t.Fatal(err)
			}
			return "", failure.Code
		}
		return decodeCacheUnaryString(t, body), 0
	}
}

`

const cacheContextAssertions = `package generated

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
)

type App struct{ Resolver *contextResolver }
type contextResolver struct {
	calls    atomic.Int64
	failures atomic.Int64
	recover  atomic.Bool
}

func (r *contextResolver) Localized(_ context.Context, locale string) (string, error) {
	r.calls.Add(1)
	return locale, nil
}

func (r *contextResolver) LocalizedFailure(_ context.Context, locale string) (string, error) {
	r.failures.Add(1)
	if r.recover.Load() {
		return locale, nil
	}
	return "", luxerrors.NotFound.WithData(luxerrors.ResourceError{Resource: locale})
}

func contextCacheRouter(t *testing.T) (*api.Router, *cache.MemoryCache, *contextResolver) {
	t.Helper()
	backend := cache.NewMemory()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	binding, err := api.NewResponseCache(backend, api.CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "context"}, api.ResponseOriginOptions{MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	if err := RegisterSchema(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	resolver := &contextResolver{}
	if err := RegisterHandlers(router, &App{Resolver: resolver}); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router, backend, resolver
}

func contextCacheCall(t *testing.T, router *api.Router, binary bool, name, locale, language string) *httptest.ResponseRecorder {
	t.Helper()
	value, err := json.Marshal(locale)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte("{\"$api\":\""+name+"\",\"locale\":"), value...)
	body = append(body, '}')
	if binary {
		body = codec.AppendVarint(nil, uint64(router.Schema.APIs[name].ID))
		body = append(body, 0, 1)
		body, err = codec.AppendString(body, locale)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, 0)
	}
	request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", language)
	if binary {
		request.Header.Set("Content-Type", "application/x-luxo")
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func contextCacheString(t *testing.T, response *httptest.ResponseRecorder, binary bool, want string) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("unexpected success status %d: %s", response.Code, response.Body.String())
	}
	var got string
	if binary {
		decoder := codec.NewDecoder(response.Body.Bytes())
		got = decoder.ReadString()
		if err := decoder.Finish(); err != nil {
			t.Fatal(err)
		}
	} else {
		var envelope struct{ Data *string }
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data == nil {
			t.Fatalf("missing success value: %s, %v", response.Body.String(), err)
		}
		got = *envelope.Data
	}
	if got != want {
		t.Fatalf("cached locale = %q, want %q", got, want)
	}
}

func TestExplicitLocaleSeparatesCachedResults(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "Binary"}[binary], func(t *testing.T) {
			router, backend, resolver := contextCacheRouter(t)
			for _, sample := range []struct{ locale, header string }{{"en", "zh-CN"}, {"zh", "en-US"}, {"en", "fr"}, {"zh", "de"}} {
				response := contextCacheCall(t, router, binary, "localized", sample.locale, sample.header)
				contextCacheString(t, response, binary, sample.locale)
			}
			if resolver.calls.Load() != 2 || backend.Len() != 2 {
				t.Fatalf("explicit locale lost or irrelevant header split cache: calls=%d entries=%d", resolver.calls.Load(), backend.Len())
			}
		})
	}
}

func contextCacheTranslator(t *testing.T, router *api.Router) {
	t.Helper()
	dir, err := os.MkdirTemp(".", "cache-translations-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	for language, message := range map[string]string{"en": "Missing ${resource}", "zh": "找不到 ${resource}"} {
		if err := os.WriteFile(filepath.Join(dir, language+".yaml"), []byte("error:\n  not_found: \""+message+"\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	translator := i18n.New("en")
	if err := translator.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	router.SetTranslator(translator)
}

func contextCacheError(t *testing.T, response *httptest.ResponseRecorder, binary bool, want string) {
	t.Helper()
	if response.Code != 404 {
		t.Fatalf("unexpected error status %d: %s", response.Code, response.Body.String())
	}
	var got string
	var data json.RawMessage
	if binary {
		failure, err := api.DecodeBinaryError(response.Body.Bytes(), response.Code)
		if err != nil {
			t.Fatal(err)
		}
		got, data = failure.Message, failure.Data
	} else {
		var failure struct {
			Message string
			Code    int
			Data    json.RawMessage
		}
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Code != 404 {
			t.Fatalf("bad error: %s, %v", response.Body.String(), err)
		}
		got, data = failure.Message, failure.Data
	}
	var detail struct{ Resource string }
	if err := json.Unmarshal(data, &detail); err != nil || detail.Resource != "User" {
		t.Fatalf("lost error data: %s, %v", data, err)
	}
	if got != want {
		t.Fatalf("translated error=%q, want %q", got, want)
	}
}

func TestRequestLanguageErrorsRemainUncached(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "Binary"}[binary], func(t *testing.T) {
			router, backend, resolver := contextCacheRouter(t)
			contextCacheTranslator(t, router)
			for _, sample := range []struct{ language, message string }{{"zh-CN", "找不到 User"}, {"en-US", "Missing User"}, {"fr", "Missing User"}, {"", "Missing User"}} {
				response := contextCacheCall(t, router, binary, "localizedFailure", "User", sample.language)
				contextCacheError(t, response, binary, sample.message)
			}
			if resolver.failures.Load() != 4 || backend.Len() != 0 {
				t.Fatalf("error entered success cache: calls=%d entries=%d", resolver.failures.Load(), backend.Len())
			}
			resolver.recover.Store(true)
			for _, language := range []string{"en-US", "zh-CN"} {
				response := contextCacheCall(t, router, binary, "localizedFailure", "User", language)
				contextCacheString(t, response, binary, "User")
			}
			if resolver.failures.Load() != 5 || backend.Len() != 1 {
				t.Fatalf("failed origin blocked recovery: calls=%d entries=%d", resolver.failures.Load(), backend.Len())
			}
		})
	}
}
`

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
func writeAdapterFixtureModule(t *testing.T, dir string, adapters ...string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf(`module example.com/luxo-cache-fixture

go 1.27.1

require github.com/light-speak/luxo v1.0.0-beta.1
replace github.com/light-speak/luxo => %s
`, filepath.ToSlash(root))
	for _, adapter := range adapters {
		module += fmt.Sprintf("require github.com/light-speak/luxo/%s v1.0.0-beta.1\nreplace github.com/light-speak/luxo/%s => %s\n", adapter, adapter, filepath.ToSlash(filepath.Join(root, adapter)))
	}
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
