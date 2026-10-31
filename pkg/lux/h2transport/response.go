package h2transport

import (
	"errors"
	"github.com/light-speak/luxo/pkg/lux/api"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

var errResponse = errors.New("invalid or oversized experimental unary HTTP response")

// The compatibility bridge intentionally captures one bounded unary response.
// It does not implement Hijacker, Flusher, server push or WebSocket Upgrade.
type response struct {
	header    http.Header
	committed http.Header
	status    int
	body      []byte
	limit     int
	err       error
	owner     *api.ResponseBuf
	budget    *byteBudget
}

func (r *response) TakeBinaryResponse(buf *api.ResponseBuf) bool {
	if buf == nil || r.err != nil || r.owner != nil || cap(r.body) != 0 || len(buf.B) > r.limit {
		return false
	}
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if !r.budget.take(cap(buf.B)) {
		r.err = errQueueFull
		return false
	}
	r.owner, r.body = buf, buf.B
	return true
}

func (r *response) release() {
	if r == nil {
		return
	}
	r.budget.give(cap(r.body))
	if r.owner != nil {
		api.PutBuf(r.owner)
		r.owner = nil
	}
	r.body = nil
}

func (r *response) Header() http.Header { return r.header }
func (r *response) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	if status < 200 || status > 599 {
		r.err = errResponse
		return
	}
	r.status = status
	r.committed = r.header.Clone()
}

func (r *response) Write(data []byte) (int, error) {
	if r.owner != nil {
		r.err = errResponse
		return 0, r.err
	}
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if r.err != nil {
		return 0, r.err
	}
	if len(data) > r.limit-len(r.body) {
		r.err = errResponse
		return 0, r.err
	}
	needed := len(r.body) + len(data)
	if needed > cap(r.body) {
		capacity := min(r.limit, max(needed, cap(r.body)+min(cap(r.body), r.limit-cap(r.body))))
		if !r.budget.take(capacity) {
			r.err = errQueueFull
			return 0, r.err
		}
		body := make([]byte, len(r.body), capacity)
		copy(body, r.body)
		r.budget.give(cap(r.body))
		r.body = body
	}
	r.body = append(r.body, data...)
	return len(data), nil
}

func (w *controlWriter) writeResponse(id uint32, r *response) error {
	if _, exists := w.data.queue.streams[id]; !exists {
		return nil
	} // Reset before completion.
	if r == nil || r.err != nil {
		return errResponse
	}
	if err := w.encodeResponseHead(r); err != nil {
		return err
	}
	if err := w.data.queue.submit(id, r.body); err != nil {
		return err
	}
	block := w.headerBlock.Bytes()
	size := min(len(block), w.frameSize)
	if err := w.data.framer.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: block[:size], EndHeaders: size == len(block)}); err != nil {
		return err
	}
	for block = block[size:]; len(block) > 0; block = block[size:] {
		size = min(len(block), w.frameSize)
		if err := w.data.framer.WriteContinuation(id, size == len(block), block[:size]); err != nil {
			return err
		}
	}
	return nil
}

func (w *controlWriter) encodeResponseHead(r *response) error {
	header := r.committed
	if header == nil {
		header = r.header
	}
	if r.status < 200 || r.status > 599 {
		return errResponse
	}
	length := strconv.Itoa(len(r.body))
	size := uint64(42 + len(length) + 32 + 14) // :status plus content-length.
	for name, values := range header {
		name = strings.ToLower(name)
		if !validResponseHeader(name, values, length) {
			return errResponse
		}
		if name == "content-length" {
			continue
		}
		for _, value := range values {
			size += uint64(32 + len(name) + len(value))
		}
	}
	if size > uint64(min(w.headerLimit, requestHeaderLimit)) {
		return errResponse
	}
	w.headerBlock.Reset()
	if err := w.encoder.WriteField(hpack.HeaderField{Name: ":status", Value: strconv.Itoa(r.status)}); err != nil {
		return err
	}
	if err := w.encoder.WriteField(hpack.HeaderField{Name: "content-length", Value: length}); err != nil {
		return err
	}
	for name, values := range header {
		name = strings.ToLower(name)
		if name == "content-length" {
			continue
		}
		for _, value := range values {
			if err := w.encoder.WriteField(hpack.HeaderField{Name: name, Value: value}); err != nil {
				return err
			}
		}
	}
	return nil
}

func validResponseHeader(name string, values []string, length string) bool {
	if !httpguts.ValidHeaderFieldName(name) {
		return false
	}
	switch name {
	case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade", "trailer":
		return false
	}
	for _, value := range values {
		if !httpguts.ValidHeaderFieldValue(value) || name == "content-length" && value != length {
			return false
		}
	}
	return true
}
