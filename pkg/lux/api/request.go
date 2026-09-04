package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/selection"
	"github.com/shopspring/decimal"
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
	"$id": true, "$api": true, "$select": true, "$filters": true, "$sorters": true,
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
	value, err := parseFilterValue(raw.Value)
	if err != nil {
		return err
	}
	f.Value = value
	f.Field = raw.Field
	f.Operator = raw.Operator
	return nil
}

func parseFilterValue(raw json.RawMessage) (string, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || value[0] == '{' || value[0] == '[' {
		return "", fmt.Errorf("filter value must be a string, number, or boolean")
	}
	if value[0] == '"' {
		var decoded string
		if err := json.Unmarshal(value, &decoded); err != nil {
			return "", err
		}
		return decoded, nil
	}
	return string(value), nil
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
	ClientKey  string                     // transport-provided key for per-client policies
	Internal   bool                       // trusted internal RPC dispatch; public-edge policies already ran

	// Binary params — zero-allocation inline storage
	// paramSlots stores values by index (position in API param list)
	// paramNames points to the static name list from Registry (never allocated per request)
	paramSlots [16]any
	paramNames []string // shared, not owned
	paramCount int
	paramSet   uint16
	paramNull  uint16

	// Canonical binary request bytes are prepared once at the transport edge.
	// Gateways forward these slices directly without decoding and re-encoding.
	binaryRequest []byte
	binaryParams  []byte
}

func (r *Request) applyBinaryListParams() {
	if page, ok := r.findParam("page"); ok {
		if value, valid := page.(int64); valid && value > 0 {
			r.Page = int(value)
		}
	}
	if pageSize, ok := r.findParam("pageSize"); ok {
		if value, valid := pageSize.(int64); valid && value >= 0 && value <= 100 {
			r.PageSize = int(value)
		}
	}
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
	return parseRawRequest(raw)
}

func parseRawRequest(raw map[string]json.RawMessage) (*Request, error) {
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
		for i := range req.Filters {
			if req.Filters[i].Field == "" || filterOperatorID(req.Filters[i].Operator) == 0 {
				return fmt.Errorf("$filters[%d]: invalid field or operator", i)
			}
		}
	}
	if sortersRaw, ok := raw["$sorters"]; ok {
		if err := json.Unmarshal(sortersRaw, &req.Sorters); err != nil {
			return fmt.Errorf("$sorters: %w", err)
		}
		if len(req.Sorters) > 100 {
			return fmt.Errorf("$sorters exceeds limit: %d > 100", len(req.Sorters))
		}
		for i := range req.Sorters {
			if req.Sorters[i].Field == "" || (req.Sorters[i].Order != "asc" && req.Sorters[i].Order != "desc") {
				return fmt.Errorf("$sorters[%d]: invalid field or order", i)
			}
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
			if r.paramSet&(1<<i) != 0 {
				return r.paramSlots[i], true
			}
			if r.paramSlots[i] == nil {
				return nil, false
			}
			return r.paramSlots[i], true
		}
	}
	return nil, false
}

func (r *Request) markParamPresent(index int) {
	r.paramSet |= 1 << index
}

func (r *Request) markParamNull(index int) {
	r.paramSet |= 1 << index
	r.paramNull |= 1 << index
	r.paramSlots[index] = nil
}

// ParamIsNull reports whether a binary parameter was explicitly encoded as null.
func (r *Request) ParamIsNull(name string) bool {
	for i := 0; i < r.paramCount; i++ {
		if r.paramNames[i] == name {
			return r.paramNull&(1<<i) != 0
		}
	}
	if raw, ok := r.Params[name]; ok {
		return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	return false
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

// ParamDateTime extracts a time.Time parameter.
func (r *Request) ParamDateTime(name string) (time.Time, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			if tv, ok := v.(time.Time); ok {
				return tv, nil
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

// ParamUUIDArray extracts a []uuid.UUID parameter. Returns 400 BadRequest on error.
func (r *Request) ParamUUIDArray(name string) ([]uuid.UUID, error) {
	if r.paramNames != nil {
		if v, ok := r.findParam(name); ok {
			switch uv := v.(type) {
			case []uuid.UUID:
				return uv, nil
			case uuid.UUID:
				return []uuid.UUID{uv}, nil
			}
		}
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	raw, ok := r.Params[name]
	if !ok {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	var value []uuid.UUID
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of UUIDs"})
	}
	return value, nil
}

// ParamJSON extracts a required parameter into the target.
func (r *Request) ParamJSON(name string, target any) error {
	return r.paramJSON(name, target, false, false)
}

// ParamJSONOptional extracts an optional parameter into the target.
func (r *Request) ParamJSONOptional(name string, target any) error {
	return r.paramJSON(name, target, true, false)
}

// ParamJSONNullable extracts a required parameter that may explicitly be null.
func (r *Request) ParamJSONNullable(name string, target any) error {
	return r.paramJSON(name, target, false, true)
}

// ParamJSONOptionalNullable extracts an optional parameter that may explicitly be null.
func (r *Request) ParamJSONOptionalNullable(name string, target any) error {
	return r.paramJSON(name, target, true, true)
}

func (r *Request) paramJSON(name string, target any, optional, nullable bool) error {
	if r.paramNames != nil {
		v, ok := r.findParam(name)
		if !ok {
			if optional {
				return nil
			}
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
		}
		if v == nil {
			if nullable {
				return nil
			}
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must not be null"})
		}
		if ptr, ok := target.(*any); ok {
			*ptr = v
			return nil
		}
		if assignBinaryParamFast(v, target) {
			return nil
		}
		data, err := json.Marshal(v)
		if err != nil || json.Unmarshal(data, target) != nil {
			return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid format"})
		}
		return nil
	}
	raw, ok := r.Params[name]
	if !ok {
		if optional {
			return nil
		}
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !nullable {
		return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must not be null"})
	}
	if err := json.Unmarshal(raw, target); err != nil {
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
// If a slot already has a value, repeated calls accumulate into a slice.
// This supports array params encoded as repeated field IDs (e.g. batchLoad keys).
func (r *Request) SetParamSlot(index int, value any) {
	if index < 0 || index >= 16 {
		return
	}
	r.markParamPresent(index)
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

// BinaryRequest returns the canonical Luxo request body.
func (r *Request) BinaryRequest() []byte {
	return r.binaryRequest
}

// BinaryParams returns the canonical parameter fields including the end marker.
func (r *Request) BinaryParams() []byte {
	return r.binaryParams
}

// assignBinaryParam assigns a binary-decoded value to a typed pointer target.
func assignBinaryParam(v any, target any) error {
	assignBinaryParamFast(v, target)
	return nil
}

func assignBinaryParamFast(v any, target any) bool {
	switch t := target.(type) {
	case **string:
		if sv, ok := v.(string); ok {
			*t = &sv
			return true
		}
	case **int64:
		if iv, ok := v.(int64); ok {
			*t = &iv
			return true
		}
	case **float64:
		if fv, ok := v.(float64); ok {
			*t = &fv
			return true
		}
	case **bool:
		if bv, ok := v.(bool); ok {
			*t = &bv
			return true
		}
	case *string:
		if sv, ok := v.(string); ok {
			*t = sv
			return true
		}
	case *int64:
		if iv, ok := v.(int64); ok {
			*t = iv
			return true
		}
	case *float64:
		if fv, ok := v.(float64); ok {
			*t = fv
			return true
		}
	case *bool:
		if bv, ok := v.(bool); ok {
			*t = bv
			return true
		}
	default:
		return assignBinaryNativeParam(v, target)
	}
	return false
}

func assignBinaryNativeParam(v any, target any) bool {
	switch t := target.(type) {
	case *time.Time:
		if tv, ok := v.(time.Time); ok {
			*t = tv
			return true
		}
	case **time.Time:
		if tv, ok := v.(time.Time); ok {
			*t = &tv
			return true
		}
	case *uuid.UUID:
		if uv, ok := v.(uuid.UUID); ok {
			*t = uv
			return true
		}
	case **uuid.UUID:
		if uv, ok := v.(uuid.UUID); ok {
			*t = &uv
			return true
		}
	case *json.RawMessage:
		if raw, ok := v.(json.RawMessage); ok {
			*t = append((*t)[:0], raw...)
			return true
		}
	case *time.Duration:
		if dv, ok := v.(time.Duration); ok {
			*t = dv
			return true
		}
	case **time.Duration:
		if dv, ok := v.(time.Duration); ok {
			*t = &dv
			return true
		}
	case *decimal.Decimal:
		if dv, ok := v.(decimal.Decimal); ok {
			*t = dv
			return true
		}
	case **decimal.Decimal:
		if dv, ok := v.(decimal.Decimal); ok {
			*t = &dv
			return true
		}
	default:
		return assignBinarySliceParam(v, target)
	}
	return false
}

func assignBinarySliceParam(v any, target any) bool {
	switch t := target.(type) {
	case *[]byte:
		if bv, ok := v.([]byte); ok {
			*t = bv
			return true
		}
	case *[]int64:
		if values, ok := v.([]int64); ok {
			*t = values
			return true
		}
	case *[]float64:
		if values, ok := v.([]float64); ok {
			*t = values
			return true
		}
	case *[]string:
		if values, ok := v.([]string); ok {
			*t = values
			return true
		}
	case *[]bool:
		if values, ok := v.([]bool); ok {
			*t = values
			return true
		}
	case *[]time.Time:
		if values, ok := v.([]time.Time); ok {
			*t = values
			return true
		}
	case *[]time.Duration:
		if values, ok := v.([]time.Duration); ok {
			*t = values
			return true
		}
	case *[]uuid.UUID:
		if values, ok := v.([]uuid.UUID); ok {
			*t = values
			return true
		}
	case *[]decimal.Decimal:
		if values, ok := v.([]decimal.Decimal); ok {
			*t = values
			return true
		}
	case *[][]byte:
		if values, ok := v.([][]byte); ok {
			*t = values
			return true
		}
	case *[]json.RawMessage:
		if values, ok := v.([]json.RawMessage); ok {
			*t = values
			return true
		}
	}
	return false
}
