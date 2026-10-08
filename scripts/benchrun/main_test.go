package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalAndInvalidCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"canonical"}, strings.NewReader("pkg/lux/cache/redis\npkg/lux/cache\npkg/parser\n"), &out, io.Discard); err != nil || out.String() != "pkg/lux/cache\npkg/parser\n" {
		t.Fatalf("%q: %v", out.String(), err)
	}
	if err := run(nil, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("missing command accepted")
	}
}

func TestCompileAndRunBothPartsOfSplit(t *testing.T) {
	root, err := os.MkdirTemp("../../.tmp", "bench-run-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	files := map[string]string{
		"go.mod":                            "module github.com/light-speak/luxo\ngo 1.27.1\n",
		"go.work":                           "go 1.27.1\nuse (\n.\n./pkg/lux/cache/redis\n)\n",
		"pkg/lux/cache/cache_test.go":       "package cache\nimport \"testing\"\nfunc BenchmarkMemory(b *testing.B) { for b.Loop() {} }\n",
		"pkg/lux/cache/redis/go.mod":        "module github.com/light-speak/luxo/pkg/lux/cache/redis\ngo 1.27.1\n",
		"pkg/lux/cache/redis/redis_test.go": "package redis\nimport \"testing\"\nfunc BenchmarkRedis(b *testing.B) { for b.Loop() {} }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	args := []string{"compile", root, filepath.Join(root, "bin"), "pkg/lux/cache"}
	if err := run(args, nil, &out, &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	args[0] = "run"
	args = append(args, "-test.run=^$", "-test.bench=.", "-test.benchtime=1x")
	if err := run(args, nil, &out, &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for _, want := range []string{"BenchmarkMemory", "BenchmarkRedis", "pkg: github.com/light-speak/luxo/pkg/lux/cache/redis"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
