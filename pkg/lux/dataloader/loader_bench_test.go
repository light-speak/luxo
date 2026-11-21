package dataloader

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

func BenchmarkLoadSequential(b *testing.B) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		result := make(map[int]string, len(keys))
		for _, k := range keys {
			result[k] = fmt.Sprintf("v%d", k)
		}
		return result, nil
	}, Config{Wait: 1 * time.Millisecond, MaxBatch: 1000})

	ctx := testScope(b, context.Background())
	b.ResetTimer()
	for i := range b.N {
		if _, err := loader.Load(ctx, i, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadConcurrent(b *testing.B) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		result := make(map[int]string, len(keys))
		for _, k := range keys {
			result[k] = fmt.Sprintf("v%d", k)
		}
		return result, nil
	}, Config{Wait: 1 * time.Millisecond, MaxBatch: 1000})

	ctx := testScope(b, context.Background())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, err := loader.Load(ctx, i, nil); err != nil {
				b.Error(err)
				return
			}
			i++
		}
	})
}

func BenchmarkLoadWithFields(b *testing.B) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		result := make(map[int]string, len(keys))
		for _, k := range keys {
			result[k] = "ok"
		}
		return result, nil
	}, Config{Wait: 1 * time.Millisecond, MaxBatch: 1000})

	ctx := testScope(b, context.Background())
	fields := []*selection.Field{{Name: "name"}, {Name: "email"}, {Name: "avatar"}}
	b.ResetTimer()

	var wg sync.WaitGroup
	for i := range b.N {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			if _, err := loader.Load(ctx, k, fields); err != nil {
				b.Error(err)
			}
		}(i)
	}
	wg.Wait()
}

var constructionSink *Loader[int, int]

func BenchmarkLoaderConstruction(b *testing.B) {
	result := map[int]int{1: 42}
	load := func(context.Context, []int, []*selection.Field) (map[int]int, error) { return result, nil }
	for _, operation := range []string{"unused", "list", "single"} {
		b.Run(operation, func(b *testing.B) {
			keys := []int{1}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				loader := New(load, Config{MaxBatch: 1})
				constructionSink = loader
				switch operation {
				case "list":
					values, err := loader.LoadAll(ctx, keys, nil)
					if err != nil || values[1] != 42 {
						b.Fatalf("list result: %v %v", values, err)
					}
				case "single":
					value, err := loader.Load(ctx, 1, nil)
					if err != nil || value != 42 {
						b.Fatalf("single result: %v %v", value, err)
					}
				}
			}
		})
	}
}

func BenchmarkLoadCallbackBoundary(b *testing.B) {
	rows := map[int]string{1: "ok"}
	fetch := func(context.Context, []int, []*selection.Field) (map[int]string, error) { return rows, nil }
	for _, operation := range []string{"all", "immediate", "threshold"} {
		b.Run(operation, func(b *testing.B) {
			ctx, keys := b.Context(), []int{1}
			loader := New(fetch, Config{Wait: time.Millisecond, MaxBatch: 1})
			b.ReportAllocs()
			if operation == "threshold" {
				ctx = testScope(b, ctx)
				loader.maxBatch = 2
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						value, err := loader.Load(ctx, 1, nil)
						if err != nil || value != "ok" {
							b.Fatalf("threshold load: %q/%v", value, err)
						}
					}
				})
				return
			}
			for b.Loop() {
				if operation == "all" {
					values, err := loader.LoadAll(ctx, keys, nil)
					if err != nil || values[1] != "ok" {
						b.Fatalf("list load: %v/%v", values, err)
					}
				} else if value, err := loader.Load(ctx, 1, nil); err != nil || value != "ok" {
					b.Fatalf("immediate load: %q/%v", value, err)
				}
			}
		})
	}
}

var batchStateSink *batch[int, string]

func BenchmarkBatchStateAllocation(b *testing.B) {
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		batchStateSink = newBatch[int, string](ctx)
	}
}
