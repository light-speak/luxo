package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedEventSnapshotDepth(t *testing.T) {
	const input = `type DepthNode { value: Int next: DepthNode? children: [DepthNode] }
event Depth(root: DepthNode, siblings: [DepthNode])`
	tokens, lexErrors := lexer.New(input, "depth.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("depth.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	if checked := semantic.Analyze([]*ast.File{file}); len(checked.Errors) != 0 {
		t.Fatal(checked.Errors)
	}
	source := generatedSnapshotSource(t, file)
	ensureGeneratedNATS(t)
	runGeneratedProtocolPackage(t, source+eventDepthReference(t, source), eventDepthAssertions+eventDepthBenchmarks)
}

// Recreate the previous unchecked generator only for legal-value comparisons.
// Never pass hostile depth or native cycles to this reference.
func eventDepthReference(t *testing.T, source string) string {
	t.Helper()
	start := strings.Index(source, "// DepthEvent is the payload")
	if start < 0 {
		t.Fatal("missing generated depth event")
	}
	lines := strings.Split(source[start:], "\n")
	kept := make([]string, 0, len(lines))
	guards := 0
	for _, line := range lines {
		if strings.Contains(line, "if depth > event.MaxSnapshotDepth") {
			guards++
			continue
		}
		if !strings.Contains(line, "const depth = 0") {
			kept = append(kept, line)
		}
	}
	if guards != 3 {
		t.Fatalf("depth reference drifted: %d guards", guards)
	}
	return strings.NewReplacer(
		", depth int", "", ", depth + 1", "",
		"DepthNode", "LegacyDepthNode", "DepthEvent", "LegacyDepthEvent",
		"UnmarshalDepth", "UnmarshalLegacyDepth", "EmitDepth", "EmitLegacyDepth",
		"RegisterEvents", "registerLegacyDepthEvents", "App", "LegacyDepthApp",
	).Replace(strings.Join(kept, "\n"))
}

const eventDepthAssertions = `package generated
import("context";"errors";"fmt";"os";"testing";"time";"github.com/light-speak/luxo/pkg/lux/codec";natsevent "github.com/light-speak/luxo/pkg/lux/nats/event";"github.com/light-speak/luxo/pkg/lux/event")

func depthValue(depth int, list bool) DepthEvent {
    node:=DepthNode{Value:1}
    for i:=1;i<depth;i++ {
        if list { node=DepthNode{Value:1,Children:[]DepthNode{node}} } else { previous:=node;node=DepthNode{Value:1,Next:&previous} }
    }
    return DepthEvent{Root:node,Siblings:make([]DepthNode,128)}
}

// Construct hostile wire iteratively, independently of the generated writer.
func depthWire(depth int,list bool)[]byte {
    var child []byte
    for i:=0;i<depth;i++ {
        var enc codec.Encoder
        enc.WriteFieldHeader(1);enc.WriteInt(1)
        enc.WriteFieldHeader(2)
        if i>0&&!list {enc.WritePresent();enc.WriteBytes(child)}else{enc.WriteNull()}
        enc.WriteFieldHeader(3)
        if i>0&&list {enc.WriteArrayHeader(1);enc.WriteBytes(child)}else{enc.WriteArrayHeader(0)}
        enc.WriteEnd();child=enc.Bytes()
    }
    var enc codec.Encoder
    enc.WriteFieldHeader(1);enc.WriteBytes(child)
    enc.WriteFieldHeader(2);enc.WriteArrayHeader(0);enc.WriteEnd()
    return enc.Bytes()
}

func checkDepth(t *testing.T,root DepthNode,depth int,list bool) {
    t.Helper()
    for i:=0;i<depth;i++ {
        if root.Value!=1{t.Fatal("payload truncated or changed")}
        if i==depth-1{if root.Next!=nil||len(root.Children)!=0{t.Fatal("unexpected descendant")};return}
        if list{if len(root.Children)!=1{t.Fatal("missing child")};root=root.Children[0]}else{if root.Next==nil{t.Fatal("missing next")};root=*root.Next}
    }
}

func TestEventDepthBoundary(t *testing.T) {
    for _,list:=range []bool{false,true}{for _,depth:=range []int{1,63,64,65,128}{
        t.Run(fmt.Sprintf("depth=%d/list=%t",depth,list),func(t *testing.T){
            source:=depthValue(depth,list);valid:=depth<=64
            owned,copyErr:=source.SnapshotEvent()
            wire,writeErr:=source.MarshalLuxo()
            var decoded DepthEvent;readErr:=decoded.UnmarshalLuxo(depthWire(depth,list))
            if !valid {
                if !errors.Is(copyErr,event.ErrSnapshotDepth)||owned!=nil{t.Errorf("memory depth rejection: %v",copyErr)}
                if !errors.Is(writeErr,event.ErrSnapshotDepth)||wire!=nil{t.Errorf("writer depth rejection: %v",writeErr)}
                if !errors.Is(readErr,event.ErrSnapshotDepth){t.Errorf("reader depth rejection: %v",readErr)}
                return
            }
            if copyErr!=nil||writeErr!=nil||readErr!=nil{t.Fatalf("legal depth rejected: %v %v %v",copyErr,writeErr,readErr)}
            checkDepth(t,decoded.Root,depth,list)
            var roundtrip DepthEvent;if err:=roundtrip.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
            checkDepth(t,roundtrip.Root,depth,list)
            if len(roundtrip.Siblings)!=128{t.Fatal("list length treated as nesting depth")}
            bus:=event.NewChanBus(1);defer bus.Close();received:=0
            for range 2{if err:=event.OnDecode(bus,"Depth",UnmarshalDepth,func(_ context.Context,value DepthEvent)error{
                checkDepth(t,value.Root,depth,list);received++;value.Root.Value=99;return nil
            });err!=nil{t.Fatal(err)}}
            if err:=EmitDepth(t.Context(),bus,source);err!=nil{t.Fatal(err)}
            source.Root.Value=99;bus.Close()
            if received!=2{t.Fatalf("delivered %d copies",received)}
        })
    }}
}

func TestEventDepthRejectsCyclesBeforeDelivery(t *testing.T){
    for _,list:=range []bool{false,true}{
        source:=DepthEvent{Root:DepthNode{Value:1}}
        if list{source.Root.Children=[]DepthNode{{Value:1}};source.Root.Children[0].Children=source.Root.Children}else{source.Root.Next=&source.Root}
        if snapshot,err:=source.SnapshotEvent();snapshot!=nil||!errors.Is(err,event.ErrSnapshotDepth){t.Fatalf("cyclic copy: %v",err)}
        if wire,err:=source.MarshalLuxo();wire!=nil||!errors.Is(err,event.ErrSnapshotDepth){t.Fatalf("cyclic wire: %v",err)}
        bus:=event.NewChanBus(1);called:=false
        if err:=event.OnDecode(bus,"Depth",UnmarshalDepth,func(context.Context,DepthEvent)error{called=true;return nil});err!=nil{t.Fatal(err)}
        err:=EmitDepth(t.Context(),bus,source);bus.Close()
        if !errors.Is(err,event.ErrSnapshotDepth)||called{t.Fatalf("cyclic publication reached listener: %v",err)}
    }
}

func TestEventDepthCountsEachRootIndependently(t *testing.T){
    for _,depth:=range []int{64,65}{
        source:=depthValue(1,false);source.Siblings=[]DepthNode{depthValue(depth,false).Root}
        _,err:=source.SnapshotEvent();wire,writeErr:=source.MarshalLuxo()
        if depth==65{if !errors.Is(err,event.ErrSnapshotDepth)||!errors.Is(writeErr,event.ErrSnapshotDepth)||wire!=nil{t.Fatal("deep sibling accepted")};continue}
        if err!=nil||writeErr!=nil{t.Fatalf("legal sibling rejected: %v %v",err,writeErr)}
        var decoded DepthEvent;if err:=decoded.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
        checkDepth(t,decoded.Siblings[0],depth,false)
    }
}

func TestEventDepthNATS(t *testing.T){
    address:=os.Getenv("NATS_URL");if address==""{t.Fatal("depth boundary integration requires a NATS test server")}
    bus,err:=natsevent.NewNATSBus(address);if err!=nil{t.Fatal(err)};defer bus.Close()
    subject:=fmt.Sprintf("luxo.tests.depth.%d",time.Now().UnixNano());received:=make(chan error,3)
    if err:=bus.On(subject,func(_ context.Context,raw any)error{
        wire,ok:=raw.([]byte);if !ok{received<-errors.New("expected wire bytes");return nil}
        var value DepthEvent;err:=value.UnmarshalLuxo(wire)
        if err==nil{checkDepth(t,value.Root,64,false)}
        received<-err;return nil
    });err!=nil{t.Fatal(err)}
    invalid:=depthValue(65,false)
    if err:=bus.Emit(t.Context(),subject,&invalid);!errors.Is(err,event.ErrSnapshotDepth){t.Fatalf("NATS accepted deep native value: %v",err)}
    // A hostile publisher can bypass the typed writer; the consumer must reject it.
    if err:=bus.Emit(t.Context(),subject,depthWire(65,false));err!=nil{t.Fatal(err)}
    valid:=depthValue(64,false);if err:=bus.Emit(t.Context(),subject,&valid);err!=nil{t.Fatal(err)}
    for index:=range 2{select{
        case err:=<-received:if index==0&&!errors.Is(err,event.ErrSnapshotDepth)||index==1&&err!=nil{t.Fatalf("delivery %d: %v",index,err)}
        case <-time.After(3*time.Second):t.Fatal("NATS boundary result missing")
    }}
}
`
