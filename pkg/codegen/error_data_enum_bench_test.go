package codegen

import (
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
