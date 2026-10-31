package rpc

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// Only error decoding is measured; successful calls never enter these decoders.
func BenchmarkRPCErrorDecode(b *testing.B) {
	for _, fixture := range []struct {
		name, text string
		code       int
	}{
		{"basic", "error.rejected", 422},
		{"unicode", "精度 café", 422},
		{"large", strings.Repeat("a", 4096), 422},
		{"internal", "private diagnostic", 503},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			body := encodeError(fixture.code, "Rejected", fixture.text)[1:]
			want := decodeFormerRPCError(body)
			for _, version := range []struct {
				name   string
				decode func([]byte) error
			}{
				{"before", decodeFormerRPCError},
				{"strict_before", decodePriorStrictRPCError},
				{"after", decodeError},
			} {
				b.Run(version.name, func(b *testing.B) {
					var got error
					b.ReportAllocs()
					for b.Loop() {
						got = version.decode(body)
					}
					checkRPCErrorBenchmark(b, got, want)
				})
			}
		})
	}
}

func BenchmarkRPCMalformedErrorDecode(b *testing.B) {
	fields := rpcErrorFields(422, "Rejected", strings.Repeat("a", 32<<10))
	badText := rpcErrorBody(fields[:]...)
	badText[len(badText)-2] = 0xff
	for name, body := range map[string][]byte{
		"bad_text":  badText,
		"duplicate": rpcErrorBody(fields[0], fields[1], fields[2], fields[0]),
		"trailing":  append(rpcErrorBody(fields[:]...), 0),
	} {
		b.Run(name, func(b *testing.B) {
			for _, version := range []struct {
				name   string
				decode func([]byte) error
			}{
				{"strict_before", decodePriorStrictRPCError},
				{"after", decodeError},
			} {
				b.Run(version.name, func(b *testing.B) {
					var got error
					b.ReportAllocs()
					for b.Loop() {
						got = version.decode(body)
					}
					if _, ok := stderrors.AsType[*luxerrors.AppError](got); got == nil || ok {
						b.Fatalf("malformed error accepted: %v", got)
					}
				})
			}
		})
	}
}

// Frozen strict decoder before delaying string ownership until validation.
func decodePriorStrictRPCError(data []byte) error {
	dec := codec.NewDecoder(data)
	var code int64
	var name, message string
	var seen uint8
	for dec.NextField() {
		field := dec.FieldID()
		bit := uint8(1 << uint(field))
		if seen&bit != 0 {
			return fmt.Errorf("rpc: duplicate error field %d", field)
		}
		seen |= bit
		switch field {
		case 1:
			code = dec.ReadInt()
		case 2:
			name = dec.ReadString()
		case 3:
			message = dec.ReadString()
		default:
			return fmt.Errorf("rpc: unknown error field %d", field)
		}
	}
	if err := validatePriorRPCErrorFields(dec, code, name, message, seen); err != nil {
		return err
	}
	value := &luxerrors.AppError{Code: int(code), Name: name, Message: message}
	if code >= 500 {
		value.Cause = fmt.Errorf("rpc error %d %s: %s", code, name, message)
		value.Message = "error.internal"
		value.Internal = true
	}
	return value
}

func validatePriorRPCErrorFields(dec *codec.Decoder, code int64, name, message string, seen uint8) error {
	if err := dec.Finish(); err != nil {
		return fmt.Errorf("rpc: decode error envelope: %w", err)
	}
	if seen != 0b1110 {
		return fmt.Errorf("rpc: error envelope missing required fields")
	}
	if int64(int(code)) != code {
		return fmt.Errorf("rpc: error code exceeds host int range")
	}
	if !utf8.ValidString(name) || !utf8.ValidString(message) {
		return fmt.Errorf("rpc: error envelope contains invalid UTF-8")
	}
	return nil
}

func checkRPCErrorBenchmark(b *testing.B, got, want error) {
	b.Helper()
	actual, ok := stderrors.AsType[*luxerrors.AppError](got)
	expected := want.(*luxerrors.AppError)
	if !ok || actual.Code != expected.Code || actual.Name != expected.Name || actual.Message != expected.Message ||
		actual.Internal != expected.Internal || fmt.Sprint(actual.Cause) != fmt.Sprint(expected.Cause) {
		b.Fatalf("error decoding changed: got %+v, want %+v", actual, expected)
	}
}

// Frozen former decoder for equal-output comparisons on valid envelopes only.
func decodeFormerRPCError(data []byte) error {
	dec := codec.NewDecoder(data)
	var code int64
	var name, message string
	var seen uint8
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			code = dec.ReadInt()
			seen |= 1 << 0
		case 2:
			name = dec.ReadString()
			seen |= 1 << 1
		case 3:
			message = dec.ReadString()
			seen |= 1 << 2
		default:
			return fmt.Errorf("rpc: unknown error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		return fmt.Errorf("rpc: decode error envelope: %w", err)
	}
	if dec.FieldID() != 0 {
		return fmt.Errorf("rpc: error envelope missing end marker")
	}
	if dec.Offset() != len(data) {
		return fmt.Errorf("rpc: error envelope has trailing bytes")
	}
	if seen != 0b111 {
		return fmt.Errorf("rpc: error envelope missing required fields")
	}
	appErr := &luxerrors.AppError{Name: name, Code: int(code), Message: message}
	if code >= 500 {
		appErr.Message = "error.internal"
		appErr.Internal = true
		appErr.Cause = fmt.Errorf("rpc error %d %s: %s", code, name, message)
	}
	return appErr
}
