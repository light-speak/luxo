package lockfile

import (
	"encoding/json"
	"encoding/json/jsontext"
	"reflect"
	"testing"
)

func FuzzReadLock(f *testing.F) {
	for _, raw := range []string{completeLockJSON, `{}`, `{"version":1}`, `{"version":1,"version":2}`, `null`, `{"apis":{"a":{"id":1}}}`} {
		f.Add([]byte(raw))
	}
	f.Add([]byte(`{"apis":{"a":{"id":1,"contract":true,"params":{"first":1,"second":2}}},"default_api_params":{"a":[2,1]}}`))
	f.Add([]byte(`{"default_api_params":{"a":[1],"\u0061":[2]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		lf, err := readLock(raw)
		if err != nil {
			if lf != nil {
				t.Fatal("failed read published partial metadata")
			}
			return
		}
		if !jsontext.Value(raw).IsValid() {
			t.Fatal("typed reader accepted input rejected by strict standard tokenizer")
		}
		if err := lf.Validate(); err != nil {
			return
		}
		encoded, err := json.Marshal(lf)
		if err != nil {
			t.Fatal(err)
		}
		again, err := readLock(encoded)
		if err != nil {
			t.Fatal(err)
		}
		// Empty optional collections may be omitted by the existing writer.
		reencoded, err := json.Marshal(again)
		if err != nil || !reflect.DeepEqual(encoded, reencoded) {
			t.Fatalf("unstable lock roundtrip: %s / %s / %v", encoded, reencoded, err)
		}
	})
}
