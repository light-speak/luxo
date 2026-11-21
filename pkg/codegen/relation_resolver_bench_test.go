package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

// Export both revisions through the same formal generator and benchmark source.
// Only explicit audit runs retain this fixture; normal tests create no artifacts.
func TestExportNestedRelationBenchmark(t *testing.T) {
	destination := os.Getenv("LUXO_NESTED_RELATION_FIXTURE")
	if destination == "" {
		t.Skip("requires an explicit project-local benchmark destination")
	}
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		t.Fatal("benchmark destination must be inside the project .tmp", err)
	}
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if err := files.Mkdir(relative, 0o700); err != nil {
		t.Fatal(err)
	}
	complete := false
	defer func() {
		if !complete {
			if err := files.RemoveAll(relative); err != nil {
				t.Error("clean incomplete benchmark fixture", err)
			}
		}
	}()
	input := analyzePermissionFixture(t, nestedRelationBatchSource)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{
		"Node": {"id": 1, "parentId": 2, "parent": 3, "children": 4},
	}})
	generator := mustNewGenerator(t, GeneratorConfig{IDs: ids})
	output, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range output.Files {
		if filepath.Base(name) != name {
			t.Fatal("unexpected nested fixture output", name)
		}
		if err := files.WriteFile(filepath.Join(relative, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := files.WriteFile(filepath.Join(relative, "relation_bench_test.go"), []byte(nestedRelationBenchmarks), 0o600); err != nil {
		t.Fatal(err)
	}
	complete = true
}

const nestedRelationBenchmarks = `package generated
import("context";"strconv";"testing";"time";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
func BenchmarkKnownReadControl(b *testing.B){
 for _,early:=range []bool{false,true}{for _,all:=range []bool{false,true}{
  name:="unlimited/single";if early{name="earlier/single"};if all{if early{name="earlier/list"}else{name="unlimited/list"}}
  b.Run(name,func(b *testing.B){
   var ctx context.Context=b.Context();if early{bounded,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel();ctx=bounded}
   rows:=map[int]int{1:42};loader:=dataloader.New(func(context.Context,[]int,[]*selection.Field)(map[int]int,error){return rows,nil},dataloader.Config{MaxBatch:1});keys:=[]int{1}
   b.ReportAllocs();b.ResetTimer();for range b.N{
    if all{got,err:=loader.LoadAll(ctx,keys,nil);if err!=nil||got[1]!=42{b.Fatal(got,err)}}else{got,err:=loader.Load(ctx,1,nil);if err!=nil||got!=42{b.Fatal(got,err)}}
   }
  })
 }}
}
func BenchmarkNestedRelations(b *testing.B){
 for _,count:=range []int{0,1,8,128}{for _,kind:=range []string{"leaf","parent","children"}{for _,values:=range []bool{false,true}{
  name:=strconv.Itoa(count)+"/"+kind+"/refs";if values{name=strconv.Itoa(count)+"/"+kind+"/values"}
  b.Run(name,func(b *testing.B){
   app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
   var calls int64;parents:=map[int64]*Node{};children:=map[int64][]*Node{}
   nodes:=make([]Node,count);refs:=make([]*Node,count)
   for i:=range nodes{key:=int64(i);nodes[i]=Node{Id:key+1,ParentId:new(key+1000)};refs[i]=&nodes[i]
    parents[key+1000]=&Node{Id:key+1000,ParentId:new(key+2000)};parents[key+2000]=&Node{Id:key+2000}
    children[key+1]=[]*Node{{Id:key+1000,ParentId:new(key+2000)}}
   }
   app.SetLoaders(NewLoaders(cfg,
    func(context.Context,[]int64,[]*selection.Field)(map[int64]*Node,error){calls++;return parents,nil},
    func(context.Context,[]int64,[]*selection.Field)(map[int64][]*Node,error){calls++;return children,nil},
   ))
   scope,err:=dataloader.NewScope(b.Context());if err!=nil{b.Fatal(err)}
   chosen:="id";if kind=="parent"{chosen="parent{parent{id}}"};if kind=="children"{chosen="children{parent{id}}"}
   fields,err:=selection.Parse(chosen);if err!=nil{b.Fatal(err)}
   b.ReportAllocs();b.ResetTimer()
   for range b.N{
    var err error;if values{err=resolveNodeValueListRelations(scope,app,nodes,fields)}else{err=resolveNodeListRelations(scope,app,refs,fields)}
    if err!=nil{b.Fatal(err)}
   }
   b.StopTimer();b.ReportMetric(float64(calls)/float64(b.N),"batches/op")
   for _,node:=range nodes{switch kind{case "parent":if node.Parent==nil||node.Parent.Parent==nil||node.Parent.Parent.Id!=*node.Parent.ParentId{b.Fatal("parent result changed")};case "children":if len(node.Children)!=1||node.Children[0].Parent==nil||node.Children[0].Parent.Id!=*node.Children[0].ParentId{b.Fatal("child result changed")}}}
  })
 }}}
}
`
