package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func TestGeneratedCacheIdentityLifecycle(t *testing.T) {
	const source = `
api cachedNative(id: Int): Int @native @auth(Admin) @cache(policy: "preview") @cacheable(dependsOn: [])
api cachedCompiled(id: Int): Int @auth(Admin) @cache(policy: "preview") {
  return my.id + id
}
`
	tokens, lexErrors := lexer.New(source, "cache-identity.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("cache-identity.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	input := analyzeCachedFixture(t, []*ast.File{file}, false)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{
		APIs:      map[string]int{"cachedNative": 1, "cachedCompiled": 2},
		APIParams: map[string]map[string]int{"cachedNative": {"id": 1}, "cachedCompiled": {"id": 1}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, &GenerateResult{Files: map[string][]byte{
		"schema.gen.go": generated.Files["schema.gen.go"], "handler.gen.go": generated.Files["handler.gen.go"],
	}}, cacheIdentityLifecycleAssertions)
}

const cacheIdentityLifecycleAssertions = `package generated

import (
  "bytes"
  "context"
  "fmt"
  "io"
  "net"
  "net/http"
  "net/http/httptest"
  "sync/atomic"
  "testing"
  "testing/synctest"
  "time"

  "github.com/light-speak/luxo/pkg/lux/api"
  "github.com/light-speak/luxo/pkg/lux/auth"
  "github.com/light-speak/luxo/pkg/lux/cache"
  "github.com/light-speak/luxo/pkg/lux/codec"
  "github.com/light-speak/luxo/pkg/lux/luvia"
)

const cacheIdentityID int64 = 9007199254740993

type App struct { Resolver *identityResolver }
type identityResolver struct { calls atomic.Int64 }

func (r *identityResolver) CachedNative(ctx context.Context, id int64) (int64, error) {
  r.calls.Add(1)
  identity := luvia.Identity(ctx)
  if identity == nil || identity.ID() != cacheIdentityID {
    return 0, fmt.Errorf("native origin lost exact verified identity")
  }
  switch identity.String("tenant") {
  case "a": return 100 + id, nil
  case "b": return 200 + id, nil
  default: return 0, fmt.Errorf("native origin lost tenant claim")
  }
}

type identityCache struct {
  *cache.MemoryCache
  gets atomic.Int64
  hits atomic.Int64
}

func (backend *identityCache) Get(ctx context.Context, key string) ([]byte, error) {
  backend.gets.Add(1)
  value, err := backend.MemoryCache.Get(ctx, key)
  if value != nil && err == nil { backend.hits.Add(1) }
  return value, err
}

type identityEndpoint struct {
  server *httptest.Server
  client *http.Client
  backend *identityCache
  origin *identityResolver
  binary bool
  protocol int
  connections atomic.Int64
}

func newIdentityEndpoint(t *testing.T, cfg *auth.Config, binary, http2 bool) *identityEndpoint {
  t.Helper()
  endpoint := &identityEndpoint{backend: &identityCache{MemoryCache: cache.NewMemory()}, origin: &identityResolver{}, binary: binary, protocol: 1}
  t.Cleanup(func() { if err := endpoint.backend.Close(); err != nil { t.Error(err) } })
  binding, err := api.NewResponseCache(endpoint.backend, api.CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "identity"}, api.ResponseOriginOptions{MaxConcurrent: 1})
  if err != nil { t.Fatal(err) }
  router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
  if err := RegisterSchema(router.Schema); err != nil { t.Fatal(err) }
  if err := RegisterHandlers(router, &App{Resolver: endpoint.origin}); err != nil { t.Fatal(err) }
  if err := router.Prepare(); err != nil { t.Fatal(err) }
  protocols := new(http.Protocols)
  if http2 { protocols.SetUnencryptedHTTP2(true); endpoint.protocol = 2 } else { protocols.SetHTTP1(true) }
  endpoint.server = httptest.NewUnstartedServer(luvia.AuthMiddleware(cfg, router))
  endpoint.server.Config.Protocols = protocols
  endpoint.server.Config.ConnState = func(_ net.Conn, state http.ConnState) { if state == http.StateNew { endpoint.connections.Add(1) } }
  endpoint.server.Start()
  t.Cleanup(endpoint.server.Close)
  transport := &http.Transport{Protocols: protocols, MaxConnsPerHost: 1}
  t.Cleanup(transport.CloseIdleConnections)
  endpoint.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
  return endpoint
}

func identityPayload(t *testing.T, apiID int, binary bool) []byte {
  t.Helper()
  name := "cachedNative"
  if apiID == 2 { name = "cachedCompiled" }
  if !binary { return []byte(fmt.Sprintf("{\"$api\":%q,\"id\":42}", name)) }
  body := codec.AppendVarint(nil, uint64(apiID))
  body = codec.AppendSvarint(append(body, 0, 1), 42)
  return append(body, 0)
}

func callIdentityEndpoint(t *testing.T, endpoint *identityEndpoint, apiID int, token string, status int, want int64, hit bool) {
  t.Helper()
  beforeGets, beforeHits, beforeCalls := endpoint.backend.gets.Load(), endpoint.backend.hits.Load(), endpoint.origin.calls.Load()
  request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.server.URL, bytes.NewReader(identityPayload(t, apiID, endpoint.binary)))
  if err != nil { t.Fatal(err) }
  request.Header.Set("Content-Type", "application/json")
  if endpoint.binary { request.Header.Set("Content-Type", "application/x-luxo"); request.Header.Set("X-Luxo-Mode", "binary") }
  if token != "" { request.Header.Set("Authorization", "Bearer " + token) }
  response, err := endpoint.client.Do(request)
  if err != nil { t.Fatal(err) }
  body, readErr := io.ReadAll(response.Body)
  closeErr := response.Body.Close()
  if readErr != nil || closeErr != nil { t.Fatalf("response read=%v close=%v", readErr, closeErr) }
  if response.ProtoMajor != endpoint.protocol || response.StatusCode != status {
    t.Fatalf("api=%d binary=%t protocol=%d/%d status=%d/%d body=%q", apiID, endpoint.binary, response.ProtoMajor, endpoint.protocol, response.StatusCode, status, body)
  }
  if status != http.StatusOK {
    if endpoint.backend.gets.Load() != beforeGets || endpoint.backend.hits.Load() != beforeHits || endpoint.origin.calls.Load() != beforeCalls {
      t.Fatal("rejected credentials reached cache or origin")
    }
    return
  }
  expected := codec.AppendSvarint(nil, want)
  if !endpoint.binary { expected = []byte(fmt.Sprintf("{\"data\":%d}", want)) }
  if !endpoint.binary { body = bytes.TrimSpace(body) }
  if !bytes.Equal(body, expected) || endpoint.backend.gets.Load() != beforeGets+1 || (endpoint.backend.hits.Load() != beforeHits) != hit {
    t.Fatalf("identity/cache mismatch api=%d binary=%t body=%q expected=%q hit=%t gets=%d hits=%d", apiID, endpoint.binary, body, expected, hit, endpoint.backend.gets.Load()-beforeGets, endpoint.backend.hits.Load()-beforeHits)
  }
  wantCalls := beforeCalls
  if apiID == 1 && !hit { wantCalls++ }
  if endpoint.origin.calls.Load() != wantCalls {
    t.Fatal("native origin call count does not match cache result")
  }
}

func identityClaims(role, tenant string) auth.ClaimsWriter {
  return func(dst []byte) ([]byte, error) {
    return fmt.Appendf(dst, "{\"id\":%d,\"role\":%q,\"tenant\":%q}", cacheIdentityID, role, tenant), nil
  }
}

func identityToken(t *testing.T, cfg *auth.Config, role, tenant string, expiry time.Duration) string {
  t.Helper()
  token, err := auth.SignWithExpiry(cfg, identityClaims(role, tenant), expiry)
  if err != nil { t.Fatal(err) }
  return token
}

func verifyIdentityPartitions(t *testing.T, endpoint *identityEndpoint, a, b, member, refresh string) {
  t.Helper()
  for apiID := 1; apiID <= 2; apiID++ {
    aValue, bValue := int64(142), int64(242)
    if apiID == 2 { aValue, bValue = cacheIdentityID+42, cacheIdentityID+42 }
    for _, sample := range []struct { token string; value int64; hit bool }{{a,aValue,false},{a,aValue,true},{b,bValue,false},{b,bValue,true}} {
      callIdentityEndpoint(t, endpoint, apiID, sample.token, http.StatusOK, sample.value, sample.hit)
    }
    for _, sample := range []struct { token string; status int }{{member,403},{refresh,401},{a+"invalid",401},{"",401}} {
      callIdentityEndpoint(t, endpoint, apiID, sample.token, sample.status, 0, false)
    }
  }
}

func waitIdentityExpiry(ctx context.Context, expiry time.Time, now func() time.Time) error {
  timer := time.NewTimer(expiry.Sub(now()))
  defer timer.Stop()
  for {
    select {
    case <-timer.C:
      remaining := expiry.Sub(now())
      if remaining <= 0 { return nil }
      timer.Reset(remaining)
    case <-ctx.Done(): return ctx.Err()
    }
  }
}

func TestIdentityExpiryRechecksTheWallClock(t *testing.T) {
  synctest.Test(t, func(t *testing.T) {
    start := time.Now()
    wallStart := time.Unix(1_800_000_000, 0)
    now := func() time.Time {
      elapsed := time.Since(start)
      if elapsed >= time.Second { elapsed -= 300*time.Millisecond }
      return wallStart.Add(elapsed)
    }
    if err := waitIdentityExpiry(t.Context(), wallStart.Add(2*time.Second), now); err != nil {
      t.Fatal(err)
    }
    if elapsed := time.Since(start); elapsed != 2300*time.Millisecond {
      t.Fatalf("expiry wait returned after %s", elapsed)
    }
  })
}

func TestIdentityExpiryWaitHonorsCancellation(t *testing.T) {
  synctest.Test(t, func(t *testing.T) {
    wall := time.Unix(1_800_000_000, 0)
    ctx, cancel := context.WithTimeout(t.Context(), time.Second)
    defer cancel()
    if err := waitIdentityExpiry(ctx, wall.Add(2*time.Second), func() time.Time { return wall }); err != context.DeadlineExceeded {
      t.Fatalf("expiry wait returned %v", err)
    }
  })
}

func TestVerifiedCacheIdentityLifecycle(t *testing.T) {
  cfg := &auth.Config{Secret: "cache-identity-lifecycle-test-only", Expires: time.Hour, RefreshEnabled: true, RefreshExpires: time.Hour}
  var endpoints []*identityEndpoint
  for _, http2 := range []bool{false,true} {
    for _, binary := range []bool{false,true} { endpoints = append(endpoints, newIdentityEndpoint(t,cfg,binary,http2)) }
  }
  a := identityToken(t,cfg,"Admin","a",5*time.Second)
  b := identityToken(t,cfg,"Admin","b",time.Hour)
  member := identityToken(t,cfg,"Member","a",time.Hour)
  refresh, err := auth.SignRefresh(cfg,identityClaims("Admin","a"))
  if err != nil { t.Fatal(err) }
  _, expiry, err := auth.VerifyCachedWithExpiry(cfg,a)
  if err != nil { t.Fatal(err) }
  for _, endpoint := range endpoints { verifyIdentityPartitions(t,endpoint,a,b,member,refresh) }
  expiryCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
  defer cancel()
  if err := waitIdentityExpiry(expiryCtx, time.Unix(expiry, 0), time.Now); err != nil { t.Fatal(err) }
  claims, err := auth.VerifyRefresh(cfg,refresh)
  if err != nil { t.Fatal(err) }
  renewed, err := auth.Sign(cfg,claims.AppendJSON)
  if err != nil { t.Fatal(err) }
  if renewed == a { t.Fatal("renewed credential retained the expired token") }
  for _, endpoint := range endpoints {
    if endpoint.backend.Len() != 4 { t.Fatal("fixture lost cached values before rejecting the expired token") }
    for apiID := 1; apiID <= 2; apiID++ {
      callIdentityEndpoint(t,endpoint,apiID,a,401,0,false)
      want, other := int64(142), int64(242)
      if apiID == 2 { want, other = cacheIdentityID+42, cacheIdentityID+42 }
      callIdentityEndpoint(t,endpoint,apiID,b,200,other,true)
      callIdentityEndpoint(t,endpoint,apiID,renewed,200,want,false)
      callIdentityEndpoint(t,endpoint,apiID,renewed,200,want,true)
    }
    if endpoint.backend.Len() != 6 { t.Fatal("renewed credential did not create a separate private cache partition") }
    if endpoint.connections.Load() != 1 { t.Fatalf("expected one reused connection, got %d", endpoint.connections.Load()) }
  }
  t.Log("96 HTTP calls: H1/H2 x JSON/Binary; same-ID tenants, roles, refresh rejection, cached expiry, explicit renewal; one reused connection per endpoint")
}

`
