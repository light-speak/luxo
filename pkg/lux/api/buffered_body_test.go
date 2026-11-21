package api

import (
	"bytes"
	"io"
	"testing"
)

func TestBufferedBodyWrappedFallback(t *testing.T) {
	data := []byte("payload")
	wrapped := io.NopCloser(io.LimitReader(NewBufferedBody(data), 3))
	got, bp, err := readTransportBody(wrapped)
	if bp != nil {
		defer putBody(bp)
	}
	if err != nil || bp == nil || !bytes.Equal(got, []byte("pay")) || &got[0] == &data[0] {
		t.Fatal("middleware reader was bypassed", err)
	}
}

func TestBufferedBodyBorrowRemaining(t *testing.T) {
	data := []byte("payload")
	body := NewBufferedBody(data)
	var first [1]byte
	if n, err := body.Read(first[:]); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	got, bp, err := readTransportBody(body)
	if err != nil || bp != nil || string(got) != "ayload" || &got[0] != &data[1] {
		t.Fatal("not borrowed", err)
	}
	if n, err := body.Read(first[:]); n != 0 || err != io.EOF {
		t.Fatal(n, err)
	}
	if string(data) != "payload" {
		t.Fatal("mutated transport storage")
	}
}

func TestBufferedBodyLimitsAndClose(t *testing.T) {
	body := NewBufferedBody([]byte("abc"))
	if _, err := body.borrow(2); err == nil {
		t.Fatal("oversize accepted")
	}
	got, err := body.borrow(3)
	if err != nil || string(got) != "abc" {
		t.Fatal(got, err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTransportBody(body); err != io.ErrClosedPipe {
		t.Fatal(err)
	}
	if _, err := body.Read(make([]byte, 1)); err != io.ErrClosedPipe {
		t.Fatal(err)
	}
}
