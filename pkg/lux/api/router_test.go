package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestRouterBasic(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		req.Buf.AppendString(`{"id":1,"name":"lin","email":"lin@test.com"}`)
		return nil
	})

	body := `{"$api":"getUser"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]json.RawMessage
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["data"]; !ok {
		t.Fatal("response should have data field")
	}
}

func TestRouterWithSelect(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		// Simulate WriteJSON with field selection
		req.Buf.AppendByte('{')
		first := true
		for _, f := range req.Select {
			if f.Name == "name" {
				if !first {
					req.Buf.AppendByte(',')
				}
				req.Buf.AppendString(`"name":"lin"`)
				first = false
			}
		}
		req.Buf.AppendByte('}')
		return nil
	})

	body := `{"$api":"getUser","$select":"name"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]json.RawMessage
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	var data map[string]any
	sonic.Unmarshal(resp["data"], &data)
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
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("something went wrong")
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

func TestRouterAppError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.MapData{"resource": "User"})
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "NotFound" {
		t.Errorf("error = %v", resp["error"])
	}
	if resp["code"].(float64) != 404 {
		t.Errorf("code = %v", resp["code"])
	}
	data := resp["data"].(map[string]any)
	if data["resource"] != "User" {
		t.Errorf("data.resource = %v", data["resource"])
	}
}

func TestRouterAppErrorWithI18n(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "zh.yaml"), []byte(`
error:
  not_found: "资源 ${resource} 不存在"
`), 0644)

	tr := i18n.New("en")
	tr.LoadDir(dir)

	rt := NewRouter()
	rt.SetTranslator(tr)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.MapData{"resource": "User"})
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	r.Header.Set("Accept-Language", "zh-CN")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "资源 User 不存在" {
		t.Errorf("message = %v", resp["message"])
	}
}

func TestRouterPlainErrorWrapped(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("db connection failed")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "Internal" {
		t.Errorf("error = %v", resp["error"])
	}
	if resp["data"] != nil {
		t.Error("internal error should not expose data")
	}
}

func TestRouterDevModeShowsCause(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.InternalError.WithCause(fmt.Errorf("disk full"))
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["cause"] != "disk full" {
		t.Errorf("cause = %v", resp["cause"])
	}
}

func TestRouterDevModeNoCauseWhenNil(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["cause"]; ok {
		t.Error("should not include cause when nil")
	}
}

func TestRouterUnknownAPIUsesAppError(t *testing.T) {
	rt := NewRouter()
	body := `{"$api":"nonExistent"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "NotFound" {
		t.Errorf("error = %v", resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["resource"] != "nonExistent" {
		t.Errorf("data.resource = %v", data["resource"])
	}
}

func TestRouterTraceIdInError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.BadRequest
	})

	handler := TraceMiddleware(rt)

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	traceID, ok := resp["traceId"].(string)
	if !ok || traceID == "" {
		t.Error("error response should include traceId")
	}
}

func TestRouterNoTraceIdWithoutMiddleware(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.BadRequest
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["traceId"]; ok {
		t.Error("should not include traceId without middleware")
	}
}

func TestRouterInternalErrorNoMessage(t *testing.T) {
	rt := NewRouter()
	tr := i18n.New("en")
	rt.SetTranslator(tr)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("sensitive db error")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	sonic.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "error.internal" {
		t.Errorf("message = %v, want raw key for internal error", resp["message"])
	}
}

func TestRouterJSONWriterFastPath(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		w := &testJSONWriter{id: 1, name: "lin"}
		w.WriteJSON(req.Buf, req.Select)
		return nil
	})

	body := `{"$api":"getUser","$select":"id,name"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"data":`) {
		t.Errorf("response should have data wrapper: %s", got)
	}
	if !strings.Contains(got, `"id":1`) {
		t.Errorf("should contain id: %s", got)
	}
}

// testJSONWriter is a mock model implementing WriteJSON with ResponseBuf.
type testJSONWriter struct {
	id   int64
	name string
}

func (tw *testJSONWriter) WriteJSON(buf *ResponseBuf, fields []*selection.Field) {
	buf.AppendByte('{')
	first := true
	if fields == nil {
		buf.AppendString(`"id":`)
		buf.AppendInt(tw.id)
		buf.AppendString(`,"name":`)
		buf.AppendJSONString(tw.name)
	} else {
		for _, f := range fields {
			switch f.Name {
			case "id":
				if !first {
					buf.AppendByte(',')
				}
				buf.AppendString(`"id":`)
				buf.AppendInt(tw.id)
				first = false
			case "name":
				if !first {
					buf.AppendByte(',')
				}
				buf.AppendString(`"name":`)
				buf.AppendJSONString(tw.name)
				first = false
			}
		}
	}
	buf.AppendByte('}')
}

func TestRouterDeleteHandler(t *testing.T) {
	rt := NewRouter()
	rt.Handle("deleteUser", func(ctx context.Context, req *Request) error {
		// Delete writes directly — no map, no any
		req.Buf.AppendString(`{"deleted":1}`)
		return nil
	})

	body := `{"$api":"deleteUser"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Errorf("got %s", w.Body.String())
	}
}

func TestRouterHandlerPanicRecovery(t *testing.T) {
	rt := NewRouter()
	rt.Handle("crashTest", func(ctx context.Context, req *Request) error {
		panic("boom")
	})

	body := `{"$api":"crashTest"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("expected error response, got %s", w.Body.String())
	}
}

// Suppress unused imports
var _ = bytes.NewBuffer
var _ = json.RawMessage{}
var _ = fmt.Sprint
