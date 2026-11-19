package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGenerateModelDepthFixture(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-model-depth-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("unowned scratch")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	for _, invalid := range []string{"", root, filepath.Join(out, "missing")} {
		if err := generateModelDepthFixture(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for index := range 5 {
		modes := make([]bool, 5)
		modes[index] = true
		if err := generateModelDepthMode(out, modes...); err == nil {
			t.Fatalf("accepted incompatible mode %d", index)
		}
	}
	if err := generateModelDepthMode(out, false, false, false, false, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model.gen.go", "handler.gen.go", "schema.gen.go", "native.gen.go", "main.go"} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("missing %s: %v", name, err)
		}
		if name == "main.go" && !bytes.Equal(data, fixtureModelDepthServer) {
			t.Fatal("wrong server template")
		}
	}
	if err := generateModelDepthFixture(out); err == nil {
		t.Fatal("overwrote existing fixture")
	}
	checkFixtureRegistrationErrors(t, "model_depth_server.go", fixtureModelDepthServer)
	if err := os.WriteFile(filepath.Join(out, "depth_test.go"), []byte(modelDepthAssertions), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated fixture: %v\n%s", err, output)
	}
}

const modelDepthAssertions = `package main
import("bytes";"context";"encoding/json";"errors";"fmt";"io";"net/http";"net/http/httptest";"strings";"sync";"testing";"nhooyr.io/websocket";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/api")
func checkDepthPayload(t *testing.T,value int64,payload *json.RawMessage,leaf bool) {
 t.Helper();expected:=1;if leaf{expected=257}
 if value!=1||payload==nil||len(*payload)!=expected{t.Fatal("wrong node/leaf JSON128")}
}
func checkDepthNode(t *testing.T,node DepthNode,depth int64,lists bool) {
 t.Helper();checkDepthPayload(t,node.Value,node.Payload,depth==1)
 if depth==1{if node.Child!=nil||len(node.Children)!=0{t.Fatal("extra node")};return}
 if lists{if node.Child!=nil||len(node.Children)!=1{t.Fatal("wrong node list")};checkDepthBranch(t,node.Children[0],depth-1,lists)
 }else{if node.Child==nil||len(node.Children)!=0{t.Fatal("wrong node child")};checkDepthBranch(t,*node.Child,depth-1,lists)}
}
func checkDepthBranch(t *testing.T,node DepthBranch,depth int64,lists bool) {
 t.Helper();checkDepthPayload(t,node.Value,node.Payload,depth==1)
 if depth==1{if node.Child!=nil||len(node.Children)!=0{t.Fatal("extra branch")};return}
 if lists{if node.Child!=nil||len(node.Children)!=1{t.Fatal("wrong branch list")};checkDepthNode(t,node.Children[0],depth-1,lists)
 }else{if node.Child==nil||len(node.Children)!=0{t.Fatal("wrong branch child")};checkDepthNode(t,*node.Child,depth-1,lists)}
}
func TestDepthCorpus(t *testing.T) {
 for _,depth:=range []int64{1,63,64,65} { for _,lists:=range []bool{false,true} {
  node,err:=validatedDepthNode(depth,lists,128);if err!=nil{t.Fatal(err)}
  checkDepthNode(t,node,depth,lists)
 } }
}
func TestInvalidDepthCorpus(t *testing.T) {
 r:=depthResolver{}
 for _,pair:=range [][2]int64{{0,0},{66,0},{1,-1},{1,130}} {
  if _,err:=r.DepthOne(context.Background(),pair[0],false,pair[1]);err==nil{t.Fatal(pair)}
  if _,err:=r.DepthMany(context.Background(),pair[0],false,pair[1]);err==nil{t.Fatal(pair)}
 }
}
func TestDepthCaptureConcurrent(t *testing.T) {
 var c depthCapture;var group sync.WaitGroup
 handler:=c.handler("echo",func(context.Context,*api.Request)error{return nil})
 for range 8{group.Go(func(){for index:=range 100{c.Record("echo",0,index==0);_ = handler(context.Background(),nil);_ = c.snapshot()}})}
 group.Wait();snapshot:=c.snapshot()
 if snapshot["calls"].(map[string]int64)["echo"]!=800||snapshot["invoked"].(map[string]int64)["echo"]!=800||snapshot["failed"]!=int64(8){t.Fatal(snapshot)}
 snapshot["calls"].(map[string]int64)["echo"]=0;snapshot["invoked"].(map[string]int64)["echo"]=0
 if c.snapshot()["calls"].(map[string]int64)["echo"]!=800||c.snapshot()["invoked"].(map[string]int64)["echo"]!=800{t.Fatal("snapshot aliases counters")}
}
func TestMalformedDepthProducer(t *testing.T){
 for _,depth:=range []int{64,65}{for _,lists:=range []bool{false,true}{
  raw:=malformedDepthWire(depth,lists);var value DepthNode;dec:=codec.NewDecoder(raw);value.ReadLuxo(dec);err:=dec.Finish()
  if depth==65{if !errors.Is(err,codec.ErrModelDepth){t.Fatal("hostile wire not rejected",err)}}else{if err!=nil{t.Fatal(err)};var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil||!bytes.Equal(buf.B,raw){t.Fatal("wire control differs from formal encoding",err)}}
  native:=depthNode(int64(depth),lists,0);expected,err:=json.Marshal(native);if err!=nil{t.Fatal(err)};if !bytes.Equal(expected,malformedDepthJSON(depth,lists)){t.Fatal("JSON hostile corpus differs")}
 }}
}
func TestDepthServerModes(t *testing.T){
 for _,malformed:=range []bool{false,true}{capture:=&depthCapture{};router,err:=depthRouter(capture,malformed);if err!=nil{t.Fatal(err)}
  for _,depth:=range []int{64,65}{generic:=0;if depth==64{generic=128}
   body:=fmt.Sprintf("{\"$api\":\"depthOne\",\"$select\":\"value,child,children,payload\",\"depth\":%d,\"lists\":true,\"jsonDepth\":%d}",depth,generic)
   req:=httptest.NewRequest("POST","/luvia",strings.NewReader(body));req.Header.Set("Content-Type","application/json");req.Header.Set("X-Luxo-Mode","json");response:=httptest.NewRecorder();router.ServeHTTP(response,req)
   accepted:=response.Code==200;if accepted!=(depth==64){t.Fatalf("depth=%d malformed=%t status=%d body=%s",depth,malformed,response.Code,response.Body)}
  }
  if capture.malformed.Load()!=0{t.Fatal("JSON model escaped the server guard")}
  expected:=int64(0);if malformed{expected=1};if capture.guardedJSON.Load()!=expected{t.Fatal("missing generated JSON rejection witness")}
 }
}
func TestHostileDepthJSONDelivered(t *testing.T){
 for _,enabled:=range []bool{false,true}{capture:=&depthCapture{};router,err:=depthRouter(capture,enabled);if err!=nil{t.Fatal(err)}
  server,engine,err:=startDepthServer(t.Context(),depthHandler(router,capture));if err!=nil{t.Fatal(err)}
  for _,major:=range []int{1,2}{transport:=server.Client().Transport.(*http.Transport).Clone();transport.ForceAttemptHTTP2=major==2
   if major==1{transport.TLSClientConfig.NextProtos=[]string{"http/1.1"}}
   client:=&http.Client{Transport:transport}
   for _,depth:=range []int{65,1}{for _,lists:=range []bool{false,true}{for _,many:=range []bool{false,true}{name:="depthOne";if many{name="depthMany"}
    body:=fmt.Sprintf("{\"$api\":%q,\"depth\":%d,\"lists\":%t,\"jsonDepth\":0}",name,depth,lists)
    req,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL+"/hostile",strings.NewReader(body));if err!=nil{t.Fatal(err)}
    req.Header.Set("Content-Type","application/json");response,err:=client.Do(req);if err!=nil{t.Fatal(err)}
    raw,err:=io.ReadAll(response.Body);response.Body.Close();if err!=nil{t.Fatal(err)}
    if response.ProtoMajor!=major{t.Fatal("protocol fallback",response.Proto)}
    if !enabled{if response.StatusCode!=404{t.Fatal("hostile peer enabled by default",response.StatusCode)};continue}
    if response.StatusCode!=200{t.Fatal("hostile bytes were blocked before reaching the client",response.StatusCode,string(raw))}
    var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(raw,&envelope);err!=nil{t.Fatal(err)}
    checkDeliveredDepthJSON(t,envelope.Data,depth,lists,many)
   }}}
   client.CloseIdleConnections()
  }
  if enabled{checkHostileDepthWS(t,server)}
  engine.Close();server.Close()
 }
}
func checkDeliveredDepthJSON(t *testing.T,raw []byte,depth int,lists,many bool){
 t.Helper();native:=depthNode(int64(depth),lists,0);expected,err:=json.Marshal(native);if err!=nil{t.Fatal(err)}
 if many{expected,err=json.Marshal([]DepthNode{native,native});if err!=nil{t.Fatal(err)}}
 if !bytes.Equal(raw,expected){t.Fatal("actual peer response differs from the native-model reference")}
}
func checkHostileDepthWS(t *testing.T,server *httptest.Server){
 t.Helper();conn,_,err:=websocket.Dial(t.Context(),"wss"+strings.TrimPrefix(server.URL,"https")+"/hostile/ws",&websocket.DialOptions{HTTPClient:server.Client()});if err!=nil{t.Fatal(err)};defer conn.CloseNow()
 for _,depth:=range []int{65,1}{for _,lists:=range []bool{false,true}{for _,many:=range []bool{false,true}{name:="depthOne";if many{name="depthMany"}
  body:=fmt.Sprintf("{\"$id\":18446744073709551615,\"$api\":%q,\"depth\":%d,\"lists\":%t,\"jsonDepth\":0}",name,depth,lists)
  if err:=conn.Write(t.Context(),websocket.MessageText,[]byte(body));err!=nil{t.Fatal(err)};kind,raw,err:=conn.Read(t.Context());if err!=nil{t.Fatal(err)}
  var envelope struct{ID uint64 ` + "`json:\"$id\"`" + `;Data json.RawMessage};if err:=json.Unmarshal(raw,&envelope);err!=nil{t.Fatal(err)}
  if kind!=websocket.MessageText||envelope.ID!=^uint64(0){t.Fatal("hostile peer changed correlation ID")}
  checkDeliveredDepthJSON(t,envelope.Data,depth,lists,many)
 }}}
}
func TestDiscardedDepthTLSInputs(t *testing.T){
 capture:=&depthCapture{};router,err:=depthRouter(capture,false);if err!=nil{t.Fatal(err)}
 server,engine,err:=startDepthServer(t.Context(),depthHandler(router,capture));if err!=nil{t.Fatal(err)};defer engine.Close();defer server.Close()
 for _,major:=range []int{1,2}{t.Run(fmt.Sprintf("h%d",major),func(t *testing.T){
  transport:=server.Client().Transport.(*http.Transport).Clone();transport.ForceAttemptHTTP2=major==2
  if major==1{transport.TLSClientConfig.NextProtos=[]string{"http/1.1"}}
  client:=&http.Client{Transport:transport};defer client.CloseIdleConnections()
  for _,depth:=range []int{64,65,64}{for _,lists:=range []bool{false,true}{
   child,err:=json.Marshal(depthNode(int64(depth-1),lists,128));if err!=nil{t.Fatal(err)}
   value:="{\"value\":1,\"child\":"+string(child)+",\"child\":null}"
   if lists{value="{\"value\":1,\"children\":["+string(child)+"],\"children\":[]}"}
   checkDiscardedTLSInput(t,client,server.URL,major,value,depth==64)
  }}
  for _,depth:=range []int{128,129,128}{raw:=strings.Repeat("[",depth)+"0"+strings.Repeat("]",depth)
   checkDiscardedTLSInput(t,client,server.URL,major,"{\"value\":1,\"payload\":"+raw+",\"payload\":0}",depth==128)
  }
  for _,value:=range []string{"{\"value\":\"bad\",\"value\":1}","{\"value\":1,\"child\":{},\"child\":null}"}{checkDiscardedTLSInput(t,client,server.URL,major,value,true)}
 })}
 t.Run("wss",func(t *testing.T){checkDiscardedWSSInputs(t,server)})
 if capture.h1.Load()!=22||capture.h2.Load()!=22||capture.ws.Load()!=1||capture.malformed.Load()!=0{t.Fatal("missing formal TLS transport coverage",capture.snapshot())}
}
func checkDiscardedTLSInput(t *testing.T,client *http.Client,url string,major int,value string,valid bool){
 t.Helper()
 for _,many:=range []bool{false,true}{name,param:="depthEcho","\"value\":"+value;if many{name="depthEchoMany";param="\"values\":["+value+"]"}
  body:="{\"$api\":\""+name+"\",\"$select\":\"value\","+param+"}"
  req,err:=http.NewRequestWithContext(t.Context(),"POST",url+"/luvia",strings.NewReader(body));if err!=nil{t.Fatal(err)}
  req.Header.Set("Content-Type","application/json");req.Header.Set("X-Luxo-Mode","json")
  response,err:=client.Do(req);if err!=nil{t.Fatal(err)};raw,err:=io.ReadAll(response.Body);response.Body.Close();if err!=nil{t.Fatal(err)}
  if response.ProtoMajor!=major||(response.StatusCode==200)!=valid{t.Fatalf("h%d %s valid=%t status=%d protocol=%s body=%s",major,name,valid,response.StatusCode,response.Proto,raw)}
  if valid&&!bytes.Contains(raw,[]byte("\"value\":1")){t.Fatal("last value changed",string(raw))}
 }
}
func checkDiscardedWSSInputs(t *testing.T,server *httptest.Server){
 t.Helper();transport:=server.Client().Transport.(*http.Transport).Clone();transport.ForceAttemptHTTP2=false;transport.TLSClientConfig.NextProtos=[]string{"http/1.1"}
 client:=&http.Client{Transport:transport};defer client.CloseIdleConnections()
 conn,_,err:=websocket.Dial(t.Context(),"wss"+strings.TrimPrefix(server.URL,"https")+"/luvia",&websocket.DialOptions{HTTPClient:client});if err!=nil{t.Fatal(err)};defer conn.CloseNow()
 node,err:=json.Marshal(depthNode(64,false,128));if err!=nil{t.Fatal(err)}
 var sequence int
 for _,sample:=range []struct{value string;valid bool}{
  {"{\"value\":1,\"child\":"+string(node)+",\"child\":null}",false},
  {"{\"value\":1,\"payload\":"+strings.Repeat("[",129)+"0"+strings.Repeat("]",129)+",\"payload\":0}",false},
  {"{\"value\":1,\"payload\":"+strings.Repeat("[",128)+"0"+strings.Repeat("]",128)+",\"payload\":0}",true},
  {"{\"value\":\"bad\",\"value\":1}",true},
  {"{\"value\":1,\"child\":{},\"child\":null}",true},
 }{for _,many:=range []bool{false,true}{sequence++;name,param:="depthEcho","\"value\":"+sample.value;if many{name="depthEchoMany";param="\"values\":["+sample.value+"]"}
  raw:=fmt.Sprintf("{\"$id\":%d,\"$api\":%q,\"$select\":\"value\",%s}",sequence,name,param)
  if err:=conn.Write(t.Context(),websocket.MessageText,[]byte(raw));err!=nil{t.Fatal(err)};kind,response,err:=conn.Read(t.Context());if err!=nil{t.Fatal(err)}
  var result struct{ID int ` + "`json:\"$id\"`" + `;Error string;Code int};if err:=json.Unmarshal(response,&result);err!=nil{t.Fatal(err)}
  if kind!=websocket.MessageText||result.ID!=sequence||(result.Error=="")!=sample.valid{t.Fatalf("invalid WSS result: %s",response)}
  if sample.valid&&!bytes.Contains(response,[]byte("\"value\":1")){t.Fatal("last WSS value changed",string(response))}
 }}
}

`
