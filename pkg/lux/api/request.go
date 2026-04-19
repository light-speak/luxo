package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// bodyPool reuses byte buffers for reading request bodies.
var bodyPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// readBody reads the full contents of r into a pooled buffer.
// Returns the data, the pool handle (caller must return via bodyPool.Put), and any error.
func readBody(r io.Reader) ([]byte, *[]byte, error) {
	bp := bodyPool.Get().(*[]byte)
	buf := bytes.NewBuffer((*bp)[:0])
	_, err := buf.ReadFrom(r)
	if err != nil {
		return nil, bp, err
	}
	*bp = buf.Bytes()
	return *bp, bp, nil
}

// reservedKeys are protocol fields that should not appear in user params.
var reservedKeys = map[string]bool{
	"$api": true, "$select": true, "$filters": true, "$sorters": true,
	"page": true, "pageSize": true,
}

// Filter represents a single filter condition from $filters.
type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"op"`
	Value    string `json:"value"`
}

// Sorter represents a sort directive from $sorters.
type Sorter struct {
	Field string `json:"field"`
	Order string `json:"order"` // "asc" or "desc"
}

// Request represents a parsed Luvia API request.
type Request struct {
	API        string                     // $api field
	Select     []*selection.Field         // parsed $select (JSON mode)
	Params     map[string]json.RawMessage // remaining fields as raw JSON
	Buf        *ResponseBuf               // response buffer — handler writes directly here
	Filters    []Filter                   // parsed $filters
	Sorters    []Sorter                   // parsed $sorters
	Page       int                        // page number (default 1)
	PageSize   int                        // page size (default 20)
	BinaryMode bool                       // true when X-Luxo-Mode: binary
	FieldMask  []byte                     // binary field mask (binary mode)

	// Binary params — zero-allocation inline storage
	// paramSlots stores values by index (position in API param list)
	// paramNames points to the static name list from Registry (never allocated per request)
	paramSlots [16]any
	paramNames []string // shared, not owned
	paramCount int
}

// ParseRequest reads an HTTP request body and extracts $api, $select, and params.
func ParseRequest(r *http.Request) (*Request, error) {
	defer r.Body.Close()
	body, bp, err := readBody(r.Body)
	if err != nil {
		if bp != nil {
			bodyPool.Put(bp)
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer bodyPool.Put(bp)

	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(body, &raw); err != nil {
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
	if err := sonic.Unmarshal(apiRaw, &req.API); err != nil {
		return nil, fmt.Errorf("$api must be a string")
	}
	if req.API == "" {
		return nil, fmt.Errorf("$api must not be empty")
	}

	// Extract $select (optional)
	if selRaw, ok := raw["$select"]; ok {
		var selStr string
		if err := sonic.Unmarshal(selRaw, &selStr); err != nil {
			return nil, fmt.Errorf("$select must be a string")
		}
		fields, err := selection.Parse(selStr)
		if err != nil {
			return nil, fmt.Errorf("$select: %w", err)
		}
		req.Select = fields
	}

	// Extract list params (filters, sorters, pagination)
	if err := req.parseListParams(raw); err != nil {
		return nil, err
	}

	// Remaining fields are params
	for k, v := range raw {
		if reservedKeys[k] {
			continue
		}
		req.Params[k] = v
	}

	return req, nil
}

// parseListParams extracts $filters, $sorters, page, pageSize from raw request.
func (req *Request) parseListParams(raw map[string]json.RawMessage) error {
	if filtersRaw, ok := raw["$filters"]; ok {
		if err := sonic.Unmarshal(filtersRaw, &req.Filters); err != nil {
			return fmt.Errorf("$filters: %w", err)
		}
	}
	if sortersRaw, ok := raw["$sorters"]; ok {
		if err := sonic.Unmarshal(sortersRaw, &req.Sorters); err != nil {
			return fmt.Errorf("$sorters: %w", err)
		}
	}
	req.Page = 1
	req.PageSize = 20
	if pageRaw, ok := raw["page"]; ok {
		if err := sonic.Unmarshal(pageRaw, &req.Page); err != nil {
			return fmt.Errorf("page must be an integer")
		}
	}
	if psRaw, ok := raw["pageSize"]; ok {
		if err := sonic.Unmarshal(psRaw, &req.PageSize); err != nil {
			return fmt.Errorf("pageSize must be an integer")
		}
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 0 || req.PageSize > 100 {
		req.PageSize = 20
	}
	// pageSize == 0 → return all (no pagination)
	return nil
}

// findParam looks up a binary param by name from the inline slot array.
// Returns the value and true if found, nil and false otherwise.
func (r *Request) findParam(name string) (any, bool) {
	for i := 0; i < r.paramCount; i++ {
		if r.paramNames[i] == name {
			return r.paramSlots[i], true
		}
	}
	return nil, false
}

// ParamInt extracts an integer parameter. Returns 400 BadRequest on error.
// Binary mode: inline array lookup (zero alloc). JSON mode: sonic.Unmarshal.
func (r *Request) ParamInt(name string) (int64, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if iv, ok := v.(int64); ok {
				return iv, nil
			}
		}
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v int64
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an integer"})
	}
	return v, nil
}

// ParamString extracts a string parameter. Returns 400 BadRequest on error.
func (r *Request) ParamString(name string) (string, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if sv, ok := v.(string); ok {
				return sv, nil
			}
		}
		return "", errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return "", errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v string
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return "", errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a string"})
	}
	return v, nil
}

// ParamDateTime extracts a time.Time parameter from RFC3339 string.
func (r *Request) ParamDateTime(name string) (time.Time, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if sv, ok := v.(string); ok {
				t, err := time.Parse(time.RFC3339, sv)
				if err != nil {
					return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be RFC3339 format"})
				}
				return t, nil
			}
		}
		return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var s string
	if err := sonic.Unmarshal(raw, &s); err != nil {
		return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a string"})
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be RFC3339 format"})
	}
	return t, nil
}

// ParamFloat extracts a float64 parameter. Returns 400 BadRequest on error.
func (r *Request) ParamFloat(name string) (float64, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if fv, ok := v.(float64); ok {
				return fv, nil
			}
		}
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v float64
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a number"})
	}
	return v, nil
}

// ParamBool extracts a boolean parameter. Returns 400 BadRequest on error.
func (r *Request) ParamBool(name string) (bool, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if bv, ok := v.(bool); ok {
				return bv, nil
			}
		}
		return false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v bool
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a boolean"})
	}
	return v, nil
}

// ParamIntArray extracts an []int64 parameter. Returns 400 BadRequest on error.
func (r *Request) ParamIntArray(name string) ([]int64, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if iv, ok := v.([]int64); ok {
				return iv, nil
			}
		}
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v []int64
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of integers"})
	}
	return v, nil
}

// ParamStringArray extracts a []string parameter. Returns 400 BadRequest on error.
func (r *Request) ParamStringArray(name string) ([]string, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if sv, ok := v.([]string); ok {
				return sv, nil
			}
		}
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v []string
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of strings"})
	}
	return v, nil
}

// ParamJSON extracts a parameter into the target. Returns 400 BadRequest on error.
func (r *Request) ParamJSON(name string, target any) error {
	if r.paramNames != nil {
		v, ok := r.findParam(name)
		if !ok {
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
		}
		if ptr, ok := target.(*any); ok {
			*ptr = v
			return nil
		}
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "binary mode: use typed param methods"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	if err := sonic.Unmarshal(raw, target); err != nil {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid format"})
	}
	return nil
}

// SetBinaryParams configures binary mode param storage.
// Called by RPC server to set up paramSlots without allocation.
func (r *Request) SetBinaryParams(names []string, count int) {
	r.paramNames = names
	r.paramCount = count
}

// SetParamSlot sets a param value by index (binary mode).
func (r *Request) SetParamSlot(index int, value any) {
	if index >= 0 && index < 16 {
		r.paramSlots[index] = value
	}
}

// HasParam checks if a parameter exists (supports both JSON and binary mode).
func (r *Request) HasParam(name string) bool {
	if r.paramNames != nil {
		_, ok := r.findParam(name)
		return ok
	}
	_, ok := r.Params[name]
	return ok
}
