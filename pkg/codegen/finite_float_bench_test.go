package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedFloatPerformanceFixture(t *testing.T) {
	model := &ast.ModelDecl{Name: "Sample", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "value", Type: &ast.TypeRef{Name: "Float"}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Sample": {"id": 1, "value": 2}})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"encoding/binary\"; \"math\"; \"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateModel(&source, model, nil)
	g.generateWriteLuxo(&source, model, nil)
	g.generateWriteColumnar(&source, model, nil)
	source.WriteString(floatWriterBaseline)
	runGeneratedProtocolPackage(t, source.String(), floatWriterBenchmark)
}

// Frozen pre-error-interface writers: same mask checks, headers and append order.
// Do not replace the unchecked primitive with the production checked writer.
const floatWriterBaseline = `
func appendFloatBaseline(dst []byte, value float64) []byte {
    var buf [8]byte
    binary.LittleEndian.PutUint64(buf[:], math.Float64bits(value))
    return append(dst, buf[:]...)
}
func (s *Sample) writeRowBaseline(buf *api.ResponseBuf, mask []byte) {
    if len(mask) == 0 {
        buf.B = append(buf.B, 0)
        buf.B = codec.AppendVarint(buf.B, 1)
        buf.B = codec.AppendSvarint(buf.B, s.Id)
        buf.B = codec.AppendVarint(buf.B, 2)
        buf.B = appendFloatBaseline(buf.B, s.Value)
        buf.B = append(buf.B, 0)
        return
    }
    mask = codec.SelectionMaskFields(mask)
    buf.B = append(buf.B, 0)
    if codec.FieldMaskHas(mask, 1) {
        buf.B = codec.AppendVarint(buf.B, 1)
        buf.B = codec.AppendSvarint(buf.B, s.Id)
    }
    if codec.FieldMaskHas(mask, 2) {
        buf.B = codec.AppendVarint(buf.B, 2)
        buf.B = appendFloatBaseline(buf.B, s.Value)
    }
    buf.B = append(buf.B, 0)
}
func writeColumnBaseline(buf *api.ResponseBuf, items []Sample, mask []byte) {
    if len(items) == 0 { buf.B = append(buf.B, 0, 0, 0); return }
    mask = codec.SelectionMaskFields(mask)
    buf.B = codec.AppendVarint(buf.B, uint64(len(items)))
    buf.B = append(buf.B, 0)
    if len(mask) == 0 || codec.FieldMaskHas(mask, 1) {
        buf.B = codec.AppendVarint(buf.B, 1)
        for row := range items { buf.B = codec.AppendSvarint(buf.B, items[row].Id) }
    }
    if len(mask) == 0 || codec.FieldMaskHas(mask, 2) {
        buf.B = codec.AppendVarint(buf.B, 2)
        for row := range items { buf.B = appendFloatBaseline(buf.B, items[row].Value) }
    }
    buf.B = append(buf.B, 0)
}
`

const floatWriterBenchmark = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

    "bytes"
    "fmt"
    "math"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
)
func TestFloatBaselineParityAndAllocations(t *testing.T) {
    values := []Sample{{1,math.MaxFloat64},{2,math.SmallestNonzeroFloat64},{3,math.Copysign(0,-1)}}
    masks := [][]byte{nil,testutil.Bytes(t)(codec.AppendSelectionMask(nil, []byte{3}, nil)),testutil.Bytes(t)(codec.AppendSelectionMask(nil, []byte{1}, nil))}
    for _, mask := range masks {
        for _, columns := range []bool{false,true} {
            baseline := api.ResponseBuf{B:make([]byte,0,4096)}
            checked := api.ResponseBuf{B:make([]byte,0,4096)}
            if columns { writeColumnBaseline(&baseline,values,mask) } else {
                for i := range values { values[i].writeRowBaseline(&baseline,mask) }
            }
            allocs := testing.AllocsPerRun(100,func(){
                checked.Reset()
                if columns {
                    if err := WriteColumnarSampleValues(&checked,values,mask); err != nil { t.Fatal(err) }
                } else {
                    for i := range values { if err := values[i].WriteLuxo(&checked,mask); err != nil { t.Fatal(err) } }
                }
            })
            if !bytes.Equal(baseline.B,checked.B) || allocs != 0 { t.Fatalf("wire or allocation mismatch: %x / %x, allocations=%g",baseline.B,checked.B,allocs) }
        }
    }
}
func BenchmarkGeneratedFloat(b *testing.B) {
    for _, count := range []int{1,128} {
        for _, columns := range []bool{false,true} {
            for _, checked := range []bool{false,true} {
                b.Run(fmt.Sprintf("rows=%d/column=%t/checked=%t",count,columns,checked),func(b *testing.B){
                    values := make([]Sample,count)
                    for i := range values { values[i] = Sample{Id:int64(i),Value:float64(i)+0.25} }
                    mask := testutil.Bytes(b)(codec.AppendSelectionMask(nil,[]byte{3},nil))
                    buf := api.ResponseBuf{B:make([]byte,0,count*32)}
                    b.ReportAllocs()
                    for b.Loop() {
                        buf.Reset()
                        if columns {
                            if checked {
                                if err := WriteColumnarSampleValues(&buf,values,mask); err != nil { b.Fatal(err) }
                            } else { writeColumnBaseline(&buf,values,mask) }
                        } else {
                            for i := range values {
                                if checked {
                                    if err := values[i].WriteLuxo(&buf,mask); err != nil { b.Fatal(err) }
                                } else { values[i].writeRowBaseline(&buf,mask) }
                            }
                        }
                    }
                })
            }
        }
    }
}
`
