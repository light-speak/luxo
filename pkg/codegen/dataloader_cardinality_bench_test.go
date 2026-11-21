package codegen

import (
	"fmt"
	"strings"
	"testing"
)

// Compare the generated grouping body with only its cardinality check removed.
// SQL construction, scanning and network I/O are deliberately outside this sample.
func TestGeneratedRelationCardinalityBenchmarks(t *testing.T) {
	var source, benchmarks strings.Builder
	source.WriteString("package generated\nimport(\"github.com/google/uuid\";\"github.com/light-speak/luxo/pkg/lux/dataloader\")\nvar _ = uuid.Nil\n")
	benchmarks.WriteString("package generated\nimport(\"testing\";\"strconv\";\"github.com/google/uuid\")\nvar _ = uuid.Nil\n")
	for _, sample := range []struct{ name, keyType, value string }{
		{"Int", "int64", "int64(i)"}, {"String", "string", "strconv.Itoa(i)"}, {"UUID", "uuid.UUID", "uuid.UUID{byte(i)}"},
	} {
		model := sample.name + "Row"
		fmt.Fprintf(&source, "type %s struct { Key %s; Nullable *%s }\n", model, sample.keyType, sample.keyType)
		for _, nullable := range []bool{false, true} {
			name, field := sample.name+"Required", "key"
			if nullable {
				name, field = sample.name+"Nullable", "nullable"
			}
			writeCardinalityComparison(&source, name, Relation{TargetName: model, RemoteKey: field, KeyGoType: sample.keyType, RemoteKeyNullable: nullable})
			fmt.Fprintf(&benchmarks, cardinalityRuntimeBenchmark, name, model, sample.keyType, sample.value, sample.name, name, name)
		}
	}
	runGeneratedProtocolPackage(t, source.String(), benchmarks.String())
}

func writeCardinalityComparison(b *strings.Builder, name string, relation Relation) {
	var body strings.Builder
	writeRelationBatchResult(&body, relation)
	checked := strings.TrimSuffix(body.String(), "\t\t},\n")
	var unchecked strings.Builder
	for _, line := range strings.Split(checked, "\n") {
		if strings.Contains(line, "nullKeys :=") || strings.Contains(line, "if len(result) !=") {
			continue
		}
		unchecked.WriteString(strings.ReplaceAll(line, "nullKeys++; ", ""))
		unchecked.WriteByte('\n')
	}
	for _, sample := range []struct{ suffix, body string }{{"", checked}, {"Unchecked", unchecked.String()}} {
		fmt.Fprintf(b, "//go:noinline\nfunc group%s%s(rows []*%s, keys []%s) (map[%s]*%s,error) {\n%s}\n", name, sample.suffix, relation.TargetName, relation.KeyGoType, relation.KeyGoType, relation.TargetName, sample.body)
	}
}

const cardinalityRuntimeBenchmark = `
func BenchmarkRelationCardinality%s(b *testing.B){
 for _,count:=range []int{0,1,128}{
  rows:=make([]*%s,count);keys:=make([]%s,count)
  for i:=range rows{key:=%s;keys[i]=key;rows[i]=&%sRow{Key:key,Nullable:&key}}
  for _,before:=range []bool{true,false}{
   name:=strconv.Itoa(count)+"/after";if before{name=strconv.Itoa(count)+"/before"}
   b.Run(name,func(b *testing.B){
    group:=group%s;if before{group=group%sUnchecked}
    b.ReportAllocs()
    for b.Loop(){got,err:=group(rows,keys);if err!=nil||len(got)!=count{b.Fatal("invalid grouping",err)}}
   })
  }
 }
}
`

// Exercise the emitted response reader without including the RPC round trip.
func TestGeneratedRemoteResponseCardinalityBenchmarks(t *testing.T) {
	source := remoteLoaderWireSource(t)
	start := strings.Index(source, "\t\t\tcount, n := codec.ReadArrayHeader(resp, 0)")
	if start < 0 {
		t.Fatal("generated primary-key response reader missing")
	}
	end := strings.Index(source[start:], "\t\t},\n")
	if end < 0 {
		t.Fatal("generated primary-key response reader boundary missing")
	}
	body := source[start : start+end]
	if strings.Count(body, " || count > len(keys)") != 1 {
		t.Fatal("generated response cardinality guard missing or duplicated")
	}
	unchecked := strings.Replace(body, " || count > len(keys)", "", 1)
	for _, sample := range []struct{ name, body string }{{"Checked", body}, {"Unchecked", unchecked}} {
		source += "//go:noinline\nfunc readResponse" + sample.name + "(resp []byte, keys []int64) (map[int64]*Post,error) {\n" + sample.body + "}\n"
	}
	runGeneratedProtocolPackage(t, source, remoteResponseCardinalityBenchmark)
}

const remoteResponseCardinalityBenchmark = `package generated
import("strconv";"testing";"github.com/light-speak/luxo/pkg/lux/codec")
func BenchmarkRelationCardinalityRemoteResponse(b *testing.B){
 for _,count:=range []int{0,1,128}{
  keys:=make([]int64,count);resp:=codec.AppendVarint(nil,uint64(count))
  for i:=range keys{keys[i]=int64(i);var enc codec.Encoder;enc.WriteFieldInt(1,int64(i));enc.WriteEnd();resp=append(resp,0);resp=append(resp,enc.Bytes()...)}
  for _,before:=range []bool{true,false}{
   name:=strconv.Itoa(count)+"/after";if before{name=strconv.Itoa(count)+"/before"}
   b.Run(name,func(b *testing.B){
    read:=readResponseChecked;if before{read=readResponseUnchecked};b.ReportAllocs()
    for b.Loop(){got,err:=read(resp,keys);if err!=nil||len(got)!=count{b.Fatal("invalid primary-key response",err)}}
   })
  }
 }
}
func TestRemoteResponseCardinalityBoundary(t *testing.T){
 response:=func(ids ...int64)[]byte{
  data:=codec.AppendVarint(nil,uint64(len(ids)))
  for _,id:=range ids{var enc codec.Encoder;enc.WriteFieldInt(1,id);enc.WriteEnd();data=append(data,0);data=append(data,enc.Bytes()...)}
  return data
 }
 for _,sample:=range []struct{name string;keys []int64;data []byte;rows int;bad bool}{
  {"no-keys",nil,response(),0,false},
  {"missing",[]int64{42},response(),0,false},
  {"zero-key",[]int64{0},response(0),1,false},
  {"partial",[]int64{42,43},response(42),1,false},
  {"full",[]int64{42,43},response(42,43),2,false},
  {"duplicate-input",[]int64{42,42},response(42),1,false},
  {"row-without-keys",nil,response(42),0,true},
  {"extra-rows",[]int64{42},response(42,43),0,true},
 }{t.Run(sample.name,func(t *testing.T){
  result,err:=readResponseChecked(sample.data,sample.keys)
  if sample.bad{if err==nil||result!=nil{t.Fatalf("invalid cardinality accepted: %v/%v",result,err)};return}
  if err!=nil||len(result)!=sample.rows{t.Fatalf("valid cardinality rejected: %v/%v",result,err)}
 })}
}
`
