package api

import (
	"testing"
	"time"
)

func TestResponseBufAppendInt(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	buf.AppendInt(42)
	if string(buf.B) != "42" {
		t.Errorf("got %q", string(buf.B))
	}
}

func TestResponseBufAppendNegativeInt(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	buf.AppendInt(-123)
	if string(buf.B) != "-123" {
		t.Errorf("got %q", string(buf.B))
	}
}

func TestResponseBufAppendFloat(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	buf.AppendFloat(3.14)
	if string(buf.B) != "3.14" {
		t.Errorf("got %q", string(buf.B))
	}
}

func TestResponseBufAppendBool(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	buf.AppendBool(true)
	buf.AppendByte(',')
	buf.AppendBool(false)
	if string(buf.B) != "true,false" {
		t.Errorf("got %q", string(buf.B))
	}
}

func TestResponseBufAppendJSONString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{"", `""`},
		{`say "hi"`, `"say \"hi\""`},
		{"back\\slash", `"back\\slash"`},
		{"new\nline", `"new\nline"`},
		{"tab\there", `"tab\there"`},
		{"return\rhere", `"return\rhere"`},
		{"\x00null", `"\u0000null"`},
		{"\x1fcontrol", `"\u001fcontrol"`},
		{"日本語", `"日本語"`},
	}
	for _, tt := range tests {
		buf := GetBuf()
		buf.AppendJSONString(tt.input)
		got := string(buf.B)
		if got != tt.want {
			t.Errorf("AppendJSONString(%q) = %s, want %s", tt.input, got, tt.want)
		}
		PutBuf(buf)
	}
}

func TestResponseBufPool(t *testing.T) {
	buf := GetBuf()
	buf.AppendString("hello")
	PutBuf(buf)

	// Get again — should be reset
	buf2 := GetBuf()
	if len(buf2.B) != 0 {
		t.Errorf("pooled buf should be reset, len = %d", len(buf2.B))
	}
	PutBuf(buf2)
}

func TestResponseBufPoolSkipsLarge(t *testing.T) {
	buf := GetBuf()
	// Grow beyond 1MB
	buf.B = make([]byte, 0, 2<<20)
	PutBuf(buf) // should not panic, just discards

	buf2 := GetBuf()
	if cap(buf2.B) > 1<<20 {
		t.Error("should not pool buffers > 1MB")
	}
	PutBuf(buf2)
}

func TestResponseBufAppendBytes(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	buf.AppendBytes([]byte("raw"))
	if string(buf.B) != "raw" {
		t.Errorf("got %q", string(buf.B))
	}
}

func TestResponseBufBuildJSON(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)

	// Simulate a WriteJSON call
	buf.AppendByte('{')
	buf.AppendString(`"id":`)
	buf.AppendInt(42)
	buf.AppendByte(',')
	buf.AppendString(`"name":`)
	buf.AppendJSONString("lin")
	buf.AppendByte(',')
	buf.AppendString(`"active":`)
	buf.AppendBool(true)
	buf.AppendByte('}')

	want := `{"id":42,"name":"lin","active":true}`
	if string(buf.B) != want {
		t.Errorf("got %s, want %s", string(buf.B), want)
	}
}

func TestAppendJSONScalars(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"int64", int64(42), "42"},
		{"float64", float64(3.14), "3.14"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"string", "hello", `"hello"`},
		{"unknown", struct{}{}, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := GetBuf()
			defer PutBuf(buf)
			buf.AppendJSON(tt.val)
			if string(buf.B) != tt.want {
				t.Errorf("AppendJSON(%v) = %q, want %q", tt.val, string(buf.B), tt.want)
			}
		})
	}
}

func TestAppendTime(t *testing.T) {
	buf := GetBuf()
	defer PutBuf(buf)
	tm := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	buf.AppendTime(tm, time.RFC3339)
	want := `"2026-04-13T10:00:00Z"`
	if string(buf.B) != want {
		t.Errorf("AppendTime = %q, want %q", string(buf.B), want)
	}
}
