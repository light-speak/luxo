package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedModelListPerformance(t *testing.T) {
	ids := StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{testField("id", "Int")}}
	var reference strings.Builder
	g.generateDirectColumnar(&reference, model, nil, "writeBeforeNilCheck", "[]*User", true, nil)
	const guard = "\tfor _, item := range items { if item == nil { return codec.ErrNullModelElement } }\n"
	if strings.Count(reference.String(), guard) != 1 {
		t.Fatal("baseline must remove only the nil-element guard")
	}
	before := strings.Replace(reference.String(), guard, "", 1)
	runGeneratedFixture(t, "model User @noTime { id: Int @id }", modelListPerformanceAssertions, ids,
		"package generated\nimport(\"github.com/light-speak/luxo/pkg/lux/api\";\"github.com/light-speak/luxo/pkg/lux/codec\")\n"+before)
}

const modelListPerformanceAssertions = `package generated
import("bytes";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api")
var modelListValuesSink []User
var modelListReferencesSink []*User
func TestModelReferenceIndexOwnsOnlyPointers(t *testing.T){
 if referenceUserList(nil)!=nil{t.Fatal("nil identity changed")}
 if refs:=referenceUserList([]User{});refs==nil||len(refs)!=0{t.Fatal("empty identity changed")}
 values:=[]User{{Id:1},{Id:2}}
 refs:=referenceUserList(values)
 if len(refs)!=2||refs[0]!=&values[0]||refs[1]!=&values[1]{t.Fatal("model values were copied")}
 refs[0].Id=42
 if values[0].Id!=42||values[1].Id!=2{t.Fatal("reference index changed aliasing")}
 raw:=[]byte("[{\"id\":1},{\"id\":2}]")
 decoded,err:=ReadJSONInputUserList(raw);if err!=nil{t.Fatal(err)}
 owned:=referenceUserList(decoded);clear(raw)
 if owned[0].Id!=1||owned[1].Id!=2{t.Fatal("input buffer aliases the model storage")}
}
func TestModelListBenchmarkControls(t *testing.T){
 for _,count:=range []int{0,1,128}{
  values:=make([]User,count);for i:=range values{values[i].Id=int64(i)}
  refs:=referenceUserList(values)
  before,after:=api.GetBuf(),api.GetBuf()
  if err:=writeBeforeNilCheck(before,refs,nil);err!=nil{t.Fatal(err)}
  if err:=WriteColumnarUser(after,refs,nil);err!=nil{t.Fatal(err)}
  if !bytes.Equal(before.B,after.B){t.Fatal("valid wire changed",count)}
  api.PutBuf(before);api.PutBuf(after)
 }
 buf:=api.GetBuf();defer api.PutBuf(buf)
 if err:=WriteColumnarUser(buf,[]*User{nil},nil);err==nil{t.Fatal("nil accepted")}
 if len(buf.B)!=0{t.Fatal("nil rejection wrote a partial message")}
}
func BenchmarkModelListNilCheck(b *testing.B){
 for _,count:=range []int{0,1,128}{
  values:=make([]User,count);for i:=range values{values[i].Id=int64(i)}
  refs:=referenceUserList(values)
  for _,candidate:=range []bool{false,true}{
   b.Run(strconv.Itoa(count)+"/candidate="+strconv.FormatBool(candidate),func(b *testing.B){
    buf:=api.GetBuf();defer api.PutBuf(buf);buf.B=make([]byte,0,4096)
    b.ReportAllocs();b.ResetTimer()
    if candidate{for b.Loop(){buf.B=buf.B[:0];if err:=WriteColumnarUser(buf,refs,nil);err!=nil{b.Fatal(err)}}
    }else{for b.Loop(){buf.B=buf.B[:0];if err:=writeBeforeNilCheck(buf,refs,nil);err!=nil{b.Fatal(err)}}}
   })
  }
 }
}
func BenchmarkModelListReadIndex(b *testing.B){
 for _,count:=range []int{0,1,128}{
  raw:=[]byte("[");for i:=0;i<count;i++{if i>0{raw=append(raw,',')};raw=append(raw,[]byte("{\"id\":42}")...)};raw=append(raw,']')
  for _,candidate:=range []bool{false,true}{
   b.Run(strconv.Itoa(count)+"/candidate="+strconv.FormatBool(candidate),func(b *testing.B){
    b.ReportAllocs()
    if candidate{for b.Loop(){values,err:=ReadJSONInputUserList(raw);if err!=nil{b.Fatal(err)};modelListReferencesSink=referenceUserList(values)}
    }else{for b.Loop(){values,err:=ReadJSONInputUserList(raw);if err!=nil{b.Fatal(err)};modelListValuesSink=values}}
   })
  }
 }
}
`
