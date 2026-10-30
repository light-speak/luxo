package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

type ownedRecorder struct {
	*httptest.ResponseRecorder
	accept bool
	buf    *ResponseBuf
}

func (r *ownedRecorder) TakeBinaryResponse(buf *ResponseBuf) bool {
	if !r.accept {
		return false
	}
	r.buf = buf
	return true
}

func TestRouterTransfersBinaryResponseOwnership(t *testing.T) {
	for _, accept := range []bool{false, true} {
		writer := &ownedRecorder{ResponseRecorder: httptest.NewRecorder(), accept: accept}
		request := httptest.NewRequest("POST", "/", nil)
		req := &Request{API: "test"}
		marker := new(int)
		new(Router).executeHTTPHandler(writer, request, req, func(_ context.Context, req *Request) error {
			req.Buf.B = append(req.Buf.B, "body"...)
			req.Buf.Identity = marker
			return nil
		}, true, nil, nil, time.Time{})
		if writer.Code != 200 || writer.Header().Get("Content-Length") != "4" {
			t.Fatal("response metadata lost")
		}
		if accept {
			if writer.buf == nil {
				t.Fatal("no ownership transfer")
			}
			if writer.buf.Identity != marker || string(writer.buf.B) != "body" || writer.Body.Len() != 0 {
				t.Fatal("buffer returned early or copied")
			}
			PutBuf(writer.buf)
		} else if writer.buf != nil || writer.Body.String() != "body" {
			t.Fatal("fallback changed")
		}
	}
}
