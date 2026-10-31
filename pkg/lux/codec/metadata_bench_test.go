package codec

import (
	"fmt"
	"testing"
)

var metadataCount, metadataOffset int
var metadataError error

func BenchmarkMetadataBounds(b *testing.B) {
	for _, kind := range []string{"array-header", "array-length", "skip-array", "row-field", "column-field", "skip-arena"} {
		values := []int{1, 128}
		switch kind {
		case "array-header", "array-length":
			values = []int{0, 128, MaxArrayElements}
		case "row-field", "column-field":
			values = []int{1, int(^uint(0) >> 1)}
		case "skip-arena":
			values = []int{0, MaxArenaSize}
		}
		for _, value := range values {
			data := AppendVarint(nil, uint64(value))
			if kind == "skip-array" || kind == "skip-arena" {
				data = append(data, make([]byte, value)...)
			}
			for _, before := range []bool{true, false} {
				b.Run(fmt.Sprintf("%s/%d/before=%v", kind, value, before), func(b *testing.B) {
					benchmarkMetadataOperation(b, kind, data, before)
				})
			}
		}
	}
}

func benchmarkMetadataOperation(b *testing.B, kind string, data []byte, before bool) {
	b.ReportAllocs()
	for b.Loop() {
		decoder := Decoder{buf: data}
		switch kind {
		case "array-header":
			if before {
				metadataCount, metadataOffset = readArrayHeaderBeforeUnsigned(data, 0)
			} else {
				metadataCount, metadataOffset = ReadArrayHeader(data, 0)
			}
			continue
		case "array-length":
			if before {
				metadataCount = decoder.readArrayLengthBeforeUnsigned()
			} else {
				metadataCount = decoder.ReadArrayLength()
			}
		case "skip-array":
			if before {
				decoder.skipArrayBeforeUnsigned(SkipVarint)
			} else {
				decoder.SkipArray(SkipVarint)
			}
		case "row-field":
			if before {
				decoder.nextFieldBeforeUnsigned()
			} else {
				decoder.NextField()
			}
			metadataCount = decoder.fieldID
		case "skip-arena":
			if before {
				decoder.skipArenaHeaderBeforeUnsigned()
			} else {
				decoder.SkipArenaHeader()
			}
		case "column-field":
			benchmarkMetadataColumn(data, before)
			continue
		}
		metadataOffset, metadataError = decoder.off, decoder.err
	}
}

func benchmarkMetadataColumn(data []byte, before bool) {
	reader := ColumnarReader{buf: data}
	if before {
		reader.nextColumnBeforeUnsigned()
	} else {
		reader.NextColumn()
	}
	metadataCount, metadataOffset, metadataError = reader.fieldID, reader.off, reader.err
}
