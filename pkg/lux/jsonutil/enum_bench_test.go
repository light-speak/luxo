package jsonutil

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Retain the bounded string reader before the generic named-string extraction.
func stringArrayBeforeNamed(raw []byte, limit int) ([]string, error) {
	var text strings.Builder
	count := 0
	return parseArrayReader(raw, func(reader *Reader) (string, error) {
		if count >= limit {
			return "", errArrayLimit
		}
		count++
		return readOwnedString(reader, &text, min(len(raw), ownedTextBlockSize))
	})
}

func BenchmarkEnumStringControl(b *testing.B) {
	for _, sample := range []struct {
		name, text string
		count      int
	}{
		{"empty", "", 0}, {"single", "OPEN", 1}, {"128short", "OPEN", 128},
		{"128long", strings.Repeat("x", 1024), 128}, {"72KB", strings.Repeat("中", 24*1024), 1},
	} {
		var raw strings.Builder
		raw.WriteByte('[')
		for i := range sample.count {
			if i != 0 {
				raw.WriteByte(',')
			}
			raw.WriteString(strconv.Quote(sample.text))
		}
		raw.WriteByte(']')
		data := []byte(raw.String())
		for _, reader := range []struct {
			name string
			read func([]byte, int) ([]string, error)
		}{{"before", stringArrayBeforeNamed}, {"after", ParseStringArrayLimit}} {
			b.Run(fmt.Sprintf("%s/%s", sample.name, reader.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					values, err := reader.read(data, 128)
					if err != nil || len(values) != sample.count {
						b.Fatal(values, err)
					}
					if len(values) > 0 && values[len(values)-1] != sample.text {
						b.Fatal("invalid result")
					}
				}
			})
		}
	}
}
