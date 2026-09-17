package i18n

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestTranslateBasic(t *testing.T) {
	tr := New("en")
	tr.loadYAML("en", []byte(`
error:
  not_found: "Resource not found"
  out_of_stock: "Out of stock, ${available} remaining"
`))

	got := tr.Translate("en", "error.not_found", nil)
	if got != "Resource not found" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateWithData(t *testing.T) {
	tr := New("en")
	tr.loadYAML("en", []byte(`
error:
  out_of_stock: "Out of stock, ${available} remaining"
`))

	got := tr.Translate("en", "error.out_of_stock", Values{"available": "3"})
	if got != "Out of stock, 3 remaining" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateFallback(t *testing.T) {
	tr := New("en")
	tr.loadYAML("en", []byte(`
error:
  not_found: "Not found"
`))
	tr.loadYAML("zh", []byte(`
error:
  other: "其他"
`))

	// zh doesn't have not_found → falls back to en
	got := tr.Translate("zh", "error.not_found", nil)
	if got != "Not found" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateRawKeyFallback(t *testing.T) {
	tr := New("en")
	// No translations loaded for this key
	got := tr.Translate("en", "error.unknown_key", nil)
	if got != "error.unknown_key" {
		t.Errorf("got %q, want raw key", got)
	}
}

func TestTranslateSameLocaleFallback(t *testing.T) {
	tr := New("en")
	// locale == fallback, key missing → raw key
	got := tr.Translate("en", "error.missing", nil)
	if got != "error.missing" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateUnknownLocale(t *testing.T) {
	tr := New("en")
	tr.loadYAML("en", []byte(`
error:
  not_found: "Not found"
`))

	// Unknown locale → fallback to en
	got := tr.Translate("fr", "error.not_found", nil)
	if got != "Not found" {
		t.Errorf("got %q", got)
	}
}

func TestSubstitute(t *testing.T) {
	tests := []struct {
		tmpl string
		data Values
		want string
	}{
		{"hello", nil, "hello"},
		{"hello ${name}", Values{"name": "world"}, "hello world"},
		{"${a} and ${b}", Values{"a": "1", "b": "2"}, "1 and 2"},
		{"${missing}", Values{}, "${missing}"},
		{"${missing}", Values{"other": "1"}, "${missing}"},
		{"no vars", Values{"a": "1"}, "no vars"},
		{"${x", Values{"x": "1"}, "${x"},               // unclosed
		{"$", Values{}, "$"},                           // lone dollar
		{"a$b", Values{}, "a$b"},                       // dollar not followed by {
		{"${a}${b}", Values{"a": "x", "b": "y"}, "xy"}, // consecutive
	}
	for _, tt := range tests {
		got := substitute(tt.tmpl, tt.data)
		if got != tt.want {
			t.Errorf("substitute(%q, %v) = %q, want %q", tt.tmpl, tt.data, got, tt.want)
		}
	}
}

func TestSubstituteOwnedOutput(t *testing.T) {
	var data Data = Values{"name": "first", "empty": ""}
	output := substitute("${name}/${empty}/${unknown}/${name}", data)
	for _, size := range []int{1, 64, 4096, 8192} {
		value := strings.Repeat("数", size)
		if got := substitute("pre:${name}:post", Values{"name": value}); got != "pre:"+value+":post" {
			t.Fatalf("output changed for size %d", size)
		}
		if output != "first//${unknown}/first" {
			t.Fatalf("previous output was overwritten: %q", output)
		}
	}
}

type missingTextData struct{ text string }

func (d missingTextData) AppendText(dst []byte, _ string) ([]byte, bool) {
	return append(dst, d.text...), false
}

func TestSubstituteMissingRollback(t *testing.T) {
	const template = "before:${unknown}:after"
	data := missingTextData{text: strings.Repeat("discard me", 64)}
	if got := substitute(template, data); got != template {
		t.Fatalf("output = %q, want %q", got, template)
	}
	buf, changed := appendSubstitution(nil, template, data)
	if changed || string(buf) != template {
		t.Fatalf("missing field changed output: %q, %v", buf, changed)
	}
	for _, value := range buf[len(buf):cap(buf)] {
		if value != 0 {
			t.Fatal("discarded text remains after rollback")
		}
	}
}

type recordingTextData struct {
	fields []string
	values Values
}

func (d *recordingTextData) AppendText(dst []byte, field string) ([]byte, bool) {
	d.fields = append(d.fields, field)
	return d.values.AppendText(dst, field)
}

func TestSubstituteVisitsFieldsOnce(t *testing.T) {
	data := &recordingTextData{values: Values{"name": "数", "": "empty"}}
	const template = "$${name}|${missing}|${name}|${}|$|${oops"
	if got := substitute(template, data); got != "$数|${missing}|数|empty|$|${oops" {
		t.Fatalf("output = %q", got)
	}
	if want := []string{"name", "missing", "name", ""}; !slices.Equal(data.fields, want) {
		t.Fatalf("fields = %q, want %q", data.fields, want)
	}
}

func TestInterpolationBufferReset(t *testing.T) {
	for _, size := range []int{0, 16, maxInterpolationCapacity, maxInterpolationCapacity + 1} {
		storage := make([]byte, size)
		for i := range storage {
			storage[i] = 'x'
		}
		buf := interpolationBuffer{bytes: storage}
		buf.reset()
		if len(buf.bytes) != 0 {
			t.Fatalf("size %d: buffer was not reset", size)
		}
		if size > maxInterpolationCapacity {
			if buf.bytes != nil {
				t.Fatal("oversized backing array was retained")
			}
			continue
		}
		if cap(buf.bytes) != size {
			t.Fatalf("size %d: reusable capacity was lost", size)
		}
		for _, value := range storage {
			if value != 0 {
				t.Fatal("backing array still contains error data")
			}
		}
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()

	// Write translation files
	os.WriteFile(filepath.Join(dir, "en.yaml"), []byte(`
error:
  not_found: "Not found"
  conflict: "Conflict"
`), 0644)
	os.WriteFile(filepath.Join(dir, "zh.yaml"), []byte(`
error:
  not_found: "资源不存在"
  conflict: "资源冲突"
`), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not yaml"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	tr := New("en")
	if err := tr.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	if got := tr.Translate("en", "error.not_found", nil); got != "Not found" {
		t.Errorf("en: got %q", got)
	}
	if got := tr.Translate("zh", "error.not_found", nil); got != "资源不存在" {
		t.Errorf("zh: got %q", got)
	}
}

func TestLoadDirYml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ja.yml"), []byte(`
error:
  not_found: "見つかりません"
`), 0644)

	tr := New("en")
	if err := tr.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	if got := tr.Translate("ja", "error.not_found", nil); got != "見つかりません" {
		t.Errorf("got %q", got)
	}
}

func TestLoadDirNotExist(t *testing.T) {
	tr := New("en")
	err := tr.LoadDir("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

func TestLoadDirBadYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("\t\tinvalid"), 0644)

	tr := New("en")
	err := tr.LoadDir(dir)
	if err == nil {
		t.Error("expected error for bad YAML")
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"zh-CN,zh;q=0.9,en;q=0.8", "zh"},
		{"en-US", "en"},
		{"fr", "fr"},
		{"de-DE,de;q=0.9", "de"},
		{"  ja  ", "ja"},
		{"*", "*"},
	}
	for _, tt := range tests {
		got := ParseAcceptLanguage(tt.header)
		if got != tt.want {
			t.Errorf("ParseAcceptLanguage(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestConcurrentTranslate(t *testing.T) {
	tr := New("en")
	tr.loadYAML("en", []byte(`
error:
  test: "hello ${name}"
`))

	var wg sync.WaitGroup
	n := 100
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			value := "world-" + strconv.Itoa(i)
			data := Values{"name": value}
			first := tr.Translate("en", "error.test", data)
			for range 100 {
				if got := tr.Translate("en", "error.test", data); got != "hello "+value {
					t.Errorf("got %q, want %q", got, "hello "+value)
				}
			}
			if first != "hello "+value {
				t.Errorf("previous result changed: %q", first)
			}
		}()
	}
	wg.Wait()
}

func TestFlattenNested(t *testing.T) {
	out := make(map[string]string)
	flatten("", map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
	}, out)
	if out["a.b.c"] != "deep" {
		t.Errorf("got %v", out)
	}
}

func TestLoadDirUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("a: b"), 0644)
	os.Chmod(path, 0000)
	defer os.Chmod(path, 0644)

	tr := New("en")
	err := tr.LoadDir(dir)
	if err == nil {
		t.Error("expected error for unreadable file")
	}
}

func TestSubstituteUnclosedAtEnd(t *testing.T) {
	// $ at end of string — must pass non-nil data to avoid early return
	got := substitute("end$", Values{"x": "1"})
	if got != "end$" {
		t.Errorf("got %q", got)
	}
	// ${ without closing }
	got = substitute("${unclosed", Values{"x": "1"})
	if got != "${unclosed" {
		t.Errorf("got %q", got)
	}
	// Template has ${} to pass early check, but also has trailing $
	got = substitute("${x}$", Values{"x": "a"})
	if got != "a$" {
		t.Errorf("got %q", got)
	}
}

func TestParseAcceptLanguageNoHyphen(t *testing.T) {
	// Language with semicolon but no hyphen
	got := ParseAcceptLanguage("en;q=0.9")
	if got != "en" {
		t.Errorf("got %q", got)
	}
}

func TestFlattenSkipsNonString(t *testing.T) {
	out := make(map[string]string)
	flatten("", map[string]any{
		"num": 42,
		"str": "hello",
	}, out)
	if _, ok := out["num"]; ok {
		t.Error("should skip non-string values")
	}
	if out["str"] != "hello" {
		t.Error("should include string values")
	}
}
