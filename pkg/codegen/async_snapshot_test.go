package codegen

import (
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

const asyncSnapshotSource = `
type AsyncNode { value: Int data: Bytes next: AsyncNode? children: [AsyncNode] other: AsyncOther? }
type AsyncOther { node: AsyncNode? }
type AsyncBuiltins { date: DateTime? amount: Decimal? key: UUID? }
fn captureScalar(gate: Channel<Int>, output: Channel<Int>): Int {
 var value = 1
 async { val _ = <-gate output <- value }
 value = 2
 return value
}
fn captureBytes(value: Bytes, gate: Channel<Int>, output: Channel<Bytes>) {
 async { val _ = <-gate output <- value }
}
fn captureJSON(value: JSON, gate: Channel<Int>, output: Channel<JSON>) {
 async { val _ = <-gate output <- value }
}
fn captureList(value: [Bytes], gate: Channel<Int>, output: Channel<[Bytes]>) {
 async { val _ = <-gate output <- value }
}
fn captureNullable(value: Int?, gate: Channel<Int>, output: Channel<Int?>) {
 async { val _ = <-gate output <- value }
}
fn captureObject(value: AsyncNode?, gate: Channel<Int>, output: Channel<AsyncNode?>) {
 async { val _ = <-gate output <- value }
}
fn captureAliases(left: AsyncNode?, right: AsyncNode?, gate: Channel<Int>, output: Channel<AsyncNode?>) {
 async { val _ = <-gate output <- left output <- right }
}
fn captureNullableBytes(value: Bytes?, gate: Channel<Int>, output: Channel<Bytes?>) {
 async { val _ = <-gate output <- value }
}
fn captureIntegers(value: [Int], gate: Channel<Int>, output: Channel<[Int]>) {
 async { val _ = <-gate output <- value }
}
fn captureObjectValue(value: AsyncNode, gate: Channel<Int>, output: Channel<AsyncNode>) {
 async { val _ = <-gate output <- value }
}
fn captureBuiltins(value: AsyncBuiltins, gate: Channel<Int>, output: Channel<AsyncBuiltins>) {
 async { val _ = <-gate output <- value }
}
`

func TestGeneratedAsyncSnapshots(t *testing.T) {
	runGeneratedFixture(t, asyncSnapshotSource, asyncSnapshotAssertions, StableIDs{
		ModelFields: map[string]map[string]int{
			"AsyncNode": {"value": 1, "data": 2, "next": 3, "children": 4, "other": 5}, "AsyncOther": {"node": 1},
			"AsyncBuiltins": {"date": 1, "amount": 2, "key": 3},
		},
	}, asyncSnapshotBenchmarks)
}

const asyncSnapshotAssertions = `package generated
import (
 "bytes"
 "encoding/json"
 "testing"
 "time"
 "github.com/google/uuid"
 "github.com/shopspring/decimal"
)
func TestAsyncSnapshots(t *testing.T) {
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 t.Run("scalar",func(t *testing.T){
  gate,output:=make(chan int64,1),make(chan int64,1)
  value,err:=app.captureScalar(t.Context(),gate,output)
  if err!=nil||value!=2{t.Fatal(value,err)}
  gate<-1
  if got:=<-output;got!=1{t.Fatalf("capture at launch: %d",got)}
 })
 t.Run("bytes",func(t *testing.T){
  for _,value:=range [][]byte{nil,{},[]byte("old")} {
   gate,output:=make(chan int64,1),make(chan []byte,1)
   before:=bytes.Clone(value)
   if err:=app.captureBytes(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
   clear(value);gate<-1
   got:=<-output
   if !bytes.Equal(got,before)||(got==nil)!=(before==nil){t.Fatalf("bytes: %v -> %v",before,got)}
  }
 })
 t.Run("json",func(t *testing.T){
  value:=json.RawMessage("123")
  gate,output:=make(chan int64,1),make(chan json.RawMessage,1)
  if err:=app.captureJSON(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  copy(value,"456");gate<-1
  if got:=<-output;string(got)!="123"{t.Fatalf("json: %s",got)}
 })
 t.Run("list",func(t *testing.T){
  value:=[][]byte{[]byte("old"),nil,{}}
  gate,output:=make(chan int64,1),make(chan [][]byte,1)
  if err:=app.captureList(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  copy(value[0],"new");value[1]=[]byte("new");gate<-1
  got:=<-output
  if string(got[0])!="old"||got[1]!=nil||got[2]==nil{t.Fatalf("list: %v",got)}
 })
 t.Run("nullable",func(t *testing.T){
  value:=int64(1)
  gate,output:=make(chan int64,1),make(chan *int64,1)
  if err:=app.captureNullable(t.Context(),&value,gate,output);err!=nil{t.Fatal(err)}
  value=2;gate<-1;got:=<-output
  if got==nil||*got!=1||got==&value{t.Fatalf("nullable: %v",got)}
 })
 t.Run("recursive object",func(t *testing.T){
  value:=&AsyncNode{Value:1,Data:[]byte("old")}
  value.Next=value;value.Children=[]AsyncNode{{Value:3,Data:[]byte("kid"),Next:value}};value.Other=&AsyncOther{Node:value}
  gate,output:=make(chan int64,1),make(chan *AsyncNode,1)
  if err:=app.captureObject(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  value.Value=2;copy(value.Data,"new");value.Children[0].Value=4;gate<-1;got:=<-output
  if got==nil||got==value||got.Value!=1||string(got.Data)!="old"||got.Next!=got||len(got.Children)!=1||got.Children[0].Value!=3||got.Children[0].Next!=got||got.Other==nil||got.Other.Node!=got{t.Fatalf("graph snapshot: %+v",got)}
  got.Data[0]='X'
  if string(value.Data)!="new"{t.Fatal("snapshot still aliases parent")}
 })
 t.Run("aliases across captures",func(t *testing.T){
  value:=&AsyncNode{Value:7}
  gate,output:=make(chan int64,1),make(chan *AsyncNode,2)
  if err:=app.captureAliases(t.Context(),value,value,gate,output);err!=nil{t.Fatal(err)}
  value.Value=8;gate<-1;left,right:=<-output,<-output
  if left==nil||left!=right||left==value||left.Value!=7{t.Fatal("capture graph aliases changed")}
 })
 t.Run("nullable bytes",func(t *testing.T){
  for _,value:=range []*[]byte{nil,new([]byte),new([]byte("old"))}{
   gate,output:=make(chan int64,1),make(chan *[]byte,1)
   if err:=app.captureNullableBytes(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
   if value!=nil{*value=[]byte("new")};gate<-1;got:=<-output
   if (got==nil)!=(value==nil)||got!=nil&&string(*got)=="new"{t.Fatal("nullable bytes aliased")}
  }
 })
 t.Run("scalar list",func(t *testing.T){
  value:=[]int64{1,2}
  gate,output:=make(chan int64,1),make(chan []int64,1)
  if err:=app.captureIntegers(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  value[0]=3;gate<-1;got:=<-output
  if len(got)!=2||got[0]!=1||got[1]!=2{t.Fatal(got)}
 })
 t.Run("value object",func(t *testing.T){
  value:=AsyncNode{Value:7,Data:[]byte("old")}
  gate,output:=make(chan int64,1),make(chan AsyncNode,1)
  if err:=app.captureObjectValue(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  copy(value.Data,"new");gate<-1;got:=<-output
  if got.Value!=7||string(got.Data)!="old"{t.Fatal(got)}
 })
 t.Run("deep graph",func(t *testing.T){
  value:=&AsyncNode{};tail:=value
  for index:=0;index<10000;index++{tail.Next=&AsyncNode{Value:int64(index)};tail=tail.Next}
  gate,output:=make(chan int64,1),make(chan *AsyncNode,1)
  if err:=app.captureObject(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  tail.Value=-1;gate<-1;got:=<-output
  for index:=0;index<10000;index++{got=got.Next;if got==nil||got.Value!=int64(index){t.Fatalf("node %d",index)}}
 })
 t.Run("builtin pointers",func(t *testing.T){
  date,amount,key:=time.Unix(123,456),decimal.NewFromInt(42),uuid.UUID{15:1}
  value:=AsyncBuiltins{Date:&date,Amount:&amount,Key:&key}
  gate,output:=make(chan int64,1),make(chan AsyncBuiltins,1)
  if err:=app.captureBuiltins(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
  date=time.Time{};amount=decimal.Zero;key=uuid.Nil;gate<-1;got:=<-output
  if got.Date==nil||got.Date.Unix()!=123||got.Date.Nanosecond()!=456||got.Amount==nil||!got.Amount.Equal(decimal.NewFromInt(42))||got.Key==nil||got.Key[15]!=1{t.Fatal(got)}
 })
}
`

func TestGeneratedAsyncModelSnapshot(t *testing.T) {
	const source = `model AsyncRecord @noTime @soft { id: Int @id data: Bytes values: [Int] }
fn captureRecord(value: AsyncRecord, gate: Channel<Int>, output: Channel<AsyncRecord>) {
 async { val _ = <-gate output <- value }
}`
	runGeneratedFixture(t, source, `package generated
import("testing";"time")
func TestModelSnapshot(t *testing.T){
 app:=&App{}
 date:=time.Unix(123,456)
 value:=&AsyncRecord{Id:7,Data:[]byte("old"),Values:[]int64{1},DeletedAt:&date}
 gate,output:=make(chan int64,1),make(chan *AsyncRecord,1)
 if err:=app.captureRecord(t.Context(),value,gate,output);err!=nil{t.Fatal(err)}
 value.Id=8;copy(value.Data,"new");value.Values[0]=2;date=time.Time{};gate<-1;got:=<-output
 if got==value||got.Id!=7||string(got.Data)!="old"||got.Values[0]!=1||got.DeletedAt==nil||got.DeletedAt.Unix()!=123{t.Fatal(got)}
}`, StableIDs{ModelFields: map[string]map[string]int{"AsyncRecord": {"id": 1, "data": 2, "values": 3, "deletedAt": 4}}})
}

func TestAsyncSnapshotMetadata(t *testing.T) {
	c := newCompiler(nil)
	snapshot := c.asyncSnapshotState()
	if snapshot != c.asyncSnapshotState() || snapshot.mutable(&ast.TypeRef{Name: "Channel"}) {
		t.Fatal("snapshot state or channel ownership")
	}
	g := defaultGenerator()
	if g.generateAsyncSnapshots("generated") != nil {
		t.Fatal("unused snapshot source")
	}
	g.asyncSnapshots = snapshot
	if g.generateAsyncSnapshots("generated") != nil {
		t.Fatal("empty snapshot source")
	}
	input := &semantic.Result{Files: []*ast.File{{Extends: []*ast.ExtendDecl{{Name: "External"}}}}}
	snapshot = g.newAsyncSnapshots(input, nil)
	if len(snapshot.fields["External"]) != 1 || snapshot.fields["External"][0].goName != "Id" {
		t.Fatal("external primary key missing from snapshot")
	}
}

func TestAsyncSnapshotGenerationIsIsolated(t *testing.T) {
	const source = `fn fork(value: Bytes, gate: Channel<Int>, output: Channel<Bytes>) {
 async { val _ = <-gate output <- value }
}`
	tokens, lexErrors := lexer.New(source, "async.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("async.luxo")
	if len(lexErrors) != 0 || len(parseErrors) != 0 {
		t.Fatal(lexErrors, parseErrors)
	}
	input := semantic.Analyze([]*ast.File{file})
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG})
	want, err := g.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			got, err := g.Generate(input, "generated", nil)
			if err != nil {
				t.Error(err)
				return
			}
			if len(got.Files) != len(want.Files) {
				t.Error("concurrent generation changed file count")
			}
			for name, source := range want.Files {
				if string(got.Files[name]) != string(source) {
					t.Errorf("concurrent snapshot generation changed %s", name)
				}
			}
		})
	}
	workers.Wait()
}
