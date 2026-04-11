package i18n

import (
	"os"
	"path/filepath"
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

	got := tr.Translate("en", "error.out_of_stock", map[string]any{"available": 3})
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
		data map[string]any
		want string
	}{
		{"hello", nil, "hello"},
		{"hello ${name}", map[string]any{"name": "world"}, "hello world"},
		{"${a} and ${b}", map[string]any{"a": 1, "b": 2}, "1 and 2"},
		{"${missing}", map[string]any{}, "${missing}"},
		{"${missing}", map[string]any{"other": 1}, "${missing}"},
		{"no vars", map[string]any{"a": 1}, "no vars"},
		{"${x", map[string]any{"x": 1}, "${x"},                 // unclosed
		{"$", map[string]any{}, "$"},                           // lone dollar
		{"a$b", map[string]any{}, "a$b"},                       // dollar not followed by {
		{"${a}${b}", map[string]any{"a": "x", "b": "y"}, "xy"}, // consecutive
	}
	for _, tt := range tests {
		got := substitute(tt.tmpl, tt.data)
		if got != tt.want {
			t.Errorf("substitute(%q, %v) = %q, want %q", tt.tmpl, tt.data, got, tt.want)
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
	for range n {
		go func() {
			defer wg.Done()
			got := tr.Translate("en", "error.test", map[string]any{"name": "world"})
			if got != "hello world" {
				t.Errorf("got %q", got)
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
	got := substitute("end$", map[string]any{"x": 1})
	if got != "end$" {
		t.Errorf("got %q", got)
	}
	// ${ without closing }
	got = substitute("${unclosed", map[string]any{"x": 1})
	if got != "${unclosed" {
		t.Errorf("got %q", got)
	}
	// Template has ${} to pass early check, but also has trailing $
	got = substitute("${x}$", map[string]any{"x": "a"})
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
