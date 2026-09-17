package rpc

import (
	"bytes"
	"testing"
)

func FuzzRequestEnvelope(f *testing.F) {
	f.Add(encodeRequestEnvelope(requestKindCall, "", []byte{1, 0, 0}))
	f.Add(encodeRequestEnvelope(requestKindStream, "token", []byte{1, 0, 0}))
	f.Add(encodeTracedRequestEnvelope(requestKindCall, "token", "trace", "a.b", true, []byte{1, 0, 0}))
	f.Add([]byte{1, 1, 0, 1, 't', 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32<<10 {
			return
		}
		envelope, err := decodeRequestEnvelope(data)
		if err != nil {
			return
		}
		if len(envelope.body) == 0 {
			t.Fatal("empty body accepted")
		}
		var encoded []byte
		if envelope.traceID == "" {
			encoded = encodeRequestEnvelope(envelope.kind, envelope.token, envelope.body)
		} else {
			encoded = encodeTracedRequestEnvelope(envelope.kind, envelope.token, envelope.traceID, envelope.fieldPath, envelope.databaseDetails, envelope.body)
		}
		roundTrip, err := decodeRequestEnvelope(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.kind != roundTrip.kind || envelope.token != roundTrip.token || envelope.traceID != roundTrip.traceID || envelope.fieldPath != roundTrip.fieldPath || envelope.databaseDetails != roundTrip.databaseDetails || !bytes.Equal(envelope.body, roundTrip.body) {
			t.Fatal("request metadata changed during canonical encoding")
		}
	})
}
