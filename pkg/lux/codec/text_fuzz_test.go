package codec

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf8"
)

func FuzzReadStringUTF8(f *testing.F) {
	for _, raw := range append(invalidTextVectors(), []byte("\ufeff東京"), []byte{}) {
		f.Add(binary.AppendUvarint(nil, uint64(len(raw))), raw)
	}
	f.Fuzz(func(t *testing.T, header, raw []byte) {
		data := append(append([]byte(nil), header...), raw...)
		length, n := binary.Uvarint(data)
		valid := n > 0 && length <= uint64(len(data)-n)
		var want []byte
		if valid {
			want = data[n : n+int(length)]
			for rest := want; len(rest) > 0; {
				r, size := utf8.DecodeRune(rest)
				if r == utf8.RuneError && size == 1 {
					valid = false
					break
				}
				rest = rest[size:]
			}
		}
		got, consumed := ReadString(data, 0)
		if valid {
			if !bytes.Equal([]byte(got), want) || consumed != n+len(want) {
				t.Fatal("valid text changed")
			}
		} else if got != "" || consumed != 0 {
			t.Fatal("invalid text consumed")
		}
		d := NewDecoder(data)
		d.SkipString()
		if (d.Err() == nil) != valid || d.Offset() != consumed {
			t.Fatal("skip/read mismatch")
		}
	})
}
