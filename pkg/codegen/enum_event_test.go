package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedEnumEvent(t *testing.T) {
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\";\"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	event := &ast.EventDecl{Name: "StateChanged", Params: []*ast.ParamDecl{
		{Name: "value", Type: &ast.TypeRef{Name: "State"}},
		{Name: "optional", Type: &ast.TypeRef{Name: "State", Nullable: true}},
		{Name: "values", Type: &ast.TypeRef{Name: "State", IsList: true}},
		{Name: "optionalValues", Type: &ast.TypeRef{Name: "State", IsList: true, Nullable: true}},
	}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"StateChanged": {"value": 1, "optional": 2, "values": 3, "optionalValues": 4}}}})
	generateEventStruct(&source, event)
	g.generateEventCodec(&source, event, map[string]bool{"State": true}, nil)
	runGeneratedProtocolPackage(t, source.String(), enumEventAssertions)
}

func TestGeneratedNullableEventLists(t *testing.T) {
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\";\"time\";\"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	decl := &ast.EventDecl{Name: "NullableLists", Params: []*ast.ParamDecl{
		{Name: "messages", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
		{Name: "dates", Type: &ast.TypeRef{Name: "DateTime", IsList: true, Nullable: true}},
	}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"NullableLists": {"messages": 1, "dates": 2}}}})
	generateEventStruct(&source, decl)
	g.generateEventCodec(&source, decl, nil, nil)
	runGeneratedProtocolPackage(t, source.String(), nullableEventListAssertions)
}

const nullableEventListAssertions = `package generated
import ("reflect";"testing";"time")
func TestNullableEventLists(t *testing.T) {
    for _,value:=range []NullableListsEvent{{},{Messages:[]string{},Dates:[]time.Time{}},{Messages:[]string{"","中"},Dates:[]time.Time{time.Unix(-1,999999999).UTC()}}} {
        data,err:=value.MarshalLuxo();if err!=nil {t.Fatal(err)}
        out:=NullableListsEvent{Messages:[]string{"old"},Dates:[]time.Time{time.Now()}}
        if err:=out.UnmarshalLuxo(data);err!=nil || !reflect.DeepEqual(out,value) {t.Fatalf("changed nullable lists: %#v %#v %v",out,value,err)}
    }
    for _,data:=range [][]byte{{1,2,0},{2,2,0},{1,1,127,0},{2,1,127,0}} {
        var value NullableListsEvent
        if err:=value.UnmarshalLuxo(data);err==nil {t.Fatalf("accepted malformed nullable list: %x",data)}
        if value.Messages!=nil || value.Dates!=nil {t.Fatal("allocated list before payload validation")}
    }
}
`

const enumEventAssertions = `package generated
import ("bytes";"errors";"reflect";"testing";"github.com/light-speak/luxo/pkg/lux/codec")

func eventWire(t *testing.T,field int,value string) []byte {
    t.Helper()
    var enc codec.Encoder
    for id:=1;id<=4;id++ {
        enc.WriteFieldHeader(id)
        if id==2 || id==4 {enc.WritePresent()}
        if id>=3 {enc.WriteArrayHeader(1)}
        text:="OPEN"; if id==field {text=value}
        if err:=enc.WriteString(text);err!=nil {t.Fatal(err)}
    }
    enc.WriteEnd()
    return enc.Bytes()
}

func TestEnumEventRejectsUnknown(t *testing.T) {
    for _,value:=range []State{"FUTURE","","open"} {
        for field:=1;field<=4;field++ {
            event:=StateChangedEvent{Value:StateOPEN,Values:[]State{StateOPEN}}
            switch field {
            case 1:event.Value=value
            case 2:event.Optional=&value
            case 3:event.Values=[]State{StateOPEN,value}
            case 4:event.OptionalValues=[]State{StateOPEN,value}
            }
            if data,err:=event.MarshalLuxo(); !errors.Is(err,codec.ErrInvalidEnum) || data!=nil {t.Fatalf("field=%d: %x %v",field,data,err)}
            if err:=event.UnmarshalLuxo(eventWire(t,field,string(value))); !errors.Is(err,codec.ErrInvalidEnum) {t.Fatalf("read field=%d: %v",field,err)}
        }
    }
}

func TestEnumEventNullAndWire(t *testing.T) {
    open:=StateOPEN
    for _,values:=range [][]State{nil,{}, {StateOPEN,StateCLOSED}} {
        event:=StateChangedEvent{Value:open,Optional:&open,Values:[]State{},OptionalValues:values}
        data,err:=event.MarshalLuxo(); if err!=nil {t.Fatal(err)}
        got:=StateChangedEvent{OptionalValues:[]State{StateCLOSED}}
        if err:=got.UnmarshalLuxo(data);err!=nil || !reflect.DeepEqual(got,event) {t.Fatalf("roundtrip: %#v %#v %v",got,event,err)}
    }
    event:=StateChangedEvent{Value:open,Optional:&open,Values:[]State{open},OptionalValues:[]State{open}}
    data,err:=event.MarshalLuxo()
    if err!=nil || !bytes.Equal(data,eventWire(t,0,"")) {t.Fatalf("wire: %x %v",data,err)}
    if err:=event.UnmarshalLuxo(append(data,0));err==nil {t.Fatal("accepted trailing event data")}
    short:=codec.AppendArrayHeader([]byte{3},10000)
    var empty StateChangedEvent
    if err:=empty.UnmarshalLuxo(short);err==nil || empty.Values!=nil {t.Fatal("accepted or allocated short array")}
    if err:=empty.UnmarshalLuxo([]byte{1,2,'O'});err==nil || errors.Is(err,codec.ErrInvalidEnum) {t.Fatalf("lost original malformed string error: %v",err)}
}
`
