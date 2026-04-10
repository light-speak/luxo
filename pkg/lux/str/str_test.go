package str

import "testing"

func TestToSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"userId", "user_id"},
		{"createdAt", "created_at"},
		{"name", "name"},
		{"ID", "i_d"},
		{"OrderItem", "order_item"},
		{"HTMLParser", "h_t_m_l_parser"},
		{"", ""},
		{"a", "a"},
	}
	for _, tt := range tests {
		if got := ToSnakeCase(tt.in); got != tt.want {
			t.Errorf("ToSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"name", "Name"},
		{"userId", "UserId"},
		{"", ""},
		{"A", "A"},
	}
	for _, tt := range tests {
		if got := Capitalize(tt.in); got != tt.want {
			t.Errorf("Capitalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLowerFirst(t *testing.T) {
	tests := []struct{ in, want string }{
		{"User", "user"},
		{"Post", "post"},
		{"", ""},
		{"a", "a"},
	}
	for _, tt := range tests {
		if got := LowerFirst(tt.in); got != tt.want {
			t.Errorf("LowerFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTrim(t *testing.T) {
	if Trim("  hello  ") != "hello" {
		t.Error("trim")
	}
}

func TestLowercaseUppercase(t *testing.T) {
	if Lowercase("Hello") != "hello" {
		t.Error("lowercase")
	}
	if Uppercase("hello") != "HELLO" {
		t.Error("uppercase")
	}
}

func TestContains(t *testing.T) {
	if !Contains("hello world", "world") {
		t.Error("contains")
	}
	if Contains("hello", "xyz") {
		t.Error("not contains")
	}
}

func TestReplace(t *testing.T) {
	if Replace("hello world", " ", "-") != "hello-world" {
		t.Error("replace")
	}
}

func TestSplit(t *testing.T) {
	parts := Split("a,b,c", ",")
	if len(parts) != 3 || parts[1] != "b" {
		t.Errorf("split: %v", parts)
	}
}

func TestStartsEndsWith(t *testing.T) {
	if !StartsWith("hello", "hel") {
		t.Error("starts")
	}
	if !EndsWith("hello", "llo") {
		t.Error("ends")
	}
}

func TestTrimPrefixSuffix(t *testing.T) {
	if TrimPrefix("hello", "hel") != "lo" {
		t.Error("prefix")
	}
	if TrimSuffix("hello", "llo") != "he" {
		t.Error("suffix")
	}
}

func TestRepeat(t *testing.T) {
	if Repeat("ab", 3) != "ababab" {
		t.Error("repeat")
	}
}

func TestPadLeftRight(t *testing.T) {
	if PadLeft("5", 3, "0") != "005" {
		t.Errorf("pad left: %q", PadLeft("5", 3, "0"))
	}
	if PadRight("5", 3, "0") != "500" {
		t.Errorf("pad right: %q", PadRight("5", 3, "0"))
	}
}

func BenchmarkToSnakeCase(b *testing.B) {
	for range b.N {
		ToSnakeCase("createdAt")
	}
}
