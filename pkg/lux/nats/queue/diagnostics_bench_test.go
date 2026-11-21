package queue

import (
	"context"
	"errors"
	"io"
	"testing"
)

func BenchmarkQueueDiagnosticLifecycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		reporter := newRenewalReports(io.Discard, 16)
		if err := reporter.Close(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

type diagnosticAckWriter struct{ written chan struct{} }

func (w diagnosticAckWriter) Write(data []byte) (int, error) {
	w.written <- struct{}{}
	return len(data), nil
}

func (w diagnosticAckWriter) WriteString(data string) (int, error) {
	w.written <- struct{}{}
	return len(data), nil
}

func BenchmarkQueueDiagnosticReport(b *testing.B) {
	writer := diagnosticAckWriter{written: make(chan struct{})}
	reporter := newRenewalReports(writer, 16)
	b.Cleanup(func() {
		if err := reporter.Close(context.Background()); err != nil {
			b.Error(err)
		}
	})
	failure := errors.New("progress lost")
	b.ReportAllocs()
	for b.Loop() {
		reporter.Submit(failure)
		<-writer.written
	}
}
