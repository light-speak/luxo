package codegen

import "testing"

func TestGeneratedSealedEventSnapshots(t *testing.T) {
	runGeneratedFixture(t, `type Box<T> { value: T }
sealed Outcome { Value(value: Int, data: Bytes) Loop(child: Outcome?) Boxed(value: Box<Outcome>) Pending }
event Changed(payload: Outcome)
on Changed @native
`, sealedEventAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Box": {"value": 1}, "Outcome": {"Value": 1, "Loop": 2, "Boxed": 3, "Pending": 4},
		"Outcome.Value": {"value": 1, "data": 2}, "Outcome.Loop": {"child": 1}, "Outcome.Boxed": {"value": 1},
	}, EventFields: map[string]map[string]int{"Changed": {"payload": 1}}})
}

const sealedEventAssertions = `package generated
import("bytes";"context";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestSealedEventSnapshotAndWire(t *testing.T){
 source:=ChangedEvent{Payload:(Outcome{}).NewVariant1(7,[]byte{1,2})}
 wire,err:=source.MarshalLuxo();if err!=nil{t.Fatal(err)};var decoded ChangedEvent;if err:=decoded.UnmarshalLuxo(wire);err!=nil{t.Fatal(err)}
 if decoded.Payload.LuxoVariantTag()!=1||decoded.Payload.AsVariant1().F1!=7||!bytes.Equal(decoded.Payload.AsVariant1().F2,[]byte{1,2}){t.Fatal("wire lost the active payload",wire,decoded)}
 if err:=decoded.UnmarshalLuxo([]byte{1,4,99,2,0,0,0});err==nil{t.Fatal("unknown wire variant accepted")}
 bus:=event.NewChanBus(1);defer bus.Close();gate:=make(chan struct{});var first,second,native ChangedEvent
 if err:=event.OnDecode(bus,"Changed",UnmarshalChanged,func(_ context.Context,value ChangedEvent)error{<-gate;first=value;value.Payload.AsVariant1().F2[0]=99;return nil});err!=nil{t.Fatal(err)}
 if err:=event.OnDecode(bus,"Changed",UnmarshalChanged,func(_ context.Context,value ChangedEvent)error{second=value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventHandlers:NativeEventHandlers{OnChanged:func(_ context.Context,value ChangedEvent)error{native=value;return nil}}};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=EmitChanged(t.Context(),bus,source);err!=nil{t.Fatal(err)};source.Payload.AsVariant1().F2[0]=8;close(gate);if err:=bus.Close();err!=nil{t.Fatal(err)}
 if first.Payload.AsVariant1().F2[0]!=99||second.Payload.AsVariant1().F2[0]!=1||native.Payload.AsVariant1().F2[0]!=1{t.Fatal("shared mutable payload",first,second,native)}
 if _,err:=(&ChangedEvent{Payload:Outcome{}}).SnapshotEvent();!errors.Is(err,event.ErrInvalidSnapshot){t.Fatal("zero holder snapshot",err)}
 loop:=(Outcome{}).NewVariant2(nil);loop.AsVariant2().F1=&loop;if _,err:=(&ChangedEvent{Payload:loop}).SnapshotEvent();!errors.Is(err,event.ErrSnapshotDepth){t.Fatal("cyclic native snapshot",err)}
 var boxed Outcome;if err:=boxed.ReadJSONInput([]byte("{\"Boxed\":{\"value\":{\"value\":{\"Value\":{\"value\":9,\"data\":\"AQI=\"}}}}}"));err!=nil{t.Fatal(err)}
 nested:=ChangedEvent{Payload:boxed};raw,err:=nested.MarshalLuxo();if err!=nil{t.Fatal(err)};var restored ChangedEvent;if err:=restored.UnmarshalLuxo(raw);err!=nil{t.Fatal("generic nested wire",raw,err)}
 if restored.Payload.AsVariant3().F1.Value.AsVariant1().F1!=9{t.Fatal("generic sealed payload lost")}
 wireBus:=event.NewChanBus(1);defer wireBus.Close();var delivered ChangedEvent
 wireApp:=&App{EventHandlers:NativeEventHandlers{OnChanged:func(_ context.Context,value ChangedEvent)error{delivered=value;return nil}}};if err:=RegisterEvents(wireBus,wireApp);err!=nil{t.Fatal(err)}
 if err:=wireBus.Emit(t.Context(),"Changed",raw);err!=nil{t.Fatal(err)};if err:=wireBus.Close();err!=nil{t.Fatal(err)};if delivered.Payload.LuxoVariantTag()!=3{t.Fatal("native wire decoder lost branch")}
}
var sealedSnapshotSink event.Snapshot
func BenchmarkSealedTypedSnapshot(b *testing.B){
 value:=ChangedEvent{Payload:(Outcome{}).NewVariant1(7,[]byte{1,2})};b.ReportAllocs();b.ResetTimer()
 for range b.N{snapshot,err:=value.SnapshotEvent();if err!=nil{b.Fatal(err)};sealedSnapshotSink=snapshot}
}
func BenchmarkSealedEventWire(b *testing.B){
 value:=ChangedEvent{Payload:(Outcome{}).NewVariant1(7,[]byte{1,2})};wire,err:=value.MarshalLuxo();if err!=nil{b.Fatal(err)}
 b.Run("encode",func(b *testing.B){b.ReportAllocs();for range b.N{if _,err:=value.MarshalLuxo();err!=nil{b.Fatal(err)}}})
 b.Run("decode",func(b *testing.B){b.ReportAllocs();for range b.N{var decoded ChangedEvent;if err:=decoded.UnmarshalLuxo(wire);err!=nil{b.Fatal(err)}}})
}
`
