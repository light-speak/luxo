package codegen

import "testing"

func TestGeneratedSealedAsyncSnapshots(t *testing.T) {
	runGeneratedFixture(t, `type Box<T> { value: T }
sealed Outcome { Value(value: Int, data: Bytes) Loop(child: Outcome?) Boxed(value: Box<Outcome>) Pending }
fn capture(value: Outcome, gate: Channel<Int>, output: Channel<Outcome>) {
 async { val _ = <-gate output <- value }
}
fn capturePair(left: Outcome, right: Outcome, gate: Channel<Int>, output: Channel<Outcome>) {
 async { val _ = <-gate output <- left output <- right }
}
fn captureBox(value: Box<Outcome>, gate: Channel<Int>, output: Channel<Box<Outcome>>) {
 async { val _ = <-gate output <- value }
}
`, sealedAsyncAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Box": {"value": 1}, "Outcome": {"Value": 1, "Loop": 2, "Boxed": 3, "Pending": 4},
		"Outcome.Value": {"value": 1, "data": 2}, "Outcome.Loop": {"child": 1}, "Outcome.Boxed": {"value": 1},
	}})
}

const sealedAsyncAssertions = `package generated
import("context";"testing")
func takeAsyncSealed[T any](t *testing.T, launch func(context.Context,T,chan int64,chan T)error,value T,mutate func())T{
 t.Helper();gate,output:=make(chan int64,1),make(chan T,1);if err:=launch(t.Context(),value,gate,output);err!=nil{t.Fatal(err)};mutate();gate<-1;return <-output
}
func TestSealedAsyncOwnership(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 source:=(Outcome{}).NewVariant1(7,[]byte{1,2})
 got:=takeAsyncSealed(t,app.capture,source,func(){source.AsVariant1().F2[0]=8});if got.AsVariant1().F1!=7||got.AsVariant1().F2[0]!=1{t.Fatal("sealed capture retained sender storage",got)}
 newer:=takeAsyncSealed(t,app.capture,source,func(){source.AsVariant1().F2[0]=9});if newer.AsVariant1().F2[0]!=8||got.AsVariant1().F2[0]!=1{t.Fatal("snapshot state leaked across launches")}
 gate,output:=make(chan int64,1),make(chan Outcome,2);if err:=app.capturePair(t.Context(),source,source,gate,output);err!=nil{t.Fatal(err)};source.AsVariant1().F2[0]=10;gate<-1;left,right:=<-output,<-output
 left.AsVariant1().F2[0]=11;if right.AsVariant1().F2[0]!=11||source.AsVariant1().F2[0]!=10{t.Fatal("captured aliases or ownership were lost")}
 loop:=(Outcome{}).NewVariant2(nil);loop.AsVariant2().F1=&loop
 cyclic:=takeAsyncSealed(t,app.capture,loop,func(){loop.AsVariant2().F1=nil});child:=cyclic.AsVariant2().F1
 if child==nil||child.AsVariant2().F1!=child{t.Fatal("cyclic sealed snapshot was not closed")}
 var boxed Outcome;if err:=boxed.ReadJSONInput([]byte("{\"Boxed\":{\"value\":{\"value\":{\"Value\":{\"value\":9,\"data\":\"AQI=\"}}}}}"));err!=nil{t.Fatal(err)}
 box:=boxed.AsVariant3().F1;captured:=takeAsyncSealed(t,app.captureBox,box,func(){box.Value.AsVariant1().F2[0]=99})
 if captured.Value.AsVariant1().F2[0]!=1{t.Fatal("generic wrapper retained sealed payload")}
}
`
