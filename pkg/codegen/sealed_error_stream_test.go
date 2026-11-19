package codegen

import "testing"

func TestGeneratedSealedEventStreamIdentity(t *testing.T) {
	runGeneratedFixture(t, `type Profile { secret: String? @visible(my.id == 42) }
sealed Outcome { Value(profile: Profile) }
event Changed(payload: Outcome)
api watch(): Outcome @stream(Changed)
`, `package generated
import("bytes";"context";"fmt";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection")
type sealedStreamIdentityKey struct{}
func TestSealedStreamIdentity(t *testing.T){
 router:=api.NewRouter();router.IdentityExtractor=func(ctx context.Context)any{return ctx.Value(sealedStreamIdentityKey{})}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 bus:=event.NewChanBus(8);defer bus.Close();if err:=RegisterStreams(router,bus,nil);err!=nil{t.Fatal(err)}
 fields,err:=selection.Parse("Value{profile{secret}}");if err!=nil{t.Fatal(err)}
 mask,err:=schema.SelectToFieldMask(fields,router.Schema.SelectionModel("Outcome"),router.Schema);if err!=nil{t.Fatal(err)}
 var subscriptions []*api.InternalStream
 for _,binary:=range []bool{false,true}{for _,id:=range []int64{42,99}{
  cfg:=&auth.Config{Secret:"sealed-stream-test",Expires:time.Hour};token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":%d}",id),nil});if err!=nil{t.Fatal(err)}
  identity,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  ctx:=context.WithValue(t.Context(),sealedStreamIdentityKey{},identity)
  sub,err:=router.OpenInternalStream(ctx,&api.Request{API:"watch",BinaryMode:binary,FieldMask:mask});if err!=nil{t.Fatal(err)}
  defer sub.Close();subscriptions=append(subscriptions,sub)
 }}
 secret:="private-for-42";value:=(Outcome{}).NewVariant1(Profile{Secret:&secret})
 if err:=EmitChanged(t.Context(),bus,ChangedEvent{Payload:value});err!=nil{t.Fatal(err)}
 for index,sub:=range subscriptions{select{
  case raw,ok:=<-sub.Messages():if !ok||bytes.Contains(raw,[]byte(secret))!=(index%2==0){t.Fatalf("wrong identity payload %d %s error=%v",index,raw,sub.Err())}
  case<-time.After(3*time.Second):t.Fatal("missing sealed stream message")
 }}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Profile": {"secret": 1}, "Outcome": {"Value": 1}, "Outcome.Value": {"profile": 1}}, EventFields: map[string]map[string]int{"Changed": {"payload": 1}}})
}

func TestGeneratedSealedErrorData(t *testing.T) {
	runGeneratedFixture(t, `type Detail { value: Int }
model Entity @noTime { id: Int @id }
sealed Outcome { Value(value: Int) Next(child: Outcome?) Pending Detail(value: Detail) Model(value: Entity) Many(values: [Int], optional: [Int]?, raw: JSON) }
error Rejected(payload: Outcome, optional: Outcome?, history: [Outcome]) { code: 422 message: "rejected" }
`, `package generated
import("bytes";"encoding/json";"testing")
func TestSealedErrorData(t *testing.T){
 value:=(Outcome{}).NewVariant1(7)
 failure:=NewRejected(value,nil,[]Outcome{value})
 raw,err:=failure.Data.AppendJSON([]byte("prefix:"));if err!=nil{t.Fatal(err)}
 want:="prefix:{\"payload\":{\"Value\":{\"value\":7}},\"optional\":null,\"history\":[{\"Value\":{\"value\":7}}]}"
 if string(raw)!=want{t.Fatal(string(raw),want)}
 if raw,ok:=failure.Data.AppendText(nil,"payload");!ok||string(raw)!="{\"Value\":{\"value\":7}}"{t.Fatal(string(raw),ok)}
 for _,sample:=range []struct{value Outcome;want string}{
  {(Outcome{}).NewVariant3(),"{\"Pending\":{}}"},
  {(Outcome{}).NewVariant4(Detail{Value:8}),"{\"Detail\":{\"value\":{\"value\":8}}}"},
  {(Outcome{}).NewVariant5(&Entity{Id:42}),"{\"Model\":{\"value\":{\"id\":42}}}"},
  {(Outcome{}).NewVariant6(nil,nil,json.RawMessage("7")),"{\"Many\":{\"values\":[],\"optional\":null,\"raw\":7}}"},
 }{if raw,ok:=NewRejected(sample.value,nil,nil).Data.AppendText(nil,"payload");!ok||string(raw)!=sample.want{t.Fatal(string(raw),sample.want,ok)}}
 for range 31{child:=value;value=(Outcome{}).NewVariant2(&child)}
 if _,err:=NewRejected(value,nil,nil).Data.AppendJSON(nil);err!=nil{t.Fatal("64 objects",err)}
 child:=value;value=(Outcome{}).NewVariant2(&child)
 for _,bad:=range []Outcome{value,Outcome{},Outcome{_tag:1},(Outcome{}).NewVariant5(nil)}{
  prefix:=[]byte("prefix:");raw,err:=NewRejected(bad,nil,nil).Data.AppendJSON(prefix)
  if err==nil||!bytes.Equal(raw,prefix){t.Fatal("partial invalid sealed data",string(raw),err)}
 }
}
func BenchmarkSealedErrorData(b *testing.B){
 value:=(Outcome{}).NewVariant1(7);failure:=NewRejected(value,nil,[]Outcome{value});dst:=make([]byte,0,256)
 b.ReportAllocs();for b.Loop(){raw,err:=failure.Data.AppendJSON(dst[:0]);if err!=nil||len(raw)==0{b.Fatal(err)};dst=raw}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entity": {"id": 1}, "Detail": {"value": 1}, "Outcome": {"Value": 1, "Next": 2, "Pending": 3, "Detail": 4, "Model": 5, "Many": 6}, "Outcome.Value": {"value": 1}, "Outcome.Next": {"child": 1}, "Outcome.Detail": {"value": 1}, "Outcome.Model": {"value": 1}, "Outcome.Many": {"values": 1, "optional": 2, "raw": 3}}})
}
