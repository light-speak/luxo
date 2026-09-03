package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/bits"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// APIRegistry maps API IDs (from luxo.lock) to handler names and param metadata.
// Built at startup by RegisterHandlers, used for binary protocol routing.
type APIRegistry struct {
	idToName   map[int]string
	nameToID   map[string]int
	paramOrder map[string][]ParamMeta
	paramNames map[string][]string // static name lists for zero-alloc param lookup
	schema     *schema.Schema
}

// SetSchema configures return-model metadata used to decode binary field masks.
func (r *APIRegistry) SetSchema(s *schema.Schema) {
	r.schema = s
}

// ParamMeta describes an API parameter for binary decoding.
type ParamMeta struct {
	Name     string
	Type     string // "Int", "Float", "String", "Boolean", etc.
	FieldID  int    // from luxo.lock
	IsList   bool   // true for [T] params
	Nullable bool   // true when the value is prefixed by null/present marker
}

const maxFieldMaskSize = 10 * 1024 // 10KB = 80,000 fields max

const (
	// BinaryFiltersFieldID and BinarySortersFieldID are reserved request TLV
	// field IDs for list controls. User parameter IDs must remain below them.
	BinaryFiltersFieldID = (1 << 31) - 2
	BinarySortersFieldID = (1 << 31) - 1
)

var filterOperators = [...]string{
	"", "eq", "ne", "gt", "gte", "lt", "lte", "contains", "startswith", "endswith", "match",
}

// NewAPIRegistry creates an empty registry.
func NewAPIRegistry() *APIRegistry {
	return &APIRegistry{
		idToName:   make(map[int]string),
		nameToID:   make(map[string]int),
		paramOrder: make(map[string][]ParamMeta),
		paramNames: make(map[string][]string),
	}
}

// Register adds an API to the registry.
func (r *APIRegistry) Register(name string, id int) {
	r.idToName[id] = name
	r.nameToID[name] = id
}

// RegisterParams sets the ordered parameter metadata for an API.
// Builds a static name list for zero-alloc param lookup at request time.
func (r *APIRegistry) RegisterParams(name string, params []ParamMeta) {
	r.paramOrder[name] = params
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	r.paramNames[name] = names
}

// NameByID returns the API name for a given ID.
func (r *APIRegistry) NameByID(id int) (string, bool) {
	name, ok := r.idToName[id]
	return name, ok
}

// IDByName returns the API ID for a given name.
func (r *APIRegistry) IDByName(name string) (int, bool) {
	id, ok := r.nameToID[name]
	return id, ok
}

// ParamOrder returns param metadata for an API (used by RPC server).
func (r *APIRegistry) ParamOrder(name string) []ParamMeta {
	return r.paramOrder[name]
}

// ParamNames returns the static param name list for an API (used by RPC server).
func (r *APIRegistry) ParamNames(name string) []string {
	return r.paramNames[name]
}

// ParseBinaryRequest decodes a Luxo binary request into a Request struct.
// Wire format:
//
//	[API ID varint] [Field Mask length varint] [Field Mask bytes] [Params...]
//	Params: [fieldID varint] [value] ... [0x00 terminator]
//
// Params are decoded to json.RawMessage so existing handlers work unchanged.
func (r *APIRegistry) ParseBinaryRequest(body []byte) (*Request, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty binary request")
	}
	apiName, fields, fieldMask, off, err := r.parseBinaryHeader(body)
	if err != nil {
		return nil, err
	}

	// Inline decode params into Request.paramSlots — zero map allocation
	paramMeta := r.paramOrder[apiName]
	req := &Request{
		API:        apiName,
		Select:     fields,
		Page:       1,
		PageSize:   20,
		BinaryMode: true,
		FieldMask:  fieldMask,
		paramNames: r.paramNames[apiName],
		paramCount: len(paramMeta),
	}
	if len(paramMeta) > len(req.paramSlots) {
		return nil, fmt.Errorf("too many params (max %d) for API %s", len(req.paramSlots), apiName)
	}
	decoder := binaryParamDecoder{req: req, buf: body[off:], meta: paramMeta}
	if err := decoder.decode(); err != nil {
		return nil, err
	}
	req.applyBinaryListParams()
	if err := r.validateRequiredParams(req); err != nil {
		return nil, err
	}
	req.binaryRequest = body
	req.binaryParams = body[off:]

	return req, nil
}

func (r *APIRegistry) prepareJSONRequest(req *Request) error {
	apiID, registered := r.nameToID[req.API]
	if !registered {
		return nil
	}
	params, err := r.encodeJSONRequestParams(req)
	if err != nil {
		return err
	}
	request := make([]byte, 0, 20+len(req.FieldMask)+len(params))
	request = codec.AppendVarint(request, uint64(apiID))
	request = codec.AppendVarint(request, uint64(len(req.FieldMask)))
	request = append(request, req.FieldMask...)
	paramOffset := len(request)
	request = append(request, params...)
	req.binaryRequest = request
	req.binaryParams = request[paramOffset:]
	return nil
}

func (r *APIRegistry) encodeJSONRequestParams(req *Request) ([]byte, error) {
	meta := r.paramOrder[req.API]
	if err := validateKnownJSONParams(req, meta); err != nil {
		return nil, err
	}
	var enc codec.Encoder
	for i := range meta {
		value, present, err := jsonParamValue(req, meta[i])
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		if err := encodeBinaryParam(&enc, meta[i], value); err != nil {
			return nil, err
		}
	}
	if len(req.Filters) > 0 {
		if err := writeBinaryFilters(&enc, req.Filters); err != nil {
			return nil, err
		}
	}
	if len(req.Sorters) > 0 {
		if err := writeBinarySorters(&enc, req.Sorters); err != nil {
			return nil, err
		}
	}
	enc.WriteEnd()
	if err := r.validateRequiredParams(req); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

func validateKnownJSONParams(req *Request, meta []ParamMeta) error {
	for name := range req.Params {
		known := false
		for i := range meta {
			if meta[i].Name == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown parameter %q for API %s", name, req.API)
		}
	}
	return nil
}

func jsonParamValue(req *Request, meta ParamMeta) (any, bool, error) {
	if meta.Name == "page" {
		return int64(req.Page), true, nil
	}
	if meta.Name == "pageSize" {
		return int64(req.PageSize), true, nil
	}
	raw, present := req.Params[meta.Name]
	if !present {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	if meta.Type == "Bytes" {
		return decodeJSONBytesParam(raw, meta)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("param %s: invalid JSON: %w", meta.Name, err)
	}
	if meta.Type == "Duration" {
		canonical, err := canonicalJSONDuration(meta, value)
		if err != nil {
			return nil, false, err
		}
		value = canonical
	}
	return value, true, nil
}

func canonicalJSONDuration(meta ParamMeta, value any) (any, error) {
	if !meta.IsList {
		converted, valid := binaryInt(value)
		if !valid {
			return nil, fmt.Errorf("param %s: expected Duration nanoseconds", meta.Name)
		}
		return converted, nil
	}
	values, valid := value.([]any)
	if !valid {
		return nil, fmt.Errorf("param %s: expected a list of Duration nanoseconds", meta.Name)
	}
	converted := make([]int64, len(values))
	for i := range values {
		item, ok := binaryInt(values[i])
		if !ok {
			return nil, fmt.Errorf("param %s[%d]: expected Duration nanoseconds", meta.Name, i)
		}
		converted[i] = item
	}
	return converted, nil
}

func decodeJSONBytesParam(raw json.RawMessage, meta ParamMeta) (any, bool, error) {
	if !meta.IsList {
		var value []byte
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, false, fmt.Errorf("param %s: expected base64 bytes", meta.Name)
		}
		return value, true, nil
	}
	var values [][]byte
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, fmt.Errorf("param %s: expected a list of base64 bytes", meta.Name)
	}
	return values, true, nil
}

func (r *APIRegistry) validateRequiredParams(req *Request) error {
	if r.schema == nil {
		return nil
	}
	definition := r.schema.APIs[req.API]
	if definition == nil {
		return nil
	}
	for i := range definition.Params {
		param := &definition.Params[i]
		if !param.HasDefault && !req.HasParam(param.Name) {
			return fmt.Errorf("param %s: missing", param.Name)
		}
	}
	return nil
}

func (r *APIRegistry) parseBinaryHeader(body []byte) (string, []*selection.Field, []byte, int, error) {
	apiID, n := codec.ReadVarint(body, 0)
	if n <= 0 {
		return "", nil, nil, 0, fmt.Errorf("invalid API ID varint")
	}
	apiName, ok := r.idToName[int(apiID)]
	if !ok {
		return "", nil, nil, 0, fmt.Errorf("unknown API ID: %d", apiID)
	}
	maskLen, consumed := codec.ReadVarint(body, n)
	if consumed <= 0 {
		return "", nil, nil, 0, fmt.Errorf("invalid field mask length")
	}
	off := n + consumed
	if maskLen == 0 {
		return apiName, nil, nil, off, nil
	}
	if maskLen > maxFieldMaskSize {
		return "", nil, nil, 0, fmt.Errorf("field mask size %d exceeds limit %d", maskLen, maxFieldMaskSize)
	}
	if maskLen > uint64(len(body)) {
		return "", nil, nil, 0, fmt.Errorf("field mask length overflow")
	}
	maskEnd := off + int(maskLen)
	if maskEnd > len(body) {
		return "", nil, nil, 0, fmt.Errorf("field mask exceeds body")
	}
	mask := body[off:maskEnd]
	if _, _, valid := codec.SplitSelectionMask(mask); !valid {
		return "", nil, nil, 0, fmt.Errorf("invalid recursive field mask")
	}
	fields, err := r.decodeFieldMask(apiName, mask)
	return apiName, fields, mask, maskEnd, err
}

type binaryParamDecoder struct {
	req        *Request
	buf        []byte
	meta       []ParamMeta
	off        int
	filtersSet bool
	sortersSet bool
}

func (d *binaryParamDecoder) decode() error {
	for d.off < len(d.buf) {
		fieldID, err := d.readFieldID()
		if err != nil {
			return err
		}
		if fieldID == 0 {
			if d.off != len(d.buf) {
				return fmt.Errorf("trailing bytes after param terminator")
			}
			return nil
		}
		if err := d.decodeField(fieldID); err != nil {
			return err
		}
	}
	return fmt.Errorf("missing param terminator")
}

func (d *binaryParamDecoder) readFieldID() (uint64, error) {
	fieldID, n := codec.ReadVarint(d.buf, d.off)
	if n < 0 {
		return 0, fmt.Errorf("param field ID varint overflow")
	}
	if n == 0 {
		return 0, fmt.Errorf("truncated param field ID")
	}
	d.off += n
	return fieldID, nil
}

func (d *binaryParamDecoder) decodeField(fieldID uint64) error {
	switch fieldID {
	case BinaryFiltersFieldID:
		return d.decodeFilters()
	case BinarySortersFieldID:
		return d.decodeSorters()
	default:
		return d.decodeParam(fieldID)
	}
}

func (d *binaryParamDecoder) decodeFilters() error {
	if d.filtersSet {
		return fmt.Errorf("duplicate binary filters field")
	}
	filters, consumed, err := readBinaryFilters(d.buf, d.off)
	if err != nil {
		return err
	}
	d.req.Filters = filters
	d.off += consumed
	d.filtersSet = true
	return nil
}

func (d *binaryParamDecoder) decodeSorters() error {
	if d.sortersSet {
		return fmt.Errorf("duplicate binary sorters field")
	}
	sorters, consumed, err := readBinarySorters(d.buf, d.off)
	if err != nil {
		return err
	}
	d.req.Sorters = sorters
	d.off += consumed
	d.sortersSet = true
	return nil
}

func (d *binaryParamDecoder) decodeParam(fieldID uint64) error {
	paramIndex, meta := findBinaryParam(d.meta, int(fieldID))
	if meta == nil {
		return fmt.Errorf("unknown param field ID %d for API %s", fieldID, d.req.API)
	}
	if d.req.paramSet&(uint16(1)<<paramIndex) != 0 {
		return fmt.Errorf("duplicate param field ID %d for API %s", fieldID, d.req.API)
	}
	d.req.markParamPresent(paramIndex)
	if meta.Nullable {
		present, consumed := codec.ReadNullable(d.buf, d.off)
		if consumed == 0 {
			return fmt.Errorf("param %s: invalid nullable marker", meta.Name)
		}
		d.off += consumed
		if !present {
			d.req.markParamNull(paramIndex)
			return nil
		}
	}
	value, consumed, err := readBinaryParam(d.buf, d.off, *meta)
	if err != nil {
		return err
	}
	d.off += consumed
	d.req.paramSlots[paramIndex] = value
	return nil
}

func findBinaryParam(params []ParamMeta, fieldID int) (int, *ParamMeta) {
	for i := range params {
		if params[i].FieldID == fieldID {
			return i, &params[i]
		}
	}
	return -1, nil
}

func (r *APIRegistry) decodeFieldMask(apiName string, mask []byte) ([]*selection.Field, error) {
	if r.schema == nil {
		return nil, nil
	}
	apiMeta := r.schema.APIs[apiName]
	if apiMeta == nil || apiMeta.ReturnType == "" {
		return nil, nil
	}
	model := schemaObject(r.schema, apiMeta.ReturnType)
	return decodeFieldMask(mask, model, r.schema, 0)
}

// decodeFieldMask converts a recursive binary selection mask to a selection tree.
func decodeFieldMask(mask []byte, model *schema.Model, schemas *schema.Schema, depth int) ([]*selection.Field, error) {
	if model == nil {
		return nil, nil
	}
	if depth >= 32 {
		return nil, fmt.Errorf("field mask depth exceeds 32")
	}
	fieldMask, children, ok := codec.SplitSelectionMask(mask)
	if !ok {
		return nil, fmt.Errorf("invalid field mask for %s", model.Name)
	}
	fields := make([]*selection.Field, 0, len(model.Fields))
	selectedByID := make(map[int]*selection.Field)
	for i := range model.Fields {
		field := &model.Fields[i]
		if !codec.FieldMaskHas(fieldMask, field.ID) {
			continue
		}
		selected := &selection.Field{Name: field.Name}
		if field.Relation {
			selected.Children = []*selection.Field{}
		}
		fields = append(fields, selected)
		selectedByID[field.ID] = selected
	}
	if countFieldMaskBits(fieldMask) != len(fields) {
		return nil, fmt.Errorf("field mask for %s contains an unknown field ID", model.Name)
	}
	for off := 0; off < len(children); {
		fieldID, n := codec.ReadVarint(children, off)
		off += n
		length, n := codec.ReadVarint(children, off)
		off += n
		end := off + int(length)
		field := model.FieldByID(int(fieldID))
		selected := selectedByID[int(fieldID)]
		if field == nil || selected == nil || !field.Relation {
			return nil, fmt.Errorf("field mask child %d is not a selected relation on %s", fieldID, model.Name)
		}
		nested := schemas.Models[field.TypeName]
		if nested == nil {
			if decl := schemas.Types[field.TypeName]; decl != nil {
				nested = decl.AsModel()
			}
		}
		if nested == nil {
			return nil, fmt.Errorf("field mask nested type %q is not registered", field.TypeName)
		}
		nestedFields, err := decodeFieldMask(children[off:end], nested, schemas, depth+1)
		if err != nil {
			return nil, err
		}
		selected.Children = nestedFields
		off = end
	}
	return fields, nil
}

func countFieldMaskBits(mask []byte) int {
	count := 0
	for _, value := range mask {
		count += bits.OnesCount8(value)
	}
	return count
}

// EncodeBinaryRequest creates a canonical Luxo binary request.
func EncodeBinaryRequest(apiID int, fieldMask []byte, params map[string]any, paramMeta []ParamMeta) ([]byte, error) {
	if apiID <= 0 {
		return nil, fmt.Errorf("invalid API ID %d", apiID)
	}
	if len(fieldMask) > maxFieldMaskSize {
		return nil, fmt.Errorf("field mask size %d exceeds limit %d", len(fieldMask), maxFieldMaskSize)
	}
	if len(fieldMask) > 0 {
		if _, _, valid := codec.SplitSelectionMask(fieldMask); !valid {
			return nil, fmt.Errorf("invalid recursive field mask")
		}
	}
	var buf []byte
	buf = codec.AppendVarint(buf, uint64(apiID))
	buf = codec.AppendVarint(buf, uint64(len(fieldMask)))
	buf = append(buf, fieldMask...)

	var enc codec.Encoder
	known := make(map[string]struct{}, len(paramMeta))
	for _, meta := range paramMeta {
		if meta.FieldID >= BinaryFiltersFieldID {
			return nil, fmt.Errorf("param %s: field ID %d is reserved", meta.Name, meta.FieldID)
		}
		known[meta.Name] = struct{}{}
		v, ok := params[meta.Name]
		if !ok {
			continue
		}
		if err := encodeBinaryParam(&enc, meta, v); err != nil {
			return nil, err
		}
	}
	if value, ok := params["$filters"]; ok {
		filters, valid := value.([]Filter)
		if !valid {
			return nil, fmt.Errorf("$filters: expected []api.Filter, got %T", value)
		}
		if err := writeBinaryFilters(&enc, filters); err != nil {
			return nil, err
		}
		known["$filters"] = struct{}{}
	}
	if value, ok := params["$sorters"]; ok {
		sorters, valid := value.([]Sorter)
		if !valid {
			return nil, fmt.Errorf("$sorters: expected []api.Sorter, got %T", value)
		}
		if err := writeBinarySorters(&enc, sorters); err != nil {
			return nil, err
		}
		known["$sorters"] = struct{}{}
	}
	for name := range params {
		if _, ok := known[name]; !ok {
			return nil, fmt.Errorf("unknown binary parameter %q", name)
		}
	}
	enc.WriteEnd()
	buf = append(buf, enc.Bytes()...)
	return buf, nil
}

func writeBinaryFilters(enc *codec.Encoder, filters []Filter) error {
	if len(filters) > 1000 {
		return fmt.Errorf("$filters exceeds limit: %d > 1000", len(filters))
	}
	enc.WriteFieldHeader(BinaryFiltersFieldID)
	enc.WriteArrayHeader(len(filters))
	for i := range filters {
		operatorID := filterOperatorID(filters[i].Operator)
		if filters[i].Field == "" || operatorID == 0 {
			return fmt.Errorf("$filters[%d]: invalid field or operator", i)
		}
		enc.WriteString(filters[i].Field)
		enc.WriteVarint(uint64(operatorID))
		enc.WriteString(filters[i].Value)
	}
	return nil
}

func readBinaryFilters(data []byte, off int) ([]Filter, int, error) {
	start := off
	count, n := codec.ReadVarint(data, off)
	if n <= 0 || count > 1000 {
		return nil, 0, fmt.Errorf("invalid binary filters count")
	}
	off += n
	filters := make([]Filter, int(count))
	for i := range filters {
		field, consumed := codec.ReadString(data, off)
		if consumed == 0 || field == "" {
			return nil, 0, fmt.Errorf("invalid binary filter field at index %d", i)
		}
		off += consumed
		operatorID, consumed := codec.ReadVarint(data, off)
		if consumed <= 0 || operatorID == 0 || operatorID >= uint64(len(filterOperators)) {
			return nil, 0, fmt.Errorf("invalid binary filter operator at index %d", i)
		}
		off += consumed
		value, consumed := codec.ReadString(data, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("invalid binary filter value at index %d", i)
		}
		off += consumed
		filters[i] = Filter{Field: field, Operator: filterOperators[operatorID], Value: value}
	}
	return filters, off - start, nil
}

func writeBinarySorters(enc *codec.Encoder, sorters []Sorter) error {
	if len(sorters) > 100 {
		return fmt.Errorf("$sorters exceeds limit: %d > 100", len(sorters))
	}
	enc.WriteFieldHeader(BinarySortersFieldID)
	enc.WriteArrayHeader(len(sorters))
	for i := range sorters {
		if sorters[i].Field == "" || (sorters[i].Order != "asc" && sorters[i].Order != "desc") {
			return fmt.Errorf("$sorters[%d]: invalid field or order", i)
		}
		enc.WriteString(sorters[i].Field)
		enc.WriteBool(sorters[i].Order == "desc")
	}
	return nil
}

func readBinarySorters(data []byte, off int) ([]Sorter, int, error) {
	start := off
	count, n := codec.ReadVarint(data, off)
	if n <= 0 || count > 100 {
		return nil, 0, fmt.Errorf("invalid binary sorters count")
	}
	off += n
	sorters := make([]Sorter, int(count))
	for i := range sorters {
		field, consumed := codec.ReadString(data, off)
		if consumed == 0 || field == "" {
			return nil, 0, fmt.Errorf("invalid binary sorter field at index %d", i)
		}
		off += consumed
		descending, consumed := codec.ReadBool(data, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("invalid binary sorter order at index %d", i)
		}
		off += consumed
		order := "asc"
		if descending {
			order = "desc"
		}
		sorters[i] = Sorter{Field: field, Order: order}
	}
	return sorters, off - start, nil
}

func filterOperatorID(operator string) int {
	for id := 1; id < len(filterOperators); id++ {
		if filterOperators[id] == operator {
			return id
		}
	}
	return 0
}

func encodeBinaryParam(enc *codec.Encoder, meta ParamMeta, value any) error {
	if meta.FieldID <= 0 {
		return fmt.Errorf("param %s: invalid field ID %d", meta.Name, meta.FieldID)
	}
	enc.WriteFieldHeader(meta.FieldID)
	if meta.Nullable {
		if value == nil {
			enc.WriteNull()
			return nil
		}
		enc.WritePresent()
	}
	if meta.IsList {
		return encodeBinaryListValue(enc, meta, value)
	}
	if encodeBinaryScalarValue(enc, meta, value) {
		return nil
	}
	return fmt.Errorf("param %s: expected %s, got %T", meta.Name, meta.Type, value)
}

func encodeBinaryScalarValue(enc *codec.Encoder, meta ParamMeta, value any) bool {
	switch meta.Type {
	case "Int":
		if v, ok := binaryInt(value); ok {
			enc.WriteInt(v)
			return true
		}
	case "Duration":
		if v, ok := binaryDuration(value); ok {
			enc.WriteInt(int64(v))
			return true
		}
	case "Float":
		if v, ok := binaryFloat(value); ok {
			enc.WriteFloat(v)
			return true
		}
	case "DateTime":
		return encodeDateTimeValue(enc, value)
	case "UUID":
		return encodeUUIDValue(enc, value)
	case "String", "Enum":
		if v, ok := value.(string); ok {
			enc.WriteString(v)
			return true
		}
	case "Decimal":
		if v, ok := binaryDecimal(value); ok {
			enc.WriteString(v)
			return true
		}
	case "Boolean":
		if v, ok := value.(bool); ok {
			enc.WriteBool(v)
			return true
		}
	case "Bytes":
		if v, ok := value.([]byte); ok {
			enc.WriteBytes(v)
			return true
		}
	case "JSON":
		if raw, ok := binaryJSON(value); ok {
			enc.WriteBytes(raw)
			return true
		}
	}
	return false
}

func binaryInt(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), v == float64(int64(v))
	}
	return 0, false
}

func binaryFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func binaryDuration(value any) (time.Duration, bool) {
	switch v := value.(type) {
	case time.Duration:
		return v, true
	case int64:
		return time.Duration(v), true
	case int:
		return time.Duration(v), true
	}
	return 0, false
}

func binaryDecimal(value any) (string, bool) {
	switch v := value.(type) {
	case decimal.Decimal:
		return v.String(), true
	case string:
		if _, err := decimal.NewFromString(v); err == nil {
			return v, true
		}
	}
	return "", false
}

func binaryJSON(value any) ([]byte, bool) {
	if raw, ok := value.(json.RawMessage); ok {
		return raw, json.Valid(raw)
	}
	raw, err := json.Marshal(value)
	return raw, err == nil
}

func encodeBinaryListValue(enc *codec.Encoder, meta ParamMeta, value any) error {
	values, ok := binaryListValues(value)
	if !ok {
		return fmt.Errorf("param %s: expected list of %s, got %T", meta.Name, meta.Type, value)
	}
	enc.WriteArrayHeader(len(values))
	switch meta.Type {
	case "Int":
		return encodeBinaryIntList(enc, meta, values, false)
	case "Duration":
		return encodeBinaryIntList(enc, meta, values, true)
	case "Float":
		return encodeBinaryFloatList(enc, meta, values)
	case "String", "Enum":
		return encodeBinaryStringList(enc, meta, values, false)
	case "Decimal":
		return encodeBinaryStringList(enc, meta, values, true)
	case "Boolean":
		return encodeBinaryBoolList(enc, meta, values)
	case "DateTime":
		return encodeBinaryDateTimeList(enc, meta, values)
	case "UUID":
		return encodeBinaryUUIDList(enc, meta, values)
	case "Bytes", "JSON":
		return encodeBinaryBytesList(enc, meta, values)
	default:
		return fmt.Errorf("param %s: unknown type %s", meta.Name, meta.Type)
	}
}

func encodeBinaryIntList(enc *codec.Encoder, meta ParamMeta, values []any, duration bool) error {
	for i, value := range values {
		if duration {
			parsed, valid := binaryDuration(value)
			if !valid {
				return binaryListElementError(meta, i, value)
			}
			enc.WriteInt(int64(parsed))
			continue
		}
		parsed, valid := binaryInt(value)
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteInt(parsed)
	}
	return nil
}

func encodeBinaryFloatList(enc *codec.Encoder, meta ParamMeta, values []any) error {
	for i, value := range values {
		parsed, valid := binaryFloat(value)
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteFloat(parsed)
	}
	return nil
}

func encodeBinaryStringList(enc *codec.Encoder, meta ParamMeta, values []any, decimalType bool) error {
	for i, value := range values {
		parsed, valid := value.(string)
		if decimalType {
			parsed, valid = binaryDecimal(value)
		}
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteString(parsed)
	}
	return nil
}

func encodeBinaryBoolList(enc *codec.Encoder, meta ParamMeta, values []any) error {
	for i, value := range values {
		parsed, valid := value.(bool)
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteBool(parsed)
	}
	return nil
}

func encodeBinaryDateTimeList(enc *codec.Encoder, meta ParamMeta, values []any) error {
	for i, value := range values {
		parsed, valid := binaryDateTime(value)
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteInt(parsed)
	}
	return nil
}

func encodeBinaryUUIDList(enc *codec.Encoder, meta ParamMeta, values []any) error {
	for i, value := range values {
		parsed, valid := binaryUUID(value)
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteUUID(parsed)
	}
	return nil
}

func encodeBinaryBytesList(enc *codec.Encoder, meta ParamMeta, values []any) error {
	for i, value := range values {
		var raw []byte
		var valid bool
		if meta.Type == "Bytes" {
			raw, valid = value.([]byte)
		} else {
			raw, valid = binaryJSON(value)
		}
		if !valid {
			return binaryListElementError(meta, i, value)
		}
		enc.WriteBytes(raw)
	}
	return nil
}

func binaryListElementError(meta ParamMeta, index int, value any) error {
	return fmt.Errorf("param %s[%d]: expected %s, got %T", meta.Name, index, meta.Type, value)
}

func binaryListValues(value any) ([]any, bool) {
	switch values := value.(type) {
	case []any:
		return values, true
	case []int:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []int64:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []float64:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []string:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []bool:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case [][]byte:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []time.Time:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []time.Duration:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []uuid.UUID:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case []decimal.Decimal:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	case [][16]byte:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out, true
	}
	return nil, false
}

// encodeDateTimeParam encodes a DateTime param as svarint(unix seconds).
// Accepts time.Time, int64, float64, or RFC3339 string. Skips on parse failure
// rather than silently encoding epoch 0.
func encodeDateTimeValue(enc *codec.Encoder, v any) bool {
	sec, ok := binaryDateTime(v)
	if ok {
		enc.WriteInt(sec)
	}
	return ok
}

func binaryDateTime(v any) (int64, bool) {
	switch tv := v.(type) {
	case time.Time:
		return tv.Unix(), true
	case int64:
		return tv, true
	case float64:
		return int64(tv), tv == float64(int64(tv))
	case string:
		if t, err := time.Parse(time.RFC3339, tv); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

// encodeUUIDParam encodes a UUID param as 16-byte fixed.
// Accepts canonical string, uuid.UUID, or [16]byte. Skips on parse failure
// rather than writing the zero UUID.
func encodeUUIDValue(enc *codec.Encoder, v any) bool {
	u, ok := binaryUUID(v)
	if ok {
		enc.WriteUUID(u)
	}
	return ok
}

func binaryUUID(v any) ([16]byte, bool) {
	switch tv := v.(type) {
	case string:
		if parsed, err := uuid.Parse(tv); err == nil {
			return [16]byte(parsed), true
		}
	case uuid.UUID:
		return [16]byte(tv), true
	case [16]byte:
		return tv, true
	}
	return [16]byte{}, false
}

// readBinaryParam reads a single typed parameter value from binary data.
func readBinaryParam(buf []byte, off int, meta ParamMeta) (any, int, error) {
	if meta.IsList {
		return readBinaryListParam(buf, off, meta)
	}
	switch meta.Type {
	case "Int":
		v, n := codec.ReadSvarint(buf, off)
		if n <= 0 {
			return nil, 0, fmt.Errorf("param %s: truncated int", meta.Name)
		}
		return v, n, nil
	case "Duration":
		v, n := codec.ReadSvarint(buf, off)
		if n <= 0 {
			return nil, 0, fmt.Errorf("param %s: truncated duration", meta.Name)
		}
		return time.Duration(v), n, nil
	case "Float":
		v, n := codec.ReadFixed64(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated float", meta.Name)
		}
		return v, n, nil
	case "DateTime":
		v, n := codec.ReadSvarint(buf, off)
		if n <= 0 {
			return nil, 0, fmt.Errorf("param %s: truncated datetime", meta.Name)
		}
		return time.Unix(v, 0).UTC(), n, nil
	case "UUID":
		u, n := codec.ReadUUID(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated uuid", meta.Name)
		}
		return uuid.UUID(u), n, nil
	case "String", "Enum":
		v, n := codec.ReadString(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated string", meta.Name)
		}
		return v, n, nil
	case "Decimal":
		v, n := codec.ReadString(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated decimal", meta.Name)
		}
		parsed, err := decimal.NewFromString(v)
		if err != nil {
			return nil, 0, fmt.Errorf("param %s: invalid decimal", meta.Name)
		}
		return parsed, n, nil
	case "Boolean":
		v, n := codec.ReadBool(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated bool", meta.Name)
		}
		return v, n, nil
	case "Bytes":
		v, n := codec.ReadBytes(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated bytes", meta.Name)
		}
		return v, n, nil
	case "JSON":
		v, n := codec.ReadBytes(buf, off)
		if n == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated JSON", meta.Name)
		}
		if !json.Valid(v) {
			return nil, 0, fmt.Errorf("param %s: invalid JSON", meta.Name)
		}
		return json.RawMessage(v), n, nil
	default:
		return nil, 0, fmt.Errorf("param %s: unknown type %s", meta.Name, meta.Type)
	}
}

func readBinaryListParam(buf []byte, off int, meta ParamMeta) (any, int, error) {
	count, n := codec.ReadArrayHeader(buf, off)
	if n == 0 {
		return nil, 0, fmt.Errorf("param %s: truncated array header", meta.Name)
	}
	if count > codec.MaxArrayElements {
		return nil, 0, fmt.Errorf("param %s: array size %d exceeds limit %d", meta.Name, count, codec.MaxArrayElements)
	}
	start := off
	off += n
	var value any
	var end int
	var err error
	switch meta.Type {
	case "Int":
		value, end, err = readBinaryIntArray(buf, off, count, meta, false)
	case "Duration":
		value, end, err = readBinaryIntArray(buf, off, count, meta, true)
	case "Float":
		value, end, err = readBinaryFloatArray(buf, off, count, meta)
	case "DateTime":
		value, end, err = readBinaryDateTimeArray(buf, off, count, meta)
	case "String", "Enum":
		value, end, err = readBinaryStringArray(buf, off, count, meta, false)
	case "Decimal":
		value, end, err = readBinaryStringArray(buf, off, count, meta, true)
	case "Boolean":
		value, end, err = readBinaryBoolArray(buf, off, count, meta)
	case "UUID":
		value, end, err = readBinaryUUIDArray(buf, off, count, meta)
	case "Bytes":
		value, end, err = readBinaryBytesArray(buf, off, count, meta, false)
	case "JSON":
		value, end, err = readBinaryBytesArray(buf, off, count, meta, true)
	default:
		return nil, 0, fmt.Errorf("param %s: unknown list type %s", meta.Name, meta.Type)
	}
	if err != nil {
		return nil, 0, err
	}
	return value, end - start, nil
}

func readBinaryIntArray(buf []byte, off, count int, meta ParamMeta, duration bool) (any, int, error) {
	if duration {
		values := make([]time.Duration, count)
		for i := range values {
			value, consumed := codec.ReadSvarint(buf, off)
			if consumed <= 0 {
				return nil, 0, fmt.Errorf("param %s: truncated duration array", meta.Name)
			}
			values[i], off = time.Duration(value), off+consumed
		}
		return values, off, nil
	}
	values := make([]int64, count)
	for i := range values {
		value, consumed := codec.ReadSvarint(buf, off)
		if consumed <= 0 {
			return nil, 0, fmt.Errorf("param %s: truncated int array", meta.Name)
		}
		values[i], off = value, off+consumed
	}
	return values, off, nil
}

func readBinaryFloatArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]float64, count)
	for i := range values {
		value, consumed := codec.ReadFixed64(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated float array", meta.Name)
		}
		values[i], off = value, off+consumed
	}
	return values, off, nil
}

func readBinaryDateTimeArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]time.Time, count)
	for i := range values {
		value, consumed := codec.ReadSvarint(buf, off)
		if consumed <= 0 {
			return nil, 0, fmt.Errorf("param %s: truncated datetime array", meta.Name)
		}
		values[i], off = time.Unix(value, 0).UTC(), off+consumed
	}
	return values, off, nil
}

func readBinaryStringArray(buf []byte, off, count int, meta ParamMeta, decimalType bool) (any, int, error) {
	if decimalType {
		return readBinaryDecimalArray(buf, off, count, meta)
	}
	values := make([]string, count)
	for i := range values {
		value, consumed := codec.ReadString(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated string array", meta.Name)
		}
		values[i], off = value, off+consumed
	}
	return values, off, nil
}

func readBinaryDecimalArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]decimal.Decimal, count)
	for i := range values {
		value, consumed := codec.ReadString(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated decimal array", meta.Name)
		}
		parsed, err := decimal.NewFromString(value)
		if err != nil {
			return nil, 0, fmt.Errorf("param %s[%d]: invalid decimal", meta.Name, i)
		}
		values[i], off = parsed, off+consumed
	}
	return values, off, nil
}

func readBinaryBoolArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]bool, count)
	for i := range values {
		value, consumed := codec.ReadBool(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated bool array", meta.Name)
		}
		values[i], off = value, off+consumed
	}
	return values, off, nil
}

func readBinaryUUIDArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]uuid.UUID, count)
	for i := range values {
		value, consumed := codec.ReadUUID(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated uuid array", meta.Name)
		}
		values[i], off = uuid.UUID(value), off+consumed
	}
	return values, off, nil
}

func readBinaryBytesArray(buf []byte, off, count int, meta ParamMeta, jsonType bool) (any, int, error) {
	if jsonType {
		return readBinaryJSONArray(buf, off, count, meta)
	}
	values := make([][]byte, count)
	for i := range values {
		value, consumed := codec.ReadBytes(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated bytes array", meta.Name)
		}
		values[i], off = value, off+consumed
	}
	return values, off, nil
}

func readBinaryJSONArray(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]json.RawMessage, count)
	for i := range values {
		value, consumed := codec.ReadBytes(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated JSON array", meta.Name)
		}
		if !json.Valid(value) {
			return nil, 0, fmt.Errorf("param %s[%d]: invalid JSON", meta.Name, i)
		}
		values[i], off = json.RawMessage(value), off+consumed
	}
	return values, off, nil
}
