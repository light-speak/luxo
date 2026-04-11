package api

import (
	"testing"
)

func TestWriteJSONStringViaResponseBuf(t *testing.T) {
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
