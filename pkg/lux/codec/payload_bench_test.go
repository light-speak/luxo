package codec

import (
	"fmt"
	"testing"
)

var payloadReadCount int
var payloadReadError error

func BenchmarkElementPayload(b *testing.B) {
	before, after := payloadReaderCasesBefore(), payloadReaderCases()
	for index, tc := range after {
		for _, count := range []int{0, 1, 128} {
			for _, column := range []bool{false, true} {
				if !column && tc.array == nil {
					continue
				}
				data := make([]byte, count*tc.width)
				if !column {
					data = append(AppendArrayHeader(nil, count), data...)
				}
				for version, reader := range []payloadReaderCase{before[index], tc} {
					b.Run(fmt.Sprintf("%s/column=%v/count=%d/version=%d", tc.name, column, count, version), func(b *testing.B) {
						benchmarkPayloadRead(b, reader, column, count, data, true)
					})
				}
			}
		}
	}
}

func BenchmarkMissingElementPayload(b *testing.B) {
	before, after := payloadReaderCasesBefore(), payloadReaderCases()
	for index, tc := range after {
		for _, column := range []bool{false, true} {
			if !column && tc.array == nil {
				continue
			}
			var data []byte
			if !column {
				data = AppendArrayHeader(nil, 16384)
			}
			for version, reader := range []payloadReaderCase{before[index], tc} {
				b.Run(fmt.Sprintf("%s/column=%v/version=%d", tc.name, column, version), func(b *testing.B) {
					benchmarkPayloadRead(b, reader, column, 16384, data, false)
				})
			}
		}
	}
}

func benchmarkPayloadRead(b *testing.B, tc payloadReaderCase, column bool, count int, data []byte, valid bool) {
	var row Decoder
	var col ColumnarReader
	read := func() (int, int, error) {
		if column {
			col = ColumnarReader{buf: data, count: count}
			n := tc.column(&col)
			return n, col.off, col.err
		}
		row = Decoder{buf: data}
		n := tc.array(&row)
		return n, row.off, row.err
	}
	got, off, err := read()
	if valid && (got != count || off != len(data) || err != nil) {
		b.Fatalf("valid benchmark input changed: count=%d off=%d err=%v", got, off, err)
	}
	if !valid && (got != 0 || err == nil) {
		b.Fatal("invalid benchmark input accepted")
	}
	b.ReportAllocs()
	for b.Loop() {
		payloadReadCount, _, payloadReadError = read()
	}
}
