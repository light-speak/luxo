package queue

import (
	"context"
	"errors"
	"os"
	"testing"
)

var memoryDiagnosticBenchmarkQueue *MemoryQueue

func BenchmarkMemoryDiagnosticInitialization(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		// Construction starts no worker; unreferenced queues own only Go memory.
		var err error
		memoryDiagnosticBenchmarkQueue, err = NewMemoryQueue(Config{Concurrency: 1, MaxRetries: 1})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryDiagnosticSuccess(b *testing.B) {
	q, err := NewMemoryQueue(Config{Concurrency: 1, MaxRetries: 1})
	if err != nil {
		b.Fatal(err)
	}
	done := make(chan struct{})
	if err := q.Subscribe("tasks", func(context.Context, []byte) error {
		done <- struct{}{}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := q.Close(); err != nil {
			b.Error(err)
		}
	})
	payload := []byte("accepted")
	b.ReportAllocs()
	for b.Loop() {
		if err := q.Publish(b.Context(), "tasks", payload); err != nil {
			b.Fatal(err)
		}
		<-done
	}
}

func BenchmarkMemoryDiagnosticLifecycle(b *testing.B) {
	for _, failed := range []bool{false, true} {
		name := "Success"
		if failed {
			name = "Failure"
		}
		b.Run(name, func(b *testing.B) { benchmarkMemoryDiagnosticLifecycle(b, failed) })
	}
}

func benchmarkMemoryDiagnosticLifecycle(b *testing.B, failed bool) {
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = sink
	b.Cleanup(func() { os.Stderr = previous; _ = sink.Close() })
	failure := errors.New("business failure")
	handler := func(context.Context, []byte) error {
		if failed {
			return failure
		}
		return nil
	}
	payload := []byte("accepted")
	b.ReportAllocs()
	for b.Loop() {
		q, err := NewMemoryQueue(Config{Concurrency: 1, MaxRetries: 1})
		if err != nil {
			b.Fatal(err)
		}
		if err := q.Subscribe("tasks", handler); err != nil {
			b.Fatal(err)
		}
		if err := q.Publish(b.Context(), "tasks", payload); err != nil {
			b.Fatal(err)
		}
		if err := q.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
