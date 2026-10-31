package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func TestGeneratedEventSnapshotListCopy(t *testing.T) {
	file := &ast.File{Types: []*ast.TypeDecl{{Name: "ListItem", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}}}
	for _, name := range []string{"Bytes", "JSON", "DateTime", "ListItem", "String"} {
		file.Events = append(file.Events, &ast.EventDecl{Name: name + "Rows", Params: []*ast.ParamDecl{
			{Name: "values", Type: &ast.TypeRef{Name: name, IsList: true}},
		}})
	}
	source := generatedSnapshotSource(t, file)
	runGeneratedProtocolPackage(t, source+snapshotBulkCopyReference(t, file), eventSnapshotCopyAssertions)
}

// Freeze the preceding bulk-copy behavior beside the actual generated candidate.
// Both variants validate and isolate payloads; neither uses the unsafe raw bus.
func snapshotBulkCopyReference(t *testing.T, file *ast.File) string {
	t.Helper()
	g := eventSnapshotGenerator{objects: map[string][]*ast.FieldDecl{"ListItem": file.Types[0].Fields}, queued: make(map[string]bool)}
	var reference strings.Builder
	renames := []string{"SnapshotEvent", "snapshotEventBulkCopy", "copyEventListItem", "copyEventListItemBulkCopy", "cloneEventListItem", "cloneEventListItemBulkCopy"}
	for _, declaration := range file.Events {
		g.writeEvent(&reference, declaration)
		renames = append(renames, "clone"+declaration.Name+"Event", "clone"+declaration.Name+"EventBulkCopy")
	}
	for _, name := range g.queue {
		g.writeObject(&reference, name)
	}
	code := reference.String()
	restored := 0
	for _, declaration := range file.Events {
		for _, param := range declaration.Params {
			for _, source := range []string{"e", "value"} {
				field := str.Capitalize(param.Name)
				anchor := "out." + field + " = make(" + resolveGoType(param.Type) + ", len(" + source + "." + field + "))\n\tfor"
				restored += strings.Count(code, anchor)
				code = strings.ReplaceAll(code, anchor, strings.TrimSuffix(anchor, "for")+"copy(out."+field+", "+source+"."+field+")\n\tfor")
			}
		}
	}
	if restored != 7 {
		t.Fatalf("bulk-copy reference drifted: restored %d passes, want 7", restored)
	}
	return strings.NewReplacer(renames...).Replace(code)
}

const eventSnapshotCopyAssertions = `package generated
import("context";"fmt";"os";"reflect";"testing";"time";"encoding/json";"github.com/light-speak/luxo/pkg/lux/event")

type snapshotReference struct{ owned event.Snapshot }
func(s snapshotReference)SnapshotEvent()(event.Snapshot,error){return s.owned,nil}

func compareSnapshotCopies[T any](t *testing.T, value *T, candidate, reference func(*T)(event.Snapshot,error)){
    t.Helper()
    left,err:=candidate(value);if err!=nil{t.Fatal(err)}
    right,err:=reference(value);if err!=nil{t.Fatal(err)}
    var got []T
    bus:=event.NewChanBus(2)
    for range 2{if err:=event.OnDecode(bus,"copy",func([]byte,any)error{t.Error("unexpected wire decode");return nil},func(_ context.Context,v T)error{got=append(got,v);return nil});err!=nil{t.Fatal(err)}}
    for _,snapshot:=range []event.Snapshot{left,right}{if err:=bus.Emit(t.Context(),"copy",snapshotReference{snapshot});err!=nil{t.Fatal(err)}}
    bus.Close()
    if len(got)!=4{t.Fatal("missing copies")}
    for _,v:=range got[1:]{if !reflect.DeepEqual(v,got[0]){t.Fatalf("copy mismatch: %#v %#v",got[0],v)}}
}

func TestSnapshotCopyReferenceParity(t *testing.T){
    compareSnapshotCopies(t,&BytesRowsEvent{Values:[][]byte{nil,{}, {1,2}}},(*BytesRowsEvent).SnapshotEvent,(*BytesRowsEvent).snapshotEventBulkCopy)
    compareSnapshotCopies(t,&JSONRowsEvent{Values:[]json.RawMessage{[]byte("null"),[]byte("{\"n\":123456789012345678901234567890}")}},(*JSONRowsEvent).SnapshotEvent,(*JSONRowsEvent).snapshotEventBulkCopy)
    compareSnapshotCopies(t,&DateTimeRowsEvent{Values:[]time.Time{time.Unix(-1,123456789).In(time.FixedZone("offset",3600))}},(*DateTimeRowsEvent).SnapshotEvent,(*DateTimeRowsEvent).snapshotEventBulkCopy)
    compareSnapshotCopies(t,&ListItemRowsEvent{Values:[]ListItem{{Id:1,Name:"one"},{Id:2,Name:"two"}}},(*ListItemRowsEvent).SnapshotEvent,(*ListItemRowsEvent).snapshotEventBulkCopy)
    compareSnapshotCopies(t,&StringRowsEvent{Values:[]string{"one","two"}},(*StringRowsEvent).SnapshotEvent,(*StringRowsEvent).snapshotEventBulkCopy)
    compareSnapshotCopies(t,&BytesRowsEvent{},(*BytesRowsEvent).SnapshotEvent,(*BytesRowsEvent).snapshotEventBulkCopy)
}

var copiedSnapshotSink event.Snapshot
func benchmarkSnapshotCopies[T any](b *testing.B,value *T,candidate,reference func(*T)(event.Snapshot,error)){
    variants:=[]struct{name string; copy func(*T)(event.Snapshot,error)}{{"bulk",reference},{"direct",candidate}}
    if os.Getenv("LUXO_EVENT_BENCHMARK_REVERSE")=="1"{variants[0],variants[1]=variants[1],variants[0]}
    for _,variant:=range variants{b.Run(variant.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){
        owned,err:=variant.copy(value);if err!=nil{b.Fatal(err)};copiedSnapshotSink=owned
    }})}
}
func BenchmarkEventSnapshotListCopy(b *testing.B){
    for _,count:=range []int{1,128}{
        blobs:=&BytesRowsEvent{Values:make([][]byte,count)};raw:=&JSONRowsEvent{Values:make([]json.RawMessage,count)}
        dates:=&DateTimeRowsEvent{Values:make([]time.Time,count)};items:=&ListItemRowsEvent{Values:make([]ListItem,count)}
        texts:=&StringRowsEvent{Values:make([]string,count)}
        for i:=range count{blobs.Values[i]=[]byte{1,2,3};raw.Values[i]=[]byte("null");dates.Values[i]=time.Unix(-1,123456789);items.Values[i]=ListItem{Id:int64(i),Name:"snapshot"};texts.Values[i]="snapshot"}
        b.Run(fmt.Sprintf("Bytes/%d",count),func(b *testing.B){benchmarkSnapshotCopies(b,blobs,(*BytesRowsEvent).SnapshotEvent,(*BytesRowsEvent).snapshotEventBulkCopy)})
        b.Run(fmt.Sprintf("JSON/%d",count),func(b *testing.B){benchmarkSnapshotCopies(b,raw,(*JSONRowsEvent).SnapshotEvent,(*JSONRowsEvent).snapshotEventBulkCopy)})
        b.Run(fmt.Sprintf("DateTime/%d",count),func(b *testing.B){benchmarkSnapshotCopies(b,dates,(*DateTimeRowsEvent).SnapshotEvent,(*DateTimeRowsEvent).snapshotEventBulkCopy)})
        b.Run(fmt.Sprintf("Object/%d",count),func(b *testing.B){benchmarkSnapshotCopies(b,items,(*ListItemRowsEvent).SnapshotEvent,(*ListItemRowsEvent).snapshotEventBulkCopy)})
        b.Run(fmt.Sprintf("StringControl/%d",count),func(b *testing.B){benchmarkSnapshotCopies(b,texts,(*StringRowsEvent).SnapshotEvent,(*StringRowsEvent).snapshotEventBulkCopy)})
    }
}
`
