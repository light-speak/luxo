package schema

import (
	"bytes"
	"fmt"
	"testing"
)

var runtimeBenchmarkSchema *Schema

func runtimeBenchmarkType(count int) *TypeDecl {
	t := &TypeDecl{Name: "Payload", Fields: make([]Field, count)}
	for i := range t.Fields {
		t.Fields[i] = Field{ID: i + 1, Name: fmt.Sprintf("field%d", i), Type: FieldInt}
	}
	return t
}

func BenchmarkPreparedLookup(b *testing.B) {
	for _, count := range []int{2, 16} {
		s := New()
		s.RegisterType(runtimeBenchmarkType(count))
		s.RegisterModel(&Model{Name: "Record", Fields: s.Types["Payload"].Fields})
		for _, name := range []string{"Record", "Payload", "missing"} {
			for _, before := range []bool{true, false} {
				b.Run(fmt.Sprintf("fields=%d/%s/before=%t", count, name, before), func(b *testing.B) {
					lookup := (*Schema).LookupModel
					if before {
						lookup = lookupModelBeforePrebuild
					}
					b.ReportAllocs()
					for b.Loop() {
						m := lookup(s, name)
						if (m == nil) != (name == "missing") {
							b.Fatal("invalid lookup")
						}
					}
				})
			}
		}
	}
}

func BenchmarkTypeRegistration(b *testing.B) {
	for _, fields := range []int{2, 16} {
		decl := runtimeBenchmarkType(fields)
		for _, before := range []bool{true, false} {
			b.Run(fmt.Sprintf("fields=%d/before=%t", fields, before), func(b *testing.B) {
				b.Cleanup(func() { runtimeBenchmarkSchema = nil })
				b.ReportAllocs()
				for b.Loop() {
					s := New()
					if before {
						s.Types[decl.Name] = decl
					} else {
						s.RegisterType(decl)
					}
					if len(s.Types) != 1 {
						b.Fatal("missing registration")
					}
					runtimeBenchmarkSchema = s
				}
			})
		}
	}
}

func BenchmarkPreparedNestedJSON(b *testing.B) {
	for _, plainType := range []bool{false, true} {
		for _, list := range []bool{false, true} {
			for _, count := range []int{1, 128} {
				b.Run(fmt.Sprintf("type=%t/list=%t/count=%d", plainType, list, count), func(b *testing.B) {
					benchmarkPreparedNestedJSON(b, plainType, list, count)
				})
			}
		}
	}
}

func benchmarkPreparedNestedJSON(b *testing.B, plainType, list bool, count int) {
	s, field, child := nestedBoundaryBenchmarkFixture(b, plainType, list, count)
	if list {
		count = 1
	}
	previous, err := appendNestedJSONBeforePrebuild(nil, child, field, s)
	current, newErr := appendNestedColumnBlobJSON(nil, child, field, s)
	if err != nil || newErr != nil || !bytes.Equal(previous, current) {
		b.Fatalf("invalid reference: %v / %v", err, newErr)
	}
	for _, before := range []bool{true, false} {
		b.Run(fmt.Sprintf("before=%t", before), func(b *testing.B) {
			convert := appendNestedColumnBlobJSON
			if before {
				convert = appendNestedJSONBeforePrebuild
			}
			dst := make([]byte, 0, len(current)*count)
			b.ReportAllocs()
			for b.Loop() {
				dst = dst[:0]
				for range count {
					dst, err = convert(dst, child, field, s)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkAsModelCopy(b *testing.B) {
	for _, directives := range []bool{false, true} {
		decl := runtimeBenchmarkType(2)
		if directives {
			decl.Directives = []string{"typeDoc"}
			decl.Fields[0].Directives = []string{"fieldDoc"}
		}
		for _, before := range []bool{true, false} {
			b.Run(fmt.Sprintf("directives=%t/before=%t", directives, before), func(b *testing.B) {
				copy := (*TypeDecl).AsModel
				if before {
					copy = asModelBeforePrebuild
				}
				b.ReportAllocs()
				for b.Loop() {
					if len(copy(decl).Fields) != 2 {
						b.Fatal("invalid copy")
					}
				}
			})
		}
	}
}
