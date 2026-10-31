package codegen

// The reference retains the old successful ChanBus path: raw payload enqueue,
// the same locks/channel/handler lookup and no validation or ownership copy.
// It is intentionally unsafe and exists only as a benchmark comparison.
const eventSnapshotBenchmarks = `
type legacySnapshotBus struct {
    mu sync.RWMutex
    subs map[string][]event.Handler
    channels map[string]chan any
    done chan struct{}
    once sync.Once
    wg sync.WaitGroup
}
func newLegacySnapshotBus()*legacySnapshotBus{return &legacySnapshotBus{subs:make(map[string][]event.Handler),channels:make(map[string]chan any),done:make(chan struct{})}}
func(b *legacySnapshotBus)Emit(_ context.Context,name string,payload any)error{
    b.mu.RLock()
    select{case <-b.done:b.mu.RUnlock();return event.ErrBusClosed;default:}
    ch,ok:=b.channels[name];if !ok{b.mu.RUnlock();return nil}
    select{case ch<-payload:b.mu.RUnlock();return nil;default:b.mu.RUnlock();return event.ErrBufferFull}
}
func(b *legacySnapshotBus)On(name string,handler event.Handler)error{
    b.mu.Lock();defer b.mu.Unlock()
    select{case <-b.done:return event.ErrBusClosed;default:}
    b.subs[name]=append(b.subs[name],handler)
    if _,ok:=b.channels[name];!ok{
        ch:=make(chan any,256);b.channels[name]=ch;b.wg.Add(1);go b.dispatch(name,ch)
    };return nil
}
func(b *legacySnapshotBus)OnQueue(name,_ string,handler event.Handler)error{return b.On(name,handler)}
func(b *legacySnapshotBus)dispatch(name string,ch <-chan any){
    defer b.wg.Done()
    for payload:=range ch{
        b.mu.RLock();handlers:=b.subs[name];b.mu.RUnlock()
        for _,h:=range handlers{if err:=legacySnapshotCall(h,payload);err!=nil{fmt.Fprintf(os.Stderr,"event %s handler error: %v\n",name,err)}}
    }
}
func legacySnapshotCall(h event.Handler,payload any)(err error){
    defer func(){if value:=recover();value!=nil{err=fmt.Errorf("event handler panic: %v",value)}}()
    return h(context.Background(),payload)
}
func(b *legacySnapshotBus)Close(){b.once.Do(func(){b.mu.Lock();close(b.done);for _,ch:=range b.channels{close(ch)};b.mu.Unlock();b.wg.Wait()})}

func BenchmarkEventSnapshotDispatch(b *testing.B){
    for _,count:=range []int{1,128}{for _,listeners:=range []int{1,4}{for _,owned:=range []bool{false,true}{
        b.Run(fmt.Sprintf("items=%d/listeners=%d/owned=%t",count,listeners,owned),func(b *testing.B){
            value:=eventFixture();value.Names=make([]string,count);for i:=range value.Names{value.Names[i]="snapshot"}
            var bus event.Bus;if owned{bus=event.NewChanBus(256)}else{bus=newLegacySnapshotBus()};defer bus.Close()
            ack:=make(chan struct{},1)
            for index:=range listeners{if err:=event.OnDecode(bus,"Probe",UnmarshalProbe,func(_ context.Context,p ProbeEvent)error{
                if len(p.Names)!=count||p.Child.Detail.Label!="detail"{panic("invalid snapshot")}
                if index==listeners-1{ack<-struct{}{}};return nil
            });err!=nil{b.Fatal(err)}}
            b.ReportAllocs();b.ResetTimer()
            for b.Loop(){
                var err error;if owned{err=EmitProbe(context.Background(),bus,value)}else{err=bus.Emit(context.Background(),"Probe",value)}
                if err!=nil{b.Fatal(err)};<-ack
            }
        })
    }}}
}

func BenchmarkEventSnapshotScalar(b *testing.B){
    for _,listeners:=range []int{1,4}{for _,owned:=range []bool{false,true}{
        b.Run(fmt.Sprintf("listeners=%d/owned=%t",listeners,owned),func(b *testing.B){
            value:=ScalarEvent{Id:42,Name:"snapshot"}
            var bus event.Bus;if owned{bus=event.NewChanBus(256)}else{bus=newLegacySnapshotBus()};defer bus.Close()
            ack:=make(chan struct{},1)
            for index:=range listeners{if err:=event.OnDecode(bus,"Scalar",UnmarshalScalar,func(_ context.Context,p ScalarEvent)error{
                if p!=value{panic("invalid snapshot")};if index==listeners-1{ack<-struct{}{}};return nil
            });err!=nil{b.Fatal(err)}}
            b.ReportAllocs();b.ResetTimer()
            for b.Loop(){
                var err error;if owned{err=EmitScalar(context.Background(),bus,value)}else{err=bus.Emit(context.Background(),"Scalar",value)}
                if err!=nil{b.Fatal(err)};<-ack
            }
        })
    }}
}
`
