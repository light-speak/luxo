package rpc

import (
	"bytes"
	"encoding/json/jsontext"
	"sync"
	"testing"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func TestRPCJSONReaderDoesNotReusePermissiveOptions(t *testing.T) {
	for range 20 {
		reader := jsonutil.BorrowReader([]byte(`{"x":1,"x":2}`), jsontext.AllowDuplicateNames(true))
		if _, err := reader.ReadValue(); err != nil {
			t.Fatal(err)
		}
		reader.Release()
		if err := validateRPCErrorText([]byte(`{"x":null,"x":null}`)); err == nil {
			t.Fatal("strict override validation inherited permissive options")
		}
		if err := validateRPCErrorText([]byte(`{"x":"\ud800"}`)); err == nil {
			t.Fatal("strict override validation accepted an invalid surrogate")
		}
	}
}

func TestRPCErrorReaderReentrantWriter(t *testing.T) {
	source := rpcNativeData{raw: `{"x":"outer","y":"outer"}`, text: func(dst []byte, field string) ([]byte, bool) {
		_, err := encodeApplicationError(luxerrors.New("Inner", 422, "error.inner").WithData(rpcPrecisionData{}))
		if err != nil {
			t.Error(err)
		}
		return append(dst, field...), true
	}}
	raw, text, err := encodeRPCErrorData(source)
	if err != nil || string(raw) != source.raw || string(text) != `{"x":"x","y":"y"}` {
		t.Fatalf("nested reader corrupted outer state: %s / %s / %v", raw, text, err)
	}
}

func TestRPCErrorDataConcurrentTextOwnership(t *testing.T) {
	data := ownRPCErrorData([]byte(rpcPrecisionJSON), []byte(`{"label":"精度 café"}`))
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 100 {
				for _, field := range []string{"label", "number", "items", "absent"} {
					got, found := data.AppendText([]byte("prefix"), field)
					want, wantFound := (rpcPrecisionData{}).AppendText([]byte("prefix"), field)
					if found != wantFound || !bytes.Equal(got, want) {
						t.Errorf("concurrent field %s corrupted: %q", field, got)
						return
					}
				}
			}
		})
	}
	group.Wait()
	if string(data.raw) != rpcPrecisionJSON || string(data.text) != `{"label":"精度 café"}` {
		t.Fatal("pooled reader mutated owned data")
	}
}

func FuzzRPCErrorReaderReuse(f *testing.F) {
	f.Add([]byte(rpcPrecisionJSON), []byte(`{"label":"精度 café"}`), "label")
	f.Add([]byte(`{"n":1,"n":1e9999}`), []byte(`{}`), "n")
	f.Add([]byte(`{"\u006e":"\ud800"}`), []byte(`{}`), "n")
	f.Add([]byte(`{"list":[{"n":-0},null]}`), []byte(`{"list":null}`), "list")
	f.Fuzz(func(t *testing.T, raw, text []byte, field string) {
		if len(raw)+len(text)+len(field) > 65536 {
			return
		}
		beforeErr := validateRPCErrorDataBeforeReaderReuse(raw, text)
		afterErr := validateRPCErrorData(raw, text)
		// The frozen baseline permitted lone surrogate escapes. That is now an
		// intentional rejection, not a reader-reuse regression.
		if raw != nil && !jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true)) {
			if afterErr == nil {
				t.Fatal("accepted invalid Unicode or JSON")
			}
			return
		}
		if (beforeErr == nil) != (afterErr == nil) {
			t.Fatalf("validation changed: before=%v after=%v", beforeErr, afterErr)
		}
		if beforeErr != nil {
			return
		}
		before := ownRPCErrorDataBeforeReaderReuse(raw, text)
		after := ownRPCErrorData(raw, text)
		want, wantFound := before.AppendText([]byte("prefix"), field)
		got, gotFound := after.AppendText([]byte("prefix"), field)
		if gotFound != wantFound || !bytes.Equal(got, want) {
			t.Fatalf("text changed: %q/%t -> %q/%t", want, wantFound, got, gotFound)
		}
	})
}
