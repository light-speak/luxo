package codec

import (
	"bytes"
	"strings"
	"testing"
)

type stringWriteBenchmark struct {
	name          string
	before, after func(*Encoder, string, []string) error
}

func BenchmarkStringWriteUTF8(b *testing.B) {
	writers := []stringWriteBenchmark{
		{"value", writeStringValueBeforeUTF8, func(e *Encoder, v string, _ []string) error { return e.WriteString(v) }},
		{"field", writeStringFieldBeforeUTF8, func(e *Encoder, v string, _ []string) error { return e.WriteFieldString(256, v) }},
		{"nullable", writeStringPtrBeforeUTF8, func(e *Encoder, v string, _ []string) error { return e.WriteFieldStringPtr(256, &v) }},
		{"array128", writeStringArrayBeforeUTF8, func(e *Encoder, _ string, v []string) error { return e.WriteFieldStringArray(256, v) }},
	}
	for _, fixture := range []struct{ name, value string }{
		{"ascii16", "0123456789abcdef"}, {"unicode24", strings.Repeat("東京", 4)},
		{"unicode1026", strings.Repeat("東京", 171)},
	} {
		for _, writer := range writers {
			b.Run(writer.name+"/"+fixture.name, func(b *testing.B) { benchmarkStringWriter(b, writer, fixture.value) })
		}
	}
}

func benchmarkStringWriter(b *testing.B, writer stringWriteBenchmark, value string) {
	values := make([]string, 128)
	for i := range values {
		values[i] = value
	}
	before, after := &Encoder{}, &Encoder{}
	if err := writer.before(before, value, values); err != nil {
		b.Fatal(err)
	}
	if err := writer.after(after, value, values); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		b.Fatalf("wire mismatch: %v", err)
	}
	for _, candidate := range []struct {
		name  string
		write func(*Encoder, string, []string) error
	}{{"before", writer.before}, {"after", writer.after}} {
		b.Run(candidate.name, func(b *testing.B) {
			encoder := &Encoder{buf: make([]byte, 0, len(before.Bytes()))}
			var err error
			b.ReportAllocs()
			for b.Loop() {
				encoder.Reset()
				err = candidate.write(encoder, value, values)
			}
			if err != nil {
				b.Fatal(err)
			}
		})
	}
}

func BenchmarkStringColumnWriteUTF8(b *testing.B) {
	for _, fixture := range []struct{ name, value string }{{"ascii16", "0123456789abcdef"}, {"unicode1026", strings.Repeat("東京", 171)}} {
		values, pointers := make([]string, 128), make([]*string, 128)
		for i := range values {
			values[i] = fixture.value
			pointers[i] = &values[i]
		}
		for _, candidate := range []struct {
			name  string
			write func(*ColumnarWriter) error
		}{
			{"value/before", func(w *ColumnarWriter) error { return writeStringColumnBeforeUTF8(w, values) }},
			{"value/after", func(w *ColumnarWriter) error { return w.WriteColumnString(256, values) }},
			{"nullable/before", func(w *ColumnarWriter) error { return writeStringPtrColumnBeforeUTF8(w, pointers) }},
			{"nullable/after", func(w *ColumnarWriter) error { return w.WriteColumnStringPtr(256, pointers) }},
		} {
			b.Run(fixture.name+"/"+candidate.name, func(b *testing.B) {
				var writer ColumnarWriter
				if err := candidate.write(&writer); err != nil || writer.totalStringLen != 128*len(fixture.value) {
					b.Fatalf("invalid column: %v", err)
				}
				var err error
				b.ReportAllocs()
				for b.Loop() {
					writer.Reset()
					writer.SetCount(128)
					err = candidate.write(&writer)
				}
				if err != nil {
					b.Fatal(err)
				}
			})
		}
	}
}
