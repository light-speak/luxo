package api

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
)

type wireErrorAdapter struct{ value *luxerrors.AppError }

func mustBinaryError(t testing.TB, dst []byte, value wireError) []byte {
	t.Helper()
	encoded, err := appendBinaryError(dst, value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (e wireErrorAdapter) Error() string { return "adapted error" }
func (e wireErrorAdapter) As(target any) bool {
	value, ok := target.(**luxerrors.AppError)
	if ok {
		*value = e.value
	}
	return ok
}

func TestWireErrorPreservesWrappedAndAdaptedErrors(t *testing.T) {
	expected := luxerrors.New("PrecisionRejected", 422, "error.precision_rejected")
	for _, err := range []error{
		expected, fmt.Errorf("outer: %w", expected),
		stderrors.Join(fmt.Errorf("unrelated"), expected, luxerrors.NotFound),
		wireErrorAdapter{value: expected},
	} {
		actual := NewRouter().buildWireError(t.Context(), "", err)
		if actual.Name != expected.Name || actual.Code != expected.Code || actual.Message != expected.Message {
			t.Fatalf("lost wrapped error identity: %+v", actual)
		}
	}
}

func TestWireErrorTranslationFallbackWithoutData(t *testing.T) {
	router := NewRouter()
	router.SetTranslator(i18n.New("en"))
	actual := router.buildWireError(t.Context(), "zh-CN", luxerrors.NotFound)
	if actual.Message != luxerrors.NotFound.Message || actual.Data != nil {
		t.Fatalf("unexpected untranslated error: %+v", actual)
	}
}

func TestAppendBinaryErrorPreservesDestination(t *testing.T) {
	value := wireError{Code: 422, Name: "Invalid", Message: "invalid"}
	prefix := []byte{BinaryFrameCallError, 42}
	dst := make([]byte, len(prefix), 256)
	copy(dst, prefix)
	got := mustBinaryError(t, dst, value)
	if !bytes.Equal(got[:len(prefix)], prefix) || !bytes.Equal(got[len(prefix):], mustBinaryError(t, nil, value)) {
		t.Fatalf("append corrupted frame prefix or payload: %x", got)
	}
	if &got[0] != &dst[0] {
		t.Fatal("append replaced sufficient destination capacity")
	}
	if allocations := testing.AllocsPerRun(100, func() { mustBinaryError(t, dst, value) }); allocations != 0 {
		t.Fatalf("error envelope requires %v intermediate allocations", allocations)
	}
}

func TestDecodeBinaryErrorRequiredFields(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			var enc codec.Encoder
			if mask&1 != 0 {
				enc.WriteFieldInt(1, 422)
			}
			if mask&2 != 0 {
				enc.WriteFieldString(2, "Invalid")
			}
			if mask&4 != 0 {
				enc.WriteFieldString(3, "invalid")
			}
			enc.WriteFieldString(4, "trace")
			enc.WriteEnd()
			got, err := DecodeBinaryError(enc.Bytes(), 500)
			if mask == 7 {
				if err != nil || got.Code != 422 {
					t.Fatalf("complete envelope: %+v, %v", got, err)
				}
			} else if err == nil || got != nil || !strings.Contains(err.Error(), "required") {
				t.Fatalf("missing fields accepted or hidden: %+v, %v", got, err)
			}
		})
	}
}

func TestDecodeBinaryErrorRejectsDuplicateFields(t *testing.T) {
	for field := 1; field <= 6; field++ {
		t.Run(fmt.Sprint(field), func(t *testing.T) {
			data := mustBinaryError(t, nil, wireError{Code: 422, Name: "Invalid", Message: "invalid",
				TraceID: "trace", Data: []byte("null"), Cause: "cause"})
			var enc codec.Encoder
			if field == 1 {
				enc.WriteFieldInt(field, 401)
			} else {
				enc.WriteFieldString(field, "null")
			}
			enc.WriteEnd()
			data = append(data[:len(data)-1], enc.Bytes()...)
			got, err := DecodeBinaryError(data, 500)
			if err == nil || got != nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate field %d accepted: %+v, %v", field, got, err)
			}
		})
	}
}

func TestDecodeBinaryErrorRejectsInvalidUTF8(t *testing.T) {
	for _, field := range []int{2, 3, 4, 5, 6} {
		for _, invalid := range []string{"\xff", "\xc0\x80", "\xe4\xb8", "\xed\xa0\x80"} {
			t.Run(fmt.Sprintf("%d/%x", field, invalid), func(t *testing.T) {
				values := []string{"", "", "Invalid", "invalid", "trace", "null", "cause"}
				values[field] = invalid
				if field == 5 {
					values[field] = `"` + invalid + `"`
				}
				var enc codec.Encoder
				enc.WriteFieldInt(1, 422)
				for id := 2; id <= 6; id++ {
					// Raw fixture bytes deliberately bypass String validation.
					enc.WriteFieldBytes(id, []byte(values[id]))
				}
				enc.WriteEnd()
				if got, err := DecodeBinaryError(enc.Bytes(), 500); err == nil || got != nil {
					t.Fatalf("invalid UTF-8 accepted in field %d: %+v, %v", field, got, err)
				}
			})
		}
	}
}

func TestDecodeBinaryErrorDataPresence(t *testing.T) {
	for _, literal := range []string{"", "{", "null", "[]", "{}", "18446744073709551616", "1.234567890123456789", "1e400",
		`{"n":1,"n":2}`, `"\ud800"`, `"\ud800\udc00"`, `"\ufffd"`, "-0"} {
		t.Run(literal, func(t *testing.T) {
			var enc codec.Encoder
			enc.WriteFieldInt(1, 422)
			enc.WriteFieldString(2, "Invalid")
			enc.WriteFieldString(3, "invalid")
			enc.WriteFieldString(5, literal)
			enc.WriteEnd()
			got, err := DecodeBinaryError(enc.Bytes(), 500)
			if literal == "" || literal == "{" || literal == `"\ud800"` {
				if err == nil || got != nil {
					t.Fatalf("invalid data treated as absent: %+v, %v", got, err)
				}
			} else if err != nil || string(got.Data) != literal {
				t.Fatalf("JSON data changed: %+v, %v", got, err)
			}
		})
	}
}

func TestDecodeBinaryErrorTruncation(t *testing.T) {
	data := mustBinaryError(t, nil, wireError{Code: 422, Name: "Invalid", Message: "精度 café",
		TraceID: "trace", Data: []byte(`{"n":9223372036854775807}`), Cause: "cause"})
	for end := range len(data) {
		if got, err := DecodeBinaryError(data[:end], 500); err == nil || got != nil {
			t.Fatalf("truncation at %d accepted: %+v, %v", end, got, err)
		}
	}
}

func TestDecodeBinaryErrorFieldOrder(t *testing.T) {
	var enc codec.Encoder
	enc.WriteFieldString(5, "null")
	enc.WriteFieldString(3, "message")
	enc.WriteFieldString(2, "Invalid")
	enc.WriteFieldInt(1, 422)
	enc.WriteEnd()
	got, err := DecodeBinaryError(enc.Bytes(), 500)
	if err != nil || got.Code != 422 || got.Name != "Invalid" || got.Message != "message" || string(got.Data) != "null" {
		t.Fatalf("valid reordered fields rejected: %+v, %v", got, err)
	}
}

func TestDecodeBinaryErrorOwnsData(t *testing.T) {
	data := mustBinaryError(t, nil, wireError{Code: 422, Name: "Invalid", Message: "invalid", Data: []byte(`{"n":1}`)})
	got, err := DecodeBinaryError(data, 500)
	if err != nil {
		t.Fatal(err)
	}
	clear(data)
	if string(got.Data) != `{"n":1}` {
		t.Fatalf("data aliases the input buffer: %q", got.Data)
	}
}

func TestDecodeBinaryErrorCodeRepresentability(t *testing.T) {
	for _, code := range []int64{math.MinInt64, math.MinInt32, 0, math.MaxInt32, math.MaxInt64} {
		var enc codec.Encoder
		enc.WriteFieldInt(1, code)
		enc.WriteFieldString(2, "")
		enc.WriteFieldString(3, "")
		enc.WriteEnd()
		got, err := DecodeBinaryError(enc.Bytes(), 500)
		if int64(int(code)) != code {
			if err == nil || got != nil {
				t.Fatalf("code narrowed to host int: %+v, %v", got, err)
			}
		} else if err != nil || int64(got.Code) != code || got.Name != "" || got.Message != "" {
			t.Fatalf("present fields replaced with defaults: %+v, %v", got, err)
		}
	}
}

func TestAppendBinaryErrorGolden(t *testing.T) {
	got := mustBinaryError(t, nil, wireError{
		Code:    400,
		Name:    "BadRequest",
		Message: "bad",
		TraceID: "t",
		Data:    []byte(`{}`),
		Cause:   "c",
	})
	want := []byte{
		1, 0xa0, 0x06,
		2, 10, 'B', 'a', 'd', 'R', 'e', 'q', 'u', 'e', 's', 't',
		3, 3, 'b', 'a', 'd',
		4, 1, 't',
		5, 2, '{', '}',
		6, 1, 'c',
		0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary error = %v, want %v", got, want)
	}
}

func TestDecodeBinaryErrorGolden(t *testing.T) {
	data := mustBinaryError(t, nil, wireError{
		Code: 422, Name: "InvalidInput", Message: "invalid", TraceID: "trace",
		Data: []byte(`{"param":"email"}`), Cause: "validation failed",
	})
	got, err := DecodeBinaryError(data, 500)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 422 || got.Name != "InvalidInput" || got.Message != "invalid" ||
		got.TraceID != "trace" || string(got.Data) != `{"param":"email"}` || got.Cause != "validation failed" {
		t.Fatalf("decoded error = %+v", got)
	}
}

func TestBinaryErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  *BinaryError
		want string
	}{
		{name: "name only", err: &BinaryError{Name: "NotFound"}, want: "NotFound"},
		{name: "name and message", err: &BinaryError{Name: "BadRequest", Message: "invalid id"}, want: "BadRequest: invalid id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeBinaryErrorRejectsInvalidEnvelope(t *testing.T) {
	for _, data := range [][]byte{
		{1, 0x80},
		{7, 0},
		{1, 2},
		{5, 1, '{', 0},
		{0, 0},
	} {
		if _, err := DecodeBinaryError(data, 400); err == nil {
			t.Fatalf("accepted invalid envelope %v", data)
		}
	}
}

func TestRouterBinaryParseErrorUsesBinaryEnvelope(t *testing.T) {
	rt := NewRouter()
	handler := TraceMiddleware(rt)
	r := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	r.Header.Set("X-Luxo-Mode", "binary")
	r.Header.Set("X-Request-Id", "trace-parse")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-luxo" {
		t.Fatalf("content type = %q", got)
	}
	got := decodeWireError(t, w.Body.Bytes())
	if got.Name != "BadRequest" || got.Code != 400 || got.Message != "empty binary request" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if got.TraceID != "trace-parse" {
		t.Fatalf("trace ID = %q", got.TraceID)
	}
}

func TestRouterBinaryAppErrorIncludesStructuredFields(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Registry.Register("fail", 1)
	rt.Handle("fail", func(context.Context, *Request) error {
		return luxerrors.New("InvalidInput", 422, "invalid input").
			WithData(luxerrors.ParamError{Param: "email", Error: "invalid"}).
			WithCause(stderrors.New("validation failed"))
	})
	handler := TraceMiddleware(rt)
	r := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader([]byte{1, 0, 0}))
	r.Header.Set("X-Luxo-Mode", "binary")
	r.Header.Set("X-Request-Id", "trace-app")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	got := decodeWireError(t, w.Body.Bytes())
	if got.Name != "InvalidInput" || got.Code != 422 || got.Message != "invalid input" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if got.TraceID != "trace-app" || string(got.Data) != `{"param":"email","error":"invalid"}` {
		t.Fatalf("missing structured fields: %+v", got)
	}
	if got.Cause != "validation failed" {
		t.Fatalf("cause = %q", got.Cause)
	}
}

func decodeWireError(t *testing.T, data []byte) wireError {
	t.Helper()
	dec := codec.NewDecoder(data)
	var got wireError
	for dec.NextField() {
		switch dec.FieldID() {
		case binaryErrorCodeField:
			got.Code = int(dec.ReadInt())
		case binaryErrorNameField:
			got.Name = dec.ReadString()
		case binaryErrorMessageField:
			got.Message = dec.ReadString()
		case binaryErrorTraceIDField:
			got.TraceID = dec.ReadString()
		case binaryErrorDataField:
			got.Data = append([]byte(nil), dec.ReadBytes()...)
		case binaryErrorCauseField:
			got.Cause = dec.ReadString()
		default:
			t.Fatalf("unexpected error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return got
}
