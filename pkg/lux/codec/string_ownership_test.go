package codec

import (
	"slices"
	"testing"
)

func TestReadStringOwnsInput(t *testing.T) {
	for _, want := range []string{"", "plain", "中文", "\ufeffvalue", "𝄞中"} {
		for _, decoder := range []bool{false, true} {
			raw, err := AppendString(nil, want)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if decoder {
				dec := NewDecoder(raw)
				got = dec.ReadString()
				if err := dec.Finish(); err != nil {
					t.Fatal(err)
				}
			} else {
				var read int
				got, read = ReadString(raw, 0)
				if read != len(raw) {
					t.Fatalf("read %d of %d bytes", read, len(raw))
				}
			}
			clear(raw)
			if got != want {
				t.Fatalf("decoder=%t input reuse changed String: %q != %q", decoder, got, want)
			}
		}
	}
}

func TestNullableStringOwnsInput(t *testing.T) {
	raw, err := AppendString(AppendPresent(nil), "中文")
	if err != nil {
		t.Fatal(err)
	}
	dec := NewDecoder(raw)
	got := dec.ReadStringPtr()
	if err := dec.Finish(); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if got == nil || *got != "中文" {
		t.Fatalf("input reuse changed nullable String: %v", got)
	}
}

func TestStringArrayOwnsInput(t *testing.T) {
	want := []string{"", "alpha", "中文", "\ufefflast"}
	raw := AppendVarint(nil, uint64(len(want)))
	for _, value := range want {
		var err error
		raw, err = AppendString(raw, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	dec := NewDecoder(raw)
	got := dec.ReadStringArray()
	if err := dec.Finish(); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if !slices.Equal(got, want) {
		t.Fatalf("input reuse changed String array: %q != %q", got, want)
	}
}

func TestStringArenaOwnsInput(t *testing.T) {
	want := "\ufeff中文"
	raw, err := AppendString(nil, want)
	if err != nil {
		t.Fatal(err)
	}
	arena := make([]byte, len(want))
	offset := 0
	dec := NewDecoder(raw)
	got := dec.ReadStringArena(arena, &offset)
	if err := dec.Finish(); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if got != want || offset != len(want) {
		t.Fatalf("input reuse changed arena String: %q, offset %d", got, offset)
	}
}
