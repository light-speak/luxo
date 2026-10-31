package h2transport

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func receiveHead(t *testing.T, id uint32, length string, end bool) *http2.MetaHeadersFrame {
	t.Helper()
	fields := requestFields()
	if length != "" {
		fields = append(fields, hpack.HeaderField{Name: "content-length", Value: length})
	}
	r := newRequestFramer(bytes.NewReader(headerWire(t, fields, end)))
	f, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	h := f.(*http2.MetaHeadersFrame)
	h.StreamID = id
	return h
}

func receiveData(t *testing.T, id uint32, end bool, data, pad []byte) *http2.DataFrame {
	t.Helper()
	var wire bytes.Buffer
	if err := http2.NewFramer(&wire, nil).WriteDataPadded(id, end, data, pad); err != nil {
		t.Fatal(err)
	}
	f, err := http2.NewFramer(nil, &wire).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	return f.(*http2.DataFrame)
}

func TestReceiveLargeBodyAndOwnership(t *testing.T) {
	r, err := newRequestReceiver(2, 200000, 400000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.open(receiveHead(t, 1, "131072", false)); err != nil {
		t.Fatal(err)
	}
	var ready *receivedRequest
	var returned uint32
	for i := range 8 {
		frame := receiveData(t, 1, i == 7, bytes.Repeat([]byte{byte(i)}, 16384), nil)
		var credit receiveCredit
		ready, credit, err = r.data(frame)
		if err != nil {
			t.Fatal(i, err)
		}
		returned += credit.connection
		frame.Data()[0] = 255
	}
	if ready == nil || len(ready.body) != 131072 || ready.body[0] != 0 || ready.body[16384] != 1 || returned != 131072 || r.buffered != cap(ready.body) {
		t.Fatal("body or accounting", returned)
	}
	r.release(1)
	if r.buffered != 0 || len(r.streams) != 0 {
		t.Fatal("body retained")
	}
}

func TestReceiveAdmissionAndBudget(t *testing.T) {
	for _, limits := range [][3]int{{0, 1, 1}, {65536, 1, 1}, {1, 0, 1}, {1, 2, 1}} {
		if _, err := newRequestReceiver(limits[0], limits[1], limits[2]); err == nil {
			t.Fatal(limits)
		}
	}
	r, _ := newRequestReceiver(1, 8, 8)
	if _, err := r.open(receiveHead(t, 2, "", false)); err == nil {
		t.Fatal("even stream")
	}
	if _, err := r.open(receiveHead(t, 1, "", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.open(receiveHead(t, 3, "", false)); err == nil {
		t.Fatal("unbounded streams")
	}
	if _, _, err := r.data(receiveData(t, 1, true, []byte("123456789"), nil)); err == nil || r.buffered != 0 {
		t.Fatal("body limit", err)
	}
	if _, err := r.open(receiveHead(t, 1, "", false)); err == nil {
		t.Fatal("reused stream")
	}
	if _, err := r.open(receiveHead(t, 5, "9", false)); err == nil {
		t.Fatal("oversize declaration")
	}
	if _, err := r.open(receiveHead(t, 7, "0", true)); err != nil {
		t.Fatal(err)
	}
	r.close()
	if r.buffered != 0 || len(r.streams) != 0 {
		t.Fatal("close retained requests")
	}
	if _, err := r.open(receiveHead(t, 9, "", false)); err == nil {
		t.Fatal("opened after close")
	}
}

func TestReceiveLengthPaddingAndClosedStreams(t *testing.T) {
	r, _ := newRequestReceiver(3, 100, 300)
	_, _ = r.open(receiveHead(t, 1, "3", false))
	ready, credit, err := r.data(receiveData(t, 1, true, []byte("abc"), make([]byte, 7)))
	if err != nil || ready == nil || string(ready.body) != "abc" || credit.connection != 11 {
		t.Fatal(credit, err)
	}
	if _, _, err := r.data(receiveData(t, 1, true, nil, nil)); err == nil {
		t.Fatal("accepted data after end")
	}
	r.release(1)
	if _, _, err := r.data(receiveData(t, 1, true, []byte("a"), nil)); err == nil {
		t.Fatal("accepted closed stream")
	}
	if _, _, err := r.data(receiveData(t, 9, true, nil, nil)); err == nil {
		t.Fatal("accepted idle stream")
	}
	for _, id := range []uint32{3, 5} {
		_, _ = r.open(receiveHead(t, id, "3", false))
		body := []byte("ab")
		if id == 5 {
			body = []byte("abcd")
		}
		if _, _, err := r.data(receiveData(t, id, true, body, nil)); err == nil {
			t.Fatal("length mismatch")
		}
	}
}

func TestReceiveWindowErrors(t *testing.T) {
	r, _ := newRequestReceiver(1, 100, 100)
	_, _ = r.open(receiveHead(t, 1, "", false))
	r.window.available = 2
	_, _, err := r.data(receiveData(t, 1, false, []byte("abc"), nil))
	if !errors.Is(err, http2.ConnectionError(http2.ErrCodeFlowControl)) {
		t.Fatal(err)
	}
	r.window.available = 65535
	r.streams[1].window.available = 2
	_, credit, err := r.data(receiveData(t, 1, false, []byte("abc"), nil))
	var streamErr http2.StreamError
	if !errors.As(err, &streamErr) || streamErr.Code != http2.ErrCodeFlowControl || credit.connection != 3 {
		t.Fatal(credit, err)
	}
}

func TestReceiveGrowthAndFailureCleanup(t *testing.T) {
	r, _ := newRequestReceiver(2, 4, 7)
	_, _ = r.open(receiveHead(t, 1, "", false))
	_, _, err := r.data(receiveData(t, 1, false, []byte("a"), nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.open(receiveHead(t, 3, "", false))
	_, _, err = r.data(receiveData(t, 3, true, []byte("xy"), nil))
	if err != nil || r.buffered != 6 {
		t.Fatal("exact capacity fallback", err, r.buffered)
	}
	r.release(3)
	_, _ = r.open(receiveHead(t, 5, "", false))
	_, _, err = r.data(receiveData(t, 5, true, []byte("wxyz"), nil))
	if err == nil || r.buffered != 4 {
		t.Fatal("connection memory budget", err)
	}
	_, _, err = r.data(receiveData(t, 1, true, []byte("bcd"), nil))
	if err != nil {
		t.Fatal(err)
	}
	r.release(1)
	r.release(1)
	_, err = r.open(receiveHead(t, 7, "1", true))
	if err == nil {
		t.Fatal("malformed open")
	}
	r.close()
	if _, _, err := r.data(receiveData(t, 1, false, nil, nil)); err == nil {
		t.Fatal("closed receiver")
	}
}

func TestReceiveTrailers(t *testing.T) {
	for _, variant := range []string{"valid", "pseudo", "missing-end", "truncated", "length", "forbidden"} {
		t.Run(variant, func(t *testing.T) {
			r, _ := newRequestReceiver(1, 100, 100)
			if _, _, err := r.headers(receiveHead(t, 1, "3", false)); err != nil {
				t.Fatal(err)
			}
			_, _, _ = r.data(receiveData(t, 1, false, []byte("abc"), nil))
			fields := []hpack.HeaderField{{Name: "x-checksum", Value: "abc"}}
			if variant == "pseudo" {
				fields = requestFields()
			}
			if variant == "forbidden" {
				fields[0].Name = "content-length"
			}
			f, err := newRequestFramer(bytes.NewReader(headerWire(t, fields, variant != "missing-end"))).ReadFrame()
			if err != nil {
				t.Fatal(err)
			}
			frame := f.(*http2.MetaHeadersFrame)
			frame.Truncated = variant == "truncated"
			if variant == "length" {
				r.streams[1].head.contentLength = 4
			}
			ready, credit, err := r.headers(frame)
			if variant != "valid" {
				if err == nil || r.buffered != 0 || credit.connection != 3 {
					t.Fatal(credit, err)
				}
				return
			}
			if err != nil || ready == nil || ready.trailers[0].Value != "abc" || credit.connection != 3 {
				t.Fatal(credit, err)
			}
			frame.Fields[0].Value = "changed"
			if ready.trailers[0].Value != "abc" {
				t.Fatal("borrowed trailers")
			}
			if _, _, err := r.headers(frame); err == nil {
				t.Fatal("second trailer block")
			}
			r.close()
			if _, _, err := r.headers(frame); err == nil {
				t.Fatal("closed headers")
			}
		})
	}
}
