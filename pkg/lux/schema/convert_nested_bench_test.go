package schema

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Freeze the entire old nested dispatch, including both fallback branches.
// Valid payloads use the unchanged child converters on both sides.
func appendNestedColumnBlobBeforeStrict(dst, blob []byte, f *Field, schemas ...*Schema) ([]byte, error) {
	if len(blob) == 0 {
		if f.IsList {
			return append(dst, "[]"...), nil
		}
		return append(dst, "null"...), nil
	}
	if len(schemas) > 0 && schemas[0] != nil {
		nested := schemas[0].Models[f.TypeName]
		if nested == nil {
			// Type declarations (non-DB types like MetricPoint) live in Types
			if td := schemas[0].Types[f.TypeName]; td != nil {
				nested = asModelBeforePrebuild(td)
			}
		}
		if nested != nil {
			if f.IsList {
				return BinaryListToJSON(dst, blob, nested, schemas[0])
			}
			return BinaryToJSON(dst, blob, nested, schemas[0])
		}
	}
	if f.IsList {
		return append(dst, "[]"...), nil
	}
	return append(dst, "null"...), nil
}

func BenchmarkNestedJSONBoundary(b *testing.B) {
	for _, plainType := range []bool{false, true} {
		for _, list := range []bool{false, true} {
			for _, count := range []int{1, 128} {
				b.Run(fmt.Sprintf("type=%t/list=%t/count=%d", plainType, list, count), func(b *testing.B) {
					benchmarkNestedJSONBoundary(b, plainType, list, count)
				})
			}
		}
	}
}

func benchmarkNestedJSONBoundary(b *testing.B, plainType, list bool, count int) {
	s, field, child := nestedBoundaryBenchmarkFixture(b, plainType, list, count)
	if list {
		count = 1
	}
	schemas := []*Schema{s}
	expected, err := appendNestedColumnBlobBeforeStrict(nil, child, field, schemas...)
	actual, currentErr := appendNestedColumnBlobJSON(nil, child, field, schemas...)
	if err != nil || currentErr != nil || !bytes.Equal(expected, actual) {
		b.Fatalf("invalid baseline: %v / %v", err, currentErr)
	}
	for _, version := range []struct {
		name    string
		convert func([]byte, []byte, *Field, ...*Schema) ([]byte, error)
	}{
		{"before", appendNestedColumnBlobBeforeStrict}, {"strict", appendNestedColumnBlobJSON},
	} {
		b.Run(version.name, func(b *testing.B) {
			dst := make([]byte, 0, len(expected)*count)
			b.ReportAllocs()
			for b.Loop() {
				dst = dst[:0]
				for range count {
					dst, err = version.convert(dst, child, field, schemas...)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func nestedBoundaryBenchmarkFixture(t testing.TB, plainType, list bool, count int) (*Schema, *Field, []byte) {
	s, parent, child := nestedBoundaryFixture(t, list, false)
	if plainType {
		m := s.Models["Child"]
		s.RegisterType(&TypeDecl{Name: m.Name, Fields: m.Fields})
		delete(s.Models, m.Name)
	}
	if list {
		var w codec.ColumnarWriter
		w.SetCount(count)
		values := make([]int64, count)
		for i := range values {
			values[i] = int64(i)
		}
		w.WriteColumnInt(1, values)
		child = testutil.Bytes(t)(w.Bytes())
	}
	return s, &parent.Fields[1], child
}
