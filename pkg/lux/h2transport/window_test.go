package h2transport

import (
	"errors"
	"testing"
)

func TestWindowIncrease(t *testing.T) {
	for _, tc := range []struct {
		value     int64
		increment uint32
		want      int64
		valid     bool
	}{
		{0, 1, 1, true}, {-10, 5, -5, true}, {maxWindow - 1, 1, maxWindow, true},
		{10, 0, 10, false}, {10, 1 << 31, 10, false}, {maxWindow, 1, maxWindow, false},
	} {
		w := sendWindow(tc.value)
		err := w.increase(tc.increment)
		if (err == nil) != tc.valid || int64(w) != tc.want {
			t.Fatalf("%+v: window=%d err=%v", tc, w, err)
		}
	}
}

func TestInitialWindowChange(t *testing.T) {
	w := sendWindow(5)
	if err := w.changeInitial(65535, 0); err != nil || w != -65530 {
		t.Fatalf("%d %v", w, err)
	}
	if err := w.changeInitial(0, 65535); err != nil || w != 5 {
		t.Fatalf("%d %v", w, err)
	}
	for _, tc := range []struct {
		value     int64
		old, next uint32
	}{
		{maxWindow, 0, 1}, {0, 1 << 31, 0}, {0, 0, 1 << 31},
	} {
		w := sendWindow(tc.value)
		if err := w.changeInitial(tc.old, tc.next); err == nil || int64(w) != tc.value {
			t.Fatal("invalid update changed state")
		}
	}
}

func TestReserveData(t *testing.T) {
	for _, tc := range []struct {
		connection, stream, size int64
		want                     error
	}{
		{10, 10, 5, nil}, {10, 10, 10, nil}, {0, -10, 0, nil},
		{3, 10, 4, errWindowBlocked}, {10, 3, 4, errWindowBlocked}, {10, -1, 1, errWindowBlocked},
		{10, 10, -1, errInvalidWindow}, {maxWindow, maxWindow, maxWindow + 1, errInvalidWindow},
	} {
		connection, stream := sendWindow(tc.connection), sendWindow(tc.stream)
		err := reserveData(&connection, &stream, tc.size)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%+v: %v", tc, err)
		}
		used := int64(0)
		if err == nil {
			used = tc.size
		}
		if int64(connection) != tc.connection-used || int64(stream) != tc.stream-used {
			t.Fatal("partial or wrong debit")
		}
	}
}

func BenchmarkReserveData(b *testing.B) {
	var connection, stream sendWindow
	b.ReportAllocs()
	for b.Loop() {
		connection, stream = 65535, 65535
		if err := reserveData(&connection, &stream, 4096); err != nil {
			b.Fatal(err)
		}
	}
}
