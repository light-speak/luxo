package codegen

import (
	"context"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestFormalControlFlowGeneratesConcreteTypes(t *testing.T) {
	const source = `
fn choose(flag: Boolean): [Int] {
 val values = when {
  flag -> [1, 2]
  else -> [3]
 }
 return values
}

fn mapped(items: [Int]): [Int] {
 return for item in items { item + 1 }
}
fn collected(items: [Int]): [Int] {
 return items.map { value -> value + 1 }.filter { it > 0 }
}
fn parallel(flag: Boolean): [Int] {
 val (left, right) = await { choose(flag) choose(false) }
 return collected(left)
}
api values(flag: Boolean): [Int] { return mapped(choose(flag)) }
`
	tokens, lexErrors := lexer.New(source, "static.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("static.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	ids := completeFixtureAPIIDs(result, StableIDs{APIs: map[string]int{"values": 1}})
	generated, err := Generate(result, "generated", GeneratorConfig{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	assertConcreteControlFlow(t, generated)
	runGeneratedFixture(t, source, `package generated
import("testing";"slices")

func TestConcreteFunctions(t *testing.T) {
 app := &App{}
 chosen,err:=app.choose(t.Context(),true)
 if err!=nil||!slices.Equal(chosen,[]int64{1,2}) {t.Fatal(chosen,err)}
 mapped,err:=app.mapped(t.Context(),chosen)
 if err!=nil||!slices.Equal(mapped,[]int64{2,3}) {t.Fatal(mapped,err)}
 filtered,err:=app.collected(t.Context(),[]int64{-2,0,1})
 if err!=nil||!slices.Equal(filtered,[]int64{1,2}) {t.Fatal(filtered,err)}
 parallel,err:=app.parallel(t.Context(),true)
 if err!=nil||!slices.Equal(parallel,[]int64{2,3}) {t.Fatal(parallel,err)}
}
`, ids)
}

func TestGeneratedChannelPayloadPrecedence(t *testing.T) {
	runGeneratedFixture(t, `
fn arithmeticPayload(value: Int): Int {
 val ch: Channel<Int> = Channel(1)
 ch <- value + 2 * 3
 return <-ch
}
fn booleanPayload(value: Int): Boolean {
 val ch: Channel<Boolean> = Channel(1)
 ch <- value > 0 && value < 10
 return <-ch
}
fn nullablePayload(value: Int?): Int {
 val ch: Channel<Int> = Channel(1)
 ch <- value ?: 42
 return <-ch
}
fn receivePayload(): Int {
 val source: Channel<Int> = Channel(1)
 val target: Channel<Int> = Channel(1)
 source <- 41
 target <- <-source + 1
 return <-target
}
fn textPayload(value: String): String {
 val ch: Channel<String> = Channel(1)
 ch <- value + "!"
 return <-ch
}
api payloadProbe(value: Int): Int { return arithmeticPayload(value) }
`, channelPayloadAssertions, StableIDs{
		APIs: map[string]int{"payloadProbe": 1}, APIParams: map[string]map[string]int{"payloadProbe": {"value": 1}},
	})
}

const channelPayloadAssertions = `package generated
import("testing";"bytes";"encoding/json";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

func TestChannelPayloadValues(t *testing.T){
 app:=&App{}
 if value,err:=app.arithmeticPayload(t.Context(),1);err!=nil||value!=7{t.Fatal(value,err)}
 for _,sample:=range []struct{input int64;want bool}{{-1,false},{5,true},{10,false}}{
  if value,err:=app.booleanPayload(t.Context(),sample.input);err!=nil||value!=sample.want{t.Fatal(value,err)}
 }
 present:=int64(8)
 for _,sample:=range []struct{input *int64;want int64}{{nil,42},{&present,8}}{
  if value,err:=app.nullablePayload(t.Context(),sample.input);err!=nil||value!=sample.want{t.Fatal(value,err)}
 }
 if value,err:=app.receivePayload(t.Context());err!=nil||value!=42{t.Fatal(value,err)}
 if value,err:=app.textPayload(t.Context(),"payload");err!=nil||value!="payload!"{t.Fatal(value,err)}
}
func TestChannelPayloadHTTP(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"payloadProbe\",\"value\":1}")
  if binary{var encoded codec.Encoder;encoded.WriteVarint(1);encoded.WriteVarint(0);encoded.WriteFieldInt(1,1);encoded.WriteEnd();body=encoded.Bytes()}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary");request.Header.Set("Content-Type","application/x-luxo")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{if !bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,7)){t.Fatal(response.Body.Bytes())}}else{
   var value struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&value);err!=nil||value.Data!=7{t.Fatal(value,err)}
  }
 }
}
`

func TestGeneratedDiscardedWhen(t *testing.T) {
	runGeneratedFixture(t, discardedWhenSource, discardedWhenAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"State": {"value": 1}},
		APIs:        map[string]int{"whenProbe": 1}, APIParams: map[string]map[string]int{"whenProbe": {"flag": 1}},
	}, discardedWhenBenchmarkSource)
}

func TestCompileDiscardedWhenTypeBranch(t *testing.T) {
	c := newCompiler(nil)
	value := &ast.Literal{Kind: token.Int, Value: "1"}
	value.SetTypeTag("Int")
	c.compileWhenStatement(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "value"},
		Branches: []*ast.WhenBranch{
			{IsType: "string", Body: value},
		},
	})
	got := c.b.String()
	for _, expected := range []string{"_subject1 := value", "switch _subject1.(type)", "case string:", "_ = 1"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q in statement branch:\n%s", expected, got)
		}
	}
	if strings.Contains(got, "func()") {
		t.Fatalf("statement branch created a value closure:\n%s", got)
	}
}

func TestGeneratedWhenBranchBindingIsolation(t *testing.T) {
	for _, sample := range []struct{ name, body string }{
		{"statement", `
 var result = 42
 when {
  flag -> { val value = "inner" result = 1 }
  else -> { if value != null { result = value + 1 } }
 }
 return result`},
		{"value", `
 return when {
  flag -> { val value = "inner" return 1 }
  else -> { if value != null { return value + 1 } 42 }
 }`},
		{"channel-statement", `
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 1 }
 var result = 42
 when {
  <-ch -> { val value = "inner" result = 1 }
  else -> { if value != null { result = value + 1 } }
 }
 return result`},
		{"channel-value", `
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 1 }
 return when {
  <-ch -> { val value = "inner" return 1 }
  else -> { if value != null { return value + 1 } 42 }
 }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runWhenBindingFixture(t, whenBindingFixture{
				body: sample.body, cases: "{true,nil,1},{true,&input,1},{false,nil,42},{false,&input,8}",
			})
		})
	}
}

func TestGeneratedChannelNullableBinding(t *testing.T) {
	for _, factory := range []struct{ name, declaration, initialize, receive, support string }{
		{"declared", "", "val channel: Channel<Int?> = Channel(1)\nif flag { channel <- value }", "<-channel", whenBindingEmptyApp},
		{"function", channelBindingFunctionSource, "val channel = source(flag, value)", "<-channel", whenBindingEmptyApp},
		{"native", channelBindingNativeSource, "val channel = source(flag, value)?", "<-channel", channelBindingNativeSupport},
		{"inline-function", channelBindingFunctionSource, "", "<-source(flag, value)", whenBindingEmptyApp},
		{"inline-native", channelBindingNativeSource, "", "<-(source(flag, value)?)", channelBindingNativeSupport},
	} {
		for _, sample := range []struct{ name, body string }{
			{"statement", `
 var result = 42
 when {
  <-channel -> { message -> if message != null { result = message + 1 } }
  else -> { result = 0 }
 }
 return result`},
			{"value", `
 return when {
  <-channel -> { message -> if message != null { return message + 1 } 42 }
  else -> 0
 }`},
		} {
			t.Run(factory.name+"/"+sample.name, func(t *testing.T) {
				runWhenBindingFixture(t, whenBindingFixture{
					declaration: factory.declaration, initialize: factory.initialize,
					body:      strings.ReplaceAll(sample.body, "<-channel", factory.receive),
					appSource: factory.support, cases: "{true,nil,42},{true,&input,8},{false,nil,0},{false,&input,0}",
				})
			})
		}
	}
}

func TestGeneratedWhenChannelResult(t *testing.T) {
	for _, sample := range []struct{ name, declaration, choose string }{
		{"expressions", "", `val channel = when { flag -> left else -> right }`},
		{"blocks", "", `val channel = when { flag -> { left } else -> { right } }`},
		{"locals", "", `val channel = when { flag -> { val local = left local } else -> { val local = right local } }`},
		{"returns", "", `val channel = when { flag -> { return left } else -> { return right } }`},
		{"function", `fn pickChannel(flag: Boolean, left: Channel<Int?>, right: Channel<Int?>): Channel<Int?> {
 return when { flag -> { left } else -> { right } }
}`, `val channel = pickChannel(flag,left,right)`},
		{"select", "", `val outer: Channel<Channel<Int?>> = Channel(1)
 if flag { outer <- left }
 val channel = when { <-outer -> { message -> message } else -> { right } }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runWhenBindingFixture(t, whenBindingFixture{
				declaration: sample.declaration,
				initialize:  "val left: Channel<Int?> = Channel(1)\nval right: Channel<Int?> = Channel(1)\n" + sample.choose,
				body:        "channel <- value\nval item = <-channel\nreturn item ?: 42",
				cases:       "{true,nil,42},{true,&input,7},{false,nil,42},{false,&input,7}",
			})
		})
	}
}

const channelBindingFunctionSource = `fn source(flag: Boolean, value: Int?): Channel<Int?> {
 val channel: Channel<Int?> = Channel(1)
 if flag { channel <- value }
 return channel
}`

func TestGeneratedNullableWhenChannelResult(t *testing.T) {
	runWhenBindingFixture(t, whenBindingFixture{
		initialize: `val left: Channel<Int?> = Channel(1)
 val channel = when { flag -> { left } else -> { null } }`,
		body: `if channel != null {
 channel <- value
 val item = <-channel
 return item ?: 42
} return 42`,
		cases: "{true,nil,42},{true,&input,7},{false,nil,42},{false,&input,42}",
	})
}

func TestGeneratedNativeWhenChannelResult(t *testing.T) {
	runWhenBindingFixture(t, whenBindingFixture{
		declaration: `fn pickChannel(left: Channel<Int?>): Result<Channel<Int?>> @native`,
		initialize: `val left: Channel<Int?> = Channel(1)
 val right: Channel<Int?> = Channel(1)
 val channel = when { flag -> { pickChannel(left)? } else -> { right } }`,
		body:      "channel <- value\nval item = <-channel\nreturn item ?: 42",
		cases:     "{true,nil,42},{true,&input,7},{false,nil,42},{false,&input,7}",
		appSource: whenChannelNativeSupport,
	})
}

const whenChannelNativeSupport = `package generated
import("context";"errors";"testing")
type whenChannelResolver struct{failure error;calls int}
func(r *whenChannelResolver)PickChannel(_ context.Context,left chan *int64)(chan *int64,error){r.calls++;return left,r.failure}
func bindingApp()*App{return &App{Resolver:&whenChannelResolver{}}}
func TestWhenNativeFailure(t *testing.T){
 failure:=errors.New("pick failed");resolver:=&whenChannelResolver{failure:failure};app:=&App{Resolver:resolver}
 if got,err:=app.choose(t.Context(),true,nil);got!=0||!errors.Is(err,failure)||resolver.calls!=1{t.Fatal(got,err,resolver.calls)}
 input:=int64(7);if got,err:=app.choose(t.Context(),false,&input);got!=7||err!=nil||resolver.calls!=1{t.Fatal(got,err,resolver.calls)}
}
`

const channelBindingNativeSource = "fn source(flag: Boolean, value: Int?): Result<Channel<Int?>> @native"

func TestGeneratedSelectReceiverEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
fn first(value: Int): Result<Channel<Int>> @native
fn second(value: Int): Result<Channel<Int>> @native
fn swap(channels: Channel<Channel<Int>>): Result<Channel<Int>> @native
fn ordered(value: Int): Int {
 return when {
  <-(first(value)?) -> { item -> item }
  <-(second(value)?) -> { item -> item }
  else -> 0
 }
}
fn captured(value: Int): Int {
 val channel: Channel<Int> = Channel(1)
 channel <- value
 val outer: Channel<Channel<Int>> = Channel(1)
 outer <- channel
 return when {
  <-(<-outer) -> { item -> item }
  <-(swap(outer)?) -> { item -> item }
 }
}
api orderProbe(value: Int): Int { return ordered(value) }
api captureProbe(value: Int): Int { return captured(value) }
`, selectReceiverEvaluationAssertions, StableIDs{APIs: map[string]int{"orderProbe": 1, "captureProbe": 2}})
}

const selectReceiverEvaluationAssertions = `package generated
import("context";"errors";"slices";"testing")
type receiverResolver struct{calls []int;firstError,secondError error}
func(r *receiverResolver)First(_ context.Context,value int64)(chan int64,error){
 r.calls=append(r.calls,1);if r.firstError!=nil{return nil,r.firstError}
 channel:=make(chan int64,1);channel<-value;return channel,nil
}
func(r *receiverResolver)Second(context.Context,int64)(chan int64,error){
 r.calls=append(r.calls,2);if r.secondError!=nil{return nil,r.secondError};return make(chan int64),nil
}
func(r *receiverResolver)Swap(_ context.Context,channels chan chan int64)(chan int64,error){
 r.calls=append(r.calls,3);select{case <-channels:default:}
 channel:=make(chan int64,1);channel<-99;channels<-channel;return make(chan int64),nil
}
func TestReceiverOrder(t *testing.T){
 resolver:=&receiverResolver{};app:=&App{Resolver:resolver}
 if value,err:=app.ordered(t.Context(),7);err!=nil||value!=7||!slices.Equal(resolver.calls,[]int{1,2}){t.Fatal(value,err,resolver.calls)}
 failure:=errors.New("source failed")
 for _,first:=range []bool{false,true}{
  resolver.calls=nil;resolver.firstError=nil;resolver.secondError=nil;want:=[]int{1,2}
  if first{resolver.firstError=failure;want=[]int{1}}else{resolver.secondError=failure}
  if value,err:=app.ordered(t.Context(),7);value!=0||!errors.Is(err,failure)||!slices.Equal(resolver.calls,want){t.Fatal(value,err,resolver.calls)}
 }
 resolver.calls=nil;resolver.firstError=nil;resolver.secondError=nil
 if value,err:=app.captured(t.Context(),7);err!=nil||value!=7||!slices.Equal(resolver.calls,[]int{3}){t.Fatal("channel operand was read after its replacement",value,err,resolver.calls)}
}
`

func TestGeneratedChannelBindingShadowsOuter(t *testing.T) {
	for _, sample := range []struct{ name, body string }{
		{"statement", `var result = 42
 when { <-channel -> { value -> result = value } else -> { if value != null { result = value + 1 } } }
 return result`},
		{"value", `return when {
 <-channel -> { value -> value }
 else -> { if value != null { return value + 1 } 42 }
 }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runWhenBindingFixture(t, whenBindingFixture{
				initialize: "val channel: Channel<Int> = Channel(1)\nif flag { channel <- 1 }",
				body:       sample.body, cases: "{true,nil,1},{true,&input,1},{false,nil,42},{false,&input,8}",
			})
		})
	}
}

func TestGeneratedNestedChannelBinding(t *testing.T) {
	for _, sample := range []struct{ name, body string }{
		{"statement", `var result = 42
 when {
 <-outer -> { message -> message <- 7 val item = <-message if item != null { result = item + 1 } }
 else -> { result = 0 }
 }
 return result`},
		{"value", `return when {
 <-outer -> { message -> message <- 7 val item = <-message if item != null { return item + 1 } 42 }
 else -> 0
 }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runWhenBindingFixture(t, whenBindingFixture{
				initialize: "val channel: Channel<Int?> = Channel(1)\nval outer: Channel<Channel<Int?>> = Channel(1)\nif flag { outer <- channel }",
				body:       sample.body, cases: "{true,nil,8},{true,&input,8},{false,nil,0},{false,&input,0}",
			})
		})
	}
}

type whenBindingFixture struct {
	declaration, initialize, body, cases, appSource string
}

func runWhenBindingFixture(t *testing.T, fixture whenBindingFixture) {
	t.Helper()
	source := fixture.declaration + "\nfn choose(flag: Boolean, value: Int?): Int {\n" +
		fixture.initialize + "\n" + fixture.body + "}\n" +
		"api probe(flag: Boolean, value: Int?): Int { return choose(flag, value) }"
	appSource := fixture.appSource
	if appSource == "" {
		appSource = whenBindingEmptyApp
	}
	runGeneratedFixture(t, source, whenBranchBindingAssertions,
		StableIDs{APIs: map[string]int{"probe": 1}, APIParams: map[string]map[string]int{"probe": {"flag": 1, "value": 2}}},
		whenBranchBindingHTTPAssertions, appSource, whenBindingCasesSource(fixture.cases))
}

func whenBindingCasesSource(values string) string {
	return `package generated
type bindingCase struct { flag bool; value *int64; want int64 }
func bindingCases() []bindingCase {
 input:=int64(7)
 return []bindingCase{` + values + `}
}
`
}

const whenBindingEmptyApp = `package generated

func bindingApp() *App { return &App{} }
`

const channelBindingNativeSupport = `package generated
import("context";"errors";"testing")
type channelBindingResolver struct{failure error}
func(r channelBindingResolver)Source(_ context.Context,flag bool,value *int64)(chan *int64,error){
 if r.failure!=nil{return nil,r.failure}
 channel:=make(chan *int64,1);if flag{channel<-value};return channel,nil
}
func bindingApp()*App{return &App{Resolver:channelBindingResolver{}}}
func TestChannelSourceFailure(t *testing.T){
 failure:=errors.New("source failed");app:=&App{Resolver:channelBindingResolver{failure:failure}}
 if got,err:=app.choose(t.Context(),true,nil);got!=0||!errors.Is(err,failure){t.Fatal(got,err)}
}
`

const whenBranchBindingAssertions = `package generated
import "testing"
func TestBranchBinding(t *testing.T) {
 app:=bindingApp()
 for _,sample:=range bindingCases(){
  value,err:=app.choose(t.Context(),sample.flag,sample.value)
  if err!=nil||value!=sample.want{t.Fatal(sample,value,err)}
 }
}
`

const whenBranchBindingHTTPAssertions = `package generated
import("bytes";"net/http/httptest";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestBranchBindingHTTP(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,bindingApp());err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range bindingCases(){
  for _,binary:=range []bool{false,true}{
   text:="null";if sample.value!=nil{text=strconv.FormatInt(*sample.value,10)}
   body:=[]byte("{\"$api\":\"probe\",\"flag\":"+strconv.FormatBool(sample.flag)+",\"value\":"+text+"}")
   if binary{
    encoder:=codec.NewEncoder([]byte{1,0});encoder.WriteFieldBool(1,sample.flag);encoder.WriteFieldHeader(2)
    if sample.value==nil{encoder.WriteNull()}else{encoder.WritePresent();encoder.WriteInt(*sample.value)}
    encoder.WriteEnd();body=encoder.Bytes()
   }
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatal(binary,response.Code,response.Body.String())}
   if binary{
    decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadInt()
    if err:=decoder.Finish();err!=nil||got!=sample.want{t.Fatal(got,sample.want,err)}
   }else if response.Body.String()!="{\"data\":"+strconv.FormatInt(sample.want,10)+"}"{t.Fatal(response.Body.String())}
  }
 }
}
`

func TestGeneratedWhenBreakTargetsLoop(t *testing.T) {
	runGeneratedFixture(t, `
fn stopLoop(): Int {
 var total = 0
 for value in 0..4 {
  when { value == 2 -> { break } else -> {} }
  total += value
 }
 return total
}
api loopProbe(): Int { return stopLoop() }
`, `package generated
import "testing"

func TestBreak(t *testing.T){value,err:=(&App{}).stopLoop(t.Context());if err!=nil||value!=1{t.Fatal(value,err)}}
`, StableIDs{APIs: map[string]int{"loopProbe": 1}})
}

func TestGeneratedWhenContinueTargetsLoop(t *testing.T) {
	runGeneratedFixture(t, `
fn skipLoop(): Int {
 var total = 0
 for value in 0..4 {
  when { value == 2 -> { continue } else -> {} }
  total += value
 }
 return total
}
api loopProbe(): Int { return skipLoop() }
`, `package generated
import "testing"

func TestContinue(t *testing.T){value,err:=(&App{}).skipLoop(t.Context());if err!=nil||value!=8{t.Fatal(value,err)}}
`, StableIDs{APIs: map[string]int{"loopProbe": 1}})
}

func TestGeneratedWhenLoopVariants(t *testing.T) {
	runGeneratedFixture(t, whenLoopVariantSource, whenLoopVariantAssertions, StableIDs{APIs: map[string]int{"loopProbe": 1}})
}

const whenLoopVariantSource = `
fn upper(): Int { return 4 }
fn dynamicRange(): Int {
 var total = 0
 for value in 0..upper() { when { value == 2 -> { break } else -> {} } total += value }
 return total
}
fn listContinue(): Int {
 var total = 0
 for value in [0,1,2,3,4] { when { value == 2 -> { continue } else -> {} } total += value }
 return total
}
fn nestedLoops(): Int {
 var total = 0
 for outer in 0..2 {
  for inner in 0..4 {
   when { inner > 0 -> { when { inner == 2 -> { break } else -> {} } } else -> {} }
   total += 1
  }
 }
 return total
}
fn sourceChannel(): Channel<Int> {
 val channel: Channel<Int> = Channel(5)
 for value in 0..4 { channel <- value }
 channel.close()
 return channel
}
fn channelBreak(): Int {
 var total = 0
 for value in sourceChannel() { when { value == 2 -> { break } else -> {} } total += value }
 return total
}
fn collectBreak(): [Int] {
 return for value in 0..4 { when { value == 2 -> { break } else -> {} } value }
}
fn collectContinue(): [Int] {
 return for value in sourceChannel() { when { value == 2 -> { continue } else -> {} } value }
}
fn nestedReturn(): Int {
 var total = 10
 when { true -> { for value in 0..4 { if value == 2 { return 99 } total += 1 } total += 100 } else -> {} }
 return total
}
fn unusedReceive(): Int {
 val channel: Channel<Int> = Channel(1)
 channel <- 42
 when { <-channel -> { unused -> } }
 return 7
}
api loopProbe(): Int { return nestedLoops() }
`

const whenLoopVariantAssertions = `package generated
import("testing";"slices")

func TestLoopVariants(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;run func()(int64,error);want int64}{
  {"dynamic",func()(int64,error){return app.dynamicRange(t.Context())},1},
  {"list",func()(int64,error){return app.listContinue(t.Context())},8},
  {"nested",func()(int64,error){return app.nestedLoops(t.Context())},6},
  {"channel",func()(int64,error){return app.channelBreak(t.Context())},1},
  {"return",func()(int64,error){return app.nestedReturn(t.Context())},12},
  {"receive",func()(int64,error){return app.unusedReceive(t.Context())},7},
 }{value,err:=sample.run();if err!=nil||value!=sample.want{t.Fatalf("%s: %d %v",sample.name,value,err)}}
 if value,err:=app.collectBreak(t.Context());err!=nil||!slices.Equal(value,[]int64{0,1}){t.Fatal(value,err)}
 if value,err:=app.collectContinue(t.Context());err!=nil||!slices.Equal(value,[]int64{0,1,3,4}){t.Fatal(value,err)}
}
`

func TestGeneratedConditionAndInfiniteLoops(t *testing.T) {
	runGeneratedFixture(t, `
fn below(count: Int, limit: Int): Boolean {
 if limit < 0 { throw error.BadRequest }
 return count < limit
}
fn conditionalLoop(limit: Int): Int {
 var count = 0
 for below(count,limit) { count += 1 }
 return count
}
fn infiniteLoop(limit: Int): Int {
 var count = 0
 for { when { count >= limit -> { break } else -> {} } count += 1 }
 return count
}
api loopProbe(limit: Int): Int { return conditionalLoop(limit) }
`, `package generated
import("testing";"errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func TestLoopForms(t *testing.T){
 app:=&App{}
 for _,limit:=range []int64{0,1,4}{
  if value,err:=app.conditionalLoop(t.Context(),limit);err!=nil||value!=limit{t.Fatal(value,err)}
  if value,err:=app.infiniteLoop(t.Context(),limit);err!=nil||value!=limit{t.Fatal(value,err)}
 }
 if _,err:=app.conditionalLoop(t.Context(),-1);!errors.Is(err,luxerrors.BadRequest){t.Fatal(err)}
}
`, StableIDs{APIs: map[string]int{"loopProbe": 1}, APIParams: map[string]map[string]int{"loopProbe": {"limit": 1}}})
}

func TestDiscardedClosureBareReturn(t *testing.T) {
	c := newCompiler(nil)
	c.valueClosure = true
	c.whenScope = &controlScope{prefix: "_when"}
	c.compileReturn(&ast.ReturnStmt{})
	if got := strings.TrimSpace(c.b.String()); got != "break _when1" {
		t.Fatalf("statement branch escaped its own return boundary: %s", got)
	}
}

const discardedWhenSource = `
type State { value: Int }
fn accepted(value: Int): Boolean {
 if value < 0 { throw error.BadRequest }
 return value > 0
}
fn assigned(flag: Boolean): Int {
 val state = State { value: 10 }
 when { flag -> { state.value += 1 } else -> { state.value += 2 } }
 return state.value
}
fn emptyBranch(flag: Boolean): Int {
 val state = State { value: 10 }
 when { flag -> {} else -> { state.value = 42 } }
 return state.value
}
fn branchError(value: Int): Int {
 val state = State { value: 10 }
 when { value > 0 -> { throw error.BadRequest } else -> { state.value = 42 } }
 return state.value
}
fn conditionError(value: Int): Int {
 val state = State { value: 10 }
 when { accepted(value) -> { state.value = 41 } else -> { state.value = 42 } }
 return state.value
}
fn localReturn(flag: Boolean): Int {
 val state = State { value: 10 }
 when { flag -> { state.value = 41 return 1 } else -> { state.value = 42 return 2 } }
 return state.value
}
fn ignoredNull(flag: Boolean): Int {
 when { flag -> null else -> null }
 return 42
}
fn nestedValue(flag: Boolean): Int {
 val state = State { value: 10 }
 when { flag -> { val value = when { flag -> 1 else -> 2 } state.value = value } else -> {} }
 return state.value
}
fn channelStatement(ready: Boolean): Int {
 val state = State { value: 10 }
 val channel: Channel<Int> = Channel(1)
 if ready { channel <- 42 }
 when { <-channel -> { value -> state.value = value } else -> {} }
 return state.value
}
fn reject(value: Int) { if value < 0 { throw error.BadRequest } }
fn voidCall(value: Int): Int {
 when { value > 0 -> reject(value) else -> reject(value) }
 return 42
}
fn subjectStatement(value: Int): Int {
 val state = State { value: 10 }
 when value { 1 -> { state.value = 41 } else -> { state.value = 42 } }
 return state.value
}
fn lazyStatement(value: Int): Int {
 val state = State { value: 10 }
 when { value < 0 -> { state.value = 39 } accepted(value) -> { state.value = 41 } else -> { state.value = 42 } }
 return state.value
}
api whenProbe(flag: Boolean): Int { return assigned(flag) }
`

const discardedWhenAssertions = `package generated
import("testing";"errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func TestDiscardedWhen(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{flag bool;assigned,empty,local int64}{{true,11,10,41},{false,12,42,42}}{
  if value,err:=app.assigned(t.Context(),sample.flag);err!=nil||value!=sample.assigned{t.Fatal(value,err)}
  if value,err:=app.emptyBranch(t.Context(),sample.flag);err!=nil||value!=sample.empty{t.Fatal(value,err)}
  if value,err:=app.localReturn(t.Context(),sample.flag);err!=nil||value!=sample.local{t.Fatal(value,err)}
 }
 for _,sample:=range []struct{input,want int64}{{0,42},{1,41}}{
  if value,err:=app.conditionError(t.Context(),sample.input);err!=nil||value!=sample.want{t.Fatal(value,err)}
 }
 if _,err:=app.conditionError(t.Context(),-1);!errors.Is(err,luxerrors.BadRequest){t.Fatal(err)}
 if _,err:=app.branchError(t.Context(),1);!errors.Is(err,luxerrors.BadRequest){t.Fatal(err)}
 if value,err:=app.branchError(t.Context(),0);err!=nil||value!=42{t.Fatal(value,err)}
 for _,flag:=range []bool{false,true}{
  if value,err:=app.ignoredNull(t.Context(),flag);err!=nil||value!=42{t.Fatal(value,err)}
  want:=int64(10);if flag{want=1}
  if value,err:=app.nestedValue(t.Context(),flag);err!=nil||value!=want{t.Fatal(value,err)}
  if flag{want=42}
  if value,err:=app.channelStatement(t.Context(),flag);err!=nil||value!=want{t.Fatal(value,err)}
 }
 for _,sample:=range []struct{input,lazy,subject int64}{{-1,39,42},{0,42,42},{1,41,41}}{
  if value,err:=app.lazyStatement(t.Context(),sample.input);err!=nil||value!=sample.lazy{t.Fatal(value,err)}
  if value,err:=app.subjectStatement(t.Context(),sample.input);err!=nil||value!=sample.subject{t.Fatal(value,err)}
 }
 if _,err:=app.voidCall(t.Context(),-1);!errors.Is(err,luxerrors.BadRequest){t.Fatal(err)}
 if value,err:=app.voidCall(t.Context(),0);err!=nil||value!=42{t.Fatal(value,err)}
}
`

func TestFormalTypedChannel(t *testing.T) {
	const source = `
model Envelope @noTime { id: Int @id }
fn payload(): Int {
 val ch: Channel<Envelope> = Channel(1)
 ch <- Envelope { id: 42 }
 val value = <-ch
 return value.id
}
fn nullableMessage(): Int {
 val ch: Channel<Int?> = Channel(1)
 ch <- 42
 val value = <-ch
 return value ?: 0
}
fn exchange(capacity: Int): Int {
 val ch: Channel<Int> = Channel(capacity)
 ch <- 42
 val value = <-ch
 ch.close()
 return value
}
fn selected(): Int {
 val ch: Channel<Int> = Channel(1)
 ch <- 41
 return when { <-ch -> { value -> value + 1 } else -> 0 }
}
api probe(): Int { return exchange(1) }
fn transfer(ch: Channel<Int>, value: Int): Int {
 ch <- value
 return <-ch
}
`
	runGeneratedFixture(t, source, `package generated
import "testing"
func TestChannel(t *testing.T) {
 app := &App{}
 value,err:=app.exchange(t.Context(),1)
 if err!=nil||value!=42 {t.Fatal(value,err)}
 if _,err=app.exchange(t.Context(),-1);err==nil {t.Fatal("negative capacity accepted")}
 if value,err=app.selected(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
 if value,err=app.payload(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
 if value,err=app.nullableMessage(t.Context());err!=nil||value!=42 {t.Fatal(value,err)}
}
// The legacy control retains the former interface payload; capacity is valid.
func legacyExchange(capacity int64) (int64,error) {
 ch:=make(chan any,capacity)
 ch<-int64(42)
 value:=(<-ch).(int64)
 close(ch)
 return value,nil
}
func uncheckedTypedExchange(capacity int64) (int64,error) {
 ch:=make(chan int64,capacity)
 ch<-42
 value:=<-ch
 close(ch)
 return value,nil
}
func BenchmarkTypedChannel(b *testing.B) {
 app:=&App{}
 ctx:=b.Context()
 b.Run("lifecycle/legacy",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=legacyExchange(1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("lifecycle/generated",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=app.exchange(ctx,1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("lifecycle/typed_unchecked",func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){value,err:=uncheckedTypedExchange(1);if err!=nil||value!=42 {b.Fatal(value,err)}}
 })
 b.Run("message/legacy",func(b *testing.B){
  ch:=make(chan any,1)
  var input int64=9007199254740993
  b.ReportAllocs()
  for b.Loop(){ch<-input;value:=(<-ch).(int64);if value!=input {b.Fatal(value)};input++}
 })
 b.Run("message/generated",func(b *testing.B){
  ch:=make(chan int64,1)
  var input int64=9007199254740993
  b.ReportAllocs()
  for b.Loop(){value,err:=app.transfer(ctx,ch,input);if err!=nil||value!=input {b.Fatal(value,err)};input++}
 })
}
`, StableIDs{APIs: map[string]int{"probe": 1}, ModelFields: map[string]map[string]int{"Envelope": {"id": 1}}})
}

func assertConcreteControlFlow(t *testing.T, generated *GenerateResult) {
	t.Helper()
	checked := 0
	for name, data := range generated.Files {
		parsed, err := goparser.ParseFile(gotoken.NewFileSet(), name, data, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			fn, ok := declaration.(*goast.FuncDecl)
			if !ok || (fn.Name.Name != "choose" && fn.Name.Name != "mapped" && fn.Name.Name != "collected" && fn.Name.Name != "parallel") {
				continue
			}
			checked++
			goast.Inspect(fn, func(node goast.Node) bool {
				if ident, ok := node.(*goast.Ident); ok && ident.Name == "any" {
					t.Errorf("%s contains dynamic fallback", fn.Name.Name)
				}
				if _, ok := node.(*goast.InterfaceType); ok {
					t.Errorf("%s contains interface fallback", fn.Name.Name)
				}
				return true
			})
		}
	}
	if checked != 4 {
		t.Fatalf("checked %d functions, want 4", checked)
	}
}

func TestGeneratedNestedYieldOwnership(t *testing.T) {
	runGeneratedFixture(t, `
fn mapped(items: [Int]): [Int] {
 val results = for item in items {
  val inner = for candidate in [item] { yield candidate }
  inner ?: 0
 }
 return results
}
api mapItems(items: [Int]): [Int] { return mapped(items) }
`, `package generated
import("testing";"slices")

func TestMapped(t *testing.T) {
 for _,items:=range [][]int64{nil,{}, {1,2,3}} {
  got,err:=(&App{}).mapped(t.Context(),items)
  if err!=nil {t.Fatal(err)}
  if !slices.Equal(got,items) {t.Fatalf("got %v, want %v",got,items)}
 }
}
`, StableIDs{})
}

func TestGeneratedChannelLoopValues(t *testing.T) {
	runGeneratedFixture(t, `
fn collectChannel(): [Int] {
 val ch: Channel<Int> = Channel(2)
 ch <- 3
 ch <- 7
 ch.close()
 val values = for item in ch { item + 1 }
 return values
}
fn findChannel(): Int? {
 val ch: Channel<Int> = Channel(2)
 ch <- 3
 ch <- 7
 ch.close()
 val found = for item in ch { if item > 5 { yield item } }
 return found
}
fn nullableChannel(): Int? {
 val ch: Channel<Int?> = Channel(2)
 ch <- null
 ch <- 42
 ch.close()
 val found = for item in ch { if item != null { yield item } }
 return found
}
fn sourceChannel(): Channel<Int> {
 val ch: Channel<Int> = Channel(1)
 ch <- 9
 ch.close()
 return ch
}
fn returnedChannel(): [Int] { val values = for item in (sourceChannel()) { item } return values }
fn returnedChannelStatement(): Int { var result = 0 for item in (sourceChannel()) { result += item } return result }

`, `package generated
import("slices";"testing")

func TestChannelLoopValues(t *testing.T){
 app:=&App{}
 got,err:=app.collectChannel(t.Context());if err!=nil||!slices.Equal(got,[]int64{4,8}){t.Fatal(got,err)}
 found,err:=app.findChannel(t.Context());if err!=nil||found==nil||*found!=7{t.Fatal(found,err)}
 found,err=app.nullableChannel(t.Context());if err!=nil||found==nil||*found!=42{t.Fatal(found,err)}
 got,err=app.returnedChannel(t.Context());if err!=nil||!slices.Equal(got,[]int64{9}){t.Fatal(got,err)}
 value,err:=app.returnedChannelStatement(t.Context());if err!=nil||value!=9{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedUnderscoreLoops(t *testing.T) {
	runGeneratedFixture(t, `
fn underscoreLoops(): Int {
 var total = 0
 for _ in [1,2] { total += 1 }
 for _ in 0..1 { total += 1 }
 val mapped = for _ in [1,2] { 7 }
 val found = for _ in [1] { yield 9 }
 for value in mapped { total += value }
 return total + (found ?: 0)
}
`, `package generated
import "testing"

func TestUnderscoreLoops(t *testing.T){
 got,err:=(&App{}).underscoreLoops(t.Context());if err!=nil||got!=27{t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedForModelValues(t *testing.T) {
	runGeneratedFixture(t, `model Node @noTime { id: Int @id }
api literal(): [Node] { return for value in [2, 3] { Node { id: value } } }
api local(): [Node] { val row = Node { id: 7 } return for value in [1, 2] { row } }
api references(): [Node] { val rows = [Node { id: 9 }] return for row in rows { row } }
`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestForModelValues(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name,want string}{{"literal","{\"data\":[{\"id\":2},{\"id\":3}]}"},{"local","{\"data\":[{\"id\":7},{\"id\":7}]}"},{"references","{\"data\":[{\"id\":9}]}"}}{
  request:=httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+sample.name+"\",\"$select\":\"id\"}"));response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200||response.Body.String()!=sample.want{t.Fatalf("%s: status=%d body=%s",sample.name,response.Code,response.Body)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}})
}

func TestGeneratedNullableYield(t *testing.T) {
	runGeneratedFixture(t, `
fn nullableInt(value: Int?): Int? {
 val found = for item in [1] { yield value }
 return found
}
fn nullableText(value: String?): String? {
 val found = for item in [1] { yield value }
 return found
}
fn narrowedInt(value: Int?): Int? {
 val found = for item in [1] { if value != null { yield value } }
 return found
}
fn unusedLoopItem(): Int {
 var count = 0
 for item in [1,2] { count += 1 }
 return count
}
`, `package generated
import "testing"

func TestNullableYield(t *testing.T){
 app:=&App{}
 integer:=int64(42);text:="hello"
 count,err:=app.unusedLoopItem(t.Context());if err!=nil||count!=2{t.Fatal(count,err)}
 for _,value:=range []*int64{nil,&integer}{
  got,err:=app.nullableInt(t.Context(),value);if err!=nil||got!=value{t.Fatal(got,err)}
  got,err=app.narrowedInt(t.Context(),value);if err!=nil||(got==nil)!=(value==nil){t.Fatal(got,err)}
  if got!=nil&&*got!=*value{t.Fatal(*got,*value)}
 }
 for _,value:=range []*string{nil,&text}{
  got,err:=app.nullableText(t.Context(),value);if err!=nil||got!=value{t.Fatal(got,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedYieldControlFlow(t *testing.T) {
	runGeneratedFixture(t, `
fn pickDirect(items: [Int]): Int? {
 val found = for item in items {
  if item <= 0 { continue }
  yield item
 }
 return found
}
fn pickNestedIf(items: [Int]): Int? {
 val found = for item in items {
  if item >= 0 { if item > 0 { yield item } }
 }
 return found
}
fn pickNestedLoop(items: [Int]): Int? {
 val found = for item in items {
  for candidate in [item] { if candidate > 0 { yield candidate } }
 }
 return found
}
fn pickWhen(items: [Int]): Int? {
 val found = for item in items {
  when {
   item > 0 -> yield item
   else -> null
  }
 }
 return found
}
fn positive(value: Int): Boolean { return value > 0 }
fn pickWhenDiscardedValue(items: [Int]): Int? {
 val found = for item in items { when { item > 0 -> yield item else -> item } }
 return found
}
fn pickWhenCall(items: [Int]): Int? {
 val found = for item in items { when { positive(item) -> yield item else -> null } }
 return found
}
fn pickWhenSubject(items: [Int]): Int? {
 val found = for item in items { when item { 7 -> yield item else -> null } }
 return found
}
fn pickWhenBlock(items: [Int]): Int? {
 val found = for item in items { when { item > 0 -> { val chosen = item yield chosen } else -> null } }
 return found
}
fn pickWhenNested(items: [Int]): Int? {
 val found = for item in items {
  when { item >= 0 -> when { item > 0 -> yield item else -> null } else -> null }
 }
 return found
}
fn pickWhenValue(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when { item > 0 -> yield item else -> 0 }
  if fallback > 0 { yield fallback }
 }
 return found
}
fn pickWhenValueNullable(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when { item > 0 -> yield item else -> null }
  if fallback != null { yield fallback }
 }
 return found
}
fn pickWhenValueBlock(items: [Int]): Int? {
 val found = for item in items {
  val fallback = when {
   item > 0 -> { val chosen = item yield chosen }
   else -> { val chosen = 0 chosen }
  }
  if fallback > 0 { yield fallback }
 }
 return found
}
api firstPositive(items: [Int]): Int? { return pickDirect(items) }
`, `package generated
import("context";"testing")

func TestFirstPositive(t *testing.T) {
 app:=&App{}
 for name,call:=range map[string]func(context.Context,[]int64)(*int64,error){
  "direct":app.pickDirect,"nested-if":app.pickNestedIf,"nested-loop":app.pickNestedLoop,"when":app.pickWhen,
  "when-call":app.pickWhenCall,"when-subject":app.pickWhenSubject,"when-block":app.pickWhenBlock,"when-nested":app.pickWhenNested,
  "when-value":app.pickWhenValue,
  "when-discarded-value":app.pickWhenDiscardedValue,
  "when-value-nullable":app.pickWhenValueNullable,"when-value-block":app.pickWhenValueBlock,
 } {t.Run(name,func(t *testing.T){
  for _,items:=range [][]int64{nil,{}, {-2,0}, {-2,0,7,9}} {
   got,err:=call(t.Context(),items);if err!=nil{t.Fatal(err)}
   if len(items)==4 {if got==nil||*got!=7{t.Fatalf("first yield: %v",got)}} else if got!=nil {t.Fatalf("no yield: %d",*got)}
  }
 })}
}
`, StableIDs{})
}

func TestGeneratedLogMethodsPreserveReturnValues(t *testing.T) {
	runGeneratedFixture(t, `
fn logged(): String { "debug".d "info".i "warn".w return "error".e }
api loggedValue(): String { return logged() }
api inlineLog(): String { return "inline".d }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/luxolog")

func TestGeneratedLogging(t *testing.T) {
 previous:=luxolog.GetLevel();defer func(){if err:=luxolog.SetLevel(previous);err!=nil{t.Error(err)}}()
 if err:=luxolog.SetLevel(luxolog.LevelError);err!=nil{t.Fatal(err)}
 got,err:=(&App{}).logged(t.Context());if err!=nil||got!="error"{t.Fatalf("%q %v",got,err)}
}
`, StableIDs{})
}

func TestWhenBindingPreservesAnalyzedType(t *testing.T) {
	for _, want := range []valType{
		{name: "Int"}, {name: "Int", isList: true},
		{name: "String", nullable: true}, {name: "User", isModel: true, isList: true},
	} {
		t.Run(goTypeForValType(want), func(t *testing.T) {
			c := newCompiler(map[string]*ast.ModelDecl{"User": {Name: "User"}})
			c.api.ReturnType = &ast.TypeRef{Name: "String"}
			expression := &ast.WhenExpr{Else: &ast.Ident{Name: "fallback"}}
			expression.SetTypeTag(want.name)
			expression.SetListType(want.isList)
			expression.SetNullable(want.nullable)
			c.compileVal(&ast.ValStmt{Name: "chosen", Value: expression})
			if got := c.vars["chosen"]; got != want {
				t.Fatalf("analyzed when type changed: got %+v, want %+v", got, want)
			}
		})
	}
}

func TestNarrowedReferencesKeepGoABI(t *testing.T) {
	c := newCompiler(map[string]*ast.ModelDecl{"User": {Name: "User"}})
	for _, name := range []string{"User", "Snapshot", "Bytes", "JSON"} {
		t.Run(name, func(t *testing.T) {
			identifier := &ast.Ident{Name: "value"}
			identifier.SetTypeTag(name)
			identifier.NarrowedTag = true
			c.vars["value"] = valType{name: name, nullable: true}
			want := "(*value)"
			if name == "User" {
				want = "value"
			}
			if got := c.compileExpr(identifier); got != want {
				t.Fatalf("reference ABI mismatch: got %s want %s", got, want)
			}
		})
	}
	list := &ast.Ident{Name: "values"}
	list.SetTypeTag("Boolean")
	list.SetListType(true)
	list.NarrowedTag = true
	c.vars["values"] = valType{name: "Boolean", nullable: true, isList: true}
	if got := c.compileExpr(list); got != "values" {
		t.Fatalf("nullable list changed ABI: %s", got)
	}
}

func TestLexicalBindingsRestorePaginationAndLocals(t *testing.T) {
	c := newCompiler(nil)
	c.vars["value"] = valType{name: "Boolean", nullable: true}
	c.paginationTotals = map[string]string{"value": "originalTotal"}
	original := c.saveBinding("value")
	c.vars["value"] = valType{name: "Int"}
	c.paginationTotals["value"] = "otherTotal"
	c.restoreBinding(original)
	if c.vars["value"] != original.value || c.paginationTotals["value"] != original.total {
		t.Fatal("outer binding was not restored")
	}
	block := &ast.Block{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		block.Stmts = append(block.Stmts, &ast.ValStmt{Name: name, Value: &ast.Literal{Kind: token.Int, Value: "1"}})
	}
	c.compileBlock(block)
	if len(c.vars) != 1 || c.vars["value"] != original.value {
		t.Fatal("block locals escaped their scope", c.vars)
	}
}

func TestGeneratedWhenBareThrows(t *testing.T) {
	runGeneratedFixture(t, `
enum ChoiceStatus { TODO DONE }
type WhenCounter { value: Int }
error ChoiceRejected(count: Int) { code: 400 message: "choice rejected" }
fn markChoice(state: WhenCounter): Int { state.value += 1 return state.value }
fn resolveChoice(value: String): ChoiceStatus {
 val status = when value {
  "TODO" -> ChoiceStatus.TODO
  "DONE" -> ChoiceStatus.DONE
  else -> throw error.ValidationFailed
 }
 return status
}
fn chooseNumber(flag: Boolean, value: Int): Int {
 return when { flag -> -value else -> throw error.BadRequest }
}
fn discardChoice(flag: Boolean, state: WhenCounter): Int {
 when { flag -> throw error.NotFound else -> { state.value += 1 } }
 return state.value
}
fn rejectedChoice(flag: Boolean, state: WhenCounter): Int {
 return when { flag -> 7 else -> throw ChoiceRejected(count: markChoice(state)) }
}
fn nestedChoice(flag: Boolean, value: String): ChoiceStatus {
 return when { flag -> when value { "TODO" -> ChoiceStatus.TODO else -> throw error.ValidationFailed } else -> ChoiceStatus.DONE }
}
fn channelChoice(flag: Boolean): Int {
 val channel: Channel<Int> = Channel(1)
 if flag { channel <- 1 }
 return when { <-channel -> throw error.NotFound else -> 9 }
}
api chooseChoice(value: String): ChoiceStatus {
 val status = when value { "TODO" -> ChoiceStatus.TODO "DONE" -> ChoiceStatus.DONE else -> throw error.ValidationFailed }
 return status
}
`, `package generated
import (
 "errors"
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)
func TestWhenBareThrowValues(t *testing.T) {
 app := &App{}
 ctx := t.Context()
 for _, test := range []struct { value string; want ChoiceStatus }{{"TODO",ChoiceStatusTODO},{"DONE",ChoiceStatusDONE}} {
  if got,err:=app.resolveChoice(ctx,test.value);err!=nil||got!=test.want { t.Fatal(got,err) }
 }
 for _,value:=range []string{"","unknown"} {
  if _,err:=app.resolveChoice(ctx,value);!errors.Is(err,luxerrors.ValidationFailed) { t.Fatal(err) }
 }
 if got,err:=app.chooseNumber(ctx,true,3);err!=nil||got!=-3 { t.Fatal(got,err) }
 if _,err:=app.chooseNumber(ctx,false,3);!errors.Is(err,luxerrors.BadRequest) { t.Fatal(err) }
 if got,err:=app.nestedChoice(ctx,false,"unknown");err!=nil||got!=ChoiceStatusDONE { t.Fatal(got,err) }
 if got,err:=app.nestedChoice(ctx,true,"TODO");err!=nil||got!=ChoiceStatusTODO { t.Fatal(got,err) }
 if _,err:=app.nestedChoice(ctx,true,"unknown");!errors.Is(err,luxerrors.ValidationFailed) { t.Fatal(err) }
 if got,err:=app.channelChoice(ctx,false);err!=nil||got!=9 { t.Fatal(got,err) }
 if _,err:=app.channelChoice(ctx,true);!errors.Is(err,luxerrors.NotFound) { t.Fatal(err) }
}
func TestWhenBareThrowEffects(t *testing.T) {
 app:=&App{}
 state:=WhenCounter{}
 if got,err:=app.discardChoice(t.Context(),false,state);err!=nil||got!=1 { t.Fatal(got,err) }
 state.Value=0
 if _,err:=app.discardChoice(t.Context(),true,state);!errors.Is(err,luxerrors.NotFound)||state.Value!=0 { t.Fatal(state,err) }
 if got,err:=app.rejectedChoice(t.Context(),true,state);err!=nil||got!=7||state.Value!=0 { t.Fatal(got,state,err) }
 _,err:=app.rejectedChoice(t.Context(),false,state)
 var failure *luxerrors.AppError
 if !errors.As(err,&failure)||failure.Code!=400 { t.Fatal(state,err) }
}
func TestWhenBareThrowHTTP(t *testing.T) {
 app,err:=New(t.Context())
 if err!=nil { t.Fatal(err) }
 defer app.Close()
 router:=api.NewRouterWithOptions(api.RouterOptions{})
 if err:=RegisterSchema(router.Schema);err!=nil { t.Fatal(err) }
 if err:=RegisterHandlers(router,app);err!=nil { t.Fatal(err) }
 for _,test:=range []struct{ value string; code int }{{"TODO",200},{"DONE",200},{"",422},{"unknown",422}} {
  request:=httptest.NewRequest("POST","/luvia",strings.NewReader("{\"$api\":\"chooseChoice\",\"value\":\""+test.value+"\"}"))
  request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder()
  router.ServeHTTP(response,request)
  if response.Code!=test.code { t.Fatalf("value=%q code=%d body=%s",test.value,response.Code,response.Body.String()) }
  if test.code==200 && response.Body.String()!="{\"data\":\""+test.value+"\"}" { t.Fatal(response.Body.String()) }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"WhenCounter": {"value": 1}}})
}

func TestGeneratedThrowingDefaultScopes(t *testing.T) {
	runGeneratedFixture(t, `
fn required(value: Int?): Int { return value ?: throw error.NotFound }
fn booleanRequired(value: Boolean): Boolean { return value ?: throw error.NotFound }
fn optional(value: Int?): Int? { val selected: Int? = value ?: throw error.NotFound return selected }
fn listRequired(value: [Int]?): [Int] { return value ?: throw error.NotFound }
fn shortCircuit(skip: Boolean, value: Int?): Boolean { return skip || (value ?: throw error.NotFound) > 0 }
fn chosen(skip: Boolean, value: Int?): Int { return when { skip -> 7 else -> value ?: throw error.NotFound } }
fn fallbackPair(left: Int?, right: Int?): Int { return left ?: (right ?: throw error.NotFound) }
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func TestThrowingDefaults(t *testing.T){
 app:=&App{};ctx:=t.Context();value:=int64(42)
 if got,err:=app.required(ctx,&value);err!=nil||got!=42{t.Fatal(got,err)}
 if _,err:=app.required(ctx,nil);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if got,err:=app.booleanRequired(ctx,true);err!=nil||!got{t.Fatal(got,err)}
 if _,err:=app.booleanRequired(ctx,false);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if got,err:=app.optional(ctx,&value);err!=nil||got==nil||*got!=42{t.Fatal(got,err)}
 if _,err:=app.optional(ctx,nil);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if got,err:=app.listRequired(ctx,[]int64{});err!=nil||got==nil||len(got)!=0{t.Fatal(got,err)}
 if _,err:=app.listRequired(ctx,nil);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 for _,skip:=range []bool{false,true}{
  got,err:=app.shortCircuit(ctx,skip,nil);if skip{if err!=nil||!got{t.Fatal(got,err)}}else if !errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
  selected,err:=app.chosen(ctx,skip,nil);if skip{if err!=nil||selected!=7{t.Fatal(selected,err)}}else if !errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 }
 for _,pair:=range [][2]*int64{{&value,nil},{nil,&value},{nil,nil}}{
  got,err:=app.fallbackPair(ctx,pair[0],pair[1]);if pair[0]==nil&&pair[1]==nil{if !errors.Is(err,luxerrors.NotFound){t.Fatal(err)}}else if err!=nil||got!=42{t.Fatal(got,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedNullableBooleanGuards(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { enabled: Boolean }
fn snapshotFlag(value: Snapshot): Boolean { return value.enabled }
fn bytesPresent(value: Bytes): Boolean { return true }
fn jsonPresent(value: JSON): Boolean { return true }
fn guardedSnapshot(value: Snapshot?): Boolean {
 if value != null { return snapshotFlag(value) }
 return false
}
fn guardedBytes(value: Bytes?): Boolean {
 if value != null { return bytesPresent(value) }
 return false
}
fn guardedJSON(value: JSON?): Boolean {
 if value != null { return jsonPresent(value) }
 return false
}
fn guarded(value: Boolean?): Boolean {
 if value != null { return !value }
 return false
}
api guardedIf(value: Boolean?): Boolean {
 if value != null { return !value }
 return false
}
api guardedFunction(value: Boolean?): Boolean { return guarded(value) }
api guardedAnd(value: Boolean?): Boolean { return value != null && !value }
api guardedOr(value: Boolean?): Boolean { return value == null || !value }
api guardedNot(value: Boolean?): Boolean { return !(value == null) && !value }
api guardedCopy(value: Boolean?): Boolean {
 if value != null { val copy = value return !copy }
 return false
}
api guardedShadow(value: Boolean?): Boolean {
 if value != null { val value = false if value { return true } }
 return value == null || !value
}
api guardedLoop(value: Boolean?): Boolean {
 if value != null { for value in [true, false] { if !value { return false } } }
 return false
}
api guardedMutation(value: Boolean?): Boolean {
 if value != null { value = null }
 return value == null
}
api guardedDestructure(value: Boolean?): Boolean {
 if value != null {
  val (value, other) = await { true false }
  return value && !other
 }
 return false
}
`, nullableBooleanGuardAssertions, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"enabled": 1}}})
}

const nullableBooleanGuardAssertions = `package generated
import("bytes";"encoding/json";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

func TestNarrowedValueArguments(t *testing.T){
 app:=&App{};snapshot:=Snapshot{Enabled:true};data:=[]byte{1};raw:=json.RawMessage("{}")
 for _,present:=range []bool{false,true}{
  var s *Snapshot;var d *[]byte;var r *json.RawMessage
  if present{s=&snapshot;d=&data;r=&raw}
  got,err:=app.guardedSnapshot(t.Context(),s);if err!=nil||got!=present{t.Fatal("snapshot",got,err)}
  got,err=app.guardedBytes(t.Context(),d);if err!=nil||got!=present{t.Fatal("bytes",got,err)}
  got,err=app.guardedJSON(t.Context(),r);if err!=nil||got!=present{t.Fatal("JSON",got,err)}
 }
}
func TestNullableBooleanGuards(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,name:=range []string{"guardedIf","guardedFunction","guardedAnd","guardedOr","guardedNot","guardedCopy","guardedShadow","guardedLoop","guardedMutation","guardedDestructure"}{
  for _,value:=range []any{nil,false,true}{for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("%s/%v/%v",name,value,binary),func(t *testing.T){
    text:="null";if value!=nil{text=fmt.Sprint(value)}
    body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"value\":%s}",name,text))
    if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"value":value},[]api.ParamMeta{{Name:"value",Type:"Boolean",FieldID:1,Nullable:true}});if err!=nil{t.Fatal(err)}}
    request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
    if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
    response:=httptest.NewRecorder();router.ServeHTTP(response,request)
    if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
    want:=value==false || value==nil&&(name=="guardedOr"||name=="guardedShadow")
    if name=="guardedLoop"{want=false}
    if name=="guardedMutation"{want=true}
    if name=="guardedDestructure"{want=value!=nil}
    if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadBool();if decoder.Finish()!=nil||got!=want{t.Fatalf("got=%v want=%v",got,want)}}else if !bytes.Contains(response.Body.Bytes(),[]byte(fmt.Sprintf("\"data\":%t",want))){t.Fatal(response.Body.String())}
   })
  }}
 }
}
`

func TestGeneratedLogicalShortCircuit(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn probe(value: Boolean): Result<Boolean> @native
fn checked(value: Boolean): Boolean { return probe(value)? }
api nativeAnd(enabled: Boolean): Boolean { return enabled && probe(true)? }
api nativeOr(enabled: Boolean): Boolean { return enabled || probe(false)? }
api localAnd(enabled: Boolean): Boolean { return enabled && checked(true) }
api localOr(enabled: Boolean): Boolean { return enabled || checked(false) }
api closure(enabled: Boolean): Boolean {
 return when {
  enabled -> false && checked(true)
  else -> true || checked(false)
 }
}
api grouped(enabled: Boolean): Boolean { return (enabled || true) && false }
api comparison(enabled: Boolean): Boolean { return enabled && (true == false) }
api nestedRight(enabled: Boolean): Boolean { return enabled && (checked(false) || checked(true)) }
api selectedClosure(enabled: Boolean): Boolean {
 return when {
  enabled -> enabled && checked(true)
  else -> false
 }
}
api leftFirst(enabled: Boolean): Boolean { return probe(enabled)? && probe(true)? }
`, logicalShortCircuitAssertions+logicalShortCircuitBenchmarks)
}

func TestGeneratedStringReceiverEvaluationOrder(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn text(stage: Int): Result<String> @native
fn checkedText(stage: Int): String { return text(stage)? }
api containsText(): Boolean { return checkedText(1).contains(checkedText(2)) }
api matchesText(): Boolean { return checkedText(1).matches(checkedText(2)) }
api replacesText(): Boolean { return checkedText(1).replace(checkedText(2), checkedText(3)) == "axc" }
api convertsText(): Boolean { return "123".toInt()? == 123 && "1.25".toFloat()? == 1.25 }
api templateText(): Boolean { return "${checkedText(1)}|${checkedText(2)}|${checkedText(3)}" == "abc|b|x" }
`, stringReceiverOrderAssertions)
}

const stringReceiverOrderAssertions = `package generated
import("bytes";"context";"errors";"fmt";"net/http/httptest";"slices";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type orderResolver struct{ calls []int64; failAt int64 }
func (r *orderResolver) Text(_ context.Context, stage int64)(string,error){
 r.calls=append(r.calls,stage)
 if stage==r.failAt{return "",errors.New("text failed")}
 return []string{"","abc","b","x"}[stage],nil
}
func TestStringReceiverOrder(t *testing.T){
 for _,sample:=range []struct{name string;count int64}{
  {"containsText",2},{"matchesText",2},{"replacesText",3},{"convertsText",0},{"templateText",3},
 }{for failAt:=int64(0);failAt<=sample.count;failAt++{for _,binary:=range []bool{false,true}{
  t.Run(fmt.Sprintf("%s/fail=%d/binary=%v",sample.name,failAt,binary),func(t *testing.T){
   router:=api.NewRouter();resolver:=&orderResolver{failAt:failAt}
   if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
   if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
   body:=[]byte(fmt.Sprintf("{\"$api\":%q}",sample.name))
   if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
   if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   count,status:=sample.count,200;if failAt>0{count,status=failAt,500}
   want:=[]int64{1,2,3}[:count]
   if response.Code!=status||!slices.Equal(resolver.calls,want){t.Fatalf("status=%d calls=%v want status=%d calls=%v body=%s",response.Code,resolver.calls,status,want,response.Body.String())}
   if status!=200{return}
   if binary{
    decoder:=codec.NewDecoder(response.Body.Bytes());value:=decoder.ReadBool()
    if err:=decoder.Finish();err!=nil||!value{t.Fatalf("value=%v err=%v",value,err)}
   }else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":true")){t.Fatalf("JSON: %s",response.Body.String())}
  })
 }}}
}
`

const logicalShortCircuitBenchmarks = `
func BenchmarkLogicalHandler(b *testing.B){
 for _,sample:=range []struct{name string;handler func(*App)api.HandlerFunc}{
  {"nativeAnd",handleNativeAnd},{"nativeOr",handleNativeOr},
  {"localAnd",handleLocalAnd},{"localOr",handleLocalOr},
 }{for _,enabled:=range []bool{false,true}{
  b.Run(fmt.Sprintf("%s/%v",sample.name,enabled),func(b *testing.B){
   resolver:=&logicalResolver{};handler:=sample.handler(&App{Resolver:resolver})
   request:=httptest.NewRequest("POST","/",bytes.NewBufferString(fmt.Sprintf("{\"$api\":%q,\"enabled\":%t}",sample.name,enabled)))
   req,err:=api.ParseRequest(request);if err!=nil{b.Fatal(err)};req.Buf=&api.ResponseBuf{B:make([]byte,0,16)}
   ctx:=context.Background();b.ReportAllocs();b.ResetTimer()
   for b.Loop(){req.Buf.B=req.Buf.B[:0];if err:=handler(ctx,req);err!=nil{b.Fatal(err)}}
   if len(req.Buf.B)!=1{b.Fatal("missing Boolean response")}
  })
 }}
}
`

const logicalShortCircuitAssertions = `package generated
import("bytes";"context";"errors";"fmt";"net/http/httptest";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type logicalResolver struct{ calls int; fail bool }
func (r *logicalResolver) Probe(_ context.Context,value bool)(bool,error){
 r.calls++;if r.fail{return false,errors.New("probe failure")};return value,nil
}
func TestLogicalShortCircuitHTTP(t *testing.T){
 for _,name:=range []string{"nativeAnd","nativeOr","localAnd","localOr","closure","grouped","comparison","nestedRight","selectedClosure","leftFirst"}{
  for _,enabled:=range []bool{false,true}{for _,fail:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
   t.Run(fmt.Sprintf("%s/enabled=%v/fail=%v/binary=%v",name,enabled,fail,binary),func(t *testing.T){
    testLogicalRequest(t,name,enabled,fail,binary)
   })
  }}}
 }
}
func testLogicalRequest(t *testing.T,name string,enabled,fail,binary bool){
 t.Helper();router:=api.NewRouter();resolver:=&logicalResolver{fail:fail}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 want,calls:=enabled,0
 switch name {
 case "nativeAnd","localAnd","selectedClosure":if enabled{calls=1}
 case "nativeOr","localOr":if !enabled{calls=1}
 case "closure":want=!enabled
 case "grouped","comparison":want=false
 case "nestedRight":if enabled{calls=2;if fail{calls=1}}
 case "leftFirst":calls=1;if enabled&&!fail{calls=2}
 }
 body:=[]byte(fmt.Sprintf("{\"$api\":%q,\"enabled\":%t}",name,enabled))
 if binary{var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"enabled":enabled},[]api.ParamMeta{{Name:"enabled",Type:"Boolean",FieldID:1}});if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 status:=200;if fail&&calls>0{status=500}
 if response.Code!=status||resolver.calls!=calls{t.Fatalf("status=%d calls=%d want status=%d calls=%d body=%s",response.Code,resolver.calls,status,calls,response.Body.String())}
 if status!=200{return}
 if binary{
  decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadBool()
  if err:=decoder.Finish();err!=nil||got!=want{t.Fatalf("got=%v want=%v err=%v",got,want,err)}
 }else if !bytes.Contains(response.Body.Bytes(),[]byte(fmt.Sprintf("\"data\":%t",want))){t.Fatalf("JSON: %s",response.Body.String())}
}
`

func TestGeneratedWhenListReturn(t *testing.T) {
	runGeneratedTransactionFixture(t, `
api chooseNumbers(enabled: Boolean): [Int] {
 val chosen = when {
  enabled -> [1, 9007199254740993]
  else -> [-7]
 }
 return chosen
}
`, whenListReturnAssertions)
}

const whenListReturnAssertions = `package generated
import("bytes";"fmt";"net/http/httptest";"slices";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

func TestWhenListHTTP(t *testing.T) {
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 for _,enabled:=range []bool{false,true} {for _,binary:=range []bool{false,true} {
  t.Run(fmt.Sprintf("enabled=%v/binary=%v",enabled,binary),func(t *testing.T){
   want:=[]int64{-7};wantJSON:="[-7]"
   if enabled {want=[]int64{1,9007199254740993};wantJSON="[1,9007199254740993]"}
   body:=[]byte(fmt.Sprintf("{\"$api\":\"chooseNumbers\",\"enabled\":%t}",enabled))
   if binary {
    var err error
    body,err=api.EncodeBinaryRequest(router.Schema.APIs["chooseNumbers"].ID,nil,map[string]any{"enabled":enabled},[]api.ParamMeta{{Name:"enabled",Type:"Boolean",FieldID:1}})
    if err!=nil{t.Fatal(err)}
   }
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
   if binary {request.Header.Set("X-Luxo-Mode","binary")} else {request.Header.Set("Content-Type","application/json")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
   if binary {
    decoder:=codec.NewDecoder(response.Body.Bytes());got:=decoder.ReadIntArray()
    if err:=decoder.Finish();err!=nil||!slices.Equal(got,want){t.Fatalf("got=%v want=%v err=%v",got,want,err)}
   } else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":"+wantJSON)) {t.Fatalf("JSON result: %s",response.Body.String())}
  })
 }}
}
`

func TestCompiledExpressionClosuresExecute(t *testing.T) {
	call := func() ast.Expr { return &ast.CallExpr{Func: &ast.Ident{Name: "check"}} }
	check := &ast.FnDecl{Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}
	loop := &ast.ForStmt{VarName: "i", Collection: &ast.RangeExpr{Start: &ast.Literal{Kind: token.Int, Value: "1"}, End: &ast.Literal{Kind: token.Int, Value: "2"}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call()}}}}
	loop.SetTypeTag("Int")
	loop.SetListType(true)
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{Condition: &ast.Ident{Name: "enabled"}, Body: call()}}, Else: &ast.Literal{Kind: token.Int, Value: "7"}}
	branch.SetTypeTag("Int")
	predicate := &ast.WhenExpr{Branches: []*ast.WhenBranch{
		{Condition: &ast.Ident{Name: "enabled"}, Body: &ast.Literal{Kind: token.Int, Value: "7"}},
		{Condition: &ast.CallExpr{Func: &ast.Ident{Name: "eligible"}}, Body: &ast.Literal{Kind: token.Int, Value: "9"}},
	}, Else: &ast.Literal{Kind: token.Int, Value: "11"}}
	predicate.SetTypeTag("Int")
	var generated strings.Builder
	for _, fn := range []*ast.FnDecl{
		{Name: "collect", ReturnType: &ast.TypeRef{Name: "Int", IsList: true}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: loop}}}},
		{Name: "choose", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: branch}}}},
		{Name: "choosePredicate", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: predicate}}}},
	} {
		defaultGenerator().compileLocalFunction(&generated, fn, nil, nil, nil, map[string]*ast.FnDecl{"check": check,
			"eligible": {Name: "eligible", ReturnType: &ast.TypeRef{Name: "Boolean"}, Body: &ast.Block{}},
		}, nil)
	}
	generated.WriteString(compiledNativeSelect())
	runGeneratedClosureTest(t, generated.String(), `
func TestRun(t *testing.T) {
 app := &App{}
 app.Resolver = &resolver{app: app}
 ch := make(chan int64, 1)
 ch <- 5
 if value, err := app.pick(context.Background(), ch); err != nil || value != 3 { t.Fatalf("native select: %v %v", value, err) }
 if value, err := app.pick(context.Background(), ch); err != nil || value != 0 { t.Fatalf("select default: %v %v", value, err) }
 app.calls = 0
 values, err := app.collect(context.Background())
 if err != nil || len(values) != 2 || app.calls != 2 { t.Fatalf("collect: %v %v calls=%d", values, err, app.calls) }
 app.calls = 0
 value, err := app.choose(context.Background(), false)
 if err != nil || value != 7 || app.calls != 0 { t.Fatalf("inactive branch ran: %v %v calls=%d", value, err, app.calls) }
 if allocations := testing.AllocsPerRun(100, func() { _, _ = app.choose(context.Background(), true) }); allocations != 0 { t.Fatalf("value closure allocated: %v", allocations) }
 app.fail = true
 app.calls = 0
 if value, err := app.choosePredicate(context.Background(), true); value != 7 || err != nil || app.calls != 0 { t.Fatalf("inactive predicate ran: %v %v", value, err) }
 if _, err := app.choosePredicate(context.Background(), false); err != sentinel { t.Fatalf("predicate error: %v", err) }
 if _, err := app.collect(context.Background()); err != sentinel { t.Fatalf("collect error: %v", err) }
 if _, err := app.choose(context.Background(), true); err != sentinel { t.Fatalf("branch error: %v", err) }
 ch <- 5
 if _, err := app.pick(context.Background(), ch); err != sentinel { t.Fatalf("native select error: %v", err) }
}
`)
}

func compiledNativeSelect() string {
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	c.nativeFunctions = map[string]bool{"nativeCheck": true}
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{
		Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch"}},
		Body: &ast.LambdaExpr{Params: []string{"message"}, Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.AssignStmt{Target: &ast.Ident{Name: "_"}, Op: "=", Value: &ast.Ident{Name: "message"}},
			&ast.ReturnStmt{Value: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{Func: &ast.Ident{Name: "nativeCheck"}}}},
		}}},
	}}, Else: &ast.Literal{Kind: token.Int, Value: "0"}}
	branch.SetTypeTag("Int")
	c.compileReturn(&ast.ReturnStmt{Value: branch})
	return "func (app *App) pick(ctx context.Context, ch <-chan int64) (_value int64, err error) {\n" + compilerOut(c) + "}\n"
}

func runGeneratedClosureTest(t *testing.T, generated, assertions string) {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "closure-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	source := `package closuretest
import ("context"; "errors"; "testing")
var sentinel = errors.New("expected failure")
type App struct { calls int; fail bool; Resolver *resolver; entered chan struct{}; release chan struct{} }
type resolver struct { app *App }
func (r *resolver) NativeCheck(ctx context.Context) (int64, error) { return r.app.check(ctx) }
func (app *App) check(ctx context.Context) (int64, error) { app.calls++; if app.fail { return 0, sentinel }; return 3, nil }
func (app *App) eligible(ctx context.Context) (bool, error) { app.calls++; if app.fail { return false, sentinel }; return true, nil }
` + generated + assertions
	if strings.Contains(generated, "errgroup.") {
		source = strings.Replace(source, `import (`, `import ("golang.org/x/sync/errgroup";`, 1)
	}
	parsed, parseErr := goparser.ParseFile(gotoken.NewFileSet(), "closure_test.go", source, 0)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	usesAPI := false
	goast.Inspect(parsed, func(node goast.Node) bool {
		if selector, ok := node.(*goast.SelectorExpr); ok {
			if root, ok := selector.X.(*goast.Ident); ok && root.Name == "api" {
				usesAPI = true
			}
		}
		return true
	})
	if usesAPI {
		source = strings.Replace(source, `import (`, `import ("github.com/light-speak/luxo/pkg/lux/api";`, 1)
	}
	if strings.Contains(assertions, "time.") {
		source = strings.Replace(source, `import (`, `import ("time";`, 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "closure_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-timeout=30s", "-count=1", ".")
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated execution: %v\n%s\n%s", err, output, source)
	}
}

func TestAwaitFunctionCallsRunInsideParallelTasks(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "waitCheck"}}
	call.SetTypeTag("Int")
	one, two := &ast.Literal{Kind: token.Int, Value: "1"}, &ast.Literal{Kind: token.Int, Value: "2"}
	one.SetTypeTag("Int")
	two.SetTypeTag("Int")
	fn := &ast.FnDecl{Name: "parallel", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Names: []string{"a", "b"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call}, &ast.ExprStmt{Expr: call}}}}},
		&ast.ValStmt{Names: []string{"c", "d"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: one}, &ast.ExprStmt{Expr: two}}}}},
		&ast.ReturnStmt{Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "a"}, Op: "+", Right: &ast.Ident{Name: "b"}}},
	}}}
	var body strings.Builder
	functions := map[string]*ast.FnDecl{"waitCheck": {Name: "waitCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	failure := &ast.CallExpr{Func: &ast.Ident{Name: "failCheck"}}
	failure.SetTypeTag("Int")
	functions["failCheck"] = &ast.FnDecl{Name: "failCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}
	fn.Name = "parallelFailure"
	fn.Body.Stmts[0].(*ast.ValStmt).Value.(*ast.AwaitExpr).Body.Stmts[1] = &ast.ExprStmt{Expr: failure}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	if strings.Contains(body.String(), " any") {
		t.Fatalf("typed await lost its value types:\n%s", body.String())
	}
	runGeneratedClosureTest(t, body.String(), `
func (app *App) waitCheck(ctx context.Context) (int64, error) {
 app.entered <- struct{}{}
 select { case <-app.release: return 3, nil; case <-ctx.Done(): return 0, ctx.Err() }
}
func (app *App) failCheck(ctx context.Context) (int64, error) {
 select { case <-app.entered: return 0, sentinel; case <-ctx.Done(): return 0, ctx.Err() }
}
func TestAwait(t *testing.T) {
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 app := &App{entered: make(chan struct{}, 2), release: make(chan struct{})}
 done := make(chan error, 1)
 go func() { value, err := app.parallel(ctx); if err == nil && value != 6 { err = errors.New("incorrect result") }; done <- err }()
 timer := time.NewTimer(time.Second)
 defer timer.Stop()
 for range 2 { select { case <-app.entered: case <-timer.C: t.Fatal("await serialized function calls") } }
 close(app.release)
 if err := <-done; err != nil { t.Fatal(err) }
}
func TestAwaitFailureCancelsSibling(t *testing.T) {
 ctx, cancel := context.WithTimeout(context.Background(), time.Second)
 defer cancel()
 app := &App{entered: make(chan struct{}, 1), release: make(chan struct{})}
 if _, err := app.parallelFailure(ctx); err != sentinel { t.Fatalf("lost task error: %v", err) }
 if ctx.Err() != nil { t.Fatal("sibling waited for the parent deadline instead of group cancellation") }
}
`)
}

func TestCompiledReturnBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		result *ast.TypeRef
		value  ast.Expr
		want   string
	}{
		{"void", nil, nil, "return nil"},
		{"list", &ast.TypeRef{Name: "Int", IsList: true}, &ast.ListExpr{Items: []ast.Expr{&ast.Literal{Kind: token.Int, Value: "1"}}}, "return []int64{1}, nil"},
		{"model", &ast.TypeRef{Name: "User"}, &ast.ObjectExpr{TypeName: "User"}, "return &User{}, nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newCompiler(makeModels("User"))
			c.inFunction, c.functionResult = true, test.result
			c.compileReturn(&ast.ReturnStmt{Value: test.value})
			if !strings.Contains(compilerOut(c), test.want) {
				t.Fatalf("return: %s", compilerOut(c))
			}
		})
	}
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	async := c.compileAsync(&ast.AsyncExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}, &ast.ReturnStmt{},
	}}})
	if !strings.Contains(async, "luxolog.Error((sentinel).Error())") || strings.Contains(async, "return _value") || strings.Contains(async, "return nil") {
		t.Fatalf("async ABI: %s", async)
	}
	transaction := c.compileTransaction(&ast.TransactionExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}}}})
	if !strings.Contains(transaction, "return sentinel") || strings.Contains(transaction, "return _value") {
		t.Fatalf("transaction ABI: %s", transaction)
	}
	c.valueResult = &ast.TypeRef{Name: "Int", Nullable: true}
	c.valueModel = true
	sub := c.valueCompiler()
	if sub.valueResult != nil || sub.valueModel || c.valueResult == nil || !c.valueModel {
		t.Fatal("nested value compiler inherited or cleared its parent's result ABI")
	}
	sub.compileReturn(&ast.ReturnStmt{})
	if !strings.Contains(sub.b.String(), "return nil") {
		t.Fatalf("nullable closure ABI: %s", sub.b.String())
	}
}

func TestYieldResultPointerRepresentation(t *testing.T) {
	c := newCompiler(makeModels("Item"))
	if c.yieldNeedsAddress(nil) {
		t.Fatal("absent result acquired an address")
	}
	for _, tc := range []struct {
		name           string
		nullable, list bool
		want           bool
	}{
		{"Int", false, false, false},
		{"Int", true, true, false},
		{"Item", true, false, false},
		{"Bytes", true, false, true},
		{"JSON", true, false, true},
		{"Leaf", true, false, true},
	} {
		expr := &ast.ForStmt{TypeTagged: ast.TypeTagged{TypeTag: tc.name, NullableTag: tc.nullable, ListTypeTag: tc.list}}
		if got := c.yieldNeedsAddress(expr); got != tc.want {
			t.Fatalf("%+v: address=%v want=%v", tc, got, tc.want)
		}
	}
}

func TestFallibleWhenSubjectWithoutElse(t *testing.T) {
	c := newCompiler(nil)
	c.functions = map[string]*ast.FnDecl{"check": {Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}}
	expression := &ast.WhenExpr{Subject: &ast.Ident{Name: "value"}, Branches: []*ast.WhenBranch{{
		Condition: &ast.CallExpr{Func: &ast.Ident{Name: "check"}}, Body: &ast.Literal{Kind: token.Int, Value: "1"},
	}}}
	expression.SetTypeTag("Int")
	c.compileWhen(expression)
	code := compilerOut(c)
	for _, want := range []string{"_subject := value", "if _subject == _result", "return 0", "return _closureErr"} {
		if !strings.Contains(code, want) {
			t.Fatalf("missing %q: %s", want, code)
		}
	}
	if got := c.compileLiteral(&ast.Literal{Kind: token.Duration, Value: "broken"}); got != "broken" {
		t.Fatalf("invalid duration recovery = %q", got)
	}
}

func TestFormalValueBlocksExecute(t *testing.T) {
	const source = `
fn chooseBlock(flag: Boolean): Int {
 return when { flag -> { val value = 1 value } else -> { return 2 val unreachable = 3 } }
}
fn mapBlock(items: [Int]): [Int] {
 return for item in items { val value = item value + 1 }
}
fn sumStatements(items: [Int]): Int {
 var total = 0
 for item in items { total += item }
 return total
}
`
	runGeneratedFixture(t, source, `package generated
import("testing";"slices")

func TestValueBlocks(t *testing.T) {
 app:=&App{}
 for _, flag:= range []bool{false,true} {
  value,err:=app.chooseBlock(t.Context(),flag)
  want:=int64(2);if flag {want=1}
  if err!=nil || value!=want {t.Fatal(value,err)}
 }
 values,err:=app.mapBlock(t.Context(),[]int64{1,2})
 if err!=nil || !slices.Equal(values,[]int64{2,3}) {t.Fatal(values,err)}
 total,err:=app.sumStatements(t.Context(),[]int64{1,2})
 if err!=nil || total!=3 {t.Fatal(total,err)}
}
`, StableIDs{})
}

func TestFormalGenerationRejectsMissingValueResults(t *testing.T) {
	for _, source := range []string{
		`fn broken(flag: Boolean): Int { return when { flag -> 1 else -> { val value = 2 } } }`,
		`fn broken(items: [Int]): [Int] { return for item in items { val value = item } }`,
	} {
		tokens, lexErrors := lexer.New(source, "missing-result.luxo").Tokenize()
		if len(lexErrors) != 0 {
			t.Fatal(lexErrors)
		}
		file, parseErrors := parser.New(tokens).Parse("missing-result.luxo")
		if len(parseErrors) != 0 {
			t.Fatal(parseErrors)
		}
		result := semantic.Analyze([]*ast.File{file})
		generated, err := Generate(result, "generated", GeneratorConfig{IDs: completeFixtureAPIIDs(result, StableIDs{})})
		if err == nil || generated != nil || !strings.Contains(err.Error(), "must produce a value") {
			t.Fatalf("missing value: generated=%v error=%v", generated != nil, err)
		}
	}
}

func TestCompiledStaticFunctionDependencies(t *testing.T) {
	source := `fn normalize(value: String, suffix: String = "!"): String { return value.trim() + suffix }
fn checked(value: String): String { if value == "" { throw error.NotFound } return normalize(value) }
fn named(value: String): String { return normalize(suffix: "?", value: value) }
fn mapped(values: [String]): [String] { return values.map { value -> checked(value) } }
fn nested(value: String): String { return checked(value) }
fn constant(): String { return "constant" }
fn validate(value: String) { if value == "" { throw error.NotFound } }
fn validated(value: String): String { validate(value) return value }
fn noargs(): String { return constant() }`
	tokens, lexErrors := lexer.New(source, "static.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("static.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	functions := make(map[string]*ast.FnDecl)
	targets := make(map[string]string)
	for _, fn := range file.Functions {
		functions[fn.Name] = fn
		targets[fn.Name] = "field_" + fn.Name
	}
	var body strings.Builder
	for _, fn := range file.Functions {
		defaultGenerator().compileStaticFunction(&body, fn, nil, nil, functions, targets, targets[fn.Name])
	}
	if strings.Contains(body.String(), "app.") || strings.Contains(body.String(), "context.") {
		t.Fatalf("static helper retained request dependencies: %s", body.String())
	}
	runGeneratedProtocolPackage(t, `package generated
import("strings";"github.com/light-speak/luxo/pkg/lux/errors")
`+body.String(), `package generated
import("testing";"slices";goerrors "errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestStaticFunctions(t *testing.T){
 if got,err:=field_noargs();err!=nil||got!="constant"{t.Fatal(got,err)}
 if got,err:=field_validated("ok");err!=nil||got!="ok"{t.Fatal(got,err)}
 if got,err:=field_validated("");!goerrors.Is(err,luxerrors.NotFound)||got!=""{t.Fatal(got,err)}
 if got,err:=field_nested(" value ");err!=nil||got!="value!"{t.Fatal(got,err)}
 if got,err:=field_named(" value ");err!=nil||got!="value?"{t.Fatal(got,err)}
 if got,err:=field_nested("");!goerrors.Is(err,luxerrors.NotFound)||got!=""{t.Fatal(got,err)}
 if got,err:=field_mapped([]string{" a ","b"});err!=nil||!slices.Equal(got,[]string{"a!","b!"}){t.Fatal(got,err)}
 if _,err:=field_mapped([]string{"ok",""});!goerrors.Is(err,luxerrors.NotFound){t.Fatal(err)}
}
`)
}

func TestCompiledFieldDirectiveFunctions(t *testing.T) {
	source := `fn normalize(value: String, suffix: String = "!"): String {
 if value == "" { throw error.NotFound }
 return value.trim() + suffix
}
model User @noTime {
 id: Int @id
 name: String? @transform { normalize(it, suffix: "?") } @beforeSave { normalize(it) normalize(it, suffix: "?") }
 score: Int @beforeSave { it += 2 it *= 3 }
 date: DateTime @beforeSave { it += 1s it -= 2s }
}`
	tokens, lexErrors := lexer.New(source, "fields.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("fields.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	functions := map[string]*ast.FnDecl{"normalize": file.Functions[0]}
	targets := map[string]string{"normalize": "field_normalize"}
	var body strings.Builder
	defaultGenerator().compileStaticFunction(&body, file.Functions[0], nil, nil, functions, targets, targets[file.Functions[0].Name])
	for _, field := range file.Models[0].Fields {
		for _, directive := range field.Directives {
			if directive.Name != "transform" && directive.Name != "beforeSave" {
				continue
			}
			name := field.Name + "_" + directive.Name
			fn := fieldDirectiveFunction(field, directive, name)
			targets[name] = name
			defaultGenerator().compileStaticFunction(&body, fn, nil, nil, functions, targets, targets[fn.Name])
		}
	}
	if !file.Models[0].Fields[1].Type.Nullable {
		t.Fatal("changed original nullable field")
	}
	if _, ok := file.Models[0].Fields[1].Directives[1].Body.Stmts[0].(*ast.ExprStmt); !ok {
		t.Fatal("changed original directive")
	}
	runGeneratedProtocolPackage(t, `package generated
import("strings";"time";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/errors")
`+body.String(), `package generated
import("testing";"time";goerrors "errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestFieldFunctions(t *testing.T){
 if got,err:=name_transform(" value ");err!=nil||got!="value?"{t.Fatal(got,err)}
 if got,err:=name_beforeSave(" value ");err!=nil||got!="value!?"{t.Fatal(got,err)}
 if got,err:=name_transform("");!goerrors.Is(err,luxerrors.NotFound)||got!=""{t.Fatal(got,err)}
 if got,err:=name_beforeSave("");!goerrors.Is(err,luxerrors.NotFound)||got!=""{t.Fatal(got,err)}
 if got,err:=score_beforeSave(4);err!=nil||got!=18{t.Fatal(got,err)}
 if got,err:=date_beforeSave(time.Unix(10,123));err!=nil||!got.Equal(time.Unix(9,123)){t.Fatal(got,err)}
}
`)
}

func TestFieldDirectiveFunctionRejectsInvalidBodies(t *testing.T) {
	value := &ast.Ident{Name: "it"}
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: value}}}
	cases := []struct {
		name      string
		field     *ast.FieldDecl
		directive *ast.Directive
	}{
		{"type", &ast.FieldDecl{Name: "value"}, &ast.Directive{Name: "transform", Body: body}},
		{"body", nil, &ast.Directive{Name: "transform"}},
		{"empty", nil, &ast.Directive{Name: "transform", Body: &ast.Block{}}},
		{"arguments", nil, &ast.Directive{Name: "transform", Args: []*ast.NamedArg{{Value: value}}, Body: body}},
		{"transform-statement", nil, &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: value}}}}},
		{"transform-nil", nil, &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{}}}}},
		{"transform-many", nil, &ast.Directive{Name: "transform", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: value}, &ast.ExprStmt{Expr: value}}}}},
		{"save-target", nil, &ast.Directive{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "other"}, Op: "=", Value: value}}}}},
		{"directive", nil, &ast.Directive{Name: "visible", Body: body}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.field == nil {
				tc.field = &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "String"}}
			}
			defer func() {
				if _, ok := recover().(*InternalCompilerError); !ok {
					t.Fatal("invalid field function accepted")
				}
			}()
			fieldDirectiveFunction(tc.field, tc.directive, "field_value")
		})
	}
}

func TestFieldDirectiveFunctionUsesPresentList(t *testing.T) {
	field := &ast.FieldDecl{Name: "values", Type: &ast.TypeRef{Name: "Int", IsList: true, Nullable: true}}
	directive := &ast.Directive{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "it"}}}}}
	fn := fieldDirectiveFunction(field, directive, "save_values")
	if fn.ReturnType.Nullable || !fn.ReturnType.IsList || fn.ReturnType == field.Type || !field.Type.Nullable {
		t.Fatal("list type was changed or shared")
	}
	returned := fn.Body.Stmts[len(fn.Body.Stmts)-1].(*ast.ReturnStmt).Value
	if !returned.IsListType() || returned.IsNullable() {
		t.Fatal("return lost list type")
	}
}
