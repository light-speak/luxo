package cache

import (
	"context"
	"errors"
	"testing"
)

func TestOpenMemory(t *testing.T) {
	backend, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if backend, err := OpenMemory(ctx); !errors.Is(err, context.Canceled) || backend != nil {
		t.Fatal("cancellation ignored")
	}
}
