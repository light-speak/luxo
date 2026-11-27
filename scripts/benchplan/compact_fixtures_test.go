package main

import (
	"io"
	"testing"
)

func TestIntelBenchmarkProfilesRequireCompleteInventories(t *testing.T) {
	const api, rpc, pg = "pkg/lux/api", "pkg/lux/rpc", "pkg/lux/pg"
	const static = "BenchmarkStaticValueParams"
	const frame = "BenchmarkLRPCConnectionFrameReuseCandidate"
	const duration = "BenchmarkDurationWriteReject"
	leaves := benchmarkCaseProduct([]string{"Bytes", "Decimal", "Duration", "RawJSON/short", "RawJSON/72KB", "UUID",
		"BoolArray/0", "BoolArray/1", "BoolArray/128", "BytesArray/0", "BytesArray/1", "BytesArray/128",
		"DateTimeArray/0", "DateTimeArray/1", "DateTimeArray/128", "DecimalArray/0", "DecimalArray/1", "DecimalArray/128",
		"DurationArray/0", "DurationArray/1", "DurationArray/128", "FloatArray/0", "FloatArray/1", "FloatArray/128",
		"RawJSONArray/0", "RawJSONArray/1", "RawJSONArray/128"}, []string{"JSON/previous", "JSON/static", "Binary/previous", "Binary/static"})
	verifyExactProfileInventory(t, api, "BenchmarkStaticParamSlot", "api-static-value-params", static, leaves,
		"DateTimeArray/256/Binary/static")
	verifyExactProfileInventory(t, rpc, "BenchmarkRPCUUIDTraceGuard", "rpc-frame-reuse", frame,
		benchmarkCaseProduct([]string{"0", "128", "4096", "65536"}, []string{"before", "reuse"}), "4096/after")
	verifyExactProfileInventory(t, pg, "BenchmarkRewrittenDuration", "pg-duration-write-reject", duration,
		[]string{"Scalar", "List128Last", "AfterDecimal"}, "List128First")
	verifyExactProfileRouting(t, rpc, "BenchmarkRPCUUIDTraceGuard", "rpc-frame-reuse", frame, "0/reuse", "^0$/^reuse$")
	verifyExactProfileRouting(t, pg, "BenchmarkRewrittenDuration", "pg-duration-write-reject", duration, "List128Last", "^List128Last$")
}

func TestBroadAPIInventoryRetainsStaticValueMeasurements(t *testing.T) {
	const values = "BenchmarkStaticValueParams/Bytes/JSON/previous-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const slot = "BenchmarkStaticParamSlot/static-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	dir := benchmarkTempDir(t)
	ordinary, broad := dir+"/ordinary.log", dir+"/broad.log"
	writeFixture(t, ordinary, values+slot+"PASS\n")
	writeFixture(t, broad, values+slot+"PASS\n")
	args := []string{"--verify-profile-inventory", "pkg/lux/api", "api-non-identity", ordinary, broad}
	if err := run(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, broad, slot+"PASS\n")
	if err := run(args, io.Discard); err == nil {
		t.Fatal("broad discovery lost its original static value inventory")
	}
}
