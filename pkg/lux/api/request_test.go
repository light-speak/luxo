package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func makeReq(body string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	return r
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("read error")
}

func TestParseRequestReadError(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/luvia", errReader{})
	_, err := ParseRequest(r)
	if err == nil {
		t.Fatal("expected error for read failure")
	}
}

func TestParseRequestBasic(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"getUser","id":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.API != "getUser" {
		t.Errorf("API = %q, want getUser", req.API)
	}
	if req.Select != nil {
		t.Error("Select should be nil when $select not provided")
	}
	id, err := req.ParamInt("id")
	if err != nil || id != 1 {
		t.Errorf("id = %d, err = %v", id, err)
	}
}

func TestParseRequestWithSelect(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"getUser","id":1,"$select":"name,email"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Select) != 2 {
		t.Fatalf("Select = %d fields, want 2", len(req.Select))
	}
	if req.Select[0].Name != "name" || req.Select[1].Name != "email" {
		t.Error("Select fields wrong")
	}
}

func TestParseRequestWithNestedSelect(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"getUser","id":1,"$select":"name,posts{title}"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Select) != 2 {
		t.Fatalf("Select = %d fields, want 2", len(req.Select))
	}
	posts := req.Select[1]
	if posts.Name != "posts" || len(posts.Children) != 1 {
		t.Error("nested select wrong")
	}
}

func TestParseRequestMultipleParams(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"search","keyword":"hello","limit":10,"active":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kw, _ := req.ParamString("keyword")
	if kw != "hello" {
		t.Errorf("keyword = %q", kw)
	}
	lim, _ := req.ParamInt("limit")
	if lim != 10 {
		t.Errorf("limit = %d", lim)
	}
	act, _ := req.ParamBool("active")
	if !act {
		t.Error("active should be true")
	}
}

func TestParseRequestHasParam(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"test","a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !req.HasParam("a") {
		t.Error("should have param a")
	}
	if req.HasParam("b") {
		t.Error("should not have param b")
	}
}

func TestParamIsNullJSONMode(t *testing.T) {
	req, err := ParseRequest(makeReq(`{"$api":"test","nullValue":null,"value":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !req.ParamIsNull("nullValue") || req.ParamIsNull("value") || req.ParamIsNull("missing") {
		t.Fatal("JSON null-state detection is incorrect")
	}
}

func TestAssignBinaryNativeParams(t *testing.T) {
	instant := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	duration := 3 * time.Second
	amount := decimal.RequireFromString("12.50")
	raw := json.RawMessage(`{"ok":true}`)

	var gotTime time.Time
	var gotTimePtr *time.Time
	var gotUUID uuid.UUID
	var gotUUIDPtr *uuid.UUID
	var gotRaw json.RawMessage
	var gotDuration time.Duration
	var gotDurationPtr *time.Duration
	var gotDecimal decimal.Decimal
	var gotDecimalPtr *decimal.Decimal
	tests := []struct {
		name   string
		value  any
		target any
	}{
		{name: "time", value: instant, target: &gotTime},
		{name: "time pointer", value: instant, target: &gotTimePtr},
		{name: "UUID", value: id, target: &gotUUID},
		{name: "UUID pointer", value: id, target: &gotUUIDPtr},
		{name: "JSON", value: raw, target: &gotRaw},
		{name: "duration", value: duration, target: &gotDuration},
		{name: "duration pointer", value: duration, target: &gotDurationPtr},
		{name: "decimal", value: amount, target: &gotDecimal},
		{name: "decimal pointer", value: amount, target: &gotDecimalPtr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !assignBinaryNativeParam(test.value, test.target) {
				t.Fatalf("%s was not assigned", test.name)
			}
		})
	}
	if gotTime != instant || gotTimePtr == nil || *gotTimePtr != instant {
		t.Fatal("time assignment failed")
	}
	if gotUUID != id || gotUUIDPtr == nil || *gotUUIDPtr != id {
		t.Fatal("UUID assignment failed")
	}
	if !reflect.DeepEqual(gotRaw, raw) || gotDuration != duration || gotDurationPtr == nil || *gotDurationPtr != duration {
		t.Fatal("JSON or duration assignment failed")
	}
	if !gotDecimal.Equal(amount) || gotDecimalPtr == nil || !gotDecimalPtr.Equal(amount) {
		t.Fatal("decimal assignment failed")
	}
	if assignBinaryNativeParam("wrong", &gotTime) {
		t.Fatal("mismatched native value was assigned")
	}
}

func TestAssignBinarySliceParams(t *testing.T) {
	instant := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	amount := decimal.RequireFromString("12.50")
	values := []struct {
		name   string
		value  any
		target any
	}{
		{name: "bytes", value: []byte{1, 2}, target: new([]byte)},
		{name: "integers", value: []int64{1, 2}, target: new([]int64)},
		{name: "floats", value: []float64{1.5}, target: new([]float64)},
		{name: "strings", value: []string{"a"}, target: new([]string)},
		{name: "booleans", value: []bool{true}, target: new([]bool)},
		{name: "times", value: []time.Time{instant}, target: new([]time.Time)},
		{name: "durations", value: []time.Duration{time.Second}, target: new([]time.Duration)},
		{name: "UUIDs", value: []uuid.UUID{id}, target: new([]uuid.UUID)},
		{name: "decimals", value: []decimal.Decimal{amount}, target: new([]decimal.Decimal)},
		{name: "byte arrays", value: [][]byte{{1, 2}}, target: new([][]byte)},
		{name: "JSON values", value: []json.RawMessage{json.RawMessage(`{"ok":true}`)}, target: new([]json.RawMessage)},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			if !assignBinarySliceParam(test.value, test.target) {
				t.Fatalf("%s was not assigned", test.name)
			}
			if !reflect.DeepEqual(reflect.ValueOf(test.target).Elem().Interface(), test.value) {
				t.Fatalf("%s assignment mismatch", test.name)
			}
		})
	}
	var integers []int64
	if assignBinarySliceParam([]string{"wrong"}, &integers) || assignBinarySliceParam(1, new(struct{})) {
		t.Fatal("mismatched slice value was assigned")
	}
}

func TestParseRequestEmptyBody(t *testing.T) {
	_, err := ParseRequest(makeReq(""))
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestParseRequestInvalidJSON(t *testing.T) {
	_, err := ParseRequest(makeReq("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseRequestMissingAPI(t *testing.T) {
	_, err := ParseRequest(makeReq(`{"id":1}`))
	if err == nil {
		t.Fatal("expected error for missing $api")
	}
}

func TestParseRequestEmptyAPI(t *testing.T) {
	_, err := ParseRequest(makeReq(`{"$api":""}`))
	if err == nil {
		t.Fatal("expected error for empty $api")
	}
}

func TestParseRequestAPINotString(t *testing.T) {
	_, err := ParseRequest(makeReq(`{"$api":123}`))
	if err == nil {
		t.Fatal("expected error for non-string $api")
	}
}

func TestParseRequestSelectNotString(t *testing.T) {
	_, err := ParseRequest(makeReq(`{"$api":"test","$select":123}`))
	if err == nil {
		t.Fatal("expected error for non-string $select")
	}
}

func TestParseRequestSelectInvalid(t *testing.T) {
	_, err := ParseRequest(makeReq(`{"$api":"test","$select":"posts{}"}`))
	if err == nil {
		t.Fatal("expected error for invalid $select")
	}
}

func TestParamIntWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","id":"notint"}`))
	_, err := req.ParamInt("id")
	if err == nil {
		t.Fatal("expected error for string as int")
	}
}

func TestParamStringWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","name":123}`))
	_, err := req.ParamString("name")
	if err == nil {
		t.Fatal("expected error for int as string")
	}
}

func TestParamBoolWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","flag":"yes"}`))
	_, err := req.ParamBool("flag")
	if err == nil {
		t.Fatal("expected error for string as bool")
	}
}

func TestParamMissing(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test"}`))
	_, err := req.ParamInt("id")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamString("name")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamBool("flag")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamFloat("price")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamDateTime("date")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamIntArray("ids")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	_, err = req.ParamStringArray("tags")
	if err == nil {
		t.Fatal("expected error for missing param")
	}
	var target struct{ X int }
	err = req.ParamJSON("obj", &target)
	if err == nil {
		t.Fatal("expected error for missing param")
	}
}

func TestParamIntArray(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","ids":[1,2,3]}`))
	ids, err := req.ParamIntArray("ids")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("ids = %v, want [1 2 3]", ids)
	}
}

func TestParamIntArrayWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","ids":"not_array"}`))
	_, err := req.ParamIntArray("ids")
	if err == nil {
		t.Fatal("expected error for string as int array")
	}
}

func TestParamStringArray(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","tags":["a","b"]}`))
	tags, err := req.ParamStringArray("tags")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", tags)
	}
}

func TestParamStringArrayWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","tags":123}`))
	_, err := req.ParamStringArray("tags")
	if err == nil {
		t.Fatal("expected error for int as string array")
	}
}

func TestParamFloat(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","price":99.5}`))
	v, err := req.ParamFloat("price")
	if err != nil {
		t.Fatal(err)
	}
	if v != 99.5 {
		t.Errorf("price = %f, want 99.5", v)
	}
}

func TestParamFloatWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","price":"abc"}`))
	_, err := req.ParamFloat("price")
	if err == nil {
		t.Fatal("expected error for string as float")
	}
}

func TestParamDateTime(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","date":"2026-04-13T10:00:00Z"}`))
	v, err := req.ParamDateTime("date")
	if err != nil {
		t.Fatal(err)
	}
	if v.Year() != 2026 || v.Month() != 4 || v.Day() != 13 {
		t.Errorf("date = %v", v)
	}
}

func TestParamDateTimeWrongFormat(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","date":"2026-04-13"}`))
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("expected error for non-RFC3339 format")
	}
}

func TestParamDateTimeWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","date":12345}`))
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("expected error for number as datetime")
	}
}

func TestParamJSON(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","input":{"name":"lin","age":30}}`))
	var target struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	if err := req.ParamJSON("input", &target); err != nil {
		t.Fatal(err)
	}
	if target.Name != "lin" || target.Age != 30 {
		t.Errorf("target = %+v", target)
	}
}

func TestParamJSONDecodesStructuredBinaryJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	req := &Request{
		paramNames: []string{"input"},
		paramCount: 1,
		paramSlots: [16]any{json.RawMessage(`{"name":"luxo"}`)},
	}
	var got input
	if err := req.ParamJSON("input", &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "luxo" {
		t.Fatalf("input = %#v", got)
	}
}

func TestParamJSONAssignsNativeBinaryTypes(t *testing.T) {
	wantTime := time.Unix(1_700_000_000, 0).UTC()
	wantUUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	req := &Request{
		paramNames: []string{"at", "id"},
		paramCount: 2,
		paramSlots: [16]any{wantTime, wantUUID},
	}
	var gotTime time.Time
	var gotUUID uuid.UUID
	if err := req.ParamJSON("at", &gotTime); err != nil {
		t.Fatal(err)
	}
	if err := req.ParamJSON("id", &gotUUID); err != nil {
		t.Fatal(err)
	}
	if !gotTime.Equal(wantTime) || gotUUID != wantUUID {
		t.Fatalf("at/id = %v/%v", gotTime, gotUUID)
	}
}

func TestParamJSONOptionalMissing(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test"}`))

	var s *string
	if err := req.ParamJSONOptional("name", &s); err != nil {
		t.Fatalf("missing nullable string param should not error: %v", err)
	}
	if s != nil {
		t.Errorf("missing param should leave target nil, got %v", *s)
	}

	var i *int64
	if err := req.ParamJSONOptional("count", &i); err != nil {
		t.Fatalf("missing nullable int param should not error: %v", err)
	}
	var f *float64
	if err := req.ParamJSONOptional("ratio", &f); err != nil {
		t.Fatalf("missing nullable float param should not error: %v", err)
	}
	var b *bool
	if err := req.ParamJSONOptional("flag", &b); err != nil {
		t.Fatalf("missing nullable bool param should not error: %v", err)
	}
	var tm *time.Time
	if err := req.ParamJSONOptional("at", &tm); err != nil {
		t.Fatalf("missing nullable datetime param should not error: %v", err)
	}
	var d *time.Duration
	if err := req.ParamJSONOptional("ttl", &d); err != nil {
		t.Fatalf("missing nullable duration param should not error: %v", err)
	}
}

func TestParamJSONNullableNull(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","name":null}`))
	var s *string
	if err := req.ParamJSONNullable("name", &s); err != nil {
		t.Fatalf("null nullable param should not error: %v", err)
	}
	if s != nil {
		t.Errorf("null param should leave target nil, got %v", *s)
	}
}

func TestParamJSONNullableRequiresPresence(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test"}`))
	var value *string
	if err := req.ParamJSONNullable("name", &value); err == nil {
		t.Fatal("required nullable param should reject absence")
	}
}

func TestParamJSONRejectsNullForNonNullableTarget(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","name":null}`))
	var value string
	if err := req.ParamJSON("name", &value); err == nil {
		t.Fatal("required non-null param should reject null")
	}
	if err := req.ParamJSONOptional("name", &value); err == nil {
		t.Fatal("optional non-null param should reject explicit null")
	}
}

func TestParamJSONRequiredStillMissing(t *testing.T) {
	// Required custom-type params (single-pointer struct targets) must still
	// report missing.
	req, _ := ParseRequest(makeReq(`{"$api":"test"}`))
	var target struct {
		Name string `json:"name"`
	}
	if err := req.ParamJSON("input", &target); err == nil {
		t.Fatal("missing required param should error")
	}
}

func TestParamJSONWrongType(t *testing.T) {
	req, _ := ParseRequest(makeReq(`{"$api":"test","input":"not_object"}`))
	var target struct {
		Name string `json:"name"`
	}
	err := req.ParamJSON("input", &target)
	if err == nil {
		t.Fatal("expected error for string as struct")
	}
}

func TestParseRequestWithFilters(t *testing.T) {
	body := `{"$api":"listUsers","$filters":[{"field":"role","op":"eq","value":"ADMIN"}]}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Filters) != 1 {
		t.Fatalf("filters = %d, want 1", len(req.Filters))
	}
	if req.Filters[0].Field != "role" || req.Filters[0].Operator != "eq" || req.Filters[0].Value != "ADMIN" {
		t.Errorf("filter = %+v", req.Filters[0])
	}
}

func TestParseRequestWithScalarFilterValues(t *testing.T) {
	body := `{"$api":"listNodes","$filters":[{"field":"projectId","op":"eq","value":42},{"field":"enabled","op":"eq","value":true}]}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Filters) != 2 || req.Filters[0].Value != "42" || req.Filters[1].Value != "true" {
		t.Fatalf("filters = %+v", req.Filters)
	}
}

func TestFilterRejectsNonScalarValues(t *testing.T) {
	tests := []string{
		`{"field":"id","op":"eq"}`,
		`{"field":"id","op":"eq","value":null}`,
		`{"field":"id","op":"eq","value":{}}`,
		`{"field":"id","op":"eq","value":[]}`,
	}
	for _, input := range tests {
		var filter Filter
		if err := json.Unmarshal([]byte(input), &filter); err == nil {
			t.Errorf("json.Unmarshal(%s) succeeded, want error", input)
		}
	}
}

func TestFilterRejectsMalformedJSON(t *testing.T) {
	var filter Filter
	if err := filter.UnmarshalJSON([]byte(`{"field":`)); err == nil {
		t.Fatal("malformed filter JSON succeeded")
	}
	if _, err := parseFilterValue([]byte{'"', '\\', 'x', '"'}); err == nil {
		t.Fatal("malformed JSON string succeeded")
	}
}

func TestParseRequestWithSorters(t *testing.T) {
	body := `{"$api":"listUsers","$sorters":[{"field":"name","order":"desc"}]}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Sorters) != 1 {
		t.Fatalf("sorters = %d, want 1", len(req.Sorters))
	}
	if req.Sorters[0].Field != "name" || req.Sorters[0].Order != "desc" {
		t.Errorf("sorter = %+v", req.Sorters[0])
	}
}

func TestParseRequestPagination(t *testing.T) {
	body := `{"$api":"listUsers","page":3,"pageSize":50}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if req.Page != 3 {
		t.Errorf("page = %d, want 3", req.Page)
	}
	if req.PageSize != 50 {
		t.Errorf("pageSize = %d, want 50", req.PageSize)
	}
	// page/pageSize should NOT be in Params
	if req.HasParam("page") || req.HasParam("pageSize") {
		t.Error("page/pageSize should not be in Params")
	}
}

func TestParseRequestPaginationDefaults(t *testing.T) {
	body := `{"$api":"listUsers"}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if req.Page != 1 {
		t.Errorf("default page = %d, want 1", req.Page)
	}
	if req.PageSize != 20 {
		t.Errorf("default pageSize = %d, want 20", req.PageSize)
	}
}

func TestParseRequestPaginationClamp(t *testing.T) {
	body := `{"$api":"listUsers","page":-1,"pageSize":999}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if req.Page != 1 {
		t.Errorf("clamped page = %d, want 1", req.Page)
	}
	if req.PageSize != 20 {
		t.Errorf("clamped pageSize = %d, want 20 (>100 resets)", req.PageSize)
	}
}

func TestParseRequestBadFilters(t *testing.T) {
	body := `{"$api":"test","$filters":"not_array"}`
	_, err := ParseRequest(makeReq(body))
	if err == nil {
		t.Fatal("expected error for bad $filters")
	}
}

func TestParseRequestBadSorters(t *testing.T) {
	body := `{"$api":"test","$sorters":"not_array"}`
	_, err := ParseRequest(makeReq(body))
	if err == nil {
		t.Fatal("expected error for bad $sorters")
	}
}

func TestParseRequestRejectsInvalidListControls(t *testing.T) {
	tests := []string{
		`{"$api":"test","$filters":[{"field":"","op":"eq","value":"x"}]}`,
		`{"$api":"test","$filters":[{"field":"name","op":"unknown","value":"x"}]}`,
		`{"$api":"test","$sorters":[{"field":"","order":"asc"}]}`,
		`{"$api":"test","$sorters":[{"field":"name","order":"sideways"}]}`,
	}
	for _, body := range tests {
		if _, err := ParseRequest(makeReq(body)); err == nil {
			t.Fatalf("invalid list controls accepted: %s", body)
		}
	}
}

func TestParseRequestReservedNotInParams(t *testing.T) {
	body := `{"$api":"test","$filters":[],"$sorters":[],"page":1,"pageSize":10,"name":"lin"}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	if !req.HasParam("name") {
		t.Error("name should be in params")
	}
	if req.HasParam("$filters") || req.HasParam("$sorters") || req.HasParam("page") {
		t.Error("reserved fields should not be in params")
	}
}

func TestPutBodyDiscardOversized(t *testing.T) {
	// Small buffer should be returned to pool
	small := make([]byte, 0, 1024)
	bp := &small
	putBody(bp)
	// No panic means success

	// Large buffer (>1MB) should be discarded, not pooled
	large := make([]byte, 0, 2<<20) // 2MB
	bp2 := &large
	putBody(bp2)
	// No panic means success
}

func TestParseRequestJSONBodySizeLimit(t *testing.T) {
	// Body exceeding MaxJSONBodySize should be rejected
	bigBody := strings.Repeat("x", MaxJSONBodySize+1)
	r := makeReq(bigBody)
	_, err := ParseRequest(r)
	if err == nil {
		t.Fatal("expected error for oversized JSON body")
	}
	if !strings.Contains(err.Error(), "JSON body exceeds") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseRequestFiltersSortersLimit(t *testing.T) {
	// Build a request with >1000 filters
	var filters []string
	for i := 0; i < 1001; i++ {
		filters = append(filters, fmt.Sprintf(`{"field":"f%d","op":"=","value":"v"}`, i))
	}
	body := fmt.Sprintf(`{"$api":"listUsers","$filters":[%s]}`, strings.Join(filters, ","))
	r := makeReq(body)
	_, err := ParseRequest(r)
	if err == nil {
		t.Fatal("expected error for >1000 filters")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- SetParamSlot accumulation tests ---

func TestSetParamSlotAccumulateInt(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"count", "keys"}, 2)

	// First write to slot 1
	req.SetParamSlot(1, int64(10))
	// Second write — should accumulate to []int64
	req.SetParamSlot(1, int64(20))
	// Third write — should append
	req.SetParamSlot(1, int64(30))

	ids, err := req.ParamIntArray("keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Fatalf("ids = %v, want [10 20 30]", ids)
	}
}

func TestSetParamSlotAccumulateString(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"tags"}, 1)

	req.SetParamSlot(0, "a")
	req.SetParamSlot(0, "b")
	req.SetParamSlot(0, "c")

	strs, err := req.ParamStringArray("tags")
	if err != nil {
		t.Fatal(err)
	}
	if len(strs) != 3 || strs[0] != "a" || strs[1] != "b" || strs[2] != "c" {
		t.Fatalf("strs = %v, want [a b c]", strs)
	}
}

func TestSetParamSlotSingleInt(t *testing.T) {
	// Single write should still work as int64, not []int64
	req := &Request{}
	req.SetBinaryParams([]string{"id"}, 1)
	req.SetParamSlot(0, int64(42))

	id, err := req.ParamInt("id")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestSetParamSlotOutOfBounds(t *testing.T) {
	req := &Request{}
	// Should not panic
	req.SetParamSlot(-1, int64(1))
	req.SetParamSlot(16, int64(1))
	req.SetParamSlot(99, int64(1))
}

func TestSetParamSlotTypeMismatch(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"val"}, 1)
	// Write int then string — type mismatch should overwrite
	req.SetParamSlot(0, int64(1))
	req.SetParamSlot(0, "hello")
	if v, ok := req.findParam("val"); !ok || v != "hello" {
		t.Fatalf("type mismatch should overwrite, got %v", v)
	}
}

// --- parseListParams additional paths ---

func TestParseRequestBadPageType(t *testing.T) {
	body := `{"$api":"test","page":"not-int"}`
	_, err := ParseRequest(makeReq(body))
	if err == nil {
		t.Fatal("expected error for non-integer page")
	}
	if !strings.Contains(err.Error(), "page must be an integer") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseRequestBadPageSizeType(t *testing.T) {
	body := `{"$api":"test","pageSize":"not-int"}`
	_, err := ParseRequest(makeReq(body))
	if err == nil {
		t.Fatal("expected error for non-integer pageSize")
	}
	if !strings.Contains(err.Error(), "pageSize must be an integer") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseRequestSortersLimit(t *testing.T) {
	var sorters []string
	for i := 0; i < 101; i++ {
		sorters = append(sorters, fmt.Sprintf(`{"field":"f%d","order":"asc"}`, i))
	}
	body := fmt.Sprintf(`{"$api":"test","$sorters":[%s]}`, strings.Join(sorters, ","))
	_, err := ParseRequest(makeReq(body))
	if err == nil {
		t.Fatal("expected error for >100 sorters")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseRequestPageSizeZero(t *testing.T) {
	body := `{"$api":"test","pageSize":0}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	// pageSize 0 means return all
	if req.PageSize != 0 {
		t.Errorf("pageSize should be 0 (return all), got %d", req.PageSize)
	}
}

func TestParseRequestPageSizeNegative(t *testing.T) {
	body := `{"$api":"test","pageSize":-5}`
	req, err := ParseRequest(makeReq(body))
	if err != nil {
		t.Fatal(err)
	}
	// Negative pageSize resets to 20
	if req.PageSize != 20 {
		t.Errorf("negative pageSize should reset to 20, got %d", req.PageSize)
	}
}

// --- ParamString binary mode wrong type ---

func TestParamStringBinaryWrongType(t *testing.T) {
	req := &Request{
		paramNames: []string{"name"},
		paramCount: 1,
		paramSlots: [16]any{int64(123)}, // wrong type
	}
	_, err := req.ParamString("name")
	if err == nil {
		t.Fatal("should error when binary param is wrong type")
	}
}

// --- readBody size limit ---

func TestReadBodySizeLimit(t *testing.T) {
	// Create a reader that produces more than MaxRequestBodySize
	bigReader := strings.NewReader(strings.Repeat("x", MaxRequestBodySize+1))
	_, _, err := readBody(bigReader)
	if err == nil {
		t.Fatal("should error on oversized body")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ParamJSON binary nil value ---

func TestParamJSONBinaryNilValue(t *testing.T) {
	req := &Request{
		paramNames: []string{"data"},
		paramCount: 1,
		paramSet:   1,
		paramNull:  1,
	}
	var v any
	err := req.ParamJSONOptionalNullable("data", &v)
	if err != nil {
		t.Fatal("nil param should not error (nullable)")
	}
}

type requestTestEnum string

func TestParamJSONBinaryConvertsNamedAndByteTypes(t *testing.T) {
	req := &Request{
		paramNames: []string{"roles", "payload"},
		paramCount: 2,
		paramSlots: [16]any{[]string{"ADMIN", "MEMBER"}, []byte{1, 2, 3}},
	}
	var roles []requestTestEnum
	if err := req.ParamJSON("roles", &roles); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roles, []requestTestEnum{"ADMIN", "MEMBER"}) {
		t.Fatalf("roles = %#v", roles)
	}
	var payload []byte
	if err := req.ParamJSON("payload", &payload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, []byte{1, 2, 3}) {
		t.Fatalf("payload = %v", payload)
	}
}

func TestHasParamBinaryDistinguishesOmittedValues(t *testing.T) {
	req := &Request{
		paramNames: []string{"present", "omitted"},
		paramCount: 2,
		paramSlots: [16]any{int64(1), nil},
	}
	if !req.HasParam("present") {
		t.Fatal("present parameter was not found")
	}
	if req.HasParam("omitted") {
		t.Fatal("omitted parameter must not be reported as present")
	}
}

func TestSetParamSlotFloatOverwrite(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"val"}, 1)
	req.SetParamSlot(0, 1.0)
	req.SetParamSlot(0, 2.0) // float → overwrite (not accumulated)
	if v, ok := req.findParam("val"); !ok || v != 2.0 {
		t.Fatalf("float should overwrite, got %v", v)
	}
}

func TestParamIntArraySingleElement(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"keys"}, 1)
	req.SetParamSlot(0, int64(42)) // single int, not []int64
	ids, err := req.ParamIntArray("keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("single int should wrap to []int64, got %v", ids)
	}
}

func TestParamStringArraySingleElement(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"tags"}, 1)
	req.SetParamSlot(0, "hello") // single string
	strs, err := req.ParamStringArray("tags")
	if err != nil {
		t.Fatal(err)
	}
	if len(strs) != 1 || strs[0] != "hello" {
		t.Fatalf("single string should wrap to []string, got %v", strs)
	}
}

func TestParamUUIDArray(t *testing.T) {
	first := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	second := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	req := &Request{}
	req.SetBinaryParams([]string{"keys"}, 1)
	req.SetParamSlot(0, []uuid.UUID{first, second})

	keys, err := req.ParamUUIDArray("keys")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []uuid.UUID{first, second}) {
		t.Fatalf("UUID keys = %v", keys)
	}
}

func TestParamUUIDArrayVariants(t *testing.T) {
	value := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	t.Run("single binary value", func(t *testing.T) {
		req := &Request{}
		req.SetBinaryParams([]string{"keys"}, 1)
		req.SetParamSlot(0, value)
		got, err := req.ParamUUIDArray("keys")
		if err != nil || len(got) != 1 || got[0] != value {
			t.Fatalf("ParamUUIDArray() = %v, %v", got, err)
		}
	})

	t.Run("missing binary value", func(t *testing.T) {
		req := &Request{}
		req.SetBinaryParams([]string{"keys"}, 1)
		if _, err := req.ParamUUIDArray("keys"); err == nil {
			t.Fatal("missing binary UUID list was accepted")
		}
	})

	t.Run("json value", func(t *testing.T) {
		req := &Request{Params: map[string]json.RawMessage{"keys": json.RawMessage(fmt.Sprintf("[%q]", value.String()))}}
		got, err := req.ParamUUIDArray("keys")
		if err != nil || len(got) != 1 || got[0] != value {
			t.Fatalf("ParamUUIDArray() = %v, %v", got, err)
		}
	})

	for name, req := range map[string]*Request{
		"missing json value": {Params: map[string]json.RawMessage{}},
		"invalid json value": {Params: map[string]json.RawMessage{"keys": json.RawMessage(`[1]`)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := req.ParamUUIDArray("keys"); err == nil {
				t.Fatal("invalid UUID list was accepted")
			}
		})
	}
}
