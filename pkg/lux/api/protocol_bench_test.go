package api

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Compare actual schema conversion with and without a nullable envelope. This
// measures the new feature's local cost, not HTTP/RPC or the previous release.
func BenchmarkNullableRootJSON(b *testing.B) {
	list := codec.AppendVarint(nil, 128)
	for range 128 {
		list = codec.AppendSvarint(list, 42)
	}
	for _, fixture := range []struct {
		name, kind, want string
		list             bool
		data             []byte
	}{
		{"int", "Int", "42", false, []byte{84}},
		{"zero", "Int", "0", false, []byte{0}},
		{"false", "Boolean", "false", false, []byte{0}},
		{"empty-text", "String", `""`, false, []byte{0}},
		{"empty-list", "Int", "[]", true, []byte{0}},
		{"list128", "Int", "[" + strings.TrimSuffix(strings.Repeat("42,", 128), ",") + "]", true, list},
		{"object", "RootItem", `{"id":42}`, false, []byte{0, 1, 84, 0}},
	} {
		for _, mode := range []string{"plain", "present", "null"} {
			b.Run(fixture.name+"/"+mode, func(b *testing.B) {
				router := NewRouter()
				if err := router.Schema.RegisterType(&schema.TypeDecl{Name: "RootItem", Fields: []schema.Field{
					{ID: 1, Name: "id", Type: schema.FieldInt},
				}}); err != nil {
					b.Fatal(err)
				}
				if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", ReturnType: fixture.kind,
					ReturnList: fixture.list, ReturnNullable: mode != "plain"}); err != nil {
					b.Fatal(err)
				}
				data, want := fixture.data, fixture.want
				if mode == "present" {
					data = append([]byte{1}, data...)
				} else if mode == "null" {
					data, want = []byte{0}, "null"
				}
				benchmarkNullableRootJSON(b, router, data, want)
			})
		}
	}
}

func benchmarkNullableRootJSON(b *testing.B, router *Router, data []byte, want string) {
	got, err := router.convertBinaryToJSON("read", data)
	if err != nil || string(got) != want {
		b.Fatalf("invalid fixture: %s, %v; want %s", got, err, want)
	}
	b.ReportAllocs()
	for b.Loop() {
		got, err = router.convertBinaryToJSON("read", data)
	}
	if err != nil || string(got) != want {
		b.Fatalf("conversion changed result: %s, %v; want %s", got, err, want)
	}
}

// Compare identical basic errors; detailed errors deliberately expose previously lost fields.
// This excludes WebSocket I/O and never measures successful requests.
func BenchmarkWSJSONErrorResponse(b *testing.B) {
	for _, detailed := range []bool{false, true} {
		ctx := context.Background()
		err := errors.New("PrecisionRejected", 422, "error.precision_rejected")
		name := "basic"
		if detailed {
			name = "structured"
			ctx = context.WithValue(ctx, traceKey{}, "error-parity")
			err = err.WithData(precisionErrorData{Value: 9223372036854775807, Label: "精度 café"})
		}
		b.Run(name, func(b *testing.B) { benchmarkWSJSONErrorResponse(b, ctx, err, detailed) })
	}
}

func benchmarkWSJSONErrorResponse(b *testing.B, ctx context.Context, err error, detailed bool) {
	router := NewRouter()
	var old, current ResponseBuf
	appendFormerWSJSONError(&old, 42, err)
	appendWSJSONError(&current, 42, router.buildWireError(ctx, "", err))
	if !detailed && !bytes.Equal(old.B, current.B) {
		b.Fatal("basic comparison must produce identical bytes")
	}
	if detailed && (!bytes.Contains(current.B, []byte(`"data":`)) || bytes.Contains(old.B, []byte(`"data":`))) {
		b.Fatal("structured comparison must retain the formerly omitted data")
	}
	for _, before := range []bool{true, false} {
		name := "after"
		if before {
			name = "before"
		}
		b.Run(name, func(b *testing.B) {
			buf := ResponseBuf{B: make([]byte, 0, 256)}
			b.ReportAllocs()
			for b.Loop() {
				buf.B = buf.B[:0]
				if before {
					appendFormerWSJSONError(&buf, 42, err)
				} else {
					appendWSJSONError(&buf, 42, router.buildWireError(ctx, "", err))
				}
			}
		})
	}
}

// Frozen former implementation; missing data/trace fields are not an acceptable optimization.
func appendFormerWSJSONError(buf *ResponseBuf, seqID uint64, err error) {
	var appErr *errors.AppError
	if !stderrors.As(err, &appErr) {
		appErr = errors.Wrap(err)
	}
	buf.AppendString(`{"$id":`)
	buf.B = strconv.AppendUint(buf.B, seqID, 10)
	buf.AppendString(`,"error":`)
	buf.AppendJSONString(appErr.Name)
	buf.AppendString(`,"code":`)
	buf.AppendInt(int64(appErr.Code))
	buf.AppendString(`,"message":`)
	buf.AppendJSONString(appErr.Message)
	buf.AppendByte('}')
}

// Both encoders write the same complete payload; JSON data is prepared before timing.
func BenchmarkBinaryErrorEnvelope(b *testing.B) {
	fixtures := map[string]wireError{
		"basic": {Code: 422, Name: "InvalidInput", Message: "error.invalid"},
		"structured": {Code: 422, Name: "PrecisionRejected", Message: "精度 café",
			TraceID: "trace", Data: []byte(`{"value":9223372036854775807}`), Cause: "cause"},
		"large": {Code: 422, Name: "InvalidInput", Message: "error.invalid",
			Data: []byte(`{"label":"` + strings.Repeat("a", 4096) + `"}`)},
	}
	for name, value := range fixtures {
		b.Run(name, func(b *testing.B) { benchmarkBinaryErrorEnvelope(b, value) })
	}
}

func benchmarkBinaryErrorEnvelope(b *testing.B, value wireError) {
	data := mustBinaryError(b, nil, value)
	if !bytes.Equal(data, appendFormerBinaryError(nil, value)) {
		b.Fatal("encoder comparison must produce identical bytes")
	}
	for _, before := range []bool{true, false} {
		name := "after"
		if before {
			name = "before"
		}
		b.Run("encode/"+name, func(b *testing.B) {
			buf := make([]byte, 0, len(data))
			var err error
			b.ReportAllocs()
			for b.Loop() {
				if before {
					buf = appendFormerBinaryError(buf[:0], value)
				} else {
					buf, err = appendBinaryError(buf[:0], value)
				}
			}
			if err != nil {
				b.Fatal(err)
			}
		})
		b.Run("decode/"+name, func(b *testing.B) {
			benchmarkBinaryErrorDecode(b, data, value, before)
		})
	}
}

func benchmarkBinaryErrorDecode(b *testing.B, data []byte, expected wireError, before bool) {
	var value *BinaryError
	var err error
	b.ReportAllocs()
	for b.Loop() {
		if before {
			value, err = decodeFormerBinaryError(data, 500)
		} else {
			value, err = DecodeBinaryError(data, 500)
		}
	}
	if err != nil || value == nil || value.Code != expected.Code || value.Name != expected.Name ||
		value.Message != expected.Message || value.TraceID != expected.TraceID ||
		value.Cause != expected.Cause || !bytes.Equal(value.Data, expected.Data) {
		b.Fatalf("decoder comparison changed the complete payload: %+v, %v", value, err)
	}
}

// Frozen encoder: creates an intermediate buffer and copies it to the response.
func appendFormerBinaryError(dst []byte, e wireError) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(binaryErrorCodeField, int64(e.Code))
	enc.WriteFieldString(binaryErrorNameField, e.Name)
	enc.WriteFieldString(binaryErrorMessageField, e.Message)
	if e.TraceID != "" {
		enc.WriteFieldString(binaryErrorTraceIDField, e.TraceID)
	}
	if len(e.Data) > 0 {
		enc.WriteFieldBytes(binaryErrorDataField, e.Data)
	}
	if e.Cause != "" {
		enc.WriteFieldString(binaryErrorCauseField, e.Cause)
	}
	enc.WriteEnd()
	return append(dst, enc.Bytes()...)
}

// Frozen decoder is used only with complete, valid envelopes, never invalid-input timing.
func decodeFormerBinaryError(data []byte, statusCode int) (*BinaryError, error) {
	dec := codec.NewDecoder(data)
	result := &BinaryError{Code: statusCode, Name: "Error", Message: fmt.Sprintf("HTTP %d", statusCode)}
	for dec.NextField() {
		switch dec.FieldID() {
		case binaryErrorCodeField:
			result.Code = int(dec.ReadInt())
		case binaryErrorNameField:
			result.Name = dec.ReadString()
		case binaryErrorMessageField:
			result.Message = dec.ReadString()
		case binaryErrorTraceIDField:
			result.TraceID = dec.ReadString()
		case binaryErrorDataField:
			result.Data = append(result.Data[:0], dec.ReadBytes()...)
		case binaryErrorCauseField:
			result.Cause = dec.ReadString()
		default:
			return nil, fmt.Errorf("unknown binary error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		return nil, fmt.Errorf("decode binary error: %w", err)
	}
	if dec.FieldID() != 0 {
		return nil, fmt.Errorf("decode binary error: missing end marker")
	}
	if dec.Offset() != len(data) {
		return nil, fmt.Errorf("decode binary error: trailing bytes")
	}
	if len(result.Data) > 0 && !json.Valid(result.Data) {
		return nil, fmt.Errorf("decode binary error: invalid JSON data")
	}
	return result, nil
}
