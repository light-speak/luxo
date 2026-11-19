package codegen

import "testing"

const checkedRepeatSource = `
fn repeatValue(value: String, count: Int): String { return value.repeat(count) }
api repeatCall(value: String, count: Int): String { return repeatValue(value, count) }
fn nestedRepeat(value: String, count: Int): String { return value.repeat(count).trim() }
fn doubleRepeat(value: String, count: Int): String { return value.repeat(count).repeat(count) }
fn safeRepeat(value: String?, count: Int): String? { return value?.repeat(count) }
fn defaultRepeat(value: String?, count: Int): String { return value?.repeat(count) ?: "fallback" }
fn mapRepeat(values: [String], count: Int): [String] { return values.map { it.repeat(count) } }
fn guardedRepeat(flag: Boolean, value: String, count: Int): Boolean { return flag && value.repeat(count).isEmpty }
fn orRepeat(flag: Boolean, value: String, count: Int): Boolean { return flag || value.repeat(count).isEmpty }
fn whenRepeat(flag: Boolean, value: String, count: Int): String { return when {
 flag -> "skipped"
 else -> value.repeat(count)
 } }
fn templateRepeat(value: String, count: Int): String { return "prefix:${value.repeat(count)}" }
fn collidingRepeat(_stringRepeat1: String, _stringRepeat1Err: Int): String { return _stringRepeat1.repeat(_stringRepeat1Err) }
fn repeatSource(fail: Boolean): Result<String> @native
fn repeatCount(fail: Boolean): Result<Int> @native
fn effectRepeat(failSource: Boolean, failCount: Boolean): String { return (repeatSource(failSource)?).repeat(repeatCount(failCount)?) }
`

func TestGeneratedCheckedStringRepeat(t *testing.T) {
	runGeneratedFixture(t, checkedRepeatSource, checkedRepeatAssertions, StableIDs{
		APIs:      map[string]int{"repeatCall": 51},
		APIParams: map[string]map[string]int{"repeatCall": {"value": 1, "count": 2}},
	})
}

const checkedRepeatAssertions = `package generated
import("bytes";"context";"errors";"encoding/json";"math";"net/http/httptest";"slices";"strconv";"strings";"testing";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
var repeatTrace []string
var repeatSourceError=errors.New("source failure")
var repeatCountError=errors.New("count failure")
type checkedRepeatResolver struct{}
func(resolver *checkedRepeatResolver) RepeatSource(ctx context.Context, fail bool)(string,error){repeatTrace=append(repeatTrace,"source");if fail{return "",repeatSourceError};return "ab",nil}
func(resolver *checkedRepeatResolver) RepeatCount(ctx context.Context, fail bool)(int64,error){repeatTrace=append(repeatTrace,"count");if fail{return 0,repeatCountError};return 2,nil}
func TestRepeatHTTP(t *testing.T){
 app:=&App{Resolver:&checkedRepeatResolver{}};router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{value string;count int64;invalid bool}{
  {"ab",-1,true},{"",-1,true},{"ab",math.MinInt64,true},{"ab",math.MaxInt64,true},
  {"abc",math.MaxInt64/3+1,true},{"",math.MaxInt64,false},{"中",2,false},{"ab",0,false},{"ab",1,false},
 }{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"repeatCall\",\"value\":"+strconv.Quote(sample.value)+",\"count\":"+strconv.FormatInt(sample.count,10)+"}")
  if binary{
   encoder:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,51),0))
   if err:=encoder.WriteFieldString(1,sample.value);err!=nil{t.Fatal(err)};encoder.WriteFieldInt(2,sample.count);encoder.WriteEnd();body=encoder.Bytes()
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if sample.invalid{if response.Code!=400{t.Fatal(binary,sample.value,sample.count,response.Code,response.Body.String())};continue}
  if response.Code!=200{t.Fatal(binary,response.Code,response.Body.String())}
  want:=strings.Repeat(sample.value,int(sample.count))
  if binary{
   expected,err:=codec.AppendString(nil,want);if err!=nil{t.Fatal(err)}
   if !bytes.Equal(response.Body.Bytes(),expected){t.Fatal(binary,response.Body.Bytes(),expected)}
  }else{
   var envelope struct{Data string};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||envelope.Data!=want{t.Fatal(envelope.Data,want,err)}
  }
 }}
}
func TestRepeatErrorPropagation(t *testing.T){
 app:=&App{Resolver:&checkedRepeatResolver{}};value:="ab"
 if got,err:=app.safeRepeat(t.Context(),nil,-1);got!=nil||err!=nil{t.Fatal(got,err)}
 if got,err:=app.safeRepeat(t.Context(),&value,2);got==nil||*got!="abab"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.defaultRepeat(t.Context(),nil,-1);got!="fallback"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.defaultRepeat(t.Context(),&value,-1);got!=""||err==nil{t.Fatal(got,err)}
 if got,err:=app.safeRepeat(t.Context(),&value,-1);got!=nil||err==nil{t.Fatal(got,err)}
 if got,err:=app.nestedRepeat(t.Context(),value,-1);got!=""||err==nil{t.Fatal(got,err)}
 if got,err:=app.templateRepeat(t.Context(),value,-1);got!=""||err==nil{t.Fatal(got,err)}
 if got,err:=app.mapRepeat(t.Context(),[]string{},-1);len(got)!=0||err!=nil{t.Fatal(got,err)}
 if got,err:=app.mapRepeat(t.Context(),[]string{value},-1);got!=nil||err==nil{t.Fatal(got,err)}
 if got,err:=app.mapRepeat(t.Context(),[]string{"ab","cd"},2);!slices.Equal(got,[]string{"abab","cdcd"})||err!=nil{t.Fatal(got,err)}
 if got,err:=app.doubleRepeat(t.Context(),value,2);got!="abababab"||err!=nil{t.Fatal(got,err)}
 if got,err:=app.collidingRepeat(t.Context(),value,2);got!="abab"||err!=nil{t.Fatal(got,err)}
 for _,flag:=range []bool{false,true}{
  if got,err:=app.guardedRepeat(t.Context(),flag,value,-1);got||((err!=nil)!=flag){t.Fatal(flag,got,err)}
  if got,err:=app.orRepeat(t.Context(),flag,value,-1);got!=flag||((err!=nil)==flag){t.Fatal(flag,got,err)}
  got,err:=app.whenRepeat(t.Context(),flag,value,-1);if flag{if got!="skipped"||err!=nil{t.Fatal(got,err)}}else if got!=""||err==nil{t.Fatal(got,err)}
 }
 for _,sample:=range []struct{sourceFailure,countFailure bool;want []string;err error}{
  {false,false,[]string{"source","count"},nil},{true,false,[]string{"source"},repeatSourceError},{false,true,[]string{"source","count"},repeatCountError},
 }{
  repeatTrace=nil;got,err:=app.effectRepeat(t.Context(),sample.sourceFailure,sample.countFailure)
  if !errors.Is(err,sample.err)||!slices.Equal(repeatTrace,sample.want){t.Fatal(got,err,repeatTrace,sample.want)}
  if sample.err==nil&&got!="abab"{t.Fatal(got)}
 }
}
var repeatBenchSink string
var repeatBenchError error
func BenchmarkRepeatedGenerated(b *testing.B){
 app:=&App{Resolver:&checkedRepeatResolver{}}
 for _,sample:=range []struct{name,value string;count int64}{
  {"zero","ab",0},{"one","ab",1},{"short","ab",2},{"medium","ab",128},
  {"unicode","中",4},{"4KiB","ab",2048},
 }{b.Run(sample.name,func(b *testing.B){got,err:=app.repeatValue(b.Context(),sample.value,sample.count);if err!=nil||got!=strings.Repeat(sample.value,int(sample.count)){b.Fatal(got,err)};b.ReportAllocs();b.ResetTimer();for range b.N{repeatBenchSink,repeatBenchError=app.repeatValue(b.Context(),sample.value,sample.count)}})}
}

func BenchmarkRepeatedGeneratedErrors(b *testing.B){
 app:=&App{Resolver:&checkedRepeatResolver{}}
 for _,sample:=range []struct{name,value string;count int64}{
  {"negative","ab",-1},{"signed-overflow","ab",math.MaxInt64},{"word-overflow","abc",math.MaxInt64},
 }{b.Run(sample.name,func(b *testing.B){if _,err:=app.repeatValue(b.Context(),sample.value,sample.count);err==nil{b.Fatal("invalid repeat succeeded")};b.ReportAllocs();b.ResetTimer();for range b.N{repeatBenchSink,repeatBenchError=app.repeatValue(b.Context(),sample.value,sample.count)}})}
}
`
