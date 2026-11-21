package api

import (
	"os"
	"testing"
)

var asyncTaskBenchmarkValue int

func BenchmarkAsyncTaskExecution(b *testing.B) {
	call := RunAsyncTask
	if os.Getenv("LUXO_ASYNC_TASK_BASELINE") == "1" {
		call = func(task func()) { task() }
	}
	task := func() { asyncTaskBenchmarkValue++ }
	b.ReportAllocs()
	for b.Loop() {
		call(task)
	}
}
