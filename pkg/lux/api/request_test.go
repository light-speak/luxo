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
}
