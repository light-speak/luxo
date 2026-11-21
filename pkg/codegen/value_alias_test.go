package codegen

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

const valueAliasCommon = `type box<T> { value: T writeLuxoDepth: Int writeLuxoDepth_: Int }
type PublicBox<T> { value: T }
type packet { value: box<Int> }
sealed choice { Done(value: box<Int>) Missing }
`

const valueAliasConsumer = `type Local { value: Int }
fn echoNumber(value: box<Int>): box<Int> { return value }
fn echoText(value: box<String>): box<String> { return value }
fn echoPublic(value: PublicBox<Int>): PublicBox<Int> { return value }
fn echoPacket(value: packet): packet { return value }
fn echoChoice(value: choice): choice { return value }
fn echoLocal(value: Local): Local { return value }
`

func analyzeValueAliasModules(t *testing.T, common, consumer string) (*semantic.Result, []*ast.File) {
	t.Helper()
	files := []*ast.File{
		parseStreamEnumSource(t, "origin/consumer/main.luxo", consumer),
		parseStreamEnumSource(t, "origin/common/value.luxo", common),
	}
	input := semantic.AnalyzeModules(files)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	view, err := semantic.CompilationView(input)
	if err != nil {
		t.Fatal(err)
	}
	return view, files
}

func valueAliasIDs(view *semantic.Result) StableIDs {
	ids := StableIDs{ModelFields: make(map[string]map[string]int)}
	for _, file := range view.Files {
		for _, declaration := range file.Types {
			fields := make(map[string]int)
			for index, field := range declaration.Fields {
				fields[field.Name] = index + 1
			}
			ids.ModelFields[declaration.Name] = fields
		}
		for _, declaration := range file.Sealeds {
			tags := make(map[string]int)
			for index, variant := range declaration.Variants {
				tags[variant.Name] = index + 1
				fields := make(map[string]int)
				for index, field := range variant.Fields {
					fields[field.Name] = index + 1
				}
				ids.ModelFields[declaration.Name+"."+variant.Name] = fields
			}
			ids.ModelFields[declaration.Name] = tags
		}
	}
	return ids
}

func valueAliasFunctionType(t *testing.T, view *semantic.Result, name string) string {
	t.Helper()
	for _, function := range view.Files[0].Functions {
		if function.Name == name {
			return function.ReturnType.Name
		}
	}
	t.Fatal("missing consumer function", name)
	return ""
}

func writeValueAliasPackages(t *testing.T, view *semantic.Result, files []*ast.File, assertions string) {
	t.Helper()
	dir := writeProtocolFixture(t, "package generated\n", "package generated\n")
	module := "github.com/light-speak/luxo/.tmp/" + filepath.Base(dir)
	context := BuildEventContext(files, module)
	ids := valueAliasIDs(view)
	before, err := json.Marshal(view.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"common", "consumer"} {
		selected := *view
		if owner == "common" {
			selected.Files = view.Files[1:2]
		}
		generated, err := Generate(&selected, "luxo", GeneratorConfig{IDs: ids, Events: context})
		if err != nil {
			t.Fatal(owner, err)
		}
		writeStreamEnumPackage(t, filepath.Join(dir, owner, "luxo"), generated.Files)
	}
	after, err := json.Marshal(view.Files)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("generation changed compilation AST", err)
	}
	assertions = strings.ReplaceAll(assertions, "MODULE_PATH", module)
	if err := os.WriteFile(filepath.Join(dir, "consumer", "luxo", "alias_test.go"), []byte(assertions), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-race", "-count=1", "-timeout=90s", "./...")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-package aliases: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
}

func TestGeneratedCrossModuleValueAndSealedAliases(t *testing.T) {
	view, files := analyzeValueAliasModules(t, valueAliasCommon, valueAliasConsumer)
	if view.CompiledValueDeclarations()["packet"].Declaration == nil {
		t.Fatal("ordinary wrapper with a concrete generic field lost canonical ownership")
	}
	number := valueAliasFunctionType(t, view, "echoNumber")
	text := valueAliasFunctionType(t, view, "echoText")
	public := valueAliasFunctionType(t, view, "echoPublic")
	choice := "LuxoPrivateType_" + hex.EncodeToString([]byte("choice"))
	assertions := strings.NewReplacer("NUMBER_TYPE", number, "TEXT_TYPE", text, "PUBLIC_TYPE", public, "CHOICE_TYPE", choice).Replace(valueAliasAssertions)
	writeValueAliasPackages(t, view, files, assertions)
}

const valueAliasAssertions = `package luxo
import("errors";"testing";common "MODULE_PATH/common/luxo";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestAliasIdentityAndCodecs(t *testing.T){
 var owned common.NUMBER_TYPE=NUMBER_TYPE{Value:42,WriteLuxoDepth:3,WriteLuxoDepth_:4}
 var value NUMBER_TYPE=owned
 var public common.PUBLIC_TYPE=PUBLIC_TYPE{Value:9};if public.Value!=9{t.Fatal(public)}
 var text common.TEXT_TYPE=TEXT_TYPE{Value:"hello"};if text.Value!="hello"{t.Fatal(text)}
 var ownedPacket common.LuxoPrivateType_7061636b6574=packet{Value:&value};var packetValue packet=ownedPacket
 var buf api.ResponseBuf;if err:=value.WriteLuxoDepth__(&buf,nil,1);err!=nil{t.Fatal(err)}
 var restored NUMBER_TYPE;decoder:=codec.NewDecoder(buf.B);restored.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||restored!=value{t.Fatal(restored,err)}
 values,err:=ReadJSONInputNUMBER_TYPEList([]byte("[{\"value\":42,\"writeLuxoDepth\":3,\"writeLuxoDepth_\":4}]"));if err!=nil||len(values)!=1||values[0]!=value{t.Fatal(values,err)}
 buf.B=nil;if err:=WriteColumnarNUMBER_TYPE(&buf,values,nil);err!=nil{t.Fatal(err)};wire:=append([]byte(nil),buf.B...)
 rows,err:=ReadColumnarNUMBER_TYPE(wire);if err!=nil||len(rows)!=1||rows[0]!=value{t.Fatal(rows,err)}
 rows,err=ReadColumnarNUMBER_TYPEDepth(wire,1);if err!=nil||len(rows)!=1{t.Fatal(rows,err)}
 buf.B=nil;if err:=WriteColumnarNUMBER_TYPEDepth(&buf,values,nil,1);err!=nil{t.Fatal(err)}
 if err:=WriteColumnarNUMBER_TYPEDepth(&buf,values,nil,codec.MaxModelDepth+1);!errors.Is(err,codec.ErrModelDepth){t.Fatal("writer lost depth",err)}
 if _,err:=ReadColumnarNUMBER_TYPEDepth(wire,codec.MaxModelDepth+1);!errors.Is(err,codec.ErrModelDepth){t.Fatal("reader lost depth",err)}
 buf.B=nil;if err:=packetValue.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)}
 var packetRestored packet;decoder=codec.NewDecoder(buf.B);packetRestored.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||packetRestored.Value==nil||*packetRestored.Value!=value{t.Fatal(packetRestored,err)}
 packets,err:=ReadJSONInputpacketList([]byte("[{\"value\":{\"value\":42,\"writeLuxoDepth\":3,\"writeLuxoDepth_\":4}}]"));if err!=nil||len(packets)!=1||packets[0].Value==nil||*packets[0].Value!=value{t.Fatal(packets,err)}
 buf.B=nil;if err:=WriteColumnarpacket(&buf,packets,nil);err!=nil{t.Fatal(err)};packetWire:=append([]byte(nil),buf.B...)
 if rows,err:=ReadColumnarpacket(packetWire);err!=nil||len(rows)!=1||rows[0].Value==nil||*rows[0].Value!=value{t.Fatal(rows,err)}
 if rows,err:=ReadColumnarpacketDepth(packetWire,1);err!=nil||len(rows)!=1||rows[0].Value==nil||*rows[0].Value!=value{t.Fatal(rows,err)}
 buf.B=nil;if err:=WriteColumnarpacketDepth(&buf,packets,nil,1);err!=nil{t.Fatal(err)}
 if err:=WriteColumnarpacketDepth(&buf,packets,nil,codec.MaxModelDepth+1);!errors.Is(err,codec.ErrModelDepth){t.Fatal(err)}
 if _,err:=ReadColumnarpacketDepth(packetWire,codec.MaxModelDepth+1);!errors.Is(err,codec.ErrModelDepth){t.Fatal(err)}
 var selected choice;if err:=selected.ReadJSONInput([]byte("{\"Done\":{\"value\":{\"value\":42,\"writeLuxoDepth\":3,\"writeLuxoDepth_\":4}}}"));err!=nil{t.Fatal(err)}
 var canonical common.CHOICE_TYPE=selected;selected=canonical
 buf.B=nil;if err:=selected.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)};var decoded choice;decoder=codec.NewDecoder(buf.B);decoded.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||decoded.LuxoVariantTag()!=1{t.Fatal(decoded,err)}
 choices,err:=ReadJSONInputchoiceList([]byte("[{\"Missing\":{}}]"));if err!=nil||len(choices)!=1{t.Fatal(choices,err)}
 buf.B=nil;if err:=WriteColumnarchoice(&buf,choices,nil);err!=nil{t.Fatal(err)};wire=append([]byte(nil),buf.B...)
 if rows,err:=ReadColumnarchoice(wire);err!=nil||len(rows)!=1||rows[0].LuxoVariantTag()!=2{t.Fatal(rows,err)}
 if rows,err:=ReadColumnarchoiceDepth(wire,1);err!=nil||len(rows)!=1{t.Fatal(rows,err)}
 buf.B=nil;if err:=WriteColumnarchoiceDepth(&buf,choices,nil,1);err!=nil{t.Fatal(err)}
 if err:=WriteColumnarchoiceDepth(&buf,choices,nil,codec.MaxModelDepth);!errors.Is(err,codec.ErrModelDepth){t.Fatal("sealed depth lost",err)}
 local:=Local{Value:7};if local.Value!=7{t.Fatal(local)}
}
`

func valueAliasCollisions(t *testing.T) (*semantic.Result, []*ast.File, string) {
	t.Helper()
	initial, _ := analyzeValueAliasModules(t, valueAliasCommon, valueAliasConsumer)
	number := valueAliasFunctionType(t, initial, "echoNumber")
	public := "LuxoPrivateType_" + hex.EncodeToString([]byte("choice"))
	var declarations strings.Builder
	for _, name := range []string{public, "ReadColumnar" + number + "Depth", "WriteColumnar" + number + "Depth"} {
		fmt.Fprintf(&declarations, "type %s { marker: Int }\nfn %s_(): Int { return 1 }\n", name, name)
	}
	view, files := analyzeValueAliasModules(t, valueAliasCommon, valueAliasConsumer+declarations.String())
	return view, files, public
}

func TestGeneratedCrossModuleAliasNamespaceCollisions(t *testing.T) {
	view, files, public := valueAliasCollisions(t)
	number := valueAliasFunctionType(t, view, "echoNumber")
	text := valueAliasFunctionType(t, view, "echoText")
	publicBox := valueAliasFunctionType(t, view, "echoPublic")
	assertions := strings.ReplaceAll(valueAliasAssertions, "NUMBER_TYPEDepth", "NUMBER_TYPEDepth__")
	assertions = strings.NewReplacer("NUMBER_TYPE", number, "TEXT_TYPE", text, "PUBLIC_TYPE", publicBox, "CHOICE_TYPE", public+"__").Replace(assertions)
	writeValueAliasPackages(t, view, files, assertions)
}

func TestCompiledValueAliasFilteringKeepsLocalDeclarations(t *testing.T) {
	view, files := analyzeValueAliasModules(t, valueAliasCommon, valueAliasConsumer)
	configuration := GeneratorConfig{IDs: valueAliasIDs(view), Events: BuildEventContext(files, "example.com/alias")}
	g, projected, err := mustNewGenerator(t, configuration).prepareValueCompilation(view)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(projected.Files)
	if err != nil {
		t.Fatal(err)
	}
	filtered := g.withoutValueAliases(projected)
	if filtered == projected || len(filtered.Files) != len(projected.Files) {
		t.Fatal("alias filtering did not produce an independent file view")
	}
	found := false
	for _, file := range filtered.Files {
		for _, declaration := range file.Types {
			if declaration.Name == "Local" {
				found = true
			}
			if g.valueAliasOwner(declaration.Name) != "" {
				t.Fatal("foreign declaration was regenerated", declaration.Name)
			}
		}
	}
	after, err := json.Marshal(projected.Files)
	if !found || err != nil || !bytes.Equal(before, after) {
		t.Fatal("filter removed local type or changed input", err)
	}
}

func TestGeneratedCrossModuleSealedOnlyAliases(t *testing.T) {
	common := `sealed choice { Done(value: Int) Missing } sealed PublicChoice { Ready(value: String) Missing }`
	consumer := `fn echoChoice(value: choice): choice { return value } fn echoPublic(value: PublicChoice): PublicChoice { return value }`
	view, files := analyzeValueAliasModules(t, common, consumer)
	assertions := strings.ReplaceAll(sealedOnlyAliasAssertions, "CHOICE_TYPE", "LuxoPrivateType_"+hex.EncodeToString([]byte("choice")))
	writeValueAliasPackages(t, view, files, assertions)
}

func TestGeneratedCrossModuleRecursiveSealedAlias(t *testing.T) {
	common := `sealed chain { Next(value: chain?) End }`
	consumer := `fn echoChain(value: chain): chain { return value }`
	view, files := analyzeValueAliasModules(t, common, consumer)
	writeValueAliasPackages(t, view, files, recursiveSealedAliasAssertions)
}

const recursiveSealedAliasAssertions = `package luxo
import("testing";common "MODULE_PATH/common/luxo";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestRecursiveSealedCanonicalIdentity(t *testing.T){
 end:=(chain{}).NewVariant2();var canonical common.LuxoPrivateType_636861696e=(chain{}).NewVariant1(&end);var value chain=canonical
 if value.AsVariant1()==nil||value.AsVariant1().F1==nil||value.AsVariant1().F1.LuxoVariantTag()!=2{t.Fatal(value)}
 var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)}
 var restored chain;decoder:=codec.NewDecoder(buf.B);restored.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)}
 if equal,err:=canonical.LuxoEqual(restored);err!=nil||!equal{t.Fatal("recursive binary alias",equal,err)}
 var fromJSON chain;if err:=fromJSON.ReadJSONInput([]byte("{\"Next\":{\"value\":{\"End\":{}}}}"));err!=nil{t.Fatal(err)}
 if equal,err:=fromJSON.LuxoEqual(canonical);err!=nil||!equal{t.Fatal("recursive JSON alias",equal,err)}
 left,right:=(chain{}).NewVariant1(nil),(chain{}).NewVariant1(nil);left.AsVariant1().F1=&left;right.AsVariant1().F1=&right
 var ownedCycle common.LuxoPrivateType_636861696e=left;if equal,err:=ownedCycle.LuxoEqual(right);err!=nil||!equal{t.Fatal("recursive equality alias",equal,err)}
 if equal,err:=ownedCycle.LuxoEqual(end);err!=nil||equal{t.Fatal("variant mismatch",equal,err)}
}
`

const sealedOnlyAliasAssertions = `package luxo
import("errors";"testing";common "MODULE_PATH/common/luxo";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSealedOnlyCanonicalIdentity(t *testing.T){
 var canonical common.CHOICE_TYPE=(choice{}).NewVariant1(42);var value choice=canonical
 if value.AsVariant1()==nil||value.AsVariant1().F1!=42{t.Fatal(value)}
 var public common.PublicChoice=(PublicChoice{}).NewVariant1("text");var alias PublicChoice=public
 if alias.AsVariant1()==nil||alias.AsVariant1().F1!="text"{t.Fatal(alias)}
 var buf api.ResponseBuf;if err:=value.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)}
 var restored choice;decoder:=codec.NewDecoder(buf.B);restored.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil||restored.AsVariant1().F1!=42{t.Fatal(restored,err)}
 list,err:=ReadJSONInputchoiceList([]byte("[{\"Done\":{\"value\":42}}]"));if err!=nil||len(list)!=1{t.Fatal(list,err)}
 buf.B=nil;if err:=WriteColumnarchoice(&buf,list,nil);err!=nil{t.Fatal(err)};wire:=append([]byte(nil),buf.B...)
 if decoded,err:=ReadColumnarchoice(wire);err!=nil||len(decoded)!=1{t.Fatal(decoded,err)}
 if decoded,err:=ReadColumnarchoiceDepth(wire,1);err!=nil||len(decoded)!=1{t.Fatal(decoded,err)}
 buf.B=nil;if err:=WriteColumnarchoiceDepth(&buf,list,nil,1);err!=nil{t.Fatal(err)}
 if err:=WriteColumnarchoiceDepth(&buf,list,nil,codec.MaxModelDepth);!errors.Is(err,codec.ErrModelDepth){t.Fatal(err)}
 if _,err:=ReadColumnarchoiceDepth(wire,codec.MaxModelDepth);!errors.Is(err,codec.ErrModelDepth){t.Fatal(err)}
 publics,err:=ReadJSONInputPublicChoiceList([]byte("[{\"Ready\":{\"value\":\"text\"}}]"));if err!=nil||len(publics)!=1{t.Fatal(publics,err)}
 buf.B=nil;if err:=WriteColumnarPublicChoice(&buf,publics,nil);err!=nil{t.Fatal(err)}
 if decoded,err:=ReadColumnarPublicChoice(buf.B);err!=nil||len(decoded)!=1{t.Fatal(decoded,err)}
 if decoded,err:=ReadColumnarPublicChoiceDepth(buf.B,1);err!=nil||len(decoded)!=1{t.Fatal(decoded,err)}
 buf.B=nil;if err:=WriteColumnarPublicChoiceDepth(&buf,publics,nil,1);err!=nil{t.Fatal(err)}
}
`

func TestCompiledAliasPlanningConcurrentSources(t *testing.T) {
	view, files, _ := valueAliasCollisions(t)
	raw := semantic.AnalyzeModules(files)
	if len(raw.Errors) != 0 {
		t.Fatal(raw.Errors)
	}
	for _, sample := range []struct {
		name  string
		input *semantic.Result
	}{{"analyzed", raw}, {"compiled_view", view}} {
		t.Run(sample.name, func(t *testing.T) {
			assertConcurrentAliasGeneration(t, sample.input, files)
		})
	}
}

func assertConcurrentAliasGeneration(t *testing.T, input *semantic.Result, files []*ast.File) {
	t.Helper()
	before, err := json.Marshal(input.Files)
	if err != nil {
		t.Fatal(err)
	}
	usage := make(map[*semantic.Symbol]bool)
	for _, symbol := range input.Scope.AllSymbols() {
		usage[symbol] = symbol.Used
	}
	config := GeneratorConfig{IDs: valueAliasIDs(input), Events: BuildEventContext(files, "example.com/alias")}
	generateAliasesConcurrently(t, input, config)
	after, err := json.Marshal(input.Files)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("concurrent generation changed source AST", err)
	}
	for symbol, used := range usage {
		if symbol.Used != used {
			t.Fatalf("concurrent generation marked source symbol %q as used", symbol.Name)
		}
	}
}

func generateAliasesConcurrently(t *testing.T, input *semantic.Result, config GeneratorConfig) {
	t.Helper()
	start := make(chan struct{})
	failures := make(chan error, 16)
	var workers sync.WaitGroup
	for index := 0; index < 4; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for round := 0; round < 4; round++ {
				_, err := Generate(input, "generated", config)
				failures <- err
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
}
