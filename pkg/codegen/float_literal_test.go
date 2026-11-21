package codegen

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestGenerateRejectsOverflowingFloatLiteral(t *testing.T) {
	value := strings.Repeat("9", 309) + ".0"
	for _, source := range []string{
		"fn read(): Float { return " + value + " }",
		"type Snapshot { value: Float = -" + value + " }",
		"model Snapshot @noTime { id: Int @id value: Float @range(0.0, " + value + ") }",
	} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || output != nil || !strings.Contains(err.Error(), "finite Float64") {
			t.Fatalf("overflowing Float emitted source: %v", err)
		}
	}
}

func TestGeneratedFiniteFloatLiteralBoundaries(t *testing.T) {
	maximum := strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64) + ".0"
	source := "fn maximum(): Float { return " + maximum + " }\n" +
		"fn subnormal(): Float { return 0." + strings.Repeat("0", 323) + "5 }\n" +
		"fn underflow(): Float { return -0." + strings.Repeat("0", 324) + "1 }\n" +
		"fn shadowed(float64: Float): Float { return float64 + 0.1 }"
	runGeneratedFixture(t, source, `package generated
import("context";"math";"testing")

func TestFiniteFloatBounds(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func(context.Context)(float64,error);want float64}{{"maximum",app.maximum,math.MaxFloat64},{"subnormal",app.subnormal,math.SmallestNonzeroFloat64},{"underflow",app.underflow,math.Copysign(0,-1)}}{
  got,err:=sample.call(t.Context());if err!=nil||math.Float64bits(got)!=math.Float64bits(sample.want){t.Errorf("%s got=%016x want=%016x error=%v",sample.name,math.Float64bits(got),math.Float64bits(sample.want),err)}
 }
 left,right:=0.2,0.1;if got,err:=app.shadowed(t.Context(),left);err!=nil||math.Float64bits(got)!=math.Float64bits(left+right){t.Error("shadowed Float64 conversion",got,err)}
}
`, StableIDs{})
}

func TestGeneratedNegativeFloatZeroLiterals(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { value: Float @transform { -0.0 } }
error Rejected(value: Float = -0.0) { code: 422 message: "rejected" }
fn literalSum(): Float { return 0.1 + 0.2 }
fn chainedSum(): Float { return 10000000000000000.0 + 1.0 - 10000000000000000.0 }
fn literalZero(): Float { return -0.0 }
fn longZero(): Float { return -00.0000 }
fn computedZero(): Float { return -(0.1 - 0.1) }
fn variableZero(value: Float): Float { return -value }
fn namedDefault(math: Float = -0.0): Float { return math }
fn callNamedDefault(): Float { return namedDefault() }
fn doubleNegative(): Float { return --0.0 }
fn defaultZero(value: Float = -0.0): Float { return value }
fn callDefault(): Float { return defaultZero() }
fn reject(): Float { throw Rejected() }
api literalValue(): Float { return -0.0 }
api defaultValue(value: Float = -0.0): Float { return value }
`, negativeFloatZeroAssertions, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"value": 1}}})
}

const negativeFloatZeroAssertions = `package generated
import("bytes";"errors";"math";"net";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestLiteralZeroBits(t *testing.T){
 app:=&App{}
 left,right:=0.1,0.2;if got,err:=app.literalSum(t.Context());err!=nil||math.Float64bits(got)!=math.Float64bits(left+right){t.Errorf("literal sum bits=%016x expected=%016x error=%v",math.Float64bits(got),math.Float64bits(left+right),err)}
 big,one:=10000000000000000.0,1.0;if got,err:=app.chainedSum(t.Context());err!=nil||math.Float64bits(got)!=math.Float64bits(big+one-big){t.Error("chained sum",got,err)}
 for name,call:=range map[string]func()(float64,error){"literal":func()(float64,error){return app.literalZero(t.Context())},"long":func()(float64,error){return app.longZero(t.Context())},"computed":func()(float64,error){return app.computedZero(t.Context())},"variable":func()(float64,error){return app.variableZero(t.Context(),0)},"reserved_name":func()(float64,error){return app.callNamedDefault(t.Context())},"default":func()(float64,error){return app.callDefault(t.Context())}}{
  got,err:=call();if err!=nil||math.Float64bits(got)!=uint64(1)<<63{t.Errorf("%s bits=%016x error=%v",name,math.Float64bits(got),err)}
 }
 if got,err:=app.doubleNegative(t.Context());err!=nil||math.Float64bits(got)!=0{t.Error(got,err)}
 _,err:=app.reject(t.Context());var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Data==nil{t.Fatal(err)};data,err:=failure.Data.AppendJSON(nil);if err!=nil||string(data)!="{\"value\":-0}"{t.Error(string(data),err)}
 value:=Snapshot{Value:1};mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
 for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return value.WriteLuxo(buf,mask)},"column":func(buf *api.ResponseBuf)error{return WriteColumnarSnapshot(buf,[]Snapshot{value},mask)}}{
  buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};var got Snapshot
  if name=="row"{decoder:=codec.NewDecoder(buf.B);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{rows,err:=ReadColumnarSnapshot(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};got=rows[0]}
  if math.Float64bits(got.Value)!=uint64(1)<<63||value.Value!=1{t.Errorf("%s lost negative zero or mutated source: %016x",name,math.Float64bits(got.Value))}
 }
}
func TestNegativeZeroWire(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,name:=range []string{"literalValue","defaultValue"}{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+name+"\"}");kind:="application/json";if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)};kind="application/x-luxo"}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type",kind);if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadFloat();if decoder.Finish()!=nil||math.Float64bits(got)!=uint64(1)<<63{t.Error(name,"HTTP bits",math.Float64bits(got),decoder.Err())}}else if response.Body.String()!="{\"data\":-0}"{t.Error(name,response.Body.String())}
 }
  data,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs[name].ID,nil,[]byte{0});if err!=nil{t.Fatal(err)};decoder:=codec.NewDecoder(data);got:=decoder.ReadFloat();if decoder.Finish()!=nil||math.Float64bits(got)!=uint64(1)<<63{t.Error(name,"LRPC bits",math.Float64bits(got),decoder.Err())}
 }
}
`

func TestGeneratedNegativeFloatZeroStreamDefaults(t *testing.T) {
	runGeneratedFixture(t, `api watch(value: Float = -0.0, optional: Float? = -0.0): Float @stream @native`, `package generated
import("encoding/json";"math";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestStreamDefaultZeroBits(t *testing.T){
 for _,binary:=range []bool{false,true}{req:=&api.Request{Params:map[string]json.RawMessage{}};if binary{req.Params=nil;req.SetBinaryParams([]string{"value","optional"},2)}
  var snapshot WatchStreamParams;if err:=snapshot.readRequest(req);err!=nil{t.Fatal(err)}
  if math.Float64bits(snapshot.Value)!=uint64(1)<<63||snapshot.Optional==nil||math.Float64bits(*snapshot.Optional)!=uint64(1)<<63{t.Error(binary,snapshot)}
 }
}

`, StableIDs{})
}

func TestGeneratedQuotedTransformPackageNames(t *testing.T) {
	runGeneratedFixture(t, `type Text { value: String @transform { "strings.ToUpper str.Capitalize math.Copysign" } }`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestQuotedPackages(t *testing.T){value:=Text{Value:"source"};mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)};buf:=&api.ResponseBuf{};if err:=value.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)};var got Text;decoder:=codec.NewDecoder(buf.B);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)};if got.Value!="strings.ToUpper str.Capitalize math.Copysign"||value.Value!="source"{t.Fatal(got,value)}}
`, StableIDs{ModelFields: map[string]map[string]int{"Text": {"value": 1}}})
}

func TestGeneratedFloatLiteralIdentityComparisons(t *testing.T) {
	runGeneratedFixture(t, `
model Principal @noTime @withAuth(stores: [id, score]) { id: Int @id score: Float }
type Visible { value: Float? @visible(my.score == 0.1 + 0.2 && -0.0 < 1.0 && 1ms == 1ms) at: DateTime }
api guarded(): Float @auth(permission: { my.score == 0.1 + 0.2 }) { return 0.1 + 0.2 }
`, floatLiteralIdentityAssertions, StableIDs{ModelFields: map[string]map[string]int{"Principal": {"id": 1, "score": 2}, "Visible": {"at": 1, "value": 2}}})
}

const floatLiteralIdentityAssertions = `package generated
import("bytes";"errors";"math";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
func TestRoundedLiteralIdentity(t *testing.T){
 t.Setenv("JWT_SECRET","float-literal-test-only");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close();left,right:=0.1,0.2;expected:=math.Float64bits(left+right)
 for _,sample:=range []struct{raw string;allowed bool}{{"{\"id\":42,\"score\":0.3}",false},{"{\"id\":42,\"score\":0.30000000000000004}",true}}{
  token,err:=auth.SignForSubject(cfg,"Principal",func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{body:=[]byte("{\"$api\":\"guarded\"}");if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["guarded"].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")};request.Header.Set("Authorization","Bearer "+token)
   response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request);want:=403;if sample.allowed{want=200};if response.Code!=want{t.Fatalf("%s binary=%t got=%d want=%d %s",sample.raw,binary,response.Code,want,response.Body)}
   if sample.allowed{if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadFloat();if decoder.Finish()!=nil||math.Float64bits(got)!=expected{t.Fatal(got,decoder.Err())}}else if response.Body.String()!="{\"data\":0.30000000000000004}"{t.Fatal(response.Body.String())}}
  }
  data,err:=client.CallWithMaskContext(t.Context(),token,router.Schema.APIs["guarded"].ID,nil,[]byte{0});if sample.allowed{if err!=nil{t.Fatal(err)};decoder:=codec.NewDecoder(data);got:=decoder.ReadFloat();if decoder.Finish()!=nil||math.Float64bits(got)!=expected{t.Fatal(got,decoder.Err())}}else{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=403{t.Fatal(err)}}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)};number:=2.0;value:=Visible{At:time.Date(2026,1,1,0,0,0,0,time.UTC),Value:&number};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
  for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return value.WriteLuxo(buf,mask)},"column":func(buf *api.ResponseBuf)error{return WriteColumnarVisible(buf,[]Visible{value},mask)}}{
   buf:=&api.ResponseBuf{Identity:claims};if err:=write(buf);err!=nil{t.Fatal(err)};var got Visible;if name=="row"{decoder:=codec.NewDecoder(buf.B);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{rows,err:=ReadColumnarVisible(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};got=rows[0]};if (got.Value!=nil)!=sample.allowed{t.Error(name,sample.raw,got)}
  }
 }
}
`
