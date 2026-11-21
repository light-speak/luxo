package codegen

import (
	"strings"
	"testing"
)

func TestGenerateRejectsOverflowingIntegralLiterals(t *testing.T) {
	for _, source := range []string{
		"fn read(): Int { return 9223372036854775808 }",
		"fn read(value: Int = -9223372036854775809): Int { return value }",
		"api read(): Duration { return 106752d }",
		"type Snapshot { value: Duration = 9223372036855ms }",
		"model Probe @noTime { id: Int @id value: Int @range(0, 9223372036854775808) }",
	} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || output != nil || !strings.Contains(err.Error(), "Int64") {
			t.Fatal("out-of-range integral literal emitted source", source, err)
		}
	}
}

func TestGeneratedIntegralLiteralBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
error Rejected(value: Int = -9223372036854775808) { code: 422 message: "rejected" }
type Snapshot { value: Int @transform { -9223372036854775808 } elapsed: Duration @transform { -106751d } }
fn minimum(): Int { return -9223372036854775808 }
fn maximum(): Int { return 9223372036854775807 }
fn defaultValue(value: Int = -9223372036854775808): Int { return value }
fn fromDefault(): Int { return defaultValue() }
fn rejected(): Int { throw Rejected() }
fn millis(): Duration { return 9223372036854ms }
fn seconds(): Duration { return 9223372036s }
fn minutes(): Duration { return 153722867m }
fn hours(): Duration { return 2562047h }
fn days(): Duration { return -106751d }
api minimumValue(): Int { return -0009223372036854775808 }
api maximumValue(): Int { return 9223372036854775807 }
api longestMillis(): Duration { return 9223372036854ms }
`, integralLiteralAssertions, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"value": 1, "elapsed": 2}}})
}

const integralLiteralAssertions = `package generated
import("bytes";"errors";"math";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestIntegralLiteralValues(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func()(int64,error);want int64}{
  {"minimum",func()(int64,error){return app.minimum(t.Context())},math.MinInt64},
  {"maximum",func()(int64,error){return app.maximum(t.Context())},math.MaxInt64},
  {"default",func()(int64,error){return app.fromDefault(t.Context())},math.MinInt64},
 }{got,err:=sample.call();if err!=nil||got!=sample.want{t.Error(sample.name,got,err)}}
 for _,sample:=range []struct{name string;call func()(time.Duration,error);want time.Duration}{
  {"millis",func()(time.Duration,error){return app.millis(t.Context())},9223372036854*time.Millisecond},
  {"seconds",func()(time.Duration,error){return app.seconds(t.Context())},9223372036*time.Second},
  {"minutes",func()(time.Duration,error){return app.minutes(t.Context())},153722867*time.Minute},
  {"hours",func()(time.Duration,error){return app.hours(t.Context())},2562047*time.Hour},
  {"days",func()(time.Duration,error){return app.days(t.Context())},-106751*24*time.Hour},
 }{got,err:=sample.call();if err!=nil||got!=sample.want{t.Error(sample.name,got,err)}}
 _,err:=app.rejected(t.Context());var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Data==nil{t.Fatal(err)}
 data,err:=failure.Data.AppendJSON(nil);if err!=nil||string(data)!="{\"value\":-9223372036854775808}"{t.Error(string(data),err)}
 original:=Snapshot{Value:42,Elapsed:time.Second};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return original.WriteLuxo(buf,mask)},"column":func(buf *api.ResponseBuf)error{return WriteColumnarSnapshot(buf,[]Snapshot{original},mask)}}{
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};var got Snapshot
  if name=="row"{decoder:=codec.NewDecoder(buf.B);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{rows,err:=ReadColumnarSnapshot(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};got=rows[0]}
  if got.Value!=math.MinInt64||got.Elapsed!=-106751*24*time.Hour||original.Value!=42||original.Elapsed!=time.Second{t.Fatal(name,got,original)}
 }
}
func TestIntegralLiteralWire(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name,decimal string;want int64}{{"minimumValue","-9223372036854775808",math.MinInt64},{"maximumValue","9223372036854775807",math.MaxInt64},{"longestMillis","9223372036854000000",9223372036854000000}}{
  for _,binary:=range []bool{false,true}{
   body:=[]byte("{\"$api\":\""+sample.name+"\"}");kind:="application/json";if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type",kind);if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(sample.name,response.Code,response.Body.String())}
   if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadInt();if decoder.Finish()!=nil||got!=sample.want{t.Error(sample.name,got,decoder.Err())}}else if response.Body.String()!="{\"data\":"+sample.decimal+"}"{t.Error(sample.name,response.Body.String())}
  }
  data,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs[sample.name].ID,nil,[]byte{0});if err!=nil{t.Fatal(err)};decoder:=codec.NewDecoder(data);got:=decoder.ReadInt();if decoder.Finish()!=nil||got!=sample.want{t.Error(sample.name,got,decoder.Err())}
 }
}
`
