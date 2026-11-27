package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"slices"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	rpcPoolReturnBenchmarkFile       = "pool_close_bench_test.go"
	rpcPoolReturnBenchmarkSHA        = "1e994039c2f4bd47e95ff18f646807e4ce04e1f6448317423ed6a9cf999092ba"
	rpcPoolReturnBenchmarkProjection = "53276ef3d60f8877b547366a08cc40c01d544aefabcb8324b4d08d8d21aa74d4"
	rpcPoolReturnReferenceFile       = "pool_close_reference_test.go"
	rpcPoolReturnReferenceSHA        = "cb3a8e922d524860026114d08edf05df31cde177aa1dc097104e7149ff193f1b"
	rpcPoolReturnLifecycleFile       = "pool_lifecycle_test.go"
	rpcPoolReturnLifecycleSHA        = "2102599dcc5f4fcf6528d75356a16520de00ff63dfd63682ce7a09c097be2dc4"
	rpcPoolReturnLifecycleProjection = "1493983cd599a3494d6ddcd985c0ae811126a47b22d993ec0231b17c4019f522"
	h2DeadlineFixture                = "scheduling_test.go"
	h2DeadlineSHA                    = "860773920d5e2bb0315c8478c1b6af8420e638d432904695fcc11ade21ecd791"
	h2DeadlineProjection             = "ca6cb3a6f7508e703de80f936f274a143ccf9d516b9167f1b8e5c16a2c0b47b1"

	transportBenchmarkFingerprint = "\n" + benchlayout.RPCPoolReturnRejectionProfile + "-overlay-v1 " +
		rpcPoolReturnBenchmarkFile + "=" + rpcPoolReturnBenchmarkSHA + ":" + rpcPoolReturnBenchmarkProjection + " " +
		rpcPoolReturnReferenceFile + "=" + rpcPoolReturnReferenceSHA + " " +
		rpcPoolReturnLifecycleFile + "=" + rpcPoolReturnLifecycleSHA + ":" + rpcPoolReturnLifecycleProjection +
		"\n" + benchlayout.H2TaskDeadlineReplacementProfile + "-overlay-v1 " +
		h2DeadlineFixture + "=" + h2DeadlineSHA + ":" + h2DeadlineProjection
)

type signedTransportTestFile struct {
	sourceSHA, projectionSHA, header string
	lines                            [][2]int
}

var signedTransportTestFiles = map[string]map[string]signedTransportTestFile{
	benchlayout.RPCPoolReturnRejectionProfile: {
		rpcPoolReturnBenchmarkFile: {
			rpcPoolReturnBenchmarkSHA, rpcPoolReturnBenchmarkProjection, "package rpc\n\nimport (\n\t\"testing\"\n)\n\n",
			[][2]int{{44, 77}},
		},
		rpcPoolReturnReferenceFile: {sourceSHA: rpcPoolReturnReferenceSHA},
		rpcPoolReturnLifecycleFile: {
			rpcPoolReturnLifecycleSHA, rpcPoolReturnLifecycleProjection, "package rpc\n\nimport (\n\t\"net\"\n\t\"sync/atomic\"\n)\n\n",
			[][2]int{{18, 22}, {426, 432}},
		},
	},
	benchlayout.H2TaskDeadlineReplacementProfile: {
		h2DeadlineFixture: {
			h2DeadlineSHA, h2DeadlineProjection, "package h2transport\n\nimport (\n\t\"strconv\"\n\t\"testing\"\n\t\"time\"\n)\n\n",
			[][2]int{{195, 226}},
		},
	},
}

func transportBenchmarkOverlay(root, pkg, profile string) (string, func() error, error) {
	files := signedTransportTestFiles[profile]
	if len(files) == 0 {
		return "", nil, fmt.Errorf("missing signed fixtures for %s", profile)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	return packageTestOverlay(root, pkg, "bench-"+profile+"-*", func(dir, workspace string) (string, error) {
		return writeProjectedBenchmarkOverlay(dir, workspace, profile, func(path, output string) (string, error) {
			fixture, selected := files[filepath.Base(path)]
			return projectTransportTestFile(path, output, fixture, selected)
		}, names...)
	})
}

func projectTransportTestFile(path, workspace string, fixture signedTransportTestFile, selected bool) (string, error) {
	source, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	if _, err := parseBenchmarkFixture(token.NewFileSet(), path, source); err != nil {
		return "", err
	}
	if !selected {
		return "", nil
	}
	if fixture.sourceSHA == "" {
		return "", fmt.Errorf("fixture has no verified source digest")
	}
	if fmt.Sprintf("%x", sha256.Sum256(source)) != fixture.sourceSHA {
		return "", fmt.Errorf("fixture does not match the verified source")
	}
	if len(fixture.lines) == 0 {
		if fixture.header != "" || fixture.projectionSHA != "" {
			return "", fmt.Errorf("fixture has no verified source spans")
		}
		return path, nil
	}
	if fixture.projectionSHA == "" {
		return "", fmt.Errorf("fixture has no verified projection digest")
	}
	// Preserve the exact signed source spans and line breaks from the native witness.
	lines := slices.Collect(bytes.Lines(source))
	projected := bytes.NewBufferString(fixture.header)
	last := 0
	for _, span := range fixture.lines {
		first, end := span[0], span[1]
		if first <= last || end < first || end > len(lines) {
			return "", fmt.Errorf("invalid source span %d–%d after line %d", first, end, last)
		}
		if last != 0 {
			projected.WriteByte('\n')
		}
		for _, line := range lines[first-1 : end] {
			projected.Write(line)
		}
		last = end
	}
	if fmt.Sprintf("%x", sha256.Sum256(projected.Bytes())) != fixture.projectionSHA {
		return "", fmt.Errorf("fixture does not match the verified projection")
	}
	output := filepath.Join(workspace, filepath.Base(path))
	return output, os.WriteFile(output, projected.Bytes(), 0o600)
}
