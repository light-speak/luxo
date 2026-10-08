package api

import (
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

var selectionTreeSink []*selection.Field

func BenchmarkBinarySelection(b *testing.B) {
	for _, item := range []struct{ width, selected, branches int }{
		{1, 1, 0}, {16, 1, 0}, {16, 16, 0}, {128, 1, 0}, {128, 128, 0},
		{1, 1, 2}, {16, 16, 4}, {1, 1, 16},
	} {
		s, model, mask := selectionFixture(b, item.width, item.selected, item.branches)
		reg := NewAPIRegistry()
		reg.Register("read", 1)
		s.RegisterAPI(&schema.API{ID: 1, Name: "read", ReturnType: model.Name})
		if err := reg.SetSchema(s); err != nil {
			b.Fatal(err)
		}
		body, err := EncodeBinaryRequest(1, mask, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		name := fmt.Sprintf("width=%d/selected=%d/branches=%d", item.width, item.selected, item.branches)
		b.Run(name+"/tree", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fields, err := decodeFieldMask(mask, model, s, 0)
				if err != nil {
					b.Fatal(err)
				}
				selectionTreeSink = fields
			}
		})
		b.Run(name+"/request", func(b *testing.B) {
			var req Request
			b.ReportAllocs()
			for b.Loop() {
				if err := reg.ParseBinaryRequestInto(body, &req); err != nil {
					b.Fatal(err)
				}
				selectionTreeSink = req.Select
			}
		})
	}
}
