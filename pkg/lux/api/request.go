package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

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

// putBody returns a body buffer to the pool, discarding oversized buffers
// to prevent a single large request from permanently inflating pool memory.
func putBody(bp *[]byte) {
	if cap(*bp) > 1<<20 { // >1MB: discard, don't pollute pool
		return
	}
	bodyPool.Put(bp)
}

// readBody reads the full contents of r into a pooled buffer.
// Returns the data, the pool handle (caller must return via bodyPool.Put), and any error.
// MaxRequestBodySize limits binary request body to 100MB to prevent OOM.
const MaxRequestBodySize = 100 * 1024 * 1024

func readBody(r io.Reader) ([]byte, *[]byte, error) {
	bp := bodyPool.Get().(*[]byte)
	buf := bytes.NewBuffer((*bp)[:0])
	n, err := buf.ReadFrom(io.LimitReader(r, MaxRequestBodySize+1))
	if err != nil {
		return nil, bp, err
	}
	if n > MaxRequestBodySize {
		return nil, bp, fmt.Errorf("request body exceeds %d bytes limit", MaxRequestBodySize)
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

// UnmarshalJSON accepts JSON scalar filter values while keeping the internal
// string representation consumed by typed FilterOp implementations.
func (f *Filter) UnmarshalJSON(data []byte) error {
	var raw struct {
		Field    string          `json:"field"`
		Operator string          `json:"op"`
		Value    json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value := bytes.TrimSpace(raw.Value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || value[0] == '{' || value[0] == '[' {
		return fmt.Errorf("filter value must be a string, number, or boolean")
	}
	if value[0] == '"' {
		if err := json.Unmarshal(value, &f.Value); err != nil {
			return err
		}
	} else {
		f.Value = string(value)
	}
	f.Field = raw.Field
	f.Operator = raw.Operator
	return nil
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
// MaxJSONBodySize limits JSON request body to 10MB (smaller than binary).
const MaxJSONBodySize = 10 * 1024 * 1024

func ParseRequest(r *http.Request) (*Request, error) {
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
		if err := json.Unmarshal(filtersRaw, &req.Filters); err != nil {
			return fmt.Errorf("$filters: %w", err)
		}
		if len(req.Filters) > 1000 {
			return fmt.Errorf("$filters exceeds limit: %d > 1000", len(req.Filters))
		}
	}
	if sortersRaw, ok := raw["$sorters"]; ok {
		if err := json.Unmarshal(sortersRaw, &req.Sorters); err != nil {
			return fmt.Errorf("$sorters: %w", err)
		}
		if len(req.Sorters) > 100 {
			return fmt.Errorf("$sorters exceeds limit: %d > 100", len(req.Sorters))
		}
	}
	req.Page = 1
	req.PageSize = 20
	if pageRaw, ok := raw["page"]; ok {
		if err := json.Unmarshal(pageRaw, &req.Page); err != nil {
			return fmt.Errorf("page must be an integer")
		}
	}
	if psRaw, ok := raw["pageSize"]; ok {
		if err := json.Unmarshal(psRaw, &req.PageSize); err != nil {
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
// Binary mode: inline array lookup (zero alloc). JSON mode: json.Unmarshal.
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
	if err := json.Unmarshal(raw, &v); err != nil {
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
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a string"})
	}
	return v, nil
}

// ParamDateTime extracts a time.Time parameter from RFC3339 string.
func (r *Request) ParamDateTime(name string) (time.Time, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			// Binary path: readBinaryParam returns int64 (unix seconds, svarint).
			if iv, ok := v.(int64); ok {
				return time.Unix(iv, 0).UTC(), nil
			}
			// Tolerate string-form params too (e.g. mixed-mode gateways or older
			// callers that still pass RFC3339 strings).
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
	if err := json.Unmarshal(raw, &s); err != nil {
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
	if err := json.Unmarshal(raw, &v); err != nil {
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
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a boolean"})
	}
	return v, nil
}

// ParamIntArray extracts an []int64 parameter. Returns 400 BadRequest on error.
func (r *Request) ParamIntArray(name string) ([]int64, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			switch iv := v.(type) {
			case []int64:
				return iv, nil
			case int64:
				return []int64{iv}, nil
			}
		}
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v []int64
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of integers"})
	}
	return v, nil
}

// ParamStringArray extracts a []string parameter. Returns 400 BadRequest on error.
func (r *Request) ParamStringArray(name string) ([]string, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			switch sv := v.(type) {
			case []string:
				return sv, nil
			case string:
				return []string{sv}, nil
			}
		}
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var v []string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of strings"})
	}
	return v, nil
}

// ParamJSON extracts a parameter into the target. Returns 400 BadRequest on error.
func (r *Request) ParamJSON(name string, target any) error {
	if r.paramNames != nil {
		v, ok := r.findParam(name)
		if !ok || v == nil {
			// Not found or nil — for pointer targets this is valid (nullable param)
			return nil
		}
		if ptr, ok := target.(*any); ok {
			*ptr = v
			return nil
		}
		// Try to assign string to *string, int64 to *int64, etc.
		return assignBinaryParam(v, target)
	}
	raw, ok := r.Params[name]
	if !ok {
		// Nullable params (double-pointer targets) tolerate a missing key —
		// omitting an optional param is valid, matching the binary path above.
		if isNullableTarget(target) {
			return nil
		}
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid format"})
	}
	return nil
}

// isNullableTarget reports whether target is a pointer to a nullable (pointer)
// scalar — the codegen shape for optional params (String? → **string, etc.).
// Type switch instead of reflection — this is the request hot path.
func isNullableTarget(target any) bool {
	switch target.(type) {
	case **string, **int64, **float64, **bool, **time.Time, **time.Duration:
		return true
	}
	return false
}

// SetBinaryParams configures binary mode param storage.
// Called by RPC server to set up paramSlots without allocation.
func (r *Request) SetBinaryParams(names []string, count int) {
	r.paramNames = names
	r.paramCount = count
}

// SetParamSlot sets a param value by index (binary mode).
// If a slot already has a value, repeated calls accumulate into a slice.
// This supports array params encoded as repeated field IDs (e.g. batchLoad keys).
func (r *Request) SetParamSlot(index int, value any) {
	if index < 0 || index >= 16 {
		return
	}
	existing := r.paramSlots[index]
	if existing == nil {
		r.paramSlots[index] = value
		return
	}
	// Accumulate into slice
	switch ev := existing.(type) {
	case int64:
		if iv, ok := value.(int64); ok {
			r.paramSlots[index] = []int64{ev, iv}
			return
		}
	case []int64:
		if iv, ok := value.(int64); ok {
			r.paramSlots[index] = append(ev, iv)
			return
		}
	case string:
		if sv, ok := value.(string); ok {
			r.paramSlots[index] = []string{ev, sv}
			return
		}
	case []string:
		if sv, ok := value.(string); ok {
			r.paramSlots[index] = append(ev, sv)
			return
		}
	}
	// Type mismatch or unsupported — overwrite
	r.paramSlots[index] = value
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

// assignBinaryParam assigns a binary-decoded value to a typed pointer target.
func assignBinaryParam(v any, target any) error {
	switch t := target.(type) {
	case **string:
		if sv, ok := v.(string); ok {
			*t = &sv
			return nil
		}
	case **int64:
		if iv, ok := v.(int64); ok {
			*t = &iv
			return nil
		}
	case **float64:
		if fv, ok := v.(float64); ok {
			*t = &fv
			return nil
		}
	case **bool:
		if bv, ok := v.(bool); ok {
			*t = &bv
			return nil
		}
	case *string:
		if sv, ok := v.(string); ok {
			*t = sv
			return nil
		}
	case *int64:
		if iv, ok := v.(int64); ok {
			*t = iv
			return nil
		}
	case *float64:
		if fv, ok := v.(float64); ok {
			*t = fv
			return nil
		}
	case *bool:
		if bv, ok := v.(bool); ok {
			*t = bv
			return nil
		}
	}
	return nil
}
