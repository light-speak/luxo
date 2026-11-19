package codegen

import "testing"

func TestGeneratedNullableCollectionPromotions(t *testing.T) {
	runGeneratedFixture(t, `type Box { values: [Int]? }
fn isNull(values: [Int]?): Boolean { return values == null }
fn inspectNull(values: [Int]?): Result<Boolean> @native
fn requiredValues(): Result<[Int]> @native
fn optionalValues(): Result<[Int]?> @native
fn liftedCall(values: [Int]): Boolean { return isNull(values.filter { it < 0 }) }
fn liftedNativeCall(values: [Int]): Boolean { return inspectNull(values.filter { it < 0 })? }
fn liftedVariable(values: [Int]): [Int]? {
 var out: [Int]? = null
 out = values.filter { it < 0 }
 return out
}
fn liftedObject(values: [Int]): [Int]? {
 val box = Box { values: values.filter { it < 0 } }
 return box.values
}
fn safeNonNull(values: [Int]): [Int]? { return values.filter { it < 0 }?.map { it + 1 } }
fn safeNullable(values: [Int]?): [Int]? { return values?.filter { it < 0 } }
api fromFilter(values: [Int]): [Int]? { return values.filter { it < 0 } }
api fromVariable(values: [Int]): [Int]? { return liftedVariable(values) }
api fromObject(values: [Int]): [Int]? { return liftedObject(values) }
api fromSafe(values: [Int]?): [Int]? { return safeNullable(values) }
api fromRequired(): [Int]? { return requiredValues()? }
api fromOptional(): [Int]? { return optionalValues()? }
`, `package generated
import("bytes";"context";"encoding/json";"net/http/httptest";"slices";"testing";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
type promotionResolver struct{}
func(*promotionResolver)InspectNull(_ context.Context,values []int64)(bool,error){return values==nil,nil}
func(*promotionResolver)RequiredValues(context.Context)([]int64,error){return nil,nil}
func(*promotionResolver)OptionalValues(context.Context)([]int64,error){return nil,nil}
func TestCollectionPromotions(t *testing.T){
 app:=&App{Resolver:&promotionResolver{}}
 for _,input:=range [][]int64{nil,{}, {1,2,3}}{
  if got,err:=app.liftedCall(t.Context(),input);got||err!=nil{t.Fatal("ordinary argument became null",got,err)}
  if got,err:=app.liftedNativeCall(t.Context(),input);got||err!=nil{t.Fatal("native argument became null",got,err)}
  for _,call:=range []func(context.Context,[]int64)([]int64,error){app.liftedVariable,app.liftedObject,app.safeNonNull}{
   got,err:=call(t.Context(),input);if got==nil||len(got)!=0||err!=nil{t.Fatal("present empty became null",got,err)}
  }
 }
 for _,input:=range [][]int64{nil,{}, {1,2,3}}{
  got,err:=app.safeNullable(t.Context(),input)
  if err!=nil||(got==nil)!=(input==nil)||len(got)!=0{t.Fatal("safe nullable distinction",input,got,err)}
 }
}
func TestPromotionHTTP(t *testing.T){
 app:=&App{Resolver:&promotionResolver{}};router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{api,input string;id uint64;null bool}{
  {"fromFilter","[1,2]",51,false},{"fromVariable","[1,2]",52,false},{"fromObject","[1,2]",53,false},
  {"fromSafe","null",54,true},{"fromSafe","[]",54,false},{"fromSafe","[1,2]",54,false},
  {"fromRequired","",55,false},{"fromOptional","",56,true},
 }{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+sample.api+"\"");if sample.input!=""{body=append(body,[]byte(",\"values\":"+sample.input)...)};body=append(body,'}')
  if binary{
   body=codec.AppendVarint(codec.AppendVarint(nil,sample.id),0)
   if sample.input!=""{
    body=codec.AppendVarint(body,1)
    if sample.input=="null"{body=codec.AppendNull(body)}else{
     if sample.api=="fromSafe"{body=codec.AppendPresent(body)}
     values:=[]int64{};if sample.input=="[1,2]"{values=[]int64{1,2}}
     body=codec.AppendVarint(body, uint64(len(values)));for _,value:=range values{body=codec.AppendSvarint(body,value)}
    }
   };body=codec.AppendVarint(body,0)
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(sample.api,binary,response.Code,response.Body.String())}
  if binary{
   expected:=codec.AppendNull(nil);if !sample.null{expected=codec.AppendVarint(codec.AppendPresent(nil),0)}
   if !bytes.Equal(response.Body.Bytes(),expected){t.Fatal(sample.api,response.Body.Bytes(),expected)}
  }else{
   var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)}
   expected:=[]byte("[]");if sample.null{expected=[]byte("null")};if !slices.Equal(envelope.Data,expected){t.Fatal(sample.api,string(envelope.Data),string(expected))}
  }
 }}
}
`, StableIDs{
		ModelFields: map[string]map[string]int{"Box": {"values": 1}},
		APIs:        map[string]int{"fromFilter": 51, "fromVariable": 52, "fromObject": 53, "fromSafe": 54, "fromRequired": 55, "fromOptional": 56},
		APIParams:   map[string]map[string]int{"fromFilter": {"values": 1}, "fromVariable": {"values": 1}, "fromObject": {"values": 1}, "fromSafe": {"values": 1}},
	})
}
