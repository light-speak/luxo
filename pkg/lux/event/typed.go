package event

import "context"

// OnDecode registers a typed broadcast handler with automatic wire-format decoding.
// ChanBus delivers the struct directly (type assertion); NATSBus delivers []byte
// which is decoded via unmarshal. This eliminates any from handler signatures.
func OnDecode[T any](bus Bus, name string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T)) error {
	return bus.On(name, func(ctx context.Context, raw any) {
		switch v := raw.(type) {
		case T:
			handler(ctx, v)
		case []byte:
			var t T
			if err := unmarshal(v, &t); err == nil {
				handler(ctx, t)
			}
		}
	})
}

// OnQueueDecode registers a typed queue handler with automatic wire-format decoding.
// Only one instance per group receives each event (competing consumers).
func OnQueueDecode[T any](bus Bus, name string, group string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T)) error {
	return bus.OnQueue(name, group, func(ctx context.Context, raw any) {
		switch v := raw.(type) {
		case T:
			handler(ctx, v)
		case []byte:
			var t T
			if err := unmarshal(v, &t); err == nil {
				handler(ctx, t)
			}
		}
	})
}
