package api

import "testing"

func BenchmarkWSCallAdmission(b *testing.B) {
	rt := NewRouterWithOptions(RouterOptions{})
	ws := &wsConn{}
	b.ReportAllocs()
	for b.Loop() {
		if !rt.acquireWSCall(ws) {
			b.Fatal("admission failed")
		}
		rt.releaseWSCall(ws)
	}
}
