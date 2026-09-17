package rpc

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

const rpcPrecisionJSON = `{"max":9223372036854775807,"min":-9223372036854775808,"number":123456789012345678901234567890.12345678901234567890,"label":"精度 café","jsonString":"quoted","date":"2026-09-16T00:00:00Z","items":[1e+9999,-0,null]}`

// The different text forms cannot be inferred from the JSON string type.
type rpcPrecisionData struct{}

func (rpcPrecisionData) AppendJSON(dst []byte) ([]byte, error) {
	return append(dst, rpcPrecisionJSON...), nil
}

func (rpcPrecisionData) AppendText(dst []byte, field string) ([]byte, bool) {
	var value string
	switch field {
	case "max":
		value = "9223372036854775807"
	case "min":
		value = "-9223372036854775808"
	case "number":
		value = "123456789012345678901234567890.12345678901234567890"
	case "label":
		value = "精度 café"
	case "jsonString":
		value = `"quoted"`
	case "date":
		value = `"2026-09-16T00:00:00Z"`
	case "items":
		value = "[1e+9999,-0,null]"
	default:
		return dst, false
	}
	return append(dst, value...), true
}

func requireRPCPrecisionData(t *testing.T, err error) *luxerrors.AppError {
	t.Helper()
	value, ok := stderrors.AsType[*luxerrors.AppError](err)
	if !ok || value.Code != 422 || value.Name != "PrecisionRejected" || value.Message != "error.precision" || value.Data == nil {
		t.Fatalf("structured error data lost: %+v", value)
	}
	encoded, encodeErr := value.Data.AppendJSON([]byte("prefix"))
	if encodeErr != nil || string(encoded) != "prefix"+rpcPrecisionJSON {
		t.Fatalf("raw number/text changed: %s, %v", encoded, encodeErr)
	}
	for _, field := range []string{"max", "min", "number", "label", "jsonString", "date", "items", "absent"} {
		got, found := value.Data.AppendText([]byte("prefix"), field)
		want, wantFound := (rpcPrecisionData{}).AppendText([]byte("prefix"), field)
		if found != wantFound || !bytes.Equal(got, want) {
			t.Errorf("text %s = %q/%t, want %q/%t", field, got, found, want, wantFound)
		}
	}
	return value
}

func TestRPCStructuredErrorDataRoundTrip(t *testing.T) {
	router := api.NewRouter()
	router.Handle("reject", func(context.Context, *api.Request) error {
		return fmt.Errorf("service: %w", luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{}))
	})
	router.Registry.Register("reject", 1)
	router.Registry.RegisterParams("reject", nil)
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	t.Cleanup(client.Close)
	for _, traced := range []bool{false, true} {
		t.Run(fmt.Sprint(traced), func(t *testing.T) {
			ctx := context.Background()
			if traced {
				ctx, _ = api.WithDebugTrace(ctx, "error-data")
			}
			_, err := client.CallWithMaskContext(ctx, "", 1, nil, nil)
			requireRPCPrecisionData(t, err)
		})
	}
}

func TestRPCErrorDataDecoding(t *testing.T) {
	fields := rpcErrorFields(422, "PrecisionRejected", "error.precision")
	data := codec.AppendBytes([]byte{4}, []byte(rpcPrecisionJSON))
	// Only text that differs from raw JSON needs a separate representation.
	text := codec.AppendBytes([]byte{5}, []byte(`{"label":"精度 café"}`))
	for _, body := range [][]byte{
		rpcErrorBody(fields[0], fields[1], fields[2], data, text),
		rpcErrorBody(text, data, fields[2], fields[1], fields[0]),
	} {
		value := requireRPCPrecisionData(t, decodeError(body))
		clear(body)
		requireRPCPrecisionData(t, value)
	}
}

func TestRPCErrorRejectsMalformedData(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	for _, raw := range []string{"", "{", "NaN", "1 2", "{\"secret\":\"\xff\"}",
		`{"n":"\ud800"}`, `{"\uDC00":1}`, `{"n":["\uDBFF"]}`} {
		body := rpcErrorBody(fields[0], fields[1], fields[2], codec.AppendBytes([]byte{4}, []byte(raw)))
		requireRPCEnvelopeFailure(t, body)
	}
	valid := codec.AppendBytes([]byte{4}, []byte(rpcPrecisionJSON))
	for _, raw := range []string{"", "[]", "null", `{"label":12}`, `{"label":true}`, `{"label":"x","label":"y"}`, `{"label":`, `{"label":"\ud800"}`, `{} true`, `{}!`} {
		body := rpcErrorBody(fields[0], fields[1], fields[2], valid, codec.AppendBytes([]byte{5}, []byte(raw)))
		requireRPCEnvelopeFailure(t, body)
	}
	for _, extras := range [][][]byte{
		{valid, valid},
		{codec.AppendBytes([]byte{5}, []byte(`{}`))},
		{valid, codec.AppendBytes([]byte{5}, []byte(`{}`)), codec.AppendBytes([]byte{5}, []byte(`{}`))},
	} {
		body := rpcErrorBody(append(fields[:], extras...)...)
		requireRPCEnvelopeFailure(t, body)
	}
}

type rpcNativeData struct {
	raw  string
	err  error
	text func([]byte, string) ([]byte, bool)
}

func (d rpcNativeData) AppendJSON(dst []byte) ([]byte, error) { return append(dst, d.raw...), d.err }
func (d rpcNativeData) AppendText(dst []byte, field string) ([]byte, bool) {
	if d.text == nil {
		return dst, false
	}
	return d.text(dst, field)
}

func TestRPCNativeErrorDataShapes(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `1e9999`, `"quoted"`, ` { "n" : -0 } `, `{}`, `{"n":{"x":1,"x":2}}`, `{"n":"\ud800\udc00"}`} {
		t.Run(raw, func(t *testing.T) {
			source := luxerrors.New("Native", 422, "error.native").WithData(rpcNativeData{raw: raw})
			encoded, err := encodeApplicationError(source)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := stderrors.AsType[*luxerrors.AppError](decodeError(encoded[1:]))
			if !ok || got.Data == nil {
				t.Fatalf("data lost: %v", got)
			}
			result, err := got.Data.AppendJSON(nil)
			if err != nil || string(result) != raw {
				t.Fatalf("data changed: %s, %v", result, err)
			}
			for _, name := range []string{"n", "missing"} {
				text, found := got.Data.AppendText([]byte("prefix"), name)
				if found || string(text) != "prefix" {
					t.Fatalf("unexposed field %s: %s", name, text)
				}
			}
		})
	}
}

func TestRPCErrorDataRawLookup(t *testing.T) {
	raw := []byte(` { "n" : 1, "n" : 1e+9999, "escaped\u006eame" : "x", "list" : [1,-0] } `)
	value := ownRPCErrorData(raw, nil)
	for name, want := range map[string]string{"n": "1e+9999", "escapedname": `"x"`, "list": "[1,-0]"} {
		got, ok := value.AppendText(nil, name)
		if !ok || string(got) != want {
			t.Errorf("%s: %s", name, got)
		}
	}
	// Relaying an already validated error must not invoke a second JSON writer.
	encoded, err := encodeApplicationError(luxerrors.New("Raw", 422, "error.raw").WithData(value))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := stderrors.AsType[*luxerrors.AppError](decodeError(encoded[1:]))
	if !ok || got.Data == nil {
		t.Fatal(got)
	}
	result, err := got.Data.AppendJSON(nil)
	if err != nil || !bytes.Equal(result, raw) {
		t.Fatalf("relay changed data: %s, %v", result, err)
	}
}

func TestRPCJSONNameComparison(t *testing.T) {
	long := strings.Repeat("field", 40)
	for _, test := range []struct {
		raw, field string
		matches    bool
	}{
		{`"field"`, "field", true}, {`"field"`, "other", false},
		{`"\u0066ield"`, "field", true}, {`"\ud800"`, "field", false},
		{`"\u0066` + long[1:] + `"`, long, true},
	} {
		if got := rpcJSONNameMatches([]byte(test.raw), test.field); got != test.matches {
			t.Fatalf("%s = %t", test.raw, got)
		}
	}
	name := []byte(`"field"`)
	if allocs := testing.AllocsPerRun(100, func() { _ = rpcJSONNameMatches(name, "field") }); allocs != 0 {
		t.Fatalf("name lookup allocated: %v", allocs)
	}
}

func TestRPCErrorDataEncodingFailureIsAtomic(t *testing.T) {
	for _, data := range []rpcNativeData{
		{raw: `{"secret":`, err: fmt.Errorf("native failed")}, {raw: ""}, {raw: `{`},
		{raw: `"\uD800"`}, {raw: `{"\uDC00":1}`}, {raw: `{"x":["\uDBFF"]}`},
		{raw: `{"n":1}`, text: func(dst []byte, _ string) ([]byte, bool) { return append(dst, 0xff), true }},
		{raw: `{"n":1,"n":2}`},
	} {
		for _, traced := range []bool{false, true} {
			source := luxerrors.New("Secret", 422, "private").WithData(data)
			var output bytes.Buffer
			var trace *api.DebugTraceSession
			if traced {
				_, trace = api.WithDebugTrace(context.Background(), "atomic-error")
			}
			if err := writeApplicationError(&output, source, trace); err != nil {
				t.Fatal(err)
			}
			frame, err := ReadFrame(&output, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = decodeTracedResponse(frame)
			value, ok := stderrors.AsType[*luxerrors.AppError](err)
			if !ok || value.Code != 500 || !value.Internal || value.Data != nil || value.Name != "Internal" {
				t.Fatalf("partial native error leaked: %+v", value)
			}
		}
	}
}

func TestRPCInternalErrorDataNeverAccessed(t *testing.T) {
	for _, code := range []int{422, 503} {
		source := luxerrors.New("Private", code, "secret")
		source.Internal = true
		source.Data = forbiddenRPCErrorData{t}
		encoded, err := encodeApplicationError(source)
		if err != nil {
			t.Fatal(err)
		}
		value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(encoded[1:]))
		if !ok || value.Code != code || !value.Internal || value.Data != nil || value.Message != "error.internal" {
			t.Fatalf("internal flag lost: %+v", value)
		}
	}
	fields := rpcErrorFields(422, "Private", "secret")
	for _, internal := range [][]byte{{6}, {6, 2}, {6, 255}, {6, 1, 6, 1}} {
		requireRPCEnvelopeFailure(t, rpcErrorBody(append(fields[:], internal)...))
	}
}

type forbiddenRPCErrorData struct{ t *testing.T }

func (d forbiddenRPCErrorData) AppendJSON(dst []byte) ([]byte, error) {
	d.t.Error("accessed internal data")
	return dst, nil
}
func (d forbiddenRPCErrorData) AppendText(dst []byte, _ string) ([]byte, bool) {
	d.t.Error("accessed internal data")
	return dst, false
}

func TestRPCErrorDataAtStreamBoundaries(t *testing.T) {
	frame, err := encodeApplicationError(luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{}))
	if err != nil {
		t.Fatal(err)
	}
	requireRPCPrecisionData(t, validateStreamAck(frame))
	client, server := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	sub := &Subscription{messages: make(chan []byte, 1), errors: make(chan error, 1), conn: client, cancel: cancel}
	sub.stop = context.AfterFunc(ctx, func() { client.Close() })
	done := make(chan struct{})
	t.Cleanup(func() { sub.Close(); server.Close(); <-done })
	go func() { defer close(done); sub.readLoop(ctx) }()
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(server, frame); err != nil {
		t.Fatal(err)
	}
	requireRPCPrecisionData(t, <-sub.Errors())
	<-done
	if _, ok := <-sub.Messages(); ok {
		t.Fatal("error appeared as a successful stream message")
	}
}

func TestRPCErrorDataPrivateParserGuards(t *testing.T) {
	for _, raw := range []string{`{x}`, `{"x":`, `{"x":1`} {
		if got := rpcJSONField([]byte(raw), "x"); got != nil {
			t.Fatalf("invalid lookup = %s", got)
		}
		if _, err := rpcErrorTextOverrides(rpcNativeData{raw: raw}, []byte(raw)); err == nil {
			t.Fatal("invalid native object accepted")
		}
	}
}

func TestRPCErrorDataTruncationAndOptionalInternalFlag(t *testing.T) {
	encoded, err := encodeApplicationError(luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{}))
	if err != nil {
		t.Fatal(err)
	}
	for length := range len(encoded) - 1 {
		requireRPCEnvelopeFailure(t, encoded[1:1+length])
	}
	requireRPCEnvelopeFailure(t, append(bytes.Clone(encoded[1:]), 0))
	body := append(bytes.Clone(encoded[1:len(encoded)-1]), 6, 0, 0)
	requireRPCPrecisionData(t, decodeError(body))
	body[len(body)-2] = 1
	value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(body))
	if !ok || !value.Internal || value.Data != nil {
		t.Fatalf("internal data leaked: %+v", value)
	}
}
