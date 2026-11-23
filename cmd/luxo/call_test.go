package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/httputil"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/spf13/cobra"
)

func TestCLICallHonorsCommandContext(t *testing.T) {
	for _, mode := range []string{"json", "binary"} {
		t.Run(mode, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			writeCLIResponseReadSchema(t)
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, `{"data":42}`)
			}))
			defer server.Close()
			useCLICallHost(t, server.URL)
			oldJSON := callJSON
			callJSON = mode == "json"
			t.Cleanup(func() { callJSON = oldJSON })
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			cmd := newCLICallCommand(ctx)
			output, err := captureCLIResponse(t, func() error { return runCall(cmd, []string{"echo"}) })
			if !errors.Is(err, context.Canceled) || requests.Load() != 0 || output != "" {
				t.Fatalf("cancelled command contacted server or printed output: requests=%d output=%q err=%v", requests.Load(), output, err)
			}
		})
	}
}

func newCLICallCommand(ctx context.Context) *cobra.Command {
	var cmd cobra.Command
	cmd.SetContext(ctx)
	return &cmd
}

func TestCLICallEnforcesResponseBodyLimit(t *testing.T) {
	for _, mode := range []string{"json", "binary"} {
		t.Run(mode, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			writeCLIResponseReadSchema(t)
			oldJSON, oldLimit := callJSON, callResponseMaxBytes
			callJSON = mode == "json"
			t.Cleanup(func() { callJSON, callResponseMaxBytes = oldJSON, oldLimit })
			payload, expected := []byte(`{"data":900}`), "{\"data\":900}\n"
			if !callJSON {
				payload, expected = codec.AppendSvarint(nil, 900), "900\n"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "binary" {
					w.Header().Set("X-Luxo-Mode", "binary")
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			useCLICallHost(t, server.URL)
			call := func() (string, error) {
				return captureCLIResponse(t, func() error { return runCall(newCLICallCommand(t.Context()), []string{"echo"}) })
			}
			callResponseMaxBytes = int64(len(payload))
			if out, err := call(); err != nil || out != expected {
				t.Fatalf("response at the limit failed: %q %v", out, err)
			}
			callResponseMaxBytes--
			out, err := call()
			var limitErr *httputil.ResponseBodyLimitError
			if out != "" || !errors.As(err, &limitErr) || limitErr.Limit != callResponseMaxBytes {
				t.Fatalf("response over the limit printed data or returned an unrelated error: %q %v", out, err)
			}
		})
	}
}

func TestCLICallTimeoutIncludesResponseBody(t *testing.T) {
	requestDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(requestDone)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
			_, _ = io.WriteString(w, `{"data":42}`)
		}
	}))
	defer server.Close()
	useCLICallHost(t, server.URL)
	oldJSON, oldTimeout := callJSON, callTimeout
	callJSON, callTimeout = true, 300*time.Millisecond
	t.Cleanup(func() { callJSON, callTimeout = oldJSON, oldTimeout })
	output, err := captureCLIResponse(t, func() error { return runCall(newCLICallCommand(t.Context()), []string{"echo"}) })
	if output != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body read exceeded the deadline or returned partial output: %q %v", output, err)
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe the cancellation")
	}
}

func TestCLICallRejectsInvalidResponseSettings(t *testing.T) {
	oldTimeout, oldLimit := callTimeout, callResponseMaxBytes
	t.Cleanup(func() { callTimeout, callResponseMaxBytes = oldTimeout, oldLimit })
	cmd := newCLICallCommand(t.Context())
	callTimeout = 0
	if err := runCall(cmd, []string{"echo"}); err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("invalid timeout: %v", err)
	}
	callTimeout, callResponseMaxBytes = oldTimeout, 0
	if err := runCall(cmd, []string{"echo"}); err == nil || !strings.Contains(err.Error(), "--max-response-bytes") {
		t.Fatalf("invalid response budget: %v", err)
	}
}

func TestCLIJSONCallKeepsRequestedAPI(t *testing.T) {
	router, requested, unexpected := newCLIAPIAuthorityRouter(t)
	server := httptest.NewServer(router)
	defer server.Close()
	oldHost, oldJSON, oldSelect := callHost, callJSON, callSelect
	callHost, callJSON, callSelect = server.URL, true, ""
	defer func() { callHost, callJSON, callSelect = oldHost, oldJSON, oldSelect }()
	for _, params := range [][]string{
		nil, {"$api=unexpected"}, {`$api="unexpected"`}, {"$api=null"},
		{"$api=123"}, {"$api=requested", "$api=unexpected"},
	} {
		t.Run(strings.Join(params, "/"), func(t *testing.T) {
			before := requested.Load()
			beforeUnexpected := unexpected.Load()
			output, err := captureCLIResponse(t, func() error {
				return runCall(newCLICallCommand(t.Context()), append([]string{"requested"}, params...))
			})
			var response struct {
				Data int64 `json:"data"`
			}
			decodeErr := json.Unmarshal([]byte(output), &response)
			if err != nil || decodeErr != nil || response.Data != 42 || requested.Load() != before+1 || unexpected.Load() != beforeUnexpected {
				t.Errorf("CLI request changed API identity: err=%v decode=%v output=%q requested=%d unexpected=%d", err, decodeErr, output, requested.Load(), unexpected.Load())
			}
		})
	}
}

func newCLIAPIAuthorityRouter(t *testing.T) (*api.Router, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	router := api.NewRouterWithOptions(api.RouterOptions{})
	requested, unexpected := new(atomic.Int64), new(atomic.Int64)
	for _, route := range []struct {
		name  string
		id    int
		value int64
		calls *atomic.Int64
	}{
		{"requested", 1, 42, requested},
		{"unexpected", 2, 99, unexpected},
	} {
		if err := router.Schema.RegisterAPI(&schema.API{ID: route.id, Name: route.name, ReturnType: "Int"}); err != nil {
			t.Fatal(err)
		}
		if err := router.Registry.Register(route.name, route.id); err != nil {
			t.Fatal(err)
		}
		router.Handle(route.name, func(_ context.Context, request *api.Request) error {
			route.calls.Add(1)
			request.Buf.B = codec.AppendSvarint(request.Buf.B, route.value)
			return nil
		})
	}
	return router, requested, unexpected
}

func TestCLIJSONAPIAuthorityPreservesNestedJSONAndSelection(t *testing.T) {
	const payload = `{"$api":"nested","items":[{"$api":"inside"}],"id":9223372036854775807}`
	received := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":42}`)
	}))
	defer server.Close()
	oldHost, oldSelect := callHost, callSelect
	callHost, callSelect = server.URL, "id,name"
	defer func() { callHost, callSelect = oldHost, oldSelect }()
	output, err := captureCLIResponse(t, func() error {
		return callJSONMode(t.Context(), "requested", []string{"$api=unexpected", "payload=" + payload})
	})
	if err != nil || output != "{\"data\":42}\n" {
		t.Fatalf("JSON call failed: %v %q", err, output)
	}
	body := <-received
	if string(body["$api"]) != `"requested"` || string(body["$select"]) != `"id,name"` || string(body["payload"]) != payload {
		t.Fatalf("routing metadata or nested JSON was changed: %s", body)
	}
}

func TestCLIRejectsInvalidBinaryResponseBeforePrinting(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{Name: "Measured", Fields: []schema.Field{{ID: 1, Name: "value", Type: schema.FieldFloat}}})
	invalid := binary.LittleEndian.AppendUint64(nil, 0x7ff0000000000000)
	row := append(append([]byte{0, 1}, invalid...), 0)
	column := append(append([]byte{1, 0, 1}, invalid...), 0)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = previous; writer.Close(); reader.Close() })
	for _, item := range []struct {
		meta schema.API
		data []byte
	}{
		{schema.API{ReturnType: "Float"}, invalid},
		{schema.API{ReturnType: "Float", ReturnList: true}, append([]byte{1}, invalid...)},
		{schema.API{ReturnType: "Float"}, nil},
		{schema.API{ReturnType: "Measured"}, row},
		{schema.API{ReturnType: "Measured", ReturnList: true}, column},
		{schema.API{ReturnType: "Measured", ReturnList: true, Paginated: true}, append(column, 2, 2, 20)},
	} {
		if err := decodeBinaryResponse(item.data, &item.meta, s); err == nil {
			t.Errorf("accepted invalid response: %+v", item.meta)
		}
	}
	writer.Close()
	os.Stdout = previous
	if data, err := io.ReadAll(reader); err != nil || len(data) != 0 {
		t.Fatalf("printed partial response: %q, %v", data, err)
	}
}

func TestCLIDecodesEnumAndScalarListResponses(t *testing.T) {
	s := schema.New()
	s.Enums["State"] = &schema.Enum{Name: "State"}
	for _, list := range []bool{false, true} {
		data, err := codec.AppendString(nil, "OPEN")
		if err != nil {
			t.Fatal(err)
		}
		want := `"OPEN"`
		if list {
			data, want = append([]byte{1}, data...), `["OPEN"]`
		}
		meta := &schema.API{ReturnType: "State", ReturnList: list}
		got, err := decodeBinaryScalarResponse(data, meta, s)
		if err != nil || string(got) != want {
			t.Fatalf("response = %q, %v; want %s", got, err, want)
		}
	}
}

func cliNumberTokens(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../../pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ JSONNumberTokens []string }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.JSONNumberTokens) == 0 {
		t.Fatal("missing numeric corpus")
	}
	return fixture.JSONNumberTokens
}

func TestCLIJSONModePreservesNumericTokens(t *testing.T) {
	received := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body["value"]
		w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	previous := callHost
	callHost = server.URL
	t.Cleanup(func() { callHost = previous })
	for _, token := range cliNumberTokens(t) {
		for _, value := range []string{token, `{"n":` + token + `}`, `[` + token + `,null]`} {
			if err := callJSONMode(t.Context(), "echoJSON", []string{"value=" + value}); err != nil {
				t.Fatal(err)
			}
			if actual := string(<-received); actual != value {
				t.Errorf("%s became %s", value, actual)
			}
		}
	}
}

func TestCLIInferenceDoesNotTruncateText(t *testing.T) {
	for _, value := range []string{"123abc", "1.2ms", "01", "+1", "", "hello", "NaN", "Infinity"} {
		if actual := inferType(value); actual != value {
			t.Errorf("%q became %#v", value, actual)
		}
	}
	for _, value := range []string{`true`, `false`, `null`, `"123"`} {
		encoded, err := json.Marshal(inferType(value))
		if err != nil || string(encoded) != value {
			t.Errorf("%s became %s: %v", value, encoded, err)
		}
	}
}

func TestCLIBinaryJSONPreservesNumericTokens(t *testing.T) {
	for _, token := range cliNumberTokens(t) {
		for _, list := range []bool{false, true} {
			raw := `{"n":` + token + `}`
			if list {
				raw = `[` + raw + `,null]`
			}
			value, err := parseBinaryCLIValue(raw, schema.Param{Type: schema.FieldJSON, IsList: list})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != raw {
				t.Errorf("%s became %s: %v", raw, encoded, err)
			}
			if _, err := api.EncodeBinaryRequest(1, nil, map[string]any{"value": value},
				[]api.ParamMeta{{Name: "value", FieldID: 1, Type: "JSON", IsList: list}}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCLIBinaryIntegerArraysUseExactValues(t *testing.T) {
	for _, fieldType := range []schema.FieldType{schema.FieldInt, schema.FieldDuration} {
		param := schema.Param{Type: fieldType, IsList: true}
		value, err := parseBinaryCLIValue(`[9007199254740993,9223372036854775807,-9223372036854775808]`, param)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := api.EncodeBinaryRequest(1, nil, map[string]any{"value": value},
			[]api.ParamMeta{{Name: "value", FieldID: 1, Type: fieldType.String(), IsList: true}})
		if err != nil {
			t.Fatal(err)
		}
		var expected codec.Encoder
		expected.WriteFieldHeader(1)
		expected.WriteArrayHeader(3)
		for _, integer := range []int64{9007199254740993, math.MaxInt64, math.MinInt64} {
			expected.WriteInt(integer)
		}
		expected.WriteEnd()
		if !bytes.Equal(wire, append([]byte{1, 0}, expected.Bytes()...)) {
			t.Errorf("wrong %s bytes: %x", fieldType, wire)
		}
		for _, invalid := range []string{`[9223372036854775808]`, `[-9223372036854775809]`, `[1.0]`, `[1e0]`, `["1"]`, `[null]`, `null`, `[1] true`} {
			if _, err := parseBinaryCLIValue(invalid, param); err == nil {
				t.Errorf("accepted %s %s", fieldType, invalid)
			}
		}
	}
}

func TestCLIBinaryFloatRejectsUnderflowWithoutNarrowingSpecialValues(t *testing.T) {
	for _, token := range []string{"1e-400", "-1e-400", "2.4703282292062327e-324", "0x1p-1075"} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat}); err == nil {
			t.Errorf("accepted underflow %s", token)
		}
	}
	for _, token := range []string{"-0", "-0.0e-99999", "-0x0p-99999", "-0_0.0"} {
		value, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat})
		if err != nil || math.Float64bits(value.(float64)) != 1<<63 {
			t.Errorf("lost negative zero %s: %v", token, err)
		}
	}
	for _, token := range []string{"NaN", "+Inf", "-Inf"} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat}); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{`[1e-400]`, `["1"]`, `[null]`} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat, IsList: true}); err == nil {
			t.Errorf("accepted %s", token)
		}
	}
}

func TestCLIBinaryScalarListsAndNullability(t *testing.T) {
	for _, item := range []struct {
		field schema.FieldType
		raw   string
		want  any
	}{
		{schema.FieldInt, `[]`, []any{}},
		{schema.FieldBool, `[true,false]`, []any{true, false}},
		{schema.FieldFloat, `[1.25,5e-324]`, []any{1.25, math.SmallestNonzeroFloat64}},
		{schema.FieldString, `["", "東京"]`, []any{"", "東京"}},
		{schema.FieldBytes, `["AQI=",""]`, []any{[]byte{1, 2}, []byte{}}},
		{schema.FieldEnum, `["OPEN"]`, []any{"OPEN"}},
		{schema.FieldDateTime, `["2026-09-15T00:00:00Z"]`, []any{"2026-09-15T00:00:00Z"}},
		{schema.FieldUUID, `["01234567-89ab-cdef-0123-456789abcdef"]`, []any{"01234567-89ab-cdef-0123-456789abcdef"}},
		{schema.FieldDecimal, `["1.2300"]`, []any{"1.2300"}},
	} {
		value, err := parseBinaryCLIValue(item.raw, schema.Param{Type: item.field, IsList: true})
		if err != nil || !reflect.DeepEqual(value, item.want) {
			t.Errorf("%s = %#v: %v", item.raw, value, err)
		}
		for _, list := range []bool{false, true} {
			value, err = parseBinaryCLIValue(" null ", schema.Param{Type: item.field, IsList: list, Nullable: true})
			if err != nil || value != nil {
				t.Errorf("nullable %s = %#v: %v", item.field, value, err)
			}
		}
	}
	for _, item := range []struct {
		field schema.FieldType
		raw   string
		list  bool
	}{
		{schema.FieldInt, `wrong`, false}, {schema.FieldFloat, `wrong`, false},
		{schema.FieldJSON, `{"n":`, false}, {schema.FieldInt, `1`, true},
		{schema.FieldBool, `[1]`, true}, {schema.FieldString, `[1]`, true},
		{schema.FieldBytes, `["***"]`, true}, {schema.FieldModel, `["unsupported"]`, true},
		{schema.FieldModel, `unsupported`, false},
	} {
		if _, err := parseBinaryCLIValue(item.raw, schema.Param{Type: item.field, IsList: item.list}); err == nil {
			t.Errorf("accepted %s %s", item.field, item.raw)
		}
	}
	if value, err := parseBinaryCLIValue("true", schema.Param{Type: schema.FieldBool}); err != nil || value != true {
		t.Fatal(value, err)
	}
	if value, err := parseBinaryCLIValue("null", schema.Param{Type: schema.FieldJSON}); err != nil || string(value.(json.RawMessage)) != "null" {
		t.Fatal(value, err)
	}
}

func TestCLIParamSplitsOnlyTheFirstEquals(t *testing.T) {
	for _, item := range [][3]string{{"name", "name", ""}, {"value=a=b", "value", "a=b"}, {"name=", "name", ""}} {
		key, value := parseParam(item[0])
		if key != item[1] || value != item[2] {
			t.Fatal(key, value)
		}
	}
}

func TestCLINullableRootResponses(t *testing.T) {
	s := schema.New()
	if err := s.RegisterModel(&schema.Model{Name: "Item", Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}}}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name, typ string
		list      bool
		data      []byte
		want      string
		fail      bool
	}{
		{"null int", "Int", false, []byte{0}, "null\n", false},
		{"present int", "Int", false, []byte{1, 84}, "42\n", false},
		{"null model", "Item", false, []byte{0}, "null\n", false},
		{"present model", "Item", false, []byte{1, 0, 1, 84, 0}, "{\"id\":42}\n", false},
		{"null list", "Int", true, []byte{0}, "null\n", false},
		{"present list", "Int", true, []byte{1, 2, 84, 86}, "[42,43]\n", false},
		{"missing flag", "Int", false, nil, "", true},
		{"invalid flag", "Int", false, []byte{2}, "", true},
		{"trailing null", "Int", false, []byte{0, 84}, "", true},
		{"missing value", "Int", false, []byte{1}, "", true},
		{"invalid value", "Int", false, []byte{1, 128}, "", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			output, err := captureCLIResponse(t, func() error {
				return decodeBinaryResponse(item.data, &schema.API{ReturnType: item.typ, ReturnNullable: true, ReturnList: item.list}, s)
			})
			if (err != nil) != item.fail || output != item.want {
				t.Fatalf("output=%q error=%v want=%q failure=%t", output, err, item.want, item.fail)
			}
		})
	}
}

func captureCLIResponse(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = previous; writer.Close(); reader.Close() }()
	runErr := run()
	writer.Close()
	os.Stdout = previous
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output), runErr
}

func TestCLICallRejectsIncompleteHTTPBody(t *testing.T) {
	for _, mode := range []string{"json", "binary"} {
		t.Run(mode, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			writeCLIResponseReadSchema(t)
			payload, want := []byte(`{"data":42}`), "{\"data\":42}\n"
			if mode == "binary" {
				payload, want = codec.AppendSvarint(nil, 42), "42\n"
			}
			for _, sample := range []struct {
				name  string
				extra int
				short bool
			}{
				{name: "valid"}, {name: "short_framing", extra: 1}, {name: "short_body", short: true},
			} {
				t.Run(sample.name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/luvia" || (r.Header.Get("X-Luxo-Mode") == "binary") != (mode == "binary") {
							t.Error("request used the wrong endpoint or wire mode")
						}
						if mode == "binary" {
							w.Header().Set("X-Luxo-Mode", "binary")
						}
						w.Header().Set("Content-Length", strconv.Itoa(len(payload)+sample.extra))
						body := payload
						if sample.short {
							body = body[:len(body)/2]
						}
						_, _ = w.Write(body)
					}))
					defer server.Close()
					useCLICallHost(t, server.URL)
					output, err := captureCLIResponse(t, func() error {
						if mode == "binary" {
							return callBinaryMode(t.Context(), "echo", nil)
						}
						return callJSONMode(t.Context(), "echo", nil)
					})
					if sample.extra != 0 || sample.short {
						if !errors.Is(err, io.ErrUnexpectedEOF) || output != "" {
							t.Fatalf("incomplete response printed %q or lost read error: %v", output, err)
						}
					} else if err != nil || output != want {
						t.Fatalf("valid response changed: output=%q err=%v want=%q", output, err, want)
					}
				})
			}
		})
	}
}

func writeCLIResponseReadSchema(t testing.TB) {
	t.Helper()
	runtime := schema.New()
	if err := runtime.RegisterAPI(&schema.API{ID: 1, Name: "echo", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("luxo.schema.json", data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCLICallReportsJSONHTTPFailure(t *testing.T) {
	const body = `{"data":42}`
	for _, mode := range []string{"json", "binary_fallback"} {
		t.Run(mode, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			writeCLIResponseReadSchema(t)
			for _, status := range []int{200, 299, 300, 404, 500} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, body)
					}))
					defer server.Close()
					useCLICallHost(t, server.URL)
					output, err := captureCLIResponse(t, func() error {
						if mode == "binary_fallback" {
							return callBinaryMode(t.Context(), "echo", nil)
						}
						return callJSONMode(t.Context(), "echo", nil)
					})
					if (err != nil) != (status >= 300) || output != body+"\n" {
						t.Fatalf("status %d: output=%q error=%v", status, output, err)
					}
					if err != nil && !strings.Contains(err.Error(), strconv.Itoa(status)) {
						t.Fatalf("HTTP failure lost its status: %v", err)
					}
				})
			}
		})
	}
}

func TestCLICallPreservesBinaryHTTPError(t *testing.T) {
	enterCacheGenerationScratch(t)
	writeCLIResponseReadSchema(t)
	body := codec.AppendSvarint([]byte{1}, 404)
	for index, value := range []string{"NotFound", "missing"} {
		var err error
		body, err = codec.AppendString(append(body, byte(index+2)), value)
		if err != nil {
			t.Fatal(err)
		}
	}
	body = append(body, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Luxo-Mode", "binary")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	useCLICallHost(t, server.URL)
	output, err := captureCLIResponse(t, func() error { return callBinaryMode(t.Context(), "echo", nil) })
	var remote *api.BinaryError
	if !errors.As(err, &remote) || remote.Code != 404 || remote.Name != "NotFound" || remote.Message != "missing" || output != "" {
		t.Fatalf("binary error changed: output=%q err=%v", output, err)
	}
}

func useCLICallHost(t testing.TB, host string) {
	t.Helper()
	previousHost, previousSelect := callHost, callSelect
	callHost, callSelect = host, ""
	t.Cleanup(func() { callHost, callSelect = previousHost, previousSelect })
}

func writeCLIRequestBoundarySchema(t *testing.T) *schema.Schema {
	t.Helper()
	runtime := schema.New()
	if err := runtime.RegisterModel(&schema.Model{Name: "Result", Fields: []schema.Field{{ID: 17, Name: "id", Type: schema.FieldInt}}}); err != nil {
		t.Fatal(err)
	}
	for _, definition := range []*schema.API{
		{ID: 19, Name: "read", ReturnType: "Result", Params: []schema.Param{{ID: 9, Name: "value", Type: schema.FieldInt}}},
		{ID: 2, Name: "scalar", ReturnType: "Int"},
		{ID: 3, Name: "text", ReturnType: "Int", Params: []schema.Param{{ID: 1, Name: "text", Type: schema.FieldString}}},
	} {
		if err := runtime.RegisterAPI(definition); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("luxo.schema.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestCLIBinaryRequestReachesRouterWithStableIDs(t *testing.T) {
	enterCacheGenerationScratch(t)
	runtime := writeCLIRequestBoundarySchema(t)
	router := api.NewRouterWithOptions(api.RouterOptions{})
	router.Schema = runtime
	if err := router.Registry.RegisterSchemaAPIs(runtime); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	router.Handle("read", func(_ context.Context, request *api.Request) error {
		value, err := request.ParamInt("value")
		if err != nil {
			return err
		}
		if !request.BinaryMode || request.API != "read" || len(request.Select) != 1 || request.Select[0].Name != "id" || value != 9007199254740993 {
			return errors.New("binary CLI lost wire identity, selection or integer precision")
		}
		calls.Add(1)
		request.Buf.B = codec.AppendSvarint(append(request.Buf.B, 0, 17), value)
		request.Buf.B = append(request.Buf.B, 0)
		return nil
	})
	server := httptest.NewServer(router)
	defer server.Close()
	useCLICallHost(t, server.URL)
	previousJSON := callJSON
	callJSON, callSelect = false, "id"
	t.Cleanup(func() { callJSON = previousJSON })
	output, err := captureCLIResponse(t, func() error {
		return runCall(newCLICallCommand(t.Context()), []string{"read", "value=9007199254740993"})
	})
	if err != nil || output != "{\"id\":9007199254740993}\n" || calls.Load() != 1 {
		t.Fatalf("binary Router result: output=%q err=%v calls=%d", output, err, calls.Load())
	}
}

type cliBoundaryTransport struct {
	calls int
	err   error
}

func (transport *cliBoundaryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	if err := request.Body.Close(); err != nil {
		return nil, err
	}
	return nil, transport.err
}

func TestCLIBinaryInvalidRequestsNeverReachTransport(t *testing.T) {
	enterCacheGenerationScratch(t)
	writeCLIRequestBoundarySchema(t)
	transport := &cliBoundaryTransport{err: errors.New("unexpected network request")}
	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previousClient })
	useCLICallHost(t, "http://cli.invalid")
	for _, sample := range []struct {
		name, api, selectText, diagnostic string
		params                            []string
	}{
		{name: "unknown_api", api: "missing", diagnostic: "unknown API"},
		{name: "unknown_param", api: "read", params: []string{"extra=1"}, diagnostic: "unknown param"},
		{name: "invalid_integer", api: "read", params: []string{"value=1junk"}, diagnostic: "param value"},
		{name: "scalar_selection", api: "scalar", selectText: "id", diagnostic: "does not return a selectable model"},
		{name: "malformed_selection", api: "read", selectText: "id {", diagnostic: "parse --select"},
		{name: "unknown_field", api: "read", selectText: "missing", diagnostic: "missing"},
		{name: "invalid_utf8", api: "text", params: []string{"text=" + string([]byte{0xff})}, diagnostic: "encode binary request"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			callSelect = sample.selectText
			output, err := captureCLIResponse(t, func() error { return callBinaryMode(t.Context(), sample.api, sample.params) })
			if err == nil || !strings.Contains(err.Error(), sample.diagnostic) || output != "" || transport.calls != 0 {
				t.Fatalf("invalid input reached network or lost error: %q %v calls=%d", output, err, transport.calls)
			}
		})
	}
}

func TestCLICallPreservesTransportFailure(t *testing.T) {
	enterCacheGenerationScratch(t)
	writeCLIRequestBoundarySchema(t)
	cause := errors.New("owned transport failure")
	transport := &cliBoundaryTransport{err: cause}
	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previousClient })
	useCLICallHost(t, "http://cli.invalid")
	for _, mode := range []string{"json", "binary"} {
		t.Run(mode, func(t *testing.T) {
			before := transport.calls
			invoke := callJSONMode
			if mode == "binary" {
				invoke = callBinaryMode
			}
			output, err := captureCLIResponse(t, func() error { return invoke(t.Context(), "scalar", nil) })
			if !errors.Is(err, cause) || output != "" || transport.calls != before+1 {
				t.Fatalf("transport failure changed: %q %v calls=%d", output, err, transport.calls)
			}
		})
	}
	callHost = "http://["
	output, err := captureCLIResponse(t, func() error { return callBinaryMode(t.Context(), "scalar", nil) })
	if err == nil || output != "" || transport.calls != 2 {
		t.Fatalf("malformed host reached transport: %q %v calls=%d", output, err, transport.calls)
	}
}

func TestCLIBinaryCallReportsSchemaFailure(t *testing.T) {
	enterCacheGenerationScratch(t)
	output, err := captureCLIResponse(t, func() error { return callBinaryMode(t.Context(), "read", nil) })
	if !errors.Is(err, os.ErrNotExist) || output != "" {
		t.Fatalf("missing schema changed: %q %v", output, err)
	}
}

func TestCLIBinaryCallRejectsMalformedRemoteError(t *testing.T) {
	enterCacheGenerationScratch(t)
	writeCLIRequestBoundarySchema(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Luxo-Mode", "binary")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte{0x80})
	}))
	defer server.Close()
	useCLICallHost(t, server.URL)
	output, err := captureCLIResponse(t, func() error { return callBinaryMode(t.Context(), "scalar", nil) })
	var remote *api.BinaryError
	if err == nil || errors.As(err, &remote) || output != "" {
		t.Fatalf("malformed remote error was accepted or printed: %q %v", output, err)
	}
}

func TestCLIDiagnosticUntypedResponses(t *testing.T) {
	for _, sample := range []struct {
		name string
		body []byte
		want string
	}{
		{"empty", nil, "(empty response)\n"},
		{"short", []byte{1, 2, 3}, "  hex: 01 02 03 \n"},
		{"wrapped", bytes.Repeat([]byte{0x2a}, 17), "  hex: " + strings.Repeat("2a ", 16) + "\n       2a \n"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			output, err := captureCLIResponse(t, func() error { return decodeBinaryResponse(sample.body, &schema.API{}, schema.New()) })
			if err != nil || output != sample.want {
				t.Fatalf("untyped response changed: %q %v", output, err)
			}
		})
	}
}
