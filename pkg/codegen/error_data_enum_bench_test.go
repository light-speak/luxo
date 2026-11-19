package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedErrorEnumPerformanceFixture(t *testing.T) {
	status := &ast.EnumDecl{Name: "Status", Values: []string{"OPEN", "CLOSED"}}
	file := &ast.File{Enums: []*ast.EnumDecl{status}}
	for _, shape := range []struct {
		name string
		list bool
	}{{"Scalar", false}, {"List", true}} {
		file.Errors = append(file.Errors, &ast.ErrorDecl{Name: shape.name, Code: 422, Fields: []*ast.ParamDecl{
			{Name: "value", Type: &ast.TypeRef{Name: "Status", IsList: shape.list}},
		}})
	}
	source := appendErrorTestEnum(string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated")), status)
	runGeneratedProtocolPackage(t, source, errorEnumBenchmarks)
}

// Frozen generated field appenders before enum membership validation. Both
// versions use the current DataWriter and identical object/text wrappers.
const errorEnumBenchmarks = `package generated
import ("bytes"; "fmt"; "testing"; "github.com/light-speak/luxo/pkg/lux/errors")

func (data *ScalarErrorData) beforeField(w *errors.DataWriter,text bool) {
 if w.Err()!=nil { return }
 if text { w.Token(string(data.field0)) } else { w.String(string(data.field0)) }
}
func (data *ListErrorData) beforeField(w *errors.DataWriter,text bool) {
 if w.Err()!=nil { return }
 if data.field0==nil { w.Token("null");return }
 w.Token("[")
 for i:=range data.field0 {
  if i>0 { w.Token(",") }
  if w.Err()!=nil { return }
  w.String(string(data.field0[i]))
 }
 w.Token("]")
}
func (data *ScalarErrorData) beforeJSON(dst []byte)([]byte,error) {
 if data==nil {return append(dst,"null"...),nil}
 w:=errors.NewDataWriter(dst)
 w.Token("{\"value\":")
 data.beforeField(&w,false)
 w.Token("}")
 return w.Result()
}
func (data *ListErrorData) beforeJSON(dst []byte)([]byte,error) {
 if data==nil {return append(dst,"null"...),nil}
 w:=errors.NewDataWriter(dst)
 w.Token("{\"value\":")
 data.beforeField(&w,false)
 w.Token("}")
 return w.Result()
}
func (data *ScalarErrorData) beforeText(dst []byte,field string)([]byte,bool) {
 if data==nil {return dst,false}
 w:=errors.NewDataWriter(dst)
 switch field {case "value":data.beforeField(&w,true);default:return dst,false}
 result,err:=w.Result();return result,err==nil
}
func (data *ListErrorData) beforeText(dst []byte,field string)([]byte,bool) {
 if data==nil {return dst,false}
 w:=errors.NewDataWriter(dst)
 switch field {case "value":data.beforeField(&w,true);default:return dst,false}
 result,err:=w.Result();return result,err==nil
}
type enumErrorSample struct {
 current errors.ErrorData
 beforeJSON func([]byte)([]byte,error)
 beforeText func([]byte,string)([]byte,bool)
}
func TestErrorEnumBenchmarkParity(t *testing.T) {
 scalar:=NewScalar(StatusOPEN).Data.(*ScalarErrorData)
 list:=NewList([]Status{StatusOPEN,StatusCLOSED}).Data.(*ListErrorData)
 for _,sample:=range []enumErrorSample{{scalar,scalar.beforeJSON,scalar.beforeText},{list,list.beforeJSON,list.beforeText}} {
  expected,err:=sample.beforeJSON(nil);if err!=nil {t.Fatal(err)}
  actual,err:=sample.current.AppendJSON(nil);if err!=nil || !bytes.Equal(actual,expected) {t.Fatalf("%s != %s: %v",actual,expected,err)}
  expected,ok:=sample.beforeText(nil,"value");if !ok {t.Fatal("missing text")}
  actual,ok=sample.current.AppendText(nil,"value");if !ok || !bytes.Equal(actual,expected) {t.Fatalf("%s != %s",actual,expected)}
 }
}
func BenchmarkEnumErrorData(b *testing.B) {
 scalar:=NewScalar(StatusOPEN).Data.(*ScalarErrorData)
 benchErrorEnumPair(b,"Scalar",enumErrorSample{scalar,scalar.beforeJSON,scalar.beforeText})
 for _,count:=range []int{0,1,128} {
  values:=make([]Status,count)
  for i:=range values {values[i]=StatusOPEN;if i%2==1 {values[i]=StatusCLOSED}}
  data:=NewList(values).Data.(*ListErrorData)
  benchErrorEnumPair(b,fmt.Sprintf("List%d",count),enumErrorSample{data,data.beforeJSON,data.beforeText})
 }
}
func benchErrorEnumPair(b *testing.B,name string,sample enumErrorSample) {
 b.Helper()
 for _,version:=range []struct{name string;json func([]byte)([]byte,error);text func([]byte,string)([]byte,bool)}{
  {"Before",sample.beforeJSON,sample.beforeText},{"After",sample.current.AppendJSON,sample.current.AppendText},
 } {
   b.Run(name+"/JSON/"+version.name,func(b *testing.B){
    dst:=make([]byte,0,4096)
    want,err:=sample.beforeJSON(dst);if err!=nil {b.Fatal(err)}
    expected:=append([]byte(nil),want...)
    got,err:=version.json(dst[:0])
    if err!=nil || !bytes.Equal(got,expected) {b.Fatal("wire mismatch",err)}
    b.ReportAllocs();b.ResetTimer()
    for b.Loop(){result,err:=version.json(dst[:0]);if err!=nil || len(result)!=len(expected){b.Fatal(err)}}
   })
   b.Run(name+"/Text/"+version.name,func(b *testing.B){
    dst:=make([]byte,0,4096)
    want,ok:=sample.beforeText(dst,"value");if !ok {b.Fatal("missing text")}
    expected:=append([]byte(nil),want...)
    got,ok:=version.text(dst[:0],"value")
    if !ok || !bytes.Equal(got,expected) {b.Fatal("text mismatch")}
    b.ReportAllocs();b.ResetTimer()
    for b.Loop(){result,ok:=version.text(dst[:0],"value");if !ok || len(result)!=len(expected){b.Fatal("invalid text")}}
   })
 }
}
`

func TestGeneratedErrorModelListPerformanceFixture(t *testing.T) {
	detail := &ast.ModelDecl{Name: "Detail", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "level", Type: &ast.TypeRef{Name: "Int"}},
	}}
	file := &ast.File{Models: []*ast.ModelDecl{detail}, Errors: []*ast.ErrorDecl{{Name: "Rejected", Code: 422, Fields: []*ast.ParamDecl{
		{Name: "details", Type: &ast.TypeRef{Name: "Detail", IsList: true}},
	}}}}
	source := string(generateErrorFile(&semantic.Result{Files: []*ast.File{file}}, "generated"))
	before := strings.ReplaceAll(source, "Rejected", "UncheckedRejected")
	before = strings.ReplaceAll(before, "appendErrorDetail", "appendUncheckedErrorDetail")
	guard := `if data.field0[i] == nil { w.Reject("model list element is nil"); return }`
	if strings.Count(before, guard) != 1 {
		t.Fatal("generated nil guard changed")
	}
	before = strings.Replace(before, guard, "", 1)
	start := strings.Index(before, "// NewUncheckedRejected")
	if start < 0 {
		t.Fatal("generated constructor missing")
	}
	source += "\ntype Detail struct { Id int64; Level int64 }\n" + before[start:]
	runGeneratedProtocolPackage(t, source, errorModelListBenchmarks)
}

// The reference differs only by omitting the new per-element nil check.
// It is not the historical generator, whose model-list code did not compile.
const errorModelListBenchmarks = `package generated
import("bytes";"fmt";"testing")
func TestModelListBenchmarkParity(t *testing.T){
 values:=[]*Detail{{Id:1,Level:42},{Id:2,Level:0}}
 before:=NewUncheckedRejected(values).Data
 after:=NewRejected(values).Data
 expected,err:=before.AppendJSON(nil);if err!=nil{t.Fatal(err)}
 actual,err:=after.AppendJSON(nil);if err!=nil||!bytes.Equal(actual,expected){t.Fatalf("%s != %s: %v",actual,expected,err)}
 values[0]=nil
 func(){
  defer func(){if recover()==nil{t.Fatal("unchecked reference unexpectedly accepted nil")}}()
  _,_=before.AppendJSON(nil)
 }()
 if actual,err:=after.AppendJSON([]byte("prefix:"));err==nil||string(actual)!="prefix:"{t.Fatalf("invalid accepted %s: %v",actual,err)}
}
func BenchmarkErrorModelList(b *testing.B){
 for _,count:=range []int{0,1,128}{
  values:=make([]*Detail,count)
  for i:=range values{values[i]=&Detail{Id:int64(i),Level:42}}
  before:=NewUncheckedRejected(values).Data.(*UncheckedRejectedErrorData)
  after:=NewRejected(values).Data.(*RejectedErrorData)
  for _,version:=range []struct{name string;write func([]byte)([]byte,error)}{
   {"Unchecked",before.AppendJSON},{"Checked",after.AppendJSON},
  }{
   b.Run(fmt.Sprintf("%d/%s",count,version.name),func(b *testing.B){
    dst:=make([]byte,0,16384)
    expected,err:=before.AppendJSON(nil);if err!=nil{b.Fatal(err)}
    actual,err:=version.write(dst[:0]);if err!=nil||!bytes.Equal(actual,expected){b.Fatal("wire mismatch",err)}
    b.ReportAllocs();b.ResetTimer()
    for b.Loop(){raw,err:=version.write(dst[:0]);if err!=nil||len(raw)!=len(expected){b.Fatal("invalid output",err)}}
   })
  }
 }
}
`
