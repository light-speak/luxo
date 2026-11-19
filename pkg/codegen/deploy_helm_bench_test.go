package codegen

import (
	"fmt"
	"testing"
)

var benchmarkHelmTemplate []byte
var benchmarkHelmChart *HelmFiles

func BenchmarkHelmConditionalTemplate(b *testing.B) {
	for _, sample := range []struct {
		name     string
		generate func() []byte
	}{
		{"service_account", generateServiceAccount},
		{"ingress", generateIngress},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkHelmTemplate = sample.generate()
			}
		})
	}
}

func BenchmarkGenerateHelmFiles(b *testing.B) {
	for _, count := range []int{1, 16} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			names := make([]string, count)
			for i := range names {
				names[i] = fmt.Sprintf("module%03d", i)
			}
			result := makeHelmResult(names...)
			b.ReportAllocs()
			for b.Loop() {
				benchmarkHelmChart = GenerateHelmFiles(result, "helm-probe")
			}
		})
	}
}
