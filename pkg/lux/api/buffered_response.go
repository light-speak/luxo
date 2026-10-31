package api

import "net/http"

// BinaryResponseOwner is an opt-in transport ownership boundary, not a business
// codec. A true result transfers buf exclusively: the transport must call
// PutBuf exactly once after I/O or cancellation finishes using it. A false result
// must not retain, modify or recycle buf; Router falls back to Write.
type BinaryResponseOwner interface {
	TakeBinaryResponse(buf *ResponseBuf) bool
}

func (rt *Router) writeOwnedResponse(w http.ResponseWriter, buf *ResponseBuf, jsonBody []byte, binaryMode bool) bool {
	if binaryMode {
		if owner, ok := w.(BinaryResponseOwner); ok {
			writeBinaryResponseHead(w, len(buf.B))
			if owner.TakeBinaryResponse(buf) {
				return true
			}
			_, _ = w.Write(buf.B)
			return false
		}
	}
	rt.writeHTTPResponse(w, buf.B, jsonBody, binaryMode)
	return false
}
