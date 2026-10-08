package testutil

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestCompilerDemoV1Integrity(t *testing.T) {
	source := CompilerDemoV1()
	if len(source) != 34735 {
		t.Fatalf("historical benchmark input changed size: %d", len(source))
	}
	const want = "0f5e8717ae3031e8d1ae08eb473d929c9f9b4a00164f6a6d329726f30f9b8c2e"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(source))); got != want {
		t.Fatalf("historical benchmark input changed hash: %s", got)
	}
}
