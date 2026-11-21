package api

import (
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type hostileAsyncTaskPanic struct{}

func (hostileAsyncTaskPanic) Error() string { panic("must not format the panic payload") }

func TestRunAsyncTaskExit(t *testing.T) {
	for _, sample := range []struct {
		name string
		task func()
		want string
	}{
		{"normal", func() {}, ""},
		{"panic", func() { panic("private failure") }, "async task panic: private failure"},
		{"opaque", func() { panic(hostileAsyncTaskPanic{}) }, "async task panic: handler panicked with a non-string value"},
		{"nil-panic", func() { panic(nil) }, "async task panic:"},
		{"goexit", runtime.Goexit, "async task exited without returning"},
		{"nil-task", nil, "async task panic:"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			output := captureAsyncTaskDiagnostic(t, sample.task)
			if sample.want == "" {
				if output != "" {
					t.Fatal("successful task reported a failure", output)
				}
			} else if !strings.Contains(output, sample.want) || strings.Count(output, "ERROR") != 1 {
				t.Fatal("task failure must be reported exactly once", output)
			}
		})
	}
}

func captureAsyncTaskDiagnostic(t *testing.T, task func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original; reader.Close(); writer.Close() }()
	synctest.Test(t, func(t *testing.T) {
		go RunAsyncTask(task)
		synctest.Wait()
	})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestRunAsyncTaskConcurrentCompletion(t *testing.T) {
	var count atomic.Int64
	var pending sync.WaitGroup
	for range 64 {
		pending.Go(func() { RunAsyncTask(func() { count.Add(1) }) })
	}
	pending.Wait()
	if count.Load() != 64 {
		t.Fatal("task was lost or repeated", count.Load())
	}
}
