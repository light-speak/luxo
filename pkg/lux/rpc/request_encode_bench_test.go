package rpc

import (
	"fmt"
	"testing"
)

var requestEncodeSink []byte

func BenchmarkLRPCDirectRequest(b *testing.B) {
	for _, size := range []int{0, 128, 4096, 65536} {
		params := make([]byte, size)
		b.Run(fmt.Sprintf("%d/plain-before", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				requestEncodeSink = encodeRequestEnvelope(requestKindCall, "token", encodeCallRequest(1, nil, params))
			}
		})
		b.Run(fmt.Sprintf("%d/plain-after", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				requestEncodeSink = prepareCallFrame(requestKindCall, "token", 1, nil, params)
			}
		})
		b.Run(fmt.Sprintf("%d/traced-before", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				requestEncodeSink = encodeTracedRequestEnvelope(requestKindCall, "token", "trace", "a.b", true, encodeCallRequest(1, nil, params))
			}
		})
		b.Run(fmt.Sprintf("%d/traced-after", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				requestEncodeSink = prepareTracedCallFrame("token", "trace", "a.b", true, 1, nil, params)
			}
		})
	}
}
