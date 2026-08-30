package str

import "testing"

func TestToSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"userId", "user_id"},
		{"createdAt", "created_at"},
		{"name", "name"},
		{"ID", "id"},
		{"OrderItem", "order_item"},
		{"HTMLParser", "html_parser"},
		{"userID", "user_id"},
		{"getHTTPResponse", "get_http_response"},
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

func TestReverse(t *testing.T) {
	if got := Reverse("hello"); got != "olleh" {
		t.Errorf("got %q", got)
	}
	if got := Reverse(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := Reverse("a"); got != "a" {
		t.Errorf("single = %q", got)
	}
}

func TestMask(t *testing.T) {
	if got := Mask("13812345678", 3, 4); got != "138****5678" {
		t.Errorf("phone mask = %q", got)
	}
	if got := Mask("ab", 3, 4); got != "ab" {
		t.Errorf("short string should not mask = %q", got)
	}
	if got := Mask("abcdef", 1, 1); got != "a****f" {
		t.Errorf("got %q", got)
	}
}

func TestMaskPattern(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		pattern string
		want    string
	}{
		{name: "phone", value: "13812345678", pattern: "###****####", want: "138****5678"},
		{name: "identity", value: "11010519491231002X", pattern: "####**********####", want: "1101**********002X"},
		{name: "unicode", value: "张三丰", pattern: "#**", want: "张**"},
		{name: "length mismatch", value: "13812345678", pattern: "###****###", want: "***********"},
		{name: "invalid pattern", value: "secret", pattern: "##x***", want: "******"},
		{name: "empty pattern", value: "secret", pattern: "", want: "******"},
		{name: "empty value", value: "", pattern: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskPattern(tt.value, tt.pattern); got != tt.want {
				t.Errorf("MaskPattern(%q, %q) = %q, want %q", tt.value, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestMaskEmail(t *testing.T) {
	if got := MaskEmail("user@example.com"); got != "u***@example.com" {
		t.Errorf("email mask = %q", got)
	}
	if got := MaskEmail("a@b.com"); got != "a@b.com" {
		t.Errorf("short email should not mask = %q", got)
	}
}

var maskPatternSink string

func BenchmarkMaskPattern(b *testing.B) {
	benchmarks := []struct {
		name    string
		value   string
		pattern string
	}{
		{name: "ascii", value: "13812345678", pattern: "###****####"},
		{name: "unicode", value: "张三丰", pattern: "#**"},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				maskPatternSink = MaskPattern(benchmark.value, benchmark.pattern)
			}
		})
	}
}

func TestPadLeft(t *testing.T) {
	if got := PadLeft("42", 5, "0"); got != "00042" {
		t.Errorf("padleft = %q", got)
	}
	if got := PadLeft("hello", 3, "x"); got != "hello" {
		t.Errorf("already long = %q", got)
	}
	if got := PadLeft("a", 5, ""); got != "a" {
		t.Errorf("empty pad = %q", got)
	}
}

func TestPadRight(t *testing.T) {
	if got := PadRight("42", 5, "0"); got != "42000" {
		t.Errorf("padright = %q", got)
	}
}

func TestMatches(t *testing.T) {
	if !Matches("^[a-z]+$", "hello") {
		t.Error("should match")
	}
	if Matches("^[a-z]+$", "Hello") {
		t.Error("should not match")
	}
	if Matches("[invalid", "x") {
		t.Error("invalid regex should not match")
	}
	// Second call should hit cache
	if !Matches("^[a-z]+$", "world") {
		t.Error("cached regex should match")
	}
}
