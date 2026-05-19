package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
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
