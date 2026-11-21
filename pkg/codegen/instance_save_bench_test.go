package codegen

import "testing"

const instanceSaveBenchmarkSource = `
model Saved @noTime { id: Int @id name: String optional: Int? tags: [String] data: Bytes }
model Plain @noTime { id: Int @id name: String optional: Int? tags: [String] data: Bytes }
fn persist(value: Saved): Saved { return value.save() }
`

func BenchmarkInstanceSaveGeneration(b *testing.B) {
	input := analyzePermissionFixture(b, instanceSaveBenchmarkSource)
	generator := mustNewGenerator(b, GeneratorConfig{IDs: instanceSaveBenchmarkIDs()})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := generator.Generate(input, "generated", nil); err != nil {
			b.Fatal(err)
		}
	}
}

func instanceSaveBenchmarkIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{
		"Saved": {"id": 1, "name": 2, "optional": 3, "tags": 4, "data": 5},
		"Plain": {"id": 1, "name": 2, "optional": 3, "tags": 4, "data": 5},
	}}
}

func TestGeneratedInstanceSaveRuntimeBenchmarks(t *testing.T) {
	runGeneratedFixture(t, instanceSaveBenchmarkSource, instanceSaveRuntimeBenchmarks, instanceSaveBenchmarkIDs())
}

const instanceSaveRuntimeBenchmarks = `package generated
import("testing";"unsafe";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/jackc/pgx/v5/pgconn")
type benchmarkRows struct { pg.Rows; fields []pgconn.FieldDescription; tags []string; data []byte; optional int64 }
func(r *benchmarkRows)FieldDescriptions()[]pgconn.FieldDescription{return r.fields}
func(r *benchmarkRows)Scan(dest ...any)error{for i,field:=range r.fields{switch field.Name{case "id":*dest[i].(*int64)=7;case "name":*dest[i].(*string)="original";case "optional":*dest[i].(**int64)=&r.optional;case "tags":*dest[i].(*[]string)=r.tags;case "data":*dest[i].(*[]byte)=r.data}};return nil}
var savedSink *Saved
var plainSink *Plain
func BenchmarkInstanceSaveScan(b *testing.B){
 for _,sample:=range []struct{name string;columns []string}{
  {"scalar",[]string{"id","name"}},{"nullable",[]string{"id","optional"}},{"mutable128",[]string{"id","tags","data"}},
 }{
  row:=&benchmarkRows{tags:make([]string,128),data:make([]byte,128),optional:2};for _,name:=range sample.columns{row.fields=append(row.fields,pgconn.FieldDescription{Name:name})}
  b.Run(sample.name+"/plain-no-snapshot",func(b *testing.B){b.ReportAllocs();b.ReportMetric(float64(unsafe.Sizeof(Plain{})),"model-B");for b.Loop(){value,err:=scanPlain(row);if err!=nil{b.Fatal(err)};plainSink=value}})
  b.Run(sample.name+"/save-snapshot",func(b *testing.B){b.ReportAllocs();b.ReportMetric(float64(unsafe.Sizeof(Saved{})),"model-B");for b.Loop(){value,err:=scanSaved(row);if err!=nil{b.Fatal(err)};savedSink=value}})
 }
}
func BenchmarkInstanceSaveNoop(b *testing.B){
 row:=&benchmarkRows{fields:[]pgconn.FieldDescription{{Name:"id"},{Name:"name"}}};value,err:=scanSaved(row);if err!=nil{b.Fatal(err)};client:=&SavedClient{};ctx:=b.Context()
 b.ReportAllocs();for b.Loop(){got,err:=client.save(ctx,value);if err!=nil||got!=value{b.Fatal(got,err)}}
}
func BenchmarkInstanceSaveCompareMutable(b *testing.B){
 row:=&benchmarkRows{fields:[]pgconn.FieldDescription{{Name:"id"},{Name:"tags"},{Name:"data"}},tags:make([]string,128),data:make([]byte,128)};value,err:=scanSaved(row);if err!=nil{b.Fatal(err)}
 b.ReportAllocs();for b.Loop(){if value._luxoSaveChangedTags()||value._luxoSaveChangedData(){b.Fatal("unchanged snapshot")}}
}
func BenchmarkInstanceSaveErrors(b *testing.B){
 changed:=&Saved{Id:1,_luxoSaveLoaded:[1]uint64{1}};changed._luxoSaveCapture();changed.Id=2
 for _,sample:=range []struct{name string;value *Saved}{
  {"nil",nil},{"unloaded-key",&Saved{_luxoSaveConstructed:true}},{"changed-key",changed},
 }{b.Run(sample.name,func(b *testing.B){client:=&SavedClient{};ctx:=b.Context();b.ReportAllocs();for b.Loop(){if _,err:=client.save(ctx,sample.value);err==nil{b.Fatal("invalid save accepted")}}})}
}
func TestInlineSnapshotAllocations(t *testing.T){
 value:=&Saved{};number:=int64(2);value.Optional=&number
 if got:=testing.AllocsPerRun(100,func(){value._luxoSaveCapture()});got!=0{t.Fatalf("scalar/nullable baseline allocates: %v",got)}
 if unsafe.Sizeof(Saved{})<=unsafe.Sizeof(Plain{}){t.Fatal("snapshot size accounting missing")}
 t.Logf("plain model=%d bytes; save-capable model=%d bytes",unsafe.Sizeof(Plain{}),unsafe.Sizeof(Saved{}))
}
`
