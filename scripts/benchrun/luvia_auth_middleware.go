package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	luviaAuthBenchmarkFixture = "middleware_bench_test.go"
	luviaAuthHelperFixture    = "middleware_test.go"
	luviaAuthBenchmarkSHA     = "a5a5cd5191827423e07bf382d572614207c3e7fb5b7df27452963c3136bb2d8e"
	luviaAuthHelperSHA        = "c70f9f8da6377c484739423793d637304f974bb0e1f1ebfd10fa75eb8d6eb3f3"
)

func writeLuviaAuthMiddlewareOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	retained := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		if !entry.Type().IsRegular() {
			return "", fmt.Errorf("Luvia auth middleware fixture %s must be a regular file", name)
		}
		switch name {
		case luviaAuthBenchmarkFixture:
			data, err := readRegularTestFixture(path)
			if err != nil {
				return "", err
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != luviaAuthBenchmarkSHA {
				return "", errors.New("Luvia auth middleware fixture does not match the verified benchmark")
			}
			retained++
		case luviaAuthHelperFixture:
			replacement, err := writeLuviaAuthHelper(path, workspace)
			if err != nil {
				return "", err
			}
			replacements[path] = replacement
			retained++
		default:
			replacements[path] = ""
		}
	}
	if retained != 2 {
		return "", errors.New("missing Luvia auth middleware test fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func writeLuviaAuthHelper(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	start := []byte("func testCfg()")
	end := []byte("func testIdentity(")
	first, last := bytes.Index(data, start), bytes.Index(data, end)
	if bytes.Count(data, start) != 1 || bytes.Count(data, end) != 1 || first >= last {
		return "", errors.New("missing or repeated helper declarations")
	}
	// These are the same helpers and imports used by the native isolation comparison.
	const header = "package luvia\n\nimport (\n\t\"time\"\n\n\t\"github.com/light-speak/luxo/pkg/lux/auth\"\n)\n\n"
	projected := append([]byte(header), data[first:last]...)
	if fmt.Sprintf("%x", sha256.Sum256(projected)) != luviaAuthHelperSHA {
		return "", errors.New("Luvia auth middleware helper does not match the verified projection")
	}
	output := filepath.Join(workspace, luviaAuthHelperFixture)
	return output, os.WriteFile(output, projected, 0o600)
}
