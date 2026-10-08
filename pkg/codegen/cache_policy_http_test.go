package codegen

// These cases exercise real HTTP and Redis with generated handlers. The native
// origin is counted, not a database; this is not a full database latency claim.
const namedPolicyHTTPAssertions = `
type policyHTTPServer struct {
 server *httptest.Server
 client *http.Client
 connections atomic.Int64
}

func newPolicyHTTPServer(t testing.TB, router *api.Router, cfg *auth.Config, http2 bool) *policyHTTPServer {
 t.Helper()
 endpoint:=&policyHTTPServer{}
 endpoint.server=httptest.NewUnstartedServer(luvia.AuthMiddleware(cfg,router))
 endpoint.server.Config.ConnState=func(_ net.Conn,state http.ConnState){if state==http.StateNew{endpoint.connections.Add(1)}}
 protocols:=new(http.Protocols)
 if http2 {protocols.SetUnencryptedHTTP2(true)} else {protocols.SetHTTP1(true)}
 endpoint.server.Config.Protocols=protocols
 endpoint.server.Start()
 t.Cleanup(endpoint.server.Close)
 transport:=&http.Transport{Protocols:protocols,MaxConnsPerHost:2}
 t.Cleanup(transport.CloseIdleConnections)
 endpoint.client=&http.Client{Transport:transport,Timeout:5*time.Second}
 return endpoint
}

func policyHTTPToken(t testing.TB,cfg *auth.Config,id int64) string {
 t.Helper()
 token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":%d,\"role\":\"Admin\"}",id),nil})
 if err!=nil {t.Fatal(err)}
 return token
}

func callPolicyHTTP(t *testing.T,endpoint *policyHTTPServer,router *api.Router,token string,id int64,binary,http2 bool) {
 t.Helper()
 body,want,contentType:=policyRequestPayload(t,router,"cachedNative",id,binary)
 status,actual,protocol:=sendPolicyHTTP(t,endpoint,token,body,contentType)
 if !binary {actual=bytes.TrimSpace(actual)}
 expectedProtocol:=1
 if http2 {expectedProtocol=2}
 if status!=200 || !bytes.Equal(actual,want) || protocol!=expectedProtocol {
  t.Fatalf("HTTP%d binary=%t id=%d: protocol=%d status=%d body=%q want=%q",expectedProtocol,binary,id,protocol,status,actual,want)
 }
}

func sendPolicyHTTP(t testing.TB,endpoint *policyHTTPServer,token string,body []byte,contentType string)(int,[]byte,int) {
 t.Helper()
 request,err:=http.NewRequestWithContext(t.Context(),http.MethodPost,endpoint.server.URL,bytes.NewReader(body))
 if err!=nil {t.Fatal(err)}
 request.Header.Set("Content-Type",contentType)
 if contentType=="application/x-luxo" {request.Header.Set("X-Luxo-Mode","binary")}
 if token!="" {request.Header.Set("Authorization","Bearer "+token)}
 response,err:=endpoint.client.Do(request)
 if err!=nil {t.Fatal(err)}
 actual,readErr:=io.ReadAll(response.Body)
 closeErr:=response.Body.Close()
 if readErr!=nil || closeErr!=nil {t.Fatalf("read=%v close=%v",readErr,closeErr)}
 return response.StatusCode,actual,response.ProtoMajor
}

func TestGeneratedRedisPolicyOverHTTP(t *testing.T) {
 for _,http2:=range []bool{false,true} {
  for _,binary:=range []bool{false,true} {
   t.Run(fmt.Sprintf("http2=%t/binary=%t",http2,binary),func(t *testing.T){verifyPolicyHTTP(t,http2,binary)})
  }
 }
}

func verifyPolicyHTTP(t *testing.T,http2,binary bool) {
 t.Helper()
 first,origin:=redisPolicyRouter(t,"network",time.Minute)
 second,other:=redisPolicyRouter(t,"network",time.Minute)
 cfg:=&auth.Config{Secret:"generated-policy-network-test-only",Expires:time.Hour}
 a:=newPolicyHTTPServer(t,first,cfg,http2)
 b:=newPolicyHTTPServer(t,second,cfg,http2)
 user:=policyHTTPToken(t,cfg,1)
 another:=policyHTTPToken(t,cfg,2)
 for _,id:=range []int64{42,84,9223372036854775807,-9223372036854775808} {
  before:=origin.calls.Load()
  callPolicyHTTP(t,a,first,user,id,binary,http2)
  callPolicyHTTP(t,b,second,user,id,binary,http2)
  if origin.calls.Load()!=before+1 || other.calls.Load()!=0 {t.Fatal("same verified identity did not share across HTTP endpoints")}
 }
 for _,token:=range []string{another,""} {
  before:=origin.calls.Load()
  callPolicyHTTP(t,a,first,token,42,binary,http2)
  callPolicyHTTP(t,b,second,token,42,binary,http2)
  if origin.calls.Load()!=before+1 || other.calls.Load()!=0 {t.Fatal("distinct identity reused another identity's result")}
 }
 before:=origin.calls.Load()
 status,_,_:=sendPolicyHTTP(t,a,user,[]byte("{\"$api\":\"cachedNative\",\"id\":null}"),"application/json")
 if status!=400 || origin.calls.Load()!=before {t.Fatal("invalid HTTP input reached origin or returned a cached result")}
 if a.connections.Load()!=1 || b.connections.Load()!=1 {t.Fatalf("connections not reused: %d/%d",a.connections.Load(),b.connections.Load())}
}

func TestGeneratedRedisPolicyHTTPOriginBudget(t *testing.T) {
 for _,http2:=range []bool{false,true} {
  t.Run(fmt.Sprintf("http2=%t",http2),func(t *testing.T){verifyPolicyOriginBudget(t,http2)})
 }
}

func verifyPolicyOriginBudget(t *testing.T,http2 bool) {
 t.Helper()
 router,resolver:=redisPolicyRouter(t,"bounded-origin",time.Minute)
 release,entered:=make(chan struct{}),make(chan struct{})
 var once sync.Once
 resolver.beforeReturn=func(ctx context.Context,id int64)error{
  if id!=84{return nil}
  once.Do(func(){close(entered)})
  select{case <-release:return nil;case <-ctx.Done():return ctx.Err()}
 }
 cfg:=&auth.Config{Secret:"generated-origin-budget-test-only",Expires:time.Hour}
 endpoint:=newPolicyHTTPServer(t,router,cfg,http2)
 released:=false
 t.Cleanup(func(){if !released{close(release)}})
 token:=policyHTTPToken(t,cfg,1)
 callPolicyHTTP(t,endpoint,router,token,42,false,http2)
 result:=make(chan error,1)
 go func(){result<-blockedPolicyRequest(t.Context(),endpoint,token)}()
 select{case <-entered:case <-time.After(5*time.Second):t.Fatal("origin request never entered")}
 // A cache hit must remain available while the only origin slot is occupied.
 callPolicyHTTP(t,endpoint,router,token,42,false,http2)
 status,_,_:=sendPolicyHTTP(t,endpoint,token,[]byte("{\"$api\":\"cachedNative\",\"id\":99}"),"application/json")
 if status!=503 || resolver.calls.Load()!=2 {t.Fatalf("origin limit: status=%d calls=%d",status,resolver.calls.Load())}
 close(release);released=true
 select{case err:=<-result:if err!=nil{t.Fatal(err)};case <-time.After(5*time.Second):t.Fatal("origin request did not finish")}
 // The overloaded response must not poison the cache once capacity is free.
 callPolicyHTTP(t,endpoint,router,token,99,false,http2)
 callPolicyHTTP(t,endpoint,router,token,99,false,http2)
 if resolver.calls.Load()!=3 {t.Fatalf("overload response cached or successful fill missed: %d",resolver.calls.Load())}
}

func blockedPolicyRequest(ctx context.Context,endpoint *policyHTTPServer,token string)error {
 request,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint.server.URL,bytes.NewBufferString("{\"$api\":\"cachedNative\",\"id\":84}"))
 if err!=nil{return err}
 request.Header.Set("Content-Type","application/json")
 request.Header.Set("Authorization","Bearer "+token)
 response,err:=endpoint.client.Do(request)
 if err!=nil{return err}
 raw,readErr:=io.ReadAll(response.Body)
 closeErr:=response.Body.Close()
 if readErr!=nil{return readErr}
 if closeErr!=nil{return closeErr}
 if response.StatusCode!=200 || !bytes.Equal(bytes.TrimSpace(raw),[]byte("{\"data\":84}")){return fmt.Errorf("blocked origin: %d %q",response.StatusCode,raw)}
 return nil
}
`
