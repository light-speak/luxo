package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestDecimalLeadingZeroLiteralEmitters(t *testing.T) {
	for _, sample := range []struct{ source, want string }{{"0", "0"}, {"000", "0"}, {"010", "10"}, {"008", "8"}, {"0009007199254740993", "9007199254740993"}, {"42", "42"}} {
		t.Run(sample.source, func(t *testing.T) {
			literal := &ast.Literal{Kind: token.Int, Value: sample.source}
			condition, ok := compileConditionLiteral(literal)
			if !ok {
				t.Fatal("integer condition refused")
			}
			stream, streamOK := compileStreamExpr(literal, nil, nil, "")
			if !streamOK {
				t.Fatal("integer stream condition refused")
			}
			for name, value := range map[string]string{
				"expression": newCompiler(nil).compileLiteral(literal),
				"default":    compileDefaultValue(literal, "int64", nil),
				"condition":  condition,
				"stream":     stream,
				"field":      compileFieldExpr(literal, "value"),
			} {
				if value != sample.want {
					t.Errorf("%s: %q, want %q", name, value, sample.want)
				}
			}
		})
	}
}

func TestNonIntegerLeadingZeroLiteralControls(t *testing.T) {
	for _, literal := range []*ast.Literal{{Kind: token.Float, Value: "00.25"}, {Kind: token.True, Value: "true"}} {
		expected := literal.Value
		if literal.Kind == token.Float {
			expected = "float64(" + literal.Value + ")"
		}
		for _, value := range []string{compileDefaultValue(literal, "", nil), compileFieldExpr(literal, "value")} {
			if value != expected {
				t.Errorf("non-integer literal changed: %q", value)
			}
		}
	}
}

func TestDecimalLeadingZeroDirectives(t *testing.T) {
	directive := &ast.Directive{Name: "rateLimit", Args: []*ast.NamedArg{
		{Name: "max", Value: &ast.Literal{Kind: token.Int, Value: "010"}},
		{Name: "window", Value: &ast.Literal{Kind: token.Int, Value: "010"}},
	}}
	var registration strings.Builder
	writeHandlerRegistration(&registration, "readValue", []*ast.Directive{directive})
	if !strings.Contains(registration.String(), "api.WithRateLimit(10, 10 * time.Second,") {
		t.Fatal(registration.String())
	}
	result := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{Name: "readValue", Directives: []*ast.Directive{directive}}}}}}
	limit := collectGatewayRateLimits(result)["readValue"]
	if limit.max != "10" || limit.window != "10 * time.Second" {
		t.Fatal(limit)
	}
	for _, sample := range []struct {
		kind, directive, want string
		args                  []string
	}{
		{"String", "minLength", "len(value) < 10", []string{"010"}},
		{"String", "maxLength", "len(value) > 10", []string{"010"}},
		{"Int", "range", "value < 10 || value > 20", []string{"010", "020"}},
	} {
		field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: sample.kind}}
		d := &ast.Directive{Name: sample.directive}
		for _, value := range sample.args {
			d.Args = append(d.Args, &ast.NamedArg{Value: &ast.Literal{Kind: token.Int, Value: value}})
		}
		field.Directives = []*ast.Directive{d}
		var output strings.Builder
		generateFieldValidation(&output, field, "value", "")
		if !strings.Contains(output.String(), sample.want) {
			t.Error(output.String())
		}
	}
}

func TestGeneratedDecimalLeadingZeroValues(t *testing.T) {
	runGeneratedFixture(t, decimalLeadingZeroSource, decimalLeadingZeroAssertions, numericLiteralIDs())
}

const decimalLeadingZeroSource = `
type Snapshot {
 amount: Int @transform { it + 010 }
 elapsed: Duration @transform { it + 010ms }
 secret: String? @visible(my.id == 010)
}
fn literalValue(): Int { return 010 }
fn zeroValue(): Int { return 000 }
fn durationValue(): Duration { return 010ms }
fn defaultValue(value: Int = 010): Int { return value }
fn defaultDuration(value: Duration = 010ms): Duration { return value }
fn usesDefault(): Int { return defaultValue() }
fn usesDurationDefault(): Duration { return defaultDuration() }
api literalDefault(value: Int = 010): Int { return value }
api guarded(): Int @auth(permission: { my.id == 010 }) { return 010 }
`

func numericLiteralIDs() StableIDs {
	return StableIDs{
		ModelFields: map[string]map[string]int{"Snapshot": {"amount": 1, "elapsed": 2, "secret": 3}},
		APIs:        map[string]int{"literalDefault": 1, "guarded": 2},
		APIParams:   map[string]map[string]int{"literalDefault": {"value": 1}},
	}
}

func TestDecimalLeadingZeroGeneratesCanonicalSource(t *testing.T) {
	canonical := strings.ReplaceAll(strings.ReplaceAll(decimalLeadingZeroSource, "010", "10"), "000", "0")
	var first *GenerateResult
	for _, source := range []string{canonical, decimalLeadingZeroSource} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: numericLiteralIDs()})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = output
			continue
		}
		if len(output.Files) != len(first.Files) {
			t.Fatal("literal spelling changed generated file set")
		}
		for name, expected := range first.Files {
			if string(output.Files[name]) != string(expected) {
				t.Errorf("literal spelling changed generated %s", name)
			}
		}
	}
}

func TestGeneratedDecimalLeadingNonOctalDigits(t *testing.T) {
	runGeneratedFixture(t, `fn eight(): Int { return 08 } fn nine(): Duration { return 009ms } fn largeValue(): Int { return 0009007199254740993 }`, `package generated
import("testing";"time")

func TestDecimalDigits(t *testing.T){app:=&App{};if value,err:=app.largeValue(t.Context());err!=nil||value!=9007199254740993{t.Fatal(value,err)};if value,err:=app.eight(t.Context());err!=nil||value!=8{t.Fatal(value,err)};if value,err:=app.nine(t.Context());err!=nil||value!=9*time.Millisecond{t.Fatal(value,err)}}
`, StableIDs{})
}

const decimalLeadingZeroAssertions = `package generated
import("bytes";"errors";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/auth";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")

func TestLiteralValues(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func()(int64,error);want int64}{
  {"literal",func()(int64,error){return app.literalValue(t.Context())},10},
  {"zero",func()(int64,error){return app.zeroValue(t.Context())},0},
  {"default",func()(int64,error){return app.usesDefault(t.Context())},10},
 }{value,err:=sample.call();if err!=nil||value!=sample.want{t.Errorf("%s: %d %v, want %d",sample.name,value,err,sample.want)}}
 for _,call:=range []func()(time.Duration,error){func()(time.Duration,error){return app.durationValue(t.Context())},func()(time.Duration,error){return app.usesDurationDefault(t.Context())}}{if value,err:=call();err!=nil||value!=10*time.Millisecond{t.Error(value,err)}}
}
func TestLiteralPermissionAndDefaults(t *testing.T){
 t.Setenv("JWT_SECRET","decimal-literal-test-only");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=luvia.New().Router;if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{raw string;allowed bool}{{"{\"id\":8}",false},{"{\"id\":10}",true}}{
  bearer,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}
  for _,name:=range []string{"literalDefault","guarded"}{want:=200;if name=="guarded"&&!sample.allowed{want=403}
   for _,binary:=range []bool{false,true}{body:=[]byte("{\"$api\":\""+name+"\"}");if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
    req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json");if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")};req.Header.Set("Authorization","Bearer "+bearer)
    response:=httptest.NewRecorder();endpoint.ServeHTTP(response,req);if response.Code!=want{t.Errorf("%s %s binary=%t status=%d want=%d",name,sample.raw,binary,response.Code,want)}
    if want==200{var data []byte;if binary{data=response.Body.Bytes();;decoder:=codec.NewDecoder(data);if value:=decoder.ReadInt();value!=10||decoder.Finish()!=nil{t.Error(value,decoder.Err())}}else if response.Body.String()!="{\"data\":10}"{t.Error(response.Body.String())}}
   }
   data,callErr:=client.CallWithMaskContext(t.Context(),bearer,router.Schema.APIs[name].ID,nil,[]byte{0})
   if want==200{if callErr!=nil||!bytes.Equal(data,codec.AppendSvarint(nil,10)){t.Error(name,data,callErr)}}else{var failure *luxerrors.AppError;if !errors.As(callErr,&failure)||failure.Code!=403{t.Error(name,callErr)}}
  }
  claims,err:=auth.Verify(cfg,bearer);if err!=nil{t.Fatal(err)};secret:="private";value:=&Snapshot{Amount:1,Elapsed:time.Nanosecond,Secret:&secret};mask,err:=codec.AppendSelectionMask(nil,[]byte{7},nil);if err!=nil{t.Fatal(err)}
  for name,write:=range map[string]func(*api.ResponseBuf)error{"row":func(buf *api.ResponseBuf)error{return value.WriteLuxo(buf,mask)},"values":func(buf *api.ResponseBuf)error{return WriteColumnarSnapshot(buf,[]Snapshot{*value},mask)}}{
   buf:=&api.ResponseBuf{Identity:claims};if err:=write(buf);err!=nil{t.Fatal(err)};var got *Snapshot
   if name=="row"{got=&Snapshot{};decoder:=codec.NewDecoder(buf.B);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{rows,err:=ReadColumnarSnapshot(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};got=&rows[0]}
   if got.Amount!=11||got.Elapsed!=10*time.Millisecond+time.Nanosecond||(got.Secret!=nil)!=sample.allowed{t.Errorf("%s %s: %+v",name,sample.raw,got)}
   if value.Amount!=1||value.Elapsed!=time.Nanosecond{t.Fatal("output mutated source")}
  }
 }
}
`
