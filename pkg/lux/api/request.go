package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// bodyPool reuses byte buffers for reading request bodies.
var bodyPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// binaryRequestPool reuses the fixed-size request envelope on binary transport
// paths. Request data is valid only for the duration of the synchronous handler,
// matching the existing pooled response-buffer lifetime.
var binaryRequestPool = sync.Pool{
	New: func() any {
		return new(Request)
	},
}

// GetRequest gets a zeroed Request from the binary transport pool.
// Callers must return it with PutRequest after synchronous processing finishes.
func GetRequest() *Request {
	return binaryRequestPool.Get().(*Request)
}

// PutRequest clears and returns a Request to the binary transport pool.
func PutRequest(req *Request) {
	*req = Request{}
	binaryRequestPool.Put(req)
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
var reservedKeys = [...]string{
	"$id", "$api", "$select", "$filters", "$sorters", "page", "pageSize",
}

// Request represents a parsed Luvia API request.
type Request struct {
	responseCache *cacheRouteBinding
	API           string                     // $api field
	Select        []*selection.Field         // parsed $select (JSON mode)
	Params        map[string]json.RawMessage // remaining fields as raw JSON
	Buf           *ResponseBuf               // response buffer — handler writes directly here
	Filters       []Filter                   // parsed $filters
	Sorters       []Sorter                   // parsed $sorters
	Page          int                        // page number (default 1)
	PageSize      int                        // page size (default 20)
	BinaryMode    bool                       // true when X-Luxo-Mode: binary
	FieldMask     []byte                     // binary field mask (binary mode)
	ClientKey     string                     // transport-provided key for per-client policies
	Internal      bool                       // trusted internal RPC dispatch; public-edge policies already ran
	pageSizeSet   bool                       // true when pageSize was explicitly supplied

	// Typed binary params and prepared JSON structured messages share inline storage.
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

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

func (r *Request) applyBinaryListParams() {
	if page, ok := r.findParam("page"); ok {
		if value, valid := page.(int64); valid && value > 0 {
			r.Page = int(value)
		}
	}
	if pageSize, ok := r.findParam("pageSize"); ok {
		r.pageSizeSet = true
		if value, valid := pageSize.(int64); valid && value >= 0 && value <= 100 {
			r.PageSize = int(value)
		}
	}
}

// ParseRequest reads an HTTP request body and extracts $api, $select, and params.
// MaxJSONBodySize limits JSON request body to 10MB (smaller than binary).
const MaxJSONBodySize = 10 * 1024 * 1024

var errInvalidJSONUTF8 = stderrors.New("invalid JSON: invalid UTF-8")

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
	// Parse names and opaque values together; reject invalid Unicode before it
	// can be replaced. The returned values own their bytes, not the pooled body.
	raw, err := jsonutil.ParseObject(body)
	if err != nil {
		if !utf8.Valid(body) {
			return nil, errInvalidJSONUTF8
		}
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return parseRawRequest(raw)
}

// parseRawRequest takes ownership of raw on success. Callers must extract any
// transport metadata first and stop using raw once the request is returned.
// Failure leaves the envelope intact; values already own their input bytes.
func parseRawRequest(raw map[string]json.RawMessage) (*Request, error) {
	req := &Request{}

	// Extract $api
	apiRaw, ok := raw["$api"]
	if !ok {
		return nil, fmt.Errorf("missing $api field")
	}
	var err error
	if req.API, err = jsonutil.ParseString(apiRaw); err != nil {
		return nil, fmt.Errorf("$api must be a string")
	}
	if req.API == "" {
		return nil, fmt.Errorf("$api must not be empty")
	}

	// Extract $select (optional)
	if selRaw, ok := raw["$select"]; ok {
		selStr, err := jsonutil.ParseString(selRaw)
		if err != nil {
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

	// Reuse the owned envelope rather than allocate and populate a second map.
	for _, key := range reservedKeys {
		delete(raw, key)
	}
	req.Params = raw

	return req, nil
}

// parseListParams extracts $filters, $sorters, page, pageSize from raw request.
func (req *Request) parseListParams(raw map[string]json.RawMessage) error {
	if filtersRaw, ok := raw["$filters"]; ok {
		filters, err := parseListObjects(filtersRaw, 1000, "op", filterMetadata)
		if err != nil {
			return fmt.Errorf("$filters: %w", err)
		}
		req.Filters = filters
		for i := range req.Filters {
			if req.Filters[i].Field == "" || filterOperatorID(req.Filters[i].Operator) == 0 {
				return fmt.Errorf("$filters[%d]: invalid field or operator", i)
			}
		}
	}
	if sortersRaw, ok := raw["$sorters"]; ok {
		sorters, err := parseListObjects(sortersRaw, 100, "order", sorterMetadata)
		if err != nil {
			return fmt.Errorf("$sorters: %w", err)
		}
		req.Sorters = sorters
		for i := range req.Sorters {
			if req.Sorters[i].Field == "" || (req.Sorters[i].Order != "asc" && req.Sorters[i].Order != "desc") {
				return fmt.Errorf("$sorters[%d]: invalid field or order", i)
			}
		}
	}
	req.Page = 1
	req.PageSize = defaultPageSize
	var err error
	if pageRaw, ok := raw["page"]; ok {
		if req.Page, err = parseJSONPageInteger(pageRaw); err != nil {
			return fmt.Errorf("page must be an integer")
		}
	}
	if psRaw, ok := raw["pageSize"]; ok {
		req.pageSizeSet = true
		if req.PageSize, err = parseJSONPageInteger(psRaw); err != nil {
			return fmt.Errorf("pageSize must be an integer")
		}
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 0 || req.PageSize > maxPageSize {
		req.PageSize = defaultPageSize
	}
	// pageSize == 0 → return all (no pagination)
	return nil
}

// Pagination still uses Go int internally; never truncate on a 32-bit host.
func parseJSONPageInteger(raw []byte) (int, error) {
	value, err := jsonutil.ParseInt64(raw)
	if err != nil || int64(int(value)) != value {
		return 0, fmt.Errorf("expected an integer within platform range")
	}
	return int(value), nil
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

// ParamIsNull reports an explicit null in the request's original transport.
func (r *Request) ParamIsNull(name string) bool {
	if r.Params != nil {
		raw, ok := r.Params[name]
		return ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	for i := 0; i < r.paramCount; i++ {
		if r.paramNames[i] == name {
			return r.paramNull&(1<<i) != 0
		}
	}
	return false
}

// ParamInt extracts an integer parameter. Returns 400 BadRequest on error.
// Binary mode uses typed slots; JSON mode reads a strict integer token.
func (r *Request) ParamInt(name string) (int64, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseInt64(raw)
	if err != nil {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an integer"})
	}
	return v, nil
}

// ParamString extracts a string parameter. Returns 400 BadRequest on error.
func (r *Request) ParamString(name string) (string, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseString(raw)
	if err != nil {
		return "", errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a string"})
	}
	return v, nil
}

// ParamDateTime extracts a time.Time parameter.
func (r *Request) ParamDateTime(name string) (time.Time, error) {
	if r.Params == nil {
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
	t, err := jsonutil.ParseDateTime(raw)
	if err != nil {
		return time.Time{}, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be RFC3339 format"})
	}
	return t, nil
}

// ParamFloat extracts a float64 parameter. Returns 400 BadRequest on error.
func (r *Request) ParamFloat(name string) (float64, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseFloat64(raw)
	if err != nil {
		return 0, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a number"})
	}
	return v, nil
}

// ParamBool extracts a boolean parameter. Returns 400 BadRequest on error.
func (r *Request) ParamBool(name string) (bool, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseBool(raw)
	if err != nil {
		return false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be a boolean"})
	}
	return v, nil
}

// ParamIntArray extracts an []int64 parameter. Returns 400 BadRequest on error.
func (r *Request) ParamIntArray(name string) ([]int64, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseInt64Array(raw)
	if err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of integers"})
	}
	return v, nil
}

// ParamStringArray extracts a []string parameter. Returns 400 BadRequest on error.
func (r *Request) ParamStringArray(name string) ([]string, error) {
	if r.Params == nil {
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
	v, err := jsonutil.ParseStringArray(raw)
	if err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of strings"})
	}
	return v, nil
}

// ParamUUIDArray extracts a []uuid.UUID parameter. Returns 400 BadRequest on error.
func (r *Request) ParamUUIDArray(name string) ([]uuid.UUID, error) {
	if r.Params == nil {
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
	var reader jsonUUIDArrayReader
	value, err := jsonutil.ParseArray(raw, reader.parse)
	if err != nil {
		return nil, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must be an array of UUIDs"})
	}
	return value, nil
}

// ParseArray has already validated this entire JSON value, including Unicode.
type jsonUUIDArrayReader struct{ scratch []byte }

func (r *jsonUUIDArrayReader) parse(raw []byte) (uuid.UUID, error) {
	if len(raw) >= 2 && raw[0] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return uuid.ParseBytes(raw[1 : len(raw)-1])
	}
	value, err := jsontext.AppendUnquote(r.scratch[:0], raw)
	if err != nil {
		return uuid.Nil, err
	}
	r.scratch = value
	return uuid.ParseBytes(value)
}

// ParamMessage returns the structured binary message prepared by the transport
// boundary. Local JSON handlers use ParamInput and generated JSON readers.
// present distinguishes an omitted optional parameter from an explicit null.
func (r *Request) ParamMessage(name string, optional, nullable bool) ([]byte, bool, error) {
	value, present, err := r.binaryStructuredParam(name, optional, nullable)
	if err != nil || !present || value == nil {
		return nil, present, err
	}
	message, ok := value.(BinaryMessage)
	if !ok {
		return nil, true, invalidBinaryStructuredParam(name)
	}
	return message, true, nil
}

// ParamMessageArray returns structured Luxo list parameters without JSON conversion.
func (r *Request) ParamMessageArray(name string, optional, nullable bool) (BinaryMessages, bool, error) {
	value, present, err := r.binaryStructuredParam(name, optional, nullable)
	if err != nil || !present || value == nil {
		return nil, present, err
	}
	messages, ok := value.(BinaryMessages)
	if !ok {
		return nil, true, invalidBinaryStructuredParam(name)
	}
	return messages, true, nil
}

func (r *Request) binaryStructuredParam(name string, optional, nullable bool) (any, bool, error) {
	value, present := r.findParam(name)
	if !present {
		if optional {
			return nil, false, nil
		}
		return nil, false, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "missing"})
	}
	if value == nil && !nullable {
		return nil, true, errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "must not be null"})
	}
	return value, true, nil
}

func invalidBinaryStructuredParam(name string) error {
	return errors.BadRequest.WithData(errors.ParamError{Param: name, Error: "invalid binary message"})
}

// InvalidParam converts a generated codec failure into a client-safe parameter error.
func InvalidParam(name string, cause error) error {
	detail := "invalid binary message"
	if cause != nil {
		detail = cause.Error()
	}
	return errors.BadRequest.
		WithData(errors.ParamError{Param: name, Error: detail}).
		WithCause(cause)
}

// SetBinaryParams configures binary mode param storage.
// Called by RPC server to set up paramSlots without allocation.
func (r *Request) SetBinaryParams(names []string, count int) {
	r.Params = nil
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
	if value == nil {
		r.markParamNull(index)
		return
	}
	r.markParamPresent(index)
	r.paramNull &^= 1 << index
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
	if r.Params == nil {
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
