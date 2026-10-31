package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedEnumReaders(t *testing.T) {
	var source strings.Builder
	source.WriteString("package generated\nimport (\"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	model := &ast.ModelDecl{Name: "Input", Fields: []*ast.FieldDecl{
		testField("state", "State"),
		{Name: "optional", Type: &ast.TypeRef{Name: "State", Nullable: true}},
		{Name: "states", Type: &ast.TypeRef{Name: "State", IsList: true}},
		{Name: "optionalStates", Type: &ast.TypeRef{Name: "State", IsList: true, Nullable: true}},
	}}
	parent := &ast.ModelDecl{Name: "Parent", Fields: []*ast.FieldDecl{
		{Name: "child", Type: &ast.TypeRef{Name: "Input"}},
		{Name: "children", Type: &ast.TypeRef{Name: "Input", IsList: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{
		"Input":  {"state": 1, "optional": 2, "states": 3, "optionalStates": 4},
		"Parent": {"child": 1, "children": 2},
	})
	enums := map[string]bool{"State": true}
	for _, m := range []*ast.ModelDecl{model, parent} {
		generateModel(&source, m, enums)
		g.generateReadLuxo(&source, m, enums)
		g.generateReadColumnar(&source, m, enums)
	}
	source.WriteString("func readPlain(dec *codec.Decoder) (Input,error) { var result Input\nfor dec.NextField() {switch dec.FieldID() {\n")
	writeReadLuxoField(&source, model.Fields[0], 1, "result.State", enums)
	writeReadLuxoField(&source, model.Fields[1], 2, "result.Optional", enums)
	source.WriteString("default:dec.SkipField()}}\nreturn result,dec.Finish()}\n")
	source.WriteString(enumReaderBaseline)
	runGeneratedProtocolPackage(t, source.String(), enumReaderAssertions+enumReaderBenchmarks)
}

const enumReaderAssertions = `package generated
import ("errors"; "fmt"; "testing"; "github.com/light-speak/luxo/pkg/lux/codec")

func stringValue(t *testing.T,text string) []byte {
    t.Helper()
    data,err:=codec.AppendString(nil,text)
    if err!=nil {t.Fatal(err)}
    return data
}

func enumField(t *testing.T,id int,text string,framed bool) []byte {
    t.Helper()
    data:=[]byte{byte(id)}
    if id==2 || id==4 {data=append(data,1)}
    value:=stringValue(t,text)
    if id>=3 {
        value=append([]byte{2,4,'O','P','E','N'},value...)
        if framed {value=codec.AppendBytes(nil,value)}
    }
    return append(data,value...)
}

func assertEnumError(t *testing.T,err error,valid bool) {
    t.Helper()
    if valid && err!=nil {t.Fatal(err)}
    if !valid && !errors.Is(err,codec.ErrInvalidEnum) {t.Fatalf("wanted enum error, got %v",err)}
}

func TestEnumReaders(t *testing.T) {
    for _,text:=range []string{"OPEN","CLOSED","FUTURE","","open"} {
        valid:=text=="OPEN" || text=="CLOSED"
        for id:=1; id<=4; id++ {
            t.Run(text+string(rune('0'+id)),func(t *testing.T){
                row:=append([]byte{0},enumField(t,id,text,false)...)
                row=append(row,0)
                var value Input
                dec:=codec.NewDecoder(row)
                value.ReadLuxoInput(dec)
                assertEnumError(t,dec.Finish(),valid)
                if valid {
                    switch id {
                    case 1: if string(value.State)!=text {t.Fatal(value)}
                    case 2: if value.Optional==nil || string(*value.Optional)!=text {t.Fatal(value)}
                    case 3: if len(value.States)!=2 || string(value.States[1])!=text {t.Fatal(value)}
                    case 4: if len(value.OptionalStates)!=2 || string(value.OptionalStates[1])!=text {t.Fatal(value)}
                    }
                }
                if id<=2 {_,err:=readPlain(codec.NewDecoder(row[1:])); assertEnumError(t,err,valid)}
                column:=append([]byte{1,0},enumField(t,id,text,true)...)
                column=append(column,0)
                values,err:=ReadColumnarInput(column)
                assertEnumError(t,err,valid)
                if valid && len(values)!=1 {t.Fatal(values)}
                if !valid && values!=nil {t.Fatal("partial columnar success")}
                for _,list:=range []bool{false,true} {
                    for _,input:=range []bool{false,true} {
                        nested:=[]byte{0,1}
                        if list {nested[1]=2}
                        if input {
                            if list {nested=append(nested,1)}
                            nested=append(nested,row...)
                        } else if list {nested=codec.AppendBytes(nested,column)} else {nested=codec.AppendBytes(nested,row)}
                        nested=append(nested,0)
                        parent:=Parent{}
                        dec:=codec.NewDecoder(nested)
                        if input {parent.ReadLuxoInput(dec)} else {parent.ReadLuxo(dec)}
                        assertEnumError(t,dec.Finish(),valid)
                    }
                }
            })
        }
    }
}

func TestEnumNullAndMalformed(t *testing.T) {
    state:=StateOPEN
    value:=Input{Optional:&state,OptionalStates:[]State{state}}
    dec:=codec.NewDecoder([]byte{0,2,0,4,0,0})
    value.ReadLuxo(dec)
    if err:=dec.Finish(); err!=nil || value.Optional!=nil || value.OptionalStates!=nil {t.Fatalf("null: %#v %v",value,err)}
    for _,raw:=range [][]byte{{0,1,8,'O'}, {0,2,1,8,'O'}, {0,3,1,8,'O'}} {
        dec=codec.NewDecoder(raw)
        value.ReadLuxo(dec)
        if err:=dec.Finish(); err==nil || errors.Is(err,codec.ErrInvalidEnum) {t.Fatalf("lost malformed error: %v",err)}
    }
}
`

// Frozen generated row reader before enum membership validation.
const enumReaderBaseline = `
func (i *Input) readBeforeEnum(dec *codec.Decoder) {
    size:=dec.ReadArenaSize()
    var arena []byte
    var off int
    if size>0 {arena=make([]byte,size)}
    for dec.NextField() {
        switch dec.FieldID() {
        case 1: i.State=State(dec.ReadStringArena(arena,&off))
        case 2:
            if raw:=dec.ReadStringArenaPtr(arena,&off); raw!=nil {value:=State(*raw); i.Optional=&value} else {i.Optional=nil}
        case 3:
            a:=dec.ReadStringArray()
            i.States=make([]State,len(a))
            for index,item:=range a {i.States[index]=State(item)}
        case 4:
            if !dec.ReadBool() {i.OptionalStates=nil;break}
            a:=dec.ReadStringArray()
            i.OptionalStates=make([]State,len(a))
            for index,item:=range a {i.OptionalStates[index]=State(item)}
        default: dec.SkipField()
        }
    }
}
`

const enumReaderBenchmarks = `
func BenchmarkEnumRows(b *testing.B) {
    for _,shape:=range []string{"scalar","nullable","list1","list128"} {
        data:=[]byte{4,1,4,'O','P','E','N',0}
        count:=1
        if shape=="nullable" {data=[]byte{4,2,1,4,'O','P','E','N',0}}
        if shape=="list1" || shape=="list128" {
            if shape=="list128" {count=128}
            data=codec.AppendArrayHeader([]byte{0,3},count)
            for range count {data=append(data,4,'O','P','E','N')}
            data=append(data,0)
        }
        for _,tc:=range []struct{name string;read func(*Input,*codec.Decoder)}{{"before",(*Input).readBeforeEnum},{"after",(*Input).ReadLuxo}} {
            b.Run(fmt.Sprintf("%s/%s",shape,tc.name),func(b *testing.B){
                b.ReportAllocs()
                for b.Loop() {
                    var out Input
                    dec:=codec.NewDecoder(data)
                    tc.read(&out,dec)
                    if err:=dec.Finish(); err!=nil {b.Fatal(err)}
                    switch shape {
                    case "scalar": if out.State!=StateOPEN {b.Fatal(out)}
                    case "nullable": if out.Optional==nil || *out.Optional!=StateOPEN {b.Fatal(out)}
                    default: if len(out.States)!=count || out.States[count-1]!=StateOPEN {b.Fatal(out)}
                    }
                }
            })
        }
    }
}
`
