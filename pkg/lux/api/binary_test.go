package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/errors"
)

func TestBinaryRequestRoundTrip(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	reg.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	// Encode binary request
	body := EncodeBinaryRequest(1, map[string]any{"id": 42}, reg.paramOrder["getUser"])

	// Decode
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "getUser" {
		t.Fatalf("API = %q, want getUser", req.API)
	}
	id, err := req.ParamInt("id")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestBinaryRequestMultipleParams(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("createTask", 2)
	reg.RegisterParams("createTask", []ParamMeta{
		{Name: "title", Type: "String", FieldID: 1},
		{Name: "projectId", Type: "Int", FieldID: 2},
		{Name: "priority", Type: "Int", FieldID: 3},
	})

	body := EncodeBinaryRequest(2, map[string]any{
		"title":     "Test task",
		"projectId": 1,
		"priority":  3,
	}, reg.paramOrder["createTask"])

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "createTask" {
		t.Fatalf("API = %q", req.API)
	}
	title, _ := req.ParamString("title")
	if title != "Test task" {
		t.Fatalf("title = %q", title)
	}
	pid, _ := req.ParamInt("projectId")
	if pid != 1 {
		t.Fatalf("projectId = %d", pid)
	}
}

func TestBinaryModeHTTP(t *testing.T) {
	rt := NewRouter()

	// Register handler
	rt.Handle("ping", func(ctx context.Context, req *Request) error {
		req.Buf.AppendString(`"pong"`)
		return nil
	})

	// Register in binary registry
	rt.Registry.Register("ping", 99)
	rt.Registry.RegisterParams("ping", nil)

	// Build binary request
	body := EncodeBinaryRequest(99, nil, nil)

	// Send as HTTP with X-Luxo-Mode: binary
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(string(body)))
	r.Header.Set("X-Luxo-Mode", "binary")
	w := httptest.NewRecorder()

	rt.ServeHTTP(w, r)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Luxo-Mode") != "binary" {
		t.Fatal("response should have X-Luxo-Mode: binary")
	}
	if resp.Header.Get("Content-Type") != "application/x-luxo" {
		t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != `"pong"` {
		t.Fatalf("body = %q", string(respBody))
	}
}

func TestBinaryModeUnknownAPI(t *testing.T) {
	reg := NewAPIRegistry()
	// API ID 999 not registered
	body := EncodeBinaryRequest(999, nil, nil)
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on unknown API ID")
	}
}

func TestBinaryRequestEmpty(t *testing.T) {
	reg := NewAPIRegistry()
	_, err := reg.ParseBinaryRequest(nil)
	if err == nil {
		t.Fatal("should error on empty body")
	}
}

func TestTypedParamsInt(t *testing.T) {
	req := &Request{paramNames: []string{"id"}, paramCount: 1, paramSlots: [16]any{int64(42)}}
	v, err := req.ParamInt("id")
	if err != nil || v != 42 {
		t.Fatalf("got %d, err=%v", v, err)
	}
	_, err = req.ParamInt("missing")
	if err == nil {
		t.Fatal("should error on missing")
	}
}

func TestTypedParamsString(t *testing.T) {
	req := &Request{paramNames: []string{"name"}, paramCount: 1, paramSlots: [16]any{"alice"}}
	v, err := req.ParamString("name")
	if err != nil || v != "alice" {
		t.Fatalf("got %q, err=%v", v, err)
	}
}

func TestTypedParamsFloat(t *testing.T) {
	req := &Request{paramNames: []string{"amount"}, paramCount: 1, paramSlots: [16]any{99.5}}
	v, err := req.ParamFloat("amount")
	if err != nil || v != 99.5 {
		t.Fatalf("got %f, err=%v", v, err)
	}
}

func TestTypedParamsBool(t *testing.T) {
	req := &Request{paramNames: []string{"active"}, paramCount: 1, paramSlots: [16]any{true}}
	v, err := req.ParamBool("active")
	if err != nil || !v {
		t.Fatalf("got %v, err=%v", v, err)
	}
}

func TestTypedParamsHasParam(t *testing.T) {
	req := &Request{paramNames: []string{"id"}, paramCount: 1, paramSlots: [16]any{int64(1)}}
	if !req.HasParam("id") {
		t.Error("should have id")
	}
	if req.HasParam("missing") {
		t.Error("should not have missing")
	}
}

func TestTypedParamsDateTime(t *testing.T) {
	req := &Request{paramNames: []string{"date"}, paramCount: 1, paramSlots: [16]any{"2026-04-17T12:00:00Z"}}
	v, err := req.ParamDateTime("date")
	if err != nil {
		t.Fatal(err)
	}
	if v.Year() != 2026 {
		t.Fatalf("year = %d", v.Year())
	}
}

func TestTypedParamsMissing(t *testing.T) {
	req := &Request{paramNames: []string{}, paramCount: 0}
	_, err := req.ParamFloat("x")
	if err == nil {
		t.Fatal("should error")
	}
	_, err = req.ParamBool("x")
	if err == nil {
		t.Fatal("should error")
	}
	_, err = req.ParamDateTime("x")
	if err == nil {
		t.Fatal("should error")
	}
}

func TestBinaryErrorResponse(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound
	})
	rt.Registry.Register("fail", 1)
	rt.Registry.RegisterParams("fail", nil)

	body := EncodeBinaryRequest(1, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(string(body)))
	r.Header.Set("X-Luxo-Mode", "binary")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if w.Header().Get("X-Luxo-Mode") != "binary" {
		t.Fatal("error response should be binary mode")
	}
}

func TestEncodeBinaryRequestFloat(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("calc", 1)
	meta := []ParamMeta{
		{Name: "amount", Type: "Float", FieldID: 1},
		{Name: "active", Type: "Boolean", FieldID: 2},
	}
	reg.RegisterParams("calc", meta)

	body := EncodeBinaryRequest(1, map[string]any{
		"amount": 99.5,
		"active": true,
	}, meta)

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	amount, _ := req.ParamFloat("amount")
	if amount != 99.5 {
		t.Fatalf("amount = %f", amount)
	}
	active, _ := req.ParamBool("active")
	if !active {
		t.Fatal("active should be true")
	}
}
