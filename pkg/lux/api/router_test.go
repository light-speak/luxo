package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterBasic(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) (any, error) {
		id, _ := req.ParamInt("id")
		return map[string]any{"id": id, "name": "lin", "email": "lin@test.com"}, nil
	})

	body := `{"$api":"getUser","id":1}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["data"]; !ok {
		t.Fatal("response should have data field")
	}
}

func TestRouterWithSelect(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) (any, error) {
		return map[string]any{"id": 1, "name": "lin", "email": "lin@test.com"}, nil
	})

	body := `{"$api":"getUser","id":1,"$select":"name"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &resp)

	var data map[string]any
	json.Unmarshal(resp["data"], &data)
	if _, ok := data["email"]; ok {
		t.Error("email should be filtered out")
	}
	if data["name"] != "lin" {
		t.Error("name should be present")
	}
}

func TestRouterUnknownAPI(t *testing.T) {
	rt := NewRouter()
	body := `{"$api":"notExist"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestRouterWrongMethod(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodGet, "/luvia", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestRouterBadJSON(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRouterHandlerError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) (any, error) {
		return nil, fmt.Errorf("something went wrong")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestRouterEmptyBody(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(""))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRouterSerializeError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("bad", func(ctx context.Context, req *Request) (any, error) {
		return make(chan int), nil // not serializable
	})

	body := `{"$api":"bad"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
