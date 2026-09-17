package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"unicode/utf8"
)

// Frozen immediately before strict object parsing. Keep the old raw UTF-8 scan
// inside the measurement; the earlier pre-text baseline did not include it.
func parseRequestBeforeUnicode(r *http.Request) (*Request, error) {
	defer r.Body.Close()
	body, bp, err := readBody(r.Body)
	if err != nil {
		if bp != nil {
			putBody(bp)
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer putBody(bp)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}
	if len(body) > MaxJSONBodySize {
		return nil, fmt.Errorf("JSON body exceeds %d bytes limit", MaxJSONBodySize)
	}
	if !utf8.Valid(body) {
		return nil, errInvalidJSONUTF8
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return parseRawRequest(raw)
}

// Frozen raw-key routing used before WebSocket JSON parsing was unified.
func isStreamMsgBeforeUnicode(data []byte) bool {
	depth := 0
	inString, escape := false, false
	for i := 0; i < len(data); i++ {
		if escape {
			escape = false
			continue
		}
		ch := data[i]
		if inString {
			if ch == '\\' {
				escape = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
		case '"':
			if depth == 1 && matchStreamKeyBeforeUnicode(data, i) {
				return true
			}
			inString = true
		}
	}
	return false
}

func matchStreamKeyBeforeUnicode(data []byte, i int) bool {
	if i+1 >= len(data) || data[i+1] != '$' {
		return false
	}
	if i+6 <= len(data) && data[i+2] == 's' && data[i+3] == 'u' && data[i+4] == 'b' && data[i+5] == '"' {
		return hasColonBeforeUnicode(data, i+6)
	}
	if i+8 <= len(data) && data[i+2] == 'u' && data[i+3] == 'n' && data[i+4] == 's' && data[i+5] == 'u' && data[i+6] == 'b' && data[i+7] == '"' {
		return hasColonBeforeUnicode(data, i+8)
	}
	return false
}

func hasColonBeforeUnicode(data []byte, pos int) bool {
	for pos < len(data) {
		if data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == '\r' {
			pos++
			continue
		}
		return data[pos] == ':'
	}
	return false
}

func parseWSEnvelopeBeforeUnicode(data []byte) (map[string]json.RawMessage, bool, error) {
	if !utf8.Valid(data) {
		return nil, false, errInvalidJSONUTF8
	}
	stream := isStreamMsgBeforeUnicode(data)
	var raw map[string]json.RawMessage
	err := json.Unmarshal(data, &raw)
	return raw, stream, err
}
