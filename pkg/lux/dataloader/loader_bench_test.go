package dataloader

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func BenchmarkLoadSequential(b *testing.B) {
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
		result := make(map[int]string, len(keys))
		for _, k := range keys {
			result[k] = "ok"
		}
		return result, nil
	}, Config{Wait: 1 * time.Millisecond, MaxBatch: 1000})

	ctx := context.Background()
	fields := []string{"name", "email", "avatar"}
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
