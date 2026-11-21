package codegen

import (
	"net"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGeneratedNamedPolicySharesRedisAcrossRouters(t *testing.T) {
	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "redis:7.4-alpine", ExposedPorts: []string{"6379/tcp"},
			WaitingFor: wait.ForListeningPort("6379/tcp"),
		}, Started: true,
	})
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(t.Context(), "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedNamedPolicy(t, namedPolicyRedisAssertions+namedPolicyHTTPAssertions+namedPolicyFailureAssertions+namedPolicyHTTPBenchmarks, []string{"LUXO_TEST_POLICY_REDIS=" + net.JoinHostPort(host, port.Port())})
}

const namedPolicyRedisAssertions = `package generated
import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "os"
 "strconv"
 "sync"
 "sync/atomic"
 "testing"
 "time"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/cache"
 rediscache "github.com/light-speak/luxo/pkg/lux/cache/redis"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/redis/go-redis/v9"
)

func redisPolicyRouter(t testing.TB, revision string, ttl time.Duration) (*api.Router, *policyResolver) {
 t.Helper()
 backend,err:=rediscache.NewRedis(os.Getenv("LUXO_TEST_POLICY_REDIS"),rediscache.WithPrefix(t.Name()+":"))
 if err!=nil {t.Fatal(err)}
 t.Cleanup(func(){if err:=backend.Close();err!=nil {t.Error(err)}})
 if err:=backend.Ping(t.Context());err!=nil {t.Fatal(err)}
 return policyRouterWithBackend(t,backend,revision,ttl,api.ResponseOriginOptions{MaxConcurrent:1})
}

func policyRouterWithBackend(t testing.TB,backend cache.Cache,revision string,ttl time.Duration,options api.ResponseOriginOptions)(*api.Router,*policyResolver){
 t.Helper()
 binding,err:=api.NewResponseCache(backend,api.CacheNamespace{Project:"policy",Environment:"test",Service:"native",Revision:revision},options)
 if err!=nil {t.Fatal(err)}
 router:=api.NewRouterWithOptions(api.RouterOptions{ResponseCache:binding})
 if err:=RegisterSchema(router.Schema);err!=nil {t.Fatal(err)}
 router.Schema.APIs["cachedNative"].CachePolicy.TTL=ttl
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil {t.Fatal(err)}
 resolver:=&policyResolver{}
 if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil {t.Fatal(err)}
 if err:=router.Prepare();err!=nil {t.Fatal(err)}
 return router,resolver
}

func policyRequestPayload(t testing.TB,router *api.Router,name string,id int64,binary bool)([]byte,[]byte,string){
 t.Helper()
 body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"id\":%d}",name,id))
 want:=[]byte("{\"data\":"+strconv.FormatInt(id,10)+"}")
 contentType:="application/json"
 if binary {
  var err error
  body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"id":id},router.Registry.ParamOrder(name))
  if err!=nil {t.Fatal(err)}
  want=codec.AppendSvarint(nil,id)
  contentType="application/x-luxo"
 }
 return body,want,contentType
}

func callRedisPolicy(t *testing.T, router *api.Router, id int64, binary bool) {
 t.Helper()
 body,expected,contentType:=policyRequestPayload(t,router,"cachedNative",id,binary)
 req:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 req.Header.Set("Content-Type",contentType)
 if binary {req.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder()
 router.ServeHTTP(response,req)
 actual:=response.Body.Bytes()
 if !binary {actual=bytes.TrimSpace(actual)}
 if response.Code!=200 || !bytes.Equal(actual,expected) {
  t.Fatalf("id=%d binary=%t response=%d %q expected=%q",id,binary,response.Code,response.Body.Bytes(),expected)
 }
}

func TestGeneratedRedisPolicySharedEntries(t *testing.T) {
 first,origin:=redisPolicyRouter(t,"shared",time.Minute)
 second,other:=redisPolicyRouter(t,"shared",time.Minute)
 for _,binary:=range []bool{false,true} {
  for _,id:=range []int64{42,84,9223372036854775807,-9223372036854775808} {
   callRedisPolicy(t,first,id,binary)
   calls:=origin.calls.Load()
   callRedisPolicy(t,second,id,binary)
   callRedisPolicy(t,first,id,binary)
   if origin.calls.Load()!=calls || other.calls.Load()!=0 {t.Fatalf("separate Redis clients did not share entries: %d %d",origin.calls.Load(),other.calls.Load())}
  }
 }
 third,changed:=redisPolicyRouter(t,"different-revision",time.Minute)
 callRedisPolicy(t,third,42,false)
 if changed.calls.Load()!=1 {t.Fatal("deployment revision reused a different deployment's entry")}
 before:=origin.calls.Load()
 req:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\"cachedNative\",\"id\":null}"))
 req.Header.Set("Content-Type","application/json")
 response:=httptest.NewRecorder();first.ServeHTTP(response,req)
 if response.Code!=400 || origin.calls.Load()!=before {t.Fatalf("invalid input reached cached native: %d calls=%d",response.Code,origin.calls.Load())}
}

func TestGeneratedRedisPolicyExpires(t *testing.T) {
 first,_:=redisPolicyRouter(t,"expiry",time.Second)
 second,resolver:=redisPolicyRouter(t,"expiry",time.Second)
 callRedisPolicy(t,first,42,false)
 probe:=redis.NewClient(&redis.Options{Addr:os.Getenv("LUXO_TEST_POLICY_REDIS")})
 defer probe.Close()
 keys,err:=probe.Keys(t.Context(),t.Name()+":*").Result()
 if err!=nil || len(keys)!=1 {t.Fatalf("Redis entry was not stored: keys=%v error=%v",keys,err)}
 ttl,err:=probe.PTTL(t.Context(),keys[0]).Result()
 if err!=nil || ttl<=0 || ttl>time.Second {t.Fatalf("Redis TTL=%v error=%v",ttl,err)}
 deadline:=time.Now().Add(5*time.Second)
 for resolver.calls.Load()==0 && time.Now().Before(deadline) {
  callRedisPolicy(t,second,42,false)
  if resolver.calls.Load()==0 {time.Sleep(10*time.Millisecond)}
 }
 if resolver.calls.Load()==0 {t.Fatal("expired Redis entry never returned to origin")}
}
`
