package env

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBasic(t *testing.T) {
	path := writeEnvFile(t, "FOO_TEST=bar\nBAZ_TEST=qux\n")

	os.Unsetenv("FOO_TEST")
	os.Unsetenv("BAZ_TEST")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}

	if v := os.Getenv("FOO_TEST"); v != "bar" {
		t.Errorf("FOO_TEST = %q, want bar", v)
	}
	if v := os.Getenv("BAZ_TEST"); v != "qux" {
		t.Errorf("BAZ_TEST = %q, want qux", v)
	}

	os.Unsetenv("FOO_TEST")
	os.Unsetenv("BAZ_TEST")
}

func TestLoadComments(t *testing.T) {
	path := writeEnvFile(t, "# comment\nKEY_COMMENT=value\n# another comment\n")

	os.Unsetenv("KEY_COMMENT")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY_COMMENT"); v != "value" {
		t.Errorf("KEY_COMMENT = %q, want value", v)
	}
	os.Unsetenv("KEY_COMMENT")
}

func TestLoadEmptyLines(t *testing.T) {
	path := writeEnvFile(t, "\n\nA_TEST=1\n\nB_TEST=2\n\n")

	os.Unsetenv("A_TEST")
	os.Unsetenv("B_TEST")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("A_TEST"); v != "1" {
		t.Errorf("A_TEST = %q, want 1", v)
	}
	if v := os.Getenv("B_TEST"); v != "2" {
		t.Errorf("B_TEST = %q, want 2", v)
	}

	os.Unsetenv("A_TEST")
	os.Unsetenv("B_TEST")
}

func TestLoadDoubleQuotes(t *testing.T) {
	path := writeEnvFile(t, `KEY_DQ="hello world"`)

	os.Unsetenv("KEY_DQ")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY_DQ"); v != "hello world" {
		t.Errorf("KEY_DQ = %q, want 'hello world'", v)
	}
	os.Unsetenv("KEY_DQ")
}

func TestLoadSingleQuotes(t *testing.T) {
	path := writeEnvFile(t, `KEY_SQ='hello world'`)

	os.Unsetenv("KEY_SQ")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY_SQ"); v != "hello world" {
		t.Errorf("KEY_SQ = %q, want 'hello world'", v)
	}
	os.Unsetenv("KEY_SQ")
}

func TestLoadNoOverride(t *testing.T) {
	os.Setenv("EXISTING_KEY", "original")
	defer os.Unsetenv("EXISTING_KEY")

	path := writeEnvFile(t, "EXISTING_KEY=overridden")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("EXISTING_KEY"); v != "original" {
		t.Errorf("should not override: got %q, want original", v)
	}
}

func TestLoadSpaces(t *testing.T) {
	path := writeEnvFile(t, "  KEY_SP  =  value_sp  \n")

	os.Unsetenv("KEY_SP")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY_SP"); v != "value_sp" {
		t.Errorf("KEY_SP = %q, want value_sp", v)
	}
	os.Unsetenv("KEY_SP")
}

func TestLoadEmptyValue(t *testing.T) {
	path := writeEnvFile(t, "EMPTY_KEY=\n")

	os.Unsetenv("EMPTY_KEY")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	v, exists := os.LookupEnv("EMPTY_KEY")
	if !exists {
		t.Error("EMPTY_KEY should exist")
	}
	if v != "" {
		t.Errorf("EMPTY_KEY = %q, want empty", v)
	}
	os.Unsetenv("EMPTY_KEY")
}

func TestLoadFileNotFound(t *testing.T) {
	err := Load("/nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadMissingEquals(t *testing.T) {
	path := writeEnvFile(t, "INVALID_LINE\n")
	err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing =")
	}
}

func TestLoadEmptyKey(t *testing.T) {
	path := writeEnvFile(t, "=value\n")
	err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestLoadValueWithEquals(t *testing.T) {
	path := writeEnvFile(t, "URL=postgres://user:pass@host:5432/db?sslmode=disable\n")

	os.Unsetenv("URL")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("URL"); v != "postgres://user:pass@host:5432/db?sslmode=disable" {
		t.Errorf("URL = %q", v)
	}
	os.Unsetenv("URL")
}

func TestMustLoadPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	MustLoad("/nonexistent/.env")
}

func TestMustLoadSuccess(t *testing.T) {
	path := writeEnvFile(t, "MUST_KEY=ok\n")
	os.Unsetenv("MUST_KEY")

	MustLoad(path)

	if v := os.Getenv("MUST_KEY"); v != "ok" {
		t.Errorf("MUST_KEY = %q", v)
	}
	os.Unsetenv("MUST_KEY")
}

func TestGet(t *testing.T) {
	os.Setenv("GET_TEST", "hello")
	defer os.Unsetenv("GET_TEST")

	val, ok := Get("GET_TEST")
	if !ok {
		t.Fatal("should exist")
	}
	if val != "hello" {
		t.Errorf("val = %q", val)
	}

	_, ok = Get("NONEXISTENT_KEY_12345")
	if ok {
		t.Error("should not exist")
	}
}

func TestMustGetSuccess(t *testing.T) {
	os.Setenv("MUSTGET_TEST", "value")
	defer os.Unsetenv("MUSTGET_TEST")

	val := MustGet("MUSTGET_TEST")
	if val != "value" {
		t.Errorf("val = %q", val)
	}
}

func TestMustGetPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	os.Unsetenv("MUSTGET_MISSING")
	MustGet("MUSTGET_MISSING")
}

func TestGetAfterLoad(t *testing.T) {
	path := writeEnvFile(t, "LOADED_VAR=from_file\n")
	os.Unsetenv("LOADED_VAR")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}

	val, ok := Get("LOADED_VAR")
	if !ok {
		t.Fatal("should exist after Load")
	}
	if val != "from_file" {
		t.Errorf("val = %q", val)
	}
	os.Unsetenv("LOADED_VAR")
}

func TestGetSystemEnvOverridesFile(t *testing.T) {
	os.Setenv("PRIORITY_TEST", "system")
	defer os.Unsetenv("PRIORITY_TEST")

	path := writeEnvFile(t, "PRIORITY_TEST=file\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}

	val, _ := Get("PRIORITY_TEST")
	if val != "system" {
		t.Errorf("system env should win: got %q", val)
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		line    string
		key     string
		value   string
		wantErr bool
	}{
		{"KEY=value", "KEY", "value", false},
		{"KEY=", "KEY", "", false},
		{`KEY="quoted"`, "KEY", "quoted", false},
		{`KEY='single'`, "KEY", "single", false},
		{"  KEY  =  val  ", "KEY", "val", false},
		{"KEY=a=b=c", "KEY", "a=b=c", false},
		{"NOEQ", "", "", true},
		{"=val", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			key, value, err := parseLine(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key != tt.key {
				t.Errorf("key = %q, want %q", key, tt.key)
			}
			if value != tt.value {
				t.Errorf("value = %q, want %q", value, tt.value)
			}
		})
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{"hello", "hello"},
		{`""`, ""},
		{`''`, ""},
		{`"`, `"`},
		{"a", "a"},
		{`"mismatched'`, `"mismatched'`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := unquote(tt.input)
			if got != tt.expect {
				t.Errorf("unquote(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}
