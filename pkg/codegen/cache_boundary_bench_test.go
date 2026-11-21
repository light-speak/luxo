package codegen

// Both revisions execute the same generated operation through authentication,
// Router preparation, cache lookup and HTTP response encoding. Network I/O,
// Redis and database queries are deliberately not represented by this fixture.
const cacheBoundaryBenchmarks = `
func BenchmarkCacheBoundary(b *testing.B) {
    for _,binary:=range []bool{false,true} {
        for _,hit:=range []bool{false,true} {
            b.Run(fmt.Sprintf("binary=%t/hit=%t",binary,hit),func(b *testing.B){ benchmarkCacheBoundary(b,binary,hit) })
        }
    }
}
func benchmarkCacheBoundary(b *testing.B,binary,hit bool) {
    var backend cache.Cache=cache.Nop{}
    if hit {backend=cache.NewMemory()}
    b.Cleanup(func(){_=backend.Close()})
    router:=cacheTestRouter(b,backend)
    router.Schema.RegisterAPI(&schema.API{Name:"cached",ID:1,ReturnType:"Int",CacheDependencies:&schema.CacheDependencies{},CachePolicy:&schema.CachePolicy{Mode:"ttl",TTL:time.Minute,Scope:"private"},Params:[]schema.Param{{Name:"id",ID:1,Type:schema.FieldInt}}})
    router.Registry.RegisterSchemaAPIs(router.Schema)
    registercached(router)
    if err:=router.Prepare();err!=nil {b.Fatal(err)}
    cfg:=&auth.Config{Secret:"cache-benchmark-only-not-a-deployment-secret",Expires:time.Hour}
    signed,err:=auth.Sign(cfg,cacheClaims(1,"Admin","a"))
    if err!=nil {b.Fatal(err)}
    endpoint:=luvia.AuthMiddleware(cfg,router)
    body,want:=[]byte("{\"$api\":\"cached\",\"id\":42}"),[]byte("{\"data\":43}")
    if binary {body=append(codec.AppendSvarint([]byte{1,0,1},42),0);want=codec.AppendSvarint(nil,43)}
    input:=bytes.NewReader(body)
    request:=httptest.NewRequest("POST","/luvia",input)
    request.Header.Set("Content-Type","application/json")
    request.Header.Set("Authorization","Bearer "+signed)
    if binary {request.Header.Set("X-Luxo-Mode","binary")}
    check:=func(){
        input.Reset(body)
        response:=httptest.NewRecorder()
        endpoint.ServeHTTP(response,request)
        if response.Code!=200 || !bytes.Equal(response.Body.Bytes(),want) {b.Fatalf("invalid cached response: status=%d body=%s",response.Code,response.Body.Bytes())}
    }
    check()
    b.ReportAllocs()
    for b.Loop(){check()}
}
`

// This measures real HTTP and Redis with the same counted native operation.
// The uncached route is a feature-cost control, not an old implementation.
const namedPolicyHTTPBenchmarks = `
type policyHTTPMeasurement struct {
 endpoint *policyHTTPServer
 router *api.Router
 origin *policyResolver
 token,phase string
 binary,http2 bool
 requests int64
}

func newPolicyHTTPMeasurement(t testing.TB,binary,http2 bool,phase string)*policyHTTPMeasurement{
 t.Helper()
 if phase!="uncached"&&phase!="hit"&&phase!="miss"{t.Fatal("unknown benchmark phase")}
 router,origin:=redisPolicyRouter(t,"http-performance",time.Minute)
 cfg:=&auth.Config{Secret:"http-cache-benchmark-test-only",Expires:time.Hour}
 return &policyHTTPMeasurement{
  endpoint:newPolicyHTTPServer(t,router,cfg,http2),router:router,origin:origin,
  token:policyHTTPToken(t,cfg,9007199254740993),phase:phase,binary:binary,http2:http2,
 }
}

func (sample *policyHTTPMeasurement) request(t testing.TB){
 t.Helper()
 sample.requests++
 id:=int64(1000000000000)
 if sample.phase!="hit"{id+=sample.requests}
 name:="cachedNative"
 if sample.phase=="uncached"{name="uncachedNative"}
 body,want,contentType:=policyRequestPayload(t,sample.router,name,id,sample.binary)
 status,data,protocol:=sendPolicyHTTP(t,sample.endpoint,sample.token,body,contentType)
 if !sample.binary{data=bytes.TrimSpace(data)}
 expectedProtocol:=1;if sample.http2{expectedProtocol=2}
 if status!=200||protocol!=expectedProtocol||!bytes.Equal(data,want){
  t.Fatalf("invalid measured response: status=%d protocol=%d data=%x want=%x",status,protocol,data,want)
 }
}

func (sample *policyHTTPMeasurement) verify(t testing.TB){
 t.Helper()
 want:=sample.requests
 if sample.phase=="hit"{want=1}
 if calls:=sample.origin.calls.Load();calls!=want{
  t.Fatalf("%s benchmark measured the wrong path: origin=%d want=%d",sample.phase,calls,want)
 }
 if connections:=sample.endpoint.connections.Load();connections!=1{
  t.Fatalf("benchmark did not reuse one HTTP connection: %d",connections)
 }
}

func TestGeneratedTTLHTTPMeasurementPaths(t *testing.T){
 for _,http2:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
  for _,phase:=range []string{"uncached","hit","miss"}{
   t.Run(fmt.Sprintf("http2=%t/binary=%t/%s",http2,binary,phase),func(t *testing.T){
    sample:=newPolicyHTTPMeasurement(t,binary,http2,phase)
    for range 3{sample.request(t)}
    sample.verify(t)
   })
  }
 }}
}

func BenchmarkGeneratedTTLHTTP(b *testing.B){
 phases:=[]string{"uncached","hit","miss"}
 if os.Getenv("LUXO_NAMED_CACHE_REVERSE")=="1"{phases=[]string{"miss","hit","uncached"}}
 for _,http2:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
  for _,phase:=range phases{
   b.Run(fmt.Sprintf("http2=%t/binary=%t/%s",http2,binary,phase),func(b *testing.B){
    sample:=newPolicyHTTPMeasurement(b,binary,http2,phase)
    sample.request(b)
    b.ReportAllocs()
    for b.Loop(){sample.request(b)}
    sample.verify(b)
    b.ReportMetric(float64(sample.origin.calls.Load()-1)/float64(b.N),"origin/op")
   })
  }
 }}
 benchmarkPolicyRedisControls(b)
}

// Read a real generated entry outside the timer so controls use its exact
// payload and key length, not an invented approximation of the wire value.
func policyRedisControlEntry(t testing.TB,binary bool)(*rediscache.RedisCache,string,[]byte){
 t.Helper()
 sample:=newPolicyHTTPMeasurement(t,binary,false,"hit")
 sample.request(t);sample.verify(t)
 probe:=redis.NewClient(&redis.Options{Addr:os.Getenv("LUXO_TEST_POLICY_REDIS")})
 t.Cleanup(func(){if err:=probe.Close();err!=nil{t.Error(err)}})
 keys,err:=probe.Keys(t.Context(),t.Name()+":*").Result()
 if err!=nil||len(keys)!=1{t.Fatalf("control requires one generated entry: keys=%v err=%v",keys,err)}
 backend,err:=rediscache.NewRedis(os.Getenv("LUXO_TEST_POLICY_REDIS"))
 if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){if err:=backend.Close();err!=nil{t.Error(err)}})
 value,err:=backend.Get(t.Context(),keys[0])
 if err!=nil||len(value)==0{t.Fatalf("invalid generated cache value: %x %v",value,err)}
 return backend,keys[0],value
}

func policyRedisControl(t testing.TB,backend *rediscache.RedisCache,key string,value []byte,hit bool,index uint64){
 t.Helper()
 if !hit{
  if len(key)<17{t.Fatal("generated key too short")}
  key="!"+key[1:len(key)-16]+fmt.Sprintf("%016x",index)
 }
 actual,err:=backend.Get(t.Context(),key)
 if err!=nil{t.Fatal(err)}
 if hit{
  if !bytes.Equal(actual,value){t.Fatalf("control cache hit changed: %x want %x",actual,value)}
  return
 }
 if actual!=nil{t.Fatal("control miss unexpectedly hit")}
 if err:=backend.Set(t.Context(),key,value,time.Minute);err!=nil{t.Fatal(err)}
}

func TestGeneratedTTLRedisControlPaths(t *testing.T){
 for _,binary:=range []bool{false,true}{for _,hit:=range []bool{true,false}{
  t.Run(fmt.Sprintf("binary=%t/hit=%t",binary,hit),func(t *testing.T){
   backend,key,value:=policyRedisControlEntry(t,binary)
   for index:=uint64(0);index<3;index++{policyRedisControl(t,backend,key,value,hit,index)}
  })
 }}
}

func benchmarkPolicyRedisControls(b *testing.B){
 hits:=[]bool{true,false}
 if os.Getenv("LUXO_NAMED_CACHE_REVERSE")=="1"{hits=[]bool{false,true}}
 for _,binary:=range []bool{false,true}{for _,hit:=range hits{
  b.Run(fmt.Sprintf("redis-only/binary=%t/hit=%t",binary,hit),func(b *testing.B){
   backend,key,value:=policyRedisControlEntry(b,binary)
   policyRedisControl(b,backend,key,value,hit,0)
   index:=uint64(1)
   b.ReportAllocs()
   for b.Loop(){policyRedisControl(b,backend,key,value,hit,index);index++}
   b.ReportMetric(float64(len(key)),"key-B")
   b.ReportMetric(float64(len(value)),"value-B")
  })
 }}
}
`
