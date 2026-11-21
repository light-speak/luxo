package h2transport

import (
	"net/http"
	"strconv"
	"testing"

	"golang.org/x/net/http2/hpack"
)

// Retired network candidate, retained only for reproducible local comparisons.
type snapshotEntry struct {
	name   string
	values []string
}

func snapshotHeaders(header http.Header) []snapshotEntry {
	count := 0
	for _, values := range header {
		count += len(values)
	}
	entries := make([]snapshotEntry, 0, len(header))
	storage := make([]string, count)
	for name, values := range header {
		n := copy(storage, values)
		entries = append(entries, snapshotEntry{name, storage[:n:n]})
		storage = storage[n:]
	}
	return entries
}

func TestHeaderSnapshotOwnership(t *testing.T) {
	header := http.Header{"X-Test": {"first", "second"}, "Empty": nil}
	snapshot := snapshotHeaders(header)
	header["X-Test"][0] = "changed"
	delete(header, "Empty")
	header.Set("Late", "absent")
	if len(snapshot) != 2 {
		t.Fatal(snapshot)
	}
	for _, entry := range snapshot {
		switch entry.name {
		case "X-Test":
			if len(entry.values) != 2 || entry.values[0] != "first" || entry.values[1] != "second" {
				t.Fatal(entry)
			}
		case "Empty":
			if len(entry.values) != 0 {
				t.Fatal(entry)
			}
		default:
			t.Fatal(entry)
		}
	}
}

func TestEmptyCommittedHeaderSnapshot(t *testing.T) {
	r := &response{header: make(http.Header)}
	r.WriteHeader(200)
	r.Header().Set("Late", "must not appear")
	q, _ := newDataQueue(1, 16, func([]byte) {})
	w := newControlWriter(newDataWriter(q, nil, 32))
	if err := w.encodeResponseHead(r); err != nil {
		t.Fatal(err)
	}
	fields, err := hpack.NewDecoder(4096, nil).DecodeFull(w.headerBlock.Bytes())
	if err != nil || len(fields) != 2 {
		t.Fatal(fields, err)
	}
	for _, field := range fields {
		if field.Name != ":status" && field.Name != "content-length" {
			t.Fatal(field)
		}
	}
}

var headerMapSink http.Header
var headerSliceSink []snapshotEntry

func BenchmarkHeaderSnapshot(b *testing.B) {
	for _, count := range []int{0, 1, 4, 16} {
		header := make(http.Header, count)
		for i := range count {
			header["X-Header-"+strconv.Itoa(i)] = []string{"first", "second"}
		}
		b.Run(strconv.Itoa(count)+"/map", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				headerMapSink = header.Clone()
			}
		})
		b.Run(strconv.Itoa(count)+"/slice", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				headerSliceSink = snapshotHeaders(header)
			}
		})
	}
}
