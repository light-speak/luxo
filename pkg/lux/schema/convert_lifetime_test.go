package schema

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

type schemaLifetimeResult struct {
	output []byte
	err    error
}

// Keep the allocation and conversion in a separate frame so stack liveness
// cannot retain the source while the caller checks its weak pointer.
//
//go:noinline
func schemaLifetimeConversion(t *testing.T, s *Schema, mode string) (weak.Pointer[byte], schemaLifetimeResult) {
	t.Helper()
	large := `"` + strings.Repeat("x", 1<<20) + `"`
	var raw []byte
	var result schemaLifetimeResult
	if strings.HasPrefix(mode, "input") {
		body := `{"payload":` + large + `}`
		switch mode {
		case "input duplicate":
			body = `{"child":{"unknown":` + large + `},"child":null}`
		case "input deep business":
			child := `{"value":null}`
			for range 62 {
				child = `{"child":` + child + `}`
			}
			body = `{"payload":` + large + `,"child":` + child + `}`
		case "input malformed":
			body += "x"
		case "input unknown":
			body = `{"unknown":` + large + `}`
		case "input depth":
			body = `{"payload":` + strings.Repeat("[", 129) + large + strings.Repeat("]", 129) + `,"payload":null}`
		}
		raw = bytes.Clone([]byte(body))
		result.output, result.err = s.JSONToBinary("Node", raw)
	} else {
		e := codec.NewEncoder(nil)
		e.WriteVarint(0)
		e.WriteFieldHeader(4)
		e.WritePresent()
		e.WriteBytes([]byte(large))
		e.WriteEnd()
		raw = bytes.Clone(e.Bytes())
		if strings.Contains(mode, "malformed") {
			raw = append(raw, 1)
		}
		if strings.Contains(mode, "column") || strings.Contains(mode, "page") {
			raw = append([]byte{1}, raw...)
		}
		if strings.Contains(mode, "page") {
			for range 3 {
				raw = codec.AppendSvarint(raw, 1)
			}
		}
		switch {
		case strings.Contains(mode, "column"):
			result.output, result.err = BinaryListToJSON(nil, raw, s.Models["Node"], s)
		case strings.Contains(mode, "page"):
			result.output, result.err = BinaryPaginatedListToJSON(nil, raw, s.Models["Node"], s)
		default:
			result.output, result.err = BinaryToJSON(nil, raw, s.Models["Node"], s)
		}
	}
	pointer := weak.Make(&raw[0])
	runtime.KeepAlive(raw)
	return pointer, result
}

func TestSchemaConversionsReleaseBorrowedInput(t *testing.T) {
	s := jsonDepthSchema(false)
	for _, mode := range []string{"input success", "input duplicate", "input deep business", "input malformed", "input unknown", "input depth", "output row", "output column", "output page", "output row malformed", "output column malformed", "output page malformed"} {
		t.Run(mode, func(t *testing.T) {
			pointer, result := schemaLifetimeConversion(t, s, mode)
			wantError := strings.Contains(mode, "malformed") || mode == "input unknown" || mode == "input depth" || mode == "input deep business"
			if (result.err != nil) != wantError || wantError && len(result.output) != 0 {
				t.Fatal("invalid conversion control", result.err)
			}
			if mode == "input deep business" {
				var diagnostic *jsonDiagnosticError
				if !errors.As(result.err, &diagnostic) {
					t.Fatal("lifetime fixture missed the finalized diagnostic")
				}
			}
			for range 3 {
				runtime.GC()
			}
			if pointer.Value() != nil {
				t.Fatal("completed conversion retains its source allocation")
			}
			// Keeping output/error and the schema alive also checks their ownership.
			runtime.KeepAlive(result)
			runtime.KeepAlive(s)
		})
	}
}

func schemaLateErrors(t testing.TB) map[string]func([]byte) ([]byte, error) {
	t.Helper()
	s := jsonDepthSchema(false)
	large := []byte(`"` + strings.Repeat("x", 1<<20) + `"`)
	row := codec.NewEncoder(nil)
	row.WriteVarint(0)
	row.WriteFieldHeader(4)
	row.WritePresent()
	row.WriteBytes(large)
	row.WriteEnd()
	badRow := append(row.Bytes(), 1)
	badJSON := []byte(strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129))
	var w codec.ColumnarWriter
	w.SetCount(2)
	w.WriteColumnBytesPtr(4, []*[]byte{&large, &badJSON})
	column, err := w.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	page := bytes.Clone(column)
	for range 3 {
		page = codec.AppendSvarint(page, 1)
	}
	scalar := codec.NewEncoder(nil)
	scalar.WriteBytes(large)
	badScalar := append(scalar.Bytes(), 1)
	badList := append([]byte{1}, badScalar...)
	return map[string]func([]byte) ([]byte, error){
		"row":        func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, badRow, s.Models["Node"], s) },
		"column":     func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, column, s.Models["Node"], s) },
		"page":       func(dst []byte) ([]byte, error) { return BinaryPaginatedListToJSON(dst, page, s.Models["Node"], s) },
		"scalar":     func(dst []byte) ([]byte, error) { return BinaryScalarToJSON(dst, badScalar, "JSON") },
		"scalarList": func(dst []byte) ([]byte, error) { return BinaryScalarListToJSON(dst, badList, "JSON") },
	}
}

func TestBinaryJSONErrorDoesNotRetainFailedOutput(t *testing.T) {
	for name, convert := range schemaLateErrors(t) {
		for _, prefix := range [][]byte{nil, []byte("prefix")} {
			t.Run(name+string(prefix), func(t *testing.T) {
				got, err := convert(prefix)
				if err == nil || len(got) != len(prefix) || cap(got) != cap(prefix) || !bytes.Equal(got, prefix) {
					t.Fatalf("failed conversion replaced destination with discarded output: length%d capacity%d original%d: %v", len(got), cap(got), cap(prefix), err)
				}
				if len(prefix) > 0 && &got[0] != &prefix[0] {
					t.Fatal("failed conversion changed destination ownership")
				}
			})
		}
	}
}

//go:noinline
func retainedSchemaInputControl(t *testing.T) weak.Pointer[byte] {
	t.Helper()
	raw := bytes.Repeat([]byte{'x'}, 1<<20)
	pointer := weak.Make(&raw[0])
	runtime.GC()
	if pointer.Value() == nil {
		t.Fatal("weak probe failed to observe a strongly held allocation")
	}
	runtime.KeepAlive(raw)
	return pointer
}

func TestSchemaInputLifetimeProbeControl(t *testing.T) {
	pointer := retainedSchemaInputControl(t)
	for range 3 {
		runtime.GC()
	}
	if pointer.Value() != nil {
		t.Fatal("weak probe did not observe collection after the strong owner returned")
	}
}
