package codegen

import "testing"

func TestGeneratedSubstringRangeErrors(t *testing.T) {
	runGeneratedFixture(t, `
fn sliceValue(value: String, start: Int, end: Int): String { return value.substring(start, end) }
fn tailValue(value: String, start: Int): String { return value.substring(start) }
fn safeSlice(value: String?, start: Int, end: Int): String? { return value?.substring(start, end) }
fn mappedSlices(values: [String], start: Int, end: Int): [String] { return values.map { it.substring(start, end) } }
fn guardedSlice(flag: Boolean, value: String, start: Int, end: Int): Boolean { return flag && value.substring(start, end).isEmpty }
fn defaultSlice(value: String?, start: Int, end: Int): String { return value?.substring(start, end) ?: "fallback" }
fn doubleSlice(value: String): String { return value.substring(0, 2).substring(0, 1) }
fn collidingSlice(_stringSubstring1: String, _stringSubstring1Err: Int, end: Int): String { return _stringSubstring1.substring(_stringSubstring1Err, end) }
api sliceCall(value: String, start: Int, end: Int): String { return sliceValue(value, start, end) }
api tailCall(value: String, start: Int): String { return tailValue(value, start) }
`, substringBoundsAssertions, StableIDs{
		APIs: map[string]int{"sliceCall": 51, "tailCall": 52},
		APIParams: map[string]map[string]int{
			"sliceCall": {"value": 1, "start": 2, "end": 3}, "tailCall": {"value": 1, "start": 2},
		},
	})
}

const substringBoundsAssertions = `package generated
import("bytes";"encoding/json";"math";"net/http/httptest";"strconv";"testing";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestSubstringNativeBounds(t *testing.T){
 app:=&App{};value:="abcd"
 for _,sample:=range []struct{name string;start,end int64}{{"negative",-1,2},{"reversed",2,1},{"end-overflow",0,5},{"start-overflow",5,5},{"extreme",math.MinInt64,math.MaxInt64}}{
  t.Run(sample.name,func(t *testing.T){
   defer func(){if panicValue:=recover();panicValue!=nil{t.Errorf("substring panicked: %v",panicValue)}}()
   if got,err:=app.sliceValue(t.Context(),value,sample.start,sample.end);got!=""||err==nil{t.Fatal(got,err)}
   if got,err:=app.safeSlice(t.Context(),&value,sample.start,sample.end);got!=nil||err==nil{t.Fatal(got,err)}
   if got,err:=app.mappedSlices(t.Context(),[]string{value},sample.start,sample.end);got!=nil||err==nil{t.Fatal(got,err)}
   if got,err:=app.guardedSlice(t.Context(),false,value,sample.start,sample.end);got||err!=nil{t.Fatal(got,err)}
  })
 }
 if got,err:=app.sliceValue(t.Context(),value,1,3);got!="bc"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.tailValue(t.Context(),value,4);got!=""||err!=nil{t.Fatal(got,err)}
 if got,err:=app.sliceValue(t.Context(),value,0,0);got!=""||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeSlice(t.Context(),nil,-1,2);got!=nil||err!=nil{t.Fatal(got,err)}
 if got,err:=app.defaultSlice(t.Context(),nil,-1,2);got!="fallback"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.defaultSlice(t.Context(),&value,-1,2);got!=""||err==nil{t.Fatal(got,err)}
 if got,err:=app.doubleSlice(t.Context(),value);got!="a"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.collidingSlice(t.Context(),value,1,3);got!="bc"||err!=nil{t.Fatal(got,err)}
}
func TestSubstringHTTPBounds(t *testing.T){
 app:=&App{};router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name string;id,start,end int64;want string;invalid bool}{
  {"sliceCall",51,-1,2,"",true},{"sliceCall",51,2,1,"",true},{"sliceCall",51,0,5,"",true},
  {"sliceCall",51,math.MinInt64,math.MaxInt64,"",true},{"tailCall",52,-1,0,"",true},
  {"tailCall",52,math.MaxInt64,0,"",true},{"sliceCall",51,1,3,"bc",false},{"tailCall",52,4,0,"",false},
 }{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+sample.name+"\",\"value\":\"abcd\",\"start\":"+strconv.FormatInt(sample.start,10))
  if sample.id==51{body=append(body,[]byte(",\"end\":"+strconv.FormatInt(sample.end,10))...)};body=append(body,'}')
  if binary{
   encoder:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,uint64(sample.id)),0))
   if err:=encoder.WriteFieldString(1,"abcd");err!=nil{t.Fatal(err)};encoder.WriteFieldInt(2,sample.start)
   if sample.id==51{encoder.WriteFieldInt(3,sample.end)};encoder.WriteEnd();body=encoder.Bytes()
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if sample.invalid{if response.Code!=400{t.Errorf("%s binary=%t start=%d end=%d status=%d",sample.name,binary,sample.start,sample.end,response.Code)};continue}
  if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{
   expected,err:=codec.AppendString(nil,sample.want);if err!=nil{t.Fatal(err)}
   if !bytes.Equal(response.Body.Bytes(),expected){t.Fatal(response.Body.Bytes(),expected)}
  }else{var envelope struct{Data string};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||envelope.Data!=sample.want{t.Fatal(envelope.Data,err)}}
 }}
}
`

func TestGeneratedSubstringOverLRPC(t *testing.T) {
	runGeneratedFixture(t, `
fn substringService(value: String, start: Int, end: Int): String @service { return value.substring(start, end) }
`, `package generated
import("bytes";"errors";"net";"sync/atomic";"testing";
 "github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";
 "github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
type substringListener struct{net.Listener;accepted atomic.Int32}
func(listener *substringListener)Accept()(net.Conn,error){connection,err:=listener.Listener.Accept();if err==nil{listener.accepted.Add(1)};return connection,err}
func TestSubstringLRPC(t *testing.T){
 router:=luvia.New().Router;app:=&App{}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 rawListener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 listener:=&substringListener{Listener:rawListener}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{value string;start,end int64;want string;invalid bool}{
  {"abcd",1,3,"bc",false},{"abcd",-1,2,"",true},{"abcd",0,5,"",true},
  {"abcd",2,1,"",true},{"中文",1,3,"",true},{"中文",3,6,"文",false},{"",0,0,"",false},
 }{
  var params codec.Encoder;if err:=params.WriteFieldString(1,sample.value);err!=nil{t.Fatal(err)}
  params.WriteFieldInt(2,sample.start);params.WriteFieldInt(3,sample.end);params.WriteEnd()
  body,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs["svc:substringService"].ID,nil,params.Bytes())
  if sample.invalid{
   failure,ok:=errors.AsType[*luxerrors.AppError](err);if !ok||failure.Code!=400||failure.Internal||failure.Data==nil{t.Fatal(body,err)}
   raw,writeErr:=failure.Data.AppendJSON(nil);if writeErr!=nil||!bytes.Contains(raw,[]byte("String.substring")){t.Fatal(string(raw),writeErr)}
   continue
  }
  expected,encodeErr:=codec.AppendString(nil,sample.want)
  if err!=nil||encodeErr!=nil||!bytes.Equal(body,expected){t.Fatal(sample.value,body,expected,err,encodeErr)}
 }
 if listener.accepted.Load()!=1{t.Fatalf("business errors replaced the connection: %d",listener.accepted.Load())}
}
`, StableIDs{})
}

func TestGeneratedSubstringFieldTransforms(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime { id: Int @id name: String? @beforeSave { it = it.substring(0, 2) } @transform { it.substring(end: 2, start: 0) } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSubstringFieldTransforms(t *testing.T){
 if got,err:=prepareSave4UserName(nil);err!=nil||got!=nil{t.Fatal(got,err)}
 text:="abcd";prepared,err:=prepareSave4UserName(&text)
 if err!=nil||prepared==nil||*prepared!="ab"||text!="abcd"{t.Fatal(prepared,text,err)}
 value:=&User{Id:1,Name:&text};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return value.WriteLuxo(out,mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  text="abcd";value.Name=&text;out:=&api.ResponseBuf{}
  if err:=write(out);err!=nil||text!="abcd"{t.Fatal(text,err)}
  text="a";out=&api.ResponseBuf{};if err:=write(out);err==nil{t.Fatal("short transform accepted")}
  value.Name=nil;out=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal("null invoked transform",err)}
 }
 text="a";if got,err:=prepareSave4UserName(&text);err==nil||got!=nil{t.Fatal(got,err)}
 text="中";if got,err:=prepareSave4UserName(&text);err==nil||got!=nil{t.Fatal("split UTF-8 accepted",got,err)}
}
`)
}

func TestGeneratedPaddingExtremeWidths(t *testing.T) {
	runGeneratedFixture(t, `
api padLeft(value: String, width: Int, pad: String): String { return value.padStart(width, pad) }
api padRight(value: String, width: Int, pad: String): String { return value.padEnd(width, pad) }
fn safePad(value: String?, width: Int, pad: String): String? { return value?.padStart(width, pad) }
fn utfPadEnd(value: String, width: Int, pad: String): String { return value.padEnd(width, pad) }
`, `package generated
import("bytes";"encoding/json";"math";"net/http/httptest";"strconv";"testing";"unicode/utf8";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestPaddingWidthBoundaries(t *testing.T) {
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 value:="abc"
 for _,width:=range []int64{math.MinInt64,math.MinInt64+1,-1,0,3} {
  got,err:=app.safePad(t.Context(),&value,width,"0");if err!=nil||got==nil||*got!=value{t.Fatal(width,got,err)}
  if got,err:=app.safePad(t.Context(),nil,width,"0");err!=nil||got!=nil{t.Fatal(width,got,err)}
 }
 for _,sample:=range []struct{value string;width int64;pad,left,right string}{
  {"a",5,"中","中a","a中"},{"a",2,"中","a","a"},{"文",9,"a中","a中a文","文a中a"},
 }{
  left,err:=app.safePad(t.Context(),&sample.value,sample.width,sample.pad)
  if err!=nil||left==nil||*left!=sample.left||!utf8.ValidString(*left){t.Fatal("padStart split UTF-8",left,err)}
  right,err:=app.utfPadEnd(t.Context(),sample.value,sample.width,sample.pad)
  if err!=nil||right!=sample.right||!utf8.ValidString(right){t.Fatal("padEnd split UTF-8",right,err)}
 }
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for index,name:=range []string{"padLeft","padRight"}{for _,binary:=range []bool{false,true}{
  for _,width:=range []int64{math.MinInt64,math.MinInt64+1,-1,0,3,4}{
   body:=[]byte("{\"$api\":\""+name+"\",\"value\":\"abc\",\"width\":"+strconv.FormatInt(width,10)+",\"pad\":\"0\"}")
   if binary{
    encoder:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,uint64(index+51)),0))
    if err:=encoder.WriteFieldString(1,value);err!=nil{t.Fatal(err)}
    encoder.WriteFieldInt(2,width)
    if err:=encoder.WriteFieldString(3,"0");err!=nil{t.Fatal(err)}
    encoder.WriteEnd();body=encoder.Bytes()
   }
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
   if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatalf("%s binary=%t width=%d: %d %s",name,binary,width,response.Code,response.Body.String())}
   want:=value;if width==4{want="0abc";if index==1{want="abc0"}}
   if binary{
    expected,err:=codec.AppendString(nil,want);if err!=nil{t.Fatal(err)}
    if !bytes.Equal(response.Body.Bytes(),expected){t.Fatalf("%s %d: %x != %x",name,width,response.Body.Bytes(),expected)}
   }else{
    var envelope struct{Data string};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||envelope.Data!=want{t.Fatal(name,width,envelope.Data,err)}
   }
  }
 }}
}
`, StableIDs{
		APIs:      map[string]int{"padLeft": 51, "padRight": 52},
		APIParams: map[string]map[string]int{"padLeft": {"value": 1, "width": 2, "pad": 3}, "padRight": {"value": 1, "width": 2, "pad": 3}},
	})
}
