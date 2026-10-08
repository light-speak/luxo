package codegen

// The owned TCP proxy drops only this fixture's Redis connections. The origin
// remains a counted native operation, not a simulated database latency claim.
const namedPolicyFailureAssertions = `
type policyRedisProxy struct {
 listener net.Listener
 target string
 mu sync.Mutex
 failed bool
 stalled <-chan struct{}
 connections map[net.Conn]struct{}
 workers sync.WaitGroup
 done chan struct{}
}

func newPolicyRedisProxy(t *testing.T) *policyRedisProxy {
 t.Helper()
 listener,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil{t.Fatal(err)}
 proxy:=&policyRedisProxy{listener:listener,target:os.Getenv("LUXO_TEST_POLICY_REDIS"),connections:make(map[net.Conn]struct{}),done:make(chan struct{})}
 go func(){
  defer close(proxy.done)
  for {
   client,err:=listener.Accept()
   if err!=nil{
    if !errors.Is(err,net.ErrClosed){t.Errorf("owned Redis proxy: %v",err)}
    return
   }
   proxy.workers.Go(func(){proxy.forward(client)})
  }
 }()
 t.Cleanup(func(){
  _=listener.Close()
  <-proxy.done
  proxy.setFailed(true)
  proxy.workers.Wait()
 })
 return proxy
}

func (proxy *policyRedisProxy) setFailed(failed bool){
 proxy.mu.Lock()
 defer proxy.mu.Unlock()
 proxy.failed=failed
 if failed{for conn:=range proxy.connections{_=conn.Close()}}
}

func (proxy *policyRedisProxy) forward(client net.Conn){
 defer client.Close()
 proxy.mu.Lock()
 failed:=proxy.failed
 proxy.mu.Unlock()
 if failed{return}
 upstream,err:=net.DialTimeout("tcp",proxy.target,time.Second)
 if err!=nil{return}
 defer upstream.Close()
 proxy.mu.Lock()
 if proxy.failed{proxy.mu.Unlock();return}
 proxy.connections[client]=struct{}{}
 proxy.connections[upstream]=struct{}{}
 stalled:=proxy.stalled
 proxy.mu.Unlock()
 defer func(){
  proxy.mu.Lock()
  delete(proxy.connections,client)
  delete(proxy.connections,upstream)
  proxy.mu.Unlock()
 }()
 if stalled!=nil{<-stalled}
 done:=make(chan struct{})
 go func(){
  defer close(done)
  _,_=io.Copy(client,upstream)
  _=client.Close()
  _=upstream.Close()
 }()
 _,_=io.Copy(upstream,client)
 _=client.Close()
 _=upstream.Close()
 <-done
}

func TestGeneratedRedisFailureBoundsConcurrentOrigin(t *testing.T){
 for _,http2:=range []bool{false,true}{
  for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("http2=%t/binary=%t",http2,binary),func(t *testing.T){
    verifyPolicyRedisFailure(t,http2,binary)
   })
  }
 }
}

type policyFailureCache struct {
 cache.Cache
 mu sync.Mutex
 getError,setError error
 writes int
 initialKey,lastGetKey string
 initialFillAt time.Time
 initialTTL time.Duration
 lastGetBytes int
}

func(backend *policyFailureCache)Get(ctx context.Context,key string)([]byte,error){
 value,err:=backend.Cache.Get(ctx,key)
 backend.mu.Lock()
 backend.getError=err
 backend.lastGetKey=key
 backend.lastGetBytes=len(value)
 backend.mu.Unlock()
 return value,err
}

func(backend *policyFailureCache)Set(ctx context.Context,key string,value []byte,ttl time.Duration)error{
 err:=backend.Cache.Set(ctx,key,value,ttl)
 backend.mu.Lock()
 backend.writes++
 backend.setError=err
 if backend.writes==1{
  backend.initialKey=key
  backend.initialFillAt=time.Now()
  backend.initialTTL=ttl
 }
 backend.mu.Unlock()
 return err
}

func(backend *policyFailureCache)assertInitialFill(t *testing.T){
 t.Helper()
 backend.mu.Lock()
 defer backend.mu.Unlock()
 if backend.writes!=1||backend.setError!=nil||backend.getError!=nil{
  t.Fatalf("Redis fixture was not healthy before fault: writes=%d get=%v set=%v",backend.writes,backend.getError,backend.setError)
 }
}

func(backend *policyFailureCache)diagnostics()string{
 backend.mu.Lock()
 defer backend.mu.Unlock()
 now:=time.Now()
 return fmt.Sprintf("writes=%d lastGet=%v lastGetBytes=%d lastSet=%v sameKey=%t elapsed=%v wallElapsed=%v ttl=%v",
  backend.writes,backend.getError,backend.lastGetBytes,backend.setError,backend.lastGetKey==backend.initialKey,
  now.Sub(backend.initialFillAt),time.Duration(now.UnixNano()-backend.initialFillAt.UnixNano()),backend.initialTTL)
}

func failurePolicyRouter(t *testing.T,proxy *policyRedisProxy)(*api.Router,*policyResolver,*policyFailureCache){
 t.Helper()
 backend,err:=rediscache.NewRedis(proxy.listener.Addr().String(),rediscache.WithPrefix(t.Name()),rediscache.WithTimeout(100*time.Millisecond))
 if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){if err:=backend.Close();err!=nil{t.Error(err)}})
 if err:=backend.Ping(t.Context());err!=nil{t.Fatal(err)}
 observed:=&policyFailureCache{Cache:backend}
 router,resolver:=policyRouterWithBackend(t,observed,"redis-failure",time.Minute,api.ResponseOriginOptions{MaxConcurrent:2})
 return router,resolver,observed
}

func holdPolicyOrigins(t *testing.T,resolver *policyResolver)(<-chan struct{},func()){
 t.Helper()
 entered,release:=make(chan struct{},2),make(chan struct{})
 var once sync.Once
 finish:=func(){once.Do(func(){close(release)})}
 t.Cleanup(finish)
 resolver.beforeReturn=func(ctx context.Context,id int64)error{
  if id!=84&&id!=85{return nil}
  select{case entered<-struct{}{}:case <-ctx.Done():return ctx.Err()}
  select{case <-release:return nil;case <-ctx.Done():return ctx.Err()}
 }
 return entered,finish
}

func verifyPolicyRedisFailure(t *testing.T,http2,binary bool){
 t.Helper()
 proxy:=newPolicyRedisProxy(t)
 router,resolver,backend:=failurePolicyRouter(t,proxy)
 entered,release:=holdPolicyOrigins(t,resolver)
 defer release()
 cfg:=&auth.Config{Secret:"redis-failure-test-only",Expires:time.Hour}
 endpoint:=newPolicyHTTPServer(t,router,cfg,http2)
 endpoint.client.Transport.(*http.Transport).MaxConnsPerHost=32
 token:=policyHTTPToken(t,cfg,9007199254740993)
 callPolicyHTTP(t,endpoint,router,token,42,binary,http2)
 backend.assertInitialFill(t)
 proxy.setFailed(true)
 var held sync.WaitGroup
 for _,id:=range []int64{84,85}{held.Go(func(){callPolicyHTTP(t,endpoint,router,token,id,binary,http2)})}
 defer held.Wait()
 defer release()
 for range 2{
  select{case <-entered:case <-time.After(5*time.Second):t.Fatal("failed Redis did not reach bounded origin")}
 }
 assertPolicyFailureBurst(t,endpoint,router,token,http2,binary)
 if got:=resolver.calls.Load();got!=3{t.Fatalf("failed Redis exceeded origin budget: %d",got)}
 release()
 held.Wait()
 proxy.setFailed(false)
 assertPolicyRecovered(t,endpoint,router,resolver,backend,cfg,token,http2,binary)
}

func assertPolicyFailureBurst(t *testing.T,endpoint *policyHTTPServer,router *api.Router,token string,http2,binary bool){
 t.Helper()
 const requests=16
 var workers sync.WaitGroup
 var completed atomic.Int32
 start:=make(chan struct{})
 for index:=range requests{
  workers.Go(func(){
   <-start
   body,_,contentType:=policyRequestPayload(t,router,"cachedNative",int64(100+index),binary)
   status,actual,protocol:=sendPolicyHTTP(t,endpoint,token,body,contentType)
   expectedProtocol:=1;if http2{expectedProtocol=2}
   if status!=503||protocol!=expectedProtocol||!bytes.Contains(actual,[]byte("CacheOriginBusy")){
    t.Errorf("Redis failure overload: protocol=%d status=%d body=%q",protocol,status,actual)
   }
   completed.Add(1)
  })
 }
 close(start)
 workers.Wait()
 if completed.Load()!=requests{t.Fatal("failed Redis left HTTP requests unresolved")}
}

func assertPolicyRecovered(t *testing.T,endpoint *policyHTTPServer,router *api.Router,resolver *policyResolver,backend *policyFailureCache,cfg *auth.Config,token string,http2,binary bool){
 t.Helper()
 // Redis failure must not clear a valid prior entry or replace the backend.
 callPolicyHTTP(t,endpoint,router,token,42,binary,http2)
 if got:=resolver.calls.Load();got!=3{t.Fatalf("recovered Redis did not hit its original entry: calls=%d %s",got,backend.diagnostics())}
 // The failed SET during the outage cannot masquerade as a successful fill.
 for range 2{callPolicyHTTP(t,endpoint,router,token,84,binary,http2)}
 if got:=resolver.calls.Load();got!=4{t.Fatalf("recovery did not refill once: %d",got)}
 // A cached identity cannot serve a different verified token or anonymous user.
 other:=policyHTTPToken(t,cfg,9007199254740994)
 for _,identity:=range []string{other,""}{
  for range 2{callPolicyHTTP(t,endpoint,router,identity,42,binary,http2)}
 }
 if got:=resolver.calls.Load();got!=6{t.Fatalf("recovery crossed identity boundaries: %d",got)}
 // Errors returned during overload must never become cached successes.
 for range 2{callPolicyHTTP(t,endpoint,router,token,100,binary,http2)}
 if got:=resolver.calls.Load();got!=7{t.Fatalf("overload poisoned recovered cache: %d",got)}
}

// A reachable TCP endpoint is not proof that a Redis command has completed.
// Keep the original entry intact while explicitly withholding all replies.
func stallPolicyRedis(proxy *policyRedisProxy)func(){
 ready:=make(chan struct{})
 proxy.mu.Lock()
 proxy.stalled=ready
 for conn:=range proxy.connections{_=conn.Close()}
 proxy.mu.Unlock()
 var once sync.Once
 return func(){once.Do(func(){
  proxy.mu.Lock()
  proxy.stalled=nil
  close(ready)
  proxy.mu.Unlock()
 })}
}

func storedPolicyValue(t *testing.T,backend *policyFailureCache)[]byte{
 t.Helper()
 backend.mu.Lock()
 key:=t.Name()+":"+backend.initialKey
 backend.mu.Unlock()
 probe:=redis.NewClient(&redis.Options{Addr:os.Getenv("LUXO_TEST_POLICY_REDIS")})
 defer probe.Close()
 value,err:=probe.Get(t.Context(),key).Bytes()
 if err!=nil{t.Fatalf("direct Redis read: %v",err)}
 ttl,err:=probe.PTTL(t.Context(),key).Result()
 if err!=nil||ttl<=0||ttl>time.Minute{t.Fatalf("direct Redis ttl=%v error=%v",ttl,err)}
 return value
}

func TestGeneratedRedisReadTimeoutDoesNotMeanEntryLoss(t *testing.T){
 for _,http2:=range []bool{false,true}{
  for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("http2=%t/binary=%t",http2,binary),func(t *testing.T){
    verifyPolicyRedisReadTimeout(t,http2,binary)
   })
  }
 }
}

func verifyPolicyRedisReadTimeout(t *testing.T,http2,binary bool){
 t.Helper()
 proxy:=newPolicyRedisProxy(t)
 router,resolver,backend:=failurePolicyRouter(t,proxy)
 cfg:=&auth.Config{Secret:"redis-read-timeout-test-only",Expires:time.Hour}
 endpoint:=newPolicyHTTPServer(t,router,cfg,http2)
 token:=policyHTTPToken(t,cfg,9007199254740993)
 callPolicyHTTP(t,endpoint,router,token,42,binary,http2)
 backend.assertInitialFill(t)
 original:=storedPolicyValue(t,backend)
 resume:=stallPolicyRedis(proxy)
 defer resume()
 callPolicyHTTP(t,endpoint,router,token,42,binary,http2)
 backend.mu.Lock()
 getErr,setErr:=backend.getError,backend.setError
 sameKey:=backend.lastGetKey==backend.initialKey
 backend.mu.Unlock()
 var timeout net.Error
 if !errors.As(getErr,&timeout)||!timeout.Timeout()||setErr==nil||!sameKey{
  t.Fatalf("stalled Redis was not a timed-out read/refill: %s",backend.diagnostics())
 }
 if got:=resolver.calls.Load();got!=2{t.Fatalf("timed-out cache did not execute origin once: %d",got)}
 if !bytes.Equal(storedPolicyValue(t,backend),original){t.Fatal("timed-out read/refill changed the stored entry")}
 t.Logf("entry preserved despite origin fallback: %s",backend.diagnostics())
 resume()
 callPolicyHTTP(t,endpoint,router,token,42,binary,http2)
 if got:=resolver.calls.Load();got!=2{t.Fatalf("resumed Redis did not hit preserved entry: %d %s",got,backend.diagnostics())}
}
`
