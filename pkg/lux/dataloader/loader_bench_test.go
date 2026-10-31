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

	ctx := context.Background()
	b.ResetTimer()
	for i := range b.N {
		loader.Load(ctx, i, nil)
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

	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			loader.Load(ctx, i, nil)
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

	ctx := context.Background()
	fields := []*selection.Field{{Name: "name"}, {Name: "email"}, {Name: "avatar"}}
	b.ResetTimer()

	var wg sync.WaitGroup
	for i := range b.N {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			loader.Load(ctx, k, fields)
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
