package codegen

const asyncSnapshotBenchmarks = `package generated
import("context";"encoding/json";"testing")
var asyncByteSink []byte
var asyncJSONSink json.RawMessage
var asyncListSink [][]byte
var asyncNodeSink *AsyncNode
var asyncIntSink int64
func BenchmarkAsyncSnapshots(b *testing.B){
 ctx:=context.Background();app:=&App{}
 b.Run("scalar",func(b *testing.B){
  gate,out:=make(chan int64,1),make(chan int64,1)
  if value,err:=app.captureScalar(ctx,gate,out);err!=nil||value!=2{b.Fatal(value,err)}
  gate<-1;if got:=<-out;got!=expectedScalar{b.Fatal("capture preflight",got)}
  b.ReportAllocs();b.ResetTimer()
  for b.Loop(){if _,err:=app.captureScalar(ctx,gate,out);err!=nil{b.Fatal(err)};gate<-1;asyncIntSink=<-out}
 })
 for _,size:=range []int{8,4096,73728}{
  name:="bytes8";if size==4096{name="bytes4KiB"};if size==73728{name="bytes72KiB"}
  b.Run(name,func(b *testing.B){
   value:=make([]byte,size);value[0]=42;gate,out:=make(chan int64,1),make(chan []byte,1)
   if err:=app.captureBytes(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;if got:=<-out;len(got)!=size||got[0]!=42{b.Fatal("bytes preflight")}
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){if err:=app.captureBytes(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;asyncByteSink=<-out}
  })
 }
 b.Run("json",func(b *testing.B){
  value:=json.RawMessage("{\"a\":1,\"b\":2,\"c\":3,\"d\":4,\"e\":5,\"f\":6,\"g\":7}");gate,out:=make(chan int64,1),make(chan json.RawMessage,1)
  if err:=app.captureJSON(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;if got:=<-out;string(got)!=string(value){b.Fatal("json preflight")}
  b.ReportAllocs();b.ResetTimer()
  for b.Loop(){if err:=app.captureJSON(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;asyncJSONSink=<-out}
 })
 b.Run("list16x8",func(b *testing.B){
  value:=make([][]byte,16);for i:=range value{value[i]=[]byte("abcdefgh")};gate,out:=make(chan int64,1),make(chan [][]byte,1)
  if err:=app.captureList(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;if got:=<-out;len(got)!=16||string(got[0])!="abcdefgh"{b.Fatal("list preflight")}
  b.ReportAllocs();b.ResetTimer()
  for b.Loop(){if err:=app.captureList(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;asyncListSink=<-out}
 })
 b.Run("cyclicObject",func(b *testing.B){
  value:=&AsyncNode{Value:42,Data:[]byte("abcdefgh")};value.Next=value;gate,out:=make(chan int64,1),make(chan *AsyncNode,1)
  if err:=app.captureObject(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;if got:=<-out;got==nil||got.Value!=42||got.Next!=got{b.Fatal("object preflight")}
  b.ReportAllocs();b.ResetTimer()
  for b.Loop(){if err:=app.captureObject(ctx,value,gate,out);err!=nil{b.Fatal(err)};gate<-1;asyncNodeSink=<-out}
 })
}

const expectedScalar int64 = 1
`
