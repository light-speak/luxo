package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedEnumOutputPerformanceFixture(t *testing.T) {
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\";\"github.com/light-speak/luxo/pkg/lux/codec\";\"github.com/light-speak/luxo/pkg/lux/api\")\n")
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	for _, pair := range [][2]string{{"State", "State"}, {"Plain", "String"}} {
		name, fieldType := pair[0]+"List", pair[1]
		decl := &ast.EventDecl{Name: name, Params: []*ast.ParamDecl{{Name: "values", Type: &ast.TypeRef{Name: fieldType, IsList: true}}}}
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{name: {"values": 1}}}})
		generateEventStruct(&source, decl)
		g.generateEventCodec(&source, decl, map[string]bool{"State": true}, nil)
		reference := strings.NewReplacer("BASE", pair[0], "TYPE", mapBaseType(fieldType)).Replace(enumEventOutputBaseline)
		source.WriteString(reference)
	}
	source.WriteString("func afterEnumList(req *api.Request, result []State) error {\n")
	writeNativeReturnEncoding(&source, &ast.TypeRef{Name: "State", IsList: true}, nil, map[string]bool{"State": true})
	source.WriteString("return nil\n}\n")
	source.WriteString(enumOutputBaseline)
	runGeneratedProtocolPackage(t, source.String(), enumOutputBenchmarks)
}

// Frozen generated list/event logic before membership, preallocation and tail checks.
const enumEventOutputBaseline = `
type BeforeBASEEvent struct { Values []TYPE }
func (e BeforeBASEEvent) MarshalLuxo() ([]byte,error) {
    var enc codec.Encoder
    enc.WriteFieldHeader(1)
    enc.WriteArrayHeader(len(e.Values))
    for i:=range e.Values {if err:=enc.WriteString(string(e.Values[i]));err!=nil{return nil,err}}
    enc.WriteEnd()
    return enc.Bytes(),nil
}
func (e *BeforeBASEEvent) UnmarshalLuxo(data []byte) error {
    var seenValues bool
    dec:=codec.NewDecoder(data)
    for dec.NextField() {
        switch dec.FieldID() {
        case 1:
            count:=dec.ReadArrayLength()
            e.Values=make([]TYPE,count)
            for i:=range e.Values {e.Values[i]=TYPE(dec.ReadString())}
            seenValues=true
        default:dec.SkipField()
        }
    }
    if err:=dec.Err();err!=nil{return err}
    if !seenValues{return fmt.Errorf("missing values")}
    return nil
}
`

const enumOutputBaseline = `
func beforeEnumList(req *api.Request,result []State) error {
    req.Buf.B=codec.AppendArrayHeader(req.Buf.B,len(result))
    for _,item:=range result {
        if encoded,err:=codec.AppendString(req.Buf.B,string(item));err!=nil{return err}else{req.Buf.B=encoded}
    }
    return nil
}
`

const enumOutputBenchmarks = `package generated
import ("bytes";"fmt";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestEnumOutputBaselineParity(t *testing.T) {
    before:=BeforeStateEvent{Values:[]State{StateOPEN,StateCLOSED}}
    after:=StateListEvent{Values:before.Values}
    a,err:=before.MarshalLuxo();if err!=nil {t.Fatal(err)}
    z,err:=after.MarshalLuxo();if err!=nil || !bytes.Equal(a,z) {t.Fatalf("wire differs: %x %x %v",a,z,err)}
    if err:=before.UnmarshalLuxo(z);err!=nil {t.Fatal(err)}
    if err:=after.UnmarshalLuxo(a);err!=nil || len(after.Values)!=2 || after.Values[1]!=StateCLOSED {t.Fatal(err)}
}
func BenchmarkEnumOutputNativeList(b *testing.B) {
    for _,count:=range []int{0,1,128} {
        values:=make([]State,count);for i:=range values {values[i]=StateOPEN;if i%2==1 {values[i]=StateCLOSED}}
        for _,version:=range []struct{name string;fn func(*api.Request,[]State)error}{{"Before",beforeEnumList},{"After",afterEnumList}} {
            b.Run(fmt.Sprintf("%d/%s",count,version.name),func(b *testing.B){
                req:=&api.Request{Buf:&api.ResponseBuf{B:make([]byte,0,2048)}}
                b.ReportAllocs();b.ResetTimer()
                for b.Loop(){req.Buf.B=req.Buf.B[:0];if err:=version.fn(req,values);err!=nil {b.Fatal(err)}}
            })
        }
    }
}
func BenchmarkEnumOutputEvent(b *testing.B) {
    for _,count:=range []int{0,1,128} {
        values:=make([]State,count);for i:=range values {values[i]=StateOPEN;if i%2==1 {values[i]=StateCLOSED}}
        before,after:=BeforeStateEvent{Values:values},StateListEvent{Values:values}
        benchEventPair(b,fmt.Sprintf("State/%d",count),before.MarshalLuxo,after.MarshalLuxo,before.UnmarshalLuxo,after.UnmarshalLuxo)
    }
    for _,sample:=range []struct{name,text string;count int}{{"Short","OPEN",1},{"List","OPEN",128},{"LongList",strings.Repeat("x",1024),128},{"Chinese",strings.Repeat("中",24576),1}} {
        values:=make([]string,sample.count);for i:=range values {values[i]=sample.text}
        before,after:=BeforePlainEvent{Values:values},PlainListEvent{Values:values}
        benchEventPair(b,"Plain/"+sample.name,before.MarshalLuxo,after.MarshalLuxo,before.UnmarshalLuxo,after.UnmarshalLuxo)
    }
}
func benchEventPair(b *testing.B,name string,beforeWrite,afterWrite func()([]byte,error),beforeRead,afterRead func([]byte)error) {
    b.Helper()
    data,err:=beforeWrite();if err!=nil {b.Fatal(err)}
    expected,err:=afterWrite();if err!=nil || !bytes.Equal(data,expected){b.Fatal("baseline wire mismatch",err)}
    for _,version:=range []struct{name string;write func()([]byte,error);read func([]byte)error}{{"Before",beforeWrite,beforeRead},{"After",afterWrite,afterRead}} {
        b.Run(name+"/Write/"+version.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){got,err:=version.write();if err!=nil || len(got)!=len(data){b.Fatal(err)}}})
        b.Run(name+"/Read/"+version.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){if err:=version.read(data);err!=nil {b.Fatal(err)}}})
    }
}
`
