package schema

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func BenchmarkSchemaDepthInput(b *testing.B) {
	for _, depth := range []int{1, 64} {
		for _, lists := range []bool{false, true} {
			if lists && depth == 1 {
				continue
			}
			for _, generic := range []int{0, 128} {
				for _, duplicate := range []bool{false, true} {
					b.Run(fmt.Sprintf("model%d/list%t/json%d/duplicate%t", depth, lists, generic, duplicate), func(b *testing.B) {
						s := jsonDepthSchema(false)
						raw := []byte(jsonDepthInput(depth, lists, generic))
						if duplicate {
							raw = []byte(`{"child":` + string(raw) + `,"child":null}`)
							if depth == 64 {
								raw = []byte(`{"child":` + jsonDepthInput(63, lists, generic) + `,"child":null}`)
							}
						}
						want, err := s.JSONToBinary("Node", raw)
						if err != nil {
							b.Fatal(err)
						}
						b.Logf("output-sha256=%x", sha256.Sum256(want))
						b.ReportAllocs()
						for b.Loop() {
							got, err := s.JSONToBinary("Node", raw)
							if err != nil || !bytes.Equal(got, want) {
								b.Fatal("invalid conversion", err)
							}
						}
					})
				}
			}
		}
	}
}

func BenchmarkSchemaDepthOutput(b *testing.B) {
	for _, depth := range []int{1, 64} {
		for _, lists := range []bool{false, true} {
			if lists && depth == 1 {
				continue
			}
			for _, generic := range []int{0, 128} {
				s := jsonDepthSchema(false)
				row := schemaDepthResponse(depth, lists, generic)
				column := append([]byte{1}, row...)
				page := append([]byte(nil), column...)
				for range 3 {
					page = codec.AppendSvarint(page, 1)
				}
				for _, entry := range []struct {
					name string
					wire []byte
					read func([]byte, []byte, *Model, ...*Schema) ([]byte, error)
				}{{"row", row, BinaryToJSON}, {"column", column, BinaryListToJSON}, {"page", page, BinaryPaginatedListToJSON}} {
					b.Run(fmt.Sprintf("model%d/list%t/json%d/%s", depth, lists, generic, entry.name), func(b *testing.B) {
						want, err := entry.read(nil, entry.wire, s.Models["Node"], s)
						if err != nil {
							b.Fatal(err)
						}
						dst := make([]byte, 0, len(want))
						b.Logf("output-sha256=%x", sha256.Sum256(want))
						b.ReportAllocs()
						for b.Loop() {
							got, err := entry.read(dst[:0], entry.wire, s.Models["Node"], s)
							if err != nil || !bytes.Equal(got, want) {
								b.Fatal("invalid conversion", err)
							}
						}
					})
				}
			}
		}
	}
}

func BenchmarkSchemaDepthErrors(b *testing.B) {
	s := jsonDepthSchema(false)
	for _, sample := range []struct {
		name string
		raw  string
	}{
		{"model65", jsonDepthInput(65, false, 0)},
		{"json129", jsonDepthInput(1, false, 129)},
		{"oldModel65", `{"child":` + jsonDepthInput(64, true, 128) + `,"child":null}`},
		{"malformed", jsonDepthInput(64, false, 128) + "x"},
		{"invalidUnicode", `{"payload":"\uD800"}`},
	} {
		b.Run("input/"+sample.name, func(b *testing.B) {
			raw := []byte(sample.raw)
			benchmarkSchemaRejected(b, func() ([]byte, error) { return s.JSONToBinary("Node", raw) })
		})
	}
	for _, limits := range []struct {
		name           string
		model, generic int
	}{
		{"model65", 65, 128}, {"json129", 1, 129},
	} {
		row := schemaDepthResponse(limits.model, false, limits.generic)
		column := append([]byte{1}, row...)
		page := append([]byte(nil), column...)
		for range 3 {
			page = codec.AppendSvarint(page, 1)
		}
		for _, entry := range []struct {
			name string
			wire []byte
			read func([]byte, []byte, *Model, ...*Schema) ([]byte, error)
		}{{"row", row, BinaryToJSON}, {"column", column, BinaryListToJSON}, {"page", page, BinaryPaginatedListToJSON}} {
			b.Run("output/"+limits.name+"/"+entry.name, func(b *testing.B) {
				benchmarkSchemaRejected(b, func() ([]byte, error) { return entry.read(nil, entry.wire, s.Models["Node"], s) })
			})
		}
	}
}

func benchmarkSchemaRejected(b *testing.B, convert func() ([]byte, error)) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		output, err := convert()
		if err == nil || output != nil {
			b.Fatal("invalid resource accepted or partial output exposed", err)
		}
	}
}

var lateErrorOutput []byte

func BenchmarkSchemaLateErrors(b *testing.B) {
	for name, convert := range schemaLateErrors(b) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := convert(nil)
				if err == nil || len(got) != 0 {
					b.Fatal("invalid late error", err)
				}
				lateErrorOutput = got
			}
		})
	}
}

func BenchmarkSchemaErrorConsumption(b *testing.B) {
	s := jsonDepthSchema(false)
	for _, sample := range schemaErrorInputs() {
		for _, message := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/message%t", sample.name, message), func(b *testing.B) {
				_, err := s.JSONToBinary("Node", sample.raw)
				if err == nil {
					b.Fatal("invalid error fixture")
				}
				want := err.Error()
				b.Logf("message-sha256=%x", sha256.Sum256([]byte(want)))
				b.ReportAllocs()
				for b.Loop() {
					out, err := s.JSONToBinary("Node", sample.raw)
					if err == nil || out != nil {
						b.Fatal("missing error or partial output", err)
					}
					if message && err.Error() != want {
						b.Fatal("error message changed")
					}
				}
			})
		}
	}
}
