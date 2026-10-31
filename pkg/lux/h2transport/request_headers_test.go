package h2transport

import (
	"bytes"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func requestFields(extra ...hpack.HeaderField) []hpack.HeaderField {
	return append([]hpack.HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "http"}, {Name: ":authority", Value: "localhost"}, {Name: ":path", Value: "/?debug=1"}}, extra...)
}

func headerWire(t *testing.T, fields []hpack.HeaderField, end bool) []byte {
	t.Helper()
	var block, wire bytes.Buffer
	e := hpack.NewEncoder(&block)
	for _, field := range fields {
		if err := e.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	f := http2.NewFramer(&wire, nil)
	if err := f.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: end}); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func decodeHead(t *testing.T, fields []hpack.HeaderField, end bool) (requestHead, error) {
	t.Helper()
	f, err := newRequestFramer(bytes.NewReader(headerWire(t, fields, end))).ReadFrame()
	if err != nil {
		return requestHead{}, err
	}
	return parseRequestHead(f.(*http2.MetaHeadersFrame))
}

func TestRequestHeaders(t *testing.T) {
	h, err := decodeHead(t, requestFields(hpack.HeaderField{Name: "authorization", Value: "Bearer secret"}, hpack.HeaderField{Name: "content-length", Value: "12"}, hpack.HeaderField{Name: "te", Value: "trailers"}), false)
	if err != nil || h.method != "POST" || h.path != "/?debug=1" || h.authority != "localhost" || h.target.Scheme != "http" || h.contentLength != 12 || len(h.fields) != 3 || h.fields[0].Value != "Bearer secret" {
		t.Fatal(h, err)
	}
	for _, method := range []string{"GET", "OPTIONS", "CUSTOM"} {
		fields := requestFields()
		fields[0].Value = method
		if method == "OPTIONS" {
			fields[3].Value = "*"
		}
		if _, err := decodeHead(t, fields, true); err != nil {
			t.Fatal(method, err)
		}
	}
}

func TestMalformedRequestHeaders(t *testing.T) {
	for _, field := range []hpack.HeaderField{
		{Name: "connection", Value: "keep-alive"}, {Name: "upgrade", Value: "websocket"},
		{Name: "proxy-connection", Value: "x"}, {Name: "keep-alive", Value: "x"},
		{Name: "transfer-encoding", Value: "chunked"}, {Name: "te", Value: "gzip"},
		{Name: "content-length", Value: "-1"}, {Name: "content-length", Value: "+1"},
		{Name: "content-length", Value: ""}, {Name: "content-length", Value: "9223372036854775808"},
		{Name: "content-length", Value: "1,2"}, {Name: "host", Value: "different"},
	} {
		if _, err := decodeHead(t, requestFields(field), false); err == nil {
			t.Fatal("accepted", field)
		}
	}
	for i, value := range []string{"BAD METHOD", "", "", "/bad#fragment"} {
		fields := requestFields()
		fields[i].Value = value
		if _, err := decodeHead(t, fields, false); err == nil {
			t.Fatal("accepted invalid pseudo header", i)
		}
	}
	for _, path := range []string{"", "relative", "*", "/bad%XX"} {
		fields := requestFields()
		fields[3].Value = path
		if _, err := decodeHead(t, fields, false); err == nil {
			t.Fatal("accepted path", path)
		}
	}
	for _, fields := range [][]hpack.HeaderField{
		requestFields()[1:],
		requestFields(hpack.HeaderField{Name: "content-length", Value: "1"}),
		requestFields(hpack.HeaderField{Name: "content-length", Value: "1"}, hpack.HeaderField{Name: "content-length", Value: "2"}),
		requestFields(hpack.HeaderField{Name: ":status", Value: "200"}),
	} {
		if _, err := decodeHead(t, fields, true); err == nil {
			t.Fatal("accepted malformed headers")
		}
	}
}

func TestHeaderOwnershipAndLimits(t *testing.T) {
	wire := append(headerWire(t, requestFields(hpack.HeaderField{Name: "x-original", Value: "retained"}), true), headerWire(t, requestFields(hpack.HeaderField{Name: "x-next", Value: "overwritten"}), true)...)
	r := newRequestFramer(bytes.NewReader(wire))
	f, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseRequestHead(f.(*http2.MetaHeadersFrame))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFrame(); err != nil {
		t.Fatal(err)
	}
	if h.fields[0].Name != "x-original" || h.fields[0].Value != "retained" {
		t.Fatal("borrowed framer fields escaped")
	}
	if _, err := decodeHead(t, requestFields(hpack.HeaderField{Name: "x-large", Value: string(bytes.Repeat([]byte{'a'}, 16384))}), true); err == nil {
		t.Fatal("oversized headers accepted")
	}
}

func TestHeaderTargetAndHostCases(t *testing.T) {
	for _, value := range []string{"https", "custom+v1.2-x"} {
		fields := requestFields()
		fields[1].Value = value
		if _, err := decodeHead(t, fields, true); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"1http", "bad scheme", "httpé"} {
		fields := requestFields()
		fields[1].Value = value
		if _, err := decodeHead(t, fields, true); err == nil {
			t.Fatal(value)
		}
	}
	fields := requestFields(hpack.HeaderField{Name: "host", Value: "localhost"})
	fields[2].Value = ""
	if _, err := decodeHead(t, fields, true); err != nil {
		t.Fatal(err)
	}
	fields = append(fields, hpack.HeaderField{Name: "host", Value: "localhost"})
	if _, err := decodeHead(t, fields, true); err == nil {
		t.Fatal("duplicate host")
	}
	connect := []hpack.HeaderField{{Name: ":method", Value: "CONNECT"}, {Name: ":authority", Value: "localhost:443"}}
	if _, err := decodeHead(t, connect, false); err != nil {
		t.Fatal(err)
	}
	connect = append(connect, hpack.HeaderField{Name: ":path", Value: "/"})
	if _, err := decodeHead(t, connect, false); err == nil {
		t.Fatal("malformed CONNECT")
	}
	fields = requestFields(hpack.HeaderField{Name: "content-length", Value: "0"}, hpack.HeaderField{Name: "content-length", Value: "00"})
	if _, err := decodeHead(t, fields, true); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationAndTruncationKeepHPACKState(t *testing.T) {
	var block, wire bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	writer := http2.NewFramer(&wire, nil)
	for _, id := range []uint32{1, 3} {
		block.Reset()
		fields := requestFields(hpack.HeaderField{Name: "x-reused", Value: "dynamic-table-entry"})
		if id == 1 {
			for range 500 {
				fields = append(fields, hpack.HeaderField{Name: "x-padding", Value: "0123456789"})
			}
		}
		for _, field := range fields {
			if err := encoder.WriteField(field); err != nil {
				t.Fatal(err)
			}
		}
		middle := block.Len() / 2
		if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: id, EndStream: true, BlockFragment: block.Bytes()[:middle]}); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteContinuation(id, true, block.Bytes()[middle:]); err != nil {
			t.Fatal(err)
		}
	}
	r := newRequestFramer(&wire)
	f, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !f.(*http2.MetaHeadersFrame).Truncated {
		t.Fatal("header count budget ignored")
	}
	if _, err := parseRequestHead(f.(*http2.MetaHeadersFrame)); err == nil {
		t.Fatal("truncated headers accepted")
	}
	f, err = r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	head, err := parseRequestHead(f.(*http2.MetaHeadersFrame))
	if err != nil || head.fields[0].Value != "dynamic-table-entry" {
		t.Fatal(head, err)
	}
}
