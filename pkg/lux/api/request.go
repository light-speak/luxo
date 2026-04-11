package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

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
	API      string                     // $api field
	Select   []*selection.Field         // parsed $select
	Params   map[string]json.RawMessage // remaining fields as raw JSON
	Buf      *ResponseBuf               // response buffer — handler writes directly here
	Filters  []Filter                   // parsed $filters
	Sorters  []Sorter                   // parsed $sorters
	Page     int                        // page number (default 1)
	PageSize int                        // page size (default 20)
}

// ParseRequest reads an HTTP request body and extracts $api, $select, and params.
func ParseRequest(r *http.Request) (*Request, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

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
	reserved := map[string]bool{"$api": true, "$select": true, "$filters": true, "$sorters": true, "page": true, "pageSize": true}
	for k, v := range raw {
		if reserved[k] {
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
		sonic.Unmarshal(pageRaw, &req.Page)
	}
	if psRaw, ok := raw["pageSize"]; ok {
		sonic.Unmarshal(psRaw, &req.PageSize)
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		req.PageSize = 20
	}
	return nil
}

// ParamInt extracts an integer parameter.
func (r *Request) ParamInt(name string) (int64, error) {
	raw, ok := r.Params[name]
	if !ok {
		return 0, fmt.Errorf("missing parameter '%s'", name)
	}
	var v int64
	if err := sonic.Unmarshal(raw, &v); err != nil {
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
	if err := sonic.Unmarshal(raw, &v); err != nil {
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
	if err := sonic.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("parameter '%s' must be a boolean", name)
	}
	return v, nil
}

// HasParam checks if a parameter exists.
func (r *Request) HasParam(name string) bool {
	_, ok := r.Params[name]
	return ok
}
