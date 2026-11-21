package codegen

// Both generated variants validate values and preserve ownership. Only the
// reference lacks the depth argument and guard; legitimate wire is identical.
const eventDepthBenchmarks = `
var depthSnapshotSink event.Snapshot
var depthWireSink []byte

func legacyDepthValue(depth, width int) LegacyDepthEvent {
    node:=LegacyDepthNode{Value:1}
    for i:=1;i<depth;i++{previous:=node;node=LegacyDepthNode{Value:1,Next:&previous}}
    return LegacyDepthEvent{Root:node,Siblings:make([]LegacyDepthNode,width)}
}

func TestEventDepthReferenceParity(t *testing.T){
    for _,depth:=range []int{1,16,64}{for _,width:=range []int{0,128}{
        current:=depthValue(depth,false);current.Siblings=make([]DepthNode,width)
        previous:=legacyDepthValue(depth,width)
        wire,err:=current.MarshalLuxo();if err!=nil{t.Fatal(err)}
        reference,err:=previous.MarshalLuxo();if err!=nil{t.Fatal(err)}
        if string(wire)!=string(reference){t.Fatal("valid wire changed")}
        var left DepthEvent;var right LegacyDepthEvent
        if err:=left.UnmarshalLuxo(reference);err!=nil{t.Fatal(err)}
        if err:=right.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
        if left.Root.Value!=right.Root.Value||len(left.Siblings)!=len(right.Siblings){t.Fatal("reference decode changed")}
    }}
}

func BenchmarkEventSnapshotDepth(b *testing.B){
    for _,sample:=range []struct{depth,width int}{{1,0},{16,0},{64,0},{1,128}}{
        current:=depthValue(sample.depth,false);current.Siblings=make([]DepthNode,sample.width)
        previous:=legacyDepthValue(sample.depth,sample.width)
        wire,err:=current.MarshalLuxo();if err!=nil{b.Fatal(err)}
        var decoded DepthEvent;var oldDecoded LegacyDepthEvent
        stages:=[]struct{name string;before,current func()error}{
            {"copy",func()error{var err error;depthSnapshotSink,err=previous.SnapshotEvent();return err},func()error{var err error;depthSnapshotSink,err=current.SnapshotEvent();return err}},
            {"write",func()error{var err error;depthWireSink,err=previous.MarshalLuxo();return err},func()error{var err error;depthWireSink,err=current.MarshalLuxo();return err}},
            {"read",func()error{return oldDecoded.UnmarshalLuxo(wire)},func()error{return decoded.UnmarshalLuxo(wire)}},
        }
        for _,stage:=range stages{
            variants:=[]struct{name string;run func()error}{{"before",stage.before},{"current",stage.current}}
            if os.Getenv("LUXO_EVENT_BENCHMARK_REVERSE")=="1"{variants[0],variants[1]=variants[1],variants[0]}
            for _,variant:=range variants{b.Run(fmt.Sprintf("depth=%d/width=%d/%s/%s",sample.depth,sample.width,stage.name,variant.name),func(b *testing.B){
                b.ReportAllocs();for b.Loop(){if err:=variant.run();err!=nil{b.Fatal(err)}}
            })}
        }
    }
}
`
