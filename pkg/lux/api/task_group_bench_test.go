package api

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"

	"golang.org/x/sync/errgroup"
)

func runTaskGroupReference(ctx context.Context, count int, task func() error) error {
	group, _ := errgroup.WithContext(ctx)
	for range count {
		group.Go(task)
	}
	return group.Wait()
}

func runTaskGroupGuarded(ctx context.Context, count int, task func() error) error {
	group, _ := NewTaskGroup(ctx)
	for range count {
		group.Go(task)
	}
	return group.Wait()
}

func BenchmarkTaskGroupLifecycle(b *testing.B) {
	run := runTaskGroupGuarded
	if os.Getenv("LUXO_TASK_GROUP_BASELINE") == "1" {
		run = runTaskGroupReference
	}
	ctx := b.Context()
	success := func() error { return nil }
	for _, count := range []int{1, 2, 16} {
		b.Run("success-"+strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := run(ctx, count, success); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	failure := errors.New("business task error")
	b.Run("business-error", func(b *testing.B) {
		task := func() error { return failure }
		b.ReportAllocs()
		for b.Loop() {
			if err := run(ctx, 1, task); err != failure {
				b.Fatal("error identity changed", err)
			}
		}
	})
}

func BenchmarkTaskGroupAbnormalExit(b *testing.B) {
	for _, sample := range []struct {
		name string
		task func() error
	}{
		{"panic", func() error { panic("private native") }},
		{"goexit", func() error { runtime.Goexit(); return nil }},
	} {
		b.Run(sample.name, func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				group, _ := NewTaskGroup(ctx)
				group.Go(sample.task)
				if group.Wait() == nil {
					b.Fatal("abnormal exit became success")
				}
			}
		})
	}
}
