package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Request represents a parsed Luvia API request.
type Request struct {
	API    string                     // $api field
	Select []*selection.Field         // parsed $select
	Params map[string]json.RawMessage // remaining fields as raw JSON
}

// ParseRequest reads an HTTP request body and extracts $api, $select, and params.
func ParseRequest(r *http.Request) (*Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	req := &Request{
		Params: make(map[string]json.RawMessage),
	}

	// Extract $api
	apiRaw, ok := raw["$api"]
	if !ok {
		return nil, fmt.Errorf("missing $api field")
	}
	if err := json.Unmarshal(apiRaw, &req.API); err != nil {
		return nil, fmt.Errorf("$api must be a string")
	}
	if req.API == "" {
		return nil, fmt.Errorf("$api must not be empty")
	}

	// Extract $select (optional)
	if selRaw, ok := raw["$select"]; ok {
		var selStr string
		if err := json.Unmarshal(selRaw, &selStr); err != nil {
			return nil, fmt.Errorf("$select must be a string")
		}
		fields, err := selection.Parse(selStr)
		if err != nil {
			return nil, fmt.Errorf("$select: %w", err)
		}
		req.Select = fields
	}

	// Remaining fields are params
	for k, v := range raw {
		if k == "$api" || k == "$select" {
			continue
		}
		req.Params[k] = v
	}

	return req, nil
}

// ParamInt extracts an integer parameter.
func (r *Request) ParamInt(name string) (int64, error) {
	raw, ok := r.Params[name]
	if !ok {
		return 0, fmt.Errorf("missing parameter '%s'", name)
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("parameter '%s' must be an integer", name)
	}
	return v, nil
}

// ParamString extracts a string parameter.
func (r *Request) ParamString(name string) (string, error) {
	raw, ok := r.Params[name]
	if !ok {
		return "", fmt.Errorf("missing parameter '%s'", name)
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("parameter '%s' must be a string", name)
	}
	return v, nil
}

// ParamBool extracts a boolean parameter.
func (r *Request) ParamBool(name string) (bool, error) {
	raw, ok := r.Params[name]
	if !ok {
		return false, fmt.Errorf("missing parameter '%s'", name)
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("parameter '%s' must be a boolean", name)
	}
	return v, nil
}

// HasParam checks if a parameter exists.
func (r *Request) HasParam(name string) bool {
	_, ok := r.Params[name]
	return ok
}
