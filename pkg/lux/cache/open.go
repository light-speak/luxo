package cache

import "context"

// OpenMemory explicitly opens an in-process backend during application startup.
// Environment variables cannot replace it with another adapter.
func OpenMemory(ctx context.Context) (Cache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return NewMemory(), nil
}
