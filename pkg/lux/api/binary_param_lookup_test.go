package api

import (
	"fmt"
	"testing"
)

func findBinaryParamLinear(params []ParamMeta, fieldID int) (int, *ParamMeta) {
	for i := range params {
		if params[i].FieldID == fieldID {
			return i, &params[i]
		}
	}
	return -1, nil
}

func TestBinaryParamLookupParity(t *testing.T) {
	for _, ids := range [][]int{nil, {1}, {1, 2, 3}, {3, 1, 2}, {7, 42, 1000}, {5, 7, 9}, {2147483645}} {
		params := make([]ParamMeta, len(ids))
		for i, id := range ids {
			params[i].FieldID = id
		}
		lookup := newBinaryParamLookup(params)
		for _, id := range []int{-1, 0, 1, 2, 3, 5, 7, 9, 42, 1000, 2147483645, 2147483647} {
			wantIndex, want := findBinaryParamLinear(params, id)
			gotIndex := lookup.find(id)
			var got *ParamMeta
			if gotIndex >= 0 {
				got = &params[gotIndex]
			}
			if gotIndex != wantIndex || got != want {
				t.Fatalf("ids=%v id=%d index=%d want=%d", ids, id, gotIndex, wantIndex)
			}
		}
	}
}

func BenchmarkBinaryParamLookup(b *testing.B) {
	for _, count := range []int{1, 16} {
		for _, stride := range []int{1, 1001, 65537} {
			params := make([]ParamMeta, count)
			for i := range params {
				params[i].FieldID = (i + 1) * stride
			}
			for _, direct := range []bool{false, true} {
				lookup := newBinaryParamLookup(params)
				b.Run(fmt.Sprintf("n=%d/stride=%d/direct=%t", count, stride, direct), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						for i := range params {
							var index int
							var meta *ParamMeta
							if direct {
								index = lookup.find(params[i].FieldID)
								if index >= 0 {
									meta = &params[index]
								}
							} else {
								index, meta = findBinaryParamLinear(params, params[i].FieldID)
							}
							if index != i || meta != &params[i] {
								b.Fatal("lookup mismatch")
							}
						}
					}
				})
			}
		}
	}
}
