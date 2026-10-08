package event

import (
	"context"
	"errors"
)

// ErrUnexpectedPayload rejects a value that is neither the registered event
// type nor wire bytes. It never includes potentially sensitive payload data.
var ErrUnexpectedPayload = errors.New("event: payload does not match registered event type")

// OnDecode registers a typed broadcast handler with automatic wire-format decoding.
// ChanBus delivers an independently owned struct; NATSBus delivers []byte
// which is decoded via unmarshal. This eliminates any from handler signatures.
func OnDecode[T any](bus Bus, name string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T) error) error {
	return bus.On(name, func(ctx context.Context, raw any) error {
		switch v := raw.(type) {
		case T:
			return handler(ctx, v)
		case []byte:
			var t T
			if err := unmarshal(v, &t); err != nil {
				return err
			}
			return handler(ctx, t)
		}
		return ErrUnexpectedPayload
	})
}

// OnQueueDecode registers a typed queue handler with automatic wire-format decoding.
// Only one instance per group receives each event (competing consumers).
func OnQueueDecode[T any](bus Bus, name string, group string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T) error) error {
	return bus.OnQueue(name, group, func(ctx context.Context, raw any) error {
		switch v := raw.(type) {
		case T:
			return handler(ctx, v)
		case []byte:
			var t T
			if err := unmarshal(v, &t); err != nil {
				return err
			}
			return handler(ctx, t)
		}
		return ErrUnexpectedPayload
	})
}
