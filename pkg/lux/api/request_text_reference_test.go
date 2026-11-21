package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Frozen pre-check implementation for valid-input differential tests and benchmarks.
func parseRequestBeforeText(r *http.Request) (*Request, error) {
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

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return parseRawRequest(raw)
}
