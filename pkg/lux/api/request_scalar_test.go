package api

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

func TestRequiredJSONParamsRejectNull(t *testing.T) {
	readers := map[string]func(*Request) error{
		"Int":      func(r *Request) error { _, err := r.ParamInt("v"); return err },
		"String":   func(r *Request) error { _, err := r.ParamString("v"); return err },
		"Boolean":  func(r *Request) error { _, err := r.ParamBool("v"); return err },
		"Float":    func(r *Request) error { _, err := r.ParamFloat("v"); return err },
		"DateTime": func(r *Request) error { _, err := r.ParamDateTime("v"); return err },
		"[Int]":    func(r *Request) error { _, err := r.ParamIntArray("v"); return err },
		"[String]": func(r *Request) error { _, err := r.ParamStringArray("v"); return err },
		"[UUID]":   func(r *Request) error { _, err := r.ParamUUIDArray("v"); return err },
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			rawValues := []string{"null", " \t\r\nnull \t\r\n"}
			if name[0] == '[' {
				rawValues = append(rawValues, "[null]")
			}
			for _, raw := range rawValues {
				req := &Request{Params: map[string]json.RawMessage{"v": []byte(raw)}}
				if err := read(req); err == nil {
					t.Errorf("accepted %s for required %s", raw, name)
				}
			}
		})
	}
}

func TestJSONStringUUIDArrayEscapesAndOwnership(t *testing.T) {
	want := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	raw := []byte(`["550e8400-e29b-41d4-a716-446655440000","\u003550e8400-e29b-41d4-a716-446655440000"]`)
	req := &Request{Params: map[string]json.RawMessage{"v": raw}}
	got, err := req.ParamUUIDArray("v")
	clear(raw)
	if err != nil || !slices.Equal(got, []uuid.UUID{want, want}) {
		t.Fatalf("UUID array changed values: %v / %v", got, err)
	}
	for _, raw := range []string{`["\uD800"]`, `[null]`, `["bad"]`, `["\u0062ad"]`, `["ok"`, `["550e8400-e29b-41d4-a716-446655440000"]true`} {
		req.Params["v"] = []byte(raw)
		if got, err := req.ParamUUIDArray("v"); err == nil || got != nil {
			t.Errorf("accepted partial/invalid UUID array %q: %v / %v", raw, got, err)
		}
	}
}

func TestDirectJSONStringParamsRejectInvalidUnicode(t *testing.T) {
	for _, raw := range []string{`"\uD800"`, `"\uDC00"`, "\"\xff\""} {
		req := &Request{Params: map[string]json.RawMessage{"v": []byte(raw)}}
		if _, err := req.ParamString("v"); err == nil {
			t.Errorf("accepted invalid string %q", raw)
		}
		req.Params["v"] = []byte("[" + raw + "]")
		if _, err := req.ParamStringArray("v"); err == nil {
			t.Errorf("accepted invalid string array %q", raw)
		}
	}
}

func TestJSONRequestMetadataRejectsNull(t *testing.T) {
	for _, body := range []string{
		`{"$api":null}`, `{"$api":"test","$select":null}`,
		`{"$api":"test","page":null}`, `{"$api":"test","pageSize":null}`,
	} {
		if req, err := ParseRequest(makeReq(body)); err == nil || req != nil {
			t.Errorf("accepted null metadata %s: %v / %v", body, req, err)
		}
	}
}

func TestJSONRequestMetadataRetainsValues(t *testing.T) {
	maxPage := int(^uint(0) >> 1)
	pageToken := strconv.Itoa(maxPage)
	for _, body := range []string{
		`{"$api":"test","$select":"id, owner { name }","page":` + pageToken + `,"pageSize":0}`,
		`{"$api":"\u0074est","$select":"\u0069d, owner { name }","page": ` + pageToken + ` ,"pageSize": 0 }`,
	} {
		req, err := ParseRequest(makeReq(body))
		if err != nil {
			t.Fatal(err)
		}
		if req.API != "test" || len(req.Select) != 2 || req.Select[1].Name != "owner" ||
			len(req.Select[1].Children) != 1 || req.Select[1].Children[0].Name != "name" ||
			req.Page != maxPage || req.PageSize != 0 || !req.pageSizeSet {
			t.Fatalf("metadata changed: %+v", req)
		}
	}
	for _, body := range []string{`{"$api":"test"}`, `{"$api":"test","page":-1,"pageSize":-1}`} {
		req, err := ParseRequest(makeReq(body))
		if err != nil || req.Page != 1 || req.PageSize != defaultPageSize {
			t.Fatalf("pagination defaults changed: %+v / %v", req, err)
		}
	}
	overflow := strconv.FormatUint(uint64(maxPage)+1, 10)
	if req, err := ParseRequest(makeReq(`{"$api":"test","page":` + overflow + `}`)); err == nil || req != nil {
		t.Errorf("accepted overflowing native page: %v / %v", req, err)
	}
}
