package h2transport

import (
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const requestHeaderLimit = 16384

// The field slice is owned, not borrowed from Framer. HPACK strings are immutable.
// HTTP admission does not decide which methods or routes Luxo exposes.
type requestHead struct {
	method, authority, path string
	contentLength           int64
	fields                  []hpack.HeaderField
	target                  *url.URL
}

func newRequestFramer(source io.Reader) *http2.Framer {
	r := http2.NewFramer(nil, &headerGuard{source: source})
	r.SetMaxReadFrameSize(16384)
	r.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	r.ReadMetaHeaders.SetMaxStringLength(requestHeaderLimit)
	r.MaxHeaderListSize = requestHeaderLimit
	return r
}

// Framer has already checked pseudo-header ordering, duplicates and field syntax.
func parseRequestHead(frame *http2.MetaHeadersFrame) (requestHead, error) {
	h := requestHead{method: frame.PseudoValue("method"), authority: frame.PseudoValue("authority"), path: frame.PseudoValue("path"), contentLength: -1}
	errMalformed := http2.StreamError{StreamID: frame.StreamID, Code: http2.ErrCodeProtocol}
	if frame.Truncated || frame.PseudoValue("status") != "" || !httpguts.ValidHeaderFieldName(h.method) {
		return requestHead{}, errMalformed
	}
	if !h.readRegular(frame.RegularFields()) || !h.validTarget(frame.PseudoValue("scheme")) {
		return requestHead{}, errMalformed
	}
	if frame.StreamEnded() && h.contentLength > 0 {
		return requestHead{}, errMalformed
	}
	h.fields = slices.Clone(frame.RegularFields())
	return h, nil
}

func (h *requestHead) readRegular(fields []hpack.HeaderField) bool {
	var host string
	for _, field := range fields {
		switch field.Name {
		case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade":
			return false
		case "te":
			if !strings.EqualFold(field.Value, "trailers") {
				return false
			}
		case "host":
			if host != "" || field.Value == "" {
				return false
			}
			host = field.Value
		case "content-length":
			value, ok := parseContentLength(field.Value)
			if !ok || h.contentLength >= 0 && value != h.contentLength {
				return false
			}
			h.contentLength = value
		}
	}
	if h.authority == "" {
		h.authority = host
	}
	return host == "" || strings.EqualFold(host, h.authority)
}

func parseContentLength(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil
}

func (h *requestHead) validTarget(scheme string) bool {
	if h.authority == "" || !httpguts.ValidHostHeader(h.authority) || strings.Contains(h.authority, "@") {
		return false
	}
	if h.method == "CONNECT" {
		return scheme == "" && h.path == ""
	}
	if !validScheme(scheme) {
		return false
	}
	if h.path == "*" {
		if h.method != "OPTIONS" {
			return false
		}
		h.target = &url.URL{Path: "*", Scheme: scheme}
		return true
	}
	if !strings.HasPrefix(h.path, "/") || strings.ContainsAny(h.path, "# \t") {
		return false
	}
	target, err := url.ParseRequestURI(h.path)
	if err != nil {
		return false
	}
	target.Scheme = scheme
	h.target = target
	return true
}

func validScheme(value string) bool {
	for i, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' {
			continue
		}
		if i > 0 && (ch >= '0' && ch <= '9' || ch == '+' || ch == '-' || ch == '.') {
			continue
		}
		return false
	}
	return value != ""
}
