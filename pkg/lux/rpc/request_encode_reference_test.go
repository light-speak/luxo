package rpc

// Frozen two-stage builders preserve an independent wire and performance oracle.
import "github.com/light-speak/luxo/pkg/lux/codec"

func encodeRequestEnvelope(kind byte, token string, body []byte) []byte {
	payload := make([]byte, 0, 2+10+len(token)+len(body))
	payload = append(payload, kind, 0)
	payload = codec.AppendVarint(payload, uint64(len(token)))
	payload = append(payload, token...)
	payload = append(payload, body...)
	return payload
}

func encodeTracedRequestEnvelope(kind byte, token, traceID, fieldPath string, databaseDetails bool, body []byte) []byte {
	payload := make([]byte, 0, 3+30+len(token)+len(traceID)+len(fieldPath)+len(body))
	payload = append(payload, kind, requestFlagTraced)
	payload = codec.AppendVarint(payload, uint64(len(token)))
	payload = append(payload, token...)
	payload = codec.AppendVarint(payload, uint64(len(traceID)))
	payload = append(payload, traceID...)
	payload = codec.AppendVarint(payload, uint64(len(fieldPath)))
	payload = append(payload, fieldPath...)
	if databaseDetails {
		payload = append(payload, 1)
	} else {
		payload = append(payload, 0)
	}
	payload = append(payload, body...)
	return payload
}

func encodeCallRequest(apiID int, fieldMask []byte, params []byte) []byte {
	// Build request payload: [API ID varint] [mask len varint] [mask] [params]
	var payload []byte
	payload = codec.AppendVarint(payload, uint64(apiID))
	payload = codec.AppendVarint(payload, uint64(len(fieldMask)))
	if len(fieldMask) > 0 {
		payload = append(payload, fieldMask...)
	}
	if len(params) > 0 {
		payload = append(payload, params...)
	} else {
		payload = append(payload, 0x00) // empty params terminator
	}
	return payload
}
