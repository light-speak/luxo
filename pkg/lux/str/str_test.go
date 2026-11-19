package str

import (
	"hash/maphash"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
	"unsafe"
)

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

func TestSubstringBounds(t *testing.T) {
	for _, sample := range []struct {
		name, value string
		start, end  int
		want        string
		err         error
	}{
		{"empty", "", 0, 0, "", nil},
		{"ascii", "abcd", 1, 3, "bc", nil},
		{"end", "abcd", 4, 4, "", nil},
		{"full", "中文", 0, 6, "中文", nil},
		{"unicode-boundaries", "中😀a", 3, 7, "😀", nil},
		{"negative", "abcd", -1, 2, "", errSubstringRange},
		{"reversed", "abcd", 2, 1, "", errSubstringRange},
		{"long-end", "abcd", 0, 5, "", errSubstringRange},
		{"long-start", "abcd", 5, 5, "", errSubstringRange},
		{"negative-end", "abcd", 0, -1, "", errSubstringRange},
		{"split-start", "中😀a", 1, 7, "", errSubstringBoundary},
		{"split-end", "中😀a", 0, 4, "", errSubstringBoundary},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got, err := Substring(sample.value, sample.start, sample.end)
			if got != sample.want || err != sample.err {
				t.Fatalf("Substring(%q,%d,%d)=%q,%v", sample.value, sample.start, sample.end, got, err)
			}
		})
	}
	if got, err := SubstringFrom("中文", 3); got != "文" || err != nil {
		t.Fatal(got, err)
	}
	if got, err := SubstringFrom("中文", 1); got != "" || err != errSubstringBoundary {
		t.Fatal(got, err)
	}
}

func TestRepeat(t *testing.T) {
	for _, sample := range []struct {
		name, value, want string
		count             int
		invalid           bool
	}{
		{"zero", "ab", "", 0, false}, {"one", "ab", "ab", 1, false},
		{"many", "ab", "ababab", 3, false}, {"unicode", "中", "中中", 2, false},
		{"empty-huge", "", "", math.MaxInt, false},
		{"negative", "ab", "", -1, true}, {"empty-negative", "", "", -1, true},
		{"minimum", "ab", "", math.MinInt, true},
		{"multiply-overflow", "ab", "", math.MaxInt, true},
		{"word-overflow", "abc", "", math.MaxInt, true},
		{"signed-overflow", "abc", "", math.MaxInt/3 + 1, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			value, err := Repeat(sample.value, sample.count)
			if (err != nil) != sample.invalid || value != sample.want {
				t.Fatalf("got %q, %v; want %q, invalid=%t", value, err, sample.want, sample.invalid)
			}
		})
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
	for _, sample := range []struct{ value, want string }{
		{"user@example.com", "u***@example.com"}, {"a@b.com", "a@b.com"},
		{"张三@example.com", "张*@example.com"}, {"张@example.com", "张@example.com"},
		{"😀用户@example.com", "😀**@example.com"}, {"a中@example.com", "a*@example.com"},
		{"éa@example.com", "é*@example.com"}, {"e\u0301x@example.com", "e**@example.com"},
		{"张三@例子.公司", "张*@例子.公司"}, {"ab@", "a*@"},
		{"abcdefg@example.com", "a******@example.com"},
		{"abcdefghi@example.com", "a********@example.com"},
		{"abcdefghijklmnop中@example.com", "a****************@example.com"},
		{"", ""}, {"@example.com", "@example.com"}, {"no-at-sign", "no-at-sign"},
	} {
		t.Run(sample.value, func(t *testing.T) {
			got := MaskEmail(sample.value)
			if got != sample.want || !utf8.ValidString(got) {
				t.Fatalf("MaskEmail(%q) = %q; want valid UTF-8 %q", sample.value, got, sample.want)
			}
		})
	}
}

func BenchmarkMaskEmail(b *testing.B) {
	for _, sample := range []struct{ name, value string }{
		{"ascii", "user@example.com"}, {"unicode", "张三丰@example.com"},
		{"single-ascii", "a@example.com"}, {"single-unicode", "张@example.com"},
		{"long-ascii", strings.Repeat("a", 1024) + "@example.com"},
		{"long-unicode", strings.Repeat("中", 1024) + "@example.com"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				maskPatternSink = MaskEmail(sample.value)
			}
		})
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
	for _, test := range []struct {
		text string
		size int
		pad  string
	}{
		{"hello", 3, "x"},
		{"hello", 5, "x"},
		{"hello", -1, "x"},
		{"a", 5, ""},
	} {
		if got := PadRight(test.text, test.size, test.pad); got != test.text {
			t.Errorf("PadRight(%q, %d, %q) = %q", test.text, test.size, test.pad, got)
		}
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

func TestRegexCacheRetainsLargePatternWithinWorkBudget(t *testing.T) {
	cache := newRegexMatcherCache()
	value := strings.Repeat("x", 5000)
	pattern := "^" + value + "$"
	if !cache.matches(pattern, value) || cachedRegexEntry(cache, pattern) == nil {
		t.Fatal("a single large pattern within the set budget was unnecessarily recompiled")
	}
}

func TestRegexCacheUsesSpareBudgetForShortPatterns(t *testing.T) {
	cache := newRegexMatcherCache()
	set := &cache.sets[0]
	for index := range 32 {
		value := "short-" + strconv.Itoa(index)
		if !set.matchMiss("^"+value+"$", value) {
			t.Fatal(index)
		}
	}
	count := 0
	for index := range regexCacheWays {
		if set.entries[index].Load() != nil {
			count++
		}
	}
	if count != 32 {
		t.Fatalf("retained %d short patterns, want 32 within the same work budget", count)
	}
}

func TestRegexCacheRetentionIsBounded(t *testing.T) {
	cache := newRegexMatcherCache()
	for index := range 2 * regexCacheSets * regexCacheWays {
		pattern := "^bounded-regex-" + strconv.Itoa(index) + "$"
		if !cache.matches(pattern, "bounded-regex-"+strconv.Itoa(index)) {
			t.Fatal("valid pattern stopped matching", index)
		}
	}
	count := 0
	for set := range cache.sets {
		for way := range regexCacheWays {
			if cache.sets[set].entries[way].Load() != nil {
				count++
			}
		}
	}
	if count > regexCacheSets*regexCacheWays {
		t.Fatalf("regex cache retained %d patterns; limit is %d", count, regexCacheSets*regexCacheWays)
	}
}

func TestRegexCacheEvictsByTotalWork(t *testing.T) {
	cache := newRegexMatcherCache()
	set := &cache.sets[0]
	for index := range 12 {
		value := strings.Repeat("x", 6000) + strconv.Itoa(index)
		if !set.matchMiss("^"+value+"$", value) {
			t.Fatal(index)
		}
		total, count := 0, 0
		for way := range regexCacheWays {
			if entry := set.entries[way].Load(); entry != nil {
				total += entry.work
				count++
			}
		}
		if total > regexCacheSetWork || set.work != total || count > 5 {
			t.Fatalf("cache budget exceeded: work=%d recorded=%d entries=%d", total, set.work, count)
		}
	}
}

func TestRegexCacheOwnsRetainedPattern(t *testing.T) {
	const expression = "^owned-regex-[0-9]+$"
	body := strings.Repeat("body", 1<<18) + expression
	pattern := body[len(body)-len(expression):]
	if !Matches(pattern, "owned-regex-42") {
		t.Fatal("valid pattern did not match")
	}
	cached := cachedRegexEntry(regexCache, pattern)
	if cached == nil {
		t.Fatal("pattern was not cached")
	}
	if unsafe.StringData(cached.pattern) == unsafe.StringData(pattern) || unsafe.StringData(cached.value.String()) == unsafe.StringData(pattern) {
		t.Fatal("cached pattern retains the original request body")
	}
}

func cachedRegexEntry(cache *regexMatcherCache, pattern string) *regexCacheEntry {
	set := &cache.sets[maphash.String(cache.seed, pattern)&(regexCacheSets-1)]
	for index := range regexCacheWays {
		if entry := set.entries[index].Load(); entry != nil && entry.pattern == pattern {
			return entry
		}
	}
	return nil
}

func TestRegexAdmissionPreservesMatching(t *testing.T) {
	if regexCachePatternWork(strings.Repeat("x", regexCachePatternSize+1)) != 0 {
		t.Fatal("oversized pattern was admitted to the cache")
	}
	for _, sample := range []struct {
		pattern, value string
		cached         bool
	}{
		{"", "anything", true},
		{`^\d{1,12}$`, "42", true},
		{`^\p{L}+$`, "中文AbC", true},
		{`^\pL+$`, "中文", true},
		{`^\P{L}+$`, "42", true},
		{`a{0}`, "", true},
		{`a{2,}`, "aaa", true},
		{`a{1000}`, strings.Repeat("a", 1000), true},
		{`a{1000}b{1000}`, strings.Repeat("a", 1000) + strings.Repeat("b", 1000), false},
		{strings.Repeat(`\pL`, 17), "中文abc中文abc中文abc中", false},
		{"^" + strings.Repeat("x", 5000) + "$", strings.Repeat("x", 5000), true},
		{"^" + strings.Repeat("x", regexCachePatternSize+1) + "$", strings.Repeat("x", regexCachePatternSize+1), false},
		{`\{literal\}`, "{literal}", true},
		{`{literal}`, "{literal}", false},
		{"[invalid", "x", false},
		{"a{", "x", false},
		{`\p{`, "x", false},
		{"a{99999999999999999999}", "x", false},
	} {
		cache := newRegexMatcherCache()
		want, _ := regexp.MatchString(sample.pattern, sample.value)
		if got := cache.matches(sample.pattern, sample.value); got != want {
			t.Fatalf("pattern %q: got %v, want %v", sample.pattern, got, want)
		}
		if got := cachedRegexEntry(cache, sample.pattern) != nil; got != sample.cached {
			t.Fatalf("pattern %q: cached=%v, want %v", sample.pattern, got, sample.cached)
		}
	}
}

func TestRegexCacheCollisionReplacementKeepsResults(t *testing.T) {
	cache := newRegexMatcherCache()
	buckets := make(map[uint64][]string)
	var colliding []string
	for index := 0; index <= regexCacheSets*regexCacheWays; index++ {
		pattern := "^collision-" + strconv.Itoa(index) + "$"
		bucket := maphash.String(cache.seed, pattern) & (regexCacheSets - 1)
		buckets[bucket] = append(buckets[bucket], pattern)
		if len(buckets[bucket]) > regexCacheWays {
			colliding = buckets[bucket]
			break
		}
	}
	if len(colliding) != regexCacheWays+1 {
		t.Fatal("failed to construct the guaranteed cache collision")
	}
	for range 3 {
		for _, pattern := range colliding {
			value := pattern[1 : len(pattern)-1]
			if !cache.matches(pattern, value) || cache.matches(pattern, "different") {
				t.Fatal("cache replacement changed the expression", pattern)
			}
		}
	}
}

func TestRegexCacheConcurrentPublicationAndReplacement(t *testing.T) {
	cache := newRegexMatcherCache()
	var group sync.WaitGroup
	failures := make(chan string, 16)
	for worker := range 16 {
		group.Go(func() {
			for index := range 128 {
				value := "worker-" + strconv.Itoa(worker) + "-" + strconv.Itoa(index)
				pattern := "^" + value + "$"
				if !cache.matches(pattern, value) || cache.matches(pattern, "different") {
					failures <- pattern
					return
				}
			}
		})
	}
	group.Wait()
	close(failures)
	for pattern := range failures {
		t.Error("concurrent cache returned a different expression", pattern)
	}
}

func TestRegexCacheConcurrentMissesDeduplicate(t *testing.T) {
	cache := newRegexMatcherCache()
	set := &cache.sets[0]
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			<-start
			if !set.matchMiss(`^shared-miss$`, "shared-miss") {
				t.Error("concurrent miss stopped matching")
			}
		})
	}
	close(start)
	group.Wait()
	count := 0
	for index := range regexCacheWays {
		if set.entries[index].Load() != nil {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("same pattern occupies %d cache ways, want 1", count)
	}
}

func TestPadExtremeNegativeWidths(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(string, int, string) string
	}{{"left", PadLeft}, {"right", PadRight}} {
		t.Run(operation.name, func(t *testing.T) {
			for _, width := range []int{math.MinInt, math.MinInt + 1, -1, 0} {
				t.Run(strconv.Itoa(width), func(t *testing.T) {
					defer func() {
						if failure := recover(); failure != nil {
							t.Errorf("negative padding width caused panic: %v", failure)
						}
					}()
					if got := operation.call("abc", width, "0"); got != "abc" {
						t.Errorf("negative padding changed text: %q", got)
					}
				})
			}
		})
	}
}

var paddingBenchmarkSink string

func BenchmarkPaddingBounds(b *testing.B) {
	for _, operation := range []struct {
		name string
		call func(string, int, string) string
	}{{"left", PadLeft}, {"right", PadRight}} {
		b.Run(operation.name, func(b *testing.B) {
			for _, sample := range []struct {
				name, text, pad string
				width           int
			}{{"short", "42", "0", 8}, {"long", "42", "01", 4096},
				{"unchanged", "already-wide", "0", 3}, {"empty-pad", "a", "", 8}} {
				b.Run(sample.name, func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						paddingBenchmarkSink = operation.call(sample.text, sample.width, sample.pad)
					}
				})
			}
		})
	}
}

func TestPaddingPatternBoundaries(t *testing.T) {
	for _, text := range []string{"", "a", "abc", "中文"} {
		for _, padding := range []string{"0", "ab", "0123456", "中", "中文"} {
			for width := 0; width <= 257; width++ {
				missing := max(0, width-len(text))
				prefix := strings.Repeat(padding, (missing+len(padding)-1)/len(padding))[:missing]
				// Complete UTF-8 patterns are controls; malformed boundaries have separate probes.
				if !utf8.ValidString(prefix) {
					continue
				}
				if got, want := PadLeft(text, width, padding), prefix+text; got != want {
					t.Fatalf("left text=%q width=%d padding=%q: %q != %q", text, width, padding, got, want)
				}
				if got, want := PadRight(text, width, padding), text+prefix; got != want {
					t.Fatalf("right text=%q width=%d padding=%q: %q != %q", text, width, padding, got, want)
				}
			}
		}
	}
}

var substringValueBenchmarkSink string
var substringErrorBenchmarkSink error

func BenchmarkSubstring(b *testing.B) {
	for _, sample := range []struct {
		name, value string
		start, end  int
		invalid     bool
	}{
		{"ASCII", "abcdefgh", 1, 7, false},
		{"Empty", "", 0, 0, false},
		{"Unicode", "中😀ab", 3, 7, false},
		{"Long", strings.Repeat("中", 1024), 300, 2700, false},
		{"InvalidRange", "abcd", -1, 2, true},
		{"InvalidBoundary", "中文", 1, 3, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			if _, err := Substring(sample.value, sample.start, sample.end); (err != nil) != sample.invalid {
				b.Fatal("unexpected substring validity", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				substringValueBenchmarkSink, substringErrorBenchmarkSink = Substring(sample.value, sample.start, sample.end)
			}
		})
	}
}

func TestPaddingRetainsCompleteUTF8Prefix(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(string, int, string) string
		left bool
	}{{"left", PadLeft, true}, {"right", PadRight, false}} {
		for _, sample := range []struct {
			text, padding, prefix string
			width                 int
		}{
			{"a", "中", "", 2},
			{"a", "中", "", 3},
			{"a", "中", "中", 4},
			{"a", "中", "中", 5},
			{"文", "a中", "a", 5},
			{"文", "a中", "a中a", 9},
			{"", "😀", "😀", 7},
			{"", "😀a", "😀a", 8},
			{"中文", "a😀中", "a😀中a", 17},
		} {
			t.Run(operation.name+"/"+sample.padding+"/"+strconv.Itoa(sample.width), func(t *testing.T) {
				want := sample.text + sample.prefix
				if operation.left {
					want = sample.prefix + sample.text
				}
				got := operation.call(sample.text, sample.width, sample.padding)
				if got != want || !utf8.ValidString(got) || len(got) > sample.width {
					t.Fatalf("padding(%q, %d, %q) = %q, want %q", sample.text, sample.width, sample.padding, got, want)
				}
			})
		}
	}
}

func BenchmarkPaddingUTF8(b *testing.B) {
	for _, sample := range []struct {
		name, text, padding string
		width               int
	}{
		{"short-complete", "a", "中", 7},
		{"short-partial", "a", "中", 6},
		{"long-complete", "a", "中", 4096},
		{"long-partial", "a", "😀a", 4096},
		{"unfit", "a", "中", 2},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				paddingBenchmarkSink = PadRight(sample.text, sample.width, sample.padding)
			}
		})
	}
}
