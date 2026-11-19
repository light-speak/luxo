package jsonutil

import (
	"errors"
	"strings"
	"testing"
)

func checkStreamList(raw []byte, limit int, check func(*DepthReader) error) error {
	return CheckJSONValueDepth(raw, func(reader *DepthReader) error { return reader.List(limit, check) })
}

func TestDepthReaderCompleteValues(t *testing.T) {
	if err := CheckJSONValueDepth(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckJSONValueDepth([]byte("0"), nil); !errors.Is(err, errNilDepthCheck) {
		t.Fatal(err)
	}
	for _, raw := range []string{"x", "[", "[0", "[0,]", "[0]0", "[0]x", "[0] []", `["\uD800"]`} {
		if err := checkStreamList([]byte(raw), 2, (*DepthReader).JSON); err == nil {
			t.Errorf("accepted malformed array %q", raw)
		}
	}
	for _, raw := range []string{"x", "0"} {
		if err := CheckJSONValueDepth([]byte(raw), func(*DepthReader) error { return nil }); err == nil {
			t.Fatal("unconsumed value accepted", raw)
		}
	}
	expected := errors.New("stop resource scan")
	if err := CheckJSONValueDepth([]byte("0"), func(*DepthReader) error { return expected }); err != expected {
		t.Fatal("callback failure replaced", err)
	}
}

func TestDepthReaderIndependentListValues(t *testing.T) {
	value := strings.Repeat("[", 128) + "0" + strings.Repeat("]", 128)
	if err := checkStreamList([]byte("["+value+","+value+"]"), 2, (*DepthReader).JSON); err != nil {
		t.Fatal(err)
	}
	if err := checkStreamList([]byte("[["+value+"]]"), 1, (*DepthReader).JSON); !errors.Is(err, errJSONDepth) {
		t.Fatal(err)
	}
	for _, raw := range []string{"null", "{}", `"ignored"`, "{\"x\":" + value + "}", "{", `{"x":}`} {
		err := checkStreamList([]byte(raw), 0, nil)
		valid := raw == "null" || raw == "{}" || raw == `"ignored"`
		if (err == nil) != valid {
			t.Fatal(raw, err)
		}
	}
	if err := checkStreamList([]byte("[]"), 0, nil); !errors.Is(err, errNilDepthCheck) {
		t.Fatal(err)
	}
	count := 0
	if err := checkStreamList([]byte("[0,1]"), 1, func(reader *DepthReader) error { count++; return reader.JSON() }); !errors.Is(err, errArrayLimit) || count != 1 {
		t.Fatal(count, err)
	}
	expected := errors.New("list stop")
	if err := checkStreamList([]byte("[0]"), 1, func(*DepthReader) error { return expected }); err != expected {
		t.Fatal(err)
	}
	if err := checkStreamList([]byte("[]"), 0, (*DepthReader).JSON); err != nil {
		t.Fatal(err)
	}
}

func TestDepthReaderObjectNamesAndErrors(t *testing.T) {
	for _, raw := range []string{"x", "{", `{"a":}`, `{"a":0,}`, `{"a":0}0`} {
		if err := CheckJSONValueDepth([]byte(raw), func(reader *DepthReader) error {
			return reader.Object(func([]byte) error { return reader.JSON() })
		}); err == nil {
			t.Fatal("invalid object accepted", raw)
		}
	}
	if err := CheckJSONValueDepth([]byte("{}"), func(reader *DepthReader) error { return reader.Object(nil) }); !errors.Is(err, errNilDepthCheck) {
		t.Fatal(err)
	}
	for _, depth := range []int{128, 129} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		err := CheckJSONValueDepth([]byte(raw), func(reader *DepthReader) error { return reader.Object(nil) })
		if (err == nil) != (depth == 128) {
			t.Fatal(depth, err)
		}
	}
	var names []string
	err := CheckJSONValueDepth([]byte(`{"a":{},"\u0061":0,"a":[],"b":{"c":null}}`), func(reader *DepthReader) error {
		if reader.Kind() != '{' {
			t.Fatal("wrong root kind")
		}
		return reader.Object(func(name []byte) error { names = append(names, string(name)); return reader.JSON() })
	})
	if err != nil || strings.Join(names, ",") != "a,a,a,b" {
		t.Fatal(names, err)
	}
	expected := errors.New("object stop")
	if err := CheckJSONValueDepth([]byte(`{"a":0}`), func(reader *DepthReader) error { return reader.Object(func([]byte) error { return expected }) }); err != expected {
		t.Fatal(err)
	}
}

func TestDepthReaderReleasesStateOnEveryExit(t *testing.T) {
	for _, raw := range []string{`{"x":0}`, `{"x":}`, `{"x":0}x`, `{"x":` + strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129) + `}`} {
		var borrowed *Reader
		err := CheckJSONValueDepth([]byte(raw), func(reader *DepthReader) error {
			borrowed = reader.reader
			return reader.Object(func([]byte) error { return reader.JSON() })
		})
		if (err == nil) != (raw == `{"x":0}`) {
			t.Fatal(raw, err)
		}
		if borrowed == nil || borrowed.input.Len() != 0 || borrowed.input.Cap() != 0 || borrowed.InputOffset() != 0 {
			t.Fatal("reader retained parser/input state after visitor returned")
		}
	}
}
