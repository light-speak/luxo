package codegen

import (
	"fmt"
	"strings"
	"testing"
)

// Exercise formal generation and the resulting handlers, not hand-written codecs.
func TestGeneratedNullableRootWriterFixture(t *testing.T) {
	var source, support strings.Builder
	source.WriteString("type Snapshot { id: Int label: String }\n")
	support.WriteString("package generated\nimport \"context\"\n")
	support.WriteString("type rootResolver struct { missing bool; calls int; failure error\n")
	types := []struct{ name, luxo, goType string }{
		{"Int", "Int", "int64"}, {"Boolean", "Boolean", "bool"},
		{"String", "String", "string"}, {"Bytes", "Bytes", "[]byte"},
		{"Integers", "[Int]", "[]int64"}, {"Snapshot", "Snapshot", "Snapshot"},
		{"Rows", "[Snapshot]", "[]Snapshot"},
	}
	for _, kind := range types {
		fmt.Fprintf(&support, "%s %s\n", kind.name, kind.goType)
	}
	support.WriteString("}\n")
	for _, kind := range types {
		writeRootWriterFixture(&source, &support, kind.name, kind.luxo, kind.goType)
	}
	runGeneratedFixture(t, source.String(), nullableRootWriterAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Snapshot": {"id": 1, "label": 2}},
	}, support.String())
}

func writeRootWriterFixture(source, support *strings.Builder, name, luxo, goType string) {
	fmt.Fprintf(source, "fn source%s(): Result<%s> @native\nfn maybe%s(): Result<%s?> @native\n", name, luxo, name, luxo)
	fmt.Fprintf(source, "api plain%s(): %s { return source%s()? }\n", name, luxo, name)
	fmt.Fprintf(source, "api present%s(): %s? { return source%s()? }\n", name, luxo, name)
	fmt.Fprintf(source, "api optional%s(): %s? { return maybe%s()? }\n", name, luxo, name)
	fmt.Fprintf(source, "api nativePlain%s(): %s @native\napi nativeOptional%s(): %s? @native\n", name, luxo, name, luxo)
	optional, value := "*"+goType, "&r."+name
	if strings.HasPrefix(luxo, "[") {
		optional, value = goType, "r."+name
	}
	fmt.Fprintf(support, "func(r *rootResolver) Source%s(context.Context)(%s,error){r.calls++;return r.%s,r.failure}\n", name, goType, name)
	fmt.Fprintf(support, "func(r *rootResolver) Maybe%s(context.Context)(%s,error){r.calls++;if r.missing{return nil,r.failure};return %s,r.failure}\n", name, optional, value)
	fmt.Fprintf(support, "func(r *rootResolver) NativePlain%s(ctx context.Context)(%s,error){return r.Source%s(ctx)}\n", name, goType, name)
	fmt.Fprintf(support, "func(r *rootResolver) NativeOptional%s(ctx context.Context)(%s,error){return r.Maybe%s(ctx)}\n", name, optional, name)
	fmt.Fprintf(support, "func rootHandlers%s(app *App) []rootHandler{return []rootHandler{", name)
	for _, prefix := range []string{"plain", "present", "optional", "nativePlain", "nativeOptional"} {
		fmt.Fprintf(support, "{name:%q,api:%q,call:handle%s%s(app)},", prefix, prefix+name, strings.ToUpper(prefix[:1])+prefix[1:], name)
	}
	support.WriteString("}}\n")
}

const nullableRootWriterAssertions = `package generated
import("bytes";"context";"errors";"math";"os";"slices";"strings";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type rootHandler struct{name,api string;call api.HandlerFunc}
type rootCase struct{name string;resolver *rootResolver;handlers func(*App)[]rootHandler;wire []byte}
func rootString(t testing.TB,dst []byte,value string)[]byte{
 t.Helper();data,err:=codec.AppendString(dst,value);if err!=nil{t.Fatal(err)};return data
}
func rootCases(t testing.TB)[]rootCase{
 t.Helper();long:=strings.Repeat("发布检查",350);raw:=bytes.Repeat([]byte{0,255,128,1},1024)
 items:=make([]int64,128);wire:=codec.AppendArrayHeader(nil,len(items))
 for i:=range items{items[i]=int64(i);wire=codec.AppendSvarint(wire,items[i])}
 snapshot:=Snapshot{Id:42,Label:"发布"};single:=codec.AppendVarint(nil,uint64(len(snapshot.Label)))
 single=codec.AppendSvarint(append(single,1),snapshot.Id)
 single=rootString(t,append(single,2),snapshot.Label);single=append(single,0)
 rows:=make([]Snapshot,128);columns:=codec.AppendVarint(codec.AppendArrayHeader(nil,len(rows)),uint64(len(rows)*len(snapshot.Label)))
 columns=append(columns,1)
 for i:=range rows{rows[i]=Snapshot{Id:int64(i),Label:"发布"};columns=codec.AppendSvarint(columns,rows[i].Id)}
 columns=append(columns,2);for _,row:=range rows{columns=rootString(t,columns,row.Label)};columns=append(columns,0)
 return []rootCase{
  {"int42",&rootResolver{Int:42},rootHandlersInt,[]byte{84}},
  {"int-max",&rootResolver{Int:math.MaxInt64},rootHandlersInt,codec.AppendSvarint(nil,math.MaxInt64)},
  {"zero",&rootResolver{},rootHandlersInt,[]byte{0}},
  {"false",&rootResolver{},rootHandlersBoolean,[]byte{0}},
  {"empty-text",&rootResolver{},rootHandlersString,[]byte{0}},
  {"unicode4K",&rootResolver{String:long},rootHandlersString,rootString(t,nil,long)},
  {"bytes4K",&rootResolver{Bytes:raw},rootHandlersBytes,codec.AppendBytes(nil,raw)},
  {"empty-list",&rootResolver{Integers:[]int64{}},rootHandlersIntegers,[]byte{0}},
  {"list128",&rootResolver{Integers:items},rootHandlersIntegers,wire},
  {"object",&rootResolver{Snapshot:snapshot},rootHandlersSnapshot,single},
  {"rows128",&rootResolver{Rows:rows},rootHandlersRows,columns},
 }
}
func rootRequest(t testing.TB) *api.Request{
 t.Helper();mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 return &api.Request{FieldMask:mask,Buf:&api.ResponseBuf{B:make([]byte,0,16384)}}
}
func rootExpected(item rootCase,handler rootHandler,missing bool)[]byte{
 if handler.name=="plain"||handler.name=="nativePlain"{return item.wire}
 if missing&&handler.name!="present"{return []byte{0}}
 return append([]byte{1},item.wire...)
}
func TestRootWriterParity(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 for _,item:=range rootCases(t){t.Run(item.name,func(t *testing.T){
  handlers:=item.handlers(&App{Resolver:item.resolver});req:=rootRequest(t)
  expectedJSON,err:=router.StreamPayloadJSON(handlers[0].api,item.wire);if err!=nil{t.Fatal(err)}
  for _,missing:=range []bool{false,true}{item.resolver.missing=missing
   for _,handler:=range handlers{req.Buf.Reset();before:=item.resolver.calls
    if err:=handler.call(t.Context(),req);err!=nil{t.Fatal(err)}
    want:=rootExpected(item,handler,missing)
    if !bytes.Equal(req.Buf.B,want)||item.resolver.calls!=before+1{t.Fatalf("%s missing=%v: %x != %x, calls=%d",handler.name,missing,req.Buf.B,want,item.resolver.calls-before)}
    got,err:=router.StreamPayloadJSON(handler.api,req.Buf.B);jsonWant:=expectedJSON
    if missing&&(handler.name=="optional"||handler.name=="nativeOptional"){jsonWant=[]byte("null")}
    if err!=nil||!bytes.Equal(got,jsonWant){t.Fatalf("%s: %s != %s (%v)",handler.name,got,jsonWant,err)}
   }
  }
  sentinel:=errors.New("native failure");item.resolver.failure=sentinel
  for _,handler:=range handlers{req.Buf.Reset();before:=item.resolver.calls
   if err:=handler.call(t.Context(),req);!errors.Is(err,sentinel)||len(req.Buf.B)!=0||item.resolver.calls!=before+1{t.Fatalf("%s swallowed failure: %v/%x",handler.name,err,req.Buf.B)}
  }
 })}
}
func BenchmarkGeneratedNullableRootWriter(b *testing.B){
 for _,item:=range rootCases(b){b.Run(item.name,func(b *testing.B){
  handlers:=item.handlers(&App{Resolver:item.resolver})
  if os.Getenv("LUXO_NULLABLE_AFTER_FIRST")=="1"{slices.Reverse(handlers)}
  for _,handler:=range handlers{b.Run(handler.name,func(b *testing.B){benchmarkRootWriter(b,item,handler,false)})}
  for _,handler:=range handlers{if handler.name=="optional"||handler.name=="nativeOptional"{
   b.Run(handler.name+"-null",func(b *testing.B){benchmarkRootWriter(b,item,handler,true)})
  }}
 })}
}
func benchmarkRootWriter(b *testing.B,item rootCase,handler rootHandler,missing bool){
 item.resolver.missing=missing;req:=rootRequest(b);ctx:=context.Background();want:=rootExpected(item,handler,missing)
 if err:=handler.call(ctx,req);err!=nil||!bytes.Equal(req.Buf.B,want){b.Fatal("invalid fixture",err,req.Buf.B,want)}
 before:=item.resolver.calls;var err error
 b.ReportAllocs()
 for b.Loop(){req.Buf.Reset();err=handler.call(ctx,req)}
 if err!=nil||!bytes.Equal(req.Buf.B,want)||item.resolver.calls!=before+b.N{b.Fatal("changed output or operation count",err)}
}
`
