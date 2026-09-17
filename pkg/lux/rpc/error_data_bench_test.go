package rpc

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// A nil text override excludes reader reuse and compares identical validation.
func BenchmarkRPCJSONSharedValidation(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"small", `{"ok":true}`}, {"precision", rpcPrecisionJSON},
		{"72KiB", `"` + strings.Repeat("sensitive", 8192) + `"`},
	} {
		for _, version := range []struct {
			name     string
			validate func([]byte, []byte) error
		}{
			{"before", validateRPCErrorDataBeforeReaderReuse}, {"shared", validateRPCErrorData},
		} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				raw := []byte(sample.raw)
				b.ReportAllocs()
				for b.Loop() {
					if err := version.validate(raw, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// The no-data case quantifies the missing feature, not an equivalent valid
// implementation. Both cases reuse TCP connections and exclude the handshake.
func BenchmarkRPCStructuredErrorRoundTrip(b *testing.B) {
	for _, withData := range []bool{false, true} {
		name := "without_data"
		source := luxerrors.New("PrecisionRejected", 422, "error.precision")
		if withData {
			name = "with_data"
			source.Data = rpcPrecisionData{}
		}
		b.Run(name, func(b *testing.B) { benchmarkRPCStructuredErrorRoundTrip(b, source) })
	}
}

func benchmarkRPCStructuredErrorRoundTrip(b *testing.B, source *luxerrors.AppError) {
	router := api.NewRouter()
	router.Handle("reject", func(context.Context, *api.Request) error { return source })
	router.Registry.Register("reject", 1)
	router.Registry.RegisterParams("reject", nil)
	client := NewClient(startTestRPCServer(b, NewServer(router)))
	b.Cleanup(client.Close)
	_, err := client.Call(1, nil)
	value, ok := stderrors.AsType[*luxerrors.AppError](err)
	if !ok || (value.Data == nil) != (source.Data == nil) {
		b.Fatalf("warm error lost data: %v", err)
	}
	if source.Data != nil {
		raw, err := value.Data.AppendJSON(nil)
		if err != nil || string(raw) != rpcPrecisionJSON {
			b.Fatalf("round trip changed raw data: %s / %v", raw, err)
		}
	}
	connection, err := client.pool.Get()
	if err != nil {
		b.Fatal(err)
	}
	client.pool.Put(connection)
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.Call(1, nil)
		if _, ok := stderrors.AsType[*luxerrors.AppError](err); !ok {
			b.Fatal(err)
		}
	}
	reused, err := client.pool.Get()
	if err != nil {
		b.Fatal(err)
	}
	client.pool.Put(reused)
	if reused != connection {
		b.Fatal("error benchmark replaced its warmed TCP connection")
	}
}

func BenchmarkRPCErrorReaderReuse(b *testing.B) {
	for _, sample := range []struct {
		name string
		data luxerrors.ErrorData
	}{
		{"precision", rpcPrecisionData{}},
		{"resource", luxerrors.ResourceError{Resource: "Task", ID: luxerrors.IntID(math.MaxInt64)}},
		{"large_text", luxerrors.ParamError{Param: "field", Error: strings.Repeat("sensitive", 8192)}},
		{"nested", rpcNativeData{raw: `{"n":` + strings.Repeat("[", 64) + `1e9999` + strings.Repeat("]", 64) + `}`}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			source := luxerrors.New("Rejected", 422, "error.rejected").WithData(sample.data)
			before, err := encodeApplicationErrorBeforeReaderReuse(source)
			if err != nil {
				b.Fatal(err)
			}
			after, err := encodeApplicationError(source)
			if err != nil || !bytes.Equal(before, after) {
				b.Fatalf("reuse changed bytes: %v", err)
			}
			benchmarkRPCErrorReaderCodec(b, source, before[1:])
		})
	}
	benchmarkRPCErrorReaderText(b)
}

func benchmarkRPCErrorReaderCodec(b *testing.B, source *luxerrors.AppError, body []byte) {
	for _, version := range []struct {
		name   string
		encode func(*luxerrors.AppError) ([]byte, error)
		decode func([]byte) error
	}{
		{"before", encodeApplicationErrorBeforeReaderReuse, decodeErrorBeforeReaderReuse},
		{"reuse", encodeApplicationError, decodeError},
	} {
		b.Run("encode/"+version.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := version.encode(source); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("decode/"+version.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = version.decode(body)
			}
		})
	}
}

func benchmarkRPCErrorReaderText(b *testing.B) {
	raw, text := []byte(rpcPrecisionJSON), []byte(`{"label":"精度 café"}`)
	for _, version := range []struct {
		name string
		data luxerrors.ErrorData
	}{
		{"before", ownRPCErrorDataBeforeReaderReuse(raw, text)},
		{"reuse", ownRPCErrorData(raw, text)},
	} {
		for _, field := range []string{"label", "number", "items", "absent"} {
			b.Run("text/"+field+"/"+version.name, func(b *testing.B) {
				dst := make([]byte, 0, 1024)
				b.ReportAllocs()
				for b.Loop() {
					dst, _ = version.data.AppendText(dst[:0], field)
				}
			})
		}
	}
}

// Freeze the previous validated decoder, including its helper/layout, rather
// than disabling checks or comparing different error payloads.
func BenchmarkRPCErrorDataMigration(b *testing.B) {
	source := luxerrors.New("Rejected", 422, "error.rejected")
	body := encodeError(source.Code, source.Name, source.Message)[1:]
	for _, version := range []struct {
		name   string
		decode func([]byte) error
	}{
		{"decode_without_data_before", decodeErrorWithoutData}, {"decode_without_data_after", decodeError},
	} {
		b.Run(version.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = version.decode(body)
			}
		})
	}
	b.Run("encode_without_data_before", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = encodeErrorWithoutData(source.Code, source.Name, source.Message)
		}
	})
	b.Run("encode_without_data_after", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := encodeApplicationError(source); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func encodeErrorWithoutData(code int, name, message string) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(1, int64(code))
	enc.WriteFieldString(2, name)
	enc.WriteFieldString(3, message)
	enc.WriteEnd()
	resp := make([]byte, 0, 1+len(enc.Bytes()))
	resp = append(resp, statusError)
	return append(resp, enc.Bytes()...)
}

func BenchmarkRPCStructuredErrorData(b *testing.B) {
	source := luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{})
	encoded, err := encodeApplicationError(source)
	if err != nil {
		b.Fatal(err)
	}
	remote := decodeError(encoded[1:]).(*luxerrors.AppError)
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := encodeApplicationError(source); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = decodeError(encoded[1:])
		}
	})
	for _, field := range []string{"label", "number", "items", "absent"} {
		b.Run("text/"+field, func(b *testing.B) {
			dst := make([]byte, 0, 1024)
			b.ReportAllocs()
			for b.Loop() {
				dst, _ = remote.Data.AppendText(dst[:0], field)
			}
		})
	}
	b.Run("forward_json", func(b *testing.B) {
		dst := make([]byte, 0, 1024)
		b.ReportAllocs()
		for b.Loop() {
			dst, _ = remote.Data.AppendJSON(dst[:0])
		}
	})
}

// decodeErrorWithoutData rejects malformed envelopes before constructing an application error.
func decodeErrorWithoutData(data []byte) error {
	fields, err := readRPCErrorFieldsWithoutData(data)
	if err != nil {
		return err
	}
	// Only 32-bit hosts can narrow the signed wire value to a host int.
	if int64(int(fields.code)) != fields.code {
		return fmt.Errorf("rpc: error code exceeds host int range")
	}
	if !utf8.Valid(fields.name) || !utf8.Valid(fields.message) {
		return fmt.Errorf("rpc: error envelope contains invalid UTF-8")
	}
	// Copy only validated text; each string owns its storage independently.
	value := &luxerrors.AppError{Code: int(fields.code), Name: string(fields.name), Message: string(fields.message)}
	if fields.code >= 500 {
		value.Cause = fmt.Errorf("rpc error %d %s: %s", fields.code, value.Name, value.Message)
		value.Message = "error.internal"
		value.Internal = true
	}
	return value
}

type rpcErrorEnvelopeWithoutData struct {
	code          int64
	name, message []byte
}

func readRPCErrorFieldsWithoutData(data []byte) (rpcErrorEnvelopeWithoutData, error) {
	var value rpcErrorEnvelopeWithoutData
	var seen uint8
	offset := 0
	for {
		field, size := codec.ReadVarint(data, offset)
		if size <= 0 {
			return value, fmt.Errorf("rpc: invalid error field identifier at offset %d", offset)
		}
		offset += size
		if field == 0 {
			break
		}
		if field > 3 {
			return value, fmt.Errorf("rpc: unknown error field %d", field)
		}
		bit := uint8(1 << field)
		if seen&bit != 0 {
			return value, fmt.Errorf("rpc: duplicate error field %d", field)
		}
		seen |= bit
		size = value.readField(data, offset, field)
		if size <= 0 {
			return value, fmt.Errorf("rpc: invalid error field %d at offset %d", field, offset)
		}
		offset += size
	}
	if offset != len(data) {
		return value, fmt.Errorf("rpc: error envelope has trailing bytes")
	}
	if seen != 0b1110 {
		return value, fmt.Errorf("rpc: error envelope missing required fields")
	}
	return value, nil
}

// readField is called only for the three known field identifiers.
func (e *rpcErrorEnvelopeWithoutData) readField(data []byte, offset int, field uint64) (size int) {
	switch field {
	case 1:
		e.code, size = codec.ReadSvarint(data, offset)
	case 2:
		e.name, size = codec.ReadBytes(data, offset)
	case 3:
		e.message, size = codec.ReadBytes(data, offset)
	}
	return size
}
