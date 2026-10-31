package api

import (
	"bytes"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestBinaryResponseDeclaresExactLength(t *testing.T) {
	for _, size := range []int{0, 1, 4096, 65536} {
		body := bytes.Repeat([]byte{0x7f}, size)
		w := httptest.NewRecorder()
		new(Router).writeHTTPResponse(w, body, nil, true)
		if got := w.Header().Get("Content-Length"); got != strconv.Itoa(size) {
			t.Fatalf("size %d: Content-Length=%q", size, got)
		}
		if !bytes.Equal(w.Body.Bytes(), body) {
			t.Fatal("response body changed")
		}
	}
}
