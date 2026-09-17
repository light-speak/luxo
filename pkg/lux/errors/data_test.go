package errors

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestDataWriterPreservesTypedValues(t *testing.T) {
	w := NewDataWriter([]byte("prefix:"))
	w.Token(`{"text":`)
	w.String("café\n\"\\")
	w.Token(`,"id":`)
	w.Int(math.MaxInt64)
	w.Token(`,"float":`)
	w.Float(math.Copysign(0, -1))
	w.Token(`,"flag":`)
	w.Bool(true)
	w.Token(`,"raw":`)
	w.JSON(json.RawMessage(`{"number":123456789012345678901234567890,"tiny":1e-400}`))
	w.Token(`,"bytes":`)
	w.Bytes([]byte{0, 255})
	w.Token(`,"time":`)
	w.Time(time.Date(2026, 9, 15, 1, 2, 3, 4, time.UTC))
	w.Token("}")
	got, err := w.Result()
	want := "prefix:" + `{"text":"café\n\"\\","id":9223372036854775807,"float":-0,"flag":true,"raw":{"number":123456789012345678901234567890,"tiny":1e-400},"bytes":"AP8=","time":"2026-09-15T01:02:03.000000004Z"}`
	if err != nil || string(got) != want {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestDataWriterRejectsInvalidValuesAndPreservesPrefix(t *testing.T) {
	for name, write := range map[string]func(*DataWriter){
		"NaN":                 func(w *DataWriter) { w.Float(math.NaN()) },
		"infinity":            func(w *DataWriter) { w.Float(math.Inf(1)) },
		"negative infinity":   func(w *DataWriter) { w.Float(math.Inf(-1)) },
		"invalid string":      func(w *DataWriter) { w.String(string([]byte{0xff})) },
		"invalid JSON":        func(w *DataWriter) { w.JSON([]byte("{")) },
		"invalid JSON UTF8":   func(w *DataWriter) { w.JSON([]byte{'"', 0xff, '"'}) },
		"invalid JSON escape": func(w *DataWriter) { w.JSON([]byte(`"\uD800"`)) },
		"invalid JSON key":    func(w *DataWriter) { w.JSON([]byte(`{"\uDC00":1}`)) },
		"invalid time":        func(w *DataWriter) { w.Time(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)) },
		"negative year":       func(w *DataWriter) { w.Time(time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)) },
		"UTC before minimum":  func(w *DataWriter) { w.Time(time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 60))) },
		"UTC after maximum":   func(w *DataWriter) { w.Time(time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("offset", -60))) },
		"explicit rejection":  func(w *DataWriter) { w.Reject("native data rejected") },
	} {
		t.Run(name, func(t *testing.T) {
			w := NewDataWriter([]byte("prefix:"))
			w.Token("{")
			write(&w)
			first := w.Err()
			if first == nil {
				t.Fatal("invalid value accepted")
			}
			w.Float(math.NaN())
			w.Reject("second rejection")
			w.String("after")
			w.JSON([]byte("0"))
			w.Time(time.Now())
			w.Token("}")
			got, err := w.Result()
			if err != first || string(got) != "prefix:" {
				t.Fatalf("partial output or error replaced: %q, %v", got, err)
			}
		})
	}
}

func TestDataWriterTimeNormalizesUTCWithoutLosingNanoseconds(t *testing.T) {
	for _, offset := range []int{60, -60, 86400, -86400} {
		value := time.Unix(253402300799, 123456789).In(time.FixedZone("offset", offset))
		w := NewDataWriter(nil)
		w.Time(value)
		got, err := w.Result()
		if err != nil || string(got) != `"9999-12-31T23:59:59.123456789Z"` {
			t.Fatalf("%v / %s / %v", value, got, err)
		}
	}
}

func TestDataWriterNilJSONAndBytes(t *testing.T) {
	w := NewDataWriter(nil)
	w.Token("[")
	w.JSON(nil)
	w.Token(",")
	w.Bytes(nil)
	w.Token(",")
	w.Bytes([]byte{})
	w.Token("]")
	got, err := w.Result()
	if err != nil || string(got) != `[null,null,""]` {
		t.Fatalf("%s: %v", got, err)
	}
}

func TestDataWriterDepthLimit(t *testing.T) {
	w := NewDataWriter(nil)
	for range 64 {
		if !w.Enter() {
			t.Fatal("valid depth rejected")
		}
	}
	w.Leave()
	if !w.Enter() || w.Enter() {
		t.Fatal("depth boundary not enforced")
	}
	if w.Enter() {
		t.Fatal("writer resumed after error")
	}
	if _, err := w.Result(); err == nil {
		t.Fatal("missing depth error")
	}
}
